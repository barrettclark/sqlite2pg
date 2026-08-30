package heuristics

import (
	"testing"

	"sqlite2pg/internal/profiler"
)

func TestUnixEpochMicros_AppliesToTimestampNamedIntegerColumns(t *testing.T) {
	h := UnixEpochMicros{}
	cases := []struct {
		meta   profiler.ColumnMeta
		expect bool
	}{
		{profiler.ColumnMeta{Name: "created_at", DeclaredType: "INTEGER"}, true},
		{profiler.ColumnMeta{Name: "notes", DeclaredType: "INTEGER"}, false},
		{profiler.ColumnMeta{Name: "created_at", DeclaredType: "TEXT"}, false},
	}
	for _, c := range cases {
		if got := h.AppliesTo(c.meta); got != c.expect {
			t.Errorf("AppliesTo(%+v) = %v, want %v", c.meta, got, c.expect)
		}
	}
}

func TestUnixEpochMicros_DetectsPlausibleMicrosecondValues(t *testing.T) {
	// 1735689600000000 us == 2025-01-01T00:00:00Z.
	h := UnixEpochMicros{}
	samples := []profiler.Value{int64(1735689600000000), int64(1704067200000000)}
	finding, ok := h.Evaluate(profiler.ColumnMeta{Name: "created_at"}, samples)
	if !ok {
		t.Fatal("expected a finding for plausible epoch-microsecond values")
	}
	if finding.SuggestedType != "timestamptz" {
		t.Errorf("expected suggested type timestamptz, got %q", finding.SuggestedType)
	}
	if finding.TransformExpr != "unix_epoch_micros" {
		t.Errorf("expected transform unix_epoch_micros, got %q", finding.TransformExpr)
	}
}

func TestUnixEpochMicros_NoOpinionForPlainEpochMillisValues(t *testing.T) {
	h := UnixEpochMicros{}
	samples := []profiler.Value{int64(1735689600000), int64(1704067200000)}
	if _, ok := h.Evaluate(profiler.ColumnMeta{Name: "created_at"}, samples); ok {
		t.Fatal("expected no opinion for plain epoch-millisecond magnitude values")
	}
}

func TestUnixEpochMicros_NoOpinionWhenNoUsableSamples(t *testing.T) {
	h := UnixEpochMicros{}
	if _, ok := h.Evaluate(profiler.ColumnMeta{Name: "created_at"}, nil); ok {
		t.Fatal("expected no opinion for zero samples")
	}
}
