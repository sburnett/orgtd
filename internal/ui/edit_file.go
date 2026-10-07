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
// orgtd created it, or else created empty (on disk right away, since an
// empty file has no edit for ":w" to save). arg is relative to the org
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
		m.message = "Created " + rel
	}
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
// there (created outside orgtd), otherwise created empty first, along
// with its directory. created reports which.
func (m *Model) addFile(path string) (f *org.File, created bool, err error) {
	if _, statErr := os.Stat(path); errors.Is(statErr, fs.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, false, fmt.Errorf("Creating %s: %v", filepath.Dir(path), err)
		}
		fh, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return nil, false, fmt.Errorf("Creating %s: %v", filepath.Base(path), err)
		}
		fh.Close()
		created = true
	}
	f, err = org.ParseFile(path)
	if err != nil {
		return nil, false, fmt.Errorf("Loading %s: %v", filepath.Base(path), err)
	}
	m.ws.Files = append(m.ws.Files, f)
	sort.Slice(m.ws.Files, func(i, j int) bool { return m.ws.Files[i].Path < m.ws.Files[j].Path })
	return f, created, nil
}
