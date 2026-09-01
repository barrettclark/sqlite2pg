package main

import (
	"strings"
	"testing"
)

// --- resolveVerifyMode -------------------------------------------------

func TestResolveVerifyMode_BothFlagsIsUsageError(t *testing.T) {
	_, err := resolveVerifyMode(true, true)
	if err == nil {
		t.Fatal("expected an error when --verify and --noverify are both set")
	}
	if !strings.Contains(err.Error(), "--verify") || !strings.Contains(err.Error(), "--noverify") {
		t.Errorf("expected the error to name both flags, got %q", err.Error())
	}
}

func TestResolveVerifyMode_VerifyOnlySetsVerifyAlways(t *testing.T) {
	mode, err := resolveVerifyMode(true, false)
	if err != nil {
		t.Fatalf("resolveVerifyMode: %v", err)
	}
	if mode != verifyAlways {
		t.Errorf("expected verifyAlways, got %v", mode)
	}
}

func TestResolveVerifyMode_NoverifyOnlySetsVerifyNever(t *testing.T) {
	mode, err := resolveVerifyMode(false, true)
	if err != nil {
		t.Fatalf("resolveVerifyMode: %v", err)
	}
	if mode != verifyNever {
		t.Errorf("expected verifyNever, got %v", mode)
	}
}

func TestResolveVerifyMode_NeitherFlagDefaultsToPrompt(t *testing.T) {
	mode, err := resolveVerifyMode(false, false)
	if err != nil {
		t.Fatalf("resolveVerifyMode: %v", err)
	}
	if mode != verifyPrompt {
		t.Errorf("expected verifyPrompt (the interactive default), got %v", mode)
	}
}

// --- determineVerify (the interactive stdin prompt) ---------------------

func TestDetermineVerify_NonPromptModesNeverTouchStdin(t *testing.T) {
	// A reader that errors if read from at all — proves verifyAlways and
	// verifyNever never consult stdin.
	poison := &poisonReader{t: t}

	if !determineVerify(verifyAlways, poison, &strings.Builder{}) {
		t.Error("expected verifyAlways to report true without reading stdin")
	}
	if determineVerify(verifyNever, poison, &strings.Builder{}) {
		t.Error("expected verifyNever to report false without reading stdin")
	}
}

type poisonReader struct{ t *testing.T }

func (p *poisonReader) Read([]byte) (int, error) {
	p.t.Fatal("unexpected read from stdin in a non-prompt verify mode")
	return 0, nil
}

func TestDetermineVerify_PromptModeParsesAffirmativeAnswers(t *testing.T) {
	affirmative := []string{"y", "Y", "yes", "Yes", "YES", "  y  \n", "y\r\n"}
	for _, answer := range affirmative {
		var out strings.Builder
		got := determineVerify(verifyPrompt, strings.NewReader(answer), &out)
		if !got {
			t.Errorf("input %q: expected determineVerify to return true", answer)
		}
		if !strings.Contains(out.String(), "Run migrate verify now?") {
			t.Errorf("input %q: expected the prompt text to be written to out, got %q", answer, out.String())
		}
	}
}

func TestDetermineVerify_PromptModeDefaultsToNoOnAnythingElse(t *testing.T) {
	negativeOrAmbiguous := []string{"n", "N", "no", "", "\n", "maybe", "yesnt", "yep"}
	for _, answer := range negativeOrAmbiguous {
		var out strings.Builder
		got := determineVerify(verifyPrompt, strings.NewReader(answer), &out)
		if got {
			t.Errorf("input %q: expected determineVerify to return false (safe default)", answer)
		}
	}
}

// --- flag wiring at the `run`/`load` CLI level --------------------------

func TestRun_RunVerifyAndNoverifyTogetherIsUsageError(t *testing.T) {
	err := run([]string{"run", "--pg", "postgres://localhost/x", "--verify", "--noverify", "source.db"})
	if err == nil {
		t.Fatal("expected an error when --verify and --noverify are both passed to run")
	}
	if !strings.Contains(err.Error(), "--verify") || !strings.Contains(err.Error(), "--noverify") {
		t.Errorf("expected the error to name both flags, got %q", err.Error())
	}
}

func TestRun_LoadVerifyAndNoverifyTogetherIsUsageError(t *testing.T) {
	err := run([]string{"load", "--verify", "--noverify", "config.migration.yaml"})
	if err == nil {
		t.Fatal("expected an error when --verify and --noverify are both passed to load")
	}
	if !strings.Contains(err.Error(), "--verify") || !strings.Contains(err.Error(), "--noverify") {
		t.Errorf("expected the error to name both flags, got %q", err.Error())
	}
}
