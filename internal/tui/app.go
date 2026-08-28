package tui

import (
	"context"

	"github.com/rivo/tview"

	"sqlite2pg/internal/review"
)

// model holds everything every screen reads and mutates: the review
// session, its current summary, and the tview primitives wired together
// as pages. Unlike the previous Bubble Tea implementation's immutable
// Model value threaded through Update(), tview's widgets are long-lived
// and mutated in place, so screens are methods on *model that update
// these fields and widgets directly.
type model struct {
	st      *review.State
	summary review.ReviewSummary

	app   *tview.Application
	pages *tview.Pages

	tableList *tview.List

	selectedTable string
	grid          *tview.Table
	status        *tview.TextView

	picker       *tview.List
	pickerColumn string

	pendingConfirm confirmState
}

// Run drives the review TUI against st in the current terminal, blocking
// until the human finishes (review.OutcomeConfirmed) or cancels
// (review.OutcomeCancelled) — check st.Outcome() after Run returns nil to
// see which. Mirrors the exact blocking contract the previous Bubble Tea
// Run had: nothing touches Postgres until the human commits.
func Run(ctx context.Context, st *review.State) error {
	m := &model{
		st:      st,
		summary: st.Summary(),
		app:     tview.NewApplication(),
		pages:   tview.NewPages(),
	}

	m.buildTableList()
	m.pages.AddPage("tablelist", m.tableList, true, true)
	m.app.SetRoot(m.pages, true)

	go func() {
		<-ctx.Done()
		m.app.Stop()
	}()

	return m.app.Run()
}
