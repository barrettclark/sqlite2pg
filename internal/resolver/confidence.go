// Package resolver arbitrates between the profiler's Findings for a column,
// deciding whether the top candidate is confident enough to auto-approve or
// must be escalated for human (or future LLM) review.
package resolver

import "sqlite2pg/internal/profiler"

// disagreementMargin is how close two top findings' confidences must be to
// count as "heuristics disagree" and force review even when both are
// individually above threshold.
const disagreementMargin = 0.1

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
	if secondBest >= 0 && best.Confidence-secondBest < disagreementMargin {
		return best, true
	}
	return best, false
}
