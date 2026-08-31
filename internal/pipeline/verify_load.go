package pipeline

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
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

// ColumnMismatch is one row's disagreement between what the source SQLite
// value transforms to and what Postgres actually holds.
type ColumnMismatch struct {
	RowIndex int // 0-based, in sequential-scan order
	Source   any // the raw SQLite value, before any transform
	Expected any // Source run through the column's configured Transform
	Actual   any // what Postgres actually returned for this row/column
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
	// walked (0 if the table was skipped or RowCountMismatch is true).
	RowsCompared int

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
// check, then (only if counts agree) a full row-by-column comparison of
// every included column, re-deriving what each Postgres value SHOULD be by
// running the exact same copywriter.Transform the original load used.
//
// The full comparison reads both sides as plain forward, sequential scans
// — sqlitereader.StreamTable on the SQLite side (this codebase's
// documented "no ORDER BY" read pattern) and a bare `SELECT columns FROM
// table` with no ORDER BY on the Postgres side — and compares the Nth row
// of one against the Nth row of the other purely by position. This relies
// on Postgres returning a freshly-COPY'd, unmodified table's rows in
// physical/insertion order under a plain sequential scan. That is NOT a
// documented Postgres guarantee (no ORDER BY means the planner is free to
// return rows in any order it likes), and in practice it is less reliable
// than it sounds: a table modified since its load is an obvious risk (an
// UPDATE writes a new tuple version that is not guaranteed to land back in
// its old scan position — confirmed directly during this feature's own
// development, where updating a single row of a freshly-loaded table was
// enough to shift everything after it by one scan position). Less
// obviously, the same drift was also observed, deterministically and
// reproducibly, on a table that was never touched after its own COPY —
// real fixtures with ordinary variable-width text columns (bikes.db,
// chinook.db's tracks) exhibited a single row landing one scan-position
// early, permanently offsetting every row after it, immediately after a
// completely fresh load. Root-caused (via direct pgx-level instrumentation
// of the exact CopyFromSource pgx.Conn.CopyFrom consumed) to something
// downstream of this codebase entirely — pgx sends rows to the server in
// the correct order — so this is either a genuine Postgres 18 storage/COPY
// behavior or a bug in it, not a bug in TableSource or LoadTable. The
// practical consequence: when VerifyTable reports mismatches, always check
// whether they show the classic drift signature (adjacent rows' expected
// and actual values swapped by one position) before concluding the data
// itself is wrong — that signature means scan-order drift, not corruption.
func VerifyTable(ctx context.Context, sourceDB *sql.DB, pgConn *pgx.Conn, table string, tc config.TableConfig) (TableVerifyResult, error) {
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
	if err := pgConn.QueryRow(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM %s`, pgx.Identifier{table}.Sanitize())).Scan(&targetCount); err != nil {
		return result, fmt.Errorf("counting rows in Postgres table %s: %w", table, err)
	}
	result.TargetRowCount = targetCount

	if sourceCount != targetCount {
		result.RowCountMismatch = true
		return result, nil
	}

	ids := ddl.PostgresColumnNames(tc)
	quotedCols := make([]string, len(included))
	for i, name := range included {
		quotedCols[i] = pgx.Identifier{ids[name]}.Sanitize()
	}
	query := fmt.Sprintf(`SELECT %s FROM %s`, strings.Join(quotedCols, ", "), pgx.Identifier{table}.Sanitize())

	pgRows, err := pgConn.Query(ctx, query)
	if err != nil {
		return result, fmt.Errorf("querying Postgres table %s: %w", table, err)
	}
	defer pgRows.Close()

	scanners := make([]*pgColumnScanner, len(included))
	scanDests := make([]any, len(included))
	for i, name := range included {
		scanners[i] = newPgColumnScanner(tc.Columns[name].TargetType)
		scanDests[i] = scanners[i].dest
	}

	rowIndex := 0
	streamErr := sqlitereader.StreamTable(sourceDB, table, included, func(row []profiler.Value) error {
		if !pgRows.Next() {
			return fmt.Errorf("postgres table %s ran out of rows at row %d despite matching row counts (concurrent write during verify?)", table, rowIndex)
		}
		if err := pgRows.Scan(scanDests...); err != nil {
			return fmt.Errorf("scanning postgres row %d of %s: %w", rowIndex, table, err)
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
		return result, fmt.Errorf("reading Postgres table %s: %w", table, err)
	}
	return result, nil
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
func valuesMatch(expected, actual any) bool {
	if expected == nil || actual == nil {
		return expected == nil && actual == nil
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
	case int64:
		if a, ok := actual.(int64); ok {
			return e == a
		}
	case float64:
		if a, ok := actual.(float64); ok {
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
	// pipeline actually produces is covered by an explicit case above.
	return fmt.Sprintf("%v", expected) == fmt.Sprintf("%v", actual)
}
