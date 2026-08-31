package heuristics

import (
	"strings"

	"sqlite2pg/internal/profiler"
)

// escapedNulSeparator is the literal 4-byte form (0x5C 0xE2 0x90 0x80: a
// backslash followed by U+2400 SYMBOL FOR NULL) that the real beets_library.db
// evidence for this heuristic actually stores between UUIDs, confirmed by a
// byte-for-byte hex comparison against the source file — not a raw NUL
// (0x00) byte as the original issue writeup assumed before that check (see
// docs/superpowers/plans/audit-phase2c-beets-results.md). Whatever
// exported/copied this particular beets database re-encoded its real
// separator into this printable escape somewhere along the way. Both forms
// are normalized to a raw NUL before splitting so this heuristic (and its
// uuid_list_format transform) recognize either — a hypothetical column
// that genuinely NUL-joins with a raw 0x00 byte still works exactly as
// originally designed.
const escapedNulSeparator = "\\␀"

// normalizeNulSeparator rewrites any escapedNulSeparator occurrence in s to
// a raw NUL byte, so downstream splitting only ever has to handle one
// separator form.
func normalizeNulSeparator(s string) string {
	return strings.ReplaceAll(s, escapedNulSeparator, "\x00")
}

// UUIDList detects TEXT/CHAR columns storing a variable-length list of
// canonical UUIDs joined into one value by a NUL (0x00) byte or beets'
// escapedNulSeparator form of it — beets' composers_ids and its siblings
// (arrangers_ids, lyricists_ids, remixers_ids, mb_artistids,
// mb_albumartistids) are the real evidence (issue #12). uuid_format
// deliberately never matches these (its regex can't match a string
// containing an embedded separator), so without this heuristic they fall
// through to default_passthrough as plain text with zero review signal.
//
// This only fires when the SAMPLE ITSELF demonstrates the list shape (at
// least one sampled value actually contains the separator) — it does not
// speculate that a column that looks scalar in every sampled row might
// secretly be a list elsewhere in the full table. A column whose sample is
// all single-UUID values still belongs to uuid_format; if the full table
// later turns out to hold a genuine multi-value row that uuid_format's
// transform can't parse, issue #13's full-table verification already
// demotes that decision to needs-review today (real example: beets'
// composers_ids/lyricists_ids) — a human reviewing it can now pick
// uuid[] from the type picker as a real, working option, rather than the
// tool guessing an array type for a column with no direct evidence for
// one.
type UUIDList struct{}

func (UUIDList) Name() string { return "uuid_list" }

func (UUIDList) AppliesTo(meta profiler.ColumnMeta) bool {
	d := strings.ToUpper(meta.DeclaredType)
	return strings.Contains(d, "TEXT") || strings.Contains(d, "CHAR")
}

func (UUIDList) Evaluate(meta profiler.ColumnMeta, samples []profiler.Value) (profiler.Finding, bool) {
	var total, matched int
	sawMultiValue := false
	for _, v := range samples {
		s, ok := v.(string)
		if !ok || s == "" {
			continue
		}
		total++
		parts := strings.Split(normalizeNulSeparator(s), "\x00")
		if len(parts) > 1 {
			sawMultiValue = true
		}
		allValid := true
		for _, p := range parts {
			if !uuidPattern.MatchString(p) {
				allValid = false
				break
			}
		}
		if allValid {
			matched++
		}
	}
	if total == 0 || matched != total || !sawMultiValue {
		return profiler.Finding{}, false
	}
	return profiler.Finding{
		SuggestedType: "uuid[]",
		Confidence:    0.9,
		Rationale:     "every sampled value is one or more canonical UUIDs joined by a NUL (0x00) byte or its escaped form",
		TransformExpr: "uuid_list_format",
	}, true
}

func init() { profiler.Register(UUIDList{}) }
