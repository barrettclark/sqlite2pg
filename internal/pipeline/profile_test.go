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

// TestProfileDatabase_RescuesASparseColumnMissedByRandomSampling is a
// regression test for a real bug: a column that's almost entirely NULL
// can easily draw zero non-NULL values in a bounded random sample, which
// starves any heuristic of the evidence it needs and silently falls
// through to the declared-type passthrough instead of being flagged —
// found for real against an Esri geodatabase fixture where a realdate
// column was 99.5% NULL. 500,000 rows with exactly one non-NULL value
// gives a plain 500-row random sample only a 0.1% chance of including it
// — virtually certain to require the rescue query this test is really
// checking for. (Confirmed by temporarily reverting the rescue and
// re-running: it failed, as expected, before being restored.)
func TestProfileDatabase_RescuesASparseColumnMissedByRandomSampling(t *testing.T) {
	db, path := openTestDB(t, `
		CREATE TABLE registry (
			id INTEGER PRIMARY KEY,
			creation_date INTEGER
		);
	`)
	const total = 500000
	// A recursive CTE generates all the NULL rows in one statement —
	// 500,000 individual inserts would make this test unreasonably slow.
	_, err := db.Exec(`
		WITH RECURSIVE seq(n) AS (
			SELECT 1
			UNION ALL
			SELECT n + 1 FROM seq WHERE n < ?
		)
		INSERT INTO registry (creation_date) SELECT NULL FROM seq;
	`, total-1)
	if err != nil {
		t.Fatalf("bulk insert NULLs: %v", err)
	}
	// The one row with a real, validly-formatted YYYYMMDD value.
	if _, err := db.Exec(`INSERT INTO registry (creation_date) VALUES (20210927)`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	result, err := ProfileDatabase(db, path, 500, 0.9)
	if err != nil {
		t.Fatalf("ProfileDatabase: %v", err)
	}

	col := result.Config.Tables["registry"].Columns["creation_date"]
	if col.TargetType != "date" || col.Source != "heuristic:yyyymmdd_date" {
		t.Errorf("expected creation_date to resolve to date via yyyymmdd_date despite being almost entirely NULL, got %q via %q", col.TargetType, col.Source)
	}
}
