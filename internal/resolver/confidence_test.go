package resolver

import (
	"testing"

	"sqlite2pg/internal/profiler"
)

func TestDecide_AutoApprovesASingleHighConfidenceFinding(t *testing.T) {
	findings := []profiler.Finding{
		{Heuristic: "esri_typename_mapping", SuggestedType: "integer", Confidence: 0.99},
	}
	decision, needsReview := Decide(findings, 0.9)
	if needsReview {
		t.Fatal("expected a single 0.99-confidence finding to be auto-approved")
	}
	if decision.SuggestedType != "integer" {
		t.Errorf("expected decision type integer, got %q", decision.SuggestedType)
	}
}

func TestDecide_FlagsBelowThresholdConfidenceForReview(t *testing.T) {
	findings := []profiler.Finding{
		{Heuristic: "boolean01", SuggestedType: "boolean", Confidence: 0.55},
	}
	_, needsReview := Decide(findings, 0.9)
	if !needsReview {
		t.Fatal("expected a 0.55-confidence finding below the 0.9 threshold to need review")
	}
}

func TestDecide_FlagsNoFindingsForReview(t *testing.T) {
	_, needsReview := Decide(nil, 0.9)
	if !needsReview {
		t.Fatal("expected zero findings to need review")
	}
}

func TestDecide_FlagsCloseDisagreementForReviewEvenAboveThreshold(t *testing.T) {
	// Gap of 0.02 — inside disagreementMargin (0.04) — so these two
	// findings are genuinely too close to call.
	findings := []profiler.Finding{
		{Heuristic: "unix_epoch_seconds", SuggestedType: "timestamptz", Confidence: 0.85},
		{Heuristic: "comma_formatted_number", SuggestedType: "integer", Confidence: 0.83},
	}
	_, needsReview := Decide(findings, 0.5)
	if !needsReview {
		t.Fatal("expected two closely-competing findings to need review despite both being above threshold")
	}
}

// TestDecide_ExactTieForcesReview covers the genuine-tie case the
// persistence mechanism (issue #20 bug 2) depends on: two findings at
// identical confidence must still force review even after the margin
// shrinks to accommodate the real heuristic ladder's gaps.
func TestDecide_ExactTieForcesReview(t *testing.T) {
	findings := []profiler.Finding{
		{Heuristic: "some_heuristic", SuggestedType: "integer", Confidence: 0.9},
		{Heuristic: "other_heuristic", SuggestedType: "date", Confidence: 0.9},
	}
	_, needsReview := Decide(findings, 0.5)
	if !needsReview {
		t.Fatal("expected an exact confidence tie between two findings to need review")
	}
}

// TestDecide_YYYYMMDDMarginOverNumericTextWinsCleanly reproduces the issue
// #20 bug 1 scenario exactly: yyyymmdd_date's 0.95 confidence is
// deliberately spaced above numeric_text's 0.90 so it "must win outright
// rather than tie and force review" (see yyyymmdd_date.go's comment on its
// Evaluate method) — but the old disagreementMargin of 0.1 was wider than
// this real ladder gap, and float subtraction (0.95-0.90 ==
// 0.049999999999999933) made even a fixed-but-still-too-wide margin risky
// at the boundary. Neither should force review here.
func TestDecide_YYYYMMDDMarginOverNumericTextWinsCleanly(t *testing.T) {
	findings := []profiler.Finding{
		{Heuristic: "yyyymmdd_date", SuggestedType: "date", Confidence: 0.95},
		{Heuristic: "numeric_text", SuggestedType: "integer", Confidence: 0.90},
	}
	decision, needsReview := Decide(findings, 0.9)
	if needsReview {
		t.Fatal("expected yyyymmdd_date's 0.95 to win outright over numeric_text's 0.90 without forcing review")
	}
	if decision.SuggestedType != "date" {
		t.Errorf("expected yyyymmdd_date's date decision to win, got %q", decision.SuggestedType)
	}
}

func TestDecide_PicksHighestConfidenceAsThePrimaryDecision(t *testing.T) {
	findings := []profiler.Finding{
		{Heuristic: "boolean01", SuggestedType: "boolean", Confidence: 0.55},
		{Heuristic: "comma_formatted_number", SuggestedType: "integer", Confidence: 0.95},
	}
	decision, _ := Decide(findings, 0.9)
	if decision.SuggestedType != "integer" {
		t.Errorf("expected the highest-confidence finding (integer) to win, got %q", decision.SuggestedType)
	}
}
