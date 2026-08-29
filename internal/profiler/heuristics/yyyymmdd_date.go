package heuristics

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"sqlite2pg/internal/profiler"
)

var dateNamePattern = regexp.MustCompile(`(?i)date`)

// YYYYMMDDDate detects columns storing dates as compact 8-digit YYYYMMDD
// values (e.g. 20210927 for 2021-09-27) — seen stored as SQLite INTEGER
// and as TEXT in the same source table (ISO 10383 MIC registry data:
// "CREATION DATE" as INTEGER, "LAST VALIDATION DATE" as TEXT, identical
// format). A plain COPY of this value into a Postgres date column fails
// outright — Postgres's date parser rejects an 8-digit run with no
// separators — so this heuristic exists to catch it before load time
// rather than mid-COPY.
type YYYYMMDDDate struct{}

func (YYYYMMDDDate) Name() string { return "yyyymmdd_date" }

func (YYYYMMDDDate) AppliesTo(meta profiler.ColumnMeta) bool {
	if !isIntOrTextDeclared(meta.DeclaredType) {
		return false
	}
	return dateNamePattern.MatchString(meta.Name)
}

// Evaluate requires every non-nil sample to be a genuine, valid 8-digit
// YYYYMMDD calendar date — unlike UnixEpoch's tolerance for a minority of
// implausible values, this heuristic's transform has no fallback for a
// value it can't parse (a plain COPY would just fail), so one bad sample
// (a real placeholder string, or an invalid date like month 13)
// disqualifies the whole column rather than being averaged away.
func (YYYYMMDDDate) Evaluate(meta profiler.ColumnMeta, samples []profiler.Value) (profiler.Finding, bool) {
	var total int
	for _, v := range samples {
		s, ok := asYYYYMMDDString(v)
		if !ok {
			if v == nil {
				continue
			}
			return profiler.Finding{}, false
		}
		if _, err := time.Parse("20060102", s); err != nil {
			return profiler.Finding{}, false
		}
		total++
	}
	if total == 0 {
		return profiler.Finding{}, false
	}
	return profiler.Finding{
		SuggestedType: "date",
		Confidence:    0.9,
		Rationale:     "column name matches a date-like pattern and every sampled value is a valid 8-digit YYYYMMDD date",
		TransformExpr: "yyyymmdd_to_date",
	}, true
}

// isIntOrTextDeclared reports whether declared is a SQLite type name with
// INTEGER or TEXT/CHAR affinity — the two ways this project has seen
// YYYYMMDD-style dates actually stored.
func isIntOrTextDeclared(declared string) bool {
	upper := strings.ToUpper(declared)
	return strings.Contains(upper, "INT") || strings.Contains(upper, "TEXT") || strings.Contains(upper, "CHAR")
}

// asYYYYMMDDString normalizes v to an 8-character digit string if it's a
// plausible YYYYMMDD candidate (an int64 or a string of exactly 8 ASCII
// digits), or reports false. A nil value is also reported false — the
// caller distinguishes "nil, skip" from "wrong shape, disqualify".
func asYYYYMMDDString(v profiler.Value) (string, bool) {
	switch val := v.(type) {
	case int64:
		s := strconv.FormatInt(val, 10)
		if len(s) != 8 {
			return "", false
		}
		return s, true
	case string:
		if len(val) != 8 {
			return "", false
		}
		for _, r := range val {
			if r < '0' || r > '9' {
				return "", false
			}
		}
		return val, true
	default:
		return "", false
	}
}

func init() { profiler.Register(YYYYMMDDDate{}) }
