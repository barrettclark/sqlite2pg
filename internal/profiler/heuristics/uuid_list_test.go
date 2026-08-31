package heuristics

import (
	"testing"

	"sqlite2pg/internal/profiler"
)

func TestUUIDList_AppliesToTextAndCharColumns(t *testing.T) {
	h := UUIDList{}
	for _, declared := range []string{"TEXT", "VARCHAR(36)", "CHAR(36)"} {
		if !h.AppliesTo(profiler.ColumnMeta{DeclaredType: declared}) {
			t.Errorf("expected AppliesTo(%q) to be true", declared)
		}
	}
	if h.AppliesTo(profiler.ColumnMeta{DeclaredType: "INTEGER"}) {
		t.Error("expected AppliesTo(INTEGER) to be false")
	}
}

func TestUUIDList_DetectsMixOfSingleAndMultiValueUUIDs(t *testing.T) {
	// beets' arrangers_ids/remixers_ids shape: some rows are one UUID,
	// some rows are several NUL-joined — the sample itself demonstrates
	// the list shape via at least one multi-value row.
	h := UUIDList{}
	samples := []profiler.Value{
		"90b141b9-c39f-4a26-8f5d-9d3c1e2a7b10",
		"cc75b164-273c-4dce-9cdf-292045a0d38b\x003422ac1a-8dbb-4f23-a337-0bd0a0150022",
		nil,
	}
	finding, ok := h.Evaluate(profiler.ColumnMeta{}, samples)
	if !ok {
		t.Fatal("expected a finding when at least one sample is a multi-value NUL-joined UUID list")
	}
	if finding.SuggestedType != "uuid[]" {
		t.Errorf("expected suggested type uuid[], got %q", finding.SuggestedType)
	}
	if finding.TransformExpr != "uuid_list_format" {
		t.Errorf("expected transform uuid_list_format, got %q", finding.TransformExpr)
	}
	if finding.Confidence != 0.9 {
		t.Errorf("expected confidence 0.9, got %v", finding.Confidence)
	}
}

func TestUUIDList_NoOpinionWhenNoSampleIsMultiValue(t *testing.T) {
	// All-single-UUID samples belong to uuid_format's territory —
	// uuid_list must not steal a column that never demonstrates the list
	// shape in its own sample (this is the whole point of the design: it
	// only fires on direct sample evidence, never speculation).
	h := UUIDList{}
	samples := []profiler.Value{
		"90b141b9-c39f-4a26-8f5d-9d3c1e2a7b10",
		"e4eff6f3-3f1a-4d6e-9c1e-7c3d2a5b9e10",
	}
	if _, ok := h.Evaluate(profiler.ColumnMeta{}, samples); ok {
		t.Fatal("expected no opinion when no sample demonstrates the multi-value list shape")
	}
}

func TestUUIDList_DetectsRealBeetsEscapedSeparator(t *testing.T) {
	// The real beets_library.db evidence for this issue doesn't use a raw
	// NUL (0x00) byte between UUIDs as the original issue writeup assumed
	// — a byte-for-byte hex comparison found the actual separator is the
	// literal escaped form "\␀" (backslash + U+2400 SYMBOL FOR NULL, 4
	// bytes), confirmed in docs/superpowers/plans/
	// audit-phase2c-beets-results.md. This heuristic must recognize that
	// real form too, not just a hypothetical raw-NUL column.
	h := UUIDList{}
	samples := []profiler.Value{
		"7113aab7-628f-4050-ae49-dbecac110ca8\\␀a5d79c54-81c3-4a73-af6a-ad5c143d3f21",
	}
	finding, ok := h.Evaluate(profiler.ColumnMeta{}, samples)
	if !ok {
		t.Fatal("expected a finding for the real beets \\␀-separated multi-value form")
	}
	if finding.SuggestedType != "uuid[]" {
		t.Errorf("expected suggested type uuid[], got %q", finding.SuggestedType)
	}
}

func TestUUIDList_NoOpinionWhenAnyPartIsInvalid(t *testing.T) {
	h := UUIDList{}
	samples := []profiler.Value{
		"cc75b164-273c-4dce-9cdf-292045a0d38b\x00not-a-uuid",
	}
	if _, ok := h.Evaluate(profiler.ColumnMeta{}, samples); ok {
		t.Fatal("expected no opinion when a NUL-joined part isn't a valid UUID")
	}
}

func TestUUIDList_NoOpinionOnAllNullOrEmptySamples(t *testing.T) {
	h := UUIDList{}
	samples := []profiler.Value{nil, nil, ""}
	if _, ok := h.Evaluate(profiler.ColumnMeta{}, samples); ok {
		t.Fatal("expected no opinion when every sample is NULL or empty")
	}
}

func TestUUIDList_NoOpinionOnTrailingNULSeparator(t *testing.T) {
	// A trailing (or leading, or doubled) NUL produces an empty part once
	// split, which is not a valid UUID — malformed, so this must not
	// match rather than silently dropping the empty element.
	h := UUIDList{}
	samples := []profiler.Value{
		"cc75b164-273c-4dce-9cdf-292045a0d38b\x003422ac1a-8dbb-4f23-a337-0bd0a0150022\x00",
	}
	if _, ok := h.Evaluate(profiler.ColumnMeta{}, samples); ok {
		t.Fatal("expected no opinion for a trailing NUL separator producing an empty part")
	}
}
