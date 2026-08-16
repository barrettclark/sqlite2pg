// Package ddl generates Postgres CREATE TABLE statements from a reviewed
// config.MigrationConfig.
package ddl

import (
	"fmt"
	"strings"

	"sqlite2pg/internal/config"
)

// dropSentinel marks a column excluded from the target schema entirely
// (e.g. Esri SHAPE blobs), set by the esri_typename_mapping heuristic.
const dropSentinel = "__drop__"

// GenerateCreateTable emits a CREATE TABLE statement for table using tc's
// ColumnOrder, skipping any column whose TargetType is the drop sentinel.
func GenerateCreateTable(table string, tc config.TableConfig) string {
	var cols []string
	for _, name := range tc.ColumnOrder {
		col, ok := tc.Columns[name]
		if !ok || col.TargetType == dropSentinel {
			continue
		}
		cols = append(cols, fmt.Sprintf("    %q %s", name, col.TargetType))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "CREATE TABLE %q (\n", table)
	b.WriteString(strings.Join(cols, ",\n"))
	b.WriteString("\n);\n")
	return b.String()
}

// IncludedColumns returns tc's column names, in declared order, excluding
// dropped columns — the column list both DDL and the COPY pipeline must
// agree on.
func IncludedColumns(tc config.TableConfig) []string {
	var names []string
	for _, name := range tc.ColumnOrder {
		if col, ok := tc.Columns[name]; ok && col.TargetType != dropSentinel {
			names = append(names, name)
		}
	}
	return names
}
