package heuristics

import (
	"testing"

	"sqlite2pg/internal/profiler"
)

func TestISO8601_DetectsISO8601Timestamps(t *testing.T) {
	h := ISO8601{}
	meta := profiler.ColumnMeta{Table: "austinroadconstruction", Name: "created_at", DeclaredType: "TEXT"}
	if !h.AppliesTo(meta) {
		t.Fatal("expected AppliesTo to return true for a TEXT column")
	}

	samples := []profiler.Value{"2026-08-14T18:01:38.401Z", "2026-08-01T00:00:00Z", nil}
	finding, ok := h.Evaluate(profiler.ColumnMeta{}, samples)
	if !ok {
		t.Fatal("expected a finding for ISO 8601 timestamp strings")
	}
	if finding.SuggestedType != "timestamptz" {
		t.Errorf("expected suggested type timestamptz, got %q", finding.SuggestedType)
	}
}

func TestISO8601_NoOpinionOnNonTimestampText(t *testing.T) {
	h := ISO8601{}
	samples := []profiler.Value{"Austin", "Texas"}
	_, ok := h.Evaluate(profiler.ColumnMeta{}, samples)
	if ok {
		t.Fatal("expected no opinion for plain text that isn't a timestamp")
	}
}
