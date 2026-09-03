package pipeline

import (
	"strings"
	"testing"
)

// TestVarcharFinding_FallsBackToTextPastPostgresLimit is issue #116's
// (L7) regression: varcharFinding widens the suggested varchar(N) to the
// table's actual longest value, but PostgreSQL caps varchar(n) at
// 10485760. A VARCHAR(255) column holding one 20 MB value produced
// SuggestedType "varchar(20000000)", which CREATE TABLE rejects with
// "length for type varchar cannot exceed 10485760". Past the cap it must
// suggest text instead.
func TestVarcharFinding_FallsBackToTextPastPostgresLimit(t *testing.T) {
	f := varcharFinding(255, 20_000_000)
	if f.SuggestedType != "text" {
		t.Errorf("SuggestedType = %q, want %q (past PostgreSQL's varchar limit)", f.SuggestedType, "text")
	}
	if !strings.Contains(f.Rationale, "10485760") {
		t.Errorf("rationale should explain the PostgreSQL limit, got %q", f.Rationale)
	}
}

// TestVarcharFinding_StillWidensWithinTheLimit guards the #116 fallback
// against firing too early — a widened value at or below the cap must
// still be a varchar(N) suggestion.
func TestVarcharFinding_StillWidensWithinTheLimit(t *testing.T) {
	f := varcharFinding(255, maxPostgresVarcharLen)
	if f.SuggestedType != "varchar(10485760)" {
		t.Errorf("SuggestedType = %q, want varchar(10485760) (exactly at the limit)", f.SuggestedType)
	}

	f = varcharFinding(5, 11)
	if f.SuggestedType != "varchar(11)" {
		t.Errorf("SuggestedType = %q, want varchar(11)", f.SuggestedType)
	}
}
