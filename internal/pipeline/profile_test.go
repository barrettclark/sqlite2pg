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

// TestProfileDatabase_PersistsFilteredSystemTablesIntoTheConfig is a
// regression test for issue #51: FilterSystemTables's result was only ever
// surfaced as a one-time stderr warning at profile time (issue #35), never
// persisted into the generated config — unlike SkippedTables (#29) and
// SkippedForeignKeys (#46), which exist so a human reviewing the config
// later, or `migrate load` running non-interactively in CI where the
// original stderr is gone, can still see what was left out and why.
func TestProfileDatabase_PersistsFilteredSystemTablesIntoTheConfig(t *testing.T) {
	db, path := openTestDB(t, `
		CREATE TABLE GDB_SystemCatalog (id INTEGER PRIMARY KEY);
		CREATE TABLE roads (road_id INTEGER PRIMARY KEY, name TEXT);
	`)

	result, err := ProfileDatabase(db, path, 500, 0.9)
	if err != nil {
		t.Fatalf("ProfileDatabase: %v", err)
	}

	if len(result.Config.FilteredSystemTables) != 1 || result.Config.FilteredSystemTables[0].Name != "GDB_SystemCatalog" {
		t.Errorf("expected config.FilteredSystemTables to list GDB_SystemCatalog, got %+v", result.Config.FilteredSystemTables)
	}
	if _, ok := result.Config.Tables["GDB_SystemCatalog"]; ok {
		t.Error("expected GDB_SystemCatalog to be filtered out of Tables")
	}
	if _, ok := result.Config.Tables["roads"]; !ok {
		t.Error("expected roads to remain in Tables")
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
func TestProfileDatabase_FlagsVaryingVARCHARLengthsAsAReviewableSuggestion(t *testing.T) {
	// first_name VARCHAR(45) and city VARCHAR(100) don't share a length —
	// evidence the lengths were chosen deliberately (real MySQL-origin
	// schema), not one mechanical export default applied to every text
	// column. Each should be suggested as varchar(N), flagged for review
	// rather than auto-applied.
	db, path := openTestDB(t, `
		CREATE TABLE customers (
			id INTEGER PRIMARY KEY,
			first_name VARCHAR(45),
			city VARCHAR(100)
		);
	`)
	for i := 0; i < 5; i++ {
		db.Exec(`INSERT INTO customers (first_name, city) VALUES (?, ?)`, "Alex", "Springfield")
	}

	result, err := ProfileDatabase(db, path, 500, 0.9)
	if err != nil {
		t.Fatalf("ProfileDatabase: %v", err)
	}

	customers := result.Config.Tables["customers"]

	firstName := customers.Columns["first_name"]
	if firstName.TargetType != "varchar(45)" {
		t.Errorf("expected first_name suggested as varchar(45), got %q", firstName.TargetType)
	}
	if firstName.Reviewed {
		t.Error("expected first_name to be flagged for review, not auto-applied")
	}

	city := customers.Columns["city"]
	if city.TargetType != "varchar(100)" {
		t.Errorf("expected city suggested as varchar(100), got %q", city.TargetType)
	}

	found := false
	for _, u := range result.Unresolved {
		if u.Table == "customers" && u.Column == "first_name" {
			found = true
		}
	}
	if !found {
		t.Error("expected first_name to appear in Unresolved")
	}
}

func TestProfileDatabase_TreatsUniformVARCHARLengthAcrossATableAsAMechanicalDefault(t *testing.T) {
	// Every VARCHAR column in this table shares the same length — the
	// hallmark of a blanket export default (e.g. every text column
	// declared VARCHAR(8000) regardless of content), not a real
	// constraint. Both columns should fall back to text, unflagged.
	db, path := openTestDB(t, `
		CREATE TABLE widgets (
			id INTEGER PRIMARY KEY,
			name VARCHAR(8000),
			description VARCHAR(8000)
		);
	`)
	for i := 0; i < 5; i++ {
		db.Exec(`INSERT INTO widgets (name, description) VALUES (?, ?)`, "Widget", "A widget")
	}

	result, err := ProfileDatabase(db, path, 500, 0.9)
	if err != nil {
		t.Fatalf("ProfileDatabase: %v", err)
	}

	widgets := result.Config.Tables["widgets"]
	name := widgets.Columns["name"]
	if name.TargetType != "text" {
		t.Errorf("expected name to fall back to text, got %q", name.TargetType)
	}
	description := widgets.Columns["description"]
	if description.TargetType != "text" {
		t.Errorf("expected description to fall back to text, got %q", description.TargetType)
	}
}

func TestProfileDatabase_SurfacesAnInferredForeignKeyAsASuggestion(t *testing.T) {
	db, path := openTestDB(t, `
		CREATE TABLE Customers (CustomerId INTEGER PRIMARY KEY);
		CREATE TABLE Invoices (InvoiceId INTEGER PRIMARY KEY, CustomerId INTEGER);
	`)
	db.Exec(`INSERT INTO Customers (CustomerId) VALUES (1), (2)`)
	db.Exec(`INSERT INTO Invoices (CustomerId) VALUES (1), (2)`)

	result, err := ProfileDatabase(db, path, 500, 0.9)
	if err != nil {
		t.Fatalf("ProfileDatabase: %v", err)
	}

	invoices := result.Config.Tables["Invoices"]
	if len(invoices.SuggestedForeignKeys) != 1 {
		t.Fatalf("expected 1 suggested foreign key on Invoices, got %d: %+v", len(invoices.SuggestedForeignKeys), invoices.SuggestedForeignKeys)
	}
	if len(invoices.ForeignKeys) != 0 {
		t.Errorf("expected the suggestion to stay out of ForeignKeys until promoted by hand, got %+v", invoices.ForeignKeys)
	}
}

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

// TestProfileDatabase_WidensVARCHARSuggestionToFitRealData is a regression
// test for issue #84: varcharSuggestions derives N purely from the
// declared SQLite type, which SQLite never enforces — a real row can
// exceed it. If the reviewer accepts the suggestion as originally shown,
// that row would abort COPY with "value too long for type character
// varying(N)". The suggestion must widen to the table's actual longest
// value instead.
func TestProfileDatabase_WidensVARCHARSuggestionToFitRealData(t *testing.T) {
	db, path := openTestDB(t, `
		CREATE TABLE customers (
			id INTEGER PRIMARY KEY,
			first_name VARCHAR(5),
			city VARCHAR(100)
		);
	`)
	db.Exec(`INSERT INTO customers (first_name, city) VALUES (?, ?)`, "Alex", "Springfield")
	// SQLite doesn't enforce VARCHAR(5); this row's first_name is 11 bytes,
	// well past the declared length.
	db.Exec(`INSERT INTO customers (first_name, city) VALUES (?, ?)`, "Bartholomew", "Shelbyville")

	result, err := ProfileDatabase(db, path, 500, 0.9)
	if err != nil {
		t.Fatalf("ProfileDatabase: %v", err)
	}

	firstName := result.Config.Tables["customers"].Columns["first_name"]
	if firstName.TargetType != "varchar(11)" {
		t.Errorf("expected first_name widened to varchar(11) to fit \"Bartholomew\", got %q", firstName.TargetType)
	}
	if firstName.Reviewed {
		t.Error("expected first_name to still be flagged for review, not auto-applied")
	}
}
