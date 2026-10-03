package ui

import (
	"strings"
	"time"

	"github.com/sburnett/orgtd/internal/org"
)

func parseRFC3339Property(h *org.Headline, key string) (time.Time, bool) {
	raw, ok := h.Properties[key]
	if !ok {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// calendarEventEntry is one calendar meeting a headline is linked to
// (see calendarEventEntries): its name and link — the same pair
// calendarEventLinks resolves into a "<meeting name>: <url>" string —
// plus its start time, when one can still be resolved (hasWhen is false
// once the meeting has aged out of calendar.org's synced window, the
// same case in which the *_LINKS property snapshot keeps the name/link
// working but there's no live time left to show).
type calendarEventEntry struct {
	title, url string
	when       time.Time
	hasWhen    bool
}

// calendarEventEntries resolves h's calendar-meeting properties into
// one entry per linked meeting, used by infoBufferLines to surface the
// meeting(s) an entry references in its own "Meeting:" section: h's own
// link, if h is itself a synced calendar event (GCAL_HTML_LINK — see
// internal/calendarsync/convert.go); one-off events it's attached to
// via "gM" (GCAL_EVENT_LINKS/GCAL_EVENT_IDS); recurring series it's
// attached to, likewise via "gM" (GCAL_RECURRING_EVENT_LINKS/
// GCAL_RECURRING_EVENT_IDS) — see resolveMeetingEntries for how each of
// the latter two pairs is resolved; and any meeting h is linked to
// purely by sharing a tag with it (see tagLinkedMeetingCandidates),
// skipping one already covered by an explicit attachment above so a
// meeting that's both "gM"-attached and tag-matched isn't listed twice.
// This is what lets calendarView show an event's meeting details (link,
// description, location) only on demand (folded by default — see
// appendCalendarHeadlines) rather than inline: the link is still always
// one glance away, in the info buffer.
func (m *Model) calendarEventEntries(h *org.Headline) []calendarEventEntry {
	var entries []calendarEventEntry
	if url := h.Properties["GCAL_HTML_LINK"]; url != "" {
		e := calendarEventEntry{title: h.Title, url: url}
		e.when, e.hasWhen = parseRFC3339Property(h, "GCAL_START")
		entries = append(entries, e)
	}
	entries = append(entries, m.resolveMeetingEntries(h, "GCAL_EVENT_LINKS", "GCAL_EVENT_ID", "GCAL_EVENT_IDS")...)
	entries = append(entries, m.resolveMeetingEntries(h, "GCAL_RECURRING_EVENT_LINKS", "GCAL_RECURRING_EVENT_ID", "GCAL_RECURRING_EVENT_IDS")...)

	attached := make(map[meetingKey]bool)
	for _, id := range strings.Fields(h.Properties["GCAL_EVENT_IDS"]) {
		attached[meetingKey{oneOffMeeting, id}] = true
	}
	for _, id := range strings.Fields(h.Properties["GCAL_RECURRING_EVENT_IDS"]) {
		attached[meetingKey{recurringMeeting, id}] = true
	}
	for _, c := range m.tagLinkedMeetingCandidates(h, time.Now()) {
		if attached[meetingKey{c.kind, c.id}] || c.link == "" {
			continue
		}
		entries = append(entries, calendarEventEntry{title: c.title, url: c.link, when: c.when, hasWhen: true})
	}
	return entries
}

// calendarEventLinks resolves h's calendar-meeting properties into
// "<meeting name>: <url>" strings — calendarEventEntries (see above)
// with the time dropped, kept only for the existing tests that check
// link resolution without caring about meeting times.
func (m *Model) calendarEventLinks(h *org.Headline) []string {
	entries := m.calendarEventEntries(h)
	if len(entries) == 0 {
		return nil
	}
	links := make([]string, len(entries))
	for i, e := range entries {
		links[i] = e.title + ": " + e.url
	}
	return links
}

// resolveMeetingEntries resolves one (linksProp, idProp, idsProp) triple
// on h into calendarEventEntry values.
//
// linksProp (set by "gM" — one "[[url][title]]" per matched/attached
// meeting) is tried first: it was captured once, at the time h was
// linked to the meeting, so its name/link keep working indefinitely,
// even long after :sync-calendar's sync window has moved past the
// meeting (or the meeting stopped recurring entirely) and calendar.org
// no longer has it cached — its start time, looked up live by
// findEventTimeByLink, is the one part of the entry that can still come
// up empty in that case. Only if linksProp is missing entirely — e.g.
// an idsProp hand-attached to a task directly (per DESIGN.md's
// project↔meeting association) rather than via "gM" — does this fall
// back to a live lookup by idProp (the per-headline property
// identifying a single calendar.org event, GCAL_EVENT_ID or
// GCAL_RECURRING_EVENT_ID) against whatever calendar.org currently has
// cached, which (with no captured link to fall back on) can come up
// empty entirely once the event ages out; an ID that resolves neither
// way is silently skipped rather than shown broken.
func (m *Model) resolveMeetingEntries(h *org.Headline, linksProp, idProp, idsProp string) []calendarEventEntry {
	if raw := h.Properties[linksProp]; raw != "" {
		var entries []calendarEventEntry
		for _, l := range org.ParseLinks(raw) {
			title := l.Description
			if title == "" {
				title = l.URL
			}
			e := calendarEventEntry{title: title, url: l.URL}
			e.when, e.hasWhen = m.findEventTimeByLink(l.URL)
			entries = append(entries, e)
		}
		return entries
	}

	raw := h.Properties[idsProp]
	if raw == "" {
		return nil
	}
	var entries []calendarEventEntry
	for _, id := range strings.Fields(raw) {
		title, url, when, hasWhen, ok := m.findHeadlineByProperty(idProp, id)
		if !ok || url == "" {
			continue
		}
		entries = append(entries, calendarEventEntry{title: title, url: url, when: when, hasWhen: hasWhen})
	}
	return entries
}

// findHeadlineByProperty searches every loaded org file for a headline
// whose idProp property (GCAL_EVENT_ID or GCAL_RECURRING_EVENT_ID —
// i.e. one :sync-calendar wrote to calendar.org) equals id, returning
// its title, GCAL_HTML_LINK, and GCAL_START (when, hasWhen — false if
// missing/unparseable, same as parseRFC3339Property).
func (m *Model) findHeadlineByProperty(idProp, id string) (title, url string, when time.Time, hasWhen bool, ok bool) {
	for _, f := range m.ws.Files {
		org.Walk(f.Headlines, func(candidate *org.Headline) {
			if ok || candidate.Properties[idProp] != id {
				return
			}
			title, url, ok = candidate.Title, candidate.Properties["GCAL_HTML_LINK"], true
			when, hasWhen = parseRFC3339Property(candidate, "GCAL_START")
		})
		if ok {
			return title, url, when, hasWhen, true
		}
	}
	return "", "", time.Time{}, false, false
}

// findEventTimeByLink searches every loaded org file for a synced
// calendar event (GCAL_HTML_LINK) matching url, returning its
// GCAL_START. Used by resolveMeetingEntries to recover a meeting's
// start time for a "gM"-attached entry whose *_LINKS property only
// captured the title/link, not the time, at attach time — comes up
// empty once the event has aged out of calendar.org's synced window,
// same as any other live lookup by ID.
func (m *Model) findEventTimeByLink(url string) (when time.Time, ok bool) {
	for _, f := range m.ws.Files {
		org.Walk(f.Headlines, func(candidate *org.Headline) {
			if ok || candidate.Properties["GCAL_HTML_LINK"] != url {
				return
			}
			when, ok = parseRFC3339Property(candidate, "GCAL_START")
		})
		if ok {
			return when, true
		}
	}
	return time.Time{}, false
}

// formatCalendarEventEntry formats one calendarEventEntry for the info
// buffer's "Meeting:" section: "<title>  <time>  <url>", using the
// meeting picker's own time format (renderMeetingPicker, below) for
// consistency, or just "<title>  <url>" when hasWhen is false.
func formatCalendarEventEntry(e calendarEventEntry) string {
	if e.hasWhen {
		return e.title + "  " + e.when.Local().Format("2006-01-02 Mon 15:04") + "  " + e.url
	}
	return e.title + "  " + e.url
}

// calendarEventDisplayLines formats every one of h's calendarEventEntries
// (see above) for the info buffer's "Meeting:" section.
func (m *Model) calendarEventDisplayLines(h *org.Headline) []string {
	entries := m.calendarEventEntries(h)
	if len(entries) == 0 {
		return nil
	}
	lines := make([]string, len(entries))
	for i, e := range entries {
		lines[i] = formatCalendarEventEntry(e)
	}
	return lines
}
