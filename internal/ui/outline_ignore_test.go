package ui

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/sburnett/orgtd/internal/workspace"
)

func ignoreFixture(t *testing.T) Model {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "old.org"), []byte("* TODO Old task\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	m := New(&workspace.Workspace{Dir: dir})
	m.cfg.OutlineIgnoreFiles = []string{"old*.org"}
	return m
}

func TestEditRefusesIgnoredFile(t *testing.T) {
	m := ignoreFixture(t)
	m.editFile("old")
	if len(m.ws.Files) != 0 {
		t.Errorf("Files = %v, want the ignored file left unloaded", m.ws.Files)
	}
	if m.message == "" {
		t.Error("expected a message saying the file is ignored")
	}
}

func TestEditCompletionSkipsIgnoredFiles(t *testing.T) {
	m := ignoreFixture(t)
	if got := m.completeEditArg("o"); slices.Contains(got, "old.org") {
		t.Errorf("completeEditArg = %v, want old.org left out", got)
	}
}
