package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/term"
)

// progressReporter prints a live, single-line progress bar as rows are
// copied into Postgres, redrawn in place via a carriage return — the
// grand total across every table this load will touch, not just whatever
// table happens to be streaming right now. Only draws a live bar when
// stdout is an actual terminal: redrawing over the same line with \r is
// exactly the wrong thing to send to a log file or a piped/redirected
// stdout, where every redraw would show up as its own line. Piped output
// keeps the plain per-table "loaded N row(s)" line this replaced, and
// nothing else — see loadWithProgress in main.go.
type progressReporter struct {
	total       int64
	done        int64
	tableName   string
	isTerminal  bool
	lastPrinted time.Time
}

// progressBarWidth is how many characters wide the "[====------]" bar is.
const progressBarWidth = 30

// progressRedrawInterval throttles redraws so a fast COPY (thousands of
// rows/sec) doesn't spend more time repainting the terminal than loading
// data — sub-frame-rate redraws are wasted on a human reader anyway.
const progressRedrawInterval = 100 * time.Millisecond

// newProgressReporter's total is the sum of every table's row count this
// run will load (already excludes tables skipped via --resume), computed
// up front so the bar has a real denominator from its very first draw.
func newProgressReporter(total int64) *progressReporter {
	return &progressReporter{
		total:      total,
		isTerminal: term.IsTerminal(int(os.Stdout.Fd())),
	}
}

// startTable switches the reporter to attribute subsequent row() calls to
// tableName and redraws immediately, so the name change is visible without
// waiting for the next throttled tick.
func (p *progressReporter) startTable(tableName string) {
	p.tableName = tableName
	p.render(true)
}

// row records one more row loaded, across every table (not reset per
// table), and redraws if the interactive redraw interval has elapsed.
func (p *progressReporter) row() {
	p.done++
	if p.isTerminal {
		p.render(false)
	}
}

// render draws the current state, skipping the draw if force is false and
// less than progressRedrawInterval has passed since the last one.
func (p *progressReporter) render(force bool) {
	if !p.isTerminal {
		return
	}
	if !force && time.Since(p.lastPrinted) < progressRedrawInterval {
		return
	}
	p.lastPrinted = time.Now()

	pct := 0.0
	if p.total > 0 {
		pct = float64(p.done) / float64(p.total) * 100
	}
	filled := int(pct / 100 * progressBarWidth)
	if filled > progressBarWidth {
		filled = progressBarWidth
	}
	bar := strings.Repeat("=", filled) + strings.Repeat("-", progressBarWidth-filled)
	fmt.Printf("\r%s[%s] %d/%d rows (%.1f%%) — %s", clearLine, bar, p.done, p.total, pct, p.tableName)
}

// clearLine is \r followed by the ANSI "erase to end of line" sequence —
// unlike padding the new text with a guessed number of trailing spaces
// (which this used to do, and which left visible leftover characters
// whenever the previous line was longer than expected: a long table name,
// or more digits once row counts climbed past what the guess accounted
// for), this reliably erases whatever was on the line before, regardless
// of length.
const clearLine = "\x1b[K"

// abort clears the live bar and moves to a fresh line, so an error
// message printed right after (e.g. a COPY failure mid-table) neither
// collides with nor leaves behind partial bar text. A no-op for
// non-terminal output, which never leaves an unterminated line to begin
// with.
func (p *progressReporter) abort() {
	if p.isTerminal {
		fmt.Print("\r" + clearLine + "\n")
	}
}

// finishTable clears the live bar and prints a permanent completion line
// for tableName — the bar for the next table (if any) starts fresh below
// it. On non-terminal output this is the only line printed per table,
// unchanged from before the progress bar existed.
func (p *progressReporter) finishTable(tableName string, rows int64) {
	line := fmt.Sprintf("%s: loaded %d row(s)", tableName, rows)
	if p.isTerminal {
		fmt.Print("\r" + clearLine + line + "\n")
		return
	}
	fmt.Println(line)
}

// skipAlreadyLoadedTable reports a table --resume found already fully
// loaded in Postgres (a prior run's COPY committed but crashed before the
// state file recorded it — see the row-count check in executeLoad) —
// rows never streamed through row() this run, so done is bumped directly
// to keep the bar's percentage honest instead of quietly undercounting
// for the rest of the load.
func (p *progressReporter) skipAlreadyLoadedTable(tableName string, rows int64) {
	p.done += rows
	// Callers pass the source's own row count for exactly this table, so
	// done should never actually exceed total by construction — but
	// clamped defensively anyway (Copilot PR #99 finding) so a >100% bar
	// can't happen even if that assumption is ever violated.
	if p.done > p.total {
		p.done = p.total
	}
	line := fmt.Sprintf("%s: already loaded (%d row(s), found by --resume)", tableName, rows)
	if p.isTerminal {
		fmt.Print("\r" + clearLine + line + "\n")
		return
	}
	fmt.Println(line)
}
