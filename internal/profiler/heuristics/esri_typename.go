package heuristics

import (
	"strings"

	"sqlite2pg/internal/profiler"
)

// esriTypeNames maps Esri File Geodatabase custom SQLite type names (which
// contain digits and are rejected outright by pgloader's schema parser) to
// their Postgres equivalents. "__drop__" marks columns that cannot be
// represented (proprietary binary geometry) and should be excluded.
// realdate is deliberately excluded here: JulianDay owns that type, since
// it also supplies the actual Julian Day Number -> date conversion, not
// just the type name mapping.
var esriTypeNames = map[string]string{
	"int32":        "integer",
	"float32":      "double precision",
	"float64":      "double precision",
	"geometryblob": "__drop__",
}

func esriTypeMapping(declared string) (string, bool) {
	t, ok := esriTypeNames[strings.ToLower(declared)]
	return t, ok
}

// EsriTypeName maps Esri custom SQLite type names to Postgres types. Unlike
// most heuristics, this decision comes entirely from the declared type, not
// sampled values.
type EsriTypeName struct{}

func (EsriTypeName) Name() string { return "esri_typename_mapping" }

func (EsriTypeName) AppliesTo(meta profiler.ColumnMeta) bool {
	_, ok := esriTypeMapping(meta.DeclaredType)
	return ok
}

func (EsriTypeName) Evaluate(meta profiler.ColumnMeta, samples []profiler.Value) (profiler.Finding, bool) {
	target, ok := esriTypeMapping(meta.DeclaredType)
	if !ok {
		return profiler.Finding{}, false
	}
	rationale := "Esri custom type name " + meta.DeclaredType + " mapped to " + target
	transform := "esri_typename"
	if target == "__drop__" {
		rationale = "Esri proprietary binary geometry column; cannot be represented without PostGIS"
		transform = "drop_column"
	}
	return profiler.Finding{
		SuggestedType: target,
		Confidence:    0.99,
		Rationale:     rationale,
		TransformExpr: transform,
	}, true
}

func init() { profiler.Register(EsriTypeName{}) }
