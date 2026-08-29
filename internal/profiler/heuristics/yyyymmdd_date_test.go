package heuristics

import (
	"testing"

	"sqlite2pg/internal/profiler"
)

func TestYYYYMMDDDate_AppliesToDateNamedIntegerAndTextColumns(t *testing.T) {
	h := YYYYMMDDDate{}
	cases := []struct {
		meta   profiler.ColumnMeta
		expect bool
	}{
		{profiler.ColumnMeta{Name: "CREATION DATE", DeclaredType: "INTEGER"}, true},
		{profiler.ColumnMeta{Name: "LAST VALIDATION DATE", DeclaredType: "TEXT"}, true},
		{profiler.ColumnMeta{Name: "EXPIRY DATE", DeclaredType: "TEXT"}, true},
		{profiler.ColumnMeta{Name: "MIC", DeclaredType: "TEXT"}, false},
		{profiler.ColumnMeta{Name: "CREATION DATE", DeclaredType: "REAL"}, false},
	}
	for _, c := range cases {
		if got := h.AppliesTo(c.meta); got != c.expect {
			t.Errorf("AppliesTo(%+v) = %v, want %v", c.meta, got, c.expect)
		}
	}
}

func TestYYYYMMDDDate_DetectsIntegerYYYYMMDDValues(t *testing.T) {
	h := YYYYMMDDDate{}
	samples := []profiler.Value{int64(20210927), int64(20090427), int64(20061225)}
	finding, ok := h.Evaluate(profiler.ColumnMeta{}, samples)
	if !ok {
		t.Fatal("expected a finding for valid 8-digit YYYYMMDD integers")
	}
	if finding.SuggestedType != "date" {
		t.Errorf("expected suggested type date, got %q", finding.SuggestedType)
	}
	if finding.TransformExpr != "yyyymmdd_to_date" {
		t.Errorf("expected transform yyyymmdd_to_date, got %q", finding.TransformExpr)
	}
}

func TestYYYYMMDDDate_DetectsStringYYYYMMDDValues(t *testing.T) {
	h := YYYYMMDDDate{}
	samples := []profiler.Value{"20210927", "20210823", "20220926"}
	finding, ok := h.Evaluate(profiler.ColumnMeta{}, samples)
	if !ok {
		t.Fatal("expected a finding for valid 8-digit YYYYMMDD strings")
	}
	if finding.SuggestedType != "date" {
		t.Errorf("expected suggested type date, got %q", finding.SuggestedType)
	}
}

func TestYYYYMMDDDate_TreatsNilAsSkippedNotDisqualifying(t *testing.T) {
	// EXPIRY DATE in the real source has NULL rows mixed with valid
	// 8-digit dates — NULLs must not disqualify the column.
	h := YYYYMMDDDate{}
	samples := []profiler.Value{nil, "20210823", nil, "20220926"}
	finding, ok := h.Evaluate(profiler.ColumnMeta{}, samples)
	if !ok {
		t.Fatal("expected a finding when non-nil samples are all valid dates")
	}
	if finding.SuggestedType != "date" {
		t.Errorf("expected suggested type date, got %q", finding.SuggestedType)
	}
}

func TestYYYYMMDDDate_NoOpinionWhenAnySampleIsNotAValidCalendarDate(t *testing.T) {
	// A single bad value (e.g. a real placeholder like the literal string
	// "YYYYMMDD", or an invalid calendar date such as month 13) must
	// disqualify the column entirely — this heuristic only fires when
	// every sample would actually load, since the transform it assigns
	// has no fallback for a value it can't parse.
	h := YYYYMMDDDate{}
	samples := []profiler.Value{"20210927", "YYYYMMDD", "20220926"}
	if _, ok := h.Evaluate(profiler.ColumnMeta{}, samples); ok {
		t.Fatal("expected no opinion when a sample isn't a valid YYYYMMDD date")
	}

	samples = []profiler.Value{"20211301"} // month 13
	if _, ok := h.Evaluate(profiler.ColumnMeta{}, samples); ok {
		t.Fatal("expected no opinion for an invalid calendar date (month 13)")
	}
}

func TestYYYYMMDDDate_NoOpinionWhenNoUsableSamples(t *testing.T) {
	h := YYYYMMDDDate{}
	if _, ok := h.Evaluate(profiler.ColumnMeta{}, []profiler.Value{nil, nil}); ok {
		t.Fatal("expected no opinion when every sample is nil")
	}
	if _, ok := h.Evaluate(profiler.ColumnMeta{}, nil); ok {
		t.Fatal("expected no opinion for zero samples")
	}
}
