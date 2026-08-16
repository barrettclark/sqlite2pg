package heuristics

import (
	"strings"
	"time"

	"sqlite2pg/internal/profiler"
)

// ISO8601 detects columns storing ISO 8601 or SQLite-canonical datetime
// strings.
type ISO8601 struct{}

func (ISO8601) Name() string { return "iso8601_timestamp" }

func (ISO8601) AppliesTo(meta profiler.ColumnMeta) bool {
	d := strings.ToUpper(meta.DeclaredType)
	return strings.Contains(d, "TEXT") || strings.Contains(d, "CHAR") ||
		strings.Contains(d, "DATE") || strings.Contains(d, "TIME")
}

func (ISO8601) Evaluate(meta profiler.ColumnMeta, samples []profiler.Value) (profiler.Finding, bool) {
	var total, matched int
	for _, v := range samples {
		// modernc.org/sqlite scans DATE/DATETIME/TIMESTAMP-declared
		// columns straight into time.Time rather than a string — the
		// driver has already done the parsing our layouts would otherwise
		// attempt, so a time.Time sample is itself an automatic match.
		if _, ok := v.(time.Time); ok {
			total++
			matched++
			continue
		}
		s, ok := v.(string)
		if !ok || s == "" {
			continue
		}
		total++
		if _, ok := profiler.ParseTimestamp(s); ok {
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

func init() { profiler.Register(ISO8601{}) }
