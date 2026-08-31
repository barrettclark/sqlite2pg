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

// Boolean01 detects integer, or TEXT/CHAR, columns whose sampled values
// are entirely within {0, 1, NULL}. This is deliberately ambiguous: such a
// column might be a real boolean flag, or a numeric code that just happens
// to only take values 0 and 1 in this dataset. Confidence is kept moderate
// so the resolver routes it to human review rather than auto-approving it.
//
// The TEXT/CHAR case (e.g. sakila.db's customer.active, declared CHAR(1)
// and storing '0'/'1' as text — issue #1) uses a different, higher
// confidence (textConfidence, close to but below numeric_text's 0.90) than
// the INTEGER case's 0.55: a plain-digit-string 0/1 column is also claimed
// by numeric_text at 0.90, and resolver.Decide's disagreementMargin (0.04)
// only forces review when the two top findings are close enough — 0.55
// would lose cleanly to 0.90 and change nothing. textConfidence is chosen
// specifically to land within that margin.
type Boolean01 struct{}

// textConfidence is the confidence assigned to a TEXT/CHAR-affinity 0/1
// finding — high enough to be taken seriously, but within
// resolver.disagreementMargin (0.04) of numeric_text's 0.90 confidence so
// the two are treated as disagreeing and routed to review, rather than
// numeric_text's 0.90 silently winning outright.
const textConfidence = 0.88

func (Boolean01) Name() string { return "boolean01" }

func (Boolean01) AppliesTo(meta profiler.ColumnMeta) bool {
	d := strings.ToUpper(meta.DeclaredType)
	if !strings.Contains(d, "INT") && !strings.Contains(d, "TEXT") && !strings.Contains(d, "CHAR") {
		return false
	}
	return !idSuffixPattern.MatchString(meta.Name)
}

func (Boolean01) Evaluate(meta profiler.ColumnMeta, samples []profiler.Value) (profiler.Finding, bool) {
	d := strings.ToUpper(meta.DeclaredType)
	if strings.Contains(d, "TEXT") || strings.Contains(d, "CHAR") {
		return evaluateTextBoolean01(samples)
	}

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

// evaluateTextBoolean01 requires every non-nil sample to be exactly the
// string "0" or "1" — not "00"/"01", not surrounding whitespace, not any
// other digit-ish string. This is deliberately stricter than "parses as a
// small integer": a value like "01" could be carrying information (e.g. a
// zero-padded code) that a bare boolean would silently discard, so it
// disqualifies the whole column rather than being coerced.
func evaluateTextBoolean01(samples []profiler.Value) (profiler.Finding, bool) {
	sawZero, sawOne, sawNonNull := false, false, false
	for _, v := range samples {
		if v == nil {
			continue
		}
		s, ok := v.(string)
		if !ok {
			return profiler.Finding{}, false
		}
		sawNonNull = true
		switch s {
		case "0":
			sawZero = true
		case "1":
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
		Confidence:    textConfidence,
		Rationale:     "sampled values are entirely within {\"0\", \"1\", NULL}; could be a flag or a numeric code stored as text",
		TransformExpr: "int_to_bool",
	}, true
}

func init() { profiler.Register(Boolean01{}) }
