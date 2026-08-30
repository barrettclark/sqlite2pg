package sqlitereader

import (
	"database/sql"
	"fmt"
)

// ColumnValuesContainedIn reports whether every non-null value of
// table.column exists in refTable.refColumn, plus how many non-null values
// table.column actually has — the value-containment check behind inferring
// an undeclared foreign key (issue #6). NULLs are ignored, not treated as
// missing (an optional reference is still a valid reference), and a
// column with zero non-null values reports contained=false: no real
// values means no real evidence of a relationship, so it must never be
// suggested as one.
func ColumnValuesContainedIn(db *sql.DB, table, column, refTable, refColumn string) (contained bool, nonNullCount int64, err error) {
	err = db.QueryRow(fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE %s IS NOT NULL`, quoteIdent(table), quoteIdent(column))).Scan(&nonNullCount)
	if err != nil {
		return false, 0, fmt.Errorf("counting non-null values in %s.%s: %w", table, column, err)
	}
	if nonNullCount == 0 {
		return false, 0, nil
	}

	var violations int64
	err = db.QueryRow(fmt.Sprintf(
		`SELECT COUNT(*) FROM %s WHERE %s IS NOT NULL AND %s NOT IN (SELECT %s FROM %s)`,
		quoteIdent(table), quoteIdent(column), quoteIdent(column), quoteIdent(refColumn), quoteIdent(refTable),
	)).Scan(&violations)
	if err != nil {
		return false, 0, fmt.Errorf("checking containment of %s.%s in %s.%s: %w", table, column, refTable, refColumn, err)
	}
	return violations == 0, nonNullCount, nil
}
