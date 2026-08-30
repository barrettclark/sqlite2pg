package heuristics

import (
	"testing"

	"sqlite2pg/internal/profiler"
)

func TestNumericText_AppliesToTextDeclaredColumns(t *testing.T) {
	h := NumericText{}
	cases := []struct {
		meta   profiler.ColumnMeta
		expect bool
	}{
		{profiler.ColumnMeta{Name: "current_employees", DeclaredType: "TEXT"}, true},
		{profiler.ColumnMeta{Name: "current_employees", DeclaredType: "VARCHAR(20)"}, true},
		{profiler.ColumnMeta{Name: "current_employees", DeclaredType: "INTEGER"}, false},
	}
	for _, c := range cases {
		if got := h.AppliesTo(c.meta); got != c.expect {
			t.Errorf("AppliesTo(%+v) = %v, want %v", c.meta, got, c.expect)
		}
	}
}

func TestNumericText_DetectsPlainIntegerStringsWithNoCommaFormatting(t *testing.T) {
	// This is real data (companies.db): current_employees/total_employees
	// are TEXT columns storing plain digit strings that never happen to
	// use comma formatting, so comma_formatted_number's own
	// comma-or-nothing evidence requirement never fires for them.
	h := NumericText{}
	samples := []profiler.Value{"24", "1", "0", "5"}
	finding, ok := h.Evaluate(profiler.ColumnMeta{}, samples)
	if !ok {
		t.Fatal("expected a finding for plain integer strings")
	}
	if finding.SuggestedType != "integer" {
		t.Errorf("expected suggested type integer, got %q", finding.SuggestedType)
	}
	if finding.TransformExpr != "numeric_text_to_integer" {
		t.Errorf("expected transform numeric_text_to_integer, got %q", finding.TransformExpr)
	}
}

func TestNumericText_DetectsWholeNumberFloatStringsAsInteger(t *testing.T) {
	// Real data (companies.db): year_founded stores "1998.0" style
	// values — a float-formatted whole number, still semantically an
	// integer year.
	h := NumericText{}
	samples := []profiler.Value{"1998.0", "2013.0", "1979.0"}
	finding, ok := h.Evaluate(profiler.ColumnMeta{}, samples)
	if !ok {
		t.Fatal("expected a finding for whole-number float strings")
	}
	if finding.SuggestedType != "integer" {
		t.Errorf("expected suggested type integer, got %q", finding.SuggestedType)
	}
	if finding.TransformExpr != "numeric_text_to_integer" {
		t.Errorf("expected transform numeric_text_to_integer, got %q", finding.TransformExpr)
	}
}

func TestNumericText_TargetsDoublePrecisionWhenAnyValueHasARealFraction(t *testing.T) {
	h := NumericText{}
	samples := []profiler.Value{"1998.0", "18.5", "2013.0"}
	finding, ok := h.Evaluate(profiler.ColumnMeta{}, samples)
	if !ok {
		t.Fatal("expected a finding")
	}
	if finding.SuggestedType != "double precision" {
		t.Errorf("expected suggested type double precision, got %q", finding.SuggestedType)
	}
	if finding.TransformExpr != "numeric_text_to_double" {
		t.Errorf("expected transform numeric_text_to_double, got %q", finding.TransformExpr)
	}
}

func TestNumericText_TreatsEmptyStringAsSkippedNotDisqualifying(t *testing.T) {
	// Real data: year_founded has empty-string rows mixed with valid
	// values ("no year on file"), the same convention seen elsewhere in
	// this project (e.g. uuid_format).
	h := NumericText{}
	samples := []profiler.Value{"", "24", "", "5"}
	finding, ok := h.Evaluate(profiler.ColumnMeta{}, samples)
	if !ok {
		t.Fatal("expected a finding when non-empty samples are all numeric")
	}
	if finding.SuggestedType != "integer" {
		t.Errorf("expected suggested type integer, got %q", finding.SuggestedType)
	}
}

func TestNumericText_NoOpinionWhenAnySampleIsNotNumericText(t *testing.T) {
	h := NumericText{}
	samples := []profiler.Value{"24", "not a number", "5"}
	if _, ok := h.Evaluate(profiler.ColumnMeta{}, samples); ok {
		t.Fatal("expected no opinion when a sample isn't plain numeric text")
	}
}

func TestNumericText_NoOpinionOnAMeaningfulLeadingZero(t *testing.T) {
	// A leading zero on a multi-digit value (e.g. a zip code, "07030")
	// is real information a numeric type would silently destroy on
	// round-trip. This must disqualify the whole column rather than risk
	// corrupting a code-like column that merely happens to look numeric.
	h := NumericText{}
	samples := []profiler.Value{"24", "07030", "5"}
	if _, ok := h.Evaluate(profiler.ColumnMeta{}, samples); ok {
		t.Fatal("expected no opinion when a sample has a meaningful leading zero")
	}
}

func TestNumericText_DoesNotFlagPlainZeroOrZeroPointSomethingAsALeadingZero(t *testing.T) {
	h := NumericText{}
	samples := []profiler.Value{"0", "0.5", "-0.5"}
	if _, ok := h.Evaluate(profiler.ColumnMeta{}, samples); !ok {
		t.Fatal("expected a finding: \"0\" and \"0.5\" are not meaningful leading zeros")
	}
}

func TestNumericText_NoOpinionWhenACommaFormattedValueIsPresent(t *testing.T) {
	// Comma-formatted numbers are comma_formatted_number's territory —
	// this heuristic only handles columns with zero comma-formatted
	// evidence, so the two heuristics never both claim the same column.
	h := NumericText{}
	samples := []profiler.Value{"2,949", "24"}
	if _, ok := h.Evaluate(profiler.ColumnMeta{}, samples); ok {
		t.Fatal("expected no opinion when a comma-formatted value is present")
	}
}

func TestNumericText_NoOpinionWhenNoUsableSamples(t *testing.T) {
	h := NumericText{}
	if _, ok := h.Evaluate(profiler.ColumnMeta{}, []profiler.Value{nil, "", nil}); ok {
		t.Fatal("expected no opinion when every sample is nil or empty")
	}
	if _, ok := h.Evaluate(profiler.ColumnMeta{}, nil); ok {
		t.Fatal("expected no opinion for zero samples")
	}
}
