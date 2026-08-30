package heuristics

import (
	"regexp"
	"strings"

	"sqlite2pg/internal/profiler"
)

// idSuffixPattern matches a column name ending in "id" or "_id"
// (case-insensitively) — foreign-key-shaped names like discogs_artistid
// or CustomerId. No reasonable person names a boolean column this way, so
// Boolean01 excludes them regardless of what values a sample happens to
// catch (issue #11: discogs_artistid/discogs_labelid, real Discogs
// numeric IDs, sampled as all-0 in a real beets library and got flagged
// as ambiguous boolean candidates).
var idSuffixPattern = regexp.MustCompile(`(?i)_?id$`)

// Boolean01 detects integer columns whose sampled values are entirely
// within {0, 1, NULL}. This is deliberately ambiguous: such a column might
// be a real boolean flag, or a numeric code that just happens to only take
// values 0 and 1 in this dataset. Confidence is kept moderate so the
// resolver routes it to human review rather than auto-approving it.
type Boolean01 struct{}

func (Boolean01) Name() string { return "boolean01" }

func (Boolean01) AppliesTo(meta profiler.ColumnMeta) bool {
	if !strings.Contains(strings.ToUpper(meta.DeclaredType), "INT") {
		return false
	}
	return !idSuffixPattern.MatchString(meta.Name)
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
