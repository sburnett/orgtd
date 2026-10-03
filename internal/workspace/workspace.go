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
