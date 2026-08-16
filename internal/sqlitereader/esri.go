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

// FilterSystemTables excludes Esri GDB_* system tables and Spatialite st_*
// tables, leaving only user data tables — the equivalent of pgloader's
// "INCLUDING ONLY TABLE NAMES LIKE" clause used to skip Esri system tables.
func FilterSystemTables(tables []TableInfo) []TableInfo {
	var out []TableInfo
	for _, t := range tables {
		if strings.HasPrefix(t.Name, "GDB_") || strings.HasPrefix(t.Name, "st_") {
			continue
		}
		out = append(out, t)
	}
	return out
}
