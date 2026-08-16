package resolver

import (
	"fmt"
	"sort"

	"sqlite2pg/internal/profiler"
)

// summarizeSamples collapses samples into distinct-value/count pairs
// (e.g. "1" (x3148)), capped at maxDistinct entries, with a trailing note
// for any remaining distinct values. Without this, a low-cardinality
// column sampled at a large --sample-size (a real case: invoice_items.
// Quantity in chinook.db, every one of 2240 sampled rows equal to 1)
// dumps thousands of duplicate lines into the report a human has to read.
func summarizeSamples(samples []profiler.Value, maxDistinct int) []string {
	order := make([]string, 0, len(samples))
	counts := make(map[string]int, len(samples))
	for _, v := range samples {
		s := fmt.Sprintf("%v", v)
		if counts[s] == 0 {
			order = append(order, s)
		}
		counts[s]++
	}

	sort.SliceStable(order, func(i, j int) bool {
		return counts[order[i]] > counts[order[j]]
	})

	shown := order
	var remainder int
	if len(order) > maxDistinct {
		shown = order[:maxDistinct]
		remainder = len(order) - maxDistinct
	}

	summary := make([]string, 0, len(shown)+1)
	for _, s := range shown {
		summary = append(summary, fmt.Sprintf("%s (x%d)", s, counts[s]))
	}
	if remainder > 0 {
		summary = append(summary, fmt.Sprintf("... and %d more distinct value(s)", remainder))
	}
	return summary
}
