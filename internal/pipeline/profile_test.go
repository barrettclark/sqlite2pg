package pipeline

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	_ "sqlite2pg/internal/profiler/heuristics" // self-registers into profiler.Default
)

func openTestDB(t *testing.T, ddl string) (*sql.DB, string) {
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
	return db, path
}

func TestProfileDatabase_AutoResolvesUnambiguousColumns(t *testing.T) {
	db, path := openTestDB(t, `
		CREATE TABLE bikes (
			bike_id INTEGER PRIMARY KEY,
			num_scooters_available INTEGER,
			last_reported INTEGER
		);
	`)
	for i := 0; i < 5; i++ {
		db.Exec(`INSERT INTO bikes (num_scooters_available, last_reported) VALUES (?, ?)`, i, 1620000000+i)
	}

	result, err := ProfileDatabase(db, path, 500, 0.9)
	if err != nil {
		t.Fatalf("ProfileDatabase: %v", err)
	}

	bikes := result.Config.Tables["bikes"]
	lastReported := bikes.Columns["last_reported"]
	if lastReported.TargetType != "timestamptz" {
		t.Errorf("expected last_reported to resolve to timestamptz, got %q", lastReported.TargetType)
	}

	numScooters := bikes.Columns["num_scooters_available"]
	if numScooters.TargetType != "integer" {
		t.Errorf("expected num_scooters_available to fall back to integer, got %q", numScooters.TargetType)
	}
	if numScooters.Source != "heuristic:default_passthrough" {
		t.Errorf("expected default_passthrough source, got %q", numScooters.Source)
	}
}

func TestProfileDatabase_FlagsAmbiguousBooleanColumnAsUnresolved(t *testing.T) {
	db, path := openTestDB(t, `
		CREATE TABLE bikes (
			bike_id INTEGER PRIMARY KEY,
			is_installed INTEGER
		);
	`)
	for _, v := range []int{0, 1, 1, 0} {
		db.Exec(`INSERT INTO bikes (is_installed) VALUES (?)`, v)
	}

	result, err := ProfileDatabase(db, path, 500, 0.9)
	if err != nil {
		t.Fatalf("ProfileDatabase: %v", err)
	}

	if len(result.Unresolved) != 1 {
		t.Fatalf("expected 1 unresolved case for the ambiguous boolean column, got %d: %+v", len(result.Unresolved), result.Unresolved)
	}
	if result.Unresolved[0].Column != "is_installed" {
		t.Errorf("expected unresolved case for is_installed, got %q", result.Unresolved[0].Column)
	}

	col := result.Config.Tables["bikes"].Columns["is_installed"]
	if col.TargetType != "boolean" {
		t.Errorf("expected draft decision to still be boolean (best guess), got %q", col.TargetType)
	}
	if col.Reviewed {
		t.Error("expected an unresolved column to be marked reviewed: false in the draft config")
	}
}
