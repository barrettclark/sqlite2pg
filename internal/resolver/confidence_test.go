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
	findings := []profiler.Finding{
		{Heuristic: "unix_epoch_seconds", SuggestedType: "timestamptz", Confidence: 0.85},
		{Heuristic: "comma_formatted_number", SuggestedType: "integer", Confidence: 0.8},
	}
	_, needsReview := Decide(findings, 0.5)
	if !needsReview {
		t.Fatal("expected two closely-competing findings to need review despite both being above threshold")
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
