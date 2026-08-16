package heuristics

import (
	"strings"

	"sqlite2pg/internal/profiler"
)

// plausible astronomical Julian Day Number range, roughly years 1 through 3000.
const (
	julianDayMin = 1721425.5
	julianDayMax = 2816787.5
)

// JulianDay detects Esri "realdate" columns storing astronomical Julian Day
// Numbers (e.g. 2453975.5), as seen in Esri File Geodatabase exports.
type JulianDay struct{}

func (JulianDay) Name() string { return "esri_julian_day" }

func (JulianDay) AppliesTo(meta profiler.ColumnMeta) bool {
	return strings.EqualFold(meta.DeclaredType, "realdate")
}

func (JulianDay) Evaluate(meta profiler.ColumnMeta, samples []profiler.Value) (profiler.Finding, bool) {
	var total, plausible int
	for _, v := range samples {
		f, ok := asFloat64(v)
		if !ok {
			continue
		}
		total++
		if f >= julianDayMin && f <= julianDayMax {
			plausible++
		}
	}
	if total == 0 || plausible != total {
		return profiler.Finding{}, false
	}
	return profiler.Finding{
		SuggestedType: "date",
		Confidence:    0.9,
		Rationale:     "declared type realdate with values in a plausible astronomical Julian Day Number range",
		TransformExpr: "julian_day_to_date",
	}, true
}

func asFloat64(v profiler.Value) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}

func init() { profiler.Register(JulianDay{}) }
