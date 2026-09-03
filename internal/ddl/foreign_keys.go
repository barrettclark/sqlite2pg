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

	// The identifier CREATE TABLE actually emitted for each table (see
	// PostgresTableNames/issue #44) — REFERENCES and the constrained
	// table itself must both name the same disambiguated relation CREATE
	// TABLE created, not the raw source table name.
	pgTableNames := PostgresTableNames(cfg)

	for _, table := range tableNames {
		tc := cfg.Tables[table]
		if !tc.Include {
			continue
		}
		included := includedSet(tc)

		valid := make([]config.ForeignKey, 0, len(tc.ForeignKeys))
		for _, fk := range tc.ForeignKeys {
			if reason := invalidForeignKeyReason(cfg, table, included, fk); reason != "" {
				skipped = append(skipped, reason)
				continue
			}
			valid = append(valid, fk)
		}
		names := foreignKeyConstraintNames(table, valid)
		for i, fk := range valid {
			refTC := cfg.Tables[fk.RefTable]
			statements = append(statements, foreignKeyStatement(pgTableNames[table], pgTableNames[fk.RefTable], fk, names[i], PostgresColumnNames(tc), PostgresColumnNames(refTC)))
		}
	}
	return statements, skipped
}

// foreignKeyConstraintNames returns the "fk_<table>_<columns>" constraint
// name to use for each foreign key in fks (index-parallel with fks),
// disambiguated against Postgres's 63-byte NAMEDATALEN limit: a long table
// name plus a composite key's joined column names can exceed it, and
// without disambiguation two such names could truncate to the same
// identifier — or two foreign keys sharing the same local columns but
// referencing different tables could already collide even before
// truncation — either way causing the second ALTER TABLE ... ADD
// CONSTRAINT to fail with "constraint ... already exists" (issue #36).
// Reuses disambiguateNames, the same truncate-then-hash approach issue #21
// established for column names, keyed here by each fk's full identity
// (columns + referenced table + referenced columns) rather than just its
// display name, since two different fks can share a display name.
func foreignKeyConstraintNames(table string, fks []config.ForeignKey) []string {
	display := make([]string, len(fks))
	identity := make([]string, len(fks))
	for i, fk := range fks {
		display[i] = fmt.Sprintf("fk_%s_%s", table, strings.Join(fk.Columns, "_"))
		identity[i] = display[i] + "\x00" + fk.RefTable + "\x00" + strings.Join(fk.RefColumns, "\x00")
	}
	return disambiguateNames(display, identity)
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

// foreignKeyStatement renders a DROP CONSTRAINT IF EXISTS followed by the
// ADD CONSTRAINT for one foreign key. The DROP makes the whole FK step
// re-runnable: `load --resume` after a crash between the FK commit and the
// state-file write would otherwise re-issue ADD and fail on "already
// exists" (issues #109, #128). ON DELETE/ON UPDATE are omitted when NO
// ACTION (Postgres's default). localIDs/refIDs and table/refTable are the
// disambiguated identifiers CREATE TABLE actually emitted (issues #21, #44).
func foreignKeyStatement(table, refTable string, fk config.ForeignKey, name string, localIDs, refIDs map[string]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "ALTER TABLE %s DROP CONSTRAINT IF EXISTS %s;\n", quoteIdent(table), quoteIdent(name))
	fmt.Fprintf(&b, "ALTER TABLE %s ADD CONSTRAINT %s FOREIGN KEY (%s) REFERENCES %s (%s)",
		quoteIdent(table), quoteIdent(name), quoteJoin(mapNames(fk.Columns, localIDs)), quoteIdent(refTable), quoteJoin(mapNames(fk.RefColumns, refIDs)))
	if fk.OnDelete != "" && fk.OnDelete != "NO ACTION" {
		fmt.Fprintf(&b, " ON DELETE %s", fk.OnDelete)
	}
	if fk.OnUpdate != "" && fk.OnUpdate != "NO ACTION" {
		fmt.Fprintf(&b, " ON UPDATE %s", fk.OnUpdate)
	}
	b.WriteString(";")
	return b.String()
}
