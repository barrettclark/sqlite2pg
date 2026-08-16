package heuristics

import (
	"strings"

	"sqlite2pg/internal/profiler"
)

// Boolean01 detects integer columns whose sampled values are entirely
// within {0, 1, NULL}. This is deliberately ambiguous: such a column might
// be a real boolean flag, or a numeric code that just happens to only take
// values 0 and 1 in this dataset. Confidence is kept moderate so the
// resolver routes it to human review rather than auto-approving it.
type Boolean01 struct{}

func (Boolean01) Name() string { return "boolean01" }

func (Boolean01) AppliesTo(meta profiler.ColumnMeta) bool {
	return strings.Contains(strings.ToUpper(meta.DeclaredType), "INT")
}

func (Boolean01) Evaluate(meta profiler.ColumnMeta, samples []profiler.Value) (profiler.Finding, bool) {
	sawZero, sawOne, sawNonNull := false, false, false
	for _, v := range samples {
		if v == nil {
			continue
		}
		i, ok := asInt64(v)
		if !ok {
			return profiler.Finding{}, false
		}
		sawNonNull = true
		switch i {
		case 0:
			sawZero = true
		case 1:
			sawOne = true
		default:
			return profiler.Finding{}, false
		}
	}
	if !sawNonNull || !(sawZero || sawOne) {
		return profiler.Finding{}, false
	}
	return profiler.Finding{
		SuggestedType: "boolean",
		Confidence:    0.55,
		Rationale:     "sampled values are entirely within {0, 1, NULL}; could be a flag or a numeric code",
		TransformExpr: "int_to_bool",
	}, true
}

func init() { profiler.Register(Boolean01{}) }
