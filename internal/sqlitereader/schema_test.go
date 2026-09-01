package sqlitereader

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	modernc "modernc.org/sqlite"
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

	tables, _, _, err := ReadSchema(db)
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

	tables, _, _, err := ReadSchema(db)
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

	fks, _, err := ReadForeignKeys(db, "addresses")
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

	fks, _, err := ReadForeignKeys(db, "children")
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

	fks, skipped, err := ReadForeignKeys(db, "delta")
	if err != nil {
		t.Fatalf("ReadForeignKeys: %v", err)
	}
	if len(skipped) != 0 {
		t.Fatalf("expected no skipped foreign keys, got %+v", skipped)
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
	fks, _, err := ReadForeignKeys(db, "standalone")
	if err != nil {
		t.Fatalf("ReadForeignKeys: %v", err)
	}
	if len(fks) != 0 {
		t.Errorf("expected no foreign keys, got %+v", fks)
	}
}

// --- issue #46: implicit-FK primary-key resolution must degrade, not abort ----
//
// primaryKeyColumn (issue #17) returns a hard error whenever the referenced
// table doesn't have exactly one declared primary key column. That's a
// legitimate, common SQLite shape (a plain rowid table with no declared
// PRIMARY KEY at all), not a driver failure — so ReadForeignKeys must drop
// just the one FK relationship it can't resolve, not abort the whole call
// and, transitively, the whole `migrate profile` run.

func TestReadForeignKeys_DropsImplicitReferenceWhenParentHasNoPrimaryKey(t *testing.T) {
	db := openTestDB(t, `
		CREATE TABLE parent (name TEXT);
		CREATE TABLE child (
			parent_id INTEGER REFERENCES parent
		);
	`)

	fks, skipped, err := ReadForeignKeys(db, "child")
	if err != nil {
		t.Fatalf("ReadForeignKeys: expected no error (should degrade gracefully), got: %v", err)
	}
	if len(fks) != 0 {
		t.Errorf("expected the unresolvable FK to be dropped, got: %+v", fks)
	}
	if len(skipped) != 1 {
		t.Fatalf("expected 1 skipped foreign key, got %d: %+v", len(skipped), skipped)
	}
	if skipped[0].Table != "child" {
		t.Errorf("expected skipped FK table child, got %q", skipped[0].Table)
	}
	if skipped[0].RefTable != "parent" {
		t.Errorf("expected skipped FK ref table parent, got %q", skipped[0].RefTable)
	}
	if !strings.Contains(skipped[0].Reason, "0 primary key columns") {
		t.Errorf("expected skipped FK reason to explain the 0-primary-key-columns cause, got %q", skipped[0].Reason)
	}
}

func TestReadForeignKeys_DropsImplicitReferenceWhenParentHasCompositePrimaryKey(t *testing.T) {
	db := openTestDB(t, `
		CREATE TABLE parent (a INTEGER, b INTEGER, PRIMARY KEY (a, b));
		CREATE TABLE child (
			parent_id INTEGER REFERENCES parent
		);
	`)

	fks, skipped, err := ReadForeignKeys(db, "child")
	if err != nil {
		t.Fatalf("ReadForeignKeys: expected no error (should degrade gracefully), got: %v", err)
	}
	if len(fks) != 0 {
		t.Errorf("expected the unresolvable FK to be dropped, got: %+v", fks)
	}
	if len(skipped) != 1 {
		t.Fatalf("expected 1 skipped foreign key, got %d: %+v", len(skipped), skipped)
	}
}

func TestReadForeignKeys_DropsImplicitReferenceWhenParentIsUnsupportedVirtualTable(t *testing.T) {
	// If the referenced table is itself a virtual table backed by a module
	// ReadSchema would skip (issue #29), primaryKeyColumn's readColumns
	// call hits the identical "no such module" error. That must degrade
	// the same way — drop just this FK — rather than surfacing as a plain
	// fatal error out of ReadForeignKeys, which would silently defeat #29's
	// skip-and-report mechanism for a table that isn't even the one being
	// read directly.
	injectedErr := errors.New(`SQL logic error: no such module: some_esoteric_module (1)`)
	db := openFailingDB(t, `
		CREATE TABLE weird_spatial_index (id INTEGER PRIMARY KEY);
		CREATE TABLE child (
			parent_id INTEGER REFERENCES weird_spatial_index
		);
	`, `table_info("weird_spatial_index")`, injectedErr)

	fks, skipped, err := ReadForeignKeys(db, "child")
	if err != nil {
		t.Fatalf("ReadForeignKeys: expected no error (should degrade gracefully), got: %v", err)
	}
	if len(fks) != 0 {
		t.Errorf("expected the unresolvable FK to be dropped, got: %+v", fks)
	}
	if len(skipped) != 1 {
		t.Fatalf("expected 1 skipped foreign key, got %d: %+v", len(skipped), skipped)
	}
	if skipped[0].RefTable != "weird_spatial_index" {
		t.Errorf("expected skipped FK ref table weird_spatial_index, got %q", skipped[0].RefTable)
	}
	if !strings.Contains(skipped[0].Reason, "no such module") {
		t.Errorf("expected skipped FK reason to preserve the underlying driver error, got %q", skipped[0].Reason)
	}
}

func TestReadSchema_SkipsSQLiteSystemTables(t *testing.T) {
	db := openTestDB(t, `
		CREATE TABLE widgets (id INTEGER PRIMARY KEY AUTOINCREMENT);
	`)
	if _, err := db.Exec(`INSERT INTO widgets DEFAULT VALUES`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	tables, _, _, err := ReadSchema(db)
	if err != nil {
		t.Fatalf("ReadSchema: %v", err)
	}
	for _, tbl := range tables {
		if tbl.Name == "sqlite_sequence" {
			t.Errorf("expected sqlite_sequence to be excluded, got tables: %+v", tables)
		}
	}
}

// --- error-path tests (issue #29) -------------------------------------
//
// ReadSchema used to drop any table whose column read failed for ANY
// reason with a bare `continue` — silently swallowing a locked database or
// corruption alongside the one case that's actually meant to be skipped
// (an unimplemented virtual table module). These tests inject a specific
// error via a wrapping driver.Conn, rather than relying on a real corrupt
// fixture, to reliably reproduce both paths.

// failingConn wraps a real modernc.org/sqlite connection, returning a
// canned error for any query containing marker instead of running it —
// simulating a column-read failure at an exact, reproducible point (e.g.
// "PRAGMA table_info" for one specific table) without needing a genuinely
// corrupt or locked database file.
type failingConn struct {
	driver.Conn
	marker string
	err    error
}

func (c *failingConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if strings.Contains(query, c.marker) {
		return nil, c.err
	}
	qc, ok := c.Conn.(driver.QueryerContext)
	if !ok {
		return nil, errors.New("underlying conn does not implement driver.QueryerContext")
	}
	return qc.QueryContext(ctx, query, args)
}

type failingDriver struct {
	marker string
	err    error
}

func (d *failingDriver) Open(name string) (driver.Conn, error) {
	c, err := (&modernc.Driver{}).Open(name)
	if err != nil {
		return nil, err
	}
	return &failingConn{Conn: c, marker: d.marker, err: d.err}, nil
}

var failingDriverSeq int64

// openFailingDB opens a fresh SQLite database (with ddl applied) through a
// driver that fails any query containing marker with err, letting a test
// reproduce an exact column-read failure deterministically.
func openFailingDB(t *testing.T, ddl, marker string, injectedErr error) *sql.DB {
	t.Helper()
	name := fmt.Sprintf("sqlite-failing-%d", atomic.AddInt64(&failingDriverSeq, 1))
	sql.Register(name, &failingDriver{marker: marker, err: injectedErr})

	path := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open(name, path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(ddl); err != nil {
		t.Fatalf("exec ddl: %v", err)
	}
	return db
}

func TestReadSchema_AbortsOnGenericColumnReadError(t *testing.T) {
	// A generic failure — standing in for a locked database, "database
	// disk image is malformed" corruption, or a permissions error — must
	// abort ReadSchema with a clear error, not silently drop the table.
	injectedErr := errors.New("disk I/O error")
	db := openFailingDB(t, `
		CREATE TABLE widgets (id INTEGER PRIMARY KEY);
	`, `table_info("widgets")`, injectedErr)

	tables, skipped, _, err := ReadSchema(db)
	if err == nil {
		t.Fatalf("expected ReadSchema to return an error, got tables=%+v skipped=%+v", tables, skipped)
	}
	if !strings.Contains(err.Error(), "widgets") {
		t.Errorf("expected error to mention the failing table %q, got: %v", "widgets", err)
	}
	if !strings.Contains(err.Error(), injectedErr.Error()) {
		t.Errorf("expected error to wrap the underlying cause %q, got: %v", injectedErr, err)
	}
}

func TestReadSchema_SkipsUnsupportedVirtualTableModuleButReportsIt(t *testing.T) {
	// The one column-read failure ReadSchema is meant to treat as an
	// intentional skip: a virtual table backed by a SQLite module
	// modernc.org/sqlite doesn't implement. It must still be skipped (so a
	// genuinely unsupported table doesn't fail the whole schema read), but
	// the skip must now be visible via the returned []SkippedTable rather
	// than silently dropped.
	injectedErr := errors.New(`SQL logic error: no such module: some_esoteric_module (1)`)
	db := openFailingDB(t, `
		CREATE TABLE widgets (id INTEGER PRIMARY KEY);
		CREATE TABLE weird_spatial_index (id INTEGER PRIMARY KEY);
	`, `table_info("weird_spatial_index")`, injectedErr)

	tables, skipped, _, err := ReadSchema(db)
	if err != nil {
		t.Fatalf("ReadSchema: %v", err)
	}
	if len(tables) != 1 || tables[0].Name != "widgets" {
		t.Fatalf("expected only widgets to be returned, got: %+v", tables)
	}
	if len(skipped) != 1 {
		t.Fatalf("expected 1 skipped table, got %d: %+v", len(skipped), skipped)
	}
	if skipped[0].Name != "weird_spatial_index" {
		t.Errorf("expected skipped table name weird_spatial_index, got %q", skipped[0].Name)
	}
	if !strings.Contains(skipped[0].Reason, "no such module") {
		t.Errorf("expected skipped table reason to preserve the underlying error, got %q", skipped[0].Reason)
	}
}

func TestReadSchema_ReadsRealRtreeVirtualTableWithoutSkippingIt(t *testing.T) {
	// Regression guard: modernc.org/sqlite genuinely implements the rtree
	// module (unlike the FGDB/Spatialite-specific modules this package
	// intentionally skips), so an R-tree virtual table — and its shadow
	// tables, which are ordinary tables SQLite creates alongside it — must
	// load normally, never land in SkippedTable.
	db := openTestDB(t, `CREATE VIRTUAL TABLE rt USING rtree(id, minX, maxX);`)

	tables, skipped, _, err := ReadSchema(db)
	if err != nil {
		t.Fatalf("ReadSchema: %v", err)
	}
	if len(skipped) != 0 {
		t.Errorf("expected no skipped tables for a real rtree virtual table, got: %+v", skipped)
	}
	found := false
	for _, tbl := range tables {
		if tbl.Name == "rt" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected rt (and its shadow tables) to be read normally, got: %+v", tables)
	}
}

func TestReadSchema_HandlesTableAndColumnNamesWithEmbeddedDoubleQuote(t *testing.T) {
	db := openTestDB(t, `
		CREATE TABLE "stats ""weird""" (
			id INTEGER PRIMARY KEY,
			"Total ""Disability"" Recipients" INTEGER
		);
	`)

	tables, _, _, err := ReadSchema(db)
	if err != nil {
		t.Fatalf("ReadSchema: %v", err)
	}
	if len(tables) != 1 {
		t.Fatalf("expected 1 table, got %d: %+v", len(tables), tables)
	}
	if tables[0].Name != `stats "weird"` {
		t.Fatalf("expected table name with embedded quote, got %q", tables[0].Name)
	}

	var found bool
	for _, c := range tables[0].Columns {
		if c.Name == `Total "Disability" Recipients` {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected column with embedded quote to be read, got columns: %+v", tables[0].Columns)
	}
}

func TestReadSchema_DropsUnresolvableImplicitForeignKeyRatherThanAborting(t *testing.T) {
	// Issue #46: an implicit `REFERENCES parent` where parent is an
	// ordinary rowid table with no declared PRIMARY KEY (perfectly valid
	// SQLite) must not abort the whole ReadSchema call — only that one FK
	// relationship is dropped, reported via SkippedForeignKey, and every
	// table's columns still come back normally.
	db := openTestDB(t, `
		CREATE TABLE parent (name TEXT);
		CREATE TABLE child (
			id INTEGER PRIMARY KEY,
			parent_id INTEGER REFERENCES parent
		);
	`)

	tables, skippedTables, skippedFKs, err := ReadSchema(db)
	if err != nil {
		t.Fatalf("ReadSchema: expected no error (should degrade gracefully), got: %v", err)
	}
	if len(skippedTables) != 0 {
		t.Errorf("expected no skipped tables, got: %+v", skippedTables)
	}
	if len(skippedFKs) != 1 {
		t.Fatalf("expected 1 skipped foreign key, got %d: %+v", len(skippedFKs), skippedFKs)
	}
	if skippedFKs[0].Table != "child" || skippedFKs[0].RefTable != "parent" {
		t.Errorf("expected skipped FK child->parent, got: %+v", skippedFKs[0])
	}

	var child TableInfo
	for _, tbl := range tables {
		if tbl.Name == "child" {
			child = tbl
		}
	}
	if len(child.Columns) != 2 {
		t.Fatalf("expected child's columns to still be read normally, got: %+v", child.Columns)
	}
	if len(child.ForeignKeys) != 0 {
		t.Errorf("expected child's unresolvable FK to be absent, got: %+v", child.ForeignKeys)
	}
}
