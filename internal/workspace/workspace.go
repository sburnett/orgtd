// Package workspace loads a directory of org files into memory, and
// provides the advisory lock that keeps two orgtd instances from racing
// over the same directory (see AcquireLock). It does not watch the
// directory for changes or write files itself: the UI mutates the loaded
// trees and saves them with org.WriteFile on :w. See DESIGN.md §3.
package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/sburnett/orgtd/internal/org"
)

// Workspace holds every org file found directly in a directory.
type Workspace struct {
	Dir   string
	Files []*org.File // sorted by Path
}

// Load discovers *.org files directly inside dir and parses each of them.
// It is non-recursive: subdirectories, including the UI's scratch/ buffer
// directory, are never scanned.
func Load(dir string) (*Workspace, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("workspace: reading %s: %w", dir, err)
	}

	var paths []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if filepath.Ext(e.Name()) != ".org" {
			continue
		}
		paths = append(paths, filepath.Join(dir, e.Name()))
	}
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
