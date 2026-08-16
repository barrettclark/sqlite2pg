package heuristics

import (
	"testing"

	"sqlite2pg/internal/profiler"
)

func TestJulianDay_AppliesToRealdateColumns(t *testing.T) {
	h := JulianDay{}
	if !h.AppliesTo(profiler.ColumnMeta{Name: "OpenDate", DeclaredType: "realdate"}) {
		t.Error("expected AppliesTo to return true for a realdate column")
	}
	if h.AppliesTo(profiler.ColumnMeta{Name: "count", DeclaredType: "INTEGER"}) {
		t.Error("expected AppliesTo to return false for a non-realdate column")
	}
}

func TestJulianDay_DetectsPlausibleJulianDayNumbers(t *testing.T) {
	h := JulianDay{}
	// 2453975.5 is the documented SchoolSites2425 example (2006-07-01-ish).
	samples := []profiler.Value{float64(2453975.5), float64(2454000.5)}
	finding, ok := h.Evaluate(profiler.ColumnMeta{}, samples)
	if !ok {
		t.Fatal("expected a finding for plausible Julian Day Numbers")
	}
	if finding.SuggestedType != "date" {
		t.Errorf("expected suggested type date, got %q", finding.SuggestedType)
	}
	if finding.TransformExpr != "julian_day_to_date" {
		t.Errorf("expected transform julian_day_to_date, got %q", finding.TransformExpr)
	}
}

func TestJulianDay_NoOpinionWhenOutsidePlausibleRange(t *testing.T) {
	h := JulianDay{}
	samples := []profiler.Value{float64(1.5), float64(2.5)}
	_, ok := h.Evaluate(profiler.ColumnMeta{}, samples)
	if ok {
		t.Fatal("expected no opinion for values far outside any plausible Julian Day range")
	}
}
