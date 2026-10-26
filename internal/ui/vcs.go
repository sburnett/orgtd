package ui

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sburnett/orgtd/internal/execlog"
	"github.com/sburnett/orgtd/internal/extprog"
	"github.com/sburnett/orgtd/internal/org"
)

// vcsFinishedMsg reports that the program launched by startVCS has exited.
type vcsFinishedMsg struct {
	cmd *exec.Cmd // for execlog.LogCompleted
	err error
}

// startVCS runs ":vcs": save every file with unsaved changes, hand the
// terminal to the configured vcs_editor (gitui, jjui, ...) running in the
// org directory, then, once it exits, reload whatever it changed on disk
// (see finishVCS). Refuses without launching anything if vcs_editor isn't
// set or if the save fails — including when a file changed on disk, since
// the VCS tool would then be committing a state that doesn't match what
// orgtd is showing.
func (m *Model) startVCS() tea.Cmd {
	fields := extprog.ParseCommand(m.cfg.VCSEditor)
	if len(fields) == 0 {
		m.message = "No VCS editor configured — set vcs_editor in the config file (e.g. \"gitui\" or \"jjui\")"
		return nil
	}
	msg, ok := m.writeAllResult(false)
	if !ok {
		m.message = msg
		return nil
	}
	cmd := exec.Command(fields[0], fields[1:]...)
	cmd.Dir = m.ws.Dir
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return vcsFinishedMsg{cmd: cmd, err: err}
	})
}

// finishVCS reloads every file whose content on disk no longer matches
// what orgtd last wrote (nothing is dirty at this point — startVCS just
// saved everything), discarding each one's undo history and marks as
// :e! does. The cursor stays where it was (clamped, if the rows shrank).
func (m Model) finishVCS(msg vcsFinishedMsg) (tea.Model, tea.Cmd) {
	execlog.LogCompleted(m.execLog, msg.cmd, msg.err)

	var reloaded, failed []string
	for _, f := range append([]*org.File(nil), m.ws.Files...) {
		changed, err := org.ChangedOnDisk(f)
		if err == nil && !changed {
			continue
		}
		name := filepath.Base(f.Path)
		if m.dirty[f] {
			failed = append(failed, name+" (edited while the VCS editor was open)")
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
		parts = append(parts, fmt.Sprintf("VCS editor exited with an error: %v", msg.err))
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
