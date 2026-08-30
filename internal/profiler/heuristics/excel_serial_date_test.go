package heuristics

import (
	"testing"

	"sqlite2pg/internal/profiler"
)

func TestExcelSerialDate_AppliesToDateNamedNumericColumns(t *testing.T) {
	h := ExcelSerialDate{}
	cases := []struct {
		meta   profiler.ColumnMeta
		expect bool
	}{
		{profiler.ColumnMeta{Name: "hire_date", DeclaredType: "INTEGER"}, true},
		{profiler.ColumnMeta{Name: "hire_date", DeclaredType: "REAL"}, true},
		{profiler.ColumnMeta{Name: "created_at", DeclaredType: "REAL"}, true},
		{profiler.ColumnMeta{Name: "notes", DeclaredType: "REAL"}, false},
		{profiler.ColumnMeta{Name: "hire_date", DeclaredType: "TEXT"}, false},
	}
	for _, c := range cases {
		if got := h.AppliesTo(c.meta); got != c.expect {
			t.Errorf("AppliesTo(%+v) = %v, want %v", c.meta, got, c.expect)
		}
	}
}

func TestExcelSerialDate_DetectsPlausibleSerialValues(t *testing.T) {
	// 44197 is the Excel serial number for 2021-01-01; 25569 is 1970-01-01.
	h := ExcelSerialDate{}
	samples := []profiler.Value{float64(44197), float64(25569), float64(40000.5)}
	finding, ok := h.Evaluate(profiler.ColumnMeta{Name: "hire_date"}, samples)
	if !ok {
		t.Fatal("expected a finding for plausible Excel serial date values")
	}
	if finding.SuggestedType != "timestamptz" {
		t.Errorf("expected suggested type timestamptz, got %q", finding.SuggestedType)
	}
	if finding.TransformExpr != "excel_serial_to_timestamptz" {
		t.Errorf("expected transform excel_serial_to_timestamptz, got %q", finding.TransformExpr)
	}
}

func TestExcelSerialDate_NoOpinionWhenValuesAreOutOfRange(t *testing.T) {
	// These look like ordinary small integers (e.g. a count or an ID), not
	// Excel serial dates that would land in a plausible calendar range.
	h := ExcelSerialDate{}
	samples := []profiler.Value{float64(1), float64(2), float64(3)}
	if _, ok := h.Evaluate(profiler.ColumnMeta{Name: "hire_date"}, samples); ok {
		t.Fatal("expected no opinion for out-of-range values")
	}
}

func TestExcelSerialDate_NoOpinionWhenNoUsableSamples(t *testing.T) {
	h := ExcelSerialDate{}
	if _, ok := h.Evaluate(profiler.ColumnMeta{Name: "hire_date"}, []profiler.Value{nil, nil}); ok {
		t.Fatal("expected no opinion when every sample is nil")
	}
	if _, ok := h.Evaluate(profiler.ColumnMeta{Name: "hire_date"}, nil); ok {
		t.Fatal("expected no opinion for zero samples")
	}
}
