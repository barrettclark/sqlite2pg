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

// FilterSystemTables excludes Esri GDB_* system tables, leaving only user
// data tables — the equivalent of pgloader's "INCLUDING ONLY TABLE NAMES
// LIKE" clause used to skip Esri system tables. When esri is true (the
// source database has already been confirmed an Esri/Spatialite geodatabase
// via IsEsriGeodatabase), Spatialite's st_* system-table convention is also
// filtered.
//
// Issue #35: st_* is only a system-table convention inside a Spatialite
// database. Applying it unconditionally silently dropped ordinary user
// tables like st_locations from plain, non-Spatialite SQLite sources, so the
// st_* filter must be gated on esri. GDB_* is filtered unconditionally
// because its presence is itself what defines an Esri geodatabase (see
// IsEsriGeodatabase) — a plain database should never legitimately have
// GDB_*-prefixed tables.
//
// filtered reports every table excluded, so callers can surface an explicit
// warning rather than let the exclusion be silent.
func FilterSystemTables(tables []TableInfo, esri bool) (kept []TableInfo, filtered []TableInfo) {
	for _, t := range tables {
		isSystem := strings.HasPrefix(t.Name, "GDB_") || (esri && strings.HasPrefix(t.Name, "st_"))
		if isSystem {
			filtered = append(filtered, t)
			continue
		}
		kept = append(kept, t)
	}
	return kept, filtered
}
