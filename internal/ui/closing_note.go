package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/sburnett/orgtd/internal/org"
	"github.com/sburnett/orgtd/internal/orgdate"
)

// closesAny reports whether applying keyword to any of the pending status
// picker's targets would complete it — what makes the picker ask for a
// closing note before applying. Moving between two done states completes
// nothing, and an entry locked by :format-links won't be changed at all.
func (m *Model) closesAny(keyword string) bool {
	if !m.kw.IsDone(keyword) {
		return false
	}
	targets := m.selectModeTargets
	if len(targets) == 0 {
		h := m.currentHeadline()
		if h == nil {
			return false
		}
		targets = []*org.Headline{h}
	}
	for _, h := range targets {
		if !m.immutable[h] && !m.kw.IsDone(h.Keyword) {
			return true
		}
	}
	return false
}

// updateClosingNoteMode handles key presses while a closing note is
// typed for several entries at once (a single entry's is typed in the
// entry itself — see startStatusNote), shown under each of them (see
// bulkNoteLines): Enter or Esc applies the state chosen in the picker,
// recording the typed note if there is one — as Esc ends a note typed in
// insert mode; "u" undoes it.
func (m Model) updateClosingNoteMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter, tea.KeyEsc, tea.KeyCtrlC:
		m.mode = normalMode
		note := m.closingNoteInput
		m.closingNoteInput = ""
		m.applyStatusToTargets(m.closingNoteKeyword, m.closingNoteLabel, note)
		return m, nil

	case tea.KeyBackspace:
		if r := []rune(m.closingNoteInput); len(r) > 0 {
			m.closingNoteInput = string(r[:len(r)-1])
		}
		return m, nil

	case tea.KeyCtrlU:
		m.closingNoteInput = ""
	case tea.KeyCtrlW:
		in := strings.TrimRight(m.closingNoteInput, " ")
		m.closingNoteInput = in[:strings.LastIndex(in, " ")+1]
	case tea.KeySpace:
		m.closingNoteInput += " "
		return m, nil

	case tea.KeyRunes:
		m.closingNoteInput += string(msg.Runes)
		return m, nil
	}
	return m, nil
}

// withClosingNote returns a extended to also log note on h under heading,
// as one undo step, or a itself if note is blank.
func (m *Model) withClosingNote(a undoAction, h *org.Headline, heading, note string) undoAction {
	if strings.TrimSpace(note) == "" {
		return a
	}
	lines := orgdate.ClosingNoteLines(heading, note, h.Level, m.now())
	return &batchAction{actions: []undoAction{a, &closingNoteAction{
		h:       h,
		f:       m.ws.FileOf(h),
		oldBody: h.Body,
		newBody: append(lines, h.Body...),
	}}}
}

// bulkNoteTargets returns the entries a closing note typed for several
// at once (closingNoteMode, from "R" on a selection or with a count)
// will be logged on: those the chosen state completes. Empty in any
// other mode.
func (m Model) bulkNoteTargets() map[*org.Headline]bool {
	if m.mode != closingNoteMode {
		return nil
	}
	out := map[*org.Headline]bool{}
	for _, h := range m.selectModeTargets {
		if !m.immutable[h] && !m.kw.IsDone(h.Keyword) {
			out[h] = true
		}
	}
	return out
}

// isEntryTitleEnd reports whether row i is the last row of its entry's
// title (it can wrap onto several), before any body rows — where a
// closing note's lines go.
func (m Model) isEntryTitleEnd(i int) bool {
	r := m.rows[i]
	if r.kind == rowBody && r.entryLine == 0 {
		return false
	}
	if i+1 < len(m.rows) {
		next := m.rows[i+1]
		if next.headline == r.headline && next.kind == rowBody && next.entryLine > 0 {
			return false
		}
	}
	return true
}

// bulkNoteLines draws the closing note being typed for several entries
// at once as it'll be logged under r's entry — its heading, then the
// note so far — in insert mode's colors, each with a caret where the
// typing goes, so it's clear the one note is going on all of them.
func (m Model) bulkNoteLines(r row) []string {
	h := r.headline
	bg := m.insertBg()
	heading := orgdate.ClosingNoteHeading
	if orgdate.ChangeState(h, m.closingNoteKeyword, m.now(), m.kw).Repeat {
		heading = orgdate.StateNoteHeading(m.closingNoteKeyword, h.Keyword)
	}
	lines := orgdate.ClosingNoteLines(heading, "", 0, m.now())
	blank := bgSpan(bg, "     ")
	if r.kind == rowHeadline || r.kind == rowCalendarEvent {
		blank = m.entryContent(r, bg).blank
	}
	text := lipgloss.NewStyle().Background(bg)
	return []string{
		m.padLineToWidth(blank+text.Render(strings.TrimLeft(lines[0], " ")), bg),
		m.padLineToWidth(blank+text.Render("  ")+m.caretStyle().Render(noteOrBlank(m.closingNoteInput)), bg),
	}
}

// noteOrBlank is note as drawn under the caret that covers it: the note,
// or one blank cell if there's none yet.
func noteOrBlank(note string) string {
	if note == "" {
		return " "
	}
	return note
}
