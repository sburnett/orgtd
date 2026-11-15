package orgdate

import (
	"time"

	"github.com/sburnett/orgtd/internal/org"
)

// StateChange is what setting a headline's TODO keyword does to it, the
// same for the desktop's "r" and the phone. It's computed by
// ChangeState and applied by Apply.
type StateChange struct {
	// Repeat is true when the change completes a repeating item: as in
	// org-mode, the keyword stays as it is, the repeating SCHEDULED
	// and/or DEADLINE advance, and LastRepeat records the completion
	// instead of CLOSED.
	Repeat bool

	Keyword   string         // the new keyword (unchanged when Repeat)
	Closed    *org.Timestamp // the new CLOSED: stamped on entering a done state, cleared on leaving one
	Scheduled *org.Timestamp // the new SCHEDULED (only advanced when Repeat)
	Deadline  *org.Timestamp // the new DEADLINE (only advanced when Repeat)

	// LastRepeat is the new LAST_REPEAT property value when Repeat.
	LastRepeat string
}

// Stamp formats now as an inactive org timestamp's text: "2026-11-14 Sat 09:12".
func Stamp(now time.Time) string { return now.Format("2006-01-02 Mon 15:04") }

// ChangeState computes setting h's keyword to keyword at now:
//
//   - Entering a done state from an active one on an item with a
//     repeating SCHEDULED or DEADLINE advances the repeater(s) and stamps
//     LAST_REPEAT, leaving the keyword (see StateChange.Repeat).
//   - Otherwise entering a done state stamps CLOSED, and leaving one
//     clears it.
//
// h is not modified.
func ChangeState(h *org.Headline, keyword string, now time.Time) StateChange {
	sc := StateChange{Keyword: keyword, Closed: h.Closed, Scheduled: h.Scheduled, Deadline: h.Deadline}
	closing := org.IsDoneKeyword(keyword) && !org.IsDoneKeyword(h.Keyword)
	if closing {
		newScheduled, schedOK := AdvanceRepeating(h.Scheduled, now)
		newDeadline, deadOK := AdvanceRepeating(h.Deadline, now)
		if schedOK || deadOK {
			sc.Repeat = true
			sc.Keyword = h.Keyword
			sc.Scheduled, sc.Deadline = newScheduled, newDeadline
			sc.LastRepeat = "[" + Stamp(now) + "]"
			return sc
		}
		sc.Closed = &org.Timestamp{Raw: Stamp(now)}
		return sc
	}
	if !org.IsDoneKeyword(keyword) && org.IsDoneKeyword(h.Keyword) {
		sc.Closed = nil
	}
	return sc
}

// Apply makes the change to h.
func (sc StateChange) Apply(h *org.Headline) {
	h.Keyword = sc.Keyword
	h.Closed = sc.Closed
	h.Scheduled = sc.Scheduled
	h.Deadline = sc.Deadline
	if sc.Repeat {
		h.SetProperty("LAST_REPEAT", sc.LastRepeat)
	}
}
