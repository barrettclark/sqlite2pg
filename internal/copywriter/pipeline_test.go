package copywriter

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"sqlite2pg/internal/config"
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

func TestTableSource_StreamsAllRowsWithTransformsApplied(t *testing.T) {
	db := openTestDB(t, `CREATE TABLE bikes (bike_id INTEGER, last_reported INTEGER, is_installed INTEGER);`)
	rows := [][3]int64{{1, 1620000000, 1}, {2, 1620003600, 0}, {3, 1620007200, 1}}
	for _, r := range rows {
		if _, err := db.Exec(`INSERT INTO bikes VALUES (?, ?, ?)`, r[0], r[1], r[2]); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	tc := config.TableConfig{
		ColumnOrder: []string{"bike_id", "last_reported", "is_installed"},
		Columns: map[string]config.ColumnConfig{
			"bike_id":       {TargetType: "integer"},
			"last_reported": {TargetType: "timestamptz", Transform: "unix_epoch_seconds"},
			"is_installed":  {TargetType: "boolean", Transform: "int_to_bool"},
		},
	}

	src := NewTableSource(db, "bikes", tc)

	var seen int
	for src.Next() {
		vals, err := src.Values()
		if err != nil {
			t.Fatalf("Values: %v", err)
		}
		if len(vals) != 3 {
			t.Fatalf("expected 3 values, got %d: %v", len(vals), vals)
		}
		if _, ok := vals[1].(interface{ Unix() int64 }); !ok {
			// last_reported should have been transformed to a time.Time
			t.Errorf("expected last_reported to be a time value, got %T", vals[1])
		}
		if _, ok := vals[2].(bool); !ok {
			t.Errorf("expected is_installed to be a bool, got %T", vals[2])
		}
		seen++
	}
	if err := src.Err(); err != nil {
		t.Fatalf("Err: %v", err)
	}
	if seen != len(rows) {
		t.Fatalf("expected to stream %d rows, got %d", len(rows), seen)
	}
}

func TestTableSource_OnRowFiresOncePerRowReturnedByNext(t *testing.T) {
	db := openTestDB(t, `CREATE TABLE bikes (bike_id INTEGER);`)
	for i := 0; i < 5; i++ {
		if _, err := db.Exec(`INSERT INTO bikes VALUES (?)`, i); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	tc := config.TableConfig{
		ColumnOrder: []string{"bike_id"},
		Columns:     map[string]config.ColumnConfig{"bike_id": {TargetType: "integer"}},
	}

	var calls int
	src := NewTableSource(db, "bikes", tc).OnRow(func() { calls++ })
	var seen int
	for src.Next() {
		seen++
	}
	if err := src.Err(); err != nil {
		t.Fatalf("Err: %v", err)
	}
	if calls != seen {
		t.Errorf("expected OnRow to fire once per row (%d), fired %d times", seen, calls)
	}
	if calls != 5 {
		t.Errorf("expected 5 calls, got %d", calls)
	}
}

func TestTableSource_OnRowIsOptional(t *testing.T) {
	db := openTestDB(t, `CREATE TABLE bikes (bike_id INTEGER);`)
	if _, err := db.Exec(`INSERT INTO bikes VALUES (1)`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	tc := config.TableConfig{
		ColumnOrder: []string{"bike_id"},
		Columns:     map[string]config.ColumnConfig{"bike_id": {TargetType: "integer"}},
	}

	// No OnRow registered — must not panic.
	src := NewTableSource(db, "bikes", tc)
	for src.Next() {
	}
	if err := src.Err(); err != nil {
		t.Fatalf("Err: %v", err)
	}
}

func TestTableSource_ExcludesDroppedColumns(t *testing.T) {
	db := openTestDB(t, `CREATE TABLE t (a INTEGER, shape BLOB);`)
	if _, err := db.Exec(`INSERT INTO t VALUES (1, x'00')`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	tc := config.TableConfig{
		ColumnOrder: []string{"a", "shape"},
		Columns: map[string]config.ColumnConfig{
			"a":     {TargetType: "integer"},
			"shape": {TargetType: "__drop__"},
		},
	}

	src := NewTableSource(db, "t", tc)
	if !src.Next() {
		t.Fatalf("expected at least one row, Err: %v", src.Err())
	}
	vals, err := src.Values()
	if err != nil {
		t.Fatalf("Values: %v", err)
	}
	if len(vals) != 1 {
		t.Fatalf("expected dropped column excluded, got %d values: %v", len(vals), vals)
	}
}

func TestTableSource_SurfacesTransformErrorsViaErr(t *testing.T) {
	db := openTestDB(t, `CREATE TABLE t (n TEXT);`)
	if _, err := db.Exec(`INSERT INTO t VALUES ('not-a-number')`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	tc := config.TableConfig{
		ColumnOrder: []string{"n"},
		Columns: map[string]config.ColumnConfig{
			"n": {TargetType: "integer", Transform: "strip_commas"},
		},
	}

	src := NewTableSource(db, "t", tc)
	for src.Next() {
		// drain
	}
	if src.Err() == nil {
		t.Fatal("expected a transform error to surface via Err()")
	}
}
