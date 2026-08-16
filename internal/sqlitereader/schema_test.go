package sqlitereader

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T, ddl string) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(ddl); err != nil {
		t.Fatalf("exec ddl: %v", err)
	}
	return db
}

func TestReadSchema_ReturnsTablesAndColumns(t *testing.T) {
	db := openTestDB(t, `
		CREATE TABLE bikes (
			bike_id INTEGER PRIMARY KEY,
			last_reported INTEGER,
			is_installed INTEGER NOT NULL
		);
	`)

	tables, err := ReadSchema(db)
	if err != nil {
		t.Fatalf("ReadSchema: %v", err)
	}
	if len(tables) != 1 {
		t.Fatalf("expected 1 table, got %d: %+v", len(tables), tables)
	}
	tbl := tables[0]
	if tbl.Name != "bikes" {
		t.Errorf("expected table name bikes, got %q", tbl.Name)
	}
	if len(tbl.Columns) != 3 {
		t.Fatalf("expected 3 columns, got %d: %+v", len(tbl.Columns), tbl.Columns)
	}

	byName := map[string]ColumnInfo{}
	for _, c := range tbl.Columns {
		byName[c.Name] = c
	}

	if !byName["bike_id"].PrimaryKey {
		t.Error("expected bike_id to be marked as primary key")
	}
	if byName["last_reported"].DeclaredType != "INTEGER" {
		t.Errorf("expected last_reported declared type INTEGER, got %q", byName["last_reported"].DeclaredType)
	}
	if !byName["is_installed"].NotNull {
		t.Error("expected is_installed to be marked NOT NULL")
	}
}

func TestReadSchema_SkipsSQLiteSystemTables(t *testing.T) {
	db := openTestDB(t, `
		CREATE TABLE widgets (id INTEGER PRIMARY KEY AUTOINCREMENT);
	`)
	if _, err := db.Exec(`INSERT INTO widgets DEFAULT VALUES`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	tables, err := ReadSchema(db)
	if err != nil {
		t.Fatalf("ReadSchema: %v", err)
	}
	for _, tbl := range tables {
		if tbl.Name == "sqlite_sequence" {
			t.Errorf("expected sqlite_sequence to be excluded, got tables: %+v", tables)
		}
	}
}
