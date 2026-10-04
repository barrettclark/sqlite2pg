package main

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"sqlite2pg/internal/config"
	"sqlite2pg/internal/ddl"
)

// postLoadTable advances a rowid-alias identity sequence past the loaded
// rows and refreshes planner stats. Idempotent, so it's also safe on a
// --resume that finds the table already loaded but not yet marked.
func postLoadTable(ctx context.Context, conn *pgx.Conn, pgTable string, tc config.TableConfig) error {
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
		// Advance via is_called rather than MAX+1, which overflows at the
		// column type's maximum (2147483647 for integer). MAX is NULL on an
		// empty table: GREATEST ignores it, so the value is 1 with is_called
		// false, and the first nextval returns 1.
		q := fmt.Sprintf(
			"SELECT setval($1, GREATEST(MAX(%[1]s), 1), COALESCE(MAX(%[1]s) >= 1, false)) FROM %[2]s",
			pgx.Identifier{col}.Sanitize(), qualified)
		if _, err := conn.Exec(ctx, q, *seq); err != nil {
			return fmt.Errorf("resetting identity sequence for %s: %w", pgTable, err)
		}
	}
	if _, err := conn.Exec(ctx, "ANALYZE "+qualified); err != nil {
		return fmt.Errorf("analyzing %s: %w", pgTable, err)
	}
	return nil
}
