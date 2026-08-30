package pipeline

import (
	"database/sql"
	"fmt"
	"regexp"
	"strings"

	"sqlite2pg/internal/config"
	"sqlite2pg/internal/sqlitereader"
)

// fkColumnNamePattern strips a trailing (optionally underscore-separated)
// "id" from a column name — "CustomerID", "customer_id", and "ArtistId"
// all yield "Customer"/"customer"/"Artist" — the base name inferForeignKeys
// compares against every table name. A column named just "id" (no base
// left after stripping) never matches: that's this table's own identity
// column, not a name pointing at another table.
var fkColumnNamePattern = regexp.MustCompile(`(?i)^(.+?)_?id$`)

// inferForeignKeys proposes undeclared foreign keys (issue #6) by naming
// convention plus a full-column value-containment check: a column named
// after another table (singular or simply pluralized) whose every
// non-null value actually exists in that table's single-column primary
// key. This is heuristic evidence, not certainty — a coincidental name
// match backed by coincidentally-overlapping values is possible — so the
// caller must surface every result for human review, never apply it
// automatically.
func inferForeignKeys(db *sql.DB, tables []sqlitereader.TableInfo) (map[string][]config.SuggestedForeignKey, error) {
	tableByLower := map[string]string{}
	pkColumn := map[string]string{}
	declaredFKColumn := map[string]bool{} // "table.column" already covered by a real FK
	for _, t := range tables {
		tableByLower[strings.ToLower(t.Name)] = t.Name
		if pk := singleColumnPrimaryKey(t.Columns); pk != "" {
			pkColumn[strings.ToLower(t.Name)] = pk
		}
		for _, fk := range t.ForeignKeys {
			for _, col := range fk.Columns {
				declaredFKColumn[t.Name+"."+col] = true
			}
		}
	}

	suggestions := map[string][]config.SuggestedForeignKey{}
	for _, t := range tables {
		for _, col := range t.Columns {
			if col.PrimaryKeySeq > 0 || declaredFKColumn[t.Name+"."+col.Name] {
				continue
			}
			base, ok := fkColumnBaseName(col.Name)
			if !ok {
				continue
			}
			refTable, ok := matchTableName(base, tableByLower)
			if !ok || strings.EqualFold(refTable, t.Name) {
				continue
			}
			refCol, ok := pkColumn[strings.ToLower(refTable)]
			if !ok {
				continue
			}

			contained, nonNullCount, err := sqlitereader.ColumnValuesContainedIn(db, t.Name, col.Name, refTable, refCol)
			if err != nil {
				return nil, fmt.Errorf("checking %s.%s against %s.%s: %w", t.Name, col.Name, refTable, refCol, err)
			}
			if !contained || nonNullCount == 0 {
				continue
			}

			suggestions[t.Name] = append(suggestions[t.Name], config.SuggestedForeignKey{
				ForeignKey: config.ForeignKey{
					Columns:    []string{col.Name},
					RefTable:   refTable,
					RefColumns: []string{refCol},
				},
				Rationale: fmt.Sprintf(
					"%s.%s matches table %q by naming convention, and every non-null value (%d checked) exists in %s.%s",
					t.Name, col.Name, refTable, nonNullCount, refTable, refCol,
				),
			})
		}
	}
	return suggestions, nil
}

// singleColumnPrimaryKey returns columns' primary key column name if
// exactly one column is a primary key, or "" for no primary key or a
// composite one — a naming-convention match can only ever point at a
// single column, so a composite-keyed table is never a valid reference
// target for this heuristic.
func singleColumnPrimaryKey(columns []sqlitereader.ColumnInfo) string {
	var name string
	count := 0
	for _, c := range columns {
		if c.PrimaryKeySeq > 0 {
			count++
			name = c.Name
		}
	}
	if count == 1 {
		return name
	}
	return ""
}

// fkColumnBaseName strips a trailing "id"/"_id" from name, reporting false
// if nothing is left (the whole name was just "id").
func fkColumnBaseName(name string) (string, bool) {
	m := fkColumnNamePattern.FindStringSubmatch(name)
	if m == nil || m[1] == "" {
		return "", false
	}
	return m[1], true
}

// matchTableName looks for a table named base itself, or a simply
// pluralized form of it ("+s", "+es", or "y" -> "ies"), case-insensitively.
// Irregular plurals aren't attempted — this is a naming-convention
// heuristic, not a full pluralization engine, and the value-containment
// check is what actually confirms a match.
func matchTableName(base string, tableByLower map[string]string) (string, bool) {
	candidates := []string{base, base + "s", base + "es"}
	if strings.HasSuffix(strings.ToLower(base), "y") && len(base) > 1 {
		candidates = append(candidates, base[:len(base)-1]+"ies")
	}
	for _, c := range candidates {
		if t, ok := tableByLower[strings.ToLower(c)]; ok {
			return t, true
		}
	}
	return "", false
}
