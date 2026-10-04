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
		// GREATEST(..., 1): a table with only non-positive rowids would
		// otherwise ask setval for a value below the sequence minimum.
		q := fmt.Sprintf(
			"SELECT setval(pg_get_serial_sequence($1, $2), GREATEST(COALESCE(MAX(%s), 0) + 1, 1), false) FROM %s",
			pgx.Identifier{col}.Sanitize(), qualified)
		if _, err := conn.Exec(ctx, q, qualified, col); err != nil {
			return fmt.Errorf("resetting identity sequence for %s: %w", pgTable, err)
		}
	}
	if _, err := conn.Exec(ctx, "ANALYZE "+qualified); err != nil {
		return fmt.Errorf("analyzing %s: %w", pgTable, err)
	}
	return nil
}
