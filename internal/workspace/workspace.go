// Package workspace loads a directory of org files into memory, and
// provides the advisory lock that keeps two orgtd instances from racing
// over the same directory (see AcquireLock). It does not watch the
// directory for changes or write files itself: the UI mutates the loaded
// trees and saves them with org.WriteFile on :w. See DESIGN.md §3.
package workspace

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/sburnett/orgtd/internal/org"
)

// ReferenceDir is the one subdirectory of the workspace directory whose
// *.org files are loaded too: reference material (see IsReference), kept
// out of the task views.
const ReferenceDir = "reference"

// Workspace holds every org file found directly in a directory, plus
// those directly in its reference/ subdirectory.
type Workspace struct {
	Dir   string
	Files []*org.File // sorted by Path
}

// Load discovers *.org files directly inside dir, and directly inside
// dir's reference/ subdirectory (if it exists), and parses each of them.
// Neither scan is recursive: any other subdirectory, including the UI's
// scratch/ buffer directory, is never scanned.
func Load(dir string) (*Workspace, error) {
	paths, err := orgPaths(dir)
	if err != nil {
		return nil, err
	}
	refPaths, err := orgPaths(filepath.Join(dir, ReferenceDir))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	paths = append(paths, refPaths...)
	sort.Strings(paths)

	ws := &Workspace{Dir: dir}
	for _, p := range paths {
		f, err := org.ParseFile(p)
		if err != nil {
			return nil, err
		}
		ws.Files = append(ws.Files, f)
	}
	return ws, nil
}

// orgPaths lists the *.org files directly inside dir (not its
// subdirectories).
func orgPaths(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("workspace: reading %s: %w", dir, err)
	}
	var paths []string
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".org" {
			continue
		}
		paths = append(paths, filepath.Join(dir, e.Name()))
	}
	return paths, nil
}

// IsReference reports whether f lives in the workspace's reference/
// subdirectory — reference material in GTD's sense: information with no
// action attached, which the UI shows only in :reference (and, by tag,
// alongside meetings and in :tags) and never treats as tasks. A file is
// told apart by its directory, not its name, so reference/inbox.org is
// not the inbox.
func (w *Workspace) IsReference(f *org.File) bool {
	return filepath.Clean(filepath.Dir(f.Path)) == filepath.Join(filepath.Clean(w.Dir), ReferenceDir)
}

// FileOf returns the loaded file that h's tree belongs to — found by
// walking up to h's root headline and looking for it among each file's
// top-level headlines — or nil if it isn't in any loaded file (e.g. an
// external edit replaced it out from under a stale reference).
func (w *Workspace) FileOf(h *org.Headline) *org.File {
	root := h
	for root.Parent != nil {
		root = root.Parent
	}
	for _, f := range w.Files {
		for _, top := range f.Headlines {
			if top == root {
				return f
			}
		}
	}
	return nil
}

// Locate returns the file, parent (nil if top-level) and index of h within
// its parent's children (or its file's top-level list). f can come back
// nil if h's root isn't in any loaded file — index is -1 in that case too
// when h is top-level, since there's nowhere to look it up; a nested h
// whose parent still lists it is found regardless of f.
func (w *Workspace) Locate(h *org.Headline) (f *org.File, parent *org.Headline, index int) {
	f = w.FileOf(h)
	parent = h.Parent
	return f, parent, org.Siblings{File: f, Parent: parent}.IndexOf(h)
}
