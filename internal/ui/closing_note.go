package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/sburnett/orgtd/internal/org"
	"github.com/sburnett/orgtd/internal/orgdate"
)

// closesAny reports whether applying keyword to any of the pending status
// picker's targets would complete it — what makes the picker ask for a
// closing note before applying. Moving between two done states completes
// nothing, and an entry locked by :format-links won't be changed at all.
func (m *Model) closesAny(keyword string) bool {
	if !org.IsDoneKeyword(keyword) {
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
		if !m.immutable[h] && !org.IsDoneKeyword(h.Keyword) {
			return true
		}
	}
	return false
}

// updateClosingNoteMode handles key presses while the closing-note prompt
// is open: Enter applies the state chosen in the picker, recording the
// typed note if there is one, and Esc cancels the state change altogether.
func (m Model) updateClosingNoteMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.mode = normalMode
		m.closingNoteInput = ""
		m.selectModeTargets = nil
		return m, nil

	case tea.KeyEnter:
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
