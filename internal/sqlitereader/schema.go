// Package sqlitereader reads schema and streams row data from a SQLite
// source database using modernc.org/sqlite (pure Go, no CGO — keeps the
// tool's single-static-binary distribution story intact).
package sqlitereader

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// quoteIdent double-quotes name as a SQL identifier, doubling any embedded
// double quotes per SQL's identifier-quoting rule (e.g. `a"b` becomes
// `"a""b"`) — the same convention SQLite itself uses for quoted identifiers.
// This must be used for every source table/column name interpolated into a
// query string here: Go's fmt.Sprintf("%q", name) backslash-escapes like a
// Go string literal, which SQLite's parser rejects outright for a name
// containing a double quote (issue #39 — the SQLite-side counterpart of
// issue #26's Postgres DDL fix; see internal/ddl/identifiers.go's quoteIdent
// for the analogous Postgres helper).
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

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

// SkippedTable records a table ReadSchema deliberately left out of its
// result because reading it hit the one error it recognizes as a genuinely
// unsupported (not fatal) case: a virtual table backed by a SQLite module
// modernc.org/sqlite doesn't implement. Reason preserves the underlying
// driver error so a caller can report exactly what was skipped and why,
// rather than the skip being invisible (issue #29).
type SkippedTable struct {
	Name   string
	Reason string
}

// SkippedForeignKey records one declared foreign key ReadForeignKeys
// deliberately dropped rather than resolved, because it's an implicit
// (column-list-less) `REFERENCES parent` clause whose target column
// couldn't be determined — either RefTable doesn't have exactly one
// declared primary key column (a plain rowid table with no PRIMARY KEY at
// all is perfectly valid SQLite, and SQLite itself doesn't enforce such an
// implicit reference either), or RefTable is itself backed by an
// unsupported virtual table module (issue #29's case, reached indirectly
// here). Reason preserves the underlying cause so the drop is visible
// rather than silent (issue #46).
type SkippedForeignKey struct {
	Table    string
	RefTable string
	Reason   string
}

// ambiguousPrimaryKeyError reports that primaryKeyColumn's target table
// doesn't have exactly one declared primary key column. This is a normal,
// expected table shape (not a driver failure), so it's a distinct type
// rather than a plain fmt.Errorf — callers use errors.As to recognize it
// and degrade gracefully instead of treating it as fatal.
type ambiguousPrimaryKeyError struct {
	table string
	count int
}

func (e *ambiguousPrimaryKeyError) Error() string {
	return fmt.Sprintf("table %s has %d primary key columns, expected exactly 1", e.table, e.count)
}

// unsupportedVirtualTableModuleMarker is the substring modernc.org/sqlite
// includes in the error returned for a CREATE VIRTUAL TABLE (or any later
// access, like PRAGMA table_info) that names a module it has no
// implementation for — e.g. "SQL logic error: no such module: some_module
// (1)". This is the one column-read failure ReadSchema treats as an
// intentional skip; verified directly against modernc.org/sqlite v1.56.0 by
// creating a virtual table with a bogus module name and inspecting the
// resulting error text (SQLite reports this case with the generic
// SQLITE_ERROR code, so the error code alone can't distinguish it from any
// other failure — only this message substring can).
const unsupportedVirtualTableModuleMarker = "no such module:"

// isUnsupportedVirtualTableModuleError reports whether err is the specific
// "no such module" failure a genuinely-unsupported virtual table produces,
// as opposed to any other column-read failure (a locked database,
// corruption, a permissions error) that must not be silently swallowed.
func isUnsupportedVirtualTableModuleError(err error) bool {
	return err != nil && strings.Contains(err.Error(), unsupportedVirtualTableModuleMarker)
}

// ReadSchema reads every user table (excluding SQLite's own sqlite_%
// system tables), its columns via PRAGMA table_info, and its declared
// foreign keys via PRAGMA foreign_key_list.
//
// A table whose column read fails because it's backed by a SQLite virtual
// table module modernc.org/sqlite doesn't implement (some FGDB/Spatialite
// exports declare these for spatial indexes and reference-system catalogs,
// which aren't user data) is skipped rather than failing the whole schema
// read, and reported back via the returned []SkippedTable so the skip is
// visible to the caller rather than silent. Any other column-read failure
// (a locked database, disk corruption, a permissions error) aborts with an
// error instead — issue #29: it must never look like a clean, complete
// migration when a table went unread for an unrelated reason.
func ReadSchema(db *sql.DB) ([]TableInfo, []SkippedTable, []SkippedForeignKey, error) {
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("listing tables: %w", err)
	}
	defer rows.Close()

	var tableNames []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, nil, nil, fmt.Errorf("scanning table name: %w", err)
		}
		tableNames = append(tableNames, name)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, nil, err
	}

	var tables []TableInfo
	var skipped []SkippedTable
	var skippedFKs []SkippedForeignKey
	for _, name := range tableNames {
		cols, err := readColumns(db, name)
		if err != nil {
			if isUnsupportedVirtualTableModuleError(err) {
				skipped = append(skipped, SkippedTable{Name: name, Reason: err.Error()})
				continue
			}
			return nil, nil, nil, fmt.Errorf("reading columns for %s: %w", name, err)
		}
		fks, fkSkipped, err := ReadForeignKeys(db, name)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("reading foreign keys for %s: %w", name, err)
		}
		skippedFKs = append(skippedFKs, fkSkipped...)
		tables = append(tables, TableInfo{Name: name, Columns: cols, ForeignKeys: fks})
	}
	return tables, skipped, skippedFKs, nil
}

func readColumns(db *sql.DB, table string) ([]ColumnInfo, error) {
	rows, err := db.Query(fmt.Sprintf(`PRAGMA table_info(%s)`, quoteIdent(table)))
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
func ReadForeignKeys(db *sql.DB, table string) ([]ForeignKeyInfo, []SkippedForeignKey, error) {
	rows, err := db.Query(fmt.Sprintf(`PRAGMA foreign_key_list(%s)`, quoteIdent(table)))
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	byID := map[int]*ForeignKeyInfo{}
	var order []int
	var skipped []SkippedForeignKey
	// skippedRefTables tracks a referenced table whose primary key already
	// failed to resolve once in this call, so a second FK row targeting the
	// same table doesn't repeat the same lookup (and the same skip record).
	skippedRefTables := map[string]bool{}
	// refPKCache memoizes each referenced table's primary key column name,
	// looked up only when an implicit (column-list-less) REFERENCES clause
	// needs it — most foreign keys declare their target column explicitly.
	refPKCache := map[string]string{}
	for rows.Next() {
		var (
			id, seq                     int
			refTable, from              string
			to                          sql.NullString
			onUpdate, onDelete, matchOn string
		)
		if err := rows.Scan(&id, &seq, &refTable, &from, &to, &onUpdate, &onDelete, &matchOn); err != nil {
			return nil, nil, err
		}
		toCol := to.String
		if !to.Valid {
			// SQLite allows an implicit-column REFERENCES clause, which
			// refers to the parent table's declared primary key rather
			// than naming a column explicitly. PRAGMA foreign_key_list
			// reports this case with a NULL "to" — resolve it ourselves.
			if skippedRefTables[refTable] {
				continue
			}
			pk, ok := refPKCache[refTable]
			if !ok {
				var err error
				pk, err = primaryKeyColumn(db, refTable)
				if err != nil {
					// Neither of primaryKeyColumn's failure modes — the
					// referenced table not having exactly one declared
					// primary key column (a plain rowid table with no
					// PRIMARY KEY at all is perfectly valid SQLite), or
					// the referenced table being backed by a SQLite
					// virtual table module this tool doesn't implement
					// (issue #29's case, reached indirectly here via
					// readColumns) — is a driver-level failure. Both are
					// expected, recoverable shapes: drop just this one FK
					// relationship rather than aborting the whole read
					// (issue #46). Anything else (a locked database,
					// corruption) still aborts.
					var ambiguous *ambiguousPrimaryKeyError
					if errors.As(err, &ambiguous) || isUnsupportedVirtualTableModuleError(err) {
						skipped = append(skipped, SkippedForeignKey{Table: table, RefTable: refTable, Reason: err.Error()})
						skippedRefTables[refTable] = true
						continue
					}
					return nil, nil, fmt.Errorf("resolving implicit foreign key reference to %s: %w", refTable, err)
				}
				refPKCache[refTable] = pk
			}
			toCol = pk
		}
		fk, ok := byID[id]
		if !ok {
			fk = &ForeignKeyInfo{RefTable: refTable, OnDelete: onDelete, OnUpdate: onUpdate}
			byID[id] = fk
			order = append(order, id)
		}
		fk.Columns = append(fk.Columns, from)
		fk.RefColumns = append(fk.RefColumns, toCol)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	fks := make([]ForeignKeyInfo, 0, len(order))
	for _, id := range order {
		fks = append(fks, *byID[id])
	}
	return fks, skipped, nil
}

// primaryKeyColumn returns table's single declared primary key column name,
// for resolving an implicit-column REFERENCES clause. SQLite only permits
// such a clause to target a table with exactly one primary key column.
func primaryKeyColumn(db *sql.DB, table string) (string, error) {
	cols, err := readColumns(db, table)
	if err != nil {
		return "", err
	}
	var pkCols []string
	for _, c := range cols {
		if c.PrimaryKeySeq > 0 {
			pkCols = append(pkCols, c.Name)
		}
	}
	if len(pkCols) != 1 {
		return "", &ambiguousPrimaryKeyError{table: table, count: len(pkCols)}
	}
	return pkCols[0], nil
}
