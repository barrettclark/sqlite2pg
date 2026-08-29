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
	// \r returns to column 0; the trailing spaces overwrite anything
	// longer left over from the previous draw (e.g. a longer table name)
	// without needing an ANSI clear-to-end-of-line sequence.
	fmt.Printf("\r[%s] %d/%d rows (%.1f%%) — %s%s", bar, p.done, p.total, pct, p.tableName, strings.Repeat(" ", 10))
}

// abort prints a trailing newline if a live bar is currently drawn, so an
// error message printed right after (e.g. a COPY failure mid-table) lands
// on its own line instead of running into whatever partial bar text is
// still sitting on the terminal's current line. A no-op for non-terminal
// output, which never leaves an unterminated line to begin with.
func (p *progressReporter) abort() {
	if p.isTerminal {
		fmt.Println()
	}
}

// finishTable clears the live bar and prints a permanent completion line
// for tableName — the bar for the next table (if any) starts fresh below
// it. On non-terminal output this is the only line printed per table,
// unchanged from before the progress bar existed.
func (p *progressReporter) finishTable(tableName string, rows int64) {
	line := fmt.Sprintf("%s: loaded %d row(s)", tableName, rows)
	if p.isTerminal {
		// Pad past whatever the last bar redraw left on this line (a
		// longer table name, more digits) before the newline commits it
		// to scrollback — trailing spaces are invisible either way.
		fmt.Printf("\r%s%s\n", line, strings.Repeat(" ", 20))
		return
	}
	fmt.Println(line)
}
