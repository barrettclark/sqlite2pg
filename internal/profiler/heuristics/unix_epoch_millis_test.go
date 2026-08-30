package heuristics

import (
	"testing"

	"sqlite2pg/internal/profiler"
)

func TestUnixEpochMillis_AppliesToTimestampNamedIntegerColumns(t *testing.T) {
	h := UnixEpochMillis{}
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

func TestUnixEpochMillis_DetectsPlausibleMillisecondValues(t *testing.T) {
	// 1735689600000 ms == 2025-01-01T00:00:00Z.
	h := UnixEpochMillis{}
	samples := []profiler.Value{int64(1735689600000), int64(1704067200000)}
	finding, ok := h.Evaluate(profiler.ColumnMeta{Name: "created_at"}, samples)
	if !ok {
		t.Fatal("expected a finding for plausible epoch-millisecond values")
	}
	if finding.SuggestedType != "timestamptz" {
		t.Errorf("expected suggested type timestamptz, got %q", finding.SuggestedType)
	}
	if finding.TransformExpr != "unix_epoch_millis" {
		t.Errorf("expected transform unix_epoch_millis, got %q", finding.TransformExpr)
	}
}

func TestUnixEpochMillis_NoOpinionForPlainEpochSecondsValues(t *testing.T) {
	// A genuine epoch-seconds value (10 digits) is three orders of
	// magnitude below the plausible millisecond range, so it must not be
	// misclassified as milliseconds.
	h := UnixEpochMillis{}
	samples := []profiler.Value{int64(1735689600), int64(1704067200)}
	if _, ok := h.Evaluate(profiler.ColumnMeta{Name: "created_at"}, samples); ok {
		t.Fatal("expected no opinion for plain epoch-seconds magnitude values")
	}
}

func TestUnixEpochMillis_NoOpinionWhenNoUsableSamples(t *testing.T) {
	h := UnixEpochMillis{}
	if _, ok := h.Evaluate(profiler.ColumnMeta{Name: "created_at"}, nil); ok {
		t.Fatal("expected no opinion for zero samples")
	}
}
