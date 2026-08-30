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

	if byName["bike_id"].PrimaryKeySeq != 1 {
		t.Errorf("expected bike_id to be primary key position 1, got %d", byName["bike_id"].PrimaryKeySeq)
	}
	if byName["last_reported"].PrimaryKeySeq != 0 {
		t.Errorf("expected last_reported to not be a primary key, got seq %d", byName["last_reported"].PrimaryKeySeq)
	}
	if byName["last_reported"].DeclaredType != "INTEGER" {
		t.Errorf("expected last_reported declared type INTEGER, got %q", byName["last_reported"].DeclaredType)
	}
	if !byName["is_installed"].NotNull {
		t.Error("expected is_installed to be marked NOT NULL")
	}
}

func TestReadSchema_ReturnsCompositePrimaryKeyInDeclaredOrder(t *testing.T) {
	db := openTestDB(t, `
		CREATE TABLE playlist_track (
			playlist_id INTEGER NOT NULL,
			track_id INTEGER NOT NULL,
			PRIMARY KEY (playlist_id, track_id)
		);
	`)

	tables, err := ReadSchema(db)
	if err != nil {
		t.Fatalf("ReadSchema: %v", err)
	}
	byName := map[string]ColumnInfo{}
	for _, c := range tables[0].Columns {
		byName[c.Name] = c
	}
	if byName["playlist_id"].PrimaryKeySeq != 1 {
		t.Errorf("expected playlist_id primary key position 1, got %d", byName["playlist_id"].PrimaryKeySeq)
	}
	if byName["track_id"].PrimaryKeySeq != 2 {
		t.Errorf("expected track_id primary key position 2, got %d", byName["track_id"].PrimaryKeySeq)
	}
}

func TestReadForeignKeys_ReturnsSingleColumnForeignKeys(t *testing.T) {
	db := openTestDB(t, `
		CREATE TABLE cities (city_id INTEGER PRIMARY KEY);
		CREATE TABLE addresses (
			address_id INTEGER PRIMARY KEY,
			city_id INTEGER,
			FOREIGN KEY (city_id) REFERENCES cities(city_id) ON DELETE CASCADE
		);
	`)

	fks, err := ReadForeignKeys(db, "addresses")
	if err != nil {
		t.Fatalf("ReadForeignKeys: %v", err)
	}
	if len(fks) != 1 {
		t.Fatalf("expected 1 foreign key, got %d: %+v", len(fks), fks)
	}
	fk := fks[0]
	if fk.RefTable != "cities" {
		t.Errorf("expected ref table cities, got %q", fk.RefTable)
	}
	if len(fk.Columns) != 1 || fk.Columns[0] != "city_id" {
		t.Errorf("expected local column [city_id], got %v", fk.Columns)
	}
	if len(fk.RefColumns) != 1 || fk.RefColumns[0] != "city_id" {
		t.Errorf("expected ref column [city_id], got %v", fk.RefColumns)
	}
	if fk.OnDelete != "CASCADE" {
		t.Errorf("expected ON DELETE CASCADE, got %q", fk.OnDelete)
	}
}

func TestReadForeignKeys_GroupsCompositeForeignKeysByID(t *testing.T) {
	db := openTestDB(t, `
		CREATE TABLE parents (a INTEGER, b INTEGER, PRIMARY KEY (a, b));
		CREATE TABLE children (
			x INTEGER,
			y INTEGER,
			FOREIGN KEY (x, y) REFERENCES parents(a, b)
		);
	`)

	fks, err := ReadForeignKeys(db, "children")
	if err != nil {
		t.Fatalf("ReadForeignKeys: %v", err)
	}
	if len(fks) != 1 {
		t.Fatalf("expected 1 composite foreign key, got %d: %+v", len(fks), fks)
	}
	fk := fks[0]
	if len(fk.Columns) != 2 || fk.Columns[0] != "x" || fk.Columns[1] != "y" {
		t.Errorf("expected local columns [x y] in seq order, got %v", fk.Columns)
	}
	if len(fk.RefColumns) != 2 || fk.RefColumns[0] != "a" || fk.RefColumns[1] != "b" {
		t.Errorf("expected ref columns [a b] in seq order, got %v", fk.RefColumns)
	}
}

func TestReadForeignKeys_ResolvesImplicitReferenceToParentPrimaryKey(t *testing.T) {
	db := openTestDB(t, `
		CREATE TABLE blob (rid INTEGER PRIMARY KEY);
		CREATE TABLE delta (
			srcid INTEGER NOT NULL REFERENCES blob
		);
	`)

	fks, err := ReadForeignKeys(db, "delta")
	if err != nil {
		t.Fatalf("ReadForeignKeys: %v", err)
	}
	if len(fks) != 1 {
		t.Fatalf("expected 1 foreign key, got %d: %+v", len(fks), fks)
	}
	fk := fks[0]
	if fk.RefTable != "blob" {
		t.Errorf("expected ref table blob, got %q", fk.RefTable)
	}
	if len(fk.Columns) != 1 || fk.Columns[0] != "srcid" {
		t.Errorf("expected local column [srcid], got %v", fk.Columns)
	}
	if len(fk.RefColumns) != 1 || fk.RefColumns[0] != "rid" {
		t.Errorf("expected ref column [rid] (resolved from blob's primary key), got %v", fk.RefColumns)
	}
}

func TestReadForeignKeys_ReturnsEmptyForTableWithNoForeignKeys(t *testing.T) {
	db := openTestDB(t, `CREATE TABLE standalone (id INTEGER PRIMARY KEY);`)
	fks, err := ReadForeignKeys(db, "standalone")
	if err != nil {
		t.Fatalf("ReadForeignKeys: %v", err)
	}
	if len(fks) != 0 {
		t.Errorf("expected no foreign keys, got %+v", fks)
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
