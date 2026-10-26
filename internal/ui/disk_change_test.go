package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sburnett/orgtd/internal/workspace"
)

// diskChangeFixture loads a one-file workspace, makes an unsaved edit
// (TODO -> NEXT), then rewrites the file on disk behind orgtd's back, as
// a `git pull` would.
func diskChangeFixture(t *testing.T) (Model, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "todo.org")
	if err := os.WriteFile(path, []byte("* TODO Old title\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws, err := workspace.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	m := New(ws)
	m.cursor = findRow(t, m, "Old title")
	m = setStatus(m, "n")
	if err := os.WriteFile(path, []byte("* TODO Upstream title\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return m, path
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestWriteRefusesFileChangedOnDisk(t *testing.T) {
	m, path := diskChangeFixture(t)

	msg, ok := m.writeAllResult(false)

	if ok {
		t.Errorf("ok = true, want the write refused")
	}
	if !strings.Contains(msg, "changed on disk") || !strings.Contains(msg, ":w!") {
		t.Errorf("message = %q, want it to explain and mention :w!", msg)
	}
	if got := readFile(t, path); got != "* TODO Upstream title\n" {
		t.Errorf("disk = %q, want the upstream change left alone", got)
	}
	if !m.dirty[m.ws.Files[0]] {
		t.Errorf("file no longer dirty after a refused write")
	}
}

func TestForceWriteOverwritesChangedFile(t *testing.T) {
	m, path := diskChangeFixture(t)

	msg, ok := m.writeAllResult(true)

	if !ok || !strings.Contains(msg, "Wrote") {
		t.Errorf("= (%q, %v), want a successful write", msg, ok)
	}
	if got := readFile(t, path); !strings.Contains(got, "NEXT Old title") {
		t.Errorf("disk = %q, want our edit", got)
	}
	// And the next plain :w sees our own write as the baseline.
	m = setStatus(m, "t")
	if msg, ok := m.writeAllResult(false); !ok {
		t.Errorf("second write refused after :w!: %q", msg)
	}
}

func TestWriteAllowedWhenContentUnchangedOnDisk(t *testing.T) {
	m, path := diskChangeFixture(t)
	// Rewrite the original bytes: mtime changes, content doesn't.
	if err := os.WriteFile(path, []byte("* TODO Old title\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if msg, ok := m.writeAllResult(false); !ok {
		t.Errorf("write refused for an unchanged file: %q", msg)
	}
}

func TestWriteQuitStaysOpenOnConflict(t *testing.T) {
	m, _ := diskChangeFixture(t)
	if cmd := m.writeAndQuit(false); cmd != nil {
		t.Errorf(":wq quit despite a file changed on disk")
	}
	if cmd := m.writeAndQuit(true); cmd == nil {
		t.Errorf(":wq! didn't quit")
	}
}

func TestReloadDiscardsEditsAndLoadsDisk(t *testing.T) {
	m, path := diskChangeFixture(t)

	m.reloadFile("")

	if !strings.Contains(m.message, "Reloaded") {
		t.Errorf("message = %q", m.message)
	}
	findRow(t, m, "Upstream title")
	if len(m.dirty) != 0 {
		t.Errorf("dirty = %v, want clean after reload", m.dirty)
	}
	if len(m.undoStack) != 0 || m.undoPos != 0 {
		t.Errorf("undo stack = %d/%d, want the reloaded file's history dropped", len(m.undoStack), m.undoPos)
	}
	if got := readFile(t, path); got != "* TODO Upstream title\n" {
		t.Errorf("disk modified by reload: %q", got)
	}
}

func TestReloadNamedFileAndUnknown(t *testing.T) {
	m, _ := diskChangeFixture(t)
	m.reloadFile("nope")
	if !strings.Contains(m.message, "isn't loaded") {
		t.Errorf("message = %q", m.message)
	}
	m.reloadFile("todo")
	findRow(t, m, "Upstream title")
}

// TestWholeFileEditDropsUndoHistory: after "i" on a file row, the
// edited file's old undo entries refer to a discarded tree, so they must
// be gone — undoing one would silently edit a file nobody can see.
func TestWholeFileEditDropsUndoHistory(t *testing.T) {
	m, _ := diskChangeFixture(t) // one unsaved edit in the undo stack
	oldFile := m.ws.Files[0]

	updated, _ := m.Update(fileEditFinishedMsg{target: oldFile})
	m = updated.(Model)

	if len(m.undoStack) != 0 || m.undoPos != 0 {
		t.Errorf("undo stack = %d/%d, want the edited file's history dropped", len(m.undoStack), m.undoPos)
	}
	if len(m.dirty) != 0 {
		t.Errorf("dirty = %v, want clean after reload", m.dirty)
	}
}
