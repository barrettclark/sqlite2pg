package heuristics

import (
	"regexp"
	"strings"

	"sqlite2pg/internal/profiler"
)

// uuidPattern matches a canonical UUID: 32 hex digits grouped 8-4-4-4-12
// with hyphens (e.g. "519da4fb-adf7-488a-9049-dc17e964ea09"). Case is
// accepted either way since both appear in the wild.
var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// UUIDFormat detects TEXT/CHAR columns storing a single canonical UUID
// per row. Real evidence: an ISO 10383 MIC registry database's station_id
// column, and a beets music library's several single-valued MusicBrainz
// ID columns (mb_workid, mb_albumid, ...). Native uuid is 16 bytes with
// format validation vs 36+ bytes as unvalidated text. Doesn't match a
// column storing more than one UUID per row (e.g. beets' composers_ids,
// which NUL-joins a variable number of them) — every sample must be
// exactly one UUID and nothing else, so a multi-value column simply never
// matches rather than partially matching on its first UUID.
type UUIDFormat struct{}

func (UUIDFormat) Name() string { return "uuid_format" }

func (UUIDFormat) AppliesTo(meta profiler.ColumnMeta) bool {
	d := strings.ToUpper(meta.DeclaredType)
	return strings.Contains(d, "TEXT") || strings.Contains(d, "CHAR")
}

func (UUIDFormat) Evaluate(meta profiler.ColumnMeta, samples []profiler.Value) (profiler.Finding, bool) {
	var total, matched int
	for _, v := range samples {
		s, ok := v.(string)
		if !ok || s == "" {
			continue
		}
		total++
		if uuidPattern.MatchString(s) {
			matched++
		}
	}
	if total == 0 || matched != total {
		return profiler.Finding{}, false
	}
	return profiler.Finding{
		SuggestedType: "uuid",
		Confidence:    0.9,
		Rationale:     "every sampled value is a single canonical UUID (8-4-4-4-12 hex)",
		TransformExpr: "uuid_format",
	}, true
}

func init() { profiler.Register(UUIDFormat{}) }
