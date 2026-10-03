package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// mode selects how key presses are interpreted: normal mode browses and
// edits the outline, and every other mode is a prompt or sub-state
// entered from it (the ":" command line, a picker, a confirmation, ...).
// What differs per mode lives in its modeSpec, in modeSpecs below — the key
// handler, how the prompt row is drawn, and any info-buffer sections it
// adds — not in switches scattered around the package.
type mode int

const (
	normalMode mode = iota
	commandMode
	selectMode
	deadlineMode
	searchMode
	confirmMode
	visualMode
	meetingPickerMode
	tagMode

	numModes // not a mode: the number of modes, sizing modeSpecs
)

// modeSpec describes one mode. Only update is required.
//
// To add a mode: add a constant above, write its update function, give it
// an entry in init below, and set whatever of prompt/infoAbove/infoBelow
// it needs. Update dispatches on it, and View and the info buffer pick up
// the rest.
type modeSpec struct {
	// update handles a key press in this mode.
	update func(m Model, msg tea.KeyMsg) (tea.Model, tea.Cmd)

	// prompt renders the command-line row at the bottom of the screen
	// while in this mode (a typed command, a prompt with its input, a
	// banner). Nil means the mode has none, and the row shows only the
	// status message, if there is one.
	prompt func(m *Model) string

	// infoAbove and infoBelow return info-buffer sections this mode adds,
	// already rendered: above the sections shown in every mode (a mode's
	// own candidate list reads best nearest the prompt's context) or below
	// them (see infoBufferLines for the ordering rationale). Nil means
	// none.
	infoAbove func(m *Model) []string
	infoBelow func(m *Model) []string
}

// modeSpecs is indexed by mode. Populated in init rather than as a var
// initializer because the handlers reach Model methods that read it —
// Go rejects that as an initialization cycle.
var modeSpecs [numModes]modeSpec

func init() {
	modeSpecs = [numModes]modeSpec{
		normalMode: {update: Model.updateNormalMode},
		visualMode: {
			update: Model.updateVisualMode,
			prompt: func(m *Model) string {
				line := m.statusStyle().Render(fmt.Sprintf("-- VISUAL LINE -- %d selected  (d: delete, y: yank, R: set status, Esc: cancel)", len(m.visualSelectedHeadlines())))
				if m.message != "" {
					line += "  " + m.errorStyle().Render(m.message)
				}
				return line
			},
		},
		commandMode: {
			update: Model.updateCommandMode,
			// Completion matches themselves are in the info buffer (see
			// infoBelow), not appended here.
			prompt: func(m *Model) string { return m.editedLine(":" + m.commandInput) },
			infoBelow: func(m *Model) []string {
				if m.commandCompletions == "" {
					return nil
				}
				return m.appendInfoSection(nil, "Matches:", strings.Fields(m.commandCompletions))
			},
		},
		searchMode: {
			update: Model.updateSearchMode,
			prompt: func(m *Model) string {
				prefix := "/"
				if !m.searchForward {
					prefix = "?"
				}
				return m.editedLine(prefix + m.searchQuery)
			},
		},
		selectMode: {
			update: Model.updateSelectMode,
			// The candidate list itself is in the info buffer (see
			// infoAbove), not appended here.
			prompt: func(m *Model) string {
				prefix := " Set status:  "
				if n := len(m.selectModeTargets); n > 0 {
					prefix = fmt.Sprintf(" Set status for %d selected:  ", n)
				}
				if m.selectFilter != "" {
					prefix += "(" + m.selectFilter + ")"
				}
				return prefix
			},
			infoAbove: func(m *Model) []string {
				return m.appendInfoSectionRendered(nil, "Status:", m.statusSelectorLines())
			},
		},
		meetingPickerMode: {
			update: Model.updateMeetingPickerMode,
			prompt: func(m *Model) string { return m.renderMeetingPicker() },
			infoAbove: func(m *Model) []string {
				return m.appendInfoSectionRendered(nil, "Attach meeting:", m.meetingPickerLines())
			},
		},
		tagMode: {
			update: Model.updateTagMode,
			prompt: func(m *Model) string {
				return m.editedLine(" Tag (Tab completes, empty cancels; retyping an existing tag removes it): " + m.tagInput)
			},
			infoAbove: func(m *Model) []string {
				if m.tagCompletions == "" {
					return nil
				}
				return m.appendInfoSection(nil, "Tags:", strings.Fields(m.tagCompletions))
			},
		},
		deadlineMode: {
			update: Model.updateDeadlineMode,
			prompt: func(m *Model) string {
				return m.editedLine(" Deadline (YYYY-MM-DD, \"3d\", \"next tue\"; empty clears): " + m.deadlineInput)
			},
		},
		confirmMode: {
			update: Model.updateConfirmMode,
			prompt: func(m *Model) string { return m.errorStyle().Render(m.confirmMessage) },
		},
	}
}

// editedLine renders text — a prompt and what's been typed after it —
// followed by the text-input caret and, if one is set, the status message.
// That's the shape of every prompt that takes typed input: a message can
// be set without leaving the mode (a failed Tab completion, an invalid
// date, a no-match incremental search) and has to be visible there, not
// only in the mode-less row.
func (m *Model) editedLine(text string) string {
	line := text + m.caretStyle().Render(" ")
	if m.message != "" {
		line += "  " + m.errorStyle().Render(m.message)
	}
	return line
}
