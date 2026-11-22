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

func poll(m Model) Model {
	updated, _ := m.Update(gitPollMsg{})
	return updated.(Model)
}

func TestPollingReloadsAnotherInstancesSave(t *testing.T) {
	a, b, _ := twoInstances(t, map[string]string{"todo.org": "* TODO Shared\n* TODO Other\n"})
	b.cursor = findRow(t, b, "Other")

	if b = poll(b); b.message != "" {
		t.Errorf("poll with nothing new: message %q", b.message)
	}

	a.cursor = findRow(t, a, "Shared")
	a = setStatus(a, "n")
	a.writeAllResult(false)

	b = poll(b)
	if got := keywordOf(t, b, "Shared"); got != "NEXT" {
		t.Errorf("after polling, Shared is %q, want a's NEXT", got)
	}
	if !strings.Contains(b.message, "Reloaded todo.org") {
		t.Errorf("message = %q", b.message)
	}
	if h := b.currentHeadline(); h == nil || h.Title != "Other" {
		t.Errorf("cursor moved off its entry: %+v", h)
	}
}

func TestPollingLeavesUnsavedFilesAlone(t *testing.T) {
	a, b, _ := twoInstances(t, map[string]string{"todo.org": "* TODO First\n* TODO Last\n"})
	a.cursor = findRow(t, a, "First")
	a = setStatus(a, "n")
	a.writeAllResult(false)

	b.cursor = findRow(t, b, "Last")
	b = setStatus(b, "w")
	b = poll(b)
	if got := keywordOf(t, b, "First"); got != "TODO" {
		t.Errorf("unsaved file reloaded under the user: First is %q", got)
	}
	if !strings.Contains(stripANSI(b.statusLine(true)), "main moved — :w merges") {
		t.Errorf("status line = %q", stripANSI(b.statusLine(true)))
	}
	if msg, ok := b.writeAllResult(false); !ok {
		t.Fatalf(":w = %q", msg)
	}
	if got := keywordOf(t, b, "First"); got != "NEXT" || b.gitStatus() != "" {
		t.Errorf("after :w, First is %q and status %q", got, b.gitStatus())
	}
}

func TestPollingWaitsOutInsertMode(t *testing.T) {
	a, b, _ := twoInstances(t, map[string]string{"todo.org": "* TODO Shared\n"})
	b.cursor = findRow(t, b, "Shared")
	b = sendKey(b, "i")

	a.cursor = findRow(t, a, "Shared")
	a = setStatus(a, "n")
	a.writeAllResult(false)

	b = poll(b)
	if !b.inInsertSession() || keywordOf(t, b, "Shared") != "TODO" {
		t.Fatalf("reloaded during insert mode")
	}
	b = sendKey(b, "esc")
	b = poll(b)
	if got := keywordOf(t, b, "Shared"); got != "NEXT" {
		t.Errorf("after leaving insert mode, Shared is %q, want a's NEXT", got)
	}
}

// conflicted sets up b with a save that conflicts with a's.
func conflicted(t *testing.T) (b Model, dir string) {
	t.Helper()
	a, b, dir := twoInstances(t, map[string]string{"todo.org": "* TODO Shared\n* TODO Mine\n"})
	a.cursor = findRow(t, a, "Shared")
	a = setStatus(a, "n")
	a.writeAllResult(false)
	b.cursor = findRow(t, b, "Shared")
	b = setStatus(b, "w")
	b.cursor = findRow(t, b, "Mine")
	b = setStatus(b, "n")
	if _, ok := b.writeAllResult(false); ok {
		t.Fatal("expected a conflict")
	}
	return b, dir
}

func TestResolveWithMergeTool(t *testing.T) {
	b, dir := conflicted(t)
	b.cfg.MergeTool = "true"
	if cmd := b.startResolve(); cmd == nil {
		t.Fatalf(":resolve launched nothing: %q", b.message)
	}
	scratch := filepath.Join(b.scratchDir(), "merge-"+b.git.store.ID())
	p := filepath.Join(scratch, "todo.org")
	data, err := os.ReadFile(p)
	if err != nil || !strings.Contains(string(data), "<<<<<<<") || !strings.Contains(string(data), "* NEXT Mine") {
		t.Fatalf("conflict file = %q, %v", data, err)
	}
	conflict := b.git.conflict.files

	// The tool exits with markers left: nothing changes.
	updated, _ := b.Update(resolveFinishedMsg{dir: scratch, conflict: conflict})
	b = updated.(Model)
	if !strings.Contains(b.message, "still has conflict markers") || b.git.conflict == nil {
		t.Fatalf("message = %q", b.message)
	}

	// Resolved by hand: saved straight away.
	os.WriteFile(p, []byte("* DONE Shared\n* NEXT Mine\n"), 0o644)
	updated, _ = b.Update(resolveFinishedMsg{dir: scratch, conflict: conflict})
	b = updated.(Model)
	if !strings.Contains(b.message, "Resolved; Wrote todo.org") {
		t.Fatalf("message = %q", b.message)
	}
	disk, _ := os.ReadFile(filepath.Join(dir, "todo.org"))
	if string(disk) != "* DONE Shared\n* NEXT Mine\n" {
		t.Errorf("disk = %q", disk)
	}
	if b.git.conflict != nil || len(b.dirty) != 0 || keywordOf(t, b, "Shared") != "DONE" {
		t.Errorf("after resolving: conflict %v, dirty %v", b.git.conflict, b.dirty)
	}
	if _, err := os.Stat(scratch); !os.IsNotExist(err) {
		t.Errorf("scratch folder left behind: %v", err)
	}
}

func TestResolveWithNothingToResolveJustSaves(t *testing.T) {
	m := New(loadFixtureCopy(t))
	if cmd := m.startResolve(); cmd != nil || m.message != "No changes to write" {
		t.Errorf(":resolve = %v, %q", cmd, m.message)
	}
}

// crashedInstance leaves behind a dead instance's pending save: b's
// edits, which conflicted with a's, then b exited. It returns a third
// instance, c, started afterwards with the orphan found, as main does.
func crashedInstance(t *testing.T) (a, c Model, dir string) {
	t.Helper()
	a, b, dir := twoInstances(t, map[string]string{"todo.org": "* TODO Shared\n* TODO Mine\n"})
	a.cursor = findRow(t, a, "Shared")
	a = setStatus(a, "n")
	a.writeAllResult(false)
	b.cursor = findRow(t, b, "Shared")
	b = setStatus(b, "w")
	b.cursor = findRow(t, b, "Mine")
	b = setStatus(b, "n")
	if _, ok := b.writeAllResult(false); ok {
		t.Fatal("expected a conflict")
	}
	b.git.store.Close() // b exits

	ws, _ := workspace.Load(dir)
	c = New(ws)
	store, err := c.gitStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	orphans, err := store.Orphans()
	if err != nil || len(orphans) != 1 {
		t.Fatalf("Orphans = %v, %v", orphans, err)
	}
	c.git.orphans = orphans
	return a, c, dir
}

func TestRecoverMergesACrashedInstancesEdits(t *testing.T) {
	a, c, dir := crashedInstance(t)
	// a puts Shared back, so b's edits now merge cleanly.
	a.cursor = findRow(t, a, "Shared")
	a = setStatus(a, "t")
	if msg, ok := a.writeAllResult(false); !ok {
		t.Fatal(msg)
	}
	c.reloadFile("todo")

	c.recoverOrphans("")
	if !strings.Contains(c.message, "Recovered todo.org; Wrote todo.org") {
		t.Fatalf("message = %q", c.message)
	}
	disk, _ := os.ReadFile(filepath.Join(dir, "todo.org"))
	if string(disk) != "* WAITING Shared\n* NEXT Mine\n" {
		t.Errorf("disk = %q", disk)
	}
	if left, _ := c.git.store.Orphans(); len(left) != 0 || len(c.git.recovering) != 0 {
		t.Errorf("orphan not dropped after recovering: %v", left)
	}
}

func TestRecoverConflictThenForce(t *testing.T) {
	_, c, dir := crashedInstance(t)
	c.recoverOrphans("")
	if !strings.Contains(c.message, "Conflict in todo.org") {
		t.Fatalf("message = %q", c.message)
	}
	if left, _ := c.git.store.Orphans(); len(left) != 1 {
		t.Errorf("orphan dropped before its edits were saved")
	}
	if msg, ok := c.writeAllResult(true); !ok {
		t.Fatal(msg)
	}
	disk, _ := os.ReadFile(filepath.Join(dir, "todo.org"))
	if string(disk) != "* WAITING Shared\n* NEXT Mine\n" {
		t.Errorf("disk = %q", disk)
	}
	if left, _ := c.git.store.Orphans(); len(left) != 0 {
		t.Errorf("orphan left after a successful save")
	}
}

func TestRecoverDrop(t *testing.T) {
	_, c, _ := crashedInstance(t)
	c.recoverOrphans("drop")
	if !strings.Contains(c.message, "Discarded") {
		t.Errorf("message = %q", c.message)
	}
	if left, _ := c.git.store.Orphans(); len(left) != 0 {
		t.Errorf("orphan left after :recover drop")
	}
	c.recoverOrphans("")
	if c.message != "Nothing to recover" {
		t.Errorf("message = %q", c.message)
	}
}

func TestStatusLineCountsOtherInstances(t *testing.T) {
	a, b, _ := twoInstances(t, map[string]string{"todo.org": "* TODO First\n"})
	poll := func(m Model) Model {
		t.Helper()
		next, _ := m.pollMain()
		return next.(Model)
	}

	a = poll(a)
	if line := stripANSI(a.normalStatusLine()); !strings.Contains(line, "1 other instance") {
		t.Errorf("with b open, status line = %q", line)
	}

	b.git.store.Close()
	a = poll(a)
	if line := stripANSI(a.normalStatusLine()); strings.Contains(line, "other instance") {
		t.Errorf("after b closed, status line = %q", line)
	}
}
