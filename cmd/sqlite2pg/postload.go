package main

import (
	"context"
	"database/sql"
	"fmt"
	"math"

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

// postLoadTable advances a rowid-alias identity sequence past the loaded
// rows and past highWater, and refreshes planner stats. Idempotent, so it's
// also safe on a --resume that finds the table already loaded but not yet
// marked.
func postLoadTable(ctx context.Context, conn *pgx.Conn, pgTable string, tc config.TableConfig, highWater int64) error {
	qualified := pgx.Identifier{pgTable}.Sanitize()
	if name, ok := ddl.RowIDAliasColumn(tc); ok {
		col := ddl.PostgresColumnNames(tc)[name]
		// pg_get_serial_sequence returns NULL when the live column has no
		// identity; setval is STRICT and would silently skip, so check here.
		var seq *string
		if err := conn.QueryRow(ctx, "SELECT pg_get_serial_sequence($1, $2)", qualified, col).Scan(&seq); err != nil {
			return fmt.Errorf("looking up identity sequence for %s.%s: %w", pgTable, col, err)
		}
		if seq == nil {
			return fmt.Errorf("%s.%s: expected an identity sequence for rowid-alias column, found none", pgTable, col)
		}
		// Refuse rather than clamp: the caller decides what a mark beyond the
		// column type should become. setval would otherwise fail with a bare
		// out-of-bounds error.
		target := tc.Columns[name].TargetType
		if colMax, ok := identityMax[target]; ok && highWater > colMax {
			return fmt.Errorf("%s.%s: sqlite_sequence high-water mark %d exceeds the %s column maximum %d",
				pgTable, col, highWater, target, colMax)
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
	}
	if _, err := conn.Exec(ctx, "ANALYZE "+qualified); err != nil {
		return fmt.Errorf("analyzing %s: %w", pgTable, err)
	}
	return nil
}
