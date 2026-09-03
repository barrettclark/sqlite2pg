package main

import (
	"testing"
	"time"
)

// nonTerminalReporter builds a progressReporter with isTerminal forced
// false, bypassing newProgressReporter's os.Stdout.Fd() check — go test's
// stdout is never a terminal, so this is the only way to test the
// terminal-mode rendering logic at all.
func nonTerminalReporter(total int64) *progressReporter {
	return &progressReporter{total: total}
}

func terminalReporter(total int64) *progressReporter {
	return &progressReporter{total: total, isTerminal: true}
}

func TestProgressReporter_RowIncrementsDone(t *testing.T) {
	p := terminalReporter(10)
	p.row()
	p.row()
	if p.done != 2 {
		t.Errorf("expected done=2, got %d", p.done)
	}
}

func TestProgressReporter_RowIsANoOpWhenNotATerminal(t *testing.T) {
	// row() must never panic or attempt to render when stdout isn't a
	// terminal — the only output for non-interactive runs comes from
	// finishTable's plain per-table line.
	p := nonTerminalReporter(10)
	for i := 0; i < 5; i++ {
		p.row()
	}
	if p.done != 5 {
		t.Errorf("expected done to still track every row, got %d", p.done)
	}
}

func TestProgressReporter_RenderThrottlesToTheRedrawInterval(t *testing.T) {
	p := terminalReporter(100)
	p.render(true) // force: always draws, and sets lastPrinted
	drawnAt := p.lastPrinted

	p.render(false) // called again immediately: too soon, should not redraw
	if p.lastPrinted != drawnAt {
		t.Error("expected render(false) called immediately after a draw to be throttled (skipped)")
	}

	p.lastPrinted = time.Now().Add(-2 * progressRedrawInterval)
	p.render(false) // enough time has passed: should redraw
	if p.lastPrinted == drawnAt {
		t.Error("expected render(false) to redraw once the interval has elapsed")
	}
}

func TestProgressReporter_AbortIsANoOpWhenNotATerminal(t *testing.T) {
	// Non-terminal output never leaves an unterminated line, so abort()
	// must not print anything extra there.
	p := nonTerminalReporter(10)
	p.abort() // must not panic
}

func TestProgressReporter_ZeroTotalDoesNotDivideByZero(t *testing.T) {
	p := terminalReporter(0)
	// Must not panic (e.g. on a table with zero rows) and should treat
	// the percentage as 0, not NaN or Inf.
	p.render(true)
}

func TestProgressReporter_StartTableSetsTheCurrentTableName(t *testing.T) {
	p := terminalReporter(10)
	p.startTable("albums")
	if p.tableName != "albums" {
		t.Errorf("expected tableName albums, got %q", p.tableName)
	}
}
