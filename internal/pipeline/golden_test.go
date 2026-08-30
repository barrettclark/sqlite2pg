package pipeline

// Tier 2: golden-fixture tests against real databases (plus a couple of
// small handcrafted ones for cases no real fixture happened to cover). The
// fixtures live in testdata/fixtures/ within this module (see
// internal/pipeline's own nesting: two levels up from here), so the tests
// aren't sensitive to wherever the surrounding project directory happens to
// sit. Rather than a full hand-authored per-column YAML diff for every
// table (atomic_database.db alone has 12 tables), these assert the
// specific decision points that needed non-default handling — the cases
// that actually matter as regression coverage for the heuristic set. No
// Postgres connection is used.

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

	// last_reported: Unix epoch timestamp.
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

func TestGolden_ChinookPreservesPrimaryKeyAndForeignKeys(t *testing.T) {
	db, path := openFixture(t, "chinook.db")
	result, err := ProfileDatabase(db, path, 500, 0.9)
	if err != nil {
		t.Fatalf("ProfileDatabase: %v", err)
	}

	albums, ok := result.Config.Tables["albums"]
	if !ok {
		t.Fatalf("expected albums table, got tables: %v", tableNames(result))
	}
	if seq := albums.Columns["AlbumId"].PrimaryKeySeq; seq != 1 {
		t.Errorf("expected albums.AlbumId primary key position 1, got %d", seq)
	}
	if len(albums.ForeignKeys) != 1 {
		t.Fatalf("expected albums to have 1 foreign key (ArtistId -> artists), got %+v", albums.ForeignKeys)
	}
	fk := albums.ForeignKeys[0]
	if fk.RefTable != "artists" {
		t.Errorf("expected foreign key to reference artists, got %q", fk.RefTable)
	}
	if len(fk.Columns) != 1 || fk.Columns[0] != "ArtistId" {
		t.Errorf("expected foreign key column [ArtistId], got %v", fk.Columns)
	}
}

func TestGolden_ChinookPreservesCompositePrimaryKey(t *testing.T) {
	db, path := openFixture(t, "chinook.db")
	result, err := ProfileDatabase(db, path, 500, 0.9)
	if err != nil {
		t.Fatalf("ProfileDatabase: %v", err)
	}
	pt, ok := result.Config.Tables["playlist_track"]
	if !ok {
		t.Fatalf("expected playlist_track table, got tables: %v", tableNames(result))
	}
	if seq := pt.Columns["PlaylistId"].PrimaryKeySeq; seq != 1 {
		t.Errorf("expected PlaylistId primary key position 1, got %d", seq)
	}
	if seq := pt.Columns["TrackId"].PrimaryKeySeq; seq != 2 {
		t.Errorf("expected TrackId primary key position 2, got %d", seq)
	}
}

func TestGolden_AtomicDatabase(t *testing.T) {
	db, path := openFixture(t, "atomic_database.db")
	// small sample size: MACS has 20k rows, this must stay fast.
	result, err := ProfileDatabase(db, path, 200, 0.9)
	if err != nil {
		t.Fatalf("ProfileDatabase: %v", err)
	}
	if len(result.Config.Tables) != 12 {
		t.Errorf("expected 12 tables, got %d: %v", len(result.Config.Tables), tableNames(result))
	}
}

// TestGolden_SampleDates covers two real-world date/time shapes that had
// no fixture-backed regression coverage before this test: 8-digit YYYYMMDD
// dates stored as both INTEGER and TEXT (found in the wild in an ISO 10383
// MIC registry database) and US-style M/D/YYYY h:mm:ss AM/PM timestamps
// (found in the wild in an NEH grants database). Both were real bugs fixed
// during dogfooding against databases outside this repo, so unlike the
// other golden tests here there's no upstream IMPORT_NOTES.md entry to
// trace to — sample-dates.sqlite is a small handcrafted fixture instead,
// same as sample-types.sqlite.
func TestGolden_SampleDates(t *testing.T) {
	db, path := openFixture(t, "sample-dates.sqlite")
	result, err := ProfileDatabase(db, path, 500, 0.9)
	if err != nil {
		t.Fatalf("ProfileDatabase: %v", err)
	}

	targetType, source, _ := decisionFor(t, result, "date_demo", "creation_date")
	if targetType != "date" || source != "heuristic:yyyymmdd_date" {
		t.Errorf("creation_date (YYYYMMDD as INTEGER): expected date via yyyymmdd_date, got %q via %q", targetType, source)
	}

	targetType, source, _ = decisionFor(t, result, "date_demo", "last_validation_date")
	if targetType != "date" || source != "heuristic:yyyymmdd_date" {
		t.Errorf("last_validation_date (YYYYMMDD as TEXT): expected date via yyyymmdd_date, got %q via %q", targetType, source)
	}

	targetType, source, _ = decisionFor(t, result, "date_demo", "logged_at")
	if targetType != "timestamptz" || source != "heuristic:iso8601_timestamp" {
		t.Errorf("logged_at (US-style M/D/YYYY h:mm:ss AM/PM): expected timestamptz via iso8601_timestamp, got %q via %q", targetType, source)
	}

	// The remaining columns cover issue #2's date/time coverage gaps:
	// month-name dates, day-first (D/M/YYYY) dates, Excel/Access serial
	// dates, and epoch timestamps in milliseconds/microseconds.
	targetType, source, _ = decisionFor(t, result, "date_demo", "month_name_date")
	if targetType != "timestamptz" || source != "heuristic:iso8601_timestamp" {
		t.Errorf("month_name_date: expected timestamptz via iso8601_timestamp, got %q via %q", targetType, source)
	}

	targetType, source, _ = decisionFor(t, result, "date_demo", "day_first_date")
	if targetType != "timestamptz" || source != "heuristic:day_first_date" {
		t.Errorf("day_first_date: expected timestamptz via day_first_date, got %q via %q", targetType, source)
	}

	targetType, source, _ = decisionFor(t, result, "date_demo", "excel_serial_date")
	if targetType != "timestamptz" || source != "heuristic:excel_serial_date" {
		t.Errorf("excel_serial_date: expected timestamptz via excel_serial_date, got %q via %q", targetType, source)
	}

	targetType, source, _ = decisionFor(t, result, "date_demo", "epoch_millis_at")
	if targetType != "timestamptz" || source != "heuristic:unix_epoch_millis" {
		t.Errorf("epoch_millis_at: expected timestamptz via unix_epoch_millis, got %q via %q", targetType, source)
	}

	targetType, source, _ = decisionFor(t, result, "date_demo", "epoch_micros_at")
	if targetType != "timestamptz" || source != "heuristic:unix_epoch_micros" {
		t.Errorf("epoch_micros_at: expected timestamptz via unix_epoch_micros, got %q via %q", targetType, source)
	}
}

// TestGolden_SampleUUIDs covers a real-world shape with no fixture-backed
// regression coverage before this test: a TEXT column storing a single
// canonical UUID per row. Real evidence: an ISO 10383 MIC registry
// database's station_id column, and a beets music library's several
// single-valued MusicBrainz ID columns — both outside this repo, so
// sample-uuids.sqlite is a small handcrafted fixture instead, same as
// sample-dates.sqlite and sample-types.sqlite. label is a plain string
// column included as a negative control: it must not also get flagged.
func TestGolden_SampleUUIDs(t *testing.T) {
	db, path := openFixture(t, "sample-uuids.sqlite")
	result, err := ProfileDatabase(db, path, 500, 0.9)
	if err != nil {
		t.Fatalf("ProfileDatabase: %v", err)
	}

	targetType, source, _ := decisionFor(t, result, "entity_demo", "station_id")
	if targetType != "uuid" || source != "heuristic:uuid_format" {
		t.Errorf("station_id: expected uuid via uuid_format, got %q via %q", targetType, source)
	}

	targetType, source, _ = decisionFor(t, result, "entity_demo", "label")
	if targetType != "text" {
		t.Errorf("label: expected the uuid_format heuristic to leave a plain string column alone, got %q via %q", targetType, source)
	}
}

// TestGolden_SampleNumericText covers a real-world shape found via
// dogfooding against a companies.db file: TEXT-declared columns storing
// plain numeric values with no comma formatting at all (current_employees,
// total_employees) or whole-number floats ("1998.0" for year_founded) —
// comma_formatted_number only fires once it's seen at least one
// comma-formatted value, so a column that never happens to use comma
// formatting got no opinion and silently fell back to text.
// postal_code is a negative control: "07030" has a meaningful leading
// zero a numeric type would destroy on round-trip, so it must stay text.
func TestGolden_SampleNumericText(t *testing.T) {
	db, path := openFixture(t, "sample-numeric-text.sqlite")
	result, err := ProfileDatabase(db, path, 500, 0.9)
	if err != nil {
		t.Fatalf("ProfileDatabase: %v", err)
	}

	targetType, source, _ := decisionFor(t, result, "company_demo", "year_founded")
	if targetType != "integer" || source != "heuristic:numeric_text" {
		t.Errorf("year_founded: expected integer via numeric_text, got %q via %q", targetType, source)
	}

	targetType, source, _ = decisionFor(t, result, "company_demo", "current_employees")
	if targetType != "integer" || source != "heuristic:numeric_text" {
		t.Errorf("current_employees: expected integer via numeric_text, got %q via %q", targetType, source)
	}

	targetType, source, _ = decisionFor(t, result, "company_demo", "total_employees")
	if targetType != "integer" || source != "heuristic:numeric_text" {
		t.Errorf("total_employees: expected integer via numeric_text, got %q via %q", targetType, source)
	}

	targetType, _, _ = decisionFor(t, result, "company_demo", "postal_code")
	if targetType != "text" {
		t.Errorf("postal_code: expected numeric_text to leave a meaningful-leading-zero column alone, got %q", targetType)
	}
}

// TestGolden_SampleVarchar covers issue #7: preserving a declared
// VARCHAR(N) length where it looks intentional. customer_demo's two
// VARCHAR columns have different declared lengths (45 vs 100) — real
// MySQL-origin schema shape — so both should be suggested as varchar(N),
// flagged for review. widget_demo's two VARCHAR columns share the same
// declared length (8000) — the hallmark of a mechanical export default —
// so both should fall back to plain text, unflagged.
func TestGolden_SampleVarchar(t *testing.T) {
	db, path := openFixture(t, "sample-varchar.sqlite")
	result, err := ProfileDatabase(db, path, 500, 0.9)
	if err != nil {
		t.Fatalf("ProfileDatabase: %v", err)
	}

	targetType, source, reviewed := decisionFor(t, result, "customer_demo", "first_name")
	if targetType != "varchar(45)" || source != "heuristic:varchar_length_preservation" {
		t.Errorf("first_name: expected varchar(45) via varchar_length_preservation, got %q via %q", targetType, source)
	}
	if reviewed {
		t.Error("first_name: expected to be flagged for review, not auto-applied")
	}

	targetType, source, _ = decisionFor(t, result, "customer_demo", "city")
	if targetType != "varchar(100)" || source != "heuristic:varchar_length_preservation" {
		t.Errorf("city: expected varchar(100) via varchar_length_preservation, got %q via %q", targetType, source)
	}

	targetType, _, _ = decisionFor(t, result, "widget_demo", "name")
	if targetType != "text" {
		t.Errorf("name: expected a uniform VARCHAR length across the table to fall back to text, got %q", targetType)
	}

	targetType, _, _ = decisionFor(t, result, "widget_demo", "description")
	if targetType != "text" {
		t.Errorf("description: expected a uniform VARCHAR length across the table to fall back to text, got %q", targetType)
	}
}
