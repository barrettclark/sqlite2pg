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

	// PrimaryKeySeq is 0 if this column isn't part of the table's primary
	// key, or its 1-based position within it otherwise — SQLite reports
	// this directly via PRAGMA table_info's "pk" column, which is how a
	// composite primary key's declared column order is preserved.
	PrimaryKeySeq int
}

// ForeignKeyInfo describes one declared foreign key constraint, which may
// span multiple columns (a composite key) — SQLite's PRAGMA foreign_key_list
// groups the columns of one constraint under a shared "id" and orders them
// within it via "seq".
type ForeignKeyInfo struct {
	Columns    []string // local columns, in declared order
	RefTable   string
	RefColumns []string // referenced columns, in declared order (aligned with Columns)
	OnDelete   string
	OnUpdate   string
}

// TableInfo describes a table, its columns, and its declared foreign keys.
type TableInfo struct {
	Name        string
	Columns     []ColumnInfo
	ForeignKeys []ForeignKeyInfo
}

// ReadSchema reads every user table (excluding SQLite's own sqlite_%
// system tables), its columns via PRAGMA table_info, and its declared
// foreign keys via PRAGMA foreign_key_list.
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
		fks, err := ReadForeignKeys(db, name)
		if err != nil {
			return nil, fmt.Errorf("reading foreign keys for %s: %w", name, err)
		}
		tables = append(tables, TableInfo{Name: name, Columns: cols, ForeignKeys: fks})
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
			Name:          name,
			DeclaredType:  ctype,
			NotNull:       notNull != 0,
			PrimaryKeySeq: primaryKey,
		})
	}
	return cols, rows.Err()
}

// ReadForeignKeys reads table's declared foreign keys via PRAGMA
// foreign_key_list, grouping multi-column (composite) constraints by their
// shared id and ordering each constraint's columns by seq.
func ReadForeignKeys(db *sql.DB, table string) ([]ForeignKeyInfo, error) {
	rows, err := db.Query(fmt.Sprintf(`PRAGMA foreign_key_list(%q)`, table))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byID := map[int]*ForeignKeyInfo{}
	var order []int
	for rows.Next() {
		var (
			id, seq                     int
			refTable, from, to          string
			onUpdate, onDelete, matchOn string
		)
		if err := rows.Scan(&id, &seq, &refTable, &from, &to, &onUpdate, &onDelete, &matchOn); err != nil {
			return nil, err
		}
		fk, ok := byID[id]
		if !ok {
			fk = &ForeignKeyInfo{RefTable: refTable, OnDelete: onDelete, OnUpdate: onUpdate}
			byID[id] = fk
			order = append(order, id)
		}
		fk.Columns = append(fk.Columns, from)
		fk.RefColumns = append(fk.RefColumns, to)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	fks := make([]ForeignKeyInfo, 0, len(order))
	for _, id := range order {
		fks = append(fks, *byID[id])
	}
	return fks, nil
}
