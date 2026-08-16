package heuristics

import (
	"regexp"

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
	for _, v := range samples {
		s, ok := v.(string)
		if !ok || s == "" {
			continue
		}
		switch {
		case commaNumberPattern.MatchString(s):
			seenComma = true
		case plainNumberPattern.MatchString(s):
			// consistent with a numeric column; doesn't itself trigger the heuristic
		default:
			return profiler.Finding{}, false
		}
	}
	if !seenComma {
		return profiler.Finding{}, false
	}
	return profiler.Finding{
		SuggestedType: "integer",
		Confidence:    0.95,
		Rationale:     "sampled values are thousand-separator-formatted numbers (e.g. \"2,949\")",
		TransformExpr: "strip_commas",
	}, true
}

func init() { profiler.Register(CommaNumber{}) }
