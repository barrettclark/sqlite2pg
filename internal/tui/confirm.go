package tui

import (
	"fmt"

	"github.com/rivo/tview"
)

// confirmState records which action ("finish" or "cancel") the
// currently-shown confirm modal is for, so confirmDone knows what "Yes"
// means. It's overwritten each time showConfirm is called; there's no
// zero-value/unset state to track since a modal is only ever shown right
// after showConfirm sets this.
type confirmState struct {
	finish bool
}

// showConfirm raises the Finish (finish is true) or Cancel confirmation
// modal over whichever screen is currently active.
func (m *model) showConfirm(finish bool) {
	m.pendingConfirm = confirmState{finish: finish}

	verb := "Cancel"
	if finish {
		verb = "Confirm & Import"
	}
	modal := tview.NewModal()
	modal.SetText(fmt.Sprintf("%s?", verb))
	modal.AddButtons([]string{"Yes", "No"})
	modal.SetDoneFunc(m.confirmDone)

	if m.pages.HasPage("confirm") {
		m.pages.RemovePage("confirm")
	}
	m.pages.AddPage("confirm", modal, true, true)
	m.app.SetFocus(modal)
}

// confirmDone handles the modal's button press: "Yes" (button index 0)
// commits the pending action and stops the application; anything else
// closes the modal without side effects.
func (m *model) confirmDone(buttonIndex int, buttonLabel string) {
	if buttonLabel != "Yes" {
		m.dismissPage("confirm")
		return
	}
	if m.pendingConfirm.finish {
		if err := m.st.Finish(); err != nil {
			m.dismissPage("confirm")
			m.showError(fmt.Sprintf("finish failed: %s", err))
			return
		}
	} else {
		m.st.Cancel()
	}
	m.app.Stop()
}

// dismissPage removes the named overlay page and restores keyboard focus
// to whichever page is now on top — without this, closing an overlay
// (the confirm modal, an error modal) leaves focus on the removed
// primitive and the underlying page silently stops responding to keys.
func (m *model) dismissPage(name string) {
	m.pages.RemovePage(name)
	_, front := m.pages.GetFrontPage()
	if front != nil {
		m.app.SetFocus(front)
	}
}

// showError displays msg in a modal with a single "OK" button, visible
// regardless of which page is currently active — unlike the status line,
// which is only part of the grid's layout and isn't visible from the
// table list.
func (m *model) showError(msg string) {
	modal := tview.NewModal()
	modal.SetText(msg)
	modal.AddButtons([]string{"OK"})
	modal.SetDoneFunc(func(buttonIndex int, buttonLabel string) {
		m.dismissPage("error")
	})
	if m.pages.HasPage("error") {
		m.pages.RemovePage("error")
	}
	m.pages.AddPage("error", modal, true, true)
	m.app.SetFocus(modal)
}
