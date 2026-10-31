package orgdate

import (
	"regexp"
	"strings"
	"time"

	"github.com/sburnett/orgtd/internal/org"
)

// ClosingNoteHeading is the heading of a note recorded for an ordinary
// close, matching org-mode's own default (org-log-note-headings).
const ClosingNoteHeading = "CLOSING NOTE"

// closingNoteRe matches the first line of a logged note: either a
// ClosingNoteHeading or the state-change heading StateNoteHeading builds.
// Group 1 is the indentation, 2 the new state (empty for a CLOSING NOTE),
// 3 the timestamp without its brackets.
var closingNoteRe = regexp.MustCompile(`^(\s*)- (?:` + ClosingNoteHeading + `|State\s+"([^"]+)"\s+from\b.*?)\s*\[([^\]]+)\]\s*(?:\\\\)?\s*$`)

// StateNoteHeading is the heading of a note recorded for completing a
// repeating item, which never really closes, matching org-mode's own
// "State %-12s from %-12S" (the from side is left empty, not quoted, for
// an item with no keyword).
func StateNoteHeading(to, from string) string {
	if from != "" {
		from = `"` + from + `"`
	}
	return "State " + padRight(`"`+to+`"`, 12) + " from " + padRight(from, 12)
}

func padRight(s string, width int) string {
	if n := width - len([]rune(s)); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

// ClosingNoteLines renders note as the raw body lines org-mode would
// write for it on a headline at level, indented the same way
// org.RenderEntry indents everything else under that headline. Runs of
// whitespace, including line breaks from a paste, collapse to one space,
// since a note is one line.
func ClosingNoteLines(heading, note string, level int, at time.Time) []string {
	indent := strings.Repeat(" ", level+1)
	return []string{
		indent + "- " + heading + " [" + at.Format("2006-01-02 Mon 15:04") + "] \\\\",
		indent + "  " + strings.Join(strings.Fields(note), " "),
	}
}

// ClosingNote returns the text of the note logged for h's latest
// completion (see CompletedTime), or "" if it has none. A note older than
// that completion belongs to an earlier one, and is skipped: a repeating
// item keeps every note, and a reopened item keeps its old one.
func ClosingNote(h *org.Headline) string {
	completed, ok := CompletedTime(h)
	if !ok {
		return ""
	}
	lines := h.TrimmedBody()
	for i, line := range lines {
		m := closingNoteRe.FindStringSubmatch(line)
		if m == nil || (m[2] != "" && !org.IsDoneKeyword(m[2])) {
			continue
		}
		if at, _, err := ParseFlexible(m[3]); err != nil || at.Before(completed) {
			continue
		}
		var text []string
		for _, next := range lines[i+1:] {
			trimmed := strings.TrimSpace(next)
			if trimmed == "" || len(next)-len(strings.TrimLeft(next, " \t")) <= len(m[1]) {
				break
			}
			text = append(text, trimmed)
		}
		return strings.Join(text, " ")
	}
	return ""
}
