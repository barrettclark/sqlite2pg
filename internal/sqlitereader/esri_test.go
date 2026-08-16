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

func TestFilterSystemTables_ExcludesGDBAndSpatialiteTables(t *testing.T) {
	tables := []TableInfo{
		{Name: "GDB_SystemCatalog"},
		{Name: "st_spatial_ref_sys"},
		{Name: "SchoolSites2425"},
	}
	filtered := FilterSystemTables(tables)
	if len(filtered) != 1 || filtered[0].Name != "SchoolSites2425" {
		t.Errorf("expected only SchoolSites2425 to remain, got %+v", filtered)
	}
}
