package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

// TestDetermineVerify_NonTerminalPipeEchoesPromptAndAnswer is issue #120's
// (L11) regression: the piped-stdin branch consumed the answer without
// ever printing the prompt, so `sqlite2pg load < answers.txt` showed no
// record of what was asked or answered — only the timeout message, and
// only when nothing arrived. The transcript should now read like an
// interactive one: the prompt, then the answer.
func TestDetermineVerify_NonTerminalPipeEchoesPromptAndAnswer(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer r.Close()
	// "yes" rather than "y": "y" is a substring of the "[y/N]" prompt, so
	// a test asserting the answer was echoed has to use a token the
	// prompt itself doesn't contain.
	if _, err := w.WriteString("yes\n"); err != nil {
		t.Fatalf("writing scripted answer: %v", err)
	}
	w.Close()

	var out strings.Builder
	done := make(chan bool, 1)
	go func() { done <- determineVerify(verifyPrompt, r, &out) }()

	select {
	case got := <-done:
		if !got {
			t.Error("determineVerify(yes) = false, want true")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("determineVerify blocked")
	}

	const prompt = "Run sqlite2pg verify now? [y/N]: "
	s := out.String()
	if !strings.HasPrefix(s, prompt) {
		t.Fatalf("piped-stdin path did not print the prompt first; got %q", s)
	}
	if rest := strings.TrimPrefix(s, prompt); !strings.HasPrefix(strings.TrimSpace(rest), "yes") {
		t.Errorf("piped-stdin path did not echo the answer after the prompt; got %q", s)
	}
}
