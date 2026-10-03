package ui

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sburnett/orgtd/internal/org"
)

func (m Model) updateCommandMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type != tea.KeyTab {
		// Any key other than Tab dismisses a shown completion list, and
		// any error it left (e.g. "No command starting with ...") —
		// they're one-shot hints for the keystroke right after Tab, not
		// a persistent part of the command line.
		m.commandCompletions = ""
		m.message = ""
	}

	switch msg.Type {
	case tea.KeyEsc:
		m.mode = normalMode
		m.commandInput = ""
		return m, nil

	case tea.KeyEnter:
		return m.runCommand()

	case tea.KeyBackspace:
		// Vim exits command-line mode when backspace is pressed with
		// nothing left to delete.
		r := []rune(m.commandInput)
		if len(r) == 0 {
			m.mode = normalMode
			return m, nil
		}
		m.commandInput = string(r[:len(r)-1])
		return m, nil

	case tea.KeySpace:
		m.commandInput += " "
		return m, nil

	case tea.KeyRunes:
		m.commandInput += string(msg.Runes)
		return m, nil

	case tea.KeyTab:
		m.completeCommand()
		return m, nil

	case tea.KeyUp:
		m.recallCommandHistory(-1)
		return m, nil

	case tea.KeyDown:
		m.recallCommandHistory(1)
		return m, nil
	}

	return m, nil
}

// recallCommandHistory moves the command line to an older (dir < 0) or
// newer (dir > 0) entry in commandHistory, matching a shell's own
// history recall: the first ↑ saves whatever was already typed
// (commandHistoryDraft) so a later ↓ back past the most recent entry
// restores it instead of leaving the line blank. Clamped at both ends —
// ↑ stops at the oldest entry, ↓ stops back at the draft — rather than
// wrapping around.
func (m *Model) recallCommandHistory(dir int) {
	if len(m.commandHistory) == 0 {
		return
	}
	if m.commandHistoryPos == len(m.commandHistory) {
		if dir > 0 {
			return
		}
		m.commandHistoryDraft = m.commandInput
	}
	pos := m.commandHistoryPos + dir
	if pos < 0 {
		pos = 0
	}
	if pos > len(m.commandHistory) {
		pos = len(m.commandHistory)
	}
	m.commandHistoryPos = pos
	if pos == len(m.commandHistory) {
		m.commandInput = m.commandHistoryDraft
	} else {
		m.commandInput = m.commandHistory[pos]
	}
}

// runCommand executes the typed command line and always returns to
// normal mode.
// commandNames lists every command-mode word tab completion knows
// about: the commands below plus every view's own (see viewSpecs). Both short and long forms of the same command (e.g. "q" and
// "quit") are listed individually, since either is something you might
// type and want completed.
func commandNames() []string {
	names := []string{
		"w", "write", "wq", "q", "quit", "q!", "quit!",
		"undo", "redo", "capture",
		"delmarks", "delmarks!", "clear-registers", "noh", "nohlsearch", "toggledone", "next", "prev", "format-links", "commit",
		"sync-calendar", "sync-calendar!",
	}
	for k := range viewSpecs {
		names = append(names, viewSpecs[k].command)
	}
	return names
}

// completeCommand implements ":<prefix><Tab>": if the command word
// typed so far (no completion once an argument is being typed, i.e.
// past the first space) is a prefix of exactly one command name, the
// input is completed to it in full; if it's a prefix of several, the
// input is extended to their longest common prefix and the matches are
// listed after it so it's clear what to type next; if it matches none,
// a message says so.
func (m *Model) completeCommand() {
	if strings.Contains(m.commandInput, " ") {
		return
	}
	word := m.commandInput

	var matches []string
	for _, name := range commandNames() {
		if strings.HasPrefix(name, word) {
			matches = append(matches, name)
		}
	}

	switch len(matches) {
	case 0:
		m.message = fmt.Sprintf("No command starting with %q", word)
	case 1:
		m.commandInput = matches[0]
	default:
		sort.Strings(matches)
		if common := commonPrefix(matches); len(common) > len(word) {
			m.commandInput = common
		}
		m.commandCompletions = strings.Join(matches, "  ")
	}
}

// commonPrefix returns the longest string that's a prefix of every
// element of strs. strs must be non-empty.
func commonPrefix(strs []string) string {
	prefix := strs[0]
	for _, s := range strs[1:] {
		for !strings.HasPrefix(s, prefix) {
			prefix = prefix[:len(prefix)-1]
		}
	}
	return prefix
}

func (m Model) runCommand() (tea.Model, tea.Cmd) {
	cmd := strings.TrimSpace(m.commandInput)
	m.mode = normalMode
	m.commandInput = ""

	if cmd != "" {
		// Recorded regardless of whether cmd turns out valid below —
		// same as vim's own cmdline history, which is exactly what
		// makes it useful for recalling and fixing a typo. Not
		// deduplicated, again matching vim, so repeating the same
		// command several times in a row leaves several entries.
		m.commandHistory = append(m.commandHistory, cmd)
	}
	m.commandHistoryPos = len(m.commandHistory)
	m.commandHistoryDraft = ""

	if k, v := viewForCommand(cmd); v != nil {
		m.openView(k)
		return m, nil
	}

	if cmd == "delmarks!" {
		m.marks = nil
		m.message = "All marks deleted"
		return m, nil
	}
	if letters, ok := strings.CutPrefix(cmd, "delmarks "); ok {
		m.deleteMarks(letters)
		return m, nil
	}

	switch cmd {
	case "":
		// Nothing typed; just dismiss the command line.

	case "w", "write":
		m.message = m.writeAll()

	case "wq":
		msg, ok := m.writeAllResult()
		m.message = msg
		if ok {
			return m, tea.Quit
		}

	case "q", "quit":
		if len(m.dirty) > 0 {
			m.message = "Unsaved changes — :w to save, or :q! to discard them"
			return m, nil
		}
		return m, tea.Quit

	case "q!", "quit!":
		return m, tea.Quit

	case "undo":
		m.undo()

	case "redo":
		m.redo()

	case "noh", "nohlsearch":
		m.lastSearchQuery = ""

	case "capture":
		return m, m.startCapture()

	case "delmarks":
		m.message = "Usage: :delmarks <letters> or :delmarks!"

	case "clear-registers":
		m.register = nil
		m.message = "Register cleared"

	case "toggledone":
		m.toggleHideDone()

	case "next":
		if m.view != clarifyView {
			m.message = ":next only works in clarify view"
		} else {
			m.clarifyStep(1)
		}

	case "prev":
		if m.view != clarifyView {
			m.message = ":prev only works in clarify view"
		} else {
			m.clarifyStep(-1)
		}

	case "format-links":
		return m, m.startFormatLinks()

	case "sync-calendar":
		return m, m.startSyncCalendar(false)

	case "sync-calendar!":
		return m, m.startSyncCalendar(true)

	case "commit":
		return m, m.startCommit()

	default:
		m.message = fmt.Sprintf("Unknown command: %s", cmd)
	}
	return m, nil
}

// writeAll writes every file with unwritten in-memory changes to disk,
// returning a status-line summary.
func (m *Model) writeAll() string {
	msg, _ := m.writeAllResult()
	return msg
}

// writeAllResult is writeAll but also reports whether every write
// succeeded (false if any file failed), so callers like :wq can decide
// whether it's safe to proceed. Refuses outright while a :commit's git
// commit/push is still running in the background (see gitRunning): it
// reads the same files that commit just captured a scope/message
// against, so writing over them mid-commit could save changes that
// either end up silently included in a commit already in flight, or
// racing its own on-disk read of what to diff/commit next.
func (m *Model) writeAllResult() (string, bool) {
	if m.gitRunning {
		return "Can't write while git is running in the background — see :log", false
	}

	var written, failed []string
	for _, f := range m.ws.Files {
		if !m.dirty[f] {
			continue
		}
		if err := org.WriteFile(f); err != nil {
			failed = append(failed, fmt.Sprintf("%s (%v)", filepath.Base(f.Path), err))
			continue
		}
		m.savedPos[f] = m.appliedCountForFile(f)
		written = append(written, filepath.Base(f.Path))
	}
	m.recomputeDirty()

	switch {
	case len(failed) > 0:
		return "Failed to write " + strings.Join(failed, ", "), false
	case len(written) == 0:
		return "No changes to write", true
	default:
		return "Wrote " + strings.Join(written, ", "), true
	}
}
