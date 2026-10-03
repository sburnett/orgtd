package ui

import (
	"sort"
	"time"

	"github.com/sburnett/orgtd/internal/org"
	"github.com/sburnett/orgtd/internal/orgdate"
)

// agendaSections lists the agenda's sections, in display order.
var agendaSections = []string{"Overdue", "Due Today", "Upcoming", "Next Actions", "Meetings"}

// agendaEntry is one (headline, relevant date) pair destined for the
// agenda — a headline with both SCHEDULED and DEADLINE produces two.
type agendaEntry struct {
	h        *org.Headline
	label    string // "Scheduled" or "Deadline"
	date     time.Time
	repeater string // e.g. "+1w", if date came from a recurring timestamp; empty otherwise
	missed   int    // occurrences elapsed since date without completion; only meaningful (and only shown) when date is Overdue
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
		date, missed, ok := orgdate.ParseTimestampDate(ts, today)
		if !ok || date.After(horizon) {
			return
		}
		entries = append(entries, agendaEntry{h: h, label: label, date: date, repeater: orgdate.RepeaterCookie(ts), missed: missed})
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
	today := orgdate.TruncateToDate(time.Now())
	entries := m.agendaEntries(today, m.agendaDays)

	bySection := make(map[string][]agendaEntry, len(agendaSections))
	for _, e := range entries {
		s := agendaSection(e.date, today)
		bySection[s] = append(bySection[s], e)
	}

	for _, section := range agendaSections {
		switch section {
		case "Next Actions":
			m.appendNextActionsSection()
			continue
		case "Meetings":
			m.appendMeetingsSection()
			continue
		}
		es := bySection[section]
		if len(es) == 0 {
			continue
		}
		sort.SliceStable(es, func(i, j int) bool { return es[i].date.Before(es[j].date) })
		m.rows = append(m.rows, row{kind: rowSection, text: section})
		for _, e := range es {
			missed := 0
			if section == "Overdue" {
				missed = e.missed
			}
			m.rows = append(m.rows, row{headline: e.h, level: 1, kind: rowAgendaItem, agendaLabel: e.label, agendaDate: e.date, agendaRepeater: e.repeater, agendaMissed: missed})
		}
	}
}

func (m *Model) appendNextActionsSection() {
	next := m.nextActionHeadlines()
	if len(next) == 0 {
		return
	}
	m.rows = append(m.rows, row{kind: rowSection, text: "Next Actions"})
	for _, h := range next {
		m.rows = append(m.rows, row{headline: h, level: 1, kind: rowAgendaItem})
	}
}

// meetingAgendaEntry is one calendar meeting occurrence in the
// "Meetings" section's window (see upcomingMeetingEntries), paired with
// the items to show grouped beneath it.
type meetingAgendaEntry struct {
	title      string
	start, end time.Time
	items      []*org.Headline
}

// upcomingMeetingEntries returns one meetingAgendaEntry per calendar
// event (any loaded headline with a GCAL_START — see
// internal/calendarsync/convert.go) whose start falls within [start of today,
// now+24h) — i.e. every meeting starting sometime today or within the
// next 24 hours, current ones included — with at least one item
// elsewhere in the workspace linked to it, whether attached via "gM" or
// sharing a tag with it (see meetings.Index.LinkedItems): something raised
// during, or otherwise linked to, this meeting (a past occurrence, for a
// recurring series; the meeting itself, for a one-off) that might need
// renewed attention or
// discussion this time around. A meeting with nothing linked to it is
// left out entirely, rather than shown with nothing under it. Sorted
// chronologically by start time; a daily (or more frequent) recurring
// meeting can legitimately produce more than one entry for the same
// series within the window (today's occurrence and tomorrow's, say),
// each with its own date/time but the same linked items.
func (m *Model) upcomingMeetingEntries(now time.Time) []meetingAgendaEntry {
	windowStart := orgdate.TruncateToDate(now)
	windowEnd := now.Add(24 * time.Hour)

	ix := m.meetingIndex()
	var upcoming []meetingAgendaEntry
	for _, o := range ix.Occurrences() {
		if !o.HasStart || !o.HasEnd || o.Start.Before(windowStart) || !o.Start.Before(windowEnd) {
			continue
		}
		items := ix.LinkedItems(o.Kind, o.ID)
		if len(items) == 0 {
			continue
		}
		upcoming = append(upcoming, meetingAgendaEntry{title: o.Headline.Title, start: o.Start, end: o.End, items: items})
	}
	sort.SliceStable(upcoming, func(i, j int) bool { return upcoming[i].start.Before(upcoming[j].start) })
	return upcoming
}

// appendMeetingsSection appends the "Meetings" section (see
// upcomingMeetingEntries): a header row per current/upcoming meeting
// (recurring series or one-off) with anything linked to it, each
// followed by its linked items,
// one level deeper (see row.level/rowLevel) than the meeting header,
// which is itself one level deeper than the section header — so the
// outline's generic level-aware navigation (moveDeeper, jumpToSubtreeTop/
// Bottom, etc.) already groups them correctly with no special-casing.
func (m *Model) appendMeetingsSection() {
	meetings := m.upcomingMeetingEntries(time.Now())
	if len(meetings) == 0 {
		return
	}
	m.rows = append(m.rows, row{kind: rowSection, text: "Meetings"})
	for _, mt := range meetings {
		m.rows = append(m.rows, row{level: 1, kind: rowMeetingHeader, meetingTitle: mt.title, meetingStart: mt.start, meetingEnd: mt.end})
		for _, h := range mt.items {
			m.rows = append(m.rows, row{headline: h, level: 2, kind: rowAgendaItem, meetingItemTitle: mt.title, meetingItemStart: mt.start})
		}
	}
}
