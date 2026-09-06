// Package workspace loads a directory of org files into memory. This
// initial version is read-only: it just discovers and parses files for
// the viewer. File watching and mutation (per DESIGN.md) come later.
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

// Load discovers *.org files directly inside dir (non-recursive; the
// .orgtd/ subdirectory for tool-managed files is skipped, since it
// doesn't exist yet in this version) and parses each of them.
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
