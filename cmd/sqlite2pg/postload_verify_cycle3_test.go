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
	if _, err := w.WriteString("y\n"); err != nil {
		t.Fatalf("writing scripted answer: %v", err)
	}
	w.Close()

	var out strings.Builder
	done := make(chan bool, 1)
	go func() { done <- determineVerify(verifyPrompt, r, &out) }()

	select {
	case got := <-done:
		if !got {
			t.Error("determineVerify(y) = false, want true")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("determineVerify blocked")
	}

	s := out.String()
	if !strings.Contains(s, "Run sqlite2pg verify now?") {
		t.Errorf("piped-stdin path did not print the prompt; got %q", s)
	}
	if !strings.Contains(s, "y") {
		t.Errorf("piped-stdin path did not echo the answer; got %q", s)
	}
}
