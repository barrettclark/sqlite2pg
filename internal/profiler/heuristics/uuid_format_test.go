package heuristics

import (
	"testing"

	"sqlite2pg/internal/profiler"
)

func TestUUIDFormat_AppliesToTextAndCharColumns(t *testing.T) {
	h := UUIDFormat{}
	for _, declared := range []string{"TEXT", "VARCHAR(36)", "CHAR(36)"} {
		if !h.AppliesTo(profiler.ColumnMeta{DeclaredType: declared}) {
			t.Errorf("expected AppliesTo(%q) to be true", declared)
		}
	}
	if h.AppliesTo(profiler.ColumnMeta{DeclaredType: "INTEGER"}) {
		t.Error("expected AppliesTo(INTEGER) to be false")
	}
}

func TestUUIDFormat_DetectsCanonicalUUIDsCaseInsensitively(t *testing.T) {
	h := UUIDFormat{}
	samples := []profiler.Value{
		"90b141b9-c39f-4a26-8f5d-9d3c1e2a7b10",
		"E4EFF6F3-3F1A-4D6E-9C1E-7C3D2A5B9E10", // uppercase, still valid
		nil,
	}
	finding, ok := h.Evaluate(profiler.ColumnMeta{}, samples)
	if !ok {
		t.Fatal("expected a finding for canonical UUID text")
	}
	if finding.SuggestedType != "uuid" {
		t.Errorf("expected suggested type uuid, got %q", finding.SuggestedType)
	}
	if finding.TransformExpr != "uuid_format" {
		t.Errorf("expected transform uuid_format, got %q", finding.TransformExpr)
	}
}

func TestUUIDFormat_NoOpinionOnPlainText(t *testing.T) {
	h := UUIDFormat{}
	samples := []profiler.Value{"just some text"}
	if _, ok := h.Evaluate(profiler.ColumnMeta{}, samples); ok {
		t.Fatal("expected no opinion for non-UUID text")
	}
}

func TestUUIDFormat_NoOpinionOnMultiValueNULJoinedUUIDs(t *testing.T) {
	// A column storing more than one UUID per row (e.g. beets'
	// composers_ids, NUL-joined) must not match — it isn't a single
	// canonical UUID, and there's no uuid[] support (yet) to route it to.
	h := UUIDFormat{}
	samples := []profiler.Value{"cc75b164-273c-4dce-9cdf-292045a0d38b\x003422ac1a-8dbb-4f23-a337-0bd0a0150022"}
	if _, ok := h.Evaluate(profiler.ColumnMeta{}, samples); ok {
		t.Fatal("expected no opinion for a NUL-joined multi-UUID value")
	}
}

func TestUUIDFormat_NoOpinionWhenAnySampleFails(t *testing.T) {
	h := UUIDFormat{}
	samples := []profiler.Value{
		"90b141b9-c39f-4a26-8f5d-9d3c1e2a7b10",
		"not-a-uuid",
	}
	if _, ok := h.Evaluate(profiler.ColumnMeta{}, samples); ok {
		t.Fatal("expected no opinion when any sample isn't a valid UUID")
	}
}

func TestUUIDFormat_NoOpinionOnAllNullSamples(t *testing.T) {
	h := UUIDFormat{}
	samples := []profiler.Value{nil, nil}
	if _, ok := h.Evaluate(profiler.ColumnMeta{}, samples); ok {
		t.Fatal("expected no opinion when every sample is NULL")
	}
}
