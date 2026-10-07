package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// command is one ":" command that isn't just "open a view" (those live in
// viewSpecs). The table of them, commandTable, is what runCommand
// dispatches on and what Tab completion draws from, so adding a command is
// one entry here — nothing else needs to know about it.
type command struct {
	// names is every spelling that runs it, e.g. {"w", "write"}. Each is
	// offered by Tab completion individually, since either is something
	// you might type and want completed.
	names []string

	// takesArg means the command may be followed by a space and an
	// argument (":delmarks ab"). Any other command must be typed exactly:
	// ":w foo" is an unknown command, not ":w".
	takesArg bool

	// run executes the command. arg is whatever followed the name (trimmed;
	// empty if nothing did, and always empty unless takesArg). It returns
	// a tea.Cmd to run, or nil.
	run func(m *Model, arg string) tea.Cmd
}

// commandTable lists every non-view command. Populated in init rather than
// as a var initializer because the handlers reach Model methods that
// (indirectly) read it — Go rejects that as an initialization cycle.
var commandTable []command

func init() {
	commandTable = []command{
		{names: []string{"w", "write"}, run: func(m *Model, _ string) tea.Cmd {
			m.message = m.writeAll()
			return nil
		}},
		{names: []string{"wq"}, run: func(m *Model, _ string) tea.Cmd {
			msg, ok := m.writeAllResult()
			m.message = msg
			if ok {
				return tea.Quit
			}
			return nil
		}},
		{names: []string{"q", "quit"}, run: func(m *Model, _ string) tea.Cmd {
			if len(m.dirty) > 0 {
				m.message = "Unsaved changes — :w to save, or :q! to discard them"
				return nil
			}
			return tea.Quit
		}},
		{names: []string{"q!", "quit!"}, run: func(*Model, string) tea.Cmd { return tea.Quit }},
		{names: []string{"undo"}, run: func(m *Model, _ string) tea.Cmd { m.undo(); return nil }},
		{names: []string{"redo"}, run: func(m *Model, _ string) tea.Cmd { m.redo(); return nil }},
		{names: []string{"noh", "nohlsearch"}, run: func(m *Model, _ string) tea.Cmd {
			m.lastSearchQuery = ""
			return nil
		}},
		{names: []string{"e", "edit"}, takesArg: true, run: func(m *Model, file string) tea.Cmd {
			m.editFile(file)
			return nil
		}},
		{names: []string{"capture"}, run: func(m *Model, _ string) tea.Cmd { return m.startCapture() }},
		{names: []string{"delmarks"}, takesArg: true, run: func(m *Model, letters string) tea.Cmd {
			if letters == "" {
				m.message = "Usage: :delmarks <letters> or :delmarks!"
				return nil
			}
			m.deleteMarks(letters)
			return nil
		}},
		{names: []string{"delmarks!"}, run: func(m *Model, _ string) tea.Cmd {
			m.marks = nil
			m.message = "All marks deleted"
			return nil
		}},
		{names: []string{"clear-registers"}, run: func(m *Model, _ string) tea.Cmd {
			m.register = nil
			m.message = "Register cleared"
			return nil
		}},
		{names: []string{"toggledone"}, run: func(m *Model, _ string) tea.Cmd { m.toggleHideDone(); return nil }},
		{names: []string{"next"}, run: func(m *Model, _ string) tea.Cmd {
			if m.view != clarifyView {
				m.message = ":next only works in clarify view"
			} else {
				m.clarifyStep(1)
			}
			return nil
		}},
		{names: []string{"prev"}, run: func(m *Model, _ string) tea.Cmd {
			if m.view != clarifyView {
				m.message = ":prev only works in clarify view"
			} else {
				m.clarifyStep(-1)
			}
			return nil
		}},
		{names: []string{"format-links"}, run: func(m *Model, _ string) tea.Cmd { return m.startFormatLinks() }},
		{names: []string{"sync-calendar"}, run: func(m *Model, _ string) tea.Cmd { return m.startSyncCalendar(false) }},
		{names: []string{"sync-calendar!"}, run: func(m *Model, _ string) tea.Cmd { return m.startSyncCalendar(true) }},
		{names: []string{"commit"}, run: func(m *Model, _ string) tea.Cmd { return m.startCommit() }},
	}
}

// lookupCommand finds the command line text cmd (already trimmed, not
// empty) refers to: an exact match on one of a command's names, or — for a
// command that takesArg — its name followed by a space and an argument. It
// returns nil if cmd matches nothing.
func lookupCommand(cmd string) (c *command, arg string) {
	name, rest, hasArg := strings.Cut(cmd, " ")
	for i := range commandTable {
		for _, n := range commandTable[i].names {
			switch {
			case n == cmd:
				return &commandTable[i], ""
			case hasArg && n == name && commandTable[i].takesArg:
				return &commandTable[i], strings.TrimSpace(rest)
			}
		}
	}
	return nil, ""
}

// commandNames lists every command-mode word Tab completion knows about:
// every spelling of every table command, plus each view's own ":command"
// (see viewSpecs).
func commandNames() []string {
	var names []string
	for _, c := range commandTable {
		names = append(names, c.names...)
	}
	for k := range viewSpecs {
		names = append(names, viewSpecs[k].command)
	}
	return names
}

// execCommand runs the command line text cmd (trimmed) — a view's
// ":command", or a table command — and returns the tea.Cmd it produced, if
// any. An empty cmd does nothing; an unrecognized one sets an error
// message.
func (m *Model) execCommand(cmd string) tea.Cmd {
	if cmd == "" {
		return nil
	}
	if k, v := viewForCommand(cmd); v != nil {
		m.openView(k)
		return nil
	}
	c, arg := lookupCommand(cmd)
	if c == nil {
		m.message = fmt.Sprintf("Unknown command: %s", cmd)
		return nil
	}
	return c.run(m, arg)
}
