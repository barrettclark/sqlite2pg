package heuristics

import (
	"testing"

	"sqlite2pg/internal/profiler"
)

func TestSentinelNull_DetectsKnownSentinelTokensMixedWithNumbers(t *testing.T) {
	h := SentinelNull{}
	meta := profiler.ColumnMeta{Table: "disabilitycompbycounty", Name: "FIPS code", DeclaredType: "INTEGER"}
	if !h.AppliesTo(meta) {
		t.Fatal("expected AppliesTo to return true for an INTEGER column")
	}

	samples := []profiler.Value{"1001", "1003", "Unknown", "1005"}
	finding, ok := h.Evaluate(meta, samples)
	if !ok {
		t.Fatal("expected a finding for numeric values mixed with a sentinel token")
	}
	if finding.SuggestedType != "integer" {
		t.Errorf("expected suggested type integer, got %q", finding.SuggestedType)
	}
	if finding.TransformExpr != "nullif_sentinels" {
		t.Errorf("expected transform nullif_sentinels, got %q", finding.TransformExpr)
	}
}

func TestSentinelNull_DetectsSentinelMixedWithNativeIntegerValues(t *testing.T) {
	h := SentinelNull{}
	// SQLite/database-sql returns numeric-affinity values as int64, not
	// string — only the sentinel row comes back as a string.
	samples := []profiler.Value{int64(1001), int64(1003), "Unknown", int64(1005)}
	finding, ok := h.Evaluate(profiler.ColumnMeta{DeclaredType: "INTEGER"}, samples)
	if !ok {
		t.Fatal("expected a finding for native int64 values mixed with a sentinel token")
	}
	if finding.SuggestedType != "integer" {
		t.Errorf("expected suggested type integer, got %q", finding.SuggestedType)
	}
}

func TestSentinelNull_SuggestsDoublePrecisionForFloatValuesMixedWithSentinel(t *testing.T) {
	// Regression: found via dogfooding against neh-grants.db. Latitude/
	// Longitude are declared REAL with genuine float64 values, and a few
	// rows use "Unknown" as a sentinel for missing coordinates — the
	// heuristic must not hardcode "integer" for numeric-plus-sentinel
	// columns regardless of what kind of number they actually are.
	h := SentinelNull{}
	meta := profiler.ColumnMeta{Table: "neh_grants", Name: "Latitude", DeclaredType: "REAL"}
	if !h.AppliesTo(meta) {
		t.Fatal("expected AppliesTo to return true for a REAL column")
	}

	samples := []profiler.Value{33.38353, -91.05397, "Unknown", 40.71864}
	finding, ok := h.Evaluate(meta, samples)
	if !ok {
		t.Fatal("expected a finding for float values mixed with a sentinel token")
	}
	if finding.SuggestedType != "double precision" {
		t.Errorf("expected suggested type double precision, got %q", finding.SuggestedType)
	}
	if finding.TransformExpr != "nullif_sentinels" {
		t.Errorf("expected transform nullif_sentinels, got %q", finding.TransformExpr)
	}
}

func TestSentinelNull_NoOpinionWhenAllValuesAreNumeric(t *testing.T) {
	h := SentinelNull{}
	samples := []profiler.Value{"1001", "1003", "1005"}
	_, ok := h.Evaluate(profiler.ColumnMeta{DeclaredType: "INTEGER"}, samples)
	if ok {
		t.Fatal("expected no opinion when there is no sentinel text to strip")
	}
}

func TestSentinelNull_NoOpinionOnUnrecognizedNonNumericText(t *testing.T) {
	h := SentinelNull{}
	// "Texas" isn't a known sentinel — this heuristic shouldn't guess.
	samples := []profiler.Value{"1001", "Texas"}
	_, ok := h.Evaluate(profiler.ColumnMeta{DeclaredType: "INTEGER"}, samples)
	if ok {
		t.Fatal("expected no opinion for unrecognized non-numeric text")
	}
}
