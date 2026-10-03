package ui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/sburnett/orgtd/internal/org"
	"github.com/sburnett/orgtd/internal/orgdate"
)

// row is one visible line in the outline: either a file header or a
// headline at some depth.
type row struct {
	file     *org.File // set for a file-header row (outline view)
	headline *org.Headline
	level    int // structural level used by level-aware navigation (rowLevel); file/section rows are 0

	// isBodyLine marks a row showing one line of headline's free-text
	// body (shown under its title when expanded — see appendBodyLines);
	// bodyText is that line's text (which may itself be empty — a blank
	// line in the body — so isBodyLine, not bodyText != "", is the
	// reliable marker). headline is still set to the owning headline on
	// such a row (not nil), so commands like i/dd/r/gd resolve to it
	// exactly as if the cursor were on the title row itself.
	isBodyLine bool
	bodyText   string

	section        string    // set for an agenda section-header row ("Overdue" etc.); outline rows never set this
	isAgendaItem   bool      // true for every agenda item row (Next Actions entries have no date/label, so this — not agendaLabel — is the reliable marker)
	agendaLabel    string    // "Scheduled" or "Deadline", set for a date-based agenda item row; empty for a Next Actions entry
	agendaDate     time.Time // the date this agenda item row is shown for, if agendaLabel is set
	agendaRepeater string    // e.g. "+1w", if agendaDate was computed from a recurring timestamp; empty otherwise
	agendaMissed   int       // occurrences skipped since agendaDate, shown as "(Nx)"; only ever set on an Overdue row

	// isMeetingHeader marks a meeting-group header row in the agenda's
	// "Meetings" section (see appendMeetingsSection): a label ("<title>
	// — <when>") to group the items below it under, one level deeper
	// than the section header and one level shallower than its items
	// (see rowLevel) — not itself a headline (headline is left nil, like
	// a plain section row), so none of the outline's per-headline
	// commands apply to it.
	isMeetingHeader bool
	meetingTitle    string
	meetingStart    time.Time
	meetingEnd      time.Time

	// meetingItemTitle/meetingItemStart are set alongside isAgendaItem on
	// a Meetings-section item row (see appendMeetingsSection), echoing
	// the meetingTitle/meetingStart of the isMeetingHeader row it's
	// nested under. An item linked to more than one meeting legitimately
	// gets one row per meeting (see appendMeetingsSection) — all sharing
	// the same headline — so sameRow (below) needs these to tell those
	// rows apart; without them, search's "n"/"N" (see findMatch) could
	// never advance past the first such row, since every later one would
	// look identical to it.
	meetingItemTitle string
	meetingItemStart time.Time

	// isCalendarItem marks a calendar-event row in calendarView (see
	// appendCalendarHeadlines): rendered with its GCAL_START/GCAL_END
	// time shown before the title (see renderCalendarItemRowWithBg),
	// rather than the outline's usual keyword-first layout.
	isCalendarItem bool

	// isMeetingTagsRecord marks a meeting-tags.org record's own row in
	// meetingTagsView (see appendMeetingTagsHeadlines/isMeetingTagsRecord
	// in meeting_tags.go): rendered without the indent/fold columns every
	// other headline row reserves (see renderMeetingTagsRecordRowWithBg)
	// — a record is always effectively top-level and never has foldable
	// content in practice, so those columns would just be dead space.
	isMeetingTagsRecordRow bool

	// isCalendarLinkedItem marks a row for an entry elsewhere in the
	// workspace linked to a calendar event — attached via "gM", or
	// sharing a tag with it (see linkedMeetingItems) — shown right after
	// the event itself in calendarView regardless of whether the event is
	// folded (see appendCalendarHeadlines), indented to level (one deeper
	// than the event) rather than the headline's own real level in its
	// own file (see renderCalendarLinkedItemRowWithBg).
	isCalendarLinkedItem bool

	// linkedFromEvent is the calendar event headline a
	// isCalendarLinkedItem row is nested under (see
	// appendCalendarHeadlines). An entry linked to more than one event
	// gets one row per event, all sharing the same linked headline — so
	// sameRow (below) needs this to tell those rows apart, the same
	// reason meetingItemTitle/meetingItemStart exist above.
	linkedFromEvent *org.Headline

	// isTagsItem marks a row in tagsView (see appendTagsRows): a flat,
	// single-line row for an entry carrying tagsItemTag, shown under that
	// tag's section header — same rendering shape as isCalendarLinkedItem
	// (no fold/body/children of its own, a "[file › parent]" place tag
	// instead), since an entry with several tags legitimately gets one row
	// per tag, all sharing the same headline pointer.
	isTagsItem  bool
	tagsItemTag string

	// isTextLine marks a plain read-only informational row (:config/:log/
	// :diff/:help), rendered flush left and never interactive; text is
	// that line's own text (which may itself be empty — a blank line, as
	// :help's embedded README naturally has plenty of — so isTextLine,
	// not text != "", is the reliable marker; same reasoning as
	// isBodyLine/bodyText above).
	isTextLine bool
	text       string
}

func (m *Model) rebuildRows() {
	m.rows = m.rows[:0]
	switch m.view {
	case agendaView:
		m.appendAgendaRows()
	case configView:
		m.appendConfigRows()
	case logView:
		m.appendLogRows()
	case diffView:
		m.appendDiffRows()
	case helpView:
		m.appendHelpRows()
	case calendarView:
		m.appendCalendarRows(&m.rows, false)
	case meetingTagsView:
		m.appendMeetingTagsRows(&m.rows, false)
	case tagsView:
		m.appendTagsRows()
	default:
		for _, f := range m.ws.Files {
			if filepath.Base(f.Path) == m.calendarFile || filepath.Base(f.Path) == m.meetingTagsFile {
				continue
			}
			m.rows = append(m.rows, row{file: f})
			m.appendHeadlines(&m.rows, f.Headlines, false)
		}
	}
	if m.cursor >= len(m.rows) {
		m.cursor = len(m.rows) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

// usesOutlineRows reports whether v's rows are the same full-outline
// listing rebuildRows falls back to in its default case (every loaded
// file, in full) — true for outlineView itself and clarifyView (which
// merely adds an info-buffer panel on top of the same rows), false for
// every other view (agenda, calendar, meeting tags, config, log, diff,
// help), whose row sets are each built from something narrower than
// "every headline". Used by finishEdit to decide whether a freshly
// captured entry (see insertContext.switchToOutline) is already visible
// where the cursor is, or needs an explicit switch to outline view to
// bring it into focus.
func usesOutlineRows(v viewKind) bool {
	switch v {
	case outlineView, clarifyView:
		return true
	default:
		return false
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
// isCalendarItem (see renderCalendarItemRowWithBg) instead of rendered
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
		*dst = append(*dst, row{headline: h, level: h.Level, isCalendarItem: true})
		if ignoreFold || !m.collapsed[h] {
			m.appendBodyLines(dst, h)
			if len(h.Children) > 0 {
				m.appendCalendarHeadlines(dst, h.Children, ignoreFold)
			}
		}
		for _, item := range m.linkedMeetingItems(h) {
			*dst = append(*dst, row{headline: item, level: h.Level + 1, isCalendarLinkedItem: true, linkedFromEvent: h})
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
		*dst = append(*dst, row{headline: h, level: h.Level + 1, isBodyLine: true, bodyText: line})
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
