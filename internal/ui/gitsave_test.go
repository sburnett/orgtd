package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sburnett/orgtd/internal/workspace"
)

// twoInstances sets up a repository with the given files committed and
// two Models open on it, as two orgtd processes would be.
func twoInstances(t *testing.T, files map[string]string) (a, b Model, dir string) {
	t.Helper()
	dir = t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	initRepo(t, dir)
	load := func() Model {
		ws, err := workspace.Load(dir)
		if err != nil {
			t.Fatal(err)
		}
		m := New(ws)
		t.Cleanup(func() { m.git.store.Close() })
		if _, err := m.gitStore(); err != nil {
			t.Fatal(err)
		}
		return m
	}
	return load(), load(), dir
}

func keywordOf(t *testing.T, m Model, title string) string {
	t.Helper()
	return m.rows[findRow(t, m, title)].headline.Keyword
}

func TestSecondInstanceMergesOnSave(t *testing.T) {
	a, b, dir := twoInstances(t, map[string]string{"todo.org": "* TODO First\n* TODO Middle\n* TODO Last\n"})

	a.cursor = findRow(t, a, "First")
	a = setStatus(a, "n")
	if msg, ok := a.writeAllResult(false); !ok {
		t.Fatalf("a :w = %q", msg)
	}

	b.cursor = findRow(t, b, "Last")
	b = setStatus(b, "w")
	msg, ok := b.writeAllResult(false)
	if !ok || !strings.Contains(msg, "merged changes from main into todo.org") {
		t.Fatalf("b :w = %q, %v; want a merge", msg, ok)
	}
	if got := keywordOf(t, b, "First"); got != "NEXT" {
		t.Errorf("b shows First as %q, want a's NEXT merged in", got)
	}
	if got := keywordOf(t, b, "Last"); got != "WAITING" {
		t.Errorf("b shows Last as %q, want its own WAITING", got)
	}
	if len(b.dirty) != 0 {
		t.Errorf("b still dirty after a merged save: %v", b.dirty)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "todo.org"))
	if want := "* NEXT First\n* TODO Middle\n* WAITING Last\n"; string(data) != want {
		t.Errorf("disk = %q, want %q", data, want)
	}
}

func TestSaveReloadsOtherFilesTheBranchChanged(t *testing.T) {
	a, b, _ := twoInstances(t, map[string]string{"a.org": "* TODO In a\n", "b.org": "* TODO In b\n"})

	a.cursor = findRow(t, a, "In a")
	a = setStatus(a, "n")
	if msg, ok := a.writeAllResult(false); !ok {
		t.Fatalf("a :w = %q", msg)
	}

	b.cursor = findRow(t, b, "In b")
	b = setStatus(b, "n")
	msg, ok := b.writeAllResult(false)
	if !ok || !strings.Contains(msg, "reloaded a.org") {
		t.Fatalf("b :w = %q, %v; want a.org reloaded", msg, ok)
	}
	if got := keywordOf(t, b, "In a"); got != "NEXT" {
		t.Errorf("b shows In a as %q, want a's save", got)
	}
}

func TestConflictingSaveThenForce(t *testing.T) {
	a, b, dir := twoInstances(t, map[string]string{"todo.org": "* TODO Shared\n"})
	a.cursor = findRow(t, a, "Shared")
	a = setStatus(a, "n")
	if msg, ok := a.writeAllResult(false); !ok {
		t.Fatalf("a :w = %q", msg)
	}

	b.cursor = findRow(t, b, "Shared")
	b = setStatus(b, "w")
	msg, ok := b.writeAllResult(false)
	if ok || !strings.Contains(msg, "Conflict in todo.org") {
		t.Fatalf("b :w = %q, %v; want a conflict", msg, ok)
	}
	if !strings.Contains(b.gitStatus(), "conflict in todo.org") {
		t.Errorf("gitStatus = %q", b.gitStatus())
	}
	if len(b.dirty) == 0 {
		t.Errorf("b's edits no longer unsaved after a conflict")
	}

	if msg, ok := b.writeAllResult(true); !ok {
		t.Fatalf("b :w! = %q", msg)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "todo.org"))
	if string(data) != "* WAITING Shared\n" {
		t.Errorf("disk = %q, want b's version", data)
	}
	if b.git.conflict != nil || b.gitStatus() != "" {
		t.Errorf("conflict still recorded after :w!")
	}
}

func TestReloadTakesTheirsAfterConflict(t *testing.T) {
	a, b, _ := twoInstances(t, map[string]string{"todo.org": "* TODO Shared\n"})
	a.cursor = findRow(t, a, "Shared")
	a = setStatus(a, "n")
	a.writeAllResult(false)

	b.cursor = findRow(t, b, "Shared")
	b = setStatus(b, "w")
	b.writeAllResult(false)
	b.reloadFile("todo")

	if got := keywordOf(t, b, "Shared"); got != "NEXT" {
		t.Errorf("after :e!, Shared is %q, want a's NEXT", got)
	}
	if b.git.conflict != nil || len(b.dirty) != 0 {
		t.Errorf("conflict %v / dirty %v left after :e!", b.git.conflict, b.dirty)
	}
}

func TestSaveRefusedOutsideARepository(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "todo.org"), []byte("* TODO x\n"), 0o644)
	ws, _ := workspace.Load(dir)
	m := New(ws)
	m.cursor = findRow(t, m, "x")
	m = setStatus(m, "n")
	msg, ok := m.writeAllResult(false)
	if ok || !strings.Contains(msg, "not the root of a git repository") {
		t.Errorf(":w = %q, %v", msg, ok)
	}
}
