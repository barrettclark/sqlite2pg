package heuristics

import (
	"testing"

	"sqlite2pg/internal/profiler"
)

func TestBoolean01_DetectsZeroOneCardinality(t *testing.T) {
	h := Boolean01{}
	meta := profiler.ColumnMeta{Table: "bikes", Name: "is_installed", DeclaredType: "INTEGER"}
	if !h.AppliesTo(meta) {
		t.Fatal("expected AppliesTo to return true for an INTEGER column")
	}

	samples := []profiler.Value{int64(0), int64(1), int64(1), int64(0), nil}
	finding, ok := h.Evaluate(profiler.ColumnMeta{}, samples)
	if !ok {
		t.Fatal("expected a finding for a 0/1/NULL-only integer column")
	}
	if finding.SuggestedType != "boolean" {
		t.Errorf("expected suggested type boolean, got %q", finding.SuggestedType)
	}
	if finding.TransformExpr != "int_to_bool" {
		t.Errorf("expected transform int_to_bool, got %q", finding.TransformExpr)
	}
	// This is the canonical ambiguous case (numeric code vs. real boolean) —
	// confidence must stay moderate so the resolver routes it to human review
	// rather than auto-approving it.
	if finding.Confidence >= 0.9 {
		t.Errorf("expected moderate, non-auto-approve confidence, got %f", finding.Confidence)
	}
}

func TestBoolean01_DoesNotApplyToIdSuffixedColumns(t *testing.T) {
	// Real bug (issue #11, found against a beets music library):
	// discogs_artistid and discogs_labelid are real Discogs numeric IDs
	// that happened to sample as all-0 in a 500-row sample, and got
	// flagged as ambiguous boolean candidates. No reasonable person names
	// a boolean column "discogs_artistid" — id/_id-suffixed columns are
	// foreign-key-shaped, not boolean-shaped, regardless of what values a
	// sample happens to catch.
	h := Boolean01{}
	cases := []struct {
		name   string
		expect bool
	}{
		{"discogs_artistid", false},
		{"discogs_labelid", false},
		{"CustomerId", false},
		{"customer_id", false},
		{"ID", false},
		{"is_installed", true},
		{"identity_flag", true}, // contains "id" but doesn't end with it
	}
	for _, c := range cases {
		meta := profiler.ColumnMeta{Name: c.name, DeclaredType: "INTEGER"}
		if got := h.AppliesTo(meta); got != c.expect {
			t.Errorf("AppliesTo(%q) = %v, want %v", c.name, got, c.expect)
		}
	}
}

func TestBoolean01_NoOpinionWhenOtherValuesPresent(t *testing.T) {
	h := Boolean01{}
	samples := []profiler.Value{int64(0), int64(1), int64(2)}
	_, ok := h.Evaluate(profiler.ColumnMeta{}, samples)
	if ok {
		t.Fatal("expected no opinion when values outside {0,1,NULL} are present")
	}
}
