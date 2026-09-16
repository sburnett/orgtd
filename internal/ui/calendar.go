package ui

import (
	"sort"
	"time"

	"github.com/sburnett/orgtd/internal/org"
)

// appendCalendarRows populates m.rows for calendarView: every headline
// in the calendar file (see findCalendarFile/WithCalendarFile), grouped
// under one flush-left day-header row per calendar day (see row.section)
// in chronological order — both the days themselves and, within each
// day, the events on it, by start time. A headline with no parseable
// GCAL_START (i.e. not one :sync-calendar wrote — see
// internal/calendarsync/convert.go)
// is left out entirely, same as a missing calendar file: there's
// nothing to date it by. Rendered via appendCalendarHeadlines — folding,
// body lines, marks, and every per-headline command (i/dd/r/gd/...) all
// work as they do in the outline, except each event starts folded (see
// appendCalendarHeadlines) and shows its time before the title instead
// of a keyword (see renderCalendarItemRowWithBg) — the timestamp and
// meeting details in its body (location, description, link) are
// redundant with that and the day-header grouping above, so they stay
// one Tab away rather than showing by default; the link is also always
// reachable straight from the status line (see calendarEventLinks).
func (m *Model) appendCalendarRows() {
	f := m.findCalendarFile()
	if f == nil {
		return
	}

	type dayGroup struct {
		day    time.Time
		events []*org.Headline
	}
	groups := make(map[time.Time]*dayGroup)
	var days []time.Time
	for _, h := range f.Headlines {
		start, ok := parseRFC3339Property(h, "GCAL_START")
		if !ok {
			continue
		}
		day := truncateToDate(start.Local())
		g, exists := groups[day]
		if !exists {
			g = &dayGroup{day: day}
			groups[day] = g
			days = append(days, day)
		}
		g.events = append(g.events, h)
	}
	sort.Slice(days, func(i, j int) bool { return days[i].Before(days[j]) })

	for _, day := range days {
		g := groups[day]
		sort.SliceStable(g.events, func(i, j int) bool {
			si, _ := parseRFC3339Property(g.events[i], "GCAL_START")
			sj, _ := parseRFC3339Property(g.events[j], "GCAL_START")
			return si.Before(sj)
		})
		m.rows = append(m.rows, row{section: day.Format("2006-01-02 Mon")})
		m.appendCalendarHeadlines(g.events)
	}
}

// linkedMeetingItems returns every entry, elsewhere in the workspace,
// attached to calendar event h via "gM" (see entriesForMeeting) — the
// same items the agenda's Meetings section groups under a meeting
// header (see appendMeetingsSection), but surfaced here alongside the
// meeting itself in calendarView (see appendCalendarHeadlines), rather
// than only for a meeting starting today or within the next 24 hours.
// nil if h isn't itself a synced calendar event (no GCAL_EVENT_ID) or
// has nothing attached.
func (m *Model) linkedMeetingItems(h *org.Headline) []*org.Headline {
	eventID := h.Properties["GCAL_EVENT_ID"]
	if eventID == "" {
		return nil
	}
	id, kind := eventID, oneOffMeeting
	if recurID := h.Properties["GCAL_RECURRING_EVENT_ID"]; recurID != "" {
		id, kind = recurID, recurringMeeting
	}
	return m.entriesForMeeting(kind, id)
}
