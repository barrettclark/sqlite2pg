package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"sqlite2pg/internal/review"
)

// defaultWidth and defaultHeight size the Model before Bubble Tea's first
// tea.WindowSizeMsg arrives with the terminal's actual dimensions.
const (
	defaultWidth  = 80
	defaultHeight = 24
)

// Run drives the review TUI against st in the current terminal, blocking
// until the human finishes (review.OutcomeConfirmed) or cancels
// (review.OutcomeCancelled) — check st.Outcome() after Run returns nil to
// see which. It mirrors the blocking contract review.Run (the old HTTP
// server entrypoint) had: nothing touches Postgres until the human commits.
func Run(ctx context.Context, st *review.State) error {
	m := New(st, defaultWidth, defaultHeight)
	p := tea.NewProgram(m, tea.WithContext(ctx))
	_, err := p.Run()
	return err
}
