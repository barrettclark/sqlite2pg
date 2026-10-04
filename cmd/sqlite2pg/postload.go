package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"

	"sqlite2pg/internal/config"
	"sqlite2pg/internal/ddl"
	"sqlite2pg/internal/sqlitereader"
)

// identityMax is the largest value each identity column type's sequence accepts.
var identityMax = map[string]int64{
	"smallint": math.MaxInt16,
	"integer":  math.MaxInt32,
	"bigint":   math.MaxInt64,
}

// identityRangeError reports a high-water mark the identity column's type
// can't hold. It is refused rather than clamped.
type identityRangeError struct {
	pgTable, column, typ string
	highWater, max       int64
}

func (e *identityRangeError) Error() string {
	return fmt.Sprintf("%s.%s: sqlite_sequence high-water mark %d exceeds the %s column maximum %d",
		e.pgTable, e.column, e.highWater, e.typ, e.max)
}

func checkIdentityRange(pgTable, col, typ string, highWater int64) error {
	colMax, ok := identityMax[strings.ToLower(typ)]
	if !ok || highWater <= colMax {
		return nil
	}
	return &identityRangeError{pgTable: pgTable, column: col, typ: typ, highWater: highWater, max: colMax}
}

// sourceHighWater returns the sqlite_sequence high-water mark to seed a
// rowid-alias identity with, or 0 for a table without one. It isn't gated on
// tc.Autoincrement: configs from before that flag existed leave it unset, and
// sqlite_sequence has no row for a non-AUTOINCREMENT table, so it reads 0.
func sourceHighWater(db *sql.DB, table string, tc config.TableConfig) (int64, error) {
	if _, ok := ddl.RowIDAliasColumn(tc); !ok {
		return 0, nil
	}
	return sqlitereader.ReadSequenceHighWater(db, table)
}

// checkTableIdentityRange checks a config table's identity against highWater
// before any of its data is loaded.
func checkTableIdentityRange(pgTable string, tc config.TableConfig, highWater int64) error {
	name, ok := ddl.RowIDAliasColumn(tc)
	if !ok {
		return nil
	}
	return checkIdentityRange(pgTable, ddl.PostgresColumnNames(tc)[name], tc.Columns[name].TargetType, highWater)
}

// reseedIdentity advances the identity sequence of pgTable.col past the loaded
// rows and past highWater. Idempotent.
func reseedIdentity(ctx context.Context, conn *pgx.Conn, pgTable, col, typ string, highWater int64) error {
	if err := checkIdentityRange(pgTable, col, typ, highWater); err != nil {
		return err
	}
	qualified := pgx.Identifier{pgTable}.Sanitize()
	// pg_get_serial_sequence returns NULL when the live column has no
	// identity; setval is STRICT and would silently skip, so check here.
	var seq *string
	if err := conn.QueryRow(ctx, "SELECT pg_get_serial_sequence($1, $2)", qualified, col).Scan(&seq); err != nil {
		return fmt.Errorf("looking up identity sequence for %s.%s: %w", pgTable, col, err)
	}
	if seq == nil {
		return fmt.Errorf("%s.%s: expected an identity sequence for rowid-alias column, found none", pgTable, col)
	}
	// Advance via is_called rather than MAX+1, which overflows at the
	// column type's maximum (2147483647 for integer). MAX is NULL on an
	// empty table and GREATEST ignores NULLs; highWater is 0 for a table
	// with no sqlite_sequence row. With nothing loaded or recorded the
	// value is 1 with is_called false, so the first nextval returns 1.
	// $2::bigint types the parameter as bigint; the range check above
	// keeps setval from rejecting it for the column's sequence type.
	q := fmt.Sprintf(
		"SELECT setval($1, GREATEST(MAX(%[1]s), $2::bigint, 1), GREATEST(MAX(%[1]s), $2::bigint) >= 1) FROM %[2]s",
		pgx.Identifier{col}.Sanitize(), qualified)
	if _, err := conn.Exec(ctx, q, *seq, highWater); err != nil {
		return fmt.Errorf("resetting identity sequence for %s: %w", pgTable, err)
	}
	return nil
}

// liveIdentityColumn returns pgTable's identity column and its type as the
// catalog reports them. ok is false when the table has no identity column.
func liveIdentityColumn(ctx context.Context, conn *pgx.Conn, pgTable string) (col, typ string, ok bool, err error) {
	err = conn.QueryRow(ctx, `SELECT a.attname, format_type(a.atttypid, NULL)
		FROM pg_attribute a
		WHERE a.attrelid = $1::regclass AND a.attidentity <> '' AND NOT a.attisdropped`,
		pgx.Identifier{pgTable}.Sanitize()).Scan(&col, &typ)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, fmt.Errorf("looking up identity column of %s: %w", pgTable, err)
	}
	return col, typ, true, nil
}

// reseedCompletedTable reseeds a table whose data is already in place, using
// the identity the catalog has rather than the config's view of it. A table
// the config expects to have an identity but the catalog doesn't is drift
// (nothing in this tool produces it), so it's reported rather than skipped.
func reseedCompletedTable(ctx context.Context, conn *pgx.Conn, sourceDB *sql.DB, tableName, pgTable string, tc config.TableConfig) error {
	col, typ, ok, err := liveIdentityColumn(ctx, conn, pgTable)
	if err != nil {
		return err
	}
	if !ok {
		if name, alias := ddl.RowIDAliasColumn(tc); alias {
			fmt.Fprintf(os.Stderr, "warning: %s.%s: config declares a rowid-alias identity but the live table has none; sequence not reseeded\n",
				pgTable, ddl.PostgresColumnNames(tc)[name])
		}
		return nil
	}
	hw, err := sqlitereader.ReadSequenceHighWater(sourceDB, tableName)
	if err != nil {
		return err
	}
	return reseedIdentity(ctx, conn, pgTable, col, typ, hw)
}

// tolerateOverflow warns and returns nil for an identityRangeError, since
// the table's data is already in place and only its identity is stuck.
// Any other error is returned unchanged, so the caller stops before the
// table is marked completed.
func tolerateOverflow(err error) error {
	var rangeErr *identityRangeError
	if err != nil && errors.As(err, &rangeErr) {
		fmt.Fprintf(os.Stderr, "warning: %v; retype the column to bigint and re-run\n", err)
		return nil
	}
	return err
}

func analyzeTable(ctx context.Context, conn *pgx.Conn, pgTable string) error {
	qualified := pgx.Identifier{pgTable}.Sanitize()
	if _, err := conn.Exec(ctx, "ANALYZE "+qualified); err != nil {
		return fmt.Errorf("analyzing %s: %w", pgTable, err)
	}
	return nil
}

// postLoadTable reseeds a rowid-alias identity past the loaded rows and
// highWater, then refreshes planner stats. Idempotent.
func postLoadTable(ctx context.Context, conn *pgx.Conn, pgTable string, tc config.TableConfig, highWater int64) error {
	if name, ok := ddl.RowIDAliasColumn(tc); ok {
		col := ddl.PostgresColumnNames(tc)[name]
		if err := reseedIdentity(ctx, conn, pgTable, col, tc.Columns[name].TargetType, highWater); err != nil {
			return err
		}
	}
	return analyzeTable(ctx, conn, pgTable)
}
