package ui

import (
	"sort"
	"time"

	"github.com/sburnett/orgtd/internal/agenda"
	"github.com/sburnett/orgtd/internal/org"
	"github.com/sburnett/orgtd/internal/orgdate"
)

// agendaSections lists the agenda's sections, in display order.
var agendaSections = []string{agenda.Overdue, agenda.DueToday, agenda.Upcoming, agenda.NextActions, "Meetings"}

// appendAgendaRows appends the rows for agenda view: one section-header
// row per non-empty section (Overdue, Due Today, Upcoming, Next
// Actions, Meetings, in that order). The date-based sections and Next
// Actions come from internal/agenda (see agenda.BySection); Meetings is
// built here, from the meetings index.
func (m *Model) appendAgendaRows(dst *[]row) {
	today := orgdate.TruncateToDate(m.now())
	bySection := agenda.BySection(m.ws, m.cfg, today)

	for _, section := range agendaSections {
		switch section {
		case agenda.NextActions:
			m.appendNextActionsSection(dst)
			continue
		case "Meetings":
			m.appendMeetingsSection(dst)
			continue
		}
		es := bySection[section]
		if len(es) == 0 {
			continue
		}
		*dst = append(*dst, row{kind: rowSection, text: section})
		for _, e := range es {
			*dst = append(*dst, row{headline: e.Headline, level: 1, kind: rowAgendaItem, agendaLabel: e.Label, agendaDate: e.Date, agendaRepeater: e.Repeater, agendaMissed: e.Missed})
		}
	}
}

func (m *Model) appendNextActionsSection(dst *[]row) {
	next := agenda.NextActionHeadlines(m.ws, m.cfg)
	if len(next) == 0 {
		return
	}
	*dst = append(*dst, row{kind: rowSection, text: "Next Actions"})
	for _, h := range next {
		*dst = append(*dst, row{headline: h, level: 1, kind: rowAgendaItem})
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
func (m *Model) appendMeetingsSection(dst *[]row) {
	meetings := m.upcomingMeetingEntries(m.now())
	if len(meetings) == 0 {
		return
	}
	*dst = append(*dst, row{kind: rowSection, text: "Meetings"})
	for _, mt := range meetings {
		*dst = append(*dst, row{level: 1, kind: rowMeetingHeader, meetingTitle: mt.title, meetingStart: mt.start, meetingEnd: mt.end})
		for _, h := range mt.items {
			*dst = append(*dst, row{headline: h, level: 2, kind: rowAgendaItem, meetingItemTitle: mt.title, meetingItemStart: mt.start})
		}
	}
}
