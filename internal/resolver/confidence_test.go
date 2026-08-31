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

// TestDecide_TextBoolean01ForcesReviewAgainstNumericText reproduces the
// sakila.db customer.active shape (issue #1): a CHAR(1) column storing
// only "0"/"1" gets a boolean01 finding at 0.88 and a competing
// numeric_text finding at 0.90. The gap (0.02) must land inside the 0.04
// disagreement margin so the resolver forces review instead of letting
// numeric_text's 0.90 win outright and silently auto-approve as plain
// integer with zero review signal — the exact real-data gap this issue
// reported.
func TestDecide_TextBoolean01ForcesReviewAgainstNumericText(t *testing.T) {
	findings := []profiler.Finding{
		{Heuristic: "boolean01", SuggestedType: "boolean", Confidence: 0.88},
		{Heuristic: "numeric_text", SuggestedType: "integer", Confidence: 0.90},
	}
	_, needsReview := Decide(findings, 0.9)
	if !needsReview {
		t.Fatal("expected boolean01's 0.88 vs. numeric_text's 0.90 to disagree closely enough to force review")
	}
}

// TestDecide_9995GapResolvesCleanlyRegardlessOfFloatRepresentation
// reproduces issue #48 bug 2: 0.99-0.95 happens to evaluate to
// 0.040000000000000036 in Go (landing just above the old
// disagreementMargin-1e-9 boundary by luck of which direction the float
// rounding error went), while the nominally-identical-sized gap
// 0.95-0.90 evaluates to 0.049999999999999933 (rounding the other way).
// A correct fix must resolve the same nominal gap the same way no matter
// how the confidence values were constructed bit-for-bit, so this test
// feeds in several float64 pairs that all nominally represent a 0.04 gap
// (i.e. 0.95 vs 0.99) but are built via different arithmetic paths, and
// asserts every single one resolves cleanly (no forced review).
func TestDecide_9995GapResolvesCleanlyRegardlessOfFloatRepresentation(t *testing.T) {
	// Runtime variables (not const expressions) so the Go compiler can't
	// constant-fold these to identical bit patterns at compile time — each
	// must actually go through float64 arithmetic at test-run time to be a
	// real test of representation independence.
	one, hundred := 1.0, 100.0
	ninetyNine, ninetyFive := 99.0, 95.0
	viaSubtraction := 1.0
	for i := 0; i < 1; i++ {
		viaSubtraction -= 0.01
	}
	viaDivision99 := ninetyNine / hundred
	viaDivision95 := ninetyFive / hundred
	var viaSum99, viaSum95 float64
	parts99 := []float64{0.33, 0.33, 0.33}
	for _, x := range parts99 {
		viaSum99 += x
	}
	parts95 := []float64{0.19, 0.19, 0.19, 0.19, 0.19}
	for _, x := range parts95 {
		viaSum95 += x
	}

	pairs := [][2]float64{
		{0.99, 0.95},                   // plain literals
		{viaSubtraction, one - 0.05},   // constructed via runtime subtraction
		{viaDivision99, viaDivision95}, // constructed via runtime division
		{viaSum99, viaSum95},           // constructed via runtime summation
	}
	for _, p := range pairs {
		findings := []profiler.Finding{
			{Heuristic: "esri_typename_mapping", SuggestedType: "integer", Confidence: p[0]},
			{Heuristic: "comma_formatted_number", SuggestedType: "integer", Confidence: p[1]},
		}
		_, needsReview := Decide(findings, 0.5)
		if needsReview {
			t.Errorf("confidence pair %v: expected the ~0.04 gap to resolve cleanly regardless of float representation, but review was forced", p)
		}
	}
}

// TestDecide_8885GapResolvesCleanlyAfterRespacing reproduces issue #48
// bug 1: boolean01's TEXT/CHAR rung (0.88) sits only 0.03 away from the
// INT-only 0.85 rung shared by sentinel_null, unix_epoch*, and
// excel_serial_date. Under the old disagreementMargin (0.04) that 0.03
// gap fell *inside* the margin, so a 0.88 boolean01 finding and an 0.85
// finding were defined to "disagree" by construction — latent today only
// because no heuristic pair at those two confidences can currently
// co-occur on the same column, but exactly the kind of gap a future
// heuristic could trip over silently. After respacing, this gap must
// resolve cleanly (the 0.88 finding wins outright, no forced review).
func TestDecide_8885GapResolvesCleanlyAfterRespacing(t *testing.T) {
	findings := []profiler.Finding{
		{Heuristic: "boolean01", SuggestedType: "boolean", Confidence: 0.88},
		{Heuristic: "sentinel_null", SuggestedType: "integer", Confidence: 0.85},
	}
	decision, needsReview := Decide(findings, 0.5)
	if needsReview {
		t.Fatal("expected boolean01's 0.88 to beat sentinel_null's 0.85 cleanly without forcing review")
	}
	if decision.SuggestedType != "boolean" {
		t.Errorf("expected boolean01's 0.88 finding to win, got %q", decision.SuggestedType)
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
