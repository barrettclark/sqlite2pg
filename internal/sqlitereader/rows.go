package sqlitereader

import (
	"database/sql"
	"fmt"
	"strings"

	"sqlite2pg/internal/profiler"
)

// SampleColumn returns up to limit randomly-chosen values from a single
// column via `ORDER BY RANDOM() LIMIT` — a bounded result, but not a bounded
// scan: SQLite has to read and shuffle every row to pick from all of them
// fairly. A table physically sorted by this column (or by one correlated
// with it — e.g. a foreign key used as the table's natural insert order)
// used to produce a sample that was entirely one repeated value with a
// plain `LIMIT`, misleading heuristics like boolean01 into thinking a
// column with real variety was binary. Profiling a whole table's worth of
// columns this way means one random scan per column; ProfileDatabase
// prefers SampleRows for that reason — one random scan covers every column
// in the table at once. SampleColumn remains for callers that only need a
// single column.
func SampleColumn(db *sql.DB, table, column string, limit int) ([]profiler.Value, error) {
	rows, err := db.Query(fmt.Sprintf(`SELECT %s FROM %s ORDER BY RANDOM() LIMIT ?`, quoteIdent(column), quoteIdent(table)), limit)
	if err != nil {
		return nil, fmt.Errorf("sampling %s.%s: %w", table, column, err)
	}
	defer rows.Close()

	var samples []profiler.Value
	for rows.Next() {
		var v any
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		samples = append(samples, normalizeBlobValue(v))
	}
	return samples, rows.Err()
}

// SampleNonNullColumn returns up to limit randomly-chosen non-NULL values
// from a single column — a targeted rescue for a column whose ordinary
// random sample (SampleColumn/SampleRows) came back entirely NULL even
// though the table has more rows than that sample covered. A very sparse
// column (a real example: an Esri geodatabase's ClosedDate, 99.5% NULL)
// can easily land zero non-NULL values in a few-hundred-row random sample;
// without ever seeing a real value, a heuristic like esri_julian_day has
// nothing to evaluate and silently falls back to a passthrough type
// instead of flagging anything — no error, just a silently wrong type.
// This costs a full scan of the column when it's this sparse, same as
// SampleColumn's own ORDER BY RANDOM() — but ProfileDatabase only calls it
// when the ordinary sample already came back entirely empty-handed, so it
// isn't paid for every column, only the ones that need it.
func SampleNonNullColumn(db *sql.DB, table, column string, limit int) ([]profiler.Value, error) {
	rows, err := db.Query(fmt.Sprintf(`SELECT %s FROM %s WHERE %s IS NOT NULL ORDER BY RANDOM() LIMIT ?`, quoteIdent(column), quoteIdent(table), quoteIdent(column)), limit)
	if err != nil {
		return nil, fmt.Errorf("rescuing sparse column %s.%s: %w", table, column, err)
	}
	defer rows.Close()

	var samples []profiler.Value
	for rows.Next() {
		var v any
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		samples = append(samples, normalizeBlobValue(v))
	}
	return samples, rows.Err()
}

// SampleRows returns up to limit complete rows (all requested columns
// together, in order), chosen at random across the whole table via `ORDER
// BY RANDOM() LIMIT` rather than just the first rows — see SampleColumn's
// comment for why a plain LIMIT is unsafe for a table that isn't stored in
// an arbitrary order. Unlike SampleColumn, which samples one column
// independently of the others, this gives synchronized rows suitable for a
// data-preview grid where each row must show values that actually belong
// together — and, sampling every requested column in one random scan
// instead of one random scan per column, it's what ProfileDatabase uses to
// profile a whole table's columns together.
func SampleRows(db *sql.DB, table string, columns []string, limit int) ([][]profiler.Value, error) {
	colList := ""
	for i, c := range columns {
		if i > 0 {
			colList += ", "
		}
		colList += quoteIdent(c)
	}

	rows, err := db.Query(fmt.Sprintf(`SELECT %s FROM %s ORDER BY RANDOM() LIMIT ?`, colList, quoteIdent(table)), limit)
	if err != nil {
		return nil, fmt.Errorf("sampling rows from %s: %w", table, err)
	}
	defer rows.Close()

	dest := make([]any, len(columns))
	ptrs := make([]any, len(columns))
	for i := range dest {
		ptrs[i] = &dest[i]
	}

	var result [][]profiler.Value
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		normalizeBlobValues(dest)
		row := make([]profiler.Value, len(dest))
		copy(row, dest)
		result = append(result, row)
	}
	return result, rows.Err()
}

// CountRows returns table's total row count.
func CountRows(db *sql.DB, table string) (int, error) {
	var n int
	err := db.QueryRow(fmt.Sprintf(`SELECT COUNT(*) FROM %s`, quoteIdent(table))).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("counting rows in %s: %w", table, err)
	}
	return n, nil
}

// StreamTable calls fn once per row of table, restricted to columns, via a
// single forward cursor. It never buffers the full table in memory — this
// is the direct fix for the pre-processing script's cur.fetchall() pattern,
// which loaded every row into a Python list before writing it back out.
//
// This intentionally has no ORDER BY: every existing caller either doesn't
// care about row order (a full-table COPY source) or explicitly wants plain
// sequential-scan order, and this function's behavior for those callers
// must not change. A caller that needs a specific, deterministic,
// repeatable order should use StreamTableOrdered instead.
func StreamTable(db *sql.DB, table string, columns []string, fn func(row []profiler.Value) error) error {
	query := fmt.Sprintf(`SELECT %s FROM %s`, quoteIdentList(columns), quoteIdent(table))
	return streamQuery(db, table, query, len(columns), fn)
}

// StreamTableOrdered is StreamTable with an added `ORDER BY orderByColumns`
// clause, for a caller that needs a specific, deterministic, repeatable row
// order rather than whatever a plain sequential scan happens to return.
// VerifyTable is the motivating caller: Postgres 18 was observed, directly
// and reproducibly, not to reliably return a freshly-COPY'd, untouched
// table's rows in insertion order on a plain sequential scan — so a
// position-based comparison against a Postgres source that's genuinely
// ORDER-BY'd needs SQLite's side genuinely ordered the same way too, not
// just assumed to already match by scan happenstance. orderByColumns is
// typically a table's primary key columns, in PrimaryKeySeq order (see
// ddl.PrimaryKeyColumns).
func StreamTableOrdered(db *sql.DB, table string, columns []string, orderByColumns []string, fn func(row []profiler.Value) error) error {
	query := fmt.Sprintf(`SELECT %s FROM %s ORDER BY %s`, quoteIdentList(columns), quoteIdent(table), quoteIdentList(orderByColumns))
	return streamQuery(db, table, query, len(columns), fn)
}

// normalizeBlobValue fixes issue #56: modernc.org/sqlite (mirroring
// SQLite's own C API — sqlite3_column_blob() returns a NULL pointer for a
// zero-length BLOB even though the column is genuinely non-NULL) returns a
// nil []byte for a zero-length BLOB, identically to how it represents a
// column that's actually NULL. Scanned into `any`, the two cases ARE still
// distinguishable at the interface level: a genuine SQL NULL comes back as
// an untyped nil interface (v == nil), while a genuine zero-length BLOB
// comes back as a non-nil interface wrapping a nil []byte (v != nil, but
// v.([]byte) == nil) — confirmed directly against the driver, independent
// of this project's code, before writing this fix. That distinction is
// real but fragile: anything downstream that checks the byte slice itself
// for nil-ness (as pgx's bytea COPY encoding does) collapses the two cases
// back together and silently writes NULL for a real zero-length value.
// Normalizing every nil []byte we already know is non-NULL (because the
// interface itself wasn't nil) to a non-nil, zero-length []byte{} here —
// right at the boundary where the distinction is still available — means
// every caller downstream sees an ordinary, unambiguous empty slice
// instead of having to remember to make the fragile interface-nil check
// itself.
func normalizeBlobValue(v any) any {
	if b, ok := v.([]byte); ok && b == nil {
		return []byte{}
	}
	return v
}

// normalizeBlobValues applies normalizeBlobValue in place to every element
// of dest — see normalizeBlobValue for why this matters.
func normalizeBlobValues(dest []any) {
	for i, v := range dest {
		dest[i] = normalizeBlobValue(v)
	}
}

// quoteIdentList quotes and comma-joins names, e.g. for a SELECT column
// list or an ORDER BY clause.
func quoteIdentList(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = quoteIdent(n)
	}
	return strings.Join(quoted, ", ")
}

// streamQuery runs query (already fully built by StreamTable or
// StreamTableOrdered) and calls fn once per resulting row, scanning
// numCols columns into a fresh []profiler.Value per row. Factored out so
// both callers share identical scanning behavior rather than duplicating
// it.
func streamQuery(db *sql.DB, table, query string, numCols int, fn func(row []profiler.Value) error) error {
	rows, err := db.Query(query)
	if err != nil {
		return fmt.Errorf("streaming %s: %w", table, err)
	}
	defer rows.Close()

	dest := make([]any, numCols)
	ptrs := make([]any, numCols)
	for i := range dest {
		ptrs[i] = &dest[i]
	}

	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return err
		}
		normalizeBlobValues(dest)
		row := make([]profiler.Value, len(dest))
		copy(row, dest)
		if err := fn(row); err != nil {
			return err
		}
	}
	return rows.Err()
}
