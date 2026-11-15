package ui

import (
	"github.com/sburnett/orgtd/internal/org"
	"github.com/sburnett/orgtd/internal/orgdate"
)

// repeatAdvanceAction builds the undo action for completing a repeating
// item, from the change orgdate.ChangeState computed (sc.Repeat). This
// replicates real org-mode: marking a repeating item DONE (or any other
// done-class keyword) never actually leaves it in that state — org
// leaves the keyword untouched, advances whichever of SCHEDULED/DEADLINE
// repeats, and records the completion via the :LAST_REPEAT: property
// instead of CLOSED, so a completed recurring item shows up as its next
// Upcoming occurrence rather than vanishing into a DONE state it never
// really enters.
func (m *Model) repeatAdvanceAction(h *org.Headline, sc orgdate.StateChange) undoAction {
	oldLastRepeat, hadLastRepeat := h.Properties["LAST_REPEAT"]
	return &repeatAdvanceAction{
		h:             h,
		f:             m.ws.FileOf(h),
		oldScheduled:  h.Scheduled,
		newScheduled:  sc.Scheduled,
		oldDeadline:   h.Deadline,
		newDeadline:   sc.Deadline,
		oldLastRepeat: oldLastRepeat,
		newLastRepeat: sc.LastRepeat,
		hadLastRepeat: hadLastRepeat,
	}
}
