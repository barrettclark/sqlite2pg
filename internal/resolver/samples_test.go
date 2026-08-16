package resolver

import (
	"strings"
	"testing"

	"sqlite2pg/internal/profiler"
)

func TestSummarizeSamples_CollapsesRepeatedValuesWithACount(t *testing.T) {
	samples := make([]profiler.Value, 3148)
	for i := range samples {
		samples[i] = int64(1)
	}

	summary := summarizeSamples(samples, 10)

	if len(summary) != 1 {
		t.Fatalf("expected 1 summarized entry for an all-identical sample, got %d: %v", len(summary), summary)
	}
	if !strings.Contains(summary[0], "1") || !strings.Contains(summary[0], "3148") {
		t.Errorf("expected the entry to show the value and its count, got %q", summary[0])
	}
}

func TestSummarizeSamples_ShowsEachDistinctValueUnderTheCap(t *testing.T) {
	samples := []profiler.Value{int64(0), int64(0), int64(1), "Unknown"}

	summary := summarizeSamples(samples, 10)

	if len(summary) != 3 {
		t.Fatalf("expected 3 distinct values, got %d: %v", len(summary), summary)
	}
}

func TestSummarizeSamples_CapsDistinctValuesAndNotesTheRemainder(t *testing.T) {
	samples := make([]profiler.Value, 0, 20)
	for i := 0; i < 20; i++ {
		samples = append(samples, int64(i)) // 20 distinct values
	}

	summary := summarizeSamples(samples, 5)

	if len(summary) != 6 {
		t.Fatalf("expected 5 shown values + 1 remainder note, got %d: %v", len(summary), summary)
	}
	last := summary[len(summary)-1]
	if !strings.Contains(last, "15") || !strings.Contains(strings.ToLower(last), "more") {
		t.Errorf("expected the last entry to note the remaining 15 distinct values, got %q", last)
	}
}
