package sqlitereader

import (
	"database/sql"
	"fmt"

	"sqlite2pg/internal/profiler"
)

// SampleColumn returns up to limit values from a single column via a bounded
// LIMIT query — it never reads the full table, which matters for tables the
// size of atomic_database.db's ~1M-row MACS.
func SampleColumn(db *sql.DB, table, column string, limit int) ([]profiler.Value, error) {
	rows, err := db.Query(fmt.Sprintf(`SELECT %q FROM %q LIMIT ?`, column, table), limit)
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
		samples = append(samples, v)
	}
	return samples, rows.Err()
}

// SampleRows returns up to limit complete rows (all requested columns
// together, in order) via a single bounded LIMIT query — unlike
// SampleColumn, which samples one column independently of the others, this
// gives synchronized rows suitable for a data-preview grid where each row
// must show values that actually belong together.
func SampleRows(db *sql.DB, table string, columns []string, limit int) ([][]profiler.Value, error) {
	colList := ""
	for i, c := range columns {
		if i > 0 {
			colList += ", "
		}
		colList += fmt.Sprintf("%q", c)
	}

	rows, err := db.Query(fmt.Sprintf(`SELECT %s FROM %q LIMIT ?`, colList, table), limit)
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
		row := make([]profiler.Value, len(dest))
		copy(row, dest)
		result = append(result, row)
	}
	return result, rows.Err()
}

// CountRows returns table's total row count.
func CountRows(db *sql.DB, table string) (int, error) {
	var n int
	err := db.QueryRow(fmt.Sprintf(`SELECT COUNT(*) FROM %q`, table)).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("counting rows in %s: %w", table, err)
	}
	return n, nil
}

// StreamTable calls fn once per row of table, restricted to columns, via a
// single forward cursor. It never buffers the full table in memory — this
// is the direct fix for the pre-processing script's cur.fetchall() pattern,
// which loaded every row into a Python list before writing it back out.
func StreamTable(db *sql.DB, table string, columns []string, fn func(row []profiler.Value) error) error {
	colList := ""
	for i, c := range columns {
		if i > 0 {
			colList += ", "
		}
		colList += fmt.Sprintf("%q", c)
	}

	rows, err := db.Query(fmt.Sprintf(`SELECT %s FROM %q`, colList, table))
	if err != nil {
		return fmt.Errorf("streaming %s: %w", table, err)
	}
	defer rows.Close()

	dest := make([]any, len(columns))
	ptrs := make([]any, len(columns))
	for i := range dest {
		ptrs[i] = &dest[i]
	}

	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return err
		}
		row := make([]profiler.Value, len(dest))
		copy(row, dest)
		if err := fn(row); err != nil {
			return err
		}
	}
	return rows.Err()
}
