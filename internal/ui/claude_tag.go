package ui

import (
	"slices"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/sburnett/orgtd/internal/org"
)

// claudeSessionProperty is the property scripts/claude_entry.py saves an
// entry's Claude Code session ID in, so running it there again resumes
// that conversation.
const claudeSessionProperty = "CLAUDE_SESSION"

// hasClaudeSession reports whether h has a Claude Code session to resume.
func hasClaudeSession(h *org.Headline) bool {
	return h != nil && strings.TrimSpace(h.Properties[claudeSessionProperty]) != ""
}

// claudeTag is the glyph shown as a generated tag on an entry with a
// Claude Code session (see displayTags).
func (m Model) claudeTag() string {
	return orDefault(m.cfg.Icons.ClaudeIcon, defaultClaudeIcon)
}

func (m Model) claudeTagStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(orDefault(m.cfg.Icons.ClaudeColor, defaultClaudeColor)))
}

// displayTags is tags — h's own, or a calendar event's union (see
// calendarDisplayTags) — as a row shows them: with the Claude tag (see
// claudeTag) appended when h has a CLAUDE_SESSION property. It's
// generated, never stored in h.Tags, so insert mode, which edits the
// headline's own text, doesn't show it, and tag completion doesn't
// offer it. tags itself is never modified.
func (m Model) displayTags(h *org.Headline, tags []string) []string {
	if !hasClaudeSession(h) {
		return tags
	}
	icon := m.claudeTag()
	if slices.Contains(tags, icon) {
		return tags
	}
	return append(slices.Clip(tags), icon)
}

// renderTagText styles s, some or all of a row's ":tag1:tag2:" text, with
// base, highlighting query's matches (see highlightMatches) — except for
// the Claude tag, drawn in its own color so it reads as generated. Only
// an entry with a session can have one, so h without one skips the
// search for it.
func (m Model) renderTagText(h *org.Headline, s, query string, base, claude lipgloss.Style) string {
	if !hasClaudeSession(h) {
		return m.highlightMatches(s, query, base)
	}
	parts := strings.Split(s, m.claudeTag())
	var b strings.Builder
	for i, part := range parts {
		if i > 0 {
			b.WriteString(claude.Render(m.claudeTag()))
		}
		if part != "" {
			b.WriteString(m.highlightMatches(part, query, base))
		}
	}
	return b.String()
}
