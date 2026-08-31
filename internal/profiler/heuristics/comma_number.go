package heuristics

import (
	"math"
	"regexp"
	"strconv"
	"strings"

	"sqlite2pg/internal/profiler"
)

var (
	commaNumberPattern = regexp.MustCompile(`^-?\d{1,3}(,\d{3})+(\.\d+)?$`)
	plainNumberPattern = regexp.MustCompile(`^-?\d+(\.\d+)?$`)
)

// CommaNumber detects thousand-separator-formatted numbers stored as text
// in an otherwise numeric SQLite column (e.g. "2,949"), which crashes
// pgloader's fetch phase if loaded without pre-processing.
type CommaNumber struct{}

func (CommaNumber) Name() string { return "comma_formatted_number" }

func (CommaNumber) AppliesTo(meta profiler.ColumnMeta) bool { return true }

func (CommaNumber) Evaluate(meta profiler.ColumnMeta, samples []profiler.Value) (profiler.Finding, bool) {
	seenComma := false
	sawFraction := false
	sawOutOfInt4Range := false
	for _, v := range samples {
		s, ok := v.(string)
		if !ok || s == "" {
			continue
		}
		switch {
		case commaNumberPattern.MatchString(s):
			seenComma = true
			if strings.Contains(s, ".") {
				sawFraction = true
			} else if n, err := strconv.ParseInt(strings.ReplaceAll(s, ",", ""), 10, 64); err == nil {
				if n < math.MinInt32 || n > math.MaxInt32 {
					sawOutOfInt4Range = true
				}
			}
		case plainNumberPattern.MatchString(s):
			// consistent with a numeric column; doesn't itself trigger the
			// heuristic, but a fractional plain value alongside a
			// comma-formatted one (e.g. "1,234.56" and "500") still means
			// the column as a whole needs a floating-point target.
			if strings.Contains(s, ".") {
				sawFraction = true
			}
		default:
			return profiler.Finding{}, false
		}
	}
	if !seenComma {
		return profiler.Finding{}, false
	}
	if sawFraction {
		// Issue #23: commaNumberPattern matches comma-formatted numbers
		// with a decimal component (e.g. "1,234.56"), but strip_commas
		// parses with strconv.ParseInt, which can't parse a decimal point
		// — mirrors numeric_text's sawFraction handling of the same
		// problem for comma-free numeric text.
		return profiler.Finding{
			SuggestedType: "double precision",
			Confidence:    0.95,
			Rationale:     "sampled values are thousand-separator-formatted numbers with a fractional part (e.g. \"1,234.56\")",
			TransformExpr: "strip_commas_float",
		}, true
	}
	suggested := "integer"
	rationale := "sampled values are thousand-separator-formatted numbers (e.g. \"2,949\")"
	if sawOutOfInt4Range {
		suggested = "bigint"
		rationale = "sampled values are thousand-separator-formatted numbers, and at least one is too large for int4 (e.g. \"9,999,999,999\")"
	}
	return profiler.Finding{
		SuggestedType: suggested,
		Confidence:    0.95,
		Rationale:     rationale,
		TransformExpr: "strip_commas",
	}, true
}

func init() { profiler.Register(CommaNumber{}) }
