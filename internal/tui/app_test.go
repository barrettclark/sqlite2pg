package tui

import (
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func TestTableListKeyCapture_QConsumesTheKey(t *testing.T) {
	m := &model{app: tview.NewApplication()}
	event := tcell.NewEventKey(tcell.KeyRune, 'q', tcell.ModNone)
	if got := m.tableListKeyCapture(event); got != nil {
		t.Errorf("expected q to be consumed (nil), got %v", got)
	}
}

func TestTableListKeyCapture_CtrlCConsumesTheKey(t *testing.T) {
	m := &model{app: tview.NewApplication()}
	event := tcell.NewEventKey(tcell.KeyCtrlC, 0, tcell.ModNone)
	if got := m.tableListKeyCapture(event); got != nil {
		t.Errorf("expected ctrl+c to be consumed (nil), got %v", got)
	}
}

func TestTableListKeyCapture_OtherKeysPassThrough(t *testing.T) {
	m := &model{app: tview.NewApplication()}
	event := tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone)
	if got := m.tableListKeyCapture(event); got != event {
		t.Errorf("expected the down arrow to pass through unchanged, got %v", got)
	}
}
