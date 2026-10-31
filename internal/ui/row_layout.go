package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/sburnett/orgtd/internal/meetings"
	"github.com/sburnett/orgtd/internal/org"
)

const (
	// titleSharePercent is how much of the space after a row's lead the
	// title may always claim once the row doesn't fit; see allocateRow.
	titleSharePercent = 60

	// titleFloorPercent is as far as a row's other parts can push the
	// title below that share to stay readable.
	titleFloorPercent = 33

	// minMetaWidth is the narrowest a metadata part is worth drawing at
	// once it has been cut: anything less is a lone "…" and a tag's first
	// letter, so the part is dropped and its room goes to the rest.
	minMetaWidth = 4

	// metaGap is the blank space in front of each metadata part.
	metaGap = 2
)

// rowMeta is one part of a row's trailing metadata (its tags, dates, or
// where it's from), as plain text: styling only happens once its final
// width is known, so a cut never splits an escape code.
type rowMeta struct {
	text    string                 // as shown on one line, when the row fits
	compact string                 // the tighter form used once it doesn't; "" if there isn't one
	render  func(s string) string  // styles the text
	cut     func(width int) string // shortens text to width; nil ellipsizes its end
	keep    int                    // how much of it is worth taking from the title's share to show

	wrap      func(width int) []string // the lines it wraps to in a column of width; nil for a row that never wraps
	wrapW     int                      // the column width it would like when wrapped
	wrapMin   int                      // the narrowest column that keeps each of its items on one line
	wrapFloor int                      // the narrowest column that needn't break inside a word
}

// rowContent is a row taken apart for fitOneLine and wrapRow to arrange.
type rowContent struct {
	lead  string // gutter, indent and fold columns: never cut
	blank string // what stands in for lead on a wrapped row's later lines
	title string // keyword and title, styled
	metas []rowMeta
}

// allocateRow divides avail columns between a title of width title and
// metadata of the given widths, for a row that doesn't fit: the title
// keeps up to titleSharePercent, the metadata shares the rest evenly, and
// whatever a part doesn't need goes to the others (the title included).
// keeps says how much of each part must survive to mean anything (see
// rowMeta.keep): that comes first, out of the title's share if need be,
// down to titleFloorPercent. A part it can't give minMetaWidth to is
// dropped, its width 0.
func allocateRow(avail, title int, metas, keeps []int) (int, []int) {
	widths := make([]int, len(metas))
	order := make([]int, 0, len(metas))
	kept := 0
	for i, w := range metas {
		if w > 0 {
			order = append(order, i)
			kept += min(keeps[i], w)
		}
	}
	n := len(order)
	avail = max(avail, 0)
	titleW := titleColumn(avail, title, n*metaGap+kept)
	rest := max(avail-titleW-n*metaGap, 0)

	// Fill the narrowest first, each to an even share of what's left.
	sort.SliceStable(order, func(a, b int) bool { return metas[order[a]] < metas[order[b]] })
	used := 0
	for k, i := range order {
		keep := min(keeps[i], metas[i])
		kept -= keep
		keep = min(keep, max(rest-used-kept, 0))
		share := max(rest-used-kept, 0) / (n - k)
		widths[i] = min(metas[i], max(share, keep))
		used += widths[i]
	}

	for _, i := range order {
		if widths[i] < minMetaWidth && widths[i] < metas[i] {
			trimmed := append([]int(nil), metas...)
			trimmed[i] = 0
			return allocateRow(avail, title, trimmed, keeps)
		}
	}
	return min(title, max(avail-used-n*metaGap, 0)), widths
}

// titleColumn is the title's starting width in a row with others columns
// to fit beside it: its share, or less to make room, but not under its
// floor.
func titleColumn(avail, title, others int) int {
	share := min(title, avail*titleSharePercent/100)
	return max(min(share, avail-others), min(title, avail*titleFloorPercent/100))
}

// allocateWrapped is allocateRow for a row wrapping into columns, where
// a part gets narrower by getting taller rather than by being cut. The
// title has the same share and floor, but gives way to the other parts'
// minimums, and the rest is filled from the right, each part as wide as
// it likes while leaving the ones to its left their minimum: dates, last,
// are the part that reads worst broken up, and tags can take a second
// line at no cost. The minimum is a part's wrapMin, or if that doesn't
// fit its wrapFloor; too narrow for either, it falls back to allocateRow.
func allocateWrapped(avail, title int, metas []rowMeta) (int, []int) {
	wants := make([]int, len(metas))
	for i, m := range metas {
		wants[i] = m.wrapW
	}
	gaps := len(metas) * metaGap
	for _, floor := range []func(rowMeta) int{
		func(m rowMeta) int { return m.wrapMin },
		func(m rowMeta) int { return m.wrapFloor },
	} {
		reserved := 0
		for _, m := range metas {
			reserved += floor(m)
		}
		titleW := titleColumn(avail, title, gaps+reserved)
		rest := avail - titleW - gaps
		if rest < reserved {
			continue
		}
		widths := make([]int, len(metas))
		for i := len(metas) - 1; i >= 0; i-- {
			reserved -= floor(metas[i])
			widths[i] = min(metas[i].wrapW, rest-reserved)
			rest -= widths[i]
		}
		return min(title, titleW+rest), widths
	}
	return allocateRow(avail, title, wants, make([]int, len(metas)))
}

// fitOneLine draws c on a single line of m.width columns. When it doesn't
// all fit, metadata with a compact form switches to it before anything is
// cut, and then allocateRow decides what's left of each part.
func (m Model) fitOneLine(c rowContent, bg lipgloss.TerminalColor) string {
	titleW := lipgloss.Width(c.title)
	natural := lipgloss.Width(c.lead) + titleW
	widths := make([]int, len(c.metas))
	for i, meta := range c.metas {
		widths[i] = lipgloss.Width(meta.text)
		natural += metaGap + widths[i]
	}

	title := c.title
	texts := make([]string, len(c.metas))
	if m.width <= 0 || natural <= m.width {
		for i, meta := range c.metas {
			texts[i] = meta.text
		}
	} else {
		for i, meta := range c.metas {
			texts[i] = meta.text
			if meta.compact != "" {
				texts[i] = meta.compact
				widths[i] = lipgloss.Width(meta.compact)
			}
		}
		var fit int
		keeps := make([]int, len(c.metas))
		for i, meta := range c.metas {
			keeps[i] = meta.keep
		}
		fit, widths = allocateRow(m.width-lipgloss.Width(c.lead), titleW, widths, keeps)
		if fit < titleW {
			title = ansi.Truncate(c.title, fit, bgSpan(bg, "…"))
		}
		for i, meta := range c.metas {
			switch {
			case widths[i] <= 0:
				texts[i] = ""
			case widths[i] >= lipgloss.Width(texts[i]):
			case meta.cut != nil:
				texts[i] = meta.cut(widths[i])
			default:
				texts[i] = ansi.Truncate(texts[i], widths[i], "…")
			}
		}
	}

	var b strings.Builder
	b.WriteString(c.lead)
	b.WriteString(title)
	for i, meta := range c.metas {
		if texts[i] != "" {
			b.WriteString(bgSpan(bg, strings.Repeat(" ", metaGap)))
			b.WriteString(meta.render(texts[i]))
		}
	}
	return b.String()
}

// wrapRow draws c as side-by-side columns, the title and each metadata
// part wrapping within its own, all starting on the first line — so a
// long title never pushes the tags down.
func (m Model) wrapRow(c rowContent, bg lipgloss.TerminalColor) []string {
	titleW := lipgloss.Width(c.title)
	fit, widths := allocateWrapped(m.width-lipgloss.Width(c.lead), titleW, c.metas)

	plain := ansi.Strip(c.title)
	var titleLines []string
	for _, span := range wrapSpans(plain, fit) {
		titleLines = append(titleLines, ansi.Cut(c.title, span[0], span[1]))
	}

	columns := [][]string{titleLines}
	for i, meta := range c.metas {
		if widths[i] <= 0 {
			continue
		}
		var lines []string
		for _, line := range meta.wrap(widths[i]) {
			lines = append(lines, meta.render(line))
		}
		columns = append(columns, lines)
	}

	height := 0
	colWidths := make([]int, len(columns))
	for i, lines := range columns {
		height = max(height, len(lines))
		for _, line := range lines {
			colWidths[i] = max(colWidths[i], lipgloss.Width(line))
		}
	}

	out := make([]string, height)
	for y := range out {
		var b strings.Builder
		if y == 0 {
			b.WriteString(c.lead)
		} else {
			b.WriteString(c.blank)
		}
		// Columns past the last one with anything on this line are left
		// off, so a line carries no trailing padding.
		last := 0
		for x, lines := range columns {
			if y < len(lines) {
				last = x
			}
		}
		for x := 0; x <= last; x++ {
			if x > 0 {
				b.WriteString(bgSpan(bg, strings.Repeat(" ", metaGap)))
			}
			cell := ""
			if y < len(columns[x]) {
				cell = columns[x][y]
			}
			b.WriteString(cell)
			if pad := colWidths[x] - lipgloss.Width(cell); pad > 0 && x < last {
				b.WriteString(bgSpan(bg, strings.Repeat(" ", pad)))
			}
		}
		out[y] = b.String()
	}
	return out
}

// wrapSpans word-wraps s, which has no escape codes, returning each
// line's [start, end) cell range: cells rather than strings so the same
// ranges can be cut out of a styled copy of s (see wrapRow). A word wider
// than width is broken between characters.
func wrapSpans(s string, width int) [][2]int {
	if width <= 0 {
		return [][2]int{{0, lipgloss.Width(s)}}
	}
	var out [][2]int
	start, end := -1, -1 // the line being built, if any

	// breaks are the cell offsets inside the current word it may split at.
	var breaks []int
	add := func(a, b int) {
		for b-a > width {
			if start >= 0 {
				out = append(out, [2]int{start, end})
				start = -1
			}
			cut := a + width
			if i := sort.SearchInts(breaks, cut+1) - 1; i >= 0 && breaks[i] > a {
				cut = breaks[i]
			}
			out = append(out, [2]int{a, cut})
			a = cut
		}
		if start >= 0 && b-start > width {
			out = append(out, [2]int{start, end})
			start = -1
		}
		if start < 0 {
			start = a
		}
		end = b
	}

	pos, word := 0, -1
	for _, r := range s {
		w := lipgloss.Width(string(r))
		switch {
		case r == ' ':
			if word >= 0 {
				add(word, pos)
				word = -1
				breaks = breaks[:0]
			}
		case word < 0:
			word = pos
		default:
			breaks = append(breaks, pos)
		}
		pos += w
	}
	if word >= 0 {
		add(word, pos)
	}
	if start >= 0 {
		out = append(out, [2]int{start, end})
	}
	if len(out) == 0 {
		return [][2]int{{0, pos}}
	}
	return out
}

// wrapPlain is wrapSpans for text that's staying plain.
func wrapPlain(s string, width int) []string {
	var out []string
	for _, span := range wrapSpans(s, width) {
		out = append(out, ansi.Cut(s, span[0], span[1]))
	}
	return out
}

// tagsMeta is the ":tag1:tag2:" part of h's row. tags is the list to
// show: h.Tags, except on a calendar event, which unions in
// meeting-tags.org tags too (see calendarDisplayTags). On one line it's
// capped at maxTagsSuffixWidth; wrapped, it's shown in full, breaking
// between tags. nil if there are none.
func (m Model) tagsMeta(h *org.Headline, tags []string, bg lipgloss.TerminalColor) []rowMeta {
	if len(tags) == 0 {
		return nil
	}
	query := m.activeSearchQuery()
	style := m.fadeIfImmutable(m.tagStyle(), h).Background(bg)
	joined := ":" + strings.Join(tags, ":") + ":"
	widest := 0
	for _, t := range tags {
		widest = max(widest, lipgloss.Width(t))
	}
	return []rowMeta{{
		text:   ansi.Truncate(joined, maxTagsSuffixWidth, "…"),
		render: func(s string) string { return m.highlightMatches(s, query, style) },
		wrap: func(width int) []string {
			lines := wrapPlain(strings.Join(tags, " "), width-2)
			for i, line := range lines {
				lines[i] = ":" + strings.ReplaceAll(line, " ", ":") + ":"
			}
			return lines
		},
		wrapW:     lipgloss.Width(joined),
		wrapMin:   widest + 2,
		wrapFloor: widest + 2,
	}}
}

// planningMeta is the part of h's row showing its planning dates. Wrapped
// they stack, one to a line. nil if it has none.
func (m Model) planningMeta(h *org.Headline, bg lipgloss.TerminalColor) []rowMeta {
	full := planningParts(h)
	if len(full) == 0 {
		return nil
	}
	style := m.fadeIfImmutable(m.timestampStyle(), h).Background(bg)
	widest := 0
	for _, part := range full {
		widest = max(widest, lipgloss.Width(part))
	}
	return []rowMeta{{
		text:    strings.Join(full, "  "),
		compact: strings.Join(m.compactPlanningParts(h), " "),
		keep:    lipgloss.Width(strings.Join(m.compactPlanningParts(h), " ")),
		render:  func(s string) string { return style.Render(s) },
		wrap: func(width int) []string {
			var lines []string
			for _, part := range full {
				lines = append(lines, wrapPlain(part, width)...)
			}
			return lines
		},
		wrapW:     widest,
		wrapMin:   widest,
		wrapFloor: longestDateWord(full),
	}}
}

// longestDateWord is the widest thing in parts wrapping shouldn't split:
// a timestamp counts as one, since it reads as one.
func longestDateWord(parts []string) int {
	widest := 0
	for _, part := range parts {
		for _, word := range strings.Fields(part) {
			widest = max(widest, lipgloss.Width(word))
		}
		if _, ts, ok := strings.Cut(part, ": "); ok {
			widest = max(widest, lipgloss.Width(ts))
		}
	}
	return widest
}

// compactPlanningParts is planningParts in the short form used when a
// row is tight, e.g. "D:11-02".
func (m Model) compactPlanningParts(h *org.Headline) []string {
	var parts []string
	add := func(letter string, ts *org.Timestamp) {
		if ts != nil {
			parts = append(parts, letter+":"+m.compactTimestamp(ts))
		}
	}
	add("S", h.Scheduled)
	add("D", h.Deadline)
	add("C", h.Closed)
	return parts
}

// compactTimestamp is ts's date alone, without its year if that's this
// year; a timestamp without a date to read comes back whole.
func (m Model) compactTimestamp(ts *org.Timestamp) string {
	raw := ts.Raw
	if len(raw) < len("2006-01-02") {
		return raw
	}
	date, err := time.Parse("2006-01-02", raw[:len("2006-01-02")])
	if err != nil {
		return raw
	}
	return m.compactDate(date)
}

func (m Model) compactDate(date time.Time) string {
	if date.Year() == m.now().Year() {
		return date.Format("01-02")
	}
	return date.Format("2006-01-02")
}

// placeMeta is the "[file › parent]" part of a flat row (see agendaPlace).
// When it's cut, the parent's title gives way before the file name.
func (m Model) placeMeta(h *org.Headline, bg lipgloss.TerminalColor) []rowMeta {
	return []rowMeta{{
		text:   "[" + m.agendaPlace(h, -1) + "]",
		render: m.timestampRender(h, bg),
		keep:   lipgloss.Width("[" + m.agendaPlace(h, 0) + "]"),
		cut: func(width int) string {
			budget := width - lipgloss.Width("["+m.agendaPlace(h, 0)+" › ]")
			if h.Parent == nil || budget < 2 {
				budget = 0
			}
			return ansi.Truncate("["+m.agendaPlace(h, budget)+"]", width, "…")
		},
	}}
}

// agendaDateMeta is the "Label: date" part of an agenda row. nil on a
// Next Actions entry, which has no date to show.
func (m Model) agendaDateMeta(r row, bg lipgloss.TerminalColor) []rowMeta {
	if r.agendaLabel == "" {
		return nil
	}
	label := r.agendaLabel
	compact := label[:1] + ":" + m.compactDate(r.agendaDate)
	if r.agendaMissed > 0 {
		label = fmt.Sprintf("%s (%dx)", label, r.agendaMissed)
		compact = fmt.Sprintf("%s (%dx)", compact, r.agendaMissed)
	}
	date := r.agendaDate.Format("2006-01-02 Mon")
	if r.agendaRepeater != "" {
		date += " " + r.agendaRepeater
	}
	return []rowMeta{{
		text:    label + ": " + date,
		compact: compact,
		keep:    lipgloss.Width(compact),
		render:  m.timestampRender(r.headline, bg),
	}}
}

func (m Model) timestampRender(h *org.Headline, bg lipgloss.TerminalColor) func(string) string {
	style := m.fadeIfImmutable(m.timestampStyle(), h).Background(bg)
	return func(s string) string { return style.Render(s) }
}

// foldColumn is the arrow column of an entry's row: down or right for an
// entry that can be unfolded, or none (blank for a calendar event, a
// small dot for an outline leaf).
func (m Model) foldColumn(h *org.Headline, none string, bg lipgloss.TerminalColor) string {
	if !m.hasFoldableContent(h) {
		return bgSpan(bg, none)
	}
	if m.isFolded(h) {
		return bgSpan(bg, "▶") // U+25B6 BLACK RIGHT-POINTING TRIANGLE (full-size; ▸ is a dedicated "small" variant)
	}
	return bgSpan(bg, "▼") // U+25BC BLACK DOWN-POINTING TRIANGLE (full-size; ▾ is a dedicated "small" variant)
}

// gutterColumns is the five columns every entry row starts with: mark,
// lock, meeting, dirty, and a space.
func (m Model) gutterColumns(h *org.Headline, bg lipgloss.TerminalColor) string {
	return m.markColumn(h, bg) + m.lockColumn(h, bg) + m.meetingColumn(h, bg) + m.gutter(m.dirtyHeadlines[h], bg) + bgSpan(bg, " ")
}

// entryContent takes apart the row of an entry that can wrap: an outline
// headline or a calendar event.
func (m Model) entryContent(r row, bg lipgloss.TerminalColor) rowContent {
	h := r.headline
	if r.kind == rowCalendarEvent {
		// See renderCalendarItemRowWithBg for why the indent is capped.
		indent := strings.Repeat("  ", min(r.level, 1))
		lead := m.gutterColumns(h, bg) + bgSpan(bg, indent) + m.foldColumn(h, " ", bg) + bgSpan(bg, " ")
		if when := calendarItemTime(h); when != "" {
			lead += m.fadeIfImmutable(m.timestampStyle(), h).Background(bg).Render(when) + bgSpan(bg, "  ")
		}
		return rowContent{
			lead:  lead,
			blank: bgSpan(bg, strings.Repeat(" ", lipgloss.Width(lead))),
			title: joinBg(m.renderKeywordAndTitle(h, bg), bg),
			metas: m.tagsMeta(h, m.calendarDisplayTags(h), bg),
		}
	}

	titleParts := m.renderKeywordAndTitle(h, bg)
	if m.view == referenceView {
		titleParts = m.renderPlainTitle(h, bg)
	}
	title := joinBg(titleParts, bg)

	// A folded entry shows how many descendants it's hiding.
	if m.hasFoldableContent(h) && m.isFolded(h) {
		if n := m.visibleDescendantCount(h); n > 0 {
			title += bgSpan(bg, " ") + m.statusStyle().Background(bg).Render(fmt.Sprintf("(%d)", n))
		}
	}

	metas := m.tagsMeta(h, h.Tags, bg)
	if m.view != referenceView {
		metas = append(metas, m.planningMeta(h, bg)...)
	}

	// Guides carry on down a wrapped row's later lines, including its
	// own (see renderBodyLineWithBg), standing in for the fold column.
	owner := h.Parent
	if len(h.Children) > 0 {
		owner = h
	}
	return rowContent{
		// A leaf gets a small dot in the arrow's column, so every row has
		// something sitting where its own guide would hang from.
		lead:  m.gutterColumns(h, bg) + m.indentGuides(h.Parent, h.Level, m.guideScope(), bg) + m.foldColumn(h, "·", bg) + bgSpan(bg, " "), // U+00B7 MIDDLE DOT
		blank: bgSpan(bg, "     ") + m.indentGuides(owner, h.Level+1, m.guideScope(), bg),
		title: title,
		metas: metas,
	}
}

// entryLines draws r, an outline headline or calendar event, as however
// many lines it takes: more than one only while it's unfolded and too
// wide for one (see entryWraps).
func (m Model) entryLines(r row, bg lipgloss.TerminalColor) []string {
	c := m.entryContent(r, bg)
	if m.entryWraps(r.headline) {
		return m.wrapRow(c, bg)
	}
	return []string{m.fitOneLine(c, bg)}
}

// renderEntryContinuation draws one of the later lines of a wrapped
// entry (see appendEntryContinuations). It's blank in the moment between
// a resize and the rebuild that follows, if there's no such line any more.
func (m Model) renderEntryContinuation(r row, bg lipgloss.TerminalColor) string {
	lines := m.entryLines(row{kind: r.continues, headline: r.headline, level: r.headline.Level}, bg)
	if r.entryLine < len(lines) {
		return lines[r.entryLine]
	}
	return ""
}

// entryWraps reports whether h's row is drawn over several lines rather
// than cut to one: it's too wide for one, and it's unfolded.
func (m *Model) entryWraps(h *org.Headline) bool {
	return m.overflows(h) && !m.isFolded(h)
}

// appendEntryContinuations appends a body-like row for each line past the
// first of owner's own row, if it's wrapping, and returns how many: they
// belong to the entry just as its body lines do, so they move and
// highlight with it and are never landed on.
func (m *Model) appendEntryContinuations(dst *[]row, owner row) int {
	if owner.kind != rowHeadline && owner.kind != rowCalendarEvent {
		return 0
	}
	h := owner.headline
	if !m.entryWraps(h) {
		return 0
	}
	saved := m.width
	m.width = m.listWidth()
	n := len(m.entryLines(owner, lipgloss.NoColor{})) - 1
	m.width = saved
	for i := 1; i <= n; i++ {
		*dst = append(*dst, row{headline: h, level: h.Level + 1, kind: rowBody, bodyIdx: i - 1, continues: owner.kind, entryLine: i})
	}
	return n
}

// overflows reports whether h's row, as drawn in the current view, can't
// show everything it has on one line, which makes it worth unfolding even
// with no body or children (see hasFoldableContent). It's worked out from
// plain widths, without rendering anything, since it's asked of every
// leaf on every rebuild. Flat rows have no folds, and a meeting-tags
// record reserves no fold column, so neither ever counts.
func (m *Model) overflows(h *org.Headline) bool {
	width := m.listWidth()
	if width <= 0 || !m.spec().folds {
		return false
	}
	isEvent := false
	if m.view == calendarView || m.view == meetingTagsView {
		if meetings.IsTagRecord(h) {
			return false
		}
		isEvent = calendarItemTime(h) != ""
		if !isEvent && m.view == calendarView {
			return false // an item linked to an event, on a flat row
		}
	}

	total := 5 + 2 + headlineTitleWidth(h)
	tags := h.Tags
	if isEvent {
		total += 2 + lipgloss.Width(calendarItemTime(h)) + 2
		tags = m.calendarDisplayTags(h)
	} else {
		total += 2 * h.Level
	}
	if len(tags) > 0 {
		w := lipgloss.Width(":" + strings.Join(tags, ":") + ":")
		if w > maxTagsSuffixWidth {
			return true
		}
		total += metaGap + w
	}
	if !isEvent && m.view != referenceView {
		if ts := planningSummary(h); ts != "" {
			total += metaGap + lipgloss.Width(ts)
		}
	}
	return total > width
}

// headlineTitleWidth is how wide renderKeywordAndTitle draws h.
func headlineTitleWidth(h *org.Headline) int {
	w := displayWidth(h.Title)
	if h.Priority != "" {
		w += lipgloss.Width(fmt.Sprintf("[#%s] ", h.Priority))
	}
	if h.Keyword != "" {
		w += lipgloss.Width(h.Keyword) + 1
	}
	return w
}
