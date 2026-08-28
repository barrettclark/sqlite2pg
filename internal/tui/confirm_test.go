package tui

import (
	"testing"

	"github.com/gdamore/tcell/v2"

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
