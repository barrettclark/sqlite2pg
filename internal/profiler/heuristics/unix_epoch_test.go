package heuristics

import (
	"testing"

	"sqlite2pg/internal/profiler"
)

func TestUnixEpoch_AppliesToTimestampNamedIntegerColumns(t *testing.T) {
	h := UnixEpoch{}
	cases := []struct {
		meta   profiler.ColumnMeta
		expect bool
	}{
		{profiler.ColumnMeta{Name: "last_reported", DeclaredType: "INTEGER"}, true},
		{profiler.ColumnMeta{Name: "created_at", DeclaredType: "INTEGER"}, true},
		{profiler.ColumnMeta{Name: "num_scooters_available", DeclaredType: "INTEGER"}, false},
		{profiler.ColumnMeta{Name: "last_reported", DeclaredType: "TEXT"}, false},
	}
	for _, c := range cases {
		if got := h.AppliesTo(c.meta); got != c.expect {
			t.Errorf("AppliesTo(%+v) = %v, want %v", c.meta, got, c.expect)
		}
	}
}

func TestUnixEpoch_DetectsPlausibleEpochSecondsWithOutlierTolerance(t *testing.T) {
	h := UnixEpoch{}
	// mostly plausible 2021-ish epoch seconds, with the documented bikes.db
	// edge case of one row at 86400 (1970-01-02) mixed in.
	samples := []profiler.Value{int64(1620000000), int64(1620003600), int64(1620007200), int64(86400)}
	finding, ok := h.Evaluate(profiler.ColumnMeta{}, samples)
	if !ok {
		t.Fatal("expected a finding for mostly-plausible epoch values")
	}
	if finding.SuggestedType != "timestamptz" {
		t.Errorf("expected suggested type timestamptz, got %q", finding.SuggestedType)
	}
	if finding.TransformExpr != "unix_epoch_seconds" {
		t.Errorf("expected transform unix_epoch_seconds, got %q", finding.TransformExpr)
	}
}

func TestUnixEpoch_NoOpinionWhenValuesImplausible(t *testing.T) {
	h := UnixEpoch{}
	// small counts, not remotely epoch-shaped
	samples := []profiler.Value{int64(1), int64(2), int64(3), int64(4)}
	_, ok := h.Evaluate(profiler.ColumnMeta{}, samples)
	if ok {
		t.Fatal("expected no opinion for values outside any plausible epoch range")
	}
}
