package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"sqlite2pg/internal/review"
)

func TestShowConfirm_FinishTrueCallsFinishOnYes(t *testing.T) {
	st, _ := newTestState(t)
	m := &model{app: newTestApp(), pages: newTestPages(), st: st, summary: st.Summary()}
	m.buildTableList()
	m.pages.AddPage("tablelist", m.tableList, true, true)

	m.showConfirm(true)
	m.confirmDone(0, "Yes") // button index 0 is "Yes" per AddButtons order below

	if st.Outcome() != review.OutcomeConfirmed {
		t.Errorf("expected OutcomeConfirmed, got %v", st.Outcome())
	}
	select {
	case <-st.Done():
	default:
		t.Error("expected Done() to be closed")
	}
}

func TestShowConfirm_CancelTrueCallsCancelOnYes(t *testing.T) {
	st, _ := newTestState(t)
	m := &model{app: newTestApp(), pages: newTestPages(), st: st, summary: st.Summary()}
	m.buildTableList()
	m.pages.AddPage("tablelist", m.tableList, true, true)

	m.showConfirm(false)
	m.confirmDone(0, "Yes")

	if st.Outcome() != review.OutcomeCancelled {
		t.Errorf("expected OutcomeCancelled, got %v", st.Outcome())
	}
}

func TestConfirmDone_NoClosesWithoutChangingOutcome(t *testing.T) {
	st, _ := newTestState(t)
	m := &model{app: newTestApp(), pages: newTestPages(), st: st, summary: st.Summary()}
	m.buildTableList()
	m.pages.AddPage("tablelist", m.tableList, true, true)

	m.showConfirm(true)
	m.confirmDone(1, "No")

	if st.Outcome() != review.OutcomePending {
		t.Errorf("expected OutcomePending after declining, got %v", st.Outcome())
	}
	if m.pages.HasPage("confirm") {
		t.Error("expected the confirm page to be removed after declining")
	}
}

func TestConfirmDone_NoRestoresFocusToUnderlyingPage(t *testing.T) {
	st, _ := newTestState(t)
	m := &model{app: newTestApp(), pages: newTestPages(), st: st, summary: st.Summary()}
	m.buildTableList()
	m.pages.AddPage("tablelist", m.tableList, true, true)
	m.app.SetFocus(m.tableList)

	m.showConfirm(true)
	m.confirmDone(1, "No")

	// Without restoring focus, the app's focus stays on the removed modal
	// primitive and the table list silently stops receiving key events —
	// this is the bug an interactive smoke test (not headless assertions
	// on state) actually caught: "No" visibly returned to the table list
	// but subsequent keypresses did nothing.
	if got := m.app.GetFocus(); got != tview.Primitive(m.tableList) {
		t.Errorf("expected focus restored to tableList after declining, got %v", got)
	}
}

func TestConfirmDone_NoRestoresFocusToGrid(t *testing.T) {
	st, _ := newTestState(t)
	m := &model{app: newTestApp(), pages: newTestPages(), st: st, summary: st.Summary()}
	m.status = tview.NewTextView()
	m.buildTableList()
	tableName := m.summary.Tables[0].Name
	m.onTableSelected(0, tableName, "", 0)

	m.showConfirm(false)
	m.confirmDone(1, "No")

	if got := m.app.GetFocus(); got != tview.Primitive(m.grid) {
		t.Errorf("expected focus restored to grid after declining, got %v", got)
	}
}

func TestTableListKeyCapture_FRaisesFinishConfirmation(t *testing.T) {
	st, _ := newTestState(t)
	m := &model{app: newTestApp(), pages: newTestPages(), st: st, summary: st.Summary()}
	m.buildTableList()
	m.pages.AddPage("tablelist", m.tableList, true, true)

	event := tcell.NewEventKey(tcell.KeyRune, 'f', tcell.ModNone)
	if got := m.tableListKeyCapture(event); got != nil {
		t.Errorf("expected f to be consumed (nil), got %v", got)
	}
	if !m.pages.HasPage("confirm") {
		t.Error("expected a confirm page to be shown")
	}
}

// TestConfirmDone_FinishErrorFromTableListDoesNotPanic is a regression test
// for a reachable nil-pointer panic: m.status used to be created lazily
// inside buildGrid, so a user who pressed f at the table list (before ever
// opening a table) and hit a Finish() error would reach
// m.status.SetText(...) in confirmDone while m.status was still nil. This
// builds a model the way Run now does (m.status created up front, before
// buildTableList or anything else), stays on the table list the whole
// time (no grid ever built), and forces st.Finish() to fail by deleting
// the config's parent directory out from under it. The test passing at
// all (no panic) is the regression check; it also asserts the error is
// now surfaced via a modal page rather than the (nonexistent, from this
// screen) status line.
func TestConfirmDone_FinishErrorFromTableListDoesNotPanic(t *testing.T) {
	st, path := newTestState(t)
	m := &model{st: st, summary: st.Summary(), app: newTestApp(), pages: newTestPages()}
	m.status = tview.NewTextView()
	m.status.SetDynamicColors(false)
	m.buildTableList()
	m.pages.AddPage("tablelist", m.tableList, true, true)

	if err := os.RemoveAll(filepath.Dir(path)); err != nil {
		t.Fatalf("removing config dir: %v", err)
	}

	m.showConfirm(true)
	m.confirmDone(0, "Yes") // no panic here is the regression check

	if !m.pages.HasPage("error") {
		t.Error("expected an error page to be shown after Finish() failed")
	}
}
