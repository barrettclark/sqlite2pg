package heuristics

import (
	"strings"

	"sqlite2pg/internal/profiler"
)

// sentinelTokens are common catch-all/aggregate-row markers seen mixed into
// otherwise-numeric columns (e.g. DisabilityCompByCounty's "FIPS code",
// which has one row of 'Unknown' for a nationwide aggregate).
var sentinelTokens = map[string]bool{
	"unknown": true,
	"n/a":     true,
	"na":      true,
	"none":    true,
	"null":    true,
	"missing": true,
	"-":       true,
}

// SentinelNull detects a numeric-declared column where non-numeric samples
// are all recognized sentinel/catch-all tokens, distinguishing "this row is
// an intentional aggregate/unknown" from an actual type mismatch.
type SentinelNull struct{}

func (SentinelNull) Name() string { return "sentinel_null" }

func (SentinelNull) AppliesTo(meta profiler.ColumnMeta) bool {
	d := strings.ToUpper(meta.DeclaredType)
	return strings.Contains(d, "INT") || strings.Contains(d, "REAL") ||
		strings.Contains(d, "FLOA") || strings.Contains(d, "DOUB")
}

func (SentinelNull) Evaluate(meta profiler.ColumnMeta, samples []profiler.Value) (profiler.Finding, bool) {
	sawNumeric, sawSentinel := false, false
	for _, v := range samples {
		if v == nil {
			continue
		}
		// SQLite/database-sql returns numeric-affinity values as int64 or
		// float64 directly, not as strings — only non-numeric cells (like a
		// sentinel token) come back as strings.
		if _, ok := asInt64(v); ok {
			sawNumeric = true
			continue
		}
		if _, ok := asFloat64(v); ok {
			sawNumeric = true
			continue
		}
		s, ok := v.(string)
		if !ok || s == "" {
			continue
		}
		switch {
		case commaNumberPattern.MatchString(s), plainNumberPattern.MatchString(s):
			sawNumeric = true
		case sentinelTokens[strings.ToLower(s)]:
			sawSentinel = true
		default:
			// unrecognized non-numeric text — not this heuristic's call to make
			return profiler.Finding{}, false
		}
	}
	if !sawNumeric || !sawSentinel {
		return profiler.Finding{}, false
	}
	return profiler.Finding{
		SuggestedType: "integer",
		Confidence:    0.85,
		Rationale:     "numeric values mixed with a recognized sentinel token (e.g. \"Unknown\") likely marking an aggregate/catch-all row",
		TransformExpr: "nullif_sentinels",
	}, true
}

func init() { profiler.Register(SentinelNull{}) }
