package ddl

import (
	"fmt"
	"sort"
	"strings"

	"sqlite2pg/internal/config"
)

// GenerateForeignKeyIndexes returns one CREATE INDEX statement per valid
// declared foreign key across every included table in cfg — the same
// validity rules GenerateForeignKeyConstraints uses, so an index is only
// suggested for a foreign key that actually becomes a real constraint.
// Postgres doesn't auto-index foreign keys the way some other databases
// do, and an index on every FK column is well-established best practice
// with no real downside, so unlike varchar length suggestions or inferred
// foreign keys, these are generated unconditionally rather than flagged
// for review.
func GenerateForeignKeyIndexes(cfg *config.MigrationConfig) (statements []string) {
	tableNames := make([]string, 0, len(cfg.Tables))
	for name := range cfg.Tables {
		tableNames = append(tableNames, name)
	}
	sort.Strings(tableNames)

	for _, table := range tableNames {
		tc := cfg.Tables[table]
		if !tc.Include {
			continue
		}
		included := includedSet(tc)

		ids := PostgresColumnNames(tc)
		for _, fk := range tc.ForeignKeys {
			if invalidForeignKeyReason(cfg, table, included, fk) != "" {
				continue
			}
			statements = append(statements, foreignKeyIndexStatement(table, fk, ids))
		}
	}
	return statements
}

// foreignKeyIndexStatement renders one CREATE INDEX statement covering
// fk's local columns, in the order they're declared in fk.Columns. ids
// maps those declared names to the identifiers CREATE TABLE actually
// emitted for them (see PostgresColumnNames) — kept consistent with
// foreignKeyStatement for the same issue #21 reason.
func foreignKeyIndexStatement(table string, fk config.ForeignKey, ids map[string]string) string {
	name := fmt.Sprintf("idx_%s_%s", table, strings.Join(fk.Columns, "_"))
	return fmt.Sprintf("CREATE INDEX %s ON %s (%s);", quoteIdent(name), quoteIdent(table), quoteJoin(mapNames(fk.Columns, ids)))
}
