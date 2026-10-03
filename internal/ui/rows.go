package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/sburnett/orgtd/internal/org"
	"github.com/sburnett/orgtd/internal/orgdate"
)

func (m *Model) rebuildRows() {
	// Every change to the files' contents is followed by a rebuild, so this
	// is where the meeting index (see meetingCache) goes stale.
	m.invalidateMeetingIndex()
	m.rows = m.rows[:0]
	m.spec().build(m, &m.rows, false)
	if m.cursor >= len(m.rows) {
		m.cursor = len(m.rows) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

// appendMeetingTagsRows appends meetingTagsFile's headlines into dst —
// see appendMeetingTagsHeadlines (meeting_tags.go) for the nesting this
// does beyond a plain appendHeadlines call. No file-header row, same as
// appendCalendarRows — there's only ever the one file behind this view,
// so a header would just be one more line of noise above every entry.
// ignoreFold is passed straight through — see appendHeadlines' own doc
// comment (used by searchRows to build the full-text search space
// regardless of fold state).
func (m *Model) appendMeetingTagsRows(dst *[]row, ignoreFold bool) {
	f := m.findMeetingTagsFile()
	if f == nil {
		return
	}
	m.appendMeetingTagsHeadlines(dst, f.Headlines, ignoreFold)
}

// toggleHideDone flips whether stale DONE/CANCELLED items (older than
// hideDoneAfterHours, per CLOSED) are hidden from the outline — a full
// on/off switch for the filtering, independent of the configured
// threshold, so a stale item is never more than a ":toggledone" away.
// Re-focuses the headline the cursor was on before the toggle, if it's
// still present among the rebuilt rows.
func (m *Model) toggleHideDone() {
	h := m.currentHeadline()
	m.hideDoneEnabled = !m.hideDoneEnabled
	m.rebuildRows()
	if h != nil {
		m.focusHeadline(h)
	}
	if m.hideDoneEnabled {
		m.message = fmt.Sprintf("Hiding DONE/CANCELLED items closed more than %dh ago", m.hideDoneAfterHours)
	} else {
		m.message = "Showing all DONE/CANCELLED items"
	}
}

// ignoreFold, when true, descends into every child/body regardless of
// collapsed[h] — used by searchRows (see below) to build the full
// outline text search scans, so a match inside a folded subtree isn't
// skipped the way it would be for ordinary rendering.
func (m *Model) appendHeadlines(dst *[]row, headlines []*org.Headline, ignoreFold bool) {
	for _, h := range headlines {
		if m.hiddenAsStaleDone(h) {
			continue
		}
		*dst = append(*dst, row{headline: h, level: h.Level})
		if ignoreFold || !m.collapsed[h] {
			m.appendBodyLines(dst, h)
			if len(h.Children) > 0 {
				m.appendHeadlines(dst, h.Children, ignoreFold)
			}
		}
	}
}

// appendCalendarHeadlines is appendHeadlines' calendarView counterpart:
// same recursion (body lines, children), but each row is marked
// rowCalendarEvent (see renderCalendarItemRowWithBg) instead of rendered
// the outline's usual way, and every event starts folded the first time
// it's ever shown — its Location/Description/link body is meeting
// detail you don't need at a glance, and stays one Tab away rather than
// cluttering every day's listing by default. "The first time" means
// exactly that: once a headline has an entry in m.collapsed at all
// (whether the user folded or unfolded it), that choice sticks across
// rebuilds instead of being reset back to folded on every redraw. Any
// item linked to the event — attached via "gM", or sharing a tag with
// it (see linkedMeetingItems) — is shown right after it regardless of
// fold state — unlike the body, it's not detail about the meeting
// itself but something that needs attention, so it isn't worth hiding
// behind an extra Tab. It's appended after the
// body/children (rather than unconditionally right after the event
// row), so unfolding an event reveals its own detail directly beneath
// it, not pushed down past whatever's attached.
func (m *Model) appendCalendarHeadlines(dst *[]row, headlines []*org.Headline, ignoreFold bool) {
	for _, h := range headlines {
		if m.hiddenAsStaleDone(h) {
			continue
		}
		if _, ok := m.collapsed[h]; !ok {
			m.collapsed[h] = true
		}
		*dst = append(*dst, row{headline: h, level: h.Level, kind: rowCalendarEvent})
		if ignoreFold || !m.collapsed[h] {
			m.appendBodyLines(dst, h)
			if len(h.Children) > 0 {
				m.appendCalendarHeadlines(dst, h.Children, ignoreFold)
			}
		}
		for _, item := range m.linkedMeetingItems(h) {
			*dst = append(*dst, row{headline: item, level: h.Level + 1, kind: rowCalendarLinked, linkedFromEvent: h})
		}
	}
}

// hiddenAsStaleDone reports whether h should be omitted from the outline
// (along with its whole subtree, and any body text) because hide-done
// filtering is enabled (see :toggledone) and h is a DONE/CANCELLED
// headline whose CLOSED timestamp is further than hideDoneAfterHours in
// the past. A DONE/CANCELLED headline with no CLOSED timestamp (e.g.
// hand-edited) or an unparseable one is never hidden — there's no age to
// judge it by.
func (m *Model) hiddenAsStaleDone(h *org.Headline) bool {
	if !m.hideDoneEnabled || !org.IsDoneKeyword(h.Keyword) || h.Closed == nil {
		return false
	}
	closed, _, err := orgdate.ParseFlexible(h.Closed.Raw)
	if err != nil {
		return false
	}
	return time.Since(closed) > time.Duration(m.hideDoneAfterHours)*time.Hour
}

// appendBodyLines appends one row per line of h's free-text body,
// indented one level deeper than h's own row (matching where a child
// would sit) — shown right under h's title, before its children, the
// same order the raw org file itself keeps them in. Subject to the same
// collapsed[h] flag as h's children (see appendHeadlines): one fold
// toggle shows or hides both together.
func (m *Model) appendBodyLines(dst *[]row, h *org.Headline) {
	for _, line := range visibleBodyLines(h) {
		*dst = append(*dst, row{headline: h, level: h.Level + 1, kind: rowBody, text: line})
	}
}

// hasFoldableContent reports whether h has anything a fold command
// could show or hide: children, a body, or both.
func hasFoldableContent(h *org.Headline) bool {
	return len(h.Children) > 0 || len(visibleBodyLines(h)) > 0
}

// visibleBodyLines returns h.Body with any trailing blank lines
// stripped. Org files conventionally have a blank line separating a
// headline from the next one, which the parser has no way to
// distinguish from deliberate trailing whitespace in the body — without
// this, that separator would show up as a meaningless empty line under
// nearly every single entry. Deliberate blank lines *within* a
// multi-paragraph body (not at the very end) are left alone.
func visibleBodyLines(h *org.Headline) []string {
	lines := h.Body
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return lines[:end]
}
