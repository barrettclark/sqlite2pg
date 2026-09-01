package pipeline

import "testing"

// TestIsTextTargetType_RecognizesBareVarcharAndVarcharN is a regression
// test for Copilot's PR #96 finding: bare "varchar" is not reachable
// through review.TypeOptions or varcharFinding today, but is a valid,
// collatable Postgres type a hand-edited config (or one carried over from
// before varcharFinding existed) could still set — isTextTargetType must
// keep treating it as text-shaped so it doesn't reintroduce the ORDER BY
// collation false-fail for that case.
func TestIsTextTargetType_RecognizesBareVarcharAndVarcharN(t *testing.T) {
	cases := []struct {
		targetType string
		want       bool
	}{
		{"text", true},
		{"varchar", true},
		{"varchar(50)", true},
		{"jsonb", false},
		{"integer", false},
	}
	for _, c := range cases {
		if got := isTextTargetType(c.targetType); got != c.want {
			t.Errorf("isTextTargetType(%q) = %v, want %v", c.targetType, got, c.want)
		}
	}
}
