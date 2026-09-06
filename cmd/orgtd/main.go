// Command orgtd is a terminal GTD workflow tool backed by org-mode files.
// This initial version only loads, renders, and lets you navigate the org
// files in a directory.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sburnett/orgtd/internal/ui"
	"github.com/sburnett/orgtd/internal/workspace"
)

func defaultOrgDir() string {
	if v := os.Getenv("ORGTD_DIR"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "org"
	}
	return filepath.Join(home, "org")
}

func main() {
	dir := flag.String("dir", defaultOrgDir(), "directory containing org files (default: $ORGTD_DIR or ~/org)")
	flag.Parse()

	ws, err := workspace.Load(*dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "orgtd: %v\n", err)
		os.Exit(1)
	}

	p := tea.NewProgram(ui.New(ws), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "orgtd: %v\n", err)
		os.Exit(1)
	}
}
