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
// ignoreFold, when true, descends into every event's body/children
// regardless of fold state — used by searchRows (see model.go) to build
// the full calendar text search scans, so a match inside a folded
// event's Location/description body isn't skipped.
func (m *Model) appendCalendarRows(dst *[]row, ignoreFold bool) {
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
		*dst = append(*dst, row{section: day.Format("2006-01-02 Mon")})
		m.appendCalendarHeadlines(dst, g.events, ignoreFold)
	}
}

// linkedMeetingItems returns every entry, elsewhere in the workspace,
// linked to calendar event h — attached via "gM", or sharing a tag with
// it (e.g. a confirmed attendee's "@username" — see entriesForMeeting
// for both) — the same items the agenda's Meetings section groups under
// a meeting header (see appendMeetingsSection), but surfaced here
// alongside the meeting itself in calendarView (see
// appendCalendarHeadlines), rather than only for a meeting starting
// today or within the next 24 hours. nil if h isn't itself a synced
// calendar event (no GCAL_EVENT_ID) or has nothing linked.
func (m *Model) linkedMeetingItems(h *org.Headline) []*org.Headline {
	kind, id, ok := meetingIdentity(h)
	if !ok {
		return nil
	}
	return m.entriesForMeeting(kind, id)
}
