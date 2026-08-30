// Package ddl generates Postgres CREATE TABLE statements from a reviewed
// config.MigrationConfig.
package ddl

import (
	"fmt"
	"sort"
	"strings"

	"sqlite2pg/internal/config"
)

// dropSentinel marks a column excluded from the target schema entirely
// (e.g. Esri SHAPE blobs), set by the esri_typename_mapping heuristic.
const dropSentinel = "__drop__"

// GenerateCreateTable emits a CREATE TABLE statement for table using tc's
// ColumnOrder, skipping any column whose TargetType is the drop sentinel,
// with an inline PRIMARY KEY clause if any included column has a
// PrimaryKeySeq — this is preserved source truth carried straight from
// SQLite, not something a heuristic decides. Column identifiers are run
// through PostgresColumnNames first, so two source columns that would
// otherwise collide once Postgres truncates overlong identifiers (issue
// #21) come out disambiguated instead.
func GenerateCreateTable(table string, tc config.TableConfig) string {
	ids := PostgresColumnNames(tc)

	var cols []string
	for _, name := range IncludedColumns(tc) {
		cols = append(cols, fmt.Sprintf("    %s %s", quoteIdent(ids[name]), tc.Columns[name].TargetType))
	}
	if pk := primaryKeyColumns(tc); len(pk) > 0 {
		pkIDs := make([]string, len(pk))
		for i, name := range pk {
			pkIDs[i] = ids[name]
		}
		cols = append(cols, fmt.Sprintf("    PRIMARY KEY (%s)", quoteJoin(pkIDs)))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "CREATE TABLE %s (\n", quoteIdent(table))
	b.WriteString(strings.Join(cols, ",\n"))
	b.WriteString("\n);\n")
	return b.String()
}

// primaryKeyColumns returns tc's included primary-key columns ordered by
// their declared PrimaryKeySeq (1-based), not by ColumnOrder — a composite
// primary key's declared column order can differ from the table's overall
// column order. A dropped or otherwise excluded column that happened to be
// part of the primary key is simply omitted, same as it is from the
// column list itself.
func primaryKeyColumns(tc config.TableConfig) []string {
	type seqCol struct {
		seq  int
		name string
	}
	var pk []seqCol
	for _, name := range IncludedColumns(tc) {
		if seq := tc.Columns[name].PrimaryKeySeq; seq > 0 {
			pk = append(pk, seqCol{seq: seq, name: name})
		}
	}
	sort.Slice(pk, func(i, j int) bool { return pk[i].seq < pk[j].seq })

	names := make([]string, len(pk))
	for i, c := range pk {
		names[i] = c.name
	}
	return names
}

// quoteJoin quotes each identifier as SQL (see quoteIdent) and joins them
// with ", " — the form both a column list and a composite key clause need.
func quoteJoin(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = quoteIdent(n)
	}
	return strings.Join(quoted, ", ")
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
