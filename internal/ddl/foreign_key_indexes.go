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

	// Every included table's valid foreign keys, and the column-identifier
	// map needed to render each one's statement, gathered up front so index
	// names can be disambiguated once across ALL of them below — a Postgres
	// index is unique per schema, not per table the way a constraint is
	// (issue #43), so per-table disambiguation isn't enough to prevent two
	// different tables from independently generating the same name.
	type tableFKs struct {
		table string
		fks   []config.ForeignKey
		ids   map[string]string
	}
	var perTable []tableFKs
	for _, table := range tableNames {
		tc := cfg.Tables[table]
		if !tc.Include {
			continue
		}
		included := includedSet(tc)

		valid := make([]config.ForeignKey, 0, len(tc.ForeignKeys))
		for _, fk := range tc.ForeignKeys {
			if invalidForeignKeyReason(cfg, table, included, fk) != "" {
				continue
			}
			valid = append(valid, fk)
		}
		if len(valid) == 0 {
			continue
		}
		perTable = append(perTable, tableFKs{table: table, fks: valid, ids: PostgresColumnNames(tc)})
	}

	var display, identity []string
	for _, pt := range perTable {
		for _, fk := range pt.fks {
			d := fmt.Sprintf("idx_%s_%s", pt.table, strings.Join(fk.Columns, "_"))
			display = append(display, d)
			identity = append(identity, pt.table+"\x00"+d+"\x00"+fk.RefTable+"\x00"+strings.Join(fk.RefColumns, "\x00"))
		}
	}
	names := disambiguateNames(display, identity)

	// The identifier CREATE TABLE actually emitted for each table (see
	// PostgresTableNames/issue #44) — the index's ON clause must name the
	// same disambiguated relation CREATE TABLE created, not the raw
	// source table name.
	pgTableNames := PostgresTableNames(cfg)

	i := 0
	for _, pt := range perTable {
		for _, fk := range pt.fks {
			statements = append(statements, foreignKeyIndexStatement(pgTableNames[pt.table], fk, names[i], pt.ids))
			i++
		}
	}
	return statements
}

// foreignKeyIndexStatement renders one CREATE INDEX statement, named name
// (see foreignKeyIndexNames), covering fk's local columns, in the order
// they're declared in fk.Columns. ids maps those declared names to the
// identifiers CREATE TABLE actually emitted for them (see
// PostgresColumnNames) — kept consistent with foreignKeyStatement for the
// same issue #21 reason. table must likewise already be the resolved
// identifier CREATE TABLE emitted for it (see PostgresTableNames/issue
// #44), not necessarily the raw source table name.
func foreignKeyIndexStatement(table string, fk config.ForeignKey, name string, ids map[string]string) string {
	return fmt.Sprintf("CREATE INDEX %s ON %s (%s);", quoteIdent(name), quoteIdent(table), quoteJoin(mapNames(fk.Columns, ids)))
}
