package sqlitereader

import (
	"database/sql"
	"fmt"
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
