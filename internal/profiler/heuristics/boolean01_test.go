package heuristics

import (
	"testing"

	"sqlite2pg/internal/profiler"
)

func TestBoolean01_DetectsZeroOneCardinality(t *testing.T) {
	h := Boolean01{}
	meta := profiler.ColumnMeta{Table: "bikes", Name: "is_installed", DeclaredType: "INTEGER"}
	if !h.AppliesTo(meta) {
		t.Fatal("expected AppliesTo to return true for an INTEGER column")
	}

	samples := []profiler.Value{int64(0), int64(1), int64(1), int64(0), nil}
	finding, ok := h.Evaluate(profiler.ColumnMeta{}, samples)
	if !ok {
		t.Fatal("expected a finding for a 0/1/NULL-only integer column")
	}
	if finding.SuggestedType != "boolean" {
		t.Errorf("expected suggested type boolean, got %q", finding.SuggestedType)
	}
	if finding.TransformExpr != "int_to_bool" {
		t.Errorf("expected transform int_to_bool, got %q", finding.TransformExpr)
	}
	// This is the canonical ambiguous case (numeric code vs. real boolean) —
	// confidence must stay moderate so the resolver routes it to human review
	// rather than auto-approving it.
	if finding.Confidence >= 0.9 {
		t.Errorf("expected moderate, non-auto-approve confidence, got %f", finding.Confidence)
	}
}

func TestBoolean01_NoOpinionWhenOtherValuesPresent(t *testing.T) {
	h := Boolean01{}
	samples := []profiler.Value{int64(0), int64(1), int64(2)}
	_, ok := h.Evaluate(profiler.ColumnMeta{}, samples)
	if ok {
		t.Fatal("expected no opinion when values outside {0,1,NULL} are present")
	}
}
