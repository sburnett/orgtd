package ui

import (
	"sort"
	"time"

	"github.com/sburnett/orgtd/internal/org"
)

// agendaSections lists the agenda's sections, in display order.
var agendaSections = []string{"Overdue", "Due Today", "Upcoming", "Next Actions"}

// parseTimestampDate returns ts's date, with any time-of-day dropped, if
// it can be parsed as one of dateInputLayouts — the same layouts
// accepted when typing a date into the deadline prompt, which covers
// every format orgtd itself writes (see applyDeadlineInput) plus a
// weekday-less fallback for hand-edited files. A Raw value in some other
// shape (a repeater, a range, or anything else emacs/org-mode might
// produce that orgtd doesn't write itself) is simply excluded from the
// agenda rather than guessed at.
func parseTimestampDate(ts *org.Timestamp) (time.Time, bool) {
	if ts == nil {
		return time.Time{}, false
	}
	for _, layout := range dateInputLayouts {
		// ParseInLocation (not Parse, which defaults to UTC) so the
		// result is comparable against truncateToDate(time.Now()), which
		// is anchored to the local zone — otherwise a date that's
		// "today" locally could parse as a different instant and get
		// bucketed into the wrong section near a timezone's UTC offset.
		if t, err := time.ParseInLocation(layout, ts.Raw, time.Local); err == nil {
			return truncateToDate(t), true
		}
	}
	return time.Time{}, false
}

// agendaEntry is one (headline, relevant date) pair destined for the
// agenda — a headline with both SCHEDULED and DEADLINE produces two.
type agendaEntry struct {
	h     *org.Headline
	label string // "Scheduled" or "Deadline"
	date  time.Time
}

// agendaEntries collects one entry per (headline, relevant date) pair
// across every file in the workspace, for every headline that's active
// (not DONE/CANCELLED) and not SOMEDAY — SOMEDAY is excluded from the
// agenda entirely, even for a date within the window, since it's
// explicitly the "not committed to a date" state. Only dates on or
// before today+windowDays are collected; anything further out (or a
// SCHEDULED/DEADLINE whose Raw text can't be parsed) is dropped.
func (m *Model) agendaEntries(today time.Time, windowDays int) []agendaEntry {
	var entries []agendaEntry
	horizon := today.AddDate(0, 0, windowDays)
	consider := func(ts *org.Timestamp, label string, h *org.Headline) {
		date, ok := parseTimestampDate(ts)
		if !ok || date.After(horizon) {
			return
		}
		entries = append(entries, agendaEntry{h: h, label: label, date: date})
	}
	for _, f := range m.ws.Files {
		org.Walk(f.Headlines, func(h *org.Headline) {
			if org.IsDoneKeyword(h.Keyword) || h.Keyword == "SOMEDAY" {
				return
			}
			consider(h.Deadline, "Deadline", h)
			consider(h.Scheduled, "Scheduled", h)
		})
	}
	return entries
}

// agendaSection buckets date relative to today: "Overdue" (before
// today), "Due Today", or "Upcoming" (after today — callers are
// expected to have already excluded anything beyond the window).
func agendaSection(date, today time.Time) string {
	switch {
	case date.Before(today):
		return "Overdue"
	case date.Equal(today):
		return "Due Today"
	default:
		return "Upcoming"
	}
}

// nextActionHeadlines returns every NEXT headline across the workspace,
// in file/tree order — regardless of whether it has a SCHEDULED or
// DEADLINE date, since NEXT is GTD's "immediately actionable" state on
// its own merits, independent of timing. A NEXT item that also has a
// near-term date still additionally appears in its date-based section
// too — the two sections answer different questions ("what's next to
// work on" vs. "what's due when").
func (m *Model) nextActionHeadlines() []*org.Headline {
	var next []*org.Headline
	for _, f := range m.ws.Files {
		org.Walk(f.Headlines, func(h *org.Headline) {
			if h.Keyword == "NEXT" {
				next = append(next, h)
			}
		})
	}
	return next
}

// appendAgendaRows populates m.rows for agenda view: one section-header
// row per non-empty section (Overdue, Due Today, Upcoming, Next
// Actions, in that order). The three date-based sections' item rows are
// sorted by date, ties broken by original file/tree order; Next
// Actions' are left in plain file/tree order, since most NEXT items
// have no date to sort by.
func (m *Model) appendAgendaRows() {
	today := truncateToDate(time.Now())
	entries := m.agendaEntries(today, m.agendaDays)

	bySection := make(map[string][]agendaEntry, len(agendaSections))
	for _, e := range entries {
		s := agendaSection(e.date, today)
		bySection[s] = append(bySection[s], e)
	}

	for _, section := range agendaSections {
		if section == "Next Actions" {
			m.appendNextActionsSection()
			continue
		}
		es := bySection[section]
		if len(es) == 0 {
			continue
		}
		sort.SliceStable(es, func(i, j int) bool { return es[i].date.Before(es[j].date) })
		m.rows = append(m.rows, row{section: section})
		for _, e := range es {
			m.rows = append(m.rows, row{headline: e.h, level: 1, isAgendaItem: true, agendaLabel: e.label, agendaDate: e.date})
		}
	}
}

func (m *Model) appendNextActionsSection() {
	next := m.nextActionHeadlines()
	if len(next) == 0 {
		return
	}
	m.rows = append(m.rows, row{section: "Next Actions"})
	for _, h := range next {
		m.rows = append(m.rows, row{headline: h, level: 1, isAgendaItem: true})
	}
}
