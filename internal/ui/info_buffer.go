package ui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"

	"github.com/sburnett/orgtd/internal/org"
)

// maxRegisterPinnedLines caps how many of the register's entries the
// info buffer's "Register:" section (see registerPinnedLines) shows at
// once. A single yank only ever queues one entry, but <N>dd and a
// visual-mode "d" can queue dozens of top-level entries at a stroke —
// showing all of them would push the actual outline listing off-screen
// entirely, so anything past this count collapses into one "...and N
// more" summary line instead.
const maxRegisterPinnedLines = 5

// pinnedRegister is one register's rows in the info buffer's registers
// section: the marker its rows start with (the register's own name), its
// entries, and whether to render them with the review target's triage
// detail (CREATED, planning) — only the read-only "%" register does.
type pinnedRegister struct {
	marker  string
	entries []*org.Headline
	triage  bool
}

// pinnedRegisters lists every non-empty register in display order: "%"
// (the review target), the unnamed register, then the named ones
// alphabetically.
func (m Model) pinnedRegisters() []pinnedRegister {
	var regs []pinnedRegister
	if e := m.registerContents('%'); len(e) > 0 {
		regs = append(regs, pinnedRegister{"%", e, true})
	}
	if len(m.register) > 0 {
		regs = append(regs, pinnedRegister{"\"", m.register, false})
	}
	for _, r := range m.namedRegisterLetters() {
		regs = append(regs, pinnedRegister{string(r), m.namedRegisters[r], false})
	}
	return regs
}

// registerPinnedLineCount is how many lines the registers section of the
// info buffer occupies below its own label: per register, one per entry,
// or maxRegisterPinnedLines plus one summary line once it holds more
// than that.
func (m *Model) registerPinnedLineCount() int {
	n := 0
	for _, r := range m.pinnedRegisters() {
		if len(r.entries) > maxRegisterPinnedLines {
			n += maxRegisterPinnedLines + 1
		} else {
			n += len(r.entries)
		}
	}
	return n
}

// registerPinnedLines renders the registers section of the info buffer:
// a "Registers:" label, then for each non-empty register (see
// pinnedRegisters) one row per entry (up to maxRegisterPinnedLines, so a
// big <N>dd or visual-mode delete can't push the actual outline listing
// off-screen) followed by a summary line for whatever didn't fit — or
// nil if every register is empty. Each row starts with its register's
// name, so what p/P, "ap or "%p would paste is always visible.
func (m Model) registerPinnedLines() []string {
	regs := m.pinnedRegisters()
	if len(regs) == 0 {
		return nil
	}
	lines := []string{m.padLineToWidth(m.fileStyle().Background(m.overlayBg()).Render("Registers:"), m.overlayBg())}
	for _, r := range regs {
		shown := r.entries
		overflow := 0
		if len(shown) > maxRegisterPinnedLines {
			overflow = len(shown) - maxRegisterPinnedLines
			shown = shown[:maxRegisterPinnedLines]
		}
		for _, h := range shown {
			lines = append(lines, m.renderPinnedRow(r.marker, h, r.triage))
		}
		if overflow > 0 {
			summary := m.statusStyle().Background(m.overlayBg()).Render(fmt.Sprintf("  ...and %d more", overflow))
			lines = append(lines, m.padLineToWidth(summary, m.overlayBg()))
		}
	}
	return lines
}

// renderPinnedRow renders one line of the info buffer's review/marks/
// register sections: marker (the review target's m.cfg.Icons.ReviewIcon, or a
// mark's letter — the register's own callers pass a literal quote mark
// instead) in place of the gutter/indent/fold a normal listing row would
// have, then h's keyword and title — the same format regardless of which
// pinned section it's in, and regardless of h's actual level in its
// file's tree. The marker is colored with m.cfg.Icons.ReviewColor when
// forReview, m.cfg.Icons.MarkColor otherwise (see WithReviewIcon/WithMarkColor),
// so it matches whichever gutter column (markColumn) it echoes. The
// whole line carries the overlay background, padded to fill the
// terminal width. forReview appends h's CREATED property (if it has
// one) and any SCHEDULED/DEADLINE/CLOSED planning line (via
// planningSummary, the same rendering the outline view itself uses) —
// on for the review target, where knowing how long an item has sat in
// the inbox and whether it already has a date is useful triage context;
// off for marks, which can point at any headline in the outline and
// aren't about triage.
func (m Model) renderPinnedRow(marker string, h *org.Headline, forReview bool) string {
	markerColor := orDefault(m.cfg.Icons.MarkColor, defaultMarkColor)
	if forReview {
		markerColor = orDefault(m.cfg.Icons.ReviewColor, defaultReviewColor)
	}
	prefix := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(markerColor)).Background(m.overlayBg()).Render(marker) +
		bgSpan(m.overlayBg(), "  ") +
		joinBg(m.renderKeywordAndTitle(h, m.overlayBg()), m.overlayBg())
	var suffix string
	if forReview {
		if created := h.Properties["CREATED"]; created != "" {
			suffix += bgSpan(m.overlayBg(), "  ") + m.timestampStyle().Background(m.overlayBg()).Render("Created: "+created)
		}
		if planning := planningSummary(h); planning != "" {
			suffix += bgSpan(m.overlayBg(), "  ") + m.timestampStyle().Background(m.overlayBg()).Render(planning)
		}
	}
	return m.padLineToWidth(fitRowLine(prefix, suffix, m.width, m.overlayBg()), m.overlayBg())
}

// sectionSeparatorBudget is how many blank separator lines a full render
// could need — one before every section-header row after the first (see
// View). Reserving this many rows of the page for them, even though any
// single page may show fewer section boundaries than the full list has,
// is always safe: unused reservation just becomes ordinary bottom
// padding, exactly like when there are fewer than a page of items.
func (m *Model) sectionSeparatorBudget() int {
	n := 0
	for _, r := range m.rows {
		if r.kind == rowSection {
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return n - 1
}

// linksInTitle returns the URL of every org-mode link in title, in order.
func linksInTitle(title string) []string {
	links := org.ParseLinks(title)
	if len(links) == 0 {
		return nil
	}
	urls := make([]string, len(links))
	for i, l := range links {
		urls[i] = l.URL
	}
	return urls
}

// maxInfoBufferLines caps how many lines any one section of the info
// buffer (see infoBufferLines) shows before collapsing the rest into an
// "...and N more" summary line — same convention as the register's own
// maxRegisterPinnedLines above, just a higher cap: unlike the register
// (routinely a handful of whole entries), a section here is more often
// a handful of one-line items (links, tags, meetings), so it can afford
// to show more before truncating.
const maxInfoBufferLines = 20

// infoBufferHeight is how many lines the info buffer occupies: 0 if
// none of its sections apply, so it doesn't cost a permanent row on
// every screen.
func (m *Model) infoBufferHeight() int {
	return len(m.infoBufferLines())
}

// infoBufferLines renders the info buffer that sits directly above the
// status line — the multi-line counterpart of the single-line status
// area, for whatever might need more than one line to show. Each
// applicable kind gets its own labeled section (already backgrounded/
// padded, ready to write straight to the screen), ordered so that the
// longer a section tends to stay open, the closer to the bottom (i.e.
// the closer to the always-visible status/command lines) it sits —
// keeping whatever's actually on screen from shifting position any more
// than it has to:
//
//   - "Tags:" — while "gt" is prompting for a tag (tagMode) and there's
//     more than one completion match (see completeTagInput), the
//     matches themselves, one per line, instead of the old single
//     space-joined line appended to the prompt.
//
//   - "Status:" — while the "R"/"r" status picker (selectMode) is open,
//     every status candidate (see statusCandidates), one per line, the
//     currently highlighted one in reverse video — the structured
//     counterpart of the old single-line renderStatusSelector.
//
//   - "Attach meeting:" — while the "gM" picker (meetingPickerMode) is
//     open, every meeting candidate matching the typed filter (see
//     meetings.Filter), one per line — title, resolved date,
//     and whether it's already attached to the target entry — the
//     currently highlighted one in reverse video (meetingPickerLines).
//     The structured counterpart of the old single-candidate
//     renderMeetingPicker, which only showed the highlighted one.
//
//   - "Links:" — every org-mode link literally in the current entry's
//     title (linksInTitle). Shown in every mode, not just when it
//     wouldn't otherwise fit on the status line — unlike the old
//     normalStatusLines, this doesn't depend on terminal width at all.
//
//   - "Meeting:" — one line per calendar meeting the current entry is
//     linked to (see calendarEventEntries), each "<title>  <time>  <url>"
//     (or just "<title>  <url>" if no time could be resolved — see
//     calendarEventEntry.hasWhen).
//
//   - "Registers:" — the read-only "%" register (the review target, in
//     reviewView) and whatever's queued in the unnamed paste register
//     (see registerPinnedLines), one row per entry up to
//     maxRegisterPinnedLines.
//
//   - "Active marks:" — every active vim-style mark (see setMark),
//     sorted by letter (sortedMarkLetters), one row each.
//
//   - "Matches:" — the same idea as "Tags:" above, for command-mode
//     ":<Tab>" completions (see completeCommand).
//
// A section that doesn't apply is simply omitted; nil (zero height) if
// none of them do at all.
func (m *Model) infoBufferLines() []string {
	var lines []string

	if above := modeSpecs[m.mode].infoAbove; above != nil {
		lines = append(lines, above(m)...)
	}
	if h := m.currentHeadline(); h != nil {
		lines = m.appendInfoSection(lines, "Links:", linksInTitle(h.Title))
		lines = m.appendInfoSection(lines, "Meeting:", m.calendarEventDisplayLines(h))
	}
	lines = append(lines, m.registerPinnedLines()...)

	if letters := m.sortedMarkLetters(); len(letters) > 0 {
		lines = append(lines, m.padLineToWidth(m.fileStyle().Background(m.overlayBg()).Render("Active marks:"), m.overlayBg()))
		for _, letter := range letters {
			lines = append(lines, m.renderPinnedRow(string(letter), m.marks[letter], false))
		}
	}
	if below := modeSpecs[m.mode].infoBelow; below != nil {
		lines = append(lines, below(m)...)
	}

	if len(lines) == 0 {
		return nil
	}
	// The trailing separator carries the overlay background too, so the
	// tinted block reads as one solid panel rather than cutting off
	// right before an untinted blank line.
	return append(lines, m.padLineToWidth("", m.overlayBg()))
}

// appendInfoSection appends one info-buffer section to lines: a label
// row, then up to maxInfoBufferLines of items — plain, unstyled text,
// in particular so a URL among them can still be detected/clicked by
// the terminal, same reasoning the old normalStatusLines called out —
// then a "...and N more" summary for anything past that cap. Returns
// lines unchanged if items is empty, so a section with nothing to show
// never contributes a bare label.
func (m Model) appendInfoSection(lines []string, label string, items []string) []string {
	rendered := make([]string, len(items))
	for i, item := range items {
		rendered[i] = m.statusStyle().Background(m.overlayBg()).Render(" " + item)
	}
	return m.appendInfoSectionRendered(lines, label, rendered)
}

// appendInfoSectionRendered is appendInfoSection's lower-level
// counterpart, for a section whose items need their own styling per
// line (e.g. the "Status:" section's highlighted candidate — see
// statusSelectorLines) rather than the uniform plain-text style
// appendInfoSection applies. items are already fully rendered; this
// only handles the shared label/cap/overflow/padding scaffolding.
func (m Model) appendInfoSectionRendered(lines []string, label string, items []string) []string {
	if len(items) == 0 {
		return lines
	}
	lines = append(lines, m.padLineToWidth(m.fileStyle().Background(m.overlayBg()).Render(label), m.overlayBg()))
	shown := items
	overflow := 0
	if len(shown) > maxInfoBufferLines {
		overflow = len(shown) - maxInfoBufferLines
		shown = shown[:maxInfoBufferLines]
	}
	for _, item := range shown {
		lines = append(lines, m.padLineToWidth(item, m.overlayBg()))
	}
	if overflow > 0 {
		lines = append(lines, m.padLineToWidth(m.statusStyle().Background(m.overlayBg()).Render(fmt.Sprintf("  ...and %d more", overflow)), m.overlayBg()))
	}
	return lines
}
