package heuristics

import (
	"strings"

	"sqlite2pg/internal/profiler"
)

// DayFirstDate detects TEXT columns storing day-first (D/M/YYYY,
// European/UK convention) dates — e.g. "31/07/2006". These are ambiguous
// with the US-style M/D/YYYY layout ISO8601 already accepts whenever both
// components are <=12, so this heuristic only fires once a sample proves
// the reading: a day-of-month value >12 could never be a valid month, so
// no US-style interpretation of that value exists. Without that proof, the
// column is left unclassified exactly as before this heuristic existed,
// rather than guessing.
type DayFirstDate struct{}

func (DayFirstDate) Name() string { return "day_first_date" }

func (DayFirstDate) AppliesTo(meta profiler.ColumnMeta) bool {
	d := strings.ToUpper(meta.DeclaredType)
	if !strings.Contains(d, "TEXT") && !strings.Contains(d, "CHAR") {
		return false
	}
	return dateNamePattern.MatchString(meta.Name)
}

func (DayFirstDate) Evaluate(meta profiler.ColumnMeta, samples []profiler.Value) (profiler.Finding, bool) {
	var total int
	var proven bool
	for _, v := range samples {
		if v == nil {
			continue
		}
		s, ok := v.(string)
		if !ok {
			return profiler.Finding{}, false
		}
		tm, ok := profiler.ParseDayFirstTimestamp(s)
		if !ok {
			return profiler.Finding{}, false
		}
		total++
		if tm.Day() > 12 {
			proven = true
		}
	}
	if total == 0 || !proven {
		return profiler.Finding{}, false
	}
	return profiler.Finding{
		SuggestedType: "timestamptz",
		Confidence:    0.9,
		Rationale:     "column name matches a date-like pattern and a sampled value's day-of-month exceeds 12, proving a day-first (D/M/YYYY) reading over the ambiguous US-style alternative",
		TransformExpr: "dayfirst_to_timestamptz",
	}, true
}

func init() { profiler.Register(DayFirstDate{}) }
