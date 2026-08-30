package sqlitereader

import "testing"

func TestIsEsriGeodatabase_DetectsGDBSystemTables(t *testing.T) {
	tables := []TableInfo{
		{Name: "GDB_SystemCatalog"},
		{Name: "GDB_Items"},
		{Name: "SchoolSites2425"},
	}
	if !IsEsriGeodatabase(tables) {
		t.Error("expected a table set containing GDB_SystemCatalog to be detected as an Esri geodatabase")
	}
}

func TestIsEsriGeodatabase_FalseForOrdinaryDatabase(t *testing.T) {
	tables := []TableInfo{{Name: "bikes"}, {Name: "sqlite_sequence"}}
	if IsEsriGeodatabase(tables) {
		t.Error("expected an ordinary table set to not be detected as an Esri geodatabase")
	}
}

func TestFilterSystemTables_ExcludesGDBAndSpatialiteTablesWhenEsri(t *testing.T) {
	tables := []TableInfo{
		{Name: "GDB_SystemCatalog"},
		{Name: "st_spatial_ref_sys"},
		{Name: "SchoolSites2425"},
	}
	kept, filtered := FilterSystemTables(tables, true)
	if len(kept) != 1 || kept[0].Name != "SchoolSites2425" {
		t.Errorf("expected only SchoolSites2425 to remain, got %+v", kept)
	}
	if len(filtered) != 2 {
		t.Errorf("expected 2 tables reported as filtered, got %+v", filtered)
	}
}

// Issue #35: a plain, non-Esri/Spatialite SQLite database can legitimately
// have a user table named st_locations (or similar). The st_* filter is a
// Spatialite system-table convention and must only apply when the source
// database has actually been confirmed Esri/Spatialite — it must not
// silently swallow ordinary user tables that happen to start with st_.
func TestFilterSystemTables_KeepsStPrefixedUserTablesWhenNotEsri(t *testing.T) {
	tables := []TableInfo{
		{Name: "st_locations"},
		{Name: "st_2024_results"},
		{Name: "bikes"},
	}
	kept, filtered := FilterSystemTables(tables, false)
	if len(kept) != 3 {
		t.Errorf("expected all 3 tables to be kept for a non-Esri database, got %+v", kept)
	}
	if len(filtered) != 0 {
		t.Errorf("expected nothing filtered for a non-Esri database, got %+v", filtered)
	}
}

// Regression guard: even on a non-Esri database, genuine Esri GDB_* system
// tables should never leak through, since their presence is itself what
// defines an Esri geodatabase (see IsEsriGeodatabase).
func TestFilterSystemTables_AlwaysExcludesGDBTables(t *testing.T) {
	tables := []TableInfo{
		{Name: "GDB_SystemCatalog"},
		{Name: "bikes"},
	}
	kept, filtered := FilterSystemTables(tables, false)
	if len(kept) != 1 || kept[0].Name != "bikes" {
		t.Errorf("expected only bikes to remain, got %+v", kept)
	}
	if len(filtered) != 1 || filtered[0].Name != "GDB_SystemCatalog" {
		t.Errorf("expected GDB_SystemCatalog to be reported as filtered, got %+v", filtered)
	}
}
