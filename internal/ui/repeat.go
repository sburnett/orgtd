package ui

import (
	"strconv"
	"strings"
	"time"

	"github.com/sburnett/orgtd/internal/org"
)

// advanceRepeatingTimestamp computes ts's next occurrence for the
// "complete a repeating item" flow (see repeatAdvanceForCompletion),
// replicating org-mode's own three repeater marks:
//
//   - "+"  (simple): exactly one interval past the old date, however
//     overdue that leaves it — org's plain, "naive" advance.
//   - "++" (catch-up): one or more intervals past the old date, however
//     many it takes to land on or after today, so completing a
//     long-overdue item doesn't just make it overdue by one interval
//     less.
//   - ".+" (from-now): one interval past *today*, the completion date,
//     ignoring how stale the old date was.
//
// This mirrors repeaterCurrentOccurrence's date/warning-cookie parsing,
// but that function computes a read-only, display-time "current
// occurrence" for the agenda and never rolls forward past today (so a
// skipped occurrence stays visibly Overdue); this one computes the new
// date to actually write back to the file once, at the moment the item
// is completed — the two serve different purposes and must stay
// separate. ok is false if ts has no repeater cookie, or its date
// portion doesn't parse, in which case ts itself is returned unchanged.
func advanceRepeatingTimestamp(ts *org.Timestamp, now time.Time) (next *org.Timestamp, ok bool) {
	if ts == nil {
		return ts, false
	}
	m := repeaterRe.FindStringSubmatch(ts.Raw)
	if m == nil {
		return ts, false
	}
	mark, numStr, unit := m[1], m[2], m[3][0]
	n, err := strconv.Atoi(numStr)
	if err != nil || n <= 0 {
		return ts, false
	}

	withoutRepeat := repeaterRe.ReplaceAllString(ts.Raw, "")
	warning := warningRe.FindString(withoutRepeat)
	dateText := strings.Join(strings.Fields(warningRe.ReplaceAllString(withoutRepeat, "")), " ")

	var base time.Time
	hasTime := false
	parsed := false
	for _, layout := range dateInputLayouts {
		if t, err := time.ParseInLocation(layout, dateText, time.Local); err == nil {
			base, hasTime, parsed = t, strings.Contains(layout, "15:04"), true
			break
		}
	}
	if !parsed {
		return ts, false
	}

	today := truncateToDate(now)
	var result time.Time
	switch mark {
	case ".+":
		result = repeaterStep(today, n, unit)
		if hasTime {
			result = time.Date(result.Year(), result.Month(), result.Day(), base.Hour(), base.Minute(), 0, 0, result.Location())
		}
	case "++":
		result = repeaterStep(base, n, unit)
		for truncateToDate(result).Before(today) {
			result = repeaterStep(result, n, unit)
		}
	default: // "+"
		result = repeaterStep(base, n, unit)
	}

	format := "2006-01-02 Mon"
	if hasTime {
		format = "2006-01-02 Mon 15:04"
	}
	raw := result.Format(format) + " " + mark + numStr + string(unit)
	if warning != "" {
		raw += " " + warning
	}
	return &org.Timestamp{Active: ts.Active, Raw: raw}, true
}

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
	now := time.Now()
	newScheduled, schedOK := advanceRepeatingTimestamp(h.Scheduled, now)
	newDeadline, deadOK := advanceRepeatingTimestamp(h.Deadline, now)
	if !schedOK && !deadOK {
		return nil
	}
	oldLastRepeat, hadLastRepeat := h.Properties["LAST_REPEAT"]
	return &repeatAdvanceAction{
		h:             h,
		f:             m.fileForHeadline(h),
		oldScheduled:  h.Scheduled,
		newScheduled:  newScheduled,
		oldDeadline:   h.Deadline,
		newDeadline:   newDeadline,
		oldLastRepeat: oldLastRepeat,
		newLastRepeat: "[" + now.Format("2006-01-02 Mon 15:04") + "]",
		hadLastRepeat: hadLastRepeat,
	}
}
