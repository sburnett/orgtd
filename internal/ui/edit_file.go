package ui

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sburnett/orgtd/internal/org"
	"github.com/sburnett/orgtd/internal/workspace"
)

// editFile runs ":edit <file>" (":e"), vim's "open this file": the
// cursor goes to the file's header row — in :reference for a file in
// reference/, in :outline for any other. If the file isn't loaded yet it
// is first added to the workspace: loaded from disk if something outside
// orgtd created it, or else as a new, empty file that exists only in
// memory until ":w" — like vim, nothing touches disk until it's saved,
// and a new file left unedited is never written. arg is relative to the org
// directory — "notes", "notes.org" and "reference/wifi" are all fine; the
// ".org" extension is added if missing, and nothing outside the org
// directory and its reference/ subdirectory is accepted, since no other
// directory is ever scanned (see workspace.Load).
//
// The calendar and meeting-tags files have no header row in the views
// that show them, so for those it just opens that view.
func (m *Model) editFile(arg string) {
	if arg == "" {
		m.message = "Usage: :edit <file> — e.g. :e notes, or :e reference/wifi"
		return
	}
	rel, err := editTarget(arg)
	if err != nil {
		m.message = err.Error()
		return
	}
	if workspace.Ignored(filepath.Base(rel), m.cfg.OutlineIgnoreFiles) {
		m.message = rel + " is ignored by outline_ignore_files"
		return
	}
	path := filepath.Join(m.ws.Dir, rel)

	f := m.loadedFile(path)
	created := false
	if f == nil {
		f, created, err = m.addFile(path)
		if err != nil {
			m.message = err.Error()
			return
		}
	}

	switch {
	case m.isNamedFile(f, m.cfg.CalendarFile):
		m.openView(calendarView)
	case m.isNamedFile(f, m.cfg.MeetingTagsFile):
		m.openView(meetingTagsView)
	case m.ws.IsReference(f):
		m.switchToView(referenceView)
		m.focusFile(f)
	default:
		m.switchToView(outlineView)
		m.focusFile(f)
	}
	if created {
		m.message = fmt.Sprintf("%q [New]", rel)
	}
}

// reloadFile runs ":e!": reload a file from the main branch (see
// reloadFromMain), throwing away its in-memory state — unsaved edits,
// undo history, marks — e.g. to take the branch's side of a conflict. arg names the file the way ":edit" does; empty means
// the file under the cursor.
func (m *Model) reloadFile(arg string) {
	var f *org.File
	if arg == "" {
		f = m.currentRowFile()
		if f == nil {
			m.message = "Usage: :e! [file] — reload a file from disk, discarding its unsaved changes"
			return
		}
	} else {
		rel, err := editTarget(arg)
		if err != nil {
			m.message = err.Error()
			return
		}
		if f = m.loadedFile(filepath.Join(m.ws.Dir, rel)); f == nil {
			m.message = rel + " isn't loaded — :e " + arg + " opens it"
			return
		}
	}
	name := filepath.Base(f.Path)
	if m.fileHasImmutableHeadline(f) {
		m.message = name + " has entries being formatted by :format-links; can't reload it yet"
		return
	}
	newFile, err := m.reloadFromMain(f)
	if err != nil {
		m.message = fmt.Sprintf("Could not reload %s: %v", name, err)
		return
	}
	m.rebuildRows()
	m.focusFile(newFile)
	m.recomputeDirty()
	m.message = "Reloaded " + name + " from " + m.cfg.MainBranch
}

// swapFile replaces the loaded file old with newFile (freshly parsed from
// disk), dropping everything that referred to old's headlines: undo
// history, marks, the review target and dirty markers. The caller rebuilds
// the rows.
func (m *Model) swapFile(old, newFile *org.File) {
	for i, wf := range m.ws.Files {
		if wf == old {
			m.ws.Files[i] = newFile
			break
		}
	}
	m.clearRefsForFile(old)
}

// editTarget turns :edit's argument into a path relative to the org
// directory — "name.org" or "reference/name.org" — or an error whose text
// is the message to show.
func editTarget(arg string) (string, error) {
	parts := strings.Split(arg, "/")
	inReference := len(parts) == 2 && parts[0] == workspace.ReferenceDir
	if inReference {
		parts = parts[1:]
	}
	if len(parts) != 1 {
		return "", fmt.Errorf("Only files in the org directory or %s/ can be edited", workspace.ReferenceDir)
	}
	name := parts[0]
	if name == "" || strings.HasPrefix(name, ".") || strings.Contains(name, `\`) {
		return "", fmt.Errorf("Invalid file name %q", name)
	}
	if filepath.Ext(name) != ".org" {
		name += ".org"
	}
	if inReference {
		return workspace.ReferenceDir + "/" + name, nil
	}
	return name, nil
}

// loadedFile returns the workspace file at path, or nil.
func (m *Model) loadedFile(path string) *org.File {
	for _, f := range m.ws.Files {
		if filepath.Clean(f.Path) == path {
			return f
		}
	}
	return nil
}

// addFile adds the file at path to the workspace: parsed from disk if it's
// there (created outside orgtd), otherwise a new, empty file held only in
// memory — nothing is written, not even its directory, until ":w" saves
// an edit to it (see org.File.New). created reports which.
func (m *Model) addFile(path string) (f *org.File, created bool, err error) {
	f, err = org.ParseFile(path, m.kw)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		f, created = &org.File{Path: path, New: true}, true
	case err != nil:
		return nil, false, fmt.Errorf("Loading %s: %v", filepath.Base(path), err)
	}
	m.ws.Files = append(m.ws.Files, f)
	sort.Slice(m.ws.Files, func(i, j int) bool { return m.ws.Files[i].Path < m.ws.Files[j].Path })
	return f, created, nil
}

// completeEditArg lists the ":edit" arguments starting with partial: the
// ".org" files directly in the org directory, "reference/" itself, and the
// files in it — each whether already loaded or only on disk (so a file
// created from a shell can be found without knowing its name by heart).
func (m *Model) completeEditArg(partial string) []string {
	seen := map[string]bool{workspace.ReferenceDir + "/": true}
	for _, dir := range []string{"", workspace.ReferenceDir} {
		entries, _ := os.ReadDir(filepath.Join(m.ws.Dir, dir))
		for _, e := range entries {
			if !e.IsDir() && filepath.Ext(e.Name()) == ".org" && !strings.HasPrefix(e.Name(), ".") {
				seen[filepath.Join(dir, e.Name())] = true
			}
		}
	}
	for _, f := range m.ws.Files {
		if rel, err := filepath.Rel(m.ws.Dir, f.Path); err == nil {
			seen[filepath.ToSlash(rel)] = true
		}
	}

	var matches []string
	for cand := range seen {
		if strings.HasPrefix(cand, partial) && !workspace.Ignored(filepath.Base(cand), m.cfg.OutlineIgnoreFiles) {
			matches = append(matches, cand)
		}
	}
	return matches
}
