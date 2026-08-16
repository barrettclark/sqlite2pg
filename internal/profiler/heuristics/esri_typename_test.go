package heuristics

import (
	"testing"

	"sqlite2pg/internal/profiler"
)

func TestEsriTypeName_MapsKnownEsriTypes(t *testing.T) {
	h := EsriTypeName{}
	cases := []struct {
		declared string
		want     string
	}{
		{"int32", "integer"},
		{"float32", "double precision"},
		{"float64", "double precision"},
		{"geometryblob", "__drop__"},
	}
	for _, c := range cases {
		meta := profiler.ColumnMeta{Name: "col", DeclaredType: c.declared}
		if !h.AppliesTo(meta) {
			t.Errorf("expected AppliesTo(%q) to return true", c.declared)
			continue
		}
		finding, ok := h.Evaluate(meta, nil)
		if !ok {
			t.Errorf("expected a finding for declared type %q", c.declared)
			continue
		}
		if finding.SuggestedType != c.want {
			t.Errorf("declared type %q: got %q, want %q", c.declared, finding.SuggestedType, c.want)
		}
	}
}

func TestEsriTypeName_NotApplicableToStandardTypes(t *testing.T) {
	h := EsriTypeName{}
	if h.AppliesTo(profiler.ColumnMeta{DeclaredType: "INTEGER"}) {
		t.Error("expected AppliesTo to return false for a standard SQLite type name")
	}
}

func TestEsriTypeName_DefersRealdateToTheMoreSpecificJulianDayHeuristic(t *testing.T) {
	// realdate columns need the actual Julian Day Number conversion math,
	// which only JulianDay implements — EsriTypeName must not compete with
	// it for the same column, or the generic (higher-confidence) mapping
	// wins and the column loses its date conversion.
	h := EsriTypeName{}
	if h.AppliesTo(profiler.ColumnMeta{DeclaredType: "realdate"}) {
		t.Error("expected EsriTypeName to not apply to realdate; that's JulianDay's job")
	}
}
