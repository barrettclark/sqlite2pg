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
	// note says what reseedIdentity did to the sequence, for the warning.
	note string
}

func (e *identityRangeError) Error() string {
	return fmt.Sprintf("%s.%s: sqlite_sequence high-water mark %d exceeds the %s column maximum %d",
		e.pgTable, e.column, e.highWater, e.typ, e.max)
}

func checkIdentityRange(pgTable, col, typ string, highWater int64) error {
	if e := identityOverflow(pgTable, col, typ, highWater); e != nil {
		return e
	}
	return nil
}

func identityOverflow(pgTable, col, typ string, highWater int64) *identityRangeError {
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
// rows and past highWater. It never moves the sequence backward: the app may
// already have issued ids past the loaded maximum. When highWater doesn't fit
// typ, the sequence is exhausted at the type's maximum, so the next insert
// fails instead of reissuing an id SQLite already handed out. The
// identityRangeError is returned with what was done to the sequence.
func reseedIdentity(ctx context.Context, conn *pgx.Conn, pgTable, col, typ string, highWater int64) error {
	rangeErr := identityOverflow(pgTable, col, typ, highWater)
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
	// MAX is NULL on an empty table; GREATEST would ignore it, so treat it as 0.
	var loaded *int64
	if err := conn.QueryRow(ctx, fmt.Sprintf("SELECT MAX(%s) FROM %s", pgx.Identifier{col}.Sanitize(), qualified)).Scan(&loaded); err != nil {
		return fmt.Errorf("reading loaded maximum of %s.%s: %w", pgTable, col, err)
	}
	target := highWater
	if loaded != nil && *loaded > target {
		target = *loaded
	}
	if rangeErr != nil {
		target = rangeErr.max
	}
	note := "sequence left unchanged"
	// Below 1 nothing was loaded or recorded, and the sequence's first
	// nextval already returns 1.
	if target >= 1 {
		var pos *int64
		if err := conn.QueryRow(ctx, "SELECT pg_sequence_last_value($1::regclass)", *seq).Scan(&pos); err != nil {
			return fmt.Errorf("reading identity sequence position for %s.%s: %w", pgTable, col, err)
		}
		// pos is NULL until the sequence has been called.
		if pos == nil || *pos < target {
			if _, err := conn.Exec(ctx, "SELECT setval($1::regclass, $2::bigint, true)", *seq, target); err != nil {
				return fmt.Errorf("resetting identity sequence for %s: %w", pgTable, err)
			}
			note = fmt.Sprintf("sequence set to %d", target)
		}
	}
	if rangeErr != nil {
		rangeErr.note = note
		return rangeErr
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
// the config declares a rowid-alias for but the catalog has no identity on was
// loaded by a build that didn't create one, so the identity is added first.
func reseedCompletedTable(ctx context.Context, conn *pgx.Conn, sourceDB *sql.DB, tableName, pgTable string, tc config.TableConfig) error {
	col, typ, ok, err := liveIdentityColumn(ctx, conn, pgTable)
	if err != nil {
		return err
	}
	if !ok {
		name, alias := ddl.RowIDAliasColumn(tc)
		if !alias {
			return nil
		}
		col = ddl.PostgresColumnNames(tc)[name]
		// The PK is NOT NULL, so existing rows can't block the identity add.
		stmt := fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s ADD GENERATED BY DEFAULT AS IDENTITY",
			pgx.Identifier{pgTable}.Sanitize(), pgx.Identifier{col}.Sanitize())
		if _, err := conn.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("adding identity to %s.%s: %w", pgTable, col, err)
		}
		fmt.Printf("%s: added identity to %s (table was loaded without one)\n", pgTable, col)
		if col, typ, ok, err = liveIdentityColumn(ctx, conn, pgTable); err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%s.%s: no identity after ADD GENERATED", pgTable, col)
		}
	}
	hw, err := sqlitereader.ReadSequenceHighWater(sourceDB, tableName)
	if err != nil {
		return err
	}
	return reseedIdentity(ctx, conn, pgTable, col, typ, hw)
}

// analyzeIfNeverAnalyzed runs ANALYZE only for a table with no manual ANALYZE
// on record, so a resume doesn't re-analyze tables that already have stats.
func analyzeIfNeverAnalyzed(ctx context.Context, conn *pgx.Conn, pgTable string) error {
	var never bool
	if err := conn.QueryRow(ctx,
		`SELECT COALESCE((SELECT last_analyze IS NULL FROM pg_stat_user_tables WHERE relid = $1::regclass), true)`,
		pgx.Identifier{pgTable}.Sanitize()).Scan(&never); err != nil {
		return fmt.Errorf("checking statistics for %s: %w", pgTable, err)
	}
	if !never {
		return nil
	}
	return analyzeTable(ctx, conn, pgTable)
}

// tolerateOverflow warns and returns nil for an identityRangeError, since
// the table's data is already in place and only its identity is stuck.
// Any other error is returned unchanged, so the caller stops before the
// table is marked completed.
func tolerateOverflow(err error) error {
	var rangeErr *identityRangeError
	if err != nil && errors.As(err, &rangeErr) {
		fmt.Fprintf(os.Stderr, "warning: %v; %s; inserts without an id fail until the column is widened: retype the column to bigint and re-run\n", err, rangeErr.note)
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
