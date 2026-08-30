// Package resolver arbitrates between the profiler's Findings for a column,
// deciding whether the top candidate is confident enough to auto-approve or
// must be escalated for human (or future LLM) review.
package resolver

import "sqlite2pg/internal/profiler"

// disagreementMargin is how close two top findings' confidences must be to
// count as "heuristics disagree" and force review even when both are
// individually above threshold. Must stay smaller than the smallest gap
// between adjacent rungs of the heuristic confidence ladder (0.99/0.95/
// 0.90/0.85/0.55) that's meant to resolve cleanly — e.g. yyyymmdd_date's
// 0.95 is deliberately spaced 0.05 above numeric_text's 0.90 specifically
// so it wins outright rather than tying (see yyyymmdd_date.go's comment).
// 0.1 used to be wider than every such gap, forcing needless review on
// every column two heuristics agreed on (issue #20).
const disagreementMargin = 0.04

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
	// Compare with an epsilon rather than a bare "<": float subtraction of
	// decimal confidence values isn't exact (e.g. 0.95-0.90 comes out as
	// 0.049999999999999933, and 0.95-0.85 as 0.09999999999999998), so a
	// gap that's meant to land exactly on the margin boundary must not
	// slip to the wrong side of it due to representation error alone.
	if secondBest >= 0 && best.Confidence-secondBest <= disagreementMargin-1e-9 {
		return best, true
	}
	return best, false
}
