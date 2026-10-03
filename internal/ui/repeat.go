package ui

import (
	"github.com/sburnett/orgtd/internal/org"
	"github.com/sburnett/orgtd/internal/orgdate"
)

// repeatAdvanceForCompletion builds the undo action for completing h
// when its SCHEDULED and/or DEADLINE is a repeating timestamp, or nil if
// neither is (the ordinary statusChangeAction handles a plain
// completion). This replicates real org-mode: marking a repeating item
// DONE (or any other done-class keyword) never actually leaves it in
// that state — org silently leaves the keyword untouched, advances
// whichever of SCHEDULED/DEADLINE repeats, and records the completion
// via the :LAST_REPEAT: property instead of CLOSED, so a completed
// recurring item shows up as its next Upcoming occurrence rather than
// vanishing into a DONE state it never really enters.
func (m *Model) repeatAdvanceForCompletion(h *org.Headline) undoAction {
	now := m.now()
	newScheduled, schedOK := orgdate.AdvanceRepeating(h.Scheduled, now)
	newDeadline, deadOK := orgdate.AdvanceRepeating(h.Deadline, now)
	if !schedOK && !deadOK {
		return nil
	}
	oldLastRepeat, hadLastRepeat := h.Properties["LAST_REPEAT"]
	return &repeatAdvanceAction{
		h:             h,
		f:             m.ws.FileOf(h),
		oldScheduled:  h.Scheduled,
		newScheduled:  newScheduled,
		oldDeadline:   h.Deadline,
		newDeadline:   newDeadline,
		oldLastRepeat: oldLastRepeat,
		newLastRepeat: "[" + now.Format("2006-01-02 Mon 15:04") + "]",
		hadLastRepeat: hadLastRepeat,
	}
}
