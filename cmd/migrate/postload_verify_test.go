package main

import (
	"os"
	"strings"
	"testing"
	"time"
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

// TestDetermineVerify_NonTerminalFileSkipsPromptWithoutBlocking is the
// regression test for the Copilot PR #59 finding: in a CI/automation
// environment, stdin is often an open pipe that's never written to and
// never closed. determineVerify used to unconditionally block on
// bufio.Reader.ReadString('\n') in verifyPrompt mode, which would hang
// forever against exactly that kind of stdin. A *os.File that is not a
// terminal (as verified via term.IsTerminal, the same check progress.go
// already uses for stdout) must be detected and skipped without reading
// from it at all, defaulting to false (no verify) — the same answer a bare
// Enter would produce. The read happens on a goroutine with a timeout so a
// genuine regression here fails fast instead of hanging the whole suite.
func TestDetermineVerify_NonTerminalFileSkipsPromptWithoutBlocking(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	done := make(chan bool, 1)
	go func() {
		done <- determineVerify(verifyPrompt, r, &strings.Builder{})
	}()

	select {
	case got := <-done:
		if got {
			t.Error("expected determineVerify to default to false for a non-terminal, unread pipe")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("determineVerify blocked reading from a non-terminal pipe instead of skipping the prompt (CI hang risk)")
	}
}

// TestDetermineVerify_NonTerminalPipeHonoursAScriptedAnswer covers issue
// #66: `echo y | migrate load config.yaml` leaves stdin connected to a
// pipe (an *os.File, not a terminal), and the CI-hang guard skipped the
// prompt without reading a byte — silently discarding the user's explicit
// "y" and never verifying. determineVerify must do a short-deadline read
// first: a scripted answer waiting in the pipe is honoured, only a
// genuinely silent pipe falls back to "no".
func TestDetermineVerify_NonTerminalPipeHonoursAScriptedAnswer(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer r.Close()
	if _, err := w.WriteString("y\n"); err != nil {
		t.Fatalf("writing scripted answer: %v", err)
	}
	w.Close()

	done := make(chan bool, 1)
	go func() { done <- determineVerify(verifyPrompt, r, &strings.Builder{}) }()

	select {
	case got := <-done:
		if !got {
			t.Error("determineVerify discarded a scripted \"y\" piped on stdin; want true")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("determineVerify blocked instead of reading the scripted answer")
	}
}

// TestDetermineVerify_NonTerminalPipeSaysWhyItSkipped confirms the silent
// skip is no longer silent: when a non-terminal stdin carries no answer,
// determineVerify still returns false but tells the user how to force it.
func TestDetermineVerify_NonTerminalPipeSaysWhyItSkipped(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer r.Close()
	defer w.Close() // left open + unwritten: the CI-hang shape

	var out strings.Builder
	done := make(chan bool, 1)
	go func() { done <- determineVerify(verifyPrompt, r, &out) }()

	select {
	case got := <-done:
		if got {
			t.Error("expected determineVerify to default to false for a silent non-terminal pipe")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("determineVerify blocked on a silent non-terminal pipe (CI hang risk)")
	}
	if !strings.Contains(out.String(), "--verify") {
		t.Errorf("expected the skip message to mention --verify, got %q", out.String())
	}
}

// TestDetermineVerify_NonTerminalPipeImmediateEOFStillSaysNoAnswer is a
// regression test for Copilot's PR #101 review finding: the fix for
// issue #94 (below) correctly stopped conflating "a real blank line
// arrived" with "no answer arrived," but over-corrected by also treating
// an immediate, empty EOF (stdin redirected from /dev/null, or a pipe
// closed before anything was ever written to it) as a received answer —
// suppressing the "no answer was provided" diagnostic even though zero
// bytes were actually read.
func TestDetermineVerify_NonTerminalPipeImmediateEOFStillSaysNoAnswer(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer r.Close()
	w.Close() // closed immediately, nothing ever written: the /dev/null shape

	var out strings.Builder
	done := make(chan bool, 1)
	go func() { done <- determineVerify(verifyPrompt, r, &out) }()

	select {
	case got := <-done:
		if got {
			t.Error("expected determineVerify to default to false for an immediately-closed empty pipe")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("determineVerify blocked on an immediately-closed empty pipe")
	}
	if !strings.Contains(out.String(), "no answer was provided") {
		t.Errorf("expected the \"no answer was provided\" message for a genuine empty EOF, got %q", out.String())
	}
}

// TestDetermineVerify_NonTerminalPipeDistinguishesEmptyAnswerFromNoAnswer
// is issue #94's (audit finding L8) regression: a scripted bare newline
// (`printf '\n' | migrate load ...`) is a real, explicit answer the user
// (or script) provided — just an empty one — not the same thing as a
// silent, unwritten pipe that never answers at all. Both used to report
// gotAnswer=false and print the same "no answer was provided" message;
// the final skip-verification behavior is unaffected either way (an empty
// answer already means "no"), but the message must reflect what actually
// happened.
func TestDetermineVerify_NonTerminalPipeDistinguishesEmptyAnswerFromNoAnswer(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer r.Close()
	if _, err := w.WriteString("\n"); err != nil {
		t.Fatalf("writing scripted blank answer: %v", err)
	}
	w.Close()

	var out strings.Builder
	done := make(chan bool, 1)
	go func() { done <- determineVerify(verifyPrompt, r, &out) }()

	select {
	case got := <-done:
		if got {
			t.Error("expected an empty answer to still default to false (skip verification)")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("determineVerify blocked on a scripted blank answer")
	}
	if strings.Contains(out.String(), "no answer was provided") {
		t.Errorf("expected no \"no answer was provided\" message for an explicit (if blank) scripted answer, got %q", out.String())
	}
}

// --- flag wiring at the `run`/`load` CLI level --------------------------

// TestRun_LoadDryRunWithVerifyIsUsageError covers the second half of issue
// #66: `migrate load --dry-run --verify` parses --verify and then returns
// from the --dry-run branch without ever using it. That combination is
// nonsensical (dry-run never loads, so there is nothing to verify) and
// should be a clear usage error rather than a silently ignored flag.
func TestRun_LoadDryRunWithVerifyIsUsageError(t *testing.T) {
	for _, flag := range []string{"--verify", "--noverify"} {
		err := run([]string{"load", "--dry-run", flag, "config.migration.yaml"})
		if err == nil {
			t.Fatalf("%s: expected an error for --dry-run with %s", flag, flag)
		}
		if !strings.Contains(err.Error(), "--dry-run") || !strings.Contains(err.Error(), flag) {
			t.Errorf("%s: expected the error to name both --dry-run and %s, got %q", flag, flag, err.Error())
		}
	}
}

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

// TestRun_RunUsageStringListsEveryFlag is the regression test for the
// Copilot PR #59 finding that `run`'s usage error omitted --keep-config
// even though it's a real, supported flag defined a few lines above the
// usage string — a user who mistypes an argument sees an inaccurate usage
// hint. Checked against the full flag set actually defined in runRun's
// flag.NewFlagSet block, not just the missing one, so a future flag added
// without updating the usage string is caught too.
func TestRun_RunUsageStringListsEveryFlag(t *testing.T) {
	err := run([]string{"run"})
	if err == nil {
		t.Fatal("expected a usage error for `run` with no source path")
	}
	for _, flag := range []string{"--pg", "--sample-size", "--threshold", "--keep-config", "--verify", "--noverify"} {
		if !strings.Contains(err.Error(), flag) {
			t.Errorf("expected run's usage string to mention %s, got %q", flag, err.Error())
		}
	}
}

// TestRun_LoadUsageStringListsEveryFlag is the regression test for the
// Copilot PR #59 finding that `load`'s usage error omitted --resume and
// --threshold even though both are real, supported flags defined in
// runLoad's flag.NewFlagSet block. Checked against the full flag set, not
// just the two named ones.
func TestRun_LoadUsageStringListsEveryFlag(t *testing.T) {
	err := run([]string{"load"})
	if err == nil {
		t.Fatal("expected a usage error for `load` with no config path")
	}
	for _, flag := range []string{"--pg", "--dry-run", "--force", "--resume", "--threshold", "--verify", "--noverify"} {
		if !strings.Contains(err.Error(), flag) {
			t.Errorf("expected load's usage string to mention %s, got %q", flag, err.Error())
		}
	}
}
