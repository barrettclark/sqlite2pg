package copywriter

import (
	"strings"
	"testing"
)

// A non-finite numeric token and a malformed one get distinct causes.
func TestNullifSentinels_ErrorNamesTheCause(t *testing.T) {
	cases := []struct {
		name, value, want string
	}{
		{"non-finite token", "Inf", "finite number"},
		{"malformed token", "abc", "invalid syntax"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Transform("nullif_sentinels", tc.value)
			if err == nil {
				t.Fatalf("Transform(nullif_sentinels, %q) succeeded, want an error", tc.value)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q, want it to contain %q", err, tc.want)
			}
		})
	}
}
