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
		valid := make([]config.ForeignKey, 0, len(tc.ForeignKeys))
		for _, fk := range tc.ForeignKeys {
			if invalidForeignKeyReason(cfg, table, included, fk) != "" {
				continue
			}
			valid = append(valid, fk)
		}
		names := foreignKeyIndexNames(table, valid)
		for i, fk := range valid {
			statements = append(statements, foreignKeyIndexStatement(table, fk, names[i], ids))
		}
	}
	return statements
}

// foreignKeyIndexNames returns the "idx_<table>_<columns>" index name to
// use for each foreign key in fks (index-parallel with fks), disambiguated
// against Postgres's 63-byte NAMEDATALEN limit the same way
// foreignKeyConstraintNames disambiguates constraint names (issue #36).
func foreignKeyIndexNames(table string, fks []config.ForeignKey) []string {
	display := make([]string, len(fks))
	identity := make([]string, len(fks))
	for i, fk := range fks {
		display[i] = fmt.Sprintf("idx_%s_%s", table, strings.Join(fk.Columns, "_"))
		identity[i] = display[i] + "\x00" + fk.RefTable + "\x00" + strings.Join(fk.RefColumns, "\x00")
	}
	return disambiguateNames(display, identity)
}

// foreignKeyIndexStatement renders one CREATE INDEX statement, named name
// (see foreignKeyIndexNames), covering fk's local columns, in the order
// they're declared in fk.Columns. ids maps those declared names to the
// identifiers CREATE TABLE actually emitted for them (see
// PostgresColumnNames) — kept consistent with foreignKeyStatement for the
// same issue #21 reason.
func foreignKeyIndexStatement(table string, fk config.ForeignKey, name string, ids map[string]string) string {
	return fmt.Sprintf("CREATE INDEX %s ON %s (%s);", quoteIdent(name), quoteIdent(table), quoteJoin(mapNames(fk.Columns, ids)))
}
