package sqlitereader

import "strings"

// IsEsriGeodatabase reports whether tables came from an Esri File
// Geodatabase, identified by the presence of its GDB_* system tables.
func IsEsriGeodatabase(tables []TableInfo) bool {
	for _, t := range tables {
		if strings.HasPrefix(t.Name, "GDB_") {
			return true
		}
	}
	return false
}

// spatialiteMarkerTables are Spatialite's own real system tables — their
// presence is what actually defines a database as Spatialite, independent
// of whether it also happens to be an Esri File Geodatabase export.
var spatialiteMarkerTables = map[string]bool{
	"spatial_ref_sys":    true,
	"geometry_columns":   true,
	"spatialite_history": true,
}

// IsSpatialite reports whether tables came from a Spatialite database,
// identified by the presence of Spatialite's own system tables
// (spatial_ref_sys, geometry_columns, spatialite_history). This is
// independent of IsEsriGeodatabase: an Esri File Geodatabase export commonly
// carries Spatialite's st_* system-table naming convention without ever
// having GDB_* tables, and conversely a plain Esri FGDB (GDB_* present) may
// not carry these Spatialite markers at all — issue #47.
func IsSpatialite(tables []TableInfo) bool {
	for _, t := range tables {
		if spatialiteMarkerTables[t.Name] {
			return true
		}
	}
	return false
}

// FilterSystemTables excludes Esri GDB_* system tables, leaving only user
// data tables — the equivalent of pgloader's "INCLUDING ONLY TABLE NAMES
// LIKE" clause used to skip Esri system tables. When esri or spatialite is
// true (the source database has been confirmed an Esri geodatabase via
// IsEsriGeodatabase, or a genuine Spatialite database via IsSpatialite),
// Spatialite's st_* system-table convention is also filtered.
//
// Issue #35: st_* is only a system-table convention inside a Spatialite
// database. Applying it unconditionally silently dropped ordinary user
// tables like st_locations from plain, non-Spatialite SQLite sources, so the
// st_* filter must be gated on the source actually being Esri/Spatialite.
// GDB_* is filtered unconditionally because its presence is itself what
// defines an Esri geodatabase (see IsEsriGeodatabase) — a plain database
// should never legitimately have GDB_*-prefixed tables.
//
// Issue #47: gating the st_* filter on esri alone missed a genuine
// Spatialite database that is NOT an Esri FGDB export (no GDB_* tables at
// all) — its real st_*-prefixed Spatialite system tables were migrated as
// ordinary user data. spatialite, computed via IsSpatialite, closes that
// gap independently of the Esri check.
//
// filtered reports every table excluded, so callers can surface an explicit
// warning rather than let the exclusion be silent.
func FilterSystemTables(tables []TableInfo, esri bool, spatialite bool) (kept []TableInfo, filtered []TableInfo) {
	filterSt := esri || spatialite
	for _, t := range tables {
		isSystem := strings.HasPrefix(t.Name, "GDB_") || (filterSt && strings.HasPrefix(t.Name, "st_"))
		if isSystem {
			filtered = append(filtered, t)
			continue
		}
		kept = append(kept, t)
	}
	return kept, filtered
}
