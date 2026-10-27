package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

func (m Model) View() string {
	if m.layout != nil {
		return m.splitView()
	}
	var b strings.Builder
	b.WriteString(m.listing(true))
	m.writeBottom(&b, false)
	return b.String()
}

// listing renders the window's scrollable content area: the rows from
// m.offset that fit, one line each (plus a blank line before each
// section after the first), padded with blank lines to exactly fill
// contentBudget. Every line ends in "\n". The cursor's row is
// highlighted brightly in the active window, faintly in any other (see
// inactiveCursorBg), so the bright one marks where keys will go.
func (m Model) listing(active bool) string {
	cursorBg := m.cursorBg()
	if !active {
		cursorBg = m.inactiveCursorBg()
	}
	var b strings.Builder
	if len(m.rows) == 0 {
		// No items to list (an empty agenda, or a workspace with no org
		// files at all) — still falls through to the status/command-line
		// area below, same as every other case, rather than returning a
		// bare message and skipping it entirely.
		msg := "No org files found."
		if empty := m.spec().empty; empty != nil {
			msg = empty(&m)
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
			if i > start && m.rows[i].kind == rowSection {
				b.WriteString("\n")
				sepShown++
			}
			var line string
			switch {
			case i >= cursorStart && i <= cursorEnd:
				line = m.padLineToWidth(m.renderRowWithBg(m.rows[i], cursorBg), cursorBg)
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
	return b.String()
}

// writeBottom writes everything below the window: its info buffer and
// status line — unless split, where every window draws its own of both
// (see paneLines) — and the command line.
func (m Model) writeBottom(b *strings.Builder, split bool) {
	if !split {
		m.writeInfoAndStatus(b, true)
	}
	m.writeCommandLine(b)
}

// writeInfoAndStatus writes the window's info buffer and, below it, its
// status line — styled as the active window's or not (see statusLine).
func (m Model) writeInfoAndStatus(b *strings.Builder, active bool) {
	// Info buffer — sits directly above the status/command-line area and
	// holds whatever might need more than one line: the review target,
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
	b.WriteString(m.statusLine(active))
	b.WriteString("\n")
}

// writeCommandLine writes the one command line at the bottom of the
// screen.
func (m Model) writeCommandLine(b *strings.Builder) {
	// Command line — vim's own command-line/message area equivalent:
	// whatever's active right now (a typed command, a prompt, a mode
	// banner, or the last message), blank if there's nothing to show.
	if prompt := modeSpecs[m.mode].prompt; prompt != nil {
		b.WriteString(prompt(&m))
	} else if m.message != "" {
		b.WriteString(m.errorStyle().Render(m.message))
	}
}

// statusLine renders the window's status line (see normalStatusLine),
// filling m.width: in the status bar's own colors for the active window,
// or — with splits, for every other window — dimmed, like vim's
// StatusLineNC, so which window has the cursor is clear at a glance.
func (m Model) statusLine(active bool) string {
	style := lipgloss.NewStyle().Bold(true).Foreground(m.statusBarFg()).Background(m.statusBarBg())
	bg := m.statusBarBg()
	if !active {
		bg = m.statusBarInactiveBg()
		style = lipgloss.NewStyle().Foreground(m.statusBarInactiveFg()).Background(bg)
	}
	return m.padLineToWidth(style.Render(m.normalStatusLine()), bg)
}

// normalStatusLine returns the single line for the default (mode-less)
// status area: "dir — item N/M". Links and calendar-meeting detail for
// the current entry no longer live here — they're always in the info
// buffer above (see infoBufferLines), one section per kind rather than
// squeezed onto this line only when there's room for them.
func (m *Model) normalStatusLine() string {
	place := m.ws.Dir
	if label := m.spec().label; label != "" {
		place = label
	}
	pos, total := m.entryPosition()
	line := fmt.Sprintf(" %s  —  item %d/%d", place, pos, total)
	if m.view == calendarView {
		line += "  —  " + m.calendarSyncStatus()
	}
	return line
}

// calendarSyncStatus summarizes, for :calendar's status line, how fresh
// what it's showing is: when the last successful sync was (a bare time if
// it was today, else the date too), and whether the most recent attempt,
// manual or automatic, failed or is still running.
func (m *Model) calendarSyncStatus() string {
	var s string
	switch last := m.lastCalendarSync; {
	case last.IsZero():
		s = "never synced"
	case sameDay(last, m.now()):
		s = "synced " + last.Format("15:04")
	default:
		s = "synced " + last.Format("Jan 2 15:04")
	}
	switch {
	case m.syncingCalendar:
		s += ", syncing now..."
	case m.calendarSyncFailed:
		s += ", last attempt failed (see :log)"
	}
	return s
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.In(a.Location()).Date()
	return ay == by && am == bm && ad == bd
}

// entryPosition is the cursor's 1-based position among every entry in the
// view and the total number of them — counting entries hidden inside
// folds (the view's full, unfolded listing, see searchRows) but not body
// lines, which are part of their entry rather than items of their own.
func (m *Model) entryPosition() (pos, total int) {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return 0, 0
	}
	rows := m.searchRows()
	cur := indexOfRow(rows, m.rows[m.cursor])
	for i, r := range rows {
		if r.kind == rowBody {
			continue
		}
		total++
		if i <= cur {
			pos = total
		}
	}
	return pos, total
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
