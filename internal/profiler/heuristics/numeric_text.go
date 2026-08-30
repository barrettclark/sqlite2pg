package heuristics

import (
	"math"
	"strconv"
	"strings"

	"sqlite2pg/internal/profiler"
)

// NumericText detects TEXT/CHAR columns storing plain numeric values with
// no comma formatting at all (e.g. "24", or "1998.0" for a whole-number
// year) — comma_formatted_number tolerates a plain number alongside a
// comma-formatted one, but only fires once it's seen at least one
// comma-formatted value as evidence; a column that never happens to use
// comma formatting gets no opinion from it and silently falls back to
// text. Real example (companies.db): current_employees, total_employees,
// and year_founded are all declared TEXT and store exactly this shape.
type NumericText struct{}

func (NumericText) Name() string { return "numeric_text" }

func (NumericText) AppliesTo(meta profiler.ColumnMeta) bool {
	d := strings.ToUpper(meta.DeclaredType)
	return strings.Contains(d, "TEXT") || strings.Contains(d, "CHAR")
}

// Evaluate requires every non-nil, non-empty sample to be a plain
// (comma-free) number with no meaningful leading zero — a single value
// outside that shape disqualifies the whole column, the same all-or-
// nothing pattern yyyymmdd_date and uuid_format use, since a wrong
// numeric conversion (destroying a zip code's leading zero, or a
// comma-formatted value comma_formatted_number should own instead) is
// worse than leaving the column as text.
func (NumericText) Evaluate(meta profiler.ColumnMeta, samples []profiler.Value) (profiler.Finding, bool) {
	var total int
	var sawFraction, sawOutOfInt4Range bool
	for _, v := range samples {
		if v == nil {
			continue
		}
		s, ok := v.(string)
		if !ok || s == "" {
			if !ok {
				return profiler.Finding{}, false
			}
			continue
		}
		if hasMeaningfulLeadingZero(s) {
			return profiler.Finding{}, false
		}
		if !plainNumberPattern.MatchString(s) {
			return profiler.Finding{}, false
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return profiler.Finding{}, false
		}
		total++
		if f != math.Trunc(f) {
			sawFraction = true
		} else if f < math.MinInt32 || f > math.MaxInt32 {
			sawOutOfInt4Range = true
		}
	}
	if total == 0 {
		return profiler.Finding{}, false
	}

	if sawFraction {
		return profiler.Finding{
			SuggestedType: "double precision",
			Confidence:    0.85,
			Rationale:     "every sampled value is a plain (comma-free) number, and at least one has a real fractional part",
			TransformExpr: "numeric_text_to_double",
		}, true
	}
	suggested := "integer"
	if sawOutOfInt4Range {
		suggested = "bigint"
	}
	return profiler.Finding{
		SuggestedType: suggested,
		Confidence:    0.9,
		Rationale:     "every sampled value is a plain (comma-free) whole number stored as text",
		TransformExpr: "numeric_text_to_integer",
	}, true
}

// hasMeaningfulLeadingZero reports whether s has a leading zero that would
// carry real information a numeric type would silently discard on
// round-trip (e.g. a zip code "07030") — as opposed to "0" itself or a
// fractional value like "0.5", where the leading zero is just how the
// number is written, not data.
func hasMeaningfulLeadingZero(s string) bool {
	s = strings.TrimPrefix(s, "-")
	return len(s) >= 2 && s[0] == '0' && s[1] >= '0' && s[1] <= '9'
}

func init() { profiler.Register(NumericText{}) }
