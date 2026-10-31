package ui

import (
	"sort"

	"github.com/charmbracelet/lipgloss"

	"github.com/sburnett/orgtd/internal/org"
	"github.com/sburnett/orgtd/internal/orgdate"
)

// appendTagsRows appends the rows for tagsView: one flush-left
// section-header row per distinct tag used anywhere in the workspace
// (excluding calendar_file/meeting_tags_file — see WithCalendarFile/
// Config.MeetingTagsFile — the same two files the outline view itself
// excludes, since a synced calendar event's attendee/"recurring" tags,
// and a meeting-tags.org record's own tag, aren't outline entries to
// catalog here), tags sorted alphabetically, each followed by every
// headline carrying that tag, ordered by CREATED (oldest first, same as
// linkedMeetingItems — see orgdate.CreatedTime) rather than by file/tree
// position. An entry with more than one tag legitimately appears once
// under each — reference entries (see workspace.Workspace.IsReference)
// included, which is how reference material gets surfaced by topic.
// Subject to the same stale-DONE hiding as the outline (see
// hiddenAsStaleDone) — :toggleclosed affects this view the same way —
// except for reference entries, which have no task state to age out.
func (m *Model) appendTagsRows(dst *[]row) {
	byTag := make(map[string][]*org.Headline)
	for _, f := range m.ws.Files {
		if m.isNamedFile(f, m.cfg.CalendarFile) || m.isNamedFile(f, m.cfg.MeetingTagsFile) {
			continue
		}
		isRef := m.ws.IsReference(f)
		org.Walk(f.Headlines, func(h *org.Headline) {
			if !isRef && m.hiddenAsStaleDone(h) {
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
			ti, iok := orgdate.CreatedTime(entries[i])
			tj, jok := orgdate.CreatedTime(entries[j])
			if !iok || !jok {
				return false
			}
			return ti.Before(tj)
		})
		*dst = append(*dst, row{kind: rowSection, text: tag})
		for _, h := range entries {
			*dst = append(*dst, row{headline: h, level: 1, kind: rowTagsItem, tagsItemTag: tag})
		}
	}
}

// renderTagsItemRowWithBg renders one tagsView or snippetsView entry row: mark/lock/
// meeting/dirty gutter and keyword/title exactly as an ordinary headline
// row, but flat (no fold column — this row doesn't support folding its
// own children/body, same as renderCalendarLinkedItemRowWithBg) and
// tagged with "[file › parent]" (see agendaPlace), since the entry lives
// wherever it actually does in the outline and wouldn't otherwise be
// placeable from its title alone.
func (m Model) renderTagsItemRowWithBg(r row, bg lipgloss.TerminalColor) string {
	return m.fitOneLine(m.flatEntryContent(r, bg), bg)
}
