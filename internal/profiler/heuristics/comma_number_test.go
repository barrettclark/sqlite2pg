package heuristics

import (
	"testing"

	"sqlite2pg/internal/profiler"
)

func TestCommaNumber_DetectsCommaFormattedIntegers(t *testing.T) {
	h := CommaNumber{}
	meta := profiler.ColumnMeta{Table: "disabilitycompbycounty", Name: "count", DeclaredType: "INTEGER"}
	if !h.AppliesTo(meta) {
		t.Fatal("expected AppliesTo to return true for an INTEGER column")
	}

	samples := []profiler.Value{"2,949", "1,024", "500", nil}
	finding, ok := h.Evaluate(profiler.ColumnMeta{}, samples)
	if !ok {
		t.Fatal("expected a finding for comma-formatted numbers")
	}
	if finding.SuggestedType != "integer" {
		t.Errorf("expected suggested type integer, got %q", finding.SuggestedType)
	}
	if finding.TransformExpr != "strip_commas" {
		t.Errorf("expected transform strip_commas, got %q", finding.TransformExpr)
	}
	if finding.Confidence < 0.9 {
		t.Errorf("expected high confidence for unambiguous comma numbers, got %f", finding.Confidence)
	}
}

func TestCommaNumber_NoOpinionWhenNoCommasPresent(t *testing.T) {
	h := CommaNumber{}
	samples := []profiler.Value{"500", "12", int64(3)}
	_, ok := h.Evaluate(profiler.ColumnMeta{}, samples)
	if ok {
		t.Fatal("expected no opinion when no sampled value contains a comma")
	}
}

func TestCommaNumber_NoOpinionOnNonNumericText(t *testing.T) {
	h := CommaNumber{}
	samples := []profiler.Value{"hello, world", "foo, bar"}
	_, ok := h.Evaluate(profiler.ColumnMeta{}, samples)
	if ok {
		t.Fatal("expected no opinion for text that merely contains a comma but isn't numeric")
	}
}
