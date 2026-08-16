package pipeline

// Tier 2: golden-fixture tests against the real, already-migrated
// databases documented in IMPORT_NOTES.md. The fixtures live in
// testdata/fixtures/ within this module (see internal/pipeline's own
// nesting: two levels up from here), so the tests aren't sensitive to
// wherever the surrounding project directory happens to sit. Rather than a
// full hand-authored per-column YAML diff for every table
// (atomic_database.db alone has 12 tables), these assert the specific
// decision points IMPORT_NOTES.md records as having required non-default
// handling — the cases that actually matter as regression coverage for the
// heuristic set. No Postgres connection is used.

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	_ "sqlite2pg/internal/profiler/heuristics"
)

// fixturesDir is testdata/fixtures/ at the sqlite2pg module root.
// Overridable with SQLITE2PG_FIXTURES_DIR for local experimentation.
func fixturesDir(t *testing.T) string {
	t.Helper()
	if dir := os.Getenv("SQLITE2PG_FIXTURES_DIR"); dir != "" {
		return dir
	}
	dir, err := filepath.Abs("../../testdata/fixtures")
	if err != nil {
		t.Fatalf("resolving fixtures dir: %v", err)
	}
	return dir
}

func openFixture(t *testing.T, name string) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(fixturesDir(t), name)
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Ping(); err != nil {
		t.Skipf("fixture %s not available: %v", name, err)
	}
	return db, path
}

func decisionFor(t *testing.T, result *ProfileResult, table, column string) (targetType string, source string, reviewed bool) {
	t.Helper()
	tbl, ok := result.Config.Tables[table]
	if !ok {
		t.Fatalf("expected table %q in profiled config, tables present: %v", table, tableNames(result))
	}
	col, ok := tbl.Columns[column]
	if !ok {
		var names []string
		for n := range tbl.Columns {
			names = append(names, n)
		}
		t.Fatalf("expected column %q in table %q, columns present: %v", column, table, names)
	}
	return col.TargetType, col.Source, col.Reviewed
}

func tableNames(r *ProfileResult) []string {
	var names []string
	for n := range r.Config.Tables {
		names = append(names, n)
	}
	return names
}

func TestGolden_Bikes(t *testing.T) {
	db, path := openFixture(t, "bikes.db")
	result, err := ProfileDatabase(db, path, 500, 0.9)
	if err != nil {
		t.Fatalf("ProfileDatabase: %v", err)
	}

	// last_reported: Unix epoch timestamp per IMPORT_NOTES.md.
	targetType, _, _ := decisionFor(t, result, "bikes", "last_reported")
	if targetType != "timestamptz" {
		t.Errorf("bikes.last_reported: expected timestamptz, got %q", targetType)
	}

	// is_installed: documented as cast to boolean, but this is the
	// canonical ambiguous 0/1 case — must be flagged for review, not
	// silently auto-approved.
	targetType, source, reviewed := decisionFor(t, result, "bikes", "is_installed")
	if targetType != "boolean" {
		t.Errorf("bikes.is_installed: expected best-guess boolean, got %q", targetType)
	}
	if reviewed {
		t.Error("bikes.is_installed: expected reviewed=false (ambiguous, needs human sign-off)")
	}
	if source != "heuristic:boolean01" {
		t.Errorf("bikes.is_installed: expected source heuristic:boolean01, got %q", source)
	}
	found := false
	for _, u := range result.Unresolved {
		if u.Table == "bikes" && u.Column == "is_installed" {
			found = true
		}
	}
	if !found {
		t.Error("expected bikes.is_installed to appear in Unresolved")
	}
}

func TestGolden_DisabilityCompByCounty(t *testing.T) {
	db, path := openFixture(t, "DisabilityCompByCounty.db")
	result, err := ProfileDatabase(db, path, 3200, 0.9)
	if err != nil {
		t.Fatalf("ProfileDatabase: %v", err)
	}

	// Comma-formatted numeric column.
	targetType, source, _ := decisionFor(t, result, "DisabilityCompByCounty", "Total: Disability Compensation Recipients")
	if targetType != "integer" {
		t.Errorf("Total: Disability Compensation Recipients: expected integer, got %q", targetType)
	}
	if source != "heuristic:comma_formatted_number" {
		t.Errorf("Total: Disability Compensation Recipients: expected comma_formatted_number heuristic, got %q", source)
	}

	// FIPS code: plain integers plus one 'Unknown' aggregate row.
	targetType, source, _ = decisionFor(t, result, "DisabilityCompByCounty", "FIPS code")
	if targetType != "integer" {
		t.Errorf("FIPS code: expected integer, got %q", targetType)
	}
	if source != "heuristic:sentinel_null" {
		t.Errorf("FIPS code: expected sentinel_null heuristic to catch the 'Unknown' row, got %q", source)
	}
}

func TestGolden_AustinRoadConstruction(t *testing.T) {
	db, path := openFixture(t, "AustinRoadConstruction.db")
	result, err := ProfileDatabase(db, path, 3600, 0.9)
	if err != nil {
		t.Fatalf("ProfileDatabase: %v", err)
	}

	targetType, _, _ := decisionFor(t, result, "construction", ":created_at")
	if targetType != "timestamptz" {
		t.Errorf(":created_at: expected timestamptz, got %q", targetType)
	}

	targetType, _, _ = decisionFor(t, result, "construction", "geometry")
	if targetType != "jsonb" {
		t.Errorf("geometry: expected jsonb, got %q", targetType)
	}
}

func TestGolden_SchoolSitesGeodatabase(t *testing.T) {
	db, path := openFixture(t, "SchoolSites2425_-4255819620268625087.geodatabase")
	result, err := ProfileDatabase(db, path, 500, 0.9)
	if err != nil {
		t.Fatalf("ProfileDatabase: %v", err)
	}

	if result.Config.Source.Kind != "esri_geodatabase" {
		t.Errorf("expected source kind esri_geodatabase, got %q", result.Config.Source.Kind)
	}

	targetType, source, _ := decisionFor(t, result, "SchoolSites2425", "EnrollTotal")
	if targetType != "integer" || source != "heuristic:esri_typename_mapping" {
		t.Errorf("EnrollTotal (int32): expected integer via esri_typename_mapping, got %q via %q", targetType, source)
	}

	targetType, source, _ = decisionFor(t, result, "SchoolSites2425", "OpenDate")
	if targetType != "date" || source != "heuristic:esri_julian_day" {
		t.Errorf("OpenDate (realdate): expected date via esri_julian_day, got %q via %q", targetType, source)
	}

	targetType, source, _ = decisionFor(t, result, "SchoolSites2425", "SHAPE")
	if targetType != "__drop__" || source != "heuristic:esri_typename_mapping" {
		t.Errorf("SHAPE (geometryblob): expected __drop__ via esri_typename_mapping, got %q via %q", targetType, source)
	}
}

func TestGolden_AviationFacilitiesGeodatabase(t *testing.T) {
	db, path := openFixture(t, "NTAD_Aviation_Facilities_698356094499483505.geodatabase")
	result, err := ProfileDatabase(db, path, 500, 0.9)
	if err != nil {
		t.Fatalf("ProfileDatabase: %v", err)
	}
	if result.Config.Source.Kind != "esri_geodatabase" {
		t.Errorf("expected source kind esri_geodatabase, got %q", result.Config.Source.Kind)
	}
	if _, ok := result.Config.Tables["Aviation_Facilities"]; !ok {
		t.Fatalf("expected Aviation_Facilities table in profiled config, got tables: %v", tableNames(result))
	}
}

func TestGolden_AtomicDatabase(t *testing.T) {
	db, path := openFixture(t, "atomic_database.db")
	// small sample size: MACS has ~1M rows, this must stay fast.
	result, err := ProfileDatabase(db, path, 200, 0.9)
	if err != nil {
		t.Fatalf("ProfileDatabase: %v", err)
	}
	if len(result.Config.Tables) != 12 {
		t.Errorf("expected 12 tables, got %d: %v", len(result.Config.Tables), tableNames(result))
	}
}
