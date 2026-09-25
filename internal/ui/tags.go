package ui

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/sburnett/orgtd/internal/org"
)

// appendTagsRows populates m.rows for tagsView: one flush-left
// section-header row per distinct tag used anywhere in the workspace
// (excluding calendar_file/meeting_tags_file — see WithCalendarFile/
// WithMeetingTagsFile — the same two files the outline view itself
// excludes, since a synced calendar event's attendee/"recurring" tags,
// and a meeting-tags.org record's own tag, aren't outline entries to
// catalog here), tags sorted alphabetically, each followed by every
// headline carrying that tag, ordered by CREATED (oldest first, same as
// linkedMeetingItems — see headlineCreatedTime) rather than by file/tree
// position. An entry with more than one tag legitimately appears once
// under each. Subject to the same stale-DONE hiding as the outline (see
// hiddenAsStaleDone) — :toggledone affects this view the same way.
func (m *Model) appendTagsRows() {
	byTag := make(map[string][]*org.Headline)
	for _, f := range m.ws.Files {
		if filepath.Base(f.Path) == m.calendarFile || filepath.Base(f.Path) == m.meetingTagsFile {
			continue
		}
		org.Walk(f.Headlines, func(h *org.Headline) {
			if m.hiddenAsStaleDone(h) {
				return
			}
			for _, tag := range h.Tags {
				byTag[tag] = append(byTag[tag], h)
			}
		})
	}
	if len(byTag) == 0 {
		return
	}

	tags := make([]string, 0, len(byTag))
	for tag := range byTag {
		tags = append(tags, tag)
	}
	sort.Strings(tags)

	for _, tag := range tags {
		entries := byTag[tag]
		sort.SliceStable(entries, func(i, j int) bool {
			ti, iok := headlineCreatedTime(entries[i])
			tj, jok := headlineCreatedTime(entries[j])
			if !iok || !jok {
				return false
			}
			return ti.Before(tj)
		})
		m.rows = append(m.rows, row{section: tag})
		for _, h := range entries {
			m.rows = append(m.rows, row{headline: h, level: 1, isTagsItem: true, tagsItemTag: tag})
		}
	}
}

// renderTagsItemRowWithBg renders one tagsView entry row: mark/lock/
// meeting/dirty gutter and keyword/title exactly as an ordinary headline
// row, but flat (no fold column — this row doesn't support folding its
// own children/body, same as renderCalendarLinkedItemRowWithBg) and
// tagged with "[file › parent]" (see agendaPlace), since the entry lives
// wherever it actually does in the outline and wouldn't otherwise be
// placeable from its title alone.
func (m Model) renderTagsItemRowWithBg(r row, bg lipgloss.TerminalColor) string {
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
