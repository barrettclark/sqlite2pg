package sqlitereader

import (
	"database/sql"
	"fmt"
)

// MaxTextLength returns the longest value column actually holds in table,
// measured the same way Postgres's varchar(N) length limit is (character
// count, not byte count — SQLite's LENGTH() on a TEXT value already counts
// UTF-8 characters, matching Postgres's semantics). ok is false when every
// row's value is NULL (MAX(LENGTH(...)) itself comes back NULL), meaning
// there is no real length evidence in the table at all.
func MaxTextLength(db *sql.DB, table, column string) (max int, ok bool, err error) {
	var n sql.NullInt64
	err = db.QueryRow(fmt.Sprintf(`SELECT MAX(LENGTH(%s)) FROM %s`, quoteIdent(column), quoteIdent(table))).Scan(&n)
	if err != nil {
		return 0, false, fmt.Errorf("measuring max length of %s.%s: %w", table, column, err)
	}
	if !n.Valid {
		return 0, false, nil
	}
	return int(n.Int64), true, nil
}
