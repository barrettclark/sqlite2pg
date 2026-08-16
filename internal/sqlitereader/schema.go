// Package sqlitereader reads schema and streams row data from a SQLite
// source database using modernc.org/sqlite (pure Go, no CGO — keeps the
// tool's single-static-binary distribution story intact).
package sqlitereader

import (
	"database/sql"
	"fmt"
)

// ColumnInfo describes a column as declared in the SQLite source schema.
type ColumnInfo struct {
	Name         string
	DeclaredType string
	NotNull      bool
	PrimaryKey   bool
}

// TableInfo describes a table and its columns.
type TableInfo struct {
	Name    string
	Columns []ColumnInfo
}

// ReadSchema reads every user table (excluding SQLite's own sqlite_%
// system tables) and its columns via PRAGMA table_info.
func ReadSchema(db *sql.DB) ([]TableInfo, error) {
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("listing tables: %w", err)
	}
	defer rows.Close()

	var tableNames []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scanning table name: %w", err)
		}
		tableNames = append(tableNames, name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var tables []TableInfo
	for _, name := range tableNames {
		cols, err := readColumns(db, name)
		if err != nil {
			// Some FGDB/Spatialite exports declare virtual tables (spatial
			// indexes, reference-system catalogs) backed by SQLite modules
			// modernc.org/sqlite doesn't implement. They're not user data —
			// skip them rather than failing the whole schema read.
			continue
		}
		tables = append(tables, TableInfo{Name: name, Columns: cols})
	}
	return tables, nil
}

func readColumns(db *sql.DB, table string) ([]ColumnInfo, error) {
	rows, err := db.Query(fmt.Sprintf(`PRAGMA table_info(%q)`, table))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cols []ColumnInfo
	for rows.Next() {
		var (
			cid        int
			name       string
			ctype      string
			notNull    int
			dfltValue  sql.NullString
			primaryKey int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dfltValue, &primaryKey); err != nil {
			return nil, err
		}
		cols = append(cols, ColumnInfo{
			Name:         name,
			DeclaredType: ctype,
			NotNull:      notNull != 0,
			PrimaryKey:   primaryKey != 0,
		})
	}
	return cols, rows.Err()
}
