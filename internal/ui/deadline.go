package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/sburnett/orgtd/internal/orgdate"
)

// startSetDeadline opens the deadline-entry prompt ("gd") for the
// current headline, pre-filled with its existing deadline if any. A
// no-op on file rows.
func (m *Model) startSetDeadline() {
	h := m.currentHeadline()
	if h == nil || m.refuseIfImmutable(h) {
		return
	}
	m.mode = deadlineMode
	m.deadlineInput = orgdate.PrefillInput(h.Deadline)
}

// updateDeadlineMode handles key presses while the deadline prompt is
// open: Enter applies the typed date (or clears the deadline if left
// empty), Esc cancels without changes. Every key but Enter also clears
// any error message left over from a previous failed attempt (Enter
// itself goes through applyDeadlineInput, which clears it before
// deciding whether to set a new one) — otherwise a stale error would
// keep showing next to input the user has already started correcting.
func (m Model) updateDeadlineMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type != tea.KeyEnter {
		m.message = ""
	}

	switch msg.Type {
	case tea.KeyEsc:
		m.mode = normalMode
		m.deadlineInput = ""
		return m, nil

	case tea.KeyEnter:
		return m.applyDeadlineInput()

	case tea.KeyBackspace:
		if r := []rune(m.deadlineInput); len(r) > 0 {
			m.deadlineInput = string(r[:len(r)-1])
		}
		return m, nil

	case tea.KeySpace:
		m.deadlineInput += " "
		return m, nil

	case tea.KeyRunes:
		m.deadlineInput += string(msg.Runes)
		return m, nil
	}
	return m, nil
}

// applyDeadlineInput parses the typed date and, if valid, records a
// deadlineChangeAction. An empty input clears the deadline. An invalid
// (non-empty) date is reported on the status line and leaves the prompt
// open, input intact, so it can be corrected. m.message is cleared
// unconditionally up front — every path below either leaves it cleared
// (success, or clearing the deadline) or sets a fresh one (failure), so
// a previous attempt's error never lingers once this one is resolved.
func (m Model) applyDeadlineInput() (tea.Model, tea.Cmd) {
	m.message = ""
	input := strings.TrimSpace(m.deadlineInput)
	h := m.currentHeadline()
	if h == nil {
		m.mode = normalMode
		m.deadlineInput = ""
		return m, nil
	}

	if input == "" {
		m.mode = normalMode
		m.deadlineInput = ""
		if h.Deadline != nil {
			m.pushUndo(&deadlineChangeAction{h: h, f: m.ws.FileOf(h), oldDeadline: h.Deadline, newDeadline: nil})
		}
		return m, nil
	}

	ts, err := orgdate.ParseDeadlineInput(input)
	if err != nil {
		m.message = err.Error()
		return m, nil
	}

	m.mode = normalMode
	m.deadlineInput = ""
	m.pushUndo(&deadlineChangeAction{h: h, f: m.ws.FileOf(h), oldDeadline: h.Deadline, newDeadline: ts})
	return m, nil
}
