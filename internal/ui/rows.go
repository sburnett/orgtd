package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
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
		m.message = fmt.Sprintf("Hiding DONE/CANCELLED items closed more than %dh ago", m.cfg.HideDoneAfterHours)
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
	if m.view == referenceView {
		return false // reference material has no task state to age out
	}
	if !m.hideDoneEnabled || !org.IsDoneKeyword(h.Keyword) || h.Closed == nil {
		return false
	}
	closed, _, err := orgdate.ParseFlexible(h.Closed.Raw)
	if err != nil {
		return false
	}
	return time.Since(closed) > time.Duration(m.cfg.HideDoneAfterHours)*time.Hour
}

// appendBodyLines appends one row per line of h's free-text body,
// indented one level deeper than h's own row (matching where a child
// would sit) — shown right under h's title, before its children, the
// same order the raw org file itself keeps them in. Subject to the same
// collapsed[h] flag as h's children (see appendHeadlines): one fold
// toggle shows or hides both together.
func (m *Model) appendBodyLines(dst *[]row, h *org.Headline) {
	// A body line is drawn after a fixed 5-column gutter, one indent
	// step per level (2 columns), and the fold column plus its space.
	avail := m.width - (5 + 2*(h.Level+1) + 2)
	for _, line := range h.TrimmedBody() {
		for _, seg := range wrapBodyLine(line, avail) {
			*dst = append(*dst, row{headline: h, level: h.Level + 1, kind: rowBody, text: seg})
		}
	}
}

// wrapBodyLine splits line into segments no wider than width columns as
// displayed (an org link counts as just its display text, see
// renderTitleForDisplay, and is never split), breaking at spaces and
// hard-breaking a plain word that is itself too wide. A non-positive
// width, or a line that already fits, comes back as is. The line's
// leading/trailing whitespace is dropped, as rendering trims it anyway.
func wrapBodyLine(line string, width int) []string {
	line = strings.TrimSpace(line)
	if width <= 0 || displayWidth(line) <= width {
		return []string{line}
	}

	// Tokens are words and whole links, each with its displayed width.
	type token struct {
		raw   string
		w     int
		space bool // preceded by whitespace
		link  bool
	}
	var toks []token
	pending := false // whitespace seen since the last token
	addPlain := func(text string) {
		for _, r := range text {
			if r == ' ' || r == '\t' {
				pending = true
				continue
			}
			if n := len(toks); n > 0 && !pending && !toks[n-1].link {
				// Continues the previous plain word.
				toks[n-1].raw += string(r)
				toks[n-1].w += lipgloss.Width(string(r))
				continue
			}
			toks = append(toks, token{raw: string(r), w: lipgloss.Width(string(r)), space: pending})
			pending = false
		}
	}
	last := 0
	for _, span := range org.LinkIndexes(line) {
		addPlain(line[last:span[0]])
		raw := line[span[0]:span[1]]
		toks = append(toks, token{raw: raw, w: displayWidth(raw), space: pending, link: true})
		pending = false
		last = span[1]
	}
	addPlain(line[last:])

	var out []string
	cur, curW := "", 0
	flush := func() {
		if cur != "" {
			out = append(out, cur)
		}
		cur, curW = "", 0
	}
	for _, t := range toks {
		sep := 0
		if cur != "" && t.space {
			sep = 1
		}
		if cur != "" && curW+sep+t.w > width {
			flush()
			sep = 0
		}
		if t.w > width && !t.link {
			// A single word wider than the row: break it by characters.
			for _, r := range t.raw {
				rw := lipgloss.Width(string(r))
				if curW+rw > width {
					flush()
				}
				cur += string(r)
				curW += rw
			}
			continue
		}
		if sep == 1 {
			cur += " "
			curW++
		}
		cur += t.raw
		curW += t.w
	}
	flush()
	if len(out) == 0 {
		return []string{line}
	}
	return out
}

// displayWidth is the width of s as drawn by renderTitleForDisplay: each
// org link shown as its description (or its URL), the rest verbatim.
func displayWidth(s string) int {
	w := 0
	last := 0
	for _, span := range org.LinkIndexes(s) {
		w += lipgloss.Width(s[last:span[0]])
		disp := s[span[2]:span[3]]
		if span[4] >= 0 && span[5] > span[4] {
			disp = s[span[4]:span[5]]
		}
		w += lipgloss.Width(disp)
		last = span[1]
	}
	return w + lipgloss.Width(s[last:])
}

// hasFoldableContent reports whether h has anything a fold command
// could show or hide: a body, or at least one child that isn't hidden
// (see hiddenAsStaleDone) — a headline whose only children are stale
// DONE ones has nothing to expand.
func (m *Model) hasFoldableContent(h *org.Headline) bool {
	if len(h.TrimmedBody()) > 0 {
		return true
	}
	for _, c := range h.Children {
		if !m.hiddenAsStaleDone(c) {
			return true
		}
	}
	return false
}

// visibleDescendantCount is how many descendant headlines of h are
// currently shown or would be on unfolding it: every child and
// grandchild, minus any stale DONE one (see hiddenAsStaleDone) and its
// whole subtree.
func (m *Model) visibleDescendantCount(h *org.Headline) int {
	n := 0
	for _, c := range h.Children {
		if m.hiddenAsStaleDone(c) {
			continue
		}
		n += 1 + m.visibleDescendantCount(c)
	}
	return n
}

// hasAnyContent is hasFoldableContent without the hidden-children
// filter: whether h has any children or body at all, shown or not.
func hasAnyContent(h *org.Headline) bool {
	return len(h.Children) > 0 || len(h.TrimmedBody()) > 0
}
