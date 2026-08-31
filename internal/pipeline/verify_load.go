package pipeline

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"sqlite2pg/internal/config"
	"sqlite2pg/internal/copywriter"
	"sqlite2pg/internal/ddl"
	"sqlite2pg/internal/profiler"
	"sqlite2pg/internal/sqlitereader"
)

// maxMismatchExamples caps how many per-row mismatch examples VerifyTable
// records for a single column, so a systematically-broken column (every
// row wrong) produces a usable report instead of a multi-million-line one.
// ColumnVerifyResult.MismatchCount is never capped — only the stored
// Examples are — so the report still shows the true total.
const maxMismatchExamples = 20

// ColumnMismatch is one disagreement between what the source SQLite value
// transforms to and what Postgres actually holds.
//
// What RowIndex means depends on TableVerifyResult.Ordered:
//   - Ordered true (the table has a primary key): both sides were read in
//     the same ORDER BY <primary key> order, so RowIndex is a real,
//     0-based row position — the Nth row by primary-key order on both
//     sides, genuinely corresponding to the same source row.
//   - Ordered false (no primary key, the aggregate comparison ran):
//     RowIndex is only the position within each column's independently
//     *sorted* value sequence (see compareColumnUnordered) — it does NOT
//     correspond to any source row, since without a key there is no way to
//     line up a Postgres row with the SQLite row it came from. Source and
//     Expected still come from the same original SQLite row as each other
//     (sorted together), but Actual, from the Postgres side, is not
//     guaranteed to be that same row's value — only that value equal to it
//     ought to appear somewhere. Report output for this case must say so
//     explicitly rather than implying row-position precision it doesn't
//     have.
type ColumnMismatch struct {
	RowIndex int // see doc comment above — meaning depends on Ordered
	Source   any // the raw SQLite value, before any transform
	Expected any // Source run through the column's configured Transform
	Actual   any // what Postgres actually returned
}

// ColumnVerifyResult accumulates every mismatch VerifyTable found in one
// column across a table's full comparison.
type ColumnVerifyResult struct {
	MismatchCount int
	Examples      []ColumnMismatch // capped at maxMismatchExamples
}

// TableVerifyResult is VerifyTable's outcome for one table.
type TableVerifyResult struct {
	Table          string
	SourceRowCount int
	TargetRowCount int

	// RowCountMismatch means SourceRowCount != TargetRowCount. When true,
	// the expensive full row-by-column comparison never ran (there is
	// already a definitive failure), so RowsCompared and ColumnResults
	// are left at their zero values.
	RowCountMismatch bool

	// RowsCompared is how many row pairs the full comparison actually
	// walked (0 if the table was skipped or RowCountMismatch is true). In
	// the no-PK aggregate path (Ordered false) this is still SourceRowCount
	// — every row's values were checked, just not row-by-row.
	RowsCompared int

	// Ordered reports which of VerifyTable's two comparison strategies ran:
	// true means the table has a primary key and both sides were read in
	// matching ORDER BY <primary key> order, so ColumnMismatch.RowIndex is
	// a real, precise row position. False means the table has no primary
	// key, so VerifyTable fell back to comparing each column's sorted
	// value multiset instead (see compareColumnUnordered) — still
	// exhaustive, but ColumnMismatch.RowIndex in that case is only a
	// position within the sorted comparison, not a source row.
	Ordered bool

	// ColumnResults is keyed by the column's SQLite name (matching
	// config.TableConfig.Columns), not its possibly-disambiguated
	// Postgres identifier. A column with zero mismatches has no entry.
	ColumnResults map[string]*ColumnVerifyResult
}

// TotalMismatches sums every column's MismatchCount.
func (r TableVerifyResult) TotalMismatches() int {
	total := 0
	for _, cr := range r.ColumnResults {
		total += cr.MismatchCount
	}
	return total
}

// Passed reports whether this table is clean: row counts agreed and (if
// the full comparison ran) not a single value mismatched.
func (r TableVerifyResult) Passed() bool {
	return !r.RowCountMismatch && r.TotalMismatches() == 0
}

// VerifyTable confirms that table's Postgres copy is a byte-for-byte,
// correctly-transformed copy of its SQLite source: first a cheap row-count
// check, then (only if counts agree) a full comparison of every included
// column, re-deriving what each Postgres value SHOULD be by running the
// exact same copywriter.Transform the original load used.
//
// The full comparison uses one of two strategies, chosen by whether table
// has a primary key (config.ColumnConfig.PrimaryKeySeq on at least one
// included column):
//
//   - With a primary key: both sides are read genuinely ORDER BY'd by it —
//     sqlitereader.StreamTableOrdered on the SQLite side, `ORDER BY
//     "pg_pk_col", ...` on the Postgres side — and compared row-by-row by
//     position (the Nth row of one against the Nth row of the other).
//     Because both orderings are real and identical, this is precise and
//     deterministic: TableVerifyResult.Ordered is true, and every
//     ColumnMismatch.RowIndex is a genuine, meaningful row position.
//
//   - Without a primary key: there is no column (or column set) guaranteed
//     both unique and indexed to order by, and — as this exact package
//     discovered during its own development — a bare, unordered scan of
//     either side cannot be trusted to line up two logically-identical
//     tables row-for-row anyway. Confirmed directly: Postgres 18 was
//     observed NOT reliably returning a freshly-COPY'd, entirely untouched
//     table's rows in insertion order on a plain sequential scan (real
//     fixtures — bikes.db, chinook.db's tracks — reproduced a single row
//     landing one scan-position early immediately after a fresh load,
//     permanently offsetting every row after it; root-caused via direct
//     pgx-level instrumentation to something downstream of pgx itself,
//     which sends rows to the server in the correct order — so this is a
//     genuine Postgres 18 storage/COPY behavior, not a bug in this
//     codebase's TableSource or LoadTable). So instead of trusting any
//     scan order, VerifyTable falls back to compareColumnUnordered: for
//     each column, collect every value from both sides independently, sort
//     each side into the same canonical order, and compare position by
//     position in that sorted order — a comparison of value *multisets*,
//     not of rows. TableVerifyResult.Ordered is false in this case, and
//     ColumnMismatch.RowIndex only means "this position in the sorted
//     comparison", not a source row (see ColumnMismatch's doc comment).
//     This path holds an entire column's worth of values from both sides
//     in memory at once to sort them — more expensive than the streaming
//     primary-key path — but that cost is only paid for tables that lack a
//     primary key, and is the accepted price for exhaustive, false-positive-
//     free verification on tables without one.
// pgTable must be the identifier CREATE TABLE actually emitted for table
// (see ddl.PostgresTableNames/issue #44) — not necessarily table itself —
// since two source tables identical in their first 63 bytes are
// disambiguated for Postgres the same way colliding column names are;
// querying by table's raw name would connect to the wrong relation (or
// none at all) whenever disambiguation actually kicked in. table is still
// what's used for every SQLite-side read, and for TableVerifyResult.Table
// and error messages, since the source name is what a human reading a
// verify report needs to recognize.
func VerifyTable(ctx context.Context, sourceDB *sql.DB, pgConn *pgx.Conn, table, pgTable string, tc config.TableConfig) (TableVerifyResult, error) {
	result := TableVerifyResult{Table: table, ColumnResults: map[string]*ColumnVerifyResult{}}

	included := ddl.IncludedColumns(tc)
	if len(included) == 0 {
		return result, nil
	}

	sourceCount, err := sqlitereader.CountRows(sourceDB, table)
	if err != nil {
		return result, err
	}
	result.SourceRowCount = sourceCount

	var targetCount int
	if err := pgConn.QueryRow(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM %s`, pgx.Identifier{pgTable}.Sanitize())).Scan(&targetCount); err != nil {
		return result, fmt.Errorf("counting rows in Postgres table %s: %w", pgTable, err)
	}
	result.TargetRowCount = targetCount

	if sourceCount != targetCount {
		result.RowCountMismatch = true
		return result, nil
	}

	pk := ddl.PrimaryKeyColumns(tc)
	if len(pk) > 0 {
		return verifyTableOrdered(ctx, sourceDB, pgConn, table, pgTable, tc, included, pk, result)
	}
	return verifyTableUnordered(ctx, sourceDB, pgConn, table, pgTable, tc, included, result)
}

// isTextTargetType reports whether targetType (a config.ColumnConfig.
// TargetType value, one of review.TypeOptions) is a Postgres type that
// supports COLLATE — i.e. text-shaped. Deliberately narrow (just "text" and
// "varchar", the only text-shaped entries review.TypeOptions offers today):
// jsonb, though it also falls back to pgtype.Text for scanning purposes
// (see newPgColumnScanner), is not a collatable type in Postgres and
// applying COLLATE to a jsonb column errors, so it must not match here.
func isTextTargetType(targetType string) bool {
	switch targetType {
	case "text", "varchar":
		return true
	default:
		return false
	}
}

// verifyTableOrdered runs VerifyTable's primary-key comparison path: both
// sides read in matching ORDER BY <pk> order, compared row-by-row by
// position. See VerifyTable's doc comment for the full rationale.
func verifyTableOrdered(ctx context.Context, sourceDB *sql.DB, pgConn *pgx.Conn, table, pgTable string, tc config.TableConfig, included, pk []string, result TableVerifyResult) (TableVerifyResult, error) {
	result.Ordered = true

	ids := ddl.PostgresColumnNames(tc)
	quotedCols := make([]string, len(included))
	for i, name := range included {
		quotedCols[i] = pgx.Identifier{ids[name]}.Sanitize()
	}
	quotedPK := make([]string, len(pk))
	for i, name := range pk {
		orderExpr := pgx.Identifier{ids[name]}.Sanitize()
		if isTextTargetType(tc.Columns[name].TargetType) {
			// SQLite's default text comparison is BINARY (byte order), but
			// Postgres's plain ORDER BY uses the database's configured
			// collation (e.g. en_US.UTF-8, locale-aware) unless told
			// otherwise — these can and do disagree (e.g. "Makefile.in"
			// sorts before "aclocal.m4" under BINARY but after it under
			// en_US.UTF-8). Forcing COLLATE "C" — Postgres's byte-order
			// collation — on any text-typed primary-key column keeps this
			// ORDER BY equivalent to SQLite's, so both sides genuinely walk
			// rows in the same order. Non-text PK column types (integer,
			// uuid, ...) have no collation concept and are left as-is.
			orderExpr += ` COLLATE "C"`
		}
		quotedPK[i] = orderExpr
	}
	query := fmt.Sprintf(`SELECT %s FROM %s ORDER BY %s`,
		strings.Join(quotedCols, ", "), pgx.Identifier{pgTable}.Sanitize(), strings.Join(quotedPK, ", "))

	pgRows, err := pgConn.Query(ctx, query)
	if err != nil {
		return result, fmt.Errorf("querying Postgres table %s: %w", pgTable, err)
	}
	defer pgRows.Close()

	scanners := make([]*pgColumnScanner, len(included))
	scanDests := make([]any, len(included))
	for i, name := range included {
		scanners[i] = newPgColumnScanner(tc.Columns[name].TargetType)
		scanDests[i] = scanners[i].dest
	}

	rowIndex := 0
	streamErr := sqlitereader.StreamTableOrdered(sourceDB, table, included, pk, func(row []profiler.Value) error {
		if !pgRows.Next() {
			return fmt.Errorf("postgres table %s ran out of rows at row %d despite matching row counts (concurrent write during verify?)", pgTable, rowIndex)
		}
		if err := pgRows.Scan(scanDests...); err != nil {
			return fmt.Errorf("scanning postgres row %d of %s: %w", rowIndex, pgTable, err)
		}

		for i, name := range included {
			expected, err := copywriter.Transform(tc.Columns[name].Transform, row[i])
			if err != nil {
				return fmt.Errorf("re-applying transform for %s.%s at row %d: %w", table, name, rowIndex, err)
			}
			actual := scanners[i].value()
			if !valuesMatch(expected, actual) {
				cr := result.ColumnResults[name]
				if cr == nil {
					cr = &ColumnVerifyResult{}
					result.ColumnResults[name] = cr
				}
				cr.MismatchCount++
				if len(cr.Examples) < maxMismatchExamples {
					cr.Examples = append(cr.Examples, ColumnMismatch{
						RowIndex: rowIndex,
						Source:   row[i],
						Expected: expected,
						Actual:   actual,
					})
				}
			}
		}

		rowIndex++
		result.RowsCompared = rowIndex
		return nil
	})
	if streamErr != nil {
		return result, streamErr
	}
	if err := pgRows.Err(); err != nil {
		return result, fmt.Errorf("reading Postgres table %s: %w", pgTable, err)
	}
	return result, nil
}

// verifyTableUnordered runs VerifyTable's no-primary-key fallback: each
// included column is compared independently via compareColumnUnordered.
// See VerifyTable's doc comment for the full rationale.
func verifyTableUnordered(ctx context.Context, sourceDB *sql.DB, pgConn *pgx.Conn, table, pgTable string, tc config.TableConfig, included []string, result TableVerifyResult) (TableVerifyResult, error) {
	result.Ordered = false
	result.RowsCompared = result.SourceRowCount

	// Collect every included column's transformed SQLite values in one
	// pass, keyed by column name — cheaper than one SQLite scan per column.
	expected := make(map[string][]any, len(included))
	streamErr := sqlitereader.StreamTable(sourceDB, table, included, func(row []profiler.Value) error {
		for i, name := range included {
			v, err := copywriter.Transform(tc.Columns[name].Transform, row[i])
			if err != nil {
				return fmt.Errorf("re-applying transform for %s.%s: %w", table, name, err)
			}
			expected[name] = append(expected[name], v)
		}
		return nil
	})
	if streamErr != nil {
		return result, streamErr
	}

	// Collect every included column's actual Postgres values in one pass.
	ids := ddl.PostgresColumnNames(tc)
	quotedCols := make([]string, len(included))
	for i, name := range included {
		quotedCols[i] = pgx.Identifier{ids[name]}.Sanitize()
	}
	query := fmt.Sprintf(`SELECT %s FROM %s`, strings.Join(quotedCols, ", "), pgx.Identifier{pgTable}.Sanitize())

	pgRows, err := pgConn.Query(ctx, query)
	if err != nil {
		return result, fmt.Errorf("querying Postgres table %s: %w", pgTable, err)
	}
	defer pgRows.Close()

	scanners := make([]*pgColumnScanner, len(included))
	scanDests := make([]any, len(included))
	for i, name := range included {
		scanners[i] = newPgColumnScanner(tc.Columns[name].TargetType)
		scanDests[i] = scanners[i].dest
	}

	actual := make(map[string][]any, len(included))
	for pgRows.Next() {
		if err := pgRows.Scan(scanDests...); err != nil {
			return result, fmt.Errorf("scanning postgres row of %s: %w", pgTable, err)
		}
		for i, name := range included {
			actual[name] = append(actual[name], scanners[i].value())
		}
	}
	if err := pgRows.Err(); err != nil {
		return result, fmt.Errorf("reading Postgres table %s: %w", pgTable, err)
	}

	for _, name := range included {
		if mismatches := compareColumnUnordered(expected[name], actual[name]); len(mismatches) > 0 {
			cr := &ColumnVerifyResult{MismatchCount: len(mismatches)}
			if len(mismatches) > maxMismatchExamples {
				cr.Examples = mismatches[:maxMismatchExamples]
			} else {
				cr.Examples = mismatches
			}
			result.ColumnResults[name] = cr
		}
	}
	return result, nil
}

// compareColumnUnordered compares one column's full set of expected
// (transformed SQLite) values against its full set of actual (Postgres)
// values without relying on either side's scan/row order: both slices are
// sorted into the same canonical order via sortKeyFor, then compared
// position by position. Equal multisets sort identically regardless of
// which physical order either side originally came back in, so this can't
// produce VerifyTable's scan-order-drift false positive the way a raw
// positional comparison could — but a mismatch it does find is reported
// against a *sorted position*, not a source row (see ColumnMismatch's doc
// comment): with no key to line source and target rows up by, there is no
// way to say the mismatch belongs to any particular original row.
func compareColumnUnordered(expected, actual []any) []ColumnMismatch {
	type keyedValue struct {
		key string
		val any
	}
	keyed := func(vals []any) []keyedValue {
		out := make([]keyedValue, len(vals))
		for i, v := range vals {
			out[i] = keyedValue{key: sortKeyFor(v), val: v}
		}
		sort.Slice(out, func(i, j int) bool { return out[i].key < out[j].key })
		return out
	}

	sortedExpected := keyed(expected)
	sortedActual := keyed(actual)

	var mismatches []ColumnMismatch
	for i := range sortedExpected {
		if sortedExpected[i].key != sortedActual[i].key {
			mismatches = append(mismatches, ColumnMismatch{
				RowIndex: i, // position in the sorted comparison, not a source row — see ColumnMismatch's doc comment
				Expected: sortedExpected[i].val,
				Actual:   sortedActual[i].val,
			})
		}
	}
	return mismatches
}

// numericValue extracts a float64 representation of v when v is an int64 or
// float64 — the two concrete Go types VerifyTable's transform/scan pipeline
// ever produces for a numeric column — reporting false for anything else.
// float64 has exact integer precision only up to 2^53 (~9x10^15), which
// comfortably covers realistic values this tool deals with (Julian-day
// numbers, Unix timestamps, row counts), so converting an int64 through
// float64 loses nothing for any value actually seen in practice.
//
// This is the single shared numeric-comparison primitive both valuesMatch
// and sortKeyFor build on, specifically so they can't silently drift apart
// again the way they already have twice: once in the original
// int64-vs-float64 type-tag bug, and again in 9de206a's partial fix, which
// made sortKeyFor mirror valuesMatch's own flawed fmt.Sprintf("%v", ...)
// fallback — a fallback that breaks on large numbers because Go's default
// float formatting switches to scientific notation above a certain
// magnitude (fmt.Sprintf("%v", float64(2454348)) == "2.454348e+06") while
// the int64 side stays plain decimal ("2454348"), so two numerically
// identical values compared as different strings. Both call sites now
// route through this same numeric conversion instead of text formatting.
func numericValue(v any) (float64, bool) {
	switch t := v.(type) {
	case int64:
		return float64(t), true
	case float64:
		return t, true
	}
	return 0, false
}

// numericKeyText renders n as fixed-point decimal text — never scientific
// notation — so it's safe to use as part of a sort key (sortKeyFor) where
// two numerically equal values, converted from whichever concrete numeric
// type they arrived as, must always render identically.
func numericKeyText(n float64) string {
	return strconv.FormatFloat(n, 'f', -1, 64)
}

// sortKeyFor renders v into a string sort key such that two values
// VerifyTable would otherwise consider equal (per valuesMatch) always
// produce the same key, and different values are vanishingly unlikely to
// collide. It doesn't need to sort by any semantically meaningful order —
// only to sort both sides of compareColumnUnordered the same, consistent
// way so equal values land at the same position on both sides. Each case
// mirrors the type valuesMatch already treats specially, formatted so a
// value and its equal counterpart from the *other* side (SQLite-derived
// vs. Postgres-derived) normalize identically despite arriving as
// different concrete representations — most notably time.Time, which must
// key off the instant (.UTC().UnixNano()) rather than its printed form,
// since the same instant can carry a different Location and so format
// differently despite being equal per time.Time.Equal.
//
// int64 and float64 deliberately share one case (and one key namespace)
// rather than being tagged apart by concrete Go type: valuesMatch treats an
// int64 on one side against a float64 on the other (e.g. a SQLite NUMERIC
// column stored dynamically as an integer, transformed straight through to
// a `double precision` target) as equal whenever they represent the same
// number (see numericValue/valuesMatch). Tagging by concrete type here, as
// this used to, gave int64(100) and float64(100) different key prefixes and
// so different sorted positions — a false-positive mismatch despite
// valuesMatch itself considering them equal. Both cases now key off
// numericValue's shared float64 conversion, rendered via numericKeyText
// (fixed-point decimal, never scientific notation — see its doc comment
// for why: a naive fmt.Sprintf("%v", ...) text key, tried here once
// already in 9de206a, broke on large numbers for exactly this reason).
// This keeps sortKeyFor's invariant intact: it still separates genuinely
// different numeric values (int64(100) vs int64(200), or float64(100) vs
// float64(100.5)) exactly as before, since each distinct value still
// converts to a distinct float64 and so distinct key text.
func sortKeyFor(v any) string {
	if v == nil {
		return "\x00nil"
	}
	switch t := v.(type) {
	case time.Time:
		return fmt.Sprintf("\x01time:%d", t.UTC().UnixNano())
	case pgtype.UUID:
		return fmt.Sprintf("\x02uuid:%v:%x", t.Valid, t.Bytes)
	case []pgtype.UUID:
		parts := make([]string, len(t))
		for i, u := range t {
			parts[i] = fmt.Sprintf("%v:%x", u.Valid, u.Bytes)
		}
		return "\x03uuidlist:" + strings.Join(parts, ",")
	case []byte:
		return "\x04bytes:" + string(t)
	case bool:
		return fmt.Sprintf("\x05bool:%v", t)
	case int64, float64:
		n, _ := numericValue(v)
		return "\x06num:" + numericKeyText(n)
	case string:
		return "\x08string:" + t
	default:
		return fmt.Sprintf("\x09%T:%v", v, v)
	}
}

// pgColumnScanner owns one column's pgx scan destination, chosen from the
// column's target Postgres type, plus the logic to read that destination
// back out as a plain, comparable Go value once Scan has populated it.
//
// Every destination is one of pgx's nullable pgtype wrapper structs (e.g.
// pgtype.Int8, pgtype.Bool) rather than a bare Go primitive: a bare *int64
// can't represent SQL NULL and Scan errors out the moment it meets one,
// where these wrapper types carry their own Valid flag. pgx's built-in
// codecs accept these wrapper types via the interfaces they implement
// (Int64Scanner, Float64Scanner, BoolScanner, TextScanner, ...) for any
// source type that can reasonably convert to that shape — e.g. an int4 or
// int2 column scans into pgtype.Int8 exactly as an int8 column would — so
// one wrapper type per family covers smallint/integer/bigint (and numeric,
// via Float64Scanner) without a case per exact OID. bytea and uuid[]
// already have a native nil-able Go representation ([]byte, []pgtype.UUID)
// and so scan directly into a plain pointer to that type.
type pgColumnScanner struct {
	targetType string
	dest       any
}

// newPgColumnScanner picks dest's concrete type from targetType — the
// value stored in config.ColumnConfig.TargetType, one of
// review.TypeOptions (see internal/review/review_model.go). Anything not
// explicitly listed (text, jsonb, or an unrecognized/future type) falls
// back to pgtype.Text, matching how the review UI treats those as
// text-shaped for preview purposes (internal/tui/logic.go's
// previewValueForType).
func newPgColumnScanner(targetType string) *pgColumnScanner {
	var dest any
	switch targetType {
	case "boolean":
		dest = new(pgtype.Bool)
	case "smallint", "integer", "bigint":
		dest = new(pgtype.Int8)
	case "double precision", "real", "numeric":
		// numeric goes through Float64Scanner (like double precision/real)
		// rather than Int64Scanner, so a fractional numeric value doesn't
		// get silently truncated by the scan itself.
		dest = new(pgtype.Float8)
	case "date":
		dest = new(pgtype.Date)
	case "timestamptz":
		dest = new(pgtype.Timestamptz)
	case "bytea":
		dest = new([]byte)
	case "uuid":
		dest = new(pgtype.UUID)
	case "uuid[]":
		dest = new([]pgtype.UUID)
	default:
		dest = new(pgtype.Text)
	}
	return &pgColumnScanner{targetType: targetType, dest: dest}
}

// value reads back whatever the most recent Scan populated s.dest with, as
// a plain Go value comparable against copywriter.Transform's output: nil
// for SQL NULL, otherwise the wrapper's underlying value unwrapped (e.g.
// pgtype.Int8{Int64: 5, Valid: true} becomes plain int64(5)).
func (s *pgColumnScanner) value() any {
	switch d := s.dest.(type) {
	case *pgtype.Bool:
		if !d.Valid {
			return nil
		}
		return d.Bool
	case *pgtype.Int8:
		if !d.Valid {
			return nil
		}
		return d.Int64
	case *pgtype.Float8:
		if !d.Valid {
			return nil
		}
		return d.Float64
	case *pgtype.Date:
		if !d.Valid {
			return nil
		}
		return d.Time
	case *pgtype.Timestamptz:
		if !d.Valid {
			return nil
		}
		return d.Time
	case *[]byte:
		if *d == nil {
			return nil
		}
		return *d
	case *pgtype.UUID:
		if !d.Valid {
			return nil
		}
		return *d
	case *[]pgtype.UUID:
		if *d == nil {
			return nil
		}
		return *d
	case *pgtype.Text:
		if !d.Valid {
			return nil
		}
		return d.String
	default:
		return nil
	}
}

// valuesMatch reports whether expected (copywriter.Transform's output) and
// actual (what pgColumnScanner.value read back from Postgres) represent
// the same value. This is deliberately a set of explicit per-type cases,
// not a generic reflect.DeepEqual: a time.Time must compare via .Equal()
// since the same instant can carry a different Location (UTC vs a fixed
// zero-offset zone) and so differ in struct equality despite being the
// same instant; a pgtype.UUID compares its Valid flag and 16-byte value
// rather than the whole struct (which also carries no other meaningful
// field, but being explicit here avoids ever silently depending on
// pgtype's internal layout); and plain scalars compare by value once their
// concrete Go types are confirmed to match.
//
// int64 vs float64 (in either direction) is handled before the typed
// switch, via the shared numericValue helper (see its doc comment), rather
// than as one of the typed cases below or via the final string-formatting
// fallback: a SQLite NUMERIC column stored dynamically as an integer,
// transformed straight through to a `double precision` target, commonly
// produces exactly this int64-vs-float64 shape, and the two values must
// compare equal whenever they represent the same number — including large
// numbers (e.g. Julian-day values like 2454348), where a text-formatting
// comparison would incorrectly disagree because Go's default float
// formatting switches to scientific notation above a certain magnitude
// while int64 formatting never does.
func valuesMatch(expected, actual any) bool {
	if expected == nil || actual == nil {
		return expected == nil && actual == nil
	}

	if en, eok := numericValue(expected); eok {
		if an, aok := numericValue(actual); aok {
			return en == an
		}
	}

	switch e := expected.(type) {
	case time.Time:
		if a, ok := actual.(time.Time); ok {
			return e.Equal(a)
		}
	case pgtype.UUID:
		if a, ok := actual.(pgtype.UUID); ok {
			return e.Valid == a.Valid && e.Bytes == a.Bytes
		}
	case []pgtype.UUID:
		if a, ok := actual.([]pgtype.UUID); ok {
			if len(e) != len(a) {
				return false
			}
			for i := range e {
				if e[i].Valid != a[i].Valid || e[i].Bytes != a[i].Bytes {
					return false
				}
			}
			return true
		}
	case []byte:
		if a, ok := actual.([]byte); ok {
			return bytes.Equal(e, a)
		}
	case bool:
		if a, ok := actual.(bool); ok {
			return e == a
		}
	case string:
		if a, ok := actual.(string); ok {
			return e == a
		}
	}

	// Fallback for a shape mismatch between expected's and actual's Go
	// types — e.g. an untransformed (passthrough) column whose SQLite
	// driver value type doesn't line up 1:1 with the destination scan
	// type chosen for its target Postgres type. Comparing formatted
	// representations here is a deliberate last resort, not the primary
	// comparison strategy: every type VerifyTable's own transform/scan
	// pipeline actually produces is covered by an explicit case above (or,
	// for numeric types, the numericValue check above the switch).
	return fmt.Sprintf("%v", expected) == fmt.Sprintf("%v", actual)
}
