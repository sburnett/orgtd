package ui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sburnett/orgtd/internal/execlog"
	"github.com/sburnett/orgtd/internal/extprog"
	"github.com/sburnett/orgtd/internal/gitstore"
	"github.com/sburnett/orgtd/internal/merge3"
	"github.com/sburnett/orgtd/internal/org"
)

// resolveFinishedMsg reports that the merge tool :resolve launched has
// exited.
type resolveFinishedMsg struct {
	cmd      *exec.Cmd
	err      error
	dir      string                       // the scratch folder holding the conflicted files
	conflict map[string]gitstore.Conflict // what was handed to the tool, by path

	// branch is the phone's branch's merge being resolved, when that's
	// what was handed to the tool rather than a save's conflict.
	branch *gitstore.BranchConflict
}

// startResolve runs ":resolve": save again (merging from scratch, so
// edits made since the conflict count), and if files still conflict,
// write them — conflict markers and all — to a scratch folder and run the
// merge tool (merge_tool, else the editor) on them, with the terminal
// handed over. With no save conflict, it does the same for the phone's
// branch's merge with the main branch, if that collided (see finishPoll).
// finishResolve takes it from there.
func (m *Model) startResolve() tea.Cmd {
	msg, ok := m.writeAllResult(false)
	switch {
	case !ok && m.git.conflict != nil:
		return m.launchMergeTool(m.git.conflict.files, nil)
	case ok && m.git.mobileConflict != nil:
		return m.launchMergeTool(m.git.mobileConflict.Files, m.git.mobileConflict)
	}
	m.message = msg
	return nil
}

// launchMergeTool writes conflict's files to a scratch folder and runs
// the merge tool on them; branch is the phone's merge they're from, if
// they aren't a save's.
func (m *Model) launchMergeTool(conflict map[string]gitstore.Conflict, branch *gitstore.BranchConflict) tea.Cmd {
	store, err := m.gitStore()
	if err != nil {
		m.message = err.Error()
		return nil
	}
	dir := filepath.Join(m.scratchDir(), "merge-"+store.ID())
	os.RemoveAll(dir)
	var paths []string
	for _, rel := range sortedKeys(conflict) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			m.message = "Can't write conflicts: " + err.Error()
			return nil
		}
		if err := os.WriteFile(p, conflict[rel].Merged, 0o644); err != nil {
			m.message = "Can't write conflicts: " + err.Error()
			return nil
		}
		paths = append(paths, p)
	}
	fields := extprog.ParseCommand(m.mergeTool())
	cmd := exec.Command(fields[0], append(fields[1:], paths...)...)
	cmd.Dir = dir
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return resolveFinishedMsg{cmd: cmd, err: err, dir: dir, conflict: conflict, branch: branch}
	})
}

// mergeTool is the command :resolve runs: merge_tool, else the editor,
// else vim.
func (m *Model) mergeTool() string {
	for _, c := range []string{m.cfg.MergeTool, m.editorCommand()} {
		if len(extprog.ParseCommand(c)) > 0 {
			return c
		}
	}
	return "vim"
}

// finishResolve takes the files back from the merge tool. If any still
// has conflict markers, nothing changes. Otherwise each resolved file
// replaces this instance's version, based on the main branch's version
// the conflict was computed against — so saving it (done right away)
// merges cleanly unless the branch moved again since.
func (m Model) finishResolve(msg resolveFinishedMsg) (tea.Model, tea.Cmd) {
	execlog.LogCompleted(m.execLog, msg.cmd, msg.err)
	if msg.err != nil {
		m.message = fmt.Sprintf("Merge tool exited with an error: %v — nothing changed; :resolve to try again", msg.err)
		return m, nil
	}
	resolved := map[string][]byte{}
	var unresolved []string
	for rel := range msg.conflict {
		data, err := os.ReadFile(filepath.Join(msg.dir, filepath.FromSlash(rel)))
		if err != nil {
			m.message = fmt.Sprintf("Reading resolved %s: %v", rel, err)
			return m, nil
		}
		if merge3.HasMarkers(data) {
			unresolved = append(unresolved, rel)
		}
		resolved[rel] = data
	}
	if len(unresolved) > 0 {
		m.message = strings.Join(unresolved, ", ") + " still has conflict markers — nothing changed; :resolve to try again"
		return m, nil
	}
	if msg.branch != nil {
		return m.finishBranchResolve(msg.branch, resolved, msg.dir)
	}

	anchor := m.cursorAnchor()
	for rel, data := range resolved {
		f := m.loadedFile(filepath.Join(m.ws.Dir, filepath.FromSlash(rel)))
		if f == nil {
			continue
		}
		newFile, err := org.ParseContent(data, f.Path, m.kw)
		if err != nil {
			m.message = fmt.Sprintf("Parsing resolved %s: %v", rel, err)
			return m, nil
		}
		theirs := msg.conflict[rel].Theirs
		newFile.Base, newFile.New = theirs, theirs == nil
		m.swapFile(f, newFile)
		m.git.mustSave[newFile] = true
	}
	m.git.conflict = nil
	m.recomputeDirty()
	m.rebuildRows()
	m.restoreCursor(anchor)

	saved, ok := m.writeAllResult(false)
	if ok {
		os.RemoveAll(msg.dir)
		m.message = "Resolved; " + saved
	} else {
		m.message = saved
	}
	return m, nil
}

// recoverOrphans runs ":recover": load the unsaved edits of crashed orgtd
// instances found at startup (see gitstore.Store.Orphans) into memory, as
// if made here, and save them, merging like any save — conflicts
// included, resolved the usual way. Each orphan's pending ref is dropped
// once a save gets its edits into the main branch. ":recover drop"
// discards them instead. A file with unsaved edits of its own here must
// be saved or reloaded first.
func (m *Model) recoverOrphans(arg string) {
	orphans := m.git.orphans
	if len(orphans) == 0 {
		m.message = "Nothing to recover"
		return
	}
	store, err := m.gitStore()
	if err != nil {
		m.message = err.Error()
		return
	}
	switch arg {
	case "drop":
		for _, o := range orphans {
			if err := store.Drop(o); err != nil {
				m.message = "Dropping a crashed orgtd's edits: " + err.Error()
				return
			}
		}
		m.git.orphans = nil
		m.message = fmt.Sprintf("Discarded unsaved edits from %d crashed orgtd instance(s)", len(orphans))
		return
	case "":
	default:
		m.message = "Usage: :recover, or :recover drop"
		return
	}

	// Oldest first, so a later save of the same file wins.
	sorted := append([]gitstore.Orphan(nil), orphans...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].When.Before(sorted[j].When) })
	var edits []gitstore.Edit
	for _, o := range sorted {
		oe, err := store.OrphanEdits(o)
		if err != nil {
			m.message = "Reading a crashed orgtd's edits: " + err.Error()
			return
		}
		edits = append(edits, oe...)
	}
	for _, e := range edits {
		if f := m.loadedFile(filepath.Join(m.ws.Dir, filepath.FromSlash(e.Path))); f != nil && m.unsaved(f) {
			m.message = e.Path + " has unsaved edits here — :w or :e! it first, then :recover"
			return
		}
	}

	anchor := m.cursorAnchor()
	var names []string
	for _, e := range edits {
		path := filepath.Join(m.ws.Dir, filepath.FromSlash(e.Path))
		newFile, err := org.ParseContent(e.Content, path, m.kw)
		if err != nil {
			m.message = fmt.Sprintf("Parsing recovered %s: %v", e.Path, err)
			return
		}
		newFile.Base, newFile.New = e.Base, !e.HasBase
		if f := m.loadedFile(path); f != nil {
			m.swapFile(f, newFile)
		} else {
			m.ws.Files = append(m.ws.Files, newFile)
			sort.Slice(m.ws.Files, func(i, j int) bool { return m.ws.Files[i].Path < m.ws.Files[j].Path })
		}
		m.git.mustSave[newFile] = true
		names = append(names, e.Path)
	}
	m.git.recovering = orphans
	m.git.orphans = nil
	m.recomputeDirty()
	m.rebuildRows()
	m.restoreCursor(anchor)

	msg, _ := m.writeAllResult(false)
	m.message = "Recovered " + strings.Join(names, ", ") + "; " + msg
}

// finishBranchResolve commits the resolution of the phone's branch's
// merge (see gitstore.ResolveBranch) and reloads the files it changed.
func (m Model) finishBranchResolve(c *gitstore.BranchConflict, resolved map[string][]byte, dir string) (tea.Model, tea.Cmd) {
	store, err := m.gitStore()
	if err != nil {
		m.message = err.Error()
		return m, nil
	}
	res, err := store.ResolveBranch(c, resolved)
	switch {
	case errors.Is(err, gitstore.ErrBranchMoved):
		m.git.mobileConflict, m.git.mobile = nil, ""
		m.message = "The phone synced again since — its changes are merged afresh; :resolve again if they still conflict"
		return m, nil
	case err != nil:
		m.message = "Merging " + mobileBranch + ": " + err.Error()
		return m, nil
	}
	m.noteMobile(res)
	switch {
	case res.Conflict != nil:
		m.message = store.MainBranch() + " changed the same lines again since — :resolve to try again"
		return m, nil
	case !res.Moved:
		if res.Blocked == nil && res.Unmanaged == nil {
			m.message = "Already merged"
			os.RemoveAll(dir)
		}
		return m, nil
	}
	os.RemoveAll(dir)
	reloaded, _ := m.reloadChanged(res.Head, res.Files, nil)
	m.message = "Resolved; merged changes from " + mobileBranch + " into " + strings.Join(res.Changed, ", ")
	if len(reloaded) > 0 {
		m.message += "; reloaded " + strings.Join(reloaded, ", ")
	}
	return m, nil
}
