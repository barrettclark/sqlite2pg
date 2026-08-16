package heuristics

import (
	"strings"
	"time"

	"sqlite2pg/internal/profiler"
)

var iso8601Layouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05",
}

// ISO8601 detects TEXT columns storing ISO 8601 timestamp strings.
type ISO8601 struct{}

func (ISO8601) Name() string { return "iso8601_timestamp" }

func (ISO8601) AppliesTo(meta profiler.ColumnMeta) bool {
	return strings.Contains(strings.ToUpper(meta.DeclaredType), "TEXT") ||
		strings.Contains(strings.ToUpper(meta.DeclaredType), "CHAR")
}

func (ISO8601) Evaluate(meta profiler.ColumnMeta, samples []profiler.Value) (profiler.Finding, bool) {
	var total, matched int
	for _, v := range samples {
		s, ok := v.(string)
		if !ok || s == "" {
			continue
		}
		total++
		if parseISO8601(s) {
			matched++
		}
	}
	if total == 0 || matched != total {
		return profiler.Finding{}, false
	}
	return profiler.Finding{
		SuggestedType: "timestamptz",
		Confidence:    0.9,
		Rationale:     "all sampled values parse as ISO 8601 timestamps",
		TransformExpr: "iso8601_to_timestamptz",
	}, true
}

func parseISO8601(s string) bool {
	for _, layout := range iso8601Layouts {
		if _, err := time.Parse(layout, s); err == nil {
			return true
		}
	}
	return false
}

func init() { profiler.Register(ISO8601{}) }
