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

// showConfirm raises the Finish ("finish is true) or Cancel confirmation
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
		m.pages.RemovePage("confirm")
		return
	}
	if m.pendingConfirm.finish {
		if err := m.st.Finish(); err != nil {
			m.status.SetText(fmt.Sprintf("error: %s", err))
			m.pages.RemovePage("confirm")
			return
		}
	} else {
		m.st.Cancel()
	}
	m.app.Stop()
}
