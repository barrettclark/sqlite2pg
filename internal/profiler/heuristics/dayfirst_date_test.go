package heuristics

import (
	"testing"

	"sqlite2pg/internal/profiler"
)

func TestDayFirstDate_AppliesToDateNamedTextColumns(t *testing.T) {
	h := DayFirstDate{}
	cases := []struct {
		meta   profiler.ColumnMeta
		expect bool
	}{
		{profiler.ColumnMeta{Name: "hire_date", DeclaredType: "TEXT"}, true},
		{profiler.ColumnMeta{Name: "start_date", DeclaredType: "VARCHAR(20)"}, true},
		{profiler.ColumnMeta{Name: "notes", DeclaredType: "TEXT"}, false},
		{profiler.ColumnMeta{Name: "hire_date", DeclaredType: "INTEGER"}, false},
	}
	for _, c := range cases {
		if got := h.AppliesTo(c.meta); got != c.expect {
			t.Errorf("AppliesTo(%+v) = %v, want %v", c.meta, got, c.expect)
		}
	}
}

func TestDayFirstDate_FiresWhenAValueProvesDayFirst(t *testing.T) {
	// "31/07/2006" could never be a valid US-style M/D/YYYY date (no month
	// 31), so it proves the whole column reads day-first.
	h := DayFirstDate{}
	samples := []profiler.Value{"31/07/2006", "02/01/2006"}
	finding, ok := h.Evaluate(profiler.ColumnMeta{Name: "hire_date"}, samples)
	if !ok {
		t.Fatal("expected a finding once a sample proves day-first")
	}
	if finding.SuggestedType != "timestamptz" {
		t.Errorf("expected suggested type timestamptz, got %q", finding.SuggestedType)
	}
	if finding.TransformExpr != "dayfirst_to_timestamptz" {
		t.Errorf("expected transform dayfirst_to_timestamptz, got %q", finding.TransformExpr)
	}
}

func TestDayFirstDate_NoOpinionWhenEveryValueIsAmbiguous(t *testing.T) {
	// Every sample here is <=12/<=12 — genuinely ambiguous with US-style
	// M/D/YYYY. Without proof, this heuristic must not fire, leaving the
	// column's classification unchanged from today's behavior.
	h := DayFirstDate{}
	samples := []profiler.Value{"02/01/2006", "05/03/2006"}
	if _, ok := h.Evaluate(profiler.ColumnMeta{Name: "hire_date"}, samples); ok {
		t.Fatal("expected no opinion when no sample disambiguates day-first from US-style")
	}
}

func TestDayFirstDate_NoOpinionWhenAnySampleFailsToParse(t *testing.T) {
	h := DayFirstDate{}
	samples := []profiler.Value{"31/07/2006", "not-a-date"}
	if _, ok := h.Evaluate(profiler.ColumnMeta{Name: "hire_date"}, samples); ok {
		t.Fatal("expected no opinion when a sample doesn't parse at all")
	}
}

func TestDayFirstDate_TreatsNilAsSkippedNotDisqualifying(t *testing.T) {
	h := DayFirstDate{}
	samples := []profiler.Value{nil, "31/07/2006", nil}
	if _, ok := h.Evaluate(profiler.ColumnMeta{Name: "hire_date"}, samples); !ok {
		t.Fatal("expected a finding when non-nil samples all prove day-first")
	}
}

func TestDayFirstDate_NoOpinionWhenNoUsableSamples(t *testing.T) {
	h := DayFirstDate{}
	if _, ok := h.Evaluate(profiler.ColumnMeta{Name: "hire_date"}, []profiler.Value{nil, nil}); ok {
		t.Fatal("expected no opinion when every sample is nil")
	}
	if _, ok := h.Evaluate(profiler.ColumnMeta{Name: "hire_date"}, nil); ok {
		t.Fatal("expected no opinion for zero samples")
	}
}
