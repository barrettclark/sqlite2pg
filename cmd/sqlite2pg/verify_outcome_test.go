package main

import (
	"errors"
	"strings"
	"testing"
)

// issue #144 (audit cycle 4 L2): a --out report write failure must not
// shadow a genuine verification failure in the message, and "report
// written to" must print only on full success (after the verdict is
// known), never above a "verification FAILED" line.
func TestVerifyOutcome(t *testing.T) {
	pass := verifySummary{totalRowsCompared: 10, tablesChecked: 2}
	fail := verifySummary{rowCountFailures: 1, totalMismatches: 3, tablesChecked: 2}
	writeErr := errors.New("writing report to /x: no space left on device")

	t.Run("pass, report ok", func(t *testing.T) {
		out, err := verifyOutcome(pass, "/x", nil)
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		joined := strings.Join(out, "\n")
		if !strings.Contains(joined, "report written to /x") || !strings.Contains(joined, "verification passed") {
			t.Fatalf("stdout = %q", out)
		}
		if idx := strings.Index(joined, "report written to"); idx > strings.Index(joined, "verification passed") {
			t.Errorf("'report written to' should print before 'verification passed': %q", out)
		}
	})

	t.Run("pass, no --out", func(t *testing.T) {
		out, err := verifyOutcome(pass, "", nil)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if strings.Contains(strings.Join(out, "\n"), "report written to") {
			t.Errorf("no --out: should not mention a report file: %q", out)
		}
	})

	t.Run("pass, report write failed", func(t *testing.T) {
		out, err := verifyOutcome(pass, "/x", writeErr)
		if err == nil {
			t.Fatal("err = nil, want the write error surfaced")
		}
		if !strings.Contains(err.Error(), "no space left") {
			t.Errorf("err = %v, want the write error", err)
		}
		if len(out) != 0 {
			t.Errorf("stdout = %q, want none on a failure", out)
		}
	})

	t.Run("verification failed, report ok", func(t *testing.T) {
		_, err := verifyOutcome(fail, "/x", nil)
		if err == nil || !strings.Contains(err.Error(), "verification FAILED") {
			t.Fatalf("err = %v, want a 'verification FAILED' error", err)
		}
	})

	t.Run("verification failed AND report write failed: verdict wins, both surfaced", func(t *testing.T) {
		_, err := verifyOutcome(fail, "/x", writeErr)
		if err == nil {
			t.Fatal("err = nil")
		}
		msg := err.Error()
		if !strings.Contains(msg, "verification FAILED") {
			t.Errorf("the verification verdict must not be shadowed by the write error: %v", err)
		}
		if !strings.Contains(msg, "no space left") {
			t.Errorf("the write failure should still be mentioned: %v", err)
		}
	})
}
