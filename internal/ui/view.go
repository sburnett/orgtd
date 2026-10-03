package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func (m Model) View() string {
	var b strings.Builder
	if len(m.rows) == 0 {
		// No items to list (an empty agenda, or a workspace with no org
		// files at all) — still falls through to the status/command-line
		// area below, same as every other case, rather than returning a
		// bare message and skipping it entirely.
		msg := "No org files found."
		if m.view == agendaView {
			msg = fmt.Sprintf("Nothing due in the next %d days. :outline to go back.", m.agendaDays)
		} else if m.view == calendarView {
			msg = "No calendar events found. :outline to go back."
		} else if m.view == meetingTagsView {
			msg = "No meeting tags yet. :outline to go back."
		} else if m.view == tagsView {
			msg = "No tags found. :outline to go back."
		}
		b.WriteString(msg)
		b.WriteString("\n")
		for i := 1; i < m.contentBudget(); i++ {
			b.WriteString("\n")
		}
	} else {
		start := m.offset
		end := start + m.visibleRowCount(start)

		// An entry's body lines highlight along with it — the whole entry
		// is one item, not a separately-steppable row per line — so
		// extend the highlight from the cursor over any of its own body
		// lines that immediately follow. In visual mode, the rest of the
		// selection (from visualAnchor to the cursor) is also
		// highlighted, but with m.visualSelectionBg() rather than m.cursorBg() —
		// otherwise the whole block looks uniform and there'd be no way
		// to tell which end is actually the cursor (e.g. before extending
		// the selection further, or right after Esc leaves the cursor
		// wherever it was).
		cursorStart, cursorEnd := m.cursor, m.entryEnd(m.cursor)
		selStart, selEnd := cursorStart, cursorEnd
		if m.mode == visualMode {
			selStart, selEnd = m.visualRange()
		}

		sepShown := 0
		for i := start; i < end; i++ {
			if i > start && m.rows[i].section != "" {
				b.WriteString("\n")
				sepShown++
			}
			var line string
			switch {
			case i >= cursorStart && i <= cursorEnd:
				line = m.padLineToWidth(m.renderRowWithBg(m.rows[i], m.cursorBg()), m.cursorBg())
			case i >= selStart && i <= selEnd:
				line = m.padLineToWidth(m.renderRowWithBg(m.rows[i], m.visualSelectionBg()), m.visualSelectionBg())
			default:
				line = m.renderRow(m.rows[i])
			}
			b.WriteString(line)
			b.WriteString("\n")
		}
		// Pad with blank lines so the status bar always sits on the last
		// row of the screen — end (via visibleRowCount) already fills as
		// much of contentBudget as the remaining rows allow, so this is
		// only needed once the list itself runs out before the budget
		// does (e.g. the last page of a paginated view).
		for i := (end - start) + sepShown; i < m.contentBudget(); i++ {
			b.WriteString("\n")
		}
	}

	// Info buffer — sits directly above the status/command-line area and
	// holds whatever might need more than one line: the clarify target,
	// active marks, and the paste register (always, in every view, kept
	// first since they're triage/navigation state rather than detail
	// tied to the current entry), then links and calendar-meeting detail
	// for the current entry (always), plus tag/command Tab-completion
	// matches and the status/meeting pickers (only while that mode is
	// active). See infoBufferLines. Renders nothing at all (zero height
	// — see infoBufferHeight) when none of that applies.
	for _, line := range m.infoBufferLines() {
		b.WriteString(line)
		b.WriteString("\n")
	}

	// Status line — vim's own statusline equivalent: always visible
	// regardless of mode, showing where we are (dir/view — item N/M).
	// The command line below it (see the switch that follows) is a
	// separate row for whatever's active right now, mirroring vim's own
	// split between the two rather than the command line ever replacing
	// the status line.
	statusLineStyle := lipgloss.NewStyle().Bold(true).Foreground(m.statusBarFg()).Background(m.statusBarBg())
	b.WriteString(m.padLineToWidth(statusLineStyle.Render(m.normalStatusLine()), m.statusBarBg()))
	b.WriteString("\n")

	// Command line — vim's own command-line/message area equivalent:
	// whatever's active right now (a typed command, a prompt, a mode
	// banner, or the last message), blank if there's nothing to show.
	switch {
	case m.mode == commandMode:
		b.WriteString(":" + m.commandInput)
		b.WriteString(m.caretStyle().Render(" ")) // caret, right after the input (no in-line editing yet)
		// m.message can be set without leaving commandMode (e.g. Tab
		// completion finding no match) — shown here too, not just in the
		// mode-less case below, or it'd be set but never actually visible.
		// Completion matches themselves are in the info buffer above
		// (see infoBufferLines), not appended here.
		if m.message != "" {
			b.WriteString("  " + m.errorStyle().Render(m.message))
		}
	case m.mode == selectMode:
		// The candidate list itself is in the info buffer above (see
		// infoBufferLines' "Status:" section), not appended here.
		prefix := " Set status:  "
		if n := len(m.selectModeTargets); n > 0 {
			prefix = fmt.Sprintf(" Set status for %d selected:  ", n)
		}
		b.WriteString(prefix)
		if m.selectFilter != "" {
			b.WriteString("(" + m.selectFilter + ")")
		}
	case m.mode == meetingPickerMode:
		b.WriteString(m.renderMeetingPicker())
	case m.mode == tagMode:
		b.WriteString(" Tag (Tab completes, empty cancels; retyping an existing tag removes it): " + m.tagInput)
		b.WriteString(m.caretStyle().Render(" "))
		// Completion matches are in the info buffer above (see
		// infoBufferLines), not appended here.
		if m.message != "" {
			b.WriteString("  " + m.errorStyle().Render(m.message))
		}
	case m.mode == deadlineMode:
		b.WriteString(" Deadline (YYYY-MM-DD, \"3d\", \"next tue\"; empty clears): " + m.deadlineInput)
		b.WriteString(m.caretStyle().Render(" "))
		// As above: an invalid date sets m.message but deliberately leaves
		// the prompt open for correction (see applyDeadlineInput), so it
		// must be shown here rather than only in the mode-less case below.
		if m.message != "" {
			b.WriteString("  " + m.errorStyle().Render(m.message))
		}
	case m.mode == searchMode:
		prefix := "/"
		if !m.searchForward {
			prefix = "?"
		}
		b.WriteString(prefix + m.searchQuery)
		b.WriteString(m.caretStyle().Render(" "))
		// m.message can be set without leaving searchMode (a no-match
		// incremental search) — shown here too, not just in the
		// mode-less case below, or it'd be set but never actually visible.
		if m.message != "" {
			b.WriteString("  " + m.errorStyle().Render(m.message))
		}
	case m.mode == confirmMode:
		b.WriteString(m.errorStyle().Render(m.confirmMessage))
	case m.mode == visualMode:
		b.WriteString(m.statusStyle().Render(fmt.Sprintf("-- VISUAL LINE -- %d selected  (d: delete, y: yank, R: set status, Esc: cancel)", len(m.visualSelectedHeadlines()))))
		if m.message != "" {
			b.WriteString("  " + m.errorStyle().Render(m.message))
		}
	case m.message != "":
		b.WriteString(m.errorStyle().Render(m.message))
	}

	return b.String()
}

// normalStatusLine returns the single line for the default (mode-less)
// status area: "dir — item N/M". Links and calendar-meeting detail for
// the current entry no longer live here — they're always in the info
// buffer above (see infoBufferLines), one section per kind rather than
// squeezed onto this line only when there's room for them.
func (m *Model) normalStatusLine() string {
	place := m.ws.Dir
	switch m.view {
	case agendaView:
		place = "agenda"
	case configView:
		place = "config"
	case logView:
		place = "log"
	case diffView:
		place = "diff"
	case helpView:
		place = "help"
	case calendarView:
		place = "calendar"
	case meetingTagsView:
		place = "meeting-tags"
	case tagsView:
		place = "tags"
	}
	return fmt.Sprintf(" %s  —  item %d/%d", place, m.cursor+1, len(m.rows))
}

// statusHeight is how many lines the bottom area occupies in total: the
// one status line (normalStatusLine) plus exactly one command-line row
// below it for whatever's active right now (a typed command, a search/
// deadline prompt, a mode banner, a message, or nothing at all).
// Mirrors vim's own split between its statusline (always
// visible, showing where you are) and the command-line/message area
// below it (always a separate row, regardless of mode) — see View. The
// info buffer (see infoBufferHeight) is accounted for separately, since
// unlike this it can be zero height.
func (m *Model) statusHeight() int {
	return 2
}
