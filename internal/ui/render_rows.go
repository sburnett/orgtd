package ui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/sburnett/orgtd/internal/meetings"
	"github.com/sburnett/orgtd/internal/org"
)

// renderTitleForDisplay renders title for the row list: each org-mode
// link is replaced with just its display text (the description, or the
// url if there's no description) and underlined, instead of showing the
// raw "[[url][description]]" syntax. base is the style otherwise applied
// to the title (e.g. doneTitleStyle for a DONE/CANCELLED item); every
// segment — link or plain text — is rendered with base (underlined,
// for a link) so the two compose without nesting escape codes. A method
// (rather than a free function) purely so it can pass m's own
// searchHighlightBg through to highlightMatches.
func (m Model) renderTitleForDisplay(title string, base lipgloss.Style, query string) string {
	matches := org.LinkIndexes(title)
	if len(matches) == 0 {
		return m.highlightMatches(title, query, base)
	}
	linkStyle := base.Underline(true)

	var b strings.Builder
	last := 0
	for _, span := range matches {
		start, end := span[0], span[1]
		url := title[span[2]:span[3]]
		desc := ""
		if span[4] >= 0 {
			desc = title[span[4]:span[5]]
		}
		display := desc
		if display == "" {
			display = url
		}
		if start > last {
			b.WriteString(m.highlightMatches(title[last:start], query, base))
		}
		b.WriteString(m.highlightMatches(display, query, linkStyle))
		last = end
	}
	if last < len(title) {
		b.WriteString(m.highlightMatches(title[last:], query, base))
	}
	return b.String()
}

// gutter renders the leftmost column of a row: a single-character dirty
// marker, always present (blank when clean) so every row lines up the
// same way vim's line-number column does, regardless of indentation. Its
// character and color come from m.cfg.Icons.DirtyIcon/dirtyColor (default "+",
// "9"; see WithDirtyIcon and the config file's [icons] section). bg is
// the background it's rendered with — lipgloss.NoColor{} normally, or
// the cursor row's highlight (see renderRowWithBg).
func (m Model) gutter(dirty bool, bg lipgloss.TerminalColor) string {
	if dirty {
		icon := orDefault(m.cfg.Icons.DirtyIcon, defaultDirtyIcon)
		color := orDefault(m.cfg.Icons.DirtyColor, defaultDirtyColor)
		return lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Background(bg).Render(icon)
	}
	return bgSpan(bg, " ")
}

// markColumn is a headline row's mark/clarify gutter column, in outline
// or agenda view alike — a column of its own, separate from gutter's
// dirty marker, so a row that's both marked (or the clarify target) and
// dirty shows both indicators at once instead of one hiding the other:
// the clarify target's marker (clarify view only, m.cfg.Icons.ClarifyIcon/
// clarifyColor — default "●", "212") takes priority over a mark's
// letter (m.cfg.Icons.MarkColor — default "212"; there's no configurable icon for
// a mark, since its glyph is always the letter it was set with), since a
// row can't be both; blank if neither applies. bg is the background it's
// rendered with (see gutter).
func (m Model) markColumn(h *org.Headline, bg lipgloss.TerminalColor) string {
	if m.view == clarifyView && h == m.clarifyTarget {
		icon := orDefault(m.cfg.Icons.ClarifyIcon, defaultClarifyIcon)
		color := orDefault(m.cfg.Icons.ClarifyColor, defaultClarifyColor)
		return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(color)).Background(bg).Render(icon)
	}
	if letter, ok := m.markLetterFor(h); ok {
		color := orDefault(m.cfg.Icons.MarkColor, defaultMarkColor)
		return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(color)).Background(bg).Render(string(letter))
	}
	return bgSpan(bg, " ")
}

// lockColumn is a headline row's :format-links gutter column — a column
// of its own (see gutter, markColumn), so it shows up alongside the
// dirty marker and any mark/clarify pin rather than hiding them. Its
// character and color come from m.cfg.Icons.LockIcon/lockColor (default "◆", the
// U+25C6 BLACK DIAMOND, "208"; see WithLockIcon and the config file's
// [icons] section) while h is locked (see m.immutable), blank otherwise.
// bg is the background it's rendered with (see gutter).
func (m Model) lockColumn(h *org.Headline, bg lipgloss.TerminalColor) string {
	if m.immutable[h] {
		icon := orDefault(m.cfg.Icons.LockIcon, defaultLockIcon)
		color := orDefault(m.cfg.Icons.LockColor, defaultLockColor)
		return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(color)).Background(bg).Render(icon)
	}
	return bgSpan(bg, " ")
}

// meetingColumn is a headline row's "linked to a meeting" gutter
// column — a column of its own (see gutter, markColumn, lockColumn),
// so it shows up alongside any of those rather than hiding them. Its
// character and color come from m.cfg.Icons.MeetingIcon/meetingColor (default
// "▣", the U+25A3 WHITE SQUARE CONTAINING BLACK SMALL SQUARE, "39"; see
// WithMeetingIcon and the config file's [icons] section) while h is
// linked to a recurring series or a one-off event, either explicitly —
// attached via "gM" (GCAL_RECURRING_EVENT_IDS or GCAL_EVENT_IDS — see
// meetings.Meeting.Kind) — or automatically, by sharing a tag with one
// (see meetings.Index.TagLinked, e.g. a confirmed attendee's
// "@username" tag) — blank otherwise. This is what lets "is this entry
// linked to some meeting" be answered by looking at the row, rather
// than opening it in $EDITOR to check its property drawer (and, for a
// tag-based link, there's no property to check there anyway — it's
// computed live from the tags, not stored). bg is the background it's
// rendered with (see gutter).
func (m Model) meetingColumn(h *org.Headline, bg lipgloss.TerminalColor) string {
	linked := h.Properties["GCAL_RECURRING_EVENT_IDS"] != "" || h.Properties["GCAL_EVENT_IDS"] != ""
	if !linked && len(h.Tags) > 0 {
		linked = len(m.meetingIndex().TagLinked(h, m.now())) > 0
	}
	if linked {
		icon := orDefault(m.cfg.Icons.MeetingIcon, defaultMeetingIcon)
		color := orDefault(m.cfg.Icons.MeetingColor, defaultMeetingColor)
		return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(color)).Background(bg).Render(icon)
	}
	return bgSpan(bg, " ")
}

// renderRow renders r with no highlight — the ordinary case, used for
// every row except the one under the cursor. See renderRowWithBg.
func (m Model) renderRow(r row) string {
	return m.renderRowWithBg(r, lipgloss.NoColor{})
}

// renderRowWithBg renders r with bg as the background behind every
// segment — not just wrapped around the finished string, which doesn't
// work: each segment (keyword, tags, timestamp, ...) already carries its
// own style with its own reset code, and that reset would cancel an
// outer background the moment it fires, leaving the highlight covering
// only the first styled segment instead of the whole line. This is the
// same technique renderPinnedRow uses for the overlay background.
func (m Model) renderRowWithBg(r row, bg lipgloss.TerminalColor) string {
	query := m.activeSearchQuery()
	switch r.kind {
	case rowText:
		// Flush left, unstyled beyond the cursor's own background — a
		// :config row is plain informational text, not a headline. A
		// :help row, though, is glamour-rendered markdown and already
		// carries its own ANSI styling with inner reset codes, which
		// would cut off an outer background partway through the line
		// (the same problem bgSpan exists to solve elsewhere) — so
		// strip it first whenever there's an actual highlight to apply.
		text := r.text
		if _, plain := bg.(lipgloss.NoColor); !plain {
			text = ansi.Strip(text)
		}
		return m.highlightMatches(text, query, lipgloss.NewStyle().Background(bg))
	case rowSection:
		// Flush left (no gutter/indent), unlike every item row below it,
		// so a section header stands out at a glance in a long agenda.
		return m.highlightMatches(r.text, query, m.fileStyle().Background(bg))
	case rowMeetingHeader:
		return m.renderMeetingHeaderRowWithBg(r, bg)
	case rowFile:
		// Blank mark, lock, and meeting columns: files themselves are
		// never marked, locked by :format-links, or attached to a
		// meeting, but this keeps every row's dirty marker lined up in
		// the same column.
		name := m.highlightMatches(filepath.Base(r.file.Path), query, m.fileStyle().Background(bg))
		return bgSpan(bg, " ") + bgSpan(bg, " ") + bgSpan(bg, " ") + m.gutter(m.dirty[r.file], bg) + bgSpan(bg, " ") + name
	case rowAgendaItem:
		return m.renderAgendaItemRowWithBg(r, bg)
	case rowCalendarEvent:
		return m.renderCalendarItemRowWithBg(r, bg)
	case rowCalendarLinked:
		return m.renderCalendarLinkedItemRowWithBg(r, bg)
	case rowTagsItem:
		return m.renderTagsItemRowWithBg(r, bg)
	case rowMeetingTagsRecord:
		return m.renderMeetingTagsRecordRowWithBg(r, bg)
	case rowBody:
		return m.renderBodyLineWithBg(r, bg)
	}

	// rowHeadline: an ordinary outline entry.

	h := r.headline
	indent := m.indentGuides(h.Parent, h.Level, m.guideScope(), bg)

	// A leaf gets a small dot in the arrow's column, so every row has
	// something sitting where its own guide would hang from.
	fold := bgSpan(bg, "·") // U+00B7 MIDDLE DOT
	if hasFoldableContent(h) {
		glyph := "▼" // U+25BC BLACK DOWN-POINTING TRIANGLE (full-size; ▾ is a dedicated "small" variant)
		if m.collapsed[h] {
			glyph = "▶" // U+25B6 BLACK RIGHT-POINTING TRIANGLE (full-size; ▸ is a dedicated "small" variant)
		}
		fold = bgSpan(bg, glyph)
	}

	prefix := m.markColumn(h, bg) + m.lockColumn(h, bg) + m.meetingColumn(h, bg) + m.gutter(m.dirtyHeadlines[h], bg) + bgSpan(bg, " ") + indent + fold + bgSpan(bg, " ") + joinBg(m.renderKeywordAndTitle(h, bg), bg)

	suffix := m.renderTagsSuffix(h, h.Tags, query, bg)

	if ts := planningSummary(h); ts != "" {
		suffix += bgSpan(bg, "  ") + m.fadeIfImmutable(m.timestampStyle(), h).Background(bg).Render(ts)
	}

	return fitRowLine(prefix, suffix, m.width, bg)
}

// renderKeywordAndTitle renders h's keyword, priority, and title (with
// its links shown as display text, see renderTitleForDisplay) as
// space-joinable parts — shared between the outline, agenda, and pinned
// row renderers. bg is the background every part is rendered with —
// lipgloss.NoColor{} outside a pinned row, where nothing is tinted.
func (m Model) renderKeywordAndTitle(h *org.Headline, bg lipgloss.TerminalColor) []string {
	query := m.activeSearchQuery()
	var parts []string
	if h.Keyword != "" {
		style, ok := m.keywordStyle(h.Keyword)
		if !ok {
			style = lipgloss.NewStyle()
		}
		style = m.fadeIfImmutable(style, h)
		parts = append(parts, m.highlightMatches(h.Keyword, query, style.Background(bg)))
	}
	if h.Priority != "" {
		style := m.fadeIfImmutable(lipgloss.NewStyle(), h)
		parts = append(parts, m.highlightMatches(fmt.Sprintf("[#%s]", h.Priority), query, style.Background(bg)))
	}

	base := lipgloss.NewStyle()
	if org.IsDoneKeyword(h.Keyword) {
		base = m.doneTitleStyle()
	}
	base = m.fadeIfImmutable(base, h).Background(bg)
	parts = append(parts, m.renderTitleForDisplay(h.Title, base, query))
	return parts
}

// renderAgendaItemRow renders one agenda item row: keyword/priority/
// title (as in outline, but with no indent or fold arrow — agenda is
// flat), tags, then which file (and, for a sub-headline, its immediate
// parent — see agendaPlace) it's from and the date/label (Scheduled or
// Deadline) it's shown for.
//
// The parent-title portion of that "[...]" tag is left full-width
// unless the row doesn't already fit. When it doesn't, the row's own
// title first gets its full natural width reserved (so a short title
// is never truncated just because the parent tag is long), and
// whatever's left over goes to the parent tag — maximizing how much of
// each shows given the other. Only once the parent tag has been shrunk
// to nothing and the row still doesn't fit does the row's own title
// give way too (see fitRowLine), exactly like any other row.
func (m Model) renderAgendaItemRowWithBg(r row, bg lipgloss.TerminalColor) string {
	h := r.headline
	prefix := m.markColumn(h, bg) + m.lockColumn(h, bg) + m.meetingColumn(h, bg) + m.gutter(m.dirtyHeadlines[h], bg) + bgSpan(bg, " ") + joinBg(m.renderKeywordAndTitle(h, bg), bg)

	tags := m.renderTagsSuffix(h, h.Tags, m.activeSearchQuery(), bg)

	timestamp := m.fadeIfImmutable(m.timestampStyle(), h).Background(bg)
	dateSuffix := func(place string) string {
		if r.agendaLabel == "" {
			// A Next Actions entry: no date to show, just where it's from.
			return bgSpan(bg, "  ") + timestamp.Render(fmt.Sprintf("[%s]", place))
		}
		label := r.agendaLabel
		if r.agendaMissed > 0 {
			label = fmt.Sprintf("%s (%dx)", label, r.agendaMissed)
		}
		date := r.agendaDate.Format("2006-01-02 Mon")
		if r.agendaRepeater != "" {
			date += " " + r.agendaRepeater
		}
		return bgSpan(bg, "  ") + timestamp.Render(fmt.Sprintf("[%s]  %s: %s", place, label, date))
	}

	suffix := tags + dateSuffix(m.agendaPlace(h, -1))
	if m.width > 0 && lipgloss.Width(prefix)+lipgloss.Width(suffix) > m.width {
		overhead := lipgloss.Width(tags) + lipgloss.Width(dateSuffix(m.agendaPlace(h, 0)))
		parentBudget := m.width - lipgloss.Width(prefix) - overhead
		if parentBudget < 0 {
			parentBudget = 0
		}
		suffix = tags + dateSuffix(m.agendaPlace(h, parentBudget))
	}

	return fitRowLine(prefix, suffix, m.width, bg)
}

// agendaPlace returns the "[...]" tag content shown on an agenda row for
// h: the file it's from, plus — since an agenda item shown out of its
// outline context often isn't identifiable from its own title alone —
// its immediate parent headline's title, if it has one (a top-level
// headline has no parent to add). Only the direct parent is shown, not
// the full ancestor chain up to the root: that's usually enough to place
// the item, and stays short even for an item buried deep in the tree —
// the full chain remains one Enter (jumpToSource) away.
//
// parentMaxWidth caps how much of the parent title is included
// (ellipsized with ansi.Truncate past that); a negative value leaves it
// full-width. See renderAgendaItemRowWithBg, the only caller, for how
// that cap is chosen.
func (m Model) agendaPlace(h *org.Headline, parentMaxWidth int) string {
	fileName := ""
	if f := m.ws.FileOf(h); f != nil {
		fileName = filepath.Base(f.Path)
	}
	if h.Parent == nil {
		return fileName
	}
	parentTitle := h.Parent.Title
	if parentMaxWidth >= 0 {
		parentTitle = ansi.Truncate(parentTitle, parentMaxWidth, "…")
	}
	return fmt.Sprintf("%s › %s", fileName, parentTitle)
}

// renderMeetingHeaderRowWithBg renders a meeting-group header row in the
// agenda's "Meetings" section: the meeting's title, bold like a section
// header (see fileStyle) so it still reads as a header despite being
// indented one level under the section row, followed by its date/time.
func (m Model) renderMeetingHeaderRowWithBg(r row, bg lipgloss.TerminalColor) string {
	query := m.activeSearchQuery()
	title := m.highlightMatches(r.meetingTitle, query, m.fileStyle().Background(bg))
	when := m.highlightMatches(formatMeetingWhen(r.meetingStart, r.meetingEnd), query, m.timestampStyle().Background(bg))
	return fitRowLine(bgSpan(bg, "  ")+title, bgSpan(bg, "  ")+when, m.width, bg)
}

// formatMeetingWhen renders a meeting's start/end for
// renderMeetingHeaderRowWithBg, in local time — omitting the time of day
// entirely for an all-day event, recognized here by both endpoints
// sitting at local midnight (see eventBounds in internal/calendarsync/convert.go,
// which anchors an all-day event's GCAL_START/GCAL_END there).
func formatMeetingWhen(start, end time.Time) string {
	start, end = start.Local(), end.Local()
	if isMidnight(start) && isMidnight(end) && !start.Equal(end) {
		return start.Format("2006-01-02 Mon")
	}
	if sameLocalDay(start, end) {
		return fmt.Sprintf("%s %s-%s", start.Format("2006-01-02 Mon"), start.Format("15:04"), end.Format("15:04"))
	}
	return fmt.Sprintf("%s %s — %s %s", start.Format("2006-01-02 Mon"), start.Format("15:04"), end.Format("2006-01-02 Mon"), end.Format("15:04"))
}

func isMidnight(t time.Time) bool {
	return t.Hour() == 0 && t.Minute() == 0
}

// renderCalendarItemRowWithBg renders one calendar event row — in
// calendarView, one of the file's own top-level events; in
// meetingTagsView, one nested under the meeting-tags.org record it
// matches (see appendMeetingTagsHeadlines) — with mark/lock/gutter/fold
// columns exactly as the outline's own default headline-row case (see
// the bottom of renderRowWithBg), but its GCAL_START/GCAL_END time (see
// calendarItemTime) shown before the title in place of a TODO keyword —
// the time is what's worth seeing at a glance here, and a calendar event
// never has a keyword anyway. Its own visual indent is capped at one
// level, regardless of r.level: r.level is genuinely record.Level+1 in
// meetingTagsView (see appendMeetingTagsHeadlines), since that's what
// moveDeeper/moveShallower ("l"/"h") use to treat the event as the
// record's child, but rendering the full, uncapped width would push it
// needlessly far right — the row's own gutter columns and its
// GCAL_START/GCAL_END time before the title already mark it as nested
// detail, not another top-level entry, so a single indent step is enough
// to read as "under" whatever's above it (same idea as
// renderCalendarLinkedItemRowWithBg's r.level-based indent, just capped
// rather than passed through raw).
func (m Model) renderCalendarItemRowWithBg(r row, bg lipgloss.TerminalColor) string {
	h := r.headline
	query := m.activeSearchQuery()
	indent := bgSpan(bg, strings.Repeat("  ", min(r.level, 1)))

	fold := bgSpan(bg, " ")
	if hasFoldableContent(h) {
		glyph := "▼"
		if m.collapsed[h] {
			glyph = "▶"
		}
		fold = bgSpan(bg, glyph)
	}

	prefix := m.markColumn(h, bg) + m.lockColumn(h, bg) + m.meetingColumn(h, bg) + m.gutter(m.dirtyHeadlines[h], bg) + bgSpan(bg, " ") + indent + fold + bgSpan(bg, " ")
	if when := calendarItemTime(h); when != "" {
		prefix += m.fadeIfImmutable(m.timestampStyle(), h).Background(bg).Render(when) + bgSpan(bg, "  ")
	}
	prefix += joinBg(m.renderKeywordAndTitle(h, bg), bg)

	suffix := m.renderTagsSuffix(h, m.calendarDisplayTags(h), query, bg)
	return fitRowLine(prefix, suffix, m.width, bg)
}

// renderMeetingTagsRecordRowWithBg renders a meeting-tags.org record's
// own row in meetingTagsView (see rowMeetingTagsRecord) — mark/lock/
// meeting/dirty gutter and title/tags exactly as the outline's own
// default headline-row case (see the bottom of renderRowWithBg), but
// without that case's indent or fold columns: a record is always
// effectively top-level here regardless of where it happens to sit in
// meeting-tags.org, and in practice never has foldable body/children
// (see applyMeetingTag, which creates one with neither), so reserving
// width for either would just be dead space in front of every record's
// title.
func (m Model) renderMeetingTagsRecordRowWithBg(r row, bg lipgloss.TerminalColor) string {
	h := r.headline
	query := m.activeSearchQuery()

	prefix := m.markColumn(h, bg) + m.lockColumn(h, bg) + m.meetingColumn(h, bg) + m.gutter(m.dirtyHeadlines[h], bg) + bgSpan(bg, " ") + joinBg(m.renderKeywordAndTitle(h, bg), bg)

	suffix := m.renderTagsSuffix(h, h.Tags, query, bg)
	if ts := planningSummary(h); ts != "" {
		suffix += bgSpan(bg, "  ") + m.fadeIfImmutable(m.timestampStyle(), h).Background(bg).Render(ts)
	}
	return fitRowLine(prefix, suffix, m.width, bg)
}

// renderCalendarLinkedItemRowWithBg renders one item linked to a
// calendar event — attached via "gM", or sharing a tag with it (see
// linkedMeetingItems/appendCalendarHeadlines):
// mark/lock/meeting/dirty gutter and keyword/title exactly as an
// ordinary headline row, but indented to r.level (one level deeper than
// the event's own indent — see renderCalendarItemRowWithBg) rather than
// the headline's own real level in whatever file it actually lives in,
// so it visibly nests under the event regardless of how deep it sits in
// its own outline. A blank fold column, like a body line's (see
// renderBodyLineWithBg) — this row doesn't support folding its own
// children/body. Tagged with "[file › parent]" (see agendaPlace), same
// as an agenda item, since the entry lives elsewhere in the workspace
// and wouldn't otherwise be placeable from its title alone — including
// the same budget-driven truncation of the parent title (see
// renderAgendaItemRowWithBg) when the row doesn't fit at full width.
func (m Model) renderCalendarLinkedItemRowWithBg(r row, bg lipgloss.TerminalColor) string {
	h := r.headline
	query := m.activeSearchQuery()
	indent := bgSpan(bg, strings.Repeat("  ", r.level))

	prefix := m.markColumn(h, bg) + m.lockColumn(h, bg) + m.meetingColumn(h, bg) + m.gutter(m.dirtyHeadlines[h], bg) + bgSpan(bg, " ") + indent + bgSpan(bg, " ") + bgSpan(bg, " ") + joinBg(m.renderKeywordAndTitle(h, bg), bg)

	tags := m.renderTagsSuffix(h, h.Tags, query, bg)
	place := func(parentMaxWidth int) string {
		return bgSpan(bg, "  ") + m.fadeIfImmutable(m.timestampStyle(), h).Background(bg).Render(fmt.Sprintf("[%s]", m.agendaPlace(h, parentMaxWidth)))
	}

	suffix := tags + place(-1)
	if m.width > 0 && lipgloss.Width(prefix)+lipgloss.Width(suffix) > m.width {
		overhead := lipgloss.Width(tags) + lipgloss.Width(place(0))
		parentBudget := m.width - lipgloss.Width(prefix) - overhead
		if parentBudget < 0 {
			parentBudget = 0
		}
		suffix = tags + place(parentBudget)
	}

	return fitRowLine(prefix, suffix, m.width, bg)
}

// calendarItemTime renders h's GCAL_START/GCAL_END as just the time of
// day, with no date — calendarView already groups h under its own
// day's header row (see appendCalendarRows), so the date would be
// redundant here. "All day" for an all-day event (both endpoints at
// local midnight — see eventBounds in internal/calendarsync/convert.go); empty
// if GCAL_START/GCAL_END don't parse (e.g. h isn't actually a synced
// calendar event).
func calendarItemTime(h *org.Headline) string {
	start, startOK := meetings.TimeProperty(h, "GCAL_START")
	end, endOK := meetings.TimeProperty(h, "GCAL_END")
	if !startOK || !endOK {
		return ""
	}
	start, end = start.Local(), end.Local()
	if isMidnight(start) && isMidnight(end) && !start.Equal(end) {
		return "All day"
	}
	return start.Format("15:04") + "-" + end.Format("15:04")
}

func sameLocalDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

// renderBodyLineWithBg renders one line of a headline's free-text body,
// indented to line up where a child's own content would start (blank
// mark/gutter/fold columns, since a body line isn't itself a separately
// addressable item — that state lives on the headline's own row), shown
// in a muted style so it doesn't compete visually with real entries.
func (m Model) renderBodyLineWithBg(r row, bg lipgloss.TerminalColor) string {
	// Guides run through a body line too, including its owner's own (the
	// column its children's fold arrows would hang from), when it has
	// children below.
	owner := r.headline.Parent
	if len(r.headline.Children) > 0 {
		owner = r.headline
	}
	indent := m.indentGuides(owner, r.level, m.guideScope(), bg)
	blanks := bgSpan(bg, "     ") + indent + bgSpan(bg, "  ") // mark + lock + meeting + dirty gutter + space, then indent, then fold + space
	style := m.fadeIfImmutable(m.bodyStyle(), r.headline).Background(bg)
	return blanks + m.highlightMatches(strings.TrimSpace(r.text), m.activeSearchQuery(), style)
}

func planningSummary(h *org.Headline) string {
	var parts []string
	if h.Scheduled != nil {
		parts = append(parts, "SCHEDULED: "+h.Scheduled.String())
	}
	if h.Deadline != nil {
		parts = append(parts, "DEADLINE: "+h.Deadline.String())
	}
	if h.Closed != nil {
		parts = append(parts, "CLOSED: "+h.Closed.String())
	}
	return strings.Join(parts, "  ")
}

const (
	defaultGuideColor      = "#444444" // dim: just enough to follow with the eye
	defaultGuideScopeColor = "#8a8a8a" // brighter: the cursor's own sibling group
)

// guideScope returns the headline whose children are the cursor row's
// siblings — the cursor headline's parent, or, on a body line, the
// owning headline itself — so indentGuides can draw that one guide
// brighter. nil when the cursor is on a top-level entry or a non-outline
// row.
func (m Model) guideScope() *org.Headline {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return nil
	}
	c := m.rows[m.cursor]
	switch c.kind {
	case rowHeadline:
		return c.headline.Parent
	case rowBody:
		return c.headline
	}
	return nil
}

// indentGuides renders the level*2-column indent of an outline row whose
// nearest ancestor to draw a guide for is innermost: a vertical bar sits
// under each ancestor's fold arrow, so the rows of one sibling group are
// visibly bracketed by the line running down the column to their left.
// The guide for scope (see guideScope) is drawn brighter. Levels are
// 1-based, so level L has L-1 ancestors whose arrows are at columns
// 2*k; the rest of the indent is blank.
func (m Model) indentGuides(innermost *org.Headline, level int, scope *org.Headline, bg lipgloss.TerminalColor) string {
	cols := make([]string, level*2)
	for i := range cols {
		cols[i] = " "
	}
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color(defaultGuideColor)).Background(bg)
	bright := lipgloss.NewStyle().Foreground(lipgloss.Color(defaultGuideScopeColor)).Background(bg)
	styled := make(map[int]lipgloss.Style)
	for a := innermost; a != nil; a = a.Parent {
		col := a.Level * 2
		if col >= len(cols) {
			continue
		}
		cols[col] = "│"
		if a == scope {
			styled[col] = bright
		} else {
			styled[col] = dim
		}
	}
	var b strings.Builder
	for i, c := range cols {
		if st, ok := styled[i]; ok {
			b.WriteString(st.Render(c))
		} else {
			b.WriteString(bgSpan(bg, c))
		}
	}
	return b.String()
}
