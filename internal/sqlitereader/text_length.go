package sqlitereader

import (
	"database/sql"
	"fmt"
	"strings"
)

// MaxTextLengths returns the length of each named column's longest value
// across every row of table, computed in a single scan.
//
// Length is measured the same way Postgres's varchar(N) limit is:
// character count, not byte count. SQLite's LENGTH() already counts UTF-8
// characters for a genuine TEXT-storage value, but SQLite's dynamic
// typing permits a BLOB-storage value in the same column (the exact shape
// issue #83 found), and LENGTH() on a BLOB counts bytes instead —
// CAST(... AS TEXT) forces character-counting for that case too.
//
// The result map contains an entry only for a column whose values aren't
// all NULL — a column absent from the map has no length evidence in the
// table at all (MAX(LENGTH(...)) itself comes back NULL).
//
// One scan per table, not per column: issue #84's varchar-widening check
// runs once per VARCHAR-suggested column, and a table with several such
// columns (the "MySQL-origin export" shape varcharSuggestions targets)
// would otherwise partially undermine issue #55's whole point (Copilot PR
// #96 finding).
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
