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
	allMidnight := true
	for _, v := range samples {
		// modernc.org/sqlite scans DATE/DATETIME/TIMESTAMP-declared
		// columns straight into time.Time rather than a string — the
		// driver has already done the parsing our layouts would otherwise
		// attempt, so a time.Time sample is itself an automatic match.
		if tm, ok := v.(time.Time); ok {
			total++
			matched++
			if !isMidnight(tm) {
				allMidnight = false
			}
			continue
		}
		s, ok := v.(string)
		if !ok || s == "" {
			continue
		}
		total++
		if tm, ok := profiler.ParseTimestamp(s); ok {
			matched++
			if !isMidnight(tm) {
				allMidnight = false
			}
		}
	}
	if total == 0 || matched != total {
		return profiler.Finding{}, false
	}
	// Issue #14: a column whose every sampled value's time-of-day is
	// exactly midnight (a genuine date-only string like "1953-09-02", or a
	// full timestamp string that's midnight in every sample, like NEH
	// grants' "4/1/2006 12:00:00 AM") is functionally date-only. Targeting
	// timestamptz and letting the transform assume UTC midnight silently
	// shifts the calendar date back a day when viewed in any non-UTC
	// session — real corruption seen identically across four independent
	// databases. Targeting date instead, mirroring yyyymmdd_date, avoids
	// the assumption entirely: there's no time-of-day to get wrong.
	if allMidnight {
		return profiler.Finding{
			SuggestedType: "date",
			Confidence:    0.9,
			Rationale:     "all sampled values parse as ISO 8601 timestamps with no time-of-day component (always midnight)",
			TransformExpr: "iso8601_to_date",
		}, true
	}
	return profiler.Finding{
		SuggestedType: "timestamptz",
		Confidence:    0.9,
		Rationale:     "all sampled values parse as ISO 8601 timestamps",
		TransformExpr: "iso8601_to_timestamptz",
	}, true
}

// isMidnight reports whether tm carries no time-of-day component at all.
func isMidnight(tm time.Time) bool {
	return tm.Hour() == 0 && tm.Minute() == 0 && tm.Second() == 0 && tm.Nanosecond() == 0
}

func init() { profiler.Register(ISO8601{}) }
