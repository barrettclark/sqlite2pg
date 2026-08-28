package tui

import (
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func TestTableListKeyCapture_QConsumesTheKey(t *testing.T) {
	m := &model{app: tview.NewApplication(), pages: tview.NewPages()}
	event := tcell.NewEventKey(tcell.KeyRune, 'q', tcell.ModNone)
	if got := m.tableListKeyCapture(event); got != nil {
		t.Errorf("expected q to be consumed (nil), got %v", got)
	}
	if !m.pages.HasPage("confirm") {
		t.Error("expected q to raise the confirm page")
	}
}

func TestTableListKeyCapture_CtrlCPassesThrough(t *testing.T) {
	// Ctrl+C is intentionally no longer special-cased: it should pass
	// through like any other unhandled key now that q/f/c all route
	// through the same confirmation modal.
	m := &model{app: tview.NewApplication(), pages: tview.NewPages()}
	event := tcell.NewEventKey(tcell.KeyCtrlC, 0, tcell.ModNone)
	if got := m.tableListKeyCapture(event); got != event {
		t.Errorf("expected ctrl+c to pass through unchanged, got %v", got)
	}
}

func TestTableListKeyCapture_OtherKeysPassThrough(t *testing.T) {
	m := &model{app: tview.NewApplication()}
	event := tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone)
	if got := m.tableListKeyCapture(event); got != event {
		t.Errorf("expected the down arrow to pass through unchanged, got %v", got)
	}
}
