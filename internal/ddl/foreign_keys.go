package ddl

import (
	"fmt"
	"sort"
	"strings"

	"sqlite2pg/internal/config"
)

// GenerateForeignKeyConstraints returns one ALTER TABLE ... ADD CONSTRAINT
// statement per valid declared foreign key across every included table in
// cfg, plus a human-readable reason for each foreign key that couldn't be
// emitted (it references a dropped column, or an excluded/missing table).
// These are meant to run only after every table has been created and
// loaded — deliberately not interleaved with CREATE TABLE/COPY — so table
// creation and data loading never need to be ordered by FK dependency:
// referential integrity is validated once, when the constraint is added,
// against data that already fully exists.
func GenerateForeignKeyConstraints(cfg *config.MigrationConfig) (statements []string, skipped []string) {
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

		for _, fk := range tc.ForeignKeys {
			if reason := invalidForeignKeyReason(cfg, table, included, fk); reason != "" {
				skipped = append(skipped, reason)
				continue
			}
			refTC := cfg.Tables[fk.RefTable]
			statements = append(statements, foreignKeyStatement(table, fk, PostgresColumnNames(tc), PostgresColumnNames(refTC)))
		}
	}
	return statements, skipped
}

// invalidForeignKeyReason reports why fk (declared on table) can't be
// emitted, or "" if it's valid. Every local column must survive into the
// target schema, and the referenced table must be included with every
// referenced column also surviving.
func invalidForeignKeyReason(cfg *config.MigrationConfig, table string, localIncluded map[string]bool, fk config.ForeignKey) string {
	for _, col := range fk.Columns {
		if !localIncluded[col] {
			return fmt.Sprintf("%s: foreign key column %q was dropped or excluded", table, col)
		}
	}
	refTC, ok := cfg.Tables[fk.RefTable]
	if !ok || !refTC.Include {
		return fmt.Sprintf("%s: references table %q, which is excluded or missing", table, fk.RefTable)
	}
	refIncluded := includedSet(refTC)
	for _, col := range fk.RefColumns {
		if !refIncluded[col] {
			return fmt.Sprintf("%s: references %s.%s, which was dropped or excluded", table, fk.RefTable, col)
		}
	}
	return ""
}

// mapNames translates each of names through ids, leaving a name unchanged
// if ids has no entry for it (defensive only — every included column
// always has one; see PostgresColumnNames).
func mapNames(names []string, ids map[string]string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		if id, ok := ids[n]; ok {
			out[i] = id
		} else {
			out[i] = n
		}
	}
	return out
}

func includedSet(tc config.TableConfig) map[string]bool {
	set := make(map[string]bool)
	for _, name := range IncludedColumns(tc) {
		set[name] = true
	}
	return set
}

// foreignKeyStatement renders one ALTER TABLE ... ADD CONSTRAINT ...
// FOREIGN KEY statement. ON DELETE/ON UPDATE clauses are only included
// when set to something other than NO ACTION, Postgres's own default —
// omitting it produces the same behavior with cleaner generated SQL.
// localIDs and refIDs map fk's declared column names to the identifiers
// actually emitted for them in CREATE TABLE (see PostgresColumnNames) —
// necessary so a foreign key on a column that CREATE TABLE had to
// disambiguate (issue #21) still references the column that really exists.
func foreignKeyStatement(table string, fk config.ForeignKey, localIDs, refIDs map[string]string) string {
	name := fmt.Sprintf("fk_%s_%s", table, strings.Join(fk.Columns, "_"))

	var b strings.Builder
	fmt.Fprintf(&b, "ALTER TABLE %q ADD CONSTRAINT %q FOREIGN KEY (%s) REFERENCES %q (%s)",
		table, name, quoteJoin(mapNames(fk.Columns, localIDs)), fk.RefTable, quoteJoin(mapNames(fk.RefColumns, refIDs)))
	if fk.OnDelete != "" && fk.OnDelete != "NO ACTION" {
		fmt.Fprintf(&b, " ON DELETE %s", fk.OnDelete)
	}
	if fk.OnUpdate != "" && fk.OnUpdate != "NO ACTION" {
		fmt.Fprintf(&b, " ON UPDATE %s", fk.OnUpdate)
	}
	b.WriteString(";")
	return b.String()
}
