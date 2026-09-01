// Package resolver arbitrates between the profiler's Findings for a column,
// deciding whether the top candidate is confident enough to auto-approve or
// must be escalated for human (or future LLM) review.
package resolver

import (
	"math"

	"sqlite2pg/internal/profiler"
)

// disagreementMargin is how close two top findings' confidences must be to
// count as "heuristics disagree" and force review even when both are
// individually above threshold.
//
// The current full heuristic confidence ladder, verified directly against
// every `Confidence:` literal in internal/profiler/heuristics (do not trust
// this comment blindly if a heuristic is added — re-verify against source):
//
//	0.55  boolean01 (INTEGER 0/1 case)
//	0.85  sentinel_null, unix_epoch, unix_epoch_millis, unix_epoch_micros,
//	      excel_serial_date, numeric_text (fractional-sample variant)
//	0.88  boolean01 (TEXT/CHAR 0/1 case, textConfidence)
//	0.90  dayfirst_date, geojson, iso8601, julian_day, numeric_text
//	      (integer-sample variant), uuid_format, uuid_list
//	0.95  comma_number, yyyymmdd_date
//	0.99  esri_typename
//
// The gaps between adjacent rungs are NOT uniformly "meant to resolve
// cleanly": the ladder deliberately includes both kinds —
//   - intentional CLEAN-WIN gaps, where the higher rung must beat the lower
//     one outright with no forced review (e.g. yyyymmdd_date's 0.95 is
//     spaced 0.05 above numeric_text's 0.90 specifically so it wins outright
//     rather than tying — see yyyymmdd_date.go's comment; likewise 0.88 vs.
//     0.85, 0.03 apart, must resolve cleanly since a boolean01 TEXT finding
//     and an unrelated 0.85 finding are never a real disagreement),
//   - and one intentional DISAGREEMENT gap: 0.88 vs. 0.90 (boolean01 TEXT
//     vs. numeric_text), 0.02 apart, which must keep forcing review — see
//     boolean01.go's comment on textConfidence.
//
// So disagreementMargin must be strictly LESS than the smallest intentional
// clean-win gap (0.85→0.88, 0.03) while staying greater than or equal to the
// intentional disagreement gap (0.88→0.90, 0.02). 0.02 satisfies both.
//
// The comparison is done on confidences rounded to integer hundredths
// (confidenceHundredths), not on raw float64 subtraction: decimal
// confidence values like 0.99 and 0.95 aren't exactly representable in
// binary floating point, so their subtraction accumulates representation
// error whose direction is essentially arbitrary (0.99-0.95 evaluates to
// 0.040000000000000036 in Go, landing just above a margin boundary, while
// the nominally-identical-sized 0.95-0.90 evaluates to
// 0.049999999999999933, landing just below it). Rounding both values to
// hundredths before differencing removes that representation question
// entirely, since every confidence literal in the ladder is already exactly
// two decimal digits.
//
// 0.1 used to be wider than every clean-win gap, forcing needless review on
// every column two heuristics agreed on (issue #20). 0.04 later turned out
// to be wider than the 0.85→0.88 clean-win gap (0.03) once boolean01's
// TEXT/CHAR rung was added, and relied on raw float subtraction landing on
// the right side of the 0.99→0.95 boundary by luck (issue #48).
const disagreementMargin = 0.02

// confidenceHundredths rounds a confidence value to the nearest integer
// hundredth (e.g. 0.88 -> 88), giving an exact integer comparison basis
// that sidesteps float64 representation error for values that are, by
// convention, always specified to two decimal places.
func confidenceHundredths(c float64) int {
	return int(math.Round(c * 100))
}

// Decide picks the highest-confidence Finding as the primary decision and
// reports whether it needs human review: because there were no findings,
// the best finding fell below threshold, or two top findings disagreed
// closely enough that picking one over the other isn't clearly justified.
func Decide(findings []profiler.Finding, threshold float64) (profiler.Finding, bool) {
	if len(findings) == 0 {
		return profiler.Finding{}, true
	}

	best := findings[0]
	secondBest := -1.0
	for _, f := range findings[1:] {
		if f.Confidence > best.Confidence {
			secondBest = best.Confidence
			best = f
		} else if f.Confidence > secondBest {
			secondBest = f.Confidence
		}
	}

	if best.Confidence < threshold {
		return best, true
	}
	// Compare on integer hundredths rather than raw float64 subtraction:
	// see disagreementMargin's doc comment for why binary floating point
	// makes decimal confidence subtraction representation-dependent.
	if secondBest >= 0 {
		gap := confidenceHundredths(best.Confidence) - confidenceHundredths(secondBest)
		if gap <= confidenceHundredths(disagreementMargin) {
			return best, true
		}
	}
	return best, false
}
