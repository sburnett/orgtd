package ui

import (
	"sort"
	"strings"
	"time"

	"github.com/sburnett/orgtd/internal/meetings"
	"github.com/sburnett/orgtd/internal/org"
	"github.com/sburnett/orgtd/internal/orgdate"
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
// regardless of fold state — used by searchRows (see search.go) to build
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
		start, ok := meetings.TimeProperty(h, "GCAL_START")
		if !ok {
			continue
		}
		day := orgdate.TruncateToDate(start.Local())
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
			si, _ := meetings.TimeProperty(g.events[i], "GCAL_START")
			sj, _ := meetings.TimeProperty(g.events[j], "GCAL_START")
			return si.Before(sj)
		})
		*dst = append(*dst, row{section: day.Format("2006-01-02 Mon")})
		m.appendCalendarHeadlines(dst, g.events, ignoreFold)
	}
}

// enterCalendarView switches to calendarView with the cursor already on
// the meeting currently in progress, or the most recently started past
// meeting if none is (see findCalendarCursorTarget), scrolled to the
// middle of the screen (see centerOnCursor) so you land right where the
// day already is rather than at the top or edge of the page — landing at
// row 0 (switchToView's own default), same as any other view switch, if
// neither applies (e.g. every synced event is still upcoming, or
// calendar_file isn't loaded/has nothing synced).
func (m *Model) enterCalendarView() {
	m.switchToView(calendarView)
	f := m.findCalendarFile()
	if f == nil {
		return
	}
	if h := findCalendarCursorTarget(f, time.Now()); h != nil {
		for i, r := range m.rows {
			if r.headline == h {
				m.cursor = i
				m.centerOnCursor()
				break
			}
		}
	}
}

// findCalendarCursorTarget picks the event enterCalendarView should land
// the cursor on: among every headline in f with a GCAL_START at or
// before now, the one with the latest start that's still in progress
// (now before its GCAL_END) wins outright; failing that, the one with
// the latest start overall (in progress or not — an unparseable/missing
// GCAL_END, or one already elapsed, both fall here) — i.e. "the current
// meeting, or the prior one if none is current". nil if nothing in f
// has a GCAL_START at or before now at all (every synced event is still
// upcoming, or f has no synced events).
func findCalendarCursorTarget(f *org.File, now time.Time) *org.Headline {
	var current, prior *org.Headline
	var currentStart, priorStart time.Time
	for _, h := range f.Headlines {
		start, ok := meetings.TimeProperty(h, "GCAL_START")
		if !ok || start.After(now) {
			continue
		}
		if end, ok := meetings.TimeProperty(h, "GCAL_END"); ok && now.Before(end) {
			if current == nil || start.After(currentStart) {
				current, currentStart = h, start
			}
			continue
		}
		if prior == nil || start.After(priorStart) {
			prior, priorStart = h, start
		}
	}
	if current != nil {
		return current
	}
	return prior
}

// linkedMeetingItems returns every entry, elsewhere in the workspace,
// linked to calendar event h — attached via "gM", or sharing a tag with
// it (e.g. a confirmed attendee's "@username" — see meetings.Index.LinkedItems
// for both) — the same items the agenda's Meetings section groups under
// a meeting header (see appendMeetingsSection), but surfaced here
// alongside the meeting itself in calendarView (see
// appendCalendarHeadlines), rather than only for a meeting starting
// today or within the next 24 hours. nil if h isn't itself a synced
// calendar event (no GCAL_EVENT_ID) or has nothing linked.
//
// Reordered from meetings.Index.LinkedItems's own file/tree order into ascending
// CREATED order (items with no parseable CREATED keep their relative
// tree-order position — see headlineCreatedTime), so that entries
// insertCalendarCapture (o/O from this view — see capture.go) adds to the
// end of the inbox still show up here in the order they were actually
// captured, even after being filed away into some other file whose
// position in file/tree order no longer reflects when it happened.
func (m *Model) linkedMeetingItems(h *org.Headline) []*org.Headline {
	key, ok := meetings.Identity(h)
	if !ok {
		return nil
	}
	// A copy: the index's slice is shared, and sorting it in place would
	// reorder it for every other caller.
	items := append([]*org.Headline(nil), m.meetingIndex().LinkedItems(key.Kind, key.ID)...)
	sort.SliceStable(items, func(i, j int) bool {
		ti, iok := headlineCreatedTime(items[i])
		tj, jok := headlineCreatedTime(items[j])
		if !iok || !jok {
			return false
		}
		return ti.Before(tj)
	})
	return items
}

// headlineCreatedTime parses h's CREATED property (set by insertHeadlineAt
// on every new entry, e.g. "[2026-09-24 Thu 14:32]") back into a
// time.Time, for ordering entries by when they were actually created
// (see linkedMeetingItems) rather than by their position in the outline.
// ok is false if CREATED is missing, or set to something orgdate.ParseFlexible
// can't read (e.g. hand-edited).
func headlineCreatedTime(h *org.Headline) (time.Time, bool) {
	raw := strings.Trim(h.Properties["CREATED"], "[]")
	if raw == "" {
		return time.Time{}, false
	}
	t, _, err := orgdate.ParseFlexible(raw)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// calendarEventForRow resolves the calendar event a calendarView row r is
// associated with, for insertCalendarCapture (o/O — see capture.go): r's
// own headline if it's itself a synced calendar event (covers both the
// event's own row, isCalendarItem, and one of its folded-open body-line
// rows, which share the same headline — see appendCalendarHeadlines), or
// linkedFromEvent if r is an isCalendarLinkedItem row instead. ok is
// false for anything else (a day's section-header row), or if
// linkedFromEvent is unset on a linked-item row (shouldn't happen in
// practice, but leaves nothing to resolve either way).
func calendarEventForRow(r row) (*org.Headline, bool) {
	if r.isCalendarLinkedItem {
		return r.linkedFromEvent, r.linkedFromEvent != nil
	}
	if r.headline != nil {
		if _, ok := meetings.Identity(r.headline); ok {
			return r.headline, true
		}
	}
	return nil, false
}
