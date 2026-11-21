package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sburnett/orgtd/internal/config"
	"github.com/sburnett/orgtd/internal/execlog"
	"github.com/sburnett/orgtd/internal/extprog"
	"github.com/sburnett/orgtd/internal/org"
)

// runFinishedMsg reports that the program launched by startRun has exited.
type runFinishedMsg struct {
	name        string    // the [[commands]] entry's name, for messages
	cmd         *exec.Cmd // for execlog.LogCompleted
	err         error
	contextFile string // the $ORGTD_CONTEXT file to remove, if any
}

// customCommand returns the [[commands]] entry called name, or nil.
func (m *Model) customCommand(name string) *config.CommandConfig {
	for i := range m.cfg.Commands {
		if m.cfg.Commands[i].Name == name {
			return &m.cfg.Commands[i]
		}
	}
	return nil
}

// customCommandNames lists every [[commands]] entry's name, in config
// order.
func (m *Model) customCommandNames() []string {
	names := make([]string, len(m.cfg.Commands))
	for i, c := range m.cfg.Commands {
		names[i] = c.Name
	}
	return names
}

// completeRunArg is ":run"'s Tab completion: the configured command
// names starting with partial. Only the name is completed, not the
// arguments after it.
func (m *Model) completeRunArg(partial string) []string {
	if strings.ContainsAny(partial, " \t") {
		return nil
	}
	var matches []string
	for _, n := range m.customCommandNames() {
		if strings.HasPrefix(n, partial) {
			matches = append(matches, n)
		}
	}
	return matches
}

// startRun runs ":run <name> [args...]": save every file with unsaved
// changes, hand the terminal to the [[commands]] entry called name
// (gitui, jjui, a sync script, ...) running in the org directory, then,
// once it exits, reload whatever it changed on disk (see finishRun). See
// prepareRun for everything up to the launch.
func (m *Model) startRun(line string) tea.Cmd {
	name, cmd, contextFile, ok := m.prepareRun(line)
	if !ok {
		return nil
	}
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return runFinishedMsg{name: name, cmd: cmd, err: err, contextFile: contextFile}
	})
}

// prepareRun does all of startRun but the launch, returning the command
// to run, ready to start, and the $ORGTD_CONTEXT file it was given ("" if
// none), which the caller removes once it exits. It refuses (ok false,
// with m.message saying why) if no such command is configured or if the
// save fails — including when a file changed on disk, since the program
// would then see a state that doesn't match what orgtd is showing.
// Arguments after the name (split shell-style) are appended to the
// command's own. A command with context = true also gets the entries
// :run applies to (see runTargets) as ORGTD_* environment variables and
// in a JSON file named by $ORGTD_CONTEXT.
func (m *Model) prepareRun(line string) (name string, cmd *exec.Cmd, contextFile string, ok bool) {
	targets := m.runTargets()
	m.commandSelection = nil
	name, args, _ := strings.Cut(strings.TrimSpace(line), " ")
	available := "none configured — add a [[commands]] table to the config file"
	if names := m.customCommandNames(); len(names) > 0 {
		available = "available: " + strings.Join(names, ", ")
	}
	if name == "" {
		m.message = "Usage: :run <name> [args...] (" + available + ")"
		return name, nil, "", false
	}
	c := m.customCommand(name)
	if c == nil {
		m.message = fmt.Sprintf("No command named %q (%s)", name, available)
		return name, nil, "", false
	}
	fields := extprog.ParseCommand(c.Command)
	if len(fields) == 0 {
		m.message = fmt.Sprintf("Command %q is empty", name)
		return name, nil, "", false
	}
	msg, saved := m.writeAllResult(false)
	if !saved {
		m.message = msg
		return name, nil, "", false
	}
	fields = append(fields, extprog.ParseCommand(args)...)
	cmd = exec.Command(fields[0], fields[1:]...)
	cmd.Dir = m.ws.Dir
	if c.Context {
		// Built only now, after the save, so line numbers match the disk.
		ctx := m.buildRunContext(targets)
		path, err := writeRunContext(ctx)
		if err != nil {
			m.message = fmt.Sprintf("Not running %s: %v", name, err)
			return name, nil, "", false
		}
		contextFile = path
		cmd.Env = append(append(os.Environ(), ctx.env()...), "ORGTD_CONTEXT="+path)
	}
	return name, cmd, contextFile, true
}

// finishRun reloads every file whose content on disk no longer matches
// what orgtd last wrote (nothing is dirty at this point — startRun just
// saved everything), discarding each one's undo history and marks as
// :e! does. The cursor stays where it was (clamped, if the rows shrank).
func (m Model) finishRun(msg runFinishedMsg) (tea.Model, tea.Cmd) {
	execlog.LogCompleted(m.execLog, msg.cmd, msg.err)
	if msg.contextFile != "" {
		os.Remove(msg.contextFile)
	}

	var reloaded, failed []string
	for _, f := range append([]*org.File(nil), m.ws.Files...) {
		changed, err := org.ChangedOnDisk(f)
		if err == nil && !changed {
			continue
		}
		name := filepath.Base(f.Path)
		if m.dirty[f] {
			failed = append(failed, name+" (edited while "+msg.name+" was running)")
			continue
		}
		if m.fileHasImmutableHeadline(f) {
			failed = append(failed, name+" (being formatted by :format-links)")
			continue
		}
		newFile, err := org.ParseFile(f.Path)
		if err != nil {
			failed = append(failed, fmt.Sprintf("%s (%v)", name, err))
			continue
		}
		m.swapFile(f, newFile)
		reloaded = append(reloaded, name)
	}
	m.rebuildRows()
	m.recomputeDirty()

	var parts []string
	if msg.err != nil {
		parts = append(parts, fmt.Sprintf("%s exited with an error: %v", msg.name, msg.err))
	}
	if len(reloaded) > 0 {
		parts = append(parts, "Reloaded "+strings.Join(reloaded, ", ")+" from disk")
	}
	if len(failed) > 0 {
		parts = append(parts, "Could not reload "+strings.Join(failed, ", ")+" — :e! to retry")
	}
	m.message = strings.Join(parts, "; ")
	return m, nil
}
