package sqlitereader

import (
	"database/sql"
	"fmt"
	"strings"
)

// MaxTextLength returns the length of column's longest value across every
// row of table, measured the same way Postgres's varchar(N) length limit
// is: character count, not byte count. SQLite's LENGTH() already counts
// UTF-8 characters for a genuine TEXT-storage value, but SQLite's dynamic
// typing permits a BLOB-storage value in the same column (the exact shape
// issue #83 found), and LENGTH() on a BLOB counts bytes instead —
// CAST(... AS TEXT) forces the same character-counting behavior for that
// case too. ok is false when every row's value is NULL (MAX(LENGTH(...))
// itself comes back NULL), meaning there is no real length evidence in
// the table at all.
func MaxTextLength(db *sql.DB, table, column string) (max int, ok bool, err error) {
	var n sql.NullInt64
	err = db.QueryRow(fmt.Sprintf(`SELECT MAX(LENGTH(CAST(%s AS TEXT))) FROM %s`, quoteIdent(column), quoteIdent(table))).Scan(&n)
	if err != nil {
		return 0, false, fmt.Errorf("measuring max length of %s.%s: %w", table, column, err)
	}
	if !n.Valid {
		return 0, false, nil
	}
	return int(n.Int64), true, nil
}

// MaxTextLengths is MaxTextLength for several columns of the same table,
// computed in a single scan instead of one scan per column — issue #84's
// varchar-widening check runs once per VARCHAR-suggested column, and a
// table with several such columns (the "MySQL-origin export" shape
// varcharSuggestions specifically targets) would otherwise partially
// undermine issue #55's whole point: paying one full-table scan per table,
// not per column (Copilot PR #96 finding). The result map only contains
// an entry for a column whose values aren't all NULL — same meaning as
// MaxTextLength's ok return, just per-column instead of a single bool.
func MaxTextLengths(db *sql.DB, table string, columns []string) (map[string]int, error) {
	result := make(map[string]int, len(columns))
	if len(columns) == 0 {
		return result, nil
	}

	exprs := make([]string, len(columns))
	for i, c := range columns {
		exprs[i] = fmt.Sprintf(`MAX(LENGTH(CAST(%s AS TEXT)))`, quoteIdent(c))
	}
	query := fmt.Sprintf(`SELECT %s FROM %s`, strings.Join(exprs, ", "), quoteIdent(table))

	dest := make([]sql.NullInt64, len(columns))
	ptrs := make([]any, len(columns))
	for i := range dest {
		ptrs[i] = &dest[i]
	}
	if err := db.QueryRow(query).Scan(ptrs...); err != nil {
		return nil, fmt.Errorf("measuring max lengths in %s: %w", table, err)
	}

	for i, c := range columns {
		if dest[i].Valid {
			result[c] = int(dest[i].Int64)
		}
	}
	return result, nil
}
