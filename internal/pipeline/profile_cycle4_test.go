package pipeline

import "testing"

// issue #149 (audit cycle 4 L7): fallbackTargetNeedsStorageCheck was
// written for fallbackTypeFor's output vocabulary (issue #69) and then
// reused verbatim to gate the heuristic-winner path (issue #84), whose
// vocabulary also includes boolean/date/jsonb/uuid/uuid[]/smallint. A
// heuristic winning with one of those and an empty TransformExpr would
// auto-approve with no full-table storage check. Enumerate the safe
// (string-holding) targets instead, so any concrete non-text target is
// covered.
func TestFallbackTargetNeedsStorageCheck_CoversHeuristicWinnerVocabulary(t *testing.T) {
	needs := []string{
		"integer", "bigint", "smallint", "double precision", "timestamptz",
		"bytea", "boolean", "date", "jsonb", "uuid", "uuid[]",
	}
	for _, target := range needs {
		if !fallbackTargetNeedsStorageCheck(target) {
			t.Errorf("fallbackTargetNeedsStorageCheck(%q) = false, want true", target)
		}
	}

	safe := []string{"text", "varchar", "varchar(80)", "varchar(255)"}
	for _, target := range safe {
		if fallbackTargetNeedsStorageCheck(target) {
			t.Errorf("fallbackTargetNeedsStorageCheck(%q) = true, want false (a string-holding target)", target)
		}
	}
}
