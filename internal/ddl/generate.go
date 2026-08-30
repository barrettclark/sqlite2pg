// Package ddl generates Postgres CREATE TABLE statements from a reviewed
// config.MigrationConfig.
package ddl

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"sqlite2pg/internal/config"
)

// dropSentinel marks a column excluded from the target schema entirely
// (e.g. Esri SHAPE blobs), set by the esri_typename_mapping heuristic.
const dropSentinel = "__drop__"

// ErrNoIncludedColumns is returned by GenerateCreateTable when a table has
// zero columns left after excluding dropped ones (or had none to begin
// with) — e.g. an Esri table whose only column is a geometryblob mapped to
// __drop__. Postgres rejects `CREATE TABLE "t" (\n\n);` as a syntax error,
// so callers must skip the table (with a warning) rather than emit it
// (issue #30).
var ErrNoIncludedColumns = errors.New("table has no included columns")

// ErrMissingColumnOrder is returned by GenerateCreateTable when a
// TableConfig has entries in Columns but an empty ColumnOrder. column_order
// is `omitempty` in the YAML schema (config.TableConfig.ColumnOrder), so a
// hand-trimmed config can lose the key entirely; IncludedColumns then
// returns nil regardless of what Columns actually holds, degenerating into
// the same "zero columns" shape as ErrNoIncludedColumns's legitimate
// all-dropped case. Unlike that case, this one is almost certainly a
// config bug rather than an intentionally column-less table, so callers
// should treat it as fatal — reject the config — rather than skip and
// continue.
var ErrMissingColumnOrder = errors.New("table has columns but no column_order — likely a hand-edited config that dropped the column_order key")

// GenerateCreateTable emits a CREATE TABLE statement for table using tc's
// ColumnOrder, skipping any column whose TargetType is the drop sentinel,
// with an inline PRIMARY KEY clause if any included column has a
// PrimaryKeySeq, and a NOT NULL constraint on any included column whose
// NotNull is set — both are preserved source truth carried straight from
// SQLite, not something a heuristic decides. Column identifiers are run
// through PostgresColumnNames first, so two source columns that would
// otherwise collide once Postgres truncates overlong identifiers (issue
// #21) come out disambiguated instead.
//
// It returns ErrMissingColumnOrder or ErrNoIncludedColumns (see both) if tc
// has no columns to emit, rather than the invalid `CREATE TABLE "t" ();`
// issue #30 reported.
func GenerateCreateTable(table string, tc config.TableConfig) (string, error) {
	if missingColumnOrder(tc) {
		return "", fmt.Errorf("%s: %w", table, ErrMissingColumnOrder)
	}
	included := IncludedColumns(tc)
	if len(included) == 0 {
		return "", fmt.Errorf("%s: %w", table, ErrNoIncludedColumns)
	}

	ids := PostgresColumnNames(tc)

	var cols []string
	for _, name := range included {
		col := fmt.Sprintf("    %s %s", quoteIdent(ids[name]), tc.Columns[name].TargetType)
		if tc.Columns[name].NotNull {
			col += " NOT NULL"
		}
		cols = append(cols, col)
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
	return b.String(), nil
}

// missingColumnOrder reports whether tc looks like the ErrMissingColumnOrder
// config bug: columns declared but no order to emit them in.
func missingColumnOrder(tc config.TableConfig) bool {
	return len(tc.ColumnOrder) == 0 && len(tc.Columns) > 0
}

// ValidateTableConfigs checks every included table in cfg for the
// ErrMissingColumnOrder config bug up front, so `migrate load` (both
// --dry-run and the real path) can reject a bad config before doing any
// Postgres work, rather than discovering it mid-run the way
// GenerateCreateTable's per-table check would.
func ValidateTableConfigs(cfg *config.MigrationConfig) error {
	names := make([]string, 0, len(cfg.Tables))
	for name := range cfg.Tables {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		tc := cfg.Tables[name]
		if !tc.Include {
			continue
		}
		if missingColumnOrder(tc) {
			return fmt.Errorf("%s: %w", name, ErrMissingColumnOrder)
		}
	}
	return nil
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
