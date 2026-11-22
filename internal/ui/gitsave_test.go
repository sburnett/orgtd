package ui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/sburnett/orgtd/internal/gitstore"
	"github.com/sburnett/orgtd/internal/gitsync"

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

func TestSaveCommitMessageDescribesTheChange(t *testing.T) {
	a, _, _ := twoInstances(t, map[string]string{"todo.org": "* TODO First\n* TODO Last\n"})
	a.cursor = findRow(t, a, "Last")
	a = setStatus(a, "d")
	if msg, ok := a.writeAllResult(false); !ok {
		t.Fatalf(":w = %q", msg)
	}
	hist, err := a.git.store.History(1)
	if err != nil || len(hist) != 1 || hist[0] != `orgtd: todo.org: mark DONE "Last"` {
		t.Errorf("history = %q, %v", hist, err)
	}
}

// pushMobile commits content for the top-level file name on top of the
// main branch and points the "mobile" branch at it, as the iOS app's
// push does.
func pushMobile(t *testing.T, dir, name, content string) plumbing.Hash {
	t.Helper()
	return pushMobileOn(t, dir, mainHead(t, dir), name, content)
}

// mainHead is where the main branch is.
func mainHead(t *testing.T, dir string) plumbing.Hash {
	t.Helper()
	repo, err := git.PlainOpen(dir)
	if err != nil {
		t.Fatal(err)
	}
	main, err := repo.Reference(plumbing.NewBranchReferenceName("main"), true)
	if err != nil {
		t.Fatal(err)
	}
	return main.Hash()
}

// pushMobileOn is pushMobile on top of commit on rather than the main
// branch: the phone's changes made before main moved on.
func pushMobileOn(t *testing.T, dir string, on plumbing.Hash, name, content string) plumbing.Hash {
	t.Helper()
	repo, err := git.PlainOpen(dir)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := repo.CommitObject(on)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := parent.Tree()
	if err != nil {
		t.Fatal(err)
	}
	store := func(o interface {
		Encode(plumbing.EncodedObject) error
	}) plumbing.Hash {
		obj := repo.Storer.NewEncodedObject()
		if err := o.Encode(obj); err != nil {
			t.Fatal(err)
		}
		h, err := repo.Storer.SetEncodedObject(obj)
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	blob := repo.Storer.NewEncodedObject()
	blob.SetType(plumbing.BlobObject)
	w, _ := blob.Writer()
	w.Write([]byte(content))
	w.Close()
	blobHash, err := repo.Storer.SetEncodedObject(blob)
	if err != nil {
		t.Fatal(err)
	}
	entries := []object.TreeEntry{}
	for _, e := range tree.Entries {
		if e.Name != name {
			entries = append(entries, e)
		}
	}
	entries = append(entries, object.TreeEntry{Name: name, Mode: filemode.Regular, Hash: blobHash})
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	sig := object.Signature{Name: "phone", Email: "phone@example.com", When: time.Now()}
	c := &object.Commit{
		Author: sig, Committer: sig, Message: "Capture",
		TreeHash:     store(&object.Tree{Entries: entries}),
		ParentHashes: []plumbing.Hash{parent.Hash},
	}
	ref := plumbing.NewHashReference(plumbing.NewBranchReferenceName("mobile"), store(c))
	if err := repo.Storer.SetReference(ref); err != nil {
		t.Fatal(err)
	}
	return ref.Hash()
}

func TestMobileBranchMatchesThePhones(t *testing.T) {
	if mobileBranch != gitsync.MobileBranch {
		t.Errorf("mobileBranch = %q, but the phone pushes to %q", mobileBranch, gitsync.MobileBranch)
	}
}

func TestPollingAppliesThePhonesBranch(t *testing.T) {
	a, _, dir := twoInstances(t, map[string]string{"inbox.org": "* TODO Old\n"})
	pushMobile(t, dir, "inbox.org", "* TODO Old\n* TODO From phone\n")

	a = poll(a)
	if !strings.Contains(a.message, "Applied changes from mobile to inbox.org") {
		t.Errorf("message = %q", a.message)
	}
	findRow(t, a, "From phone")
	data, _ := os.ReadFile(filepath.Join(dir, "inbox.org"))
	if string(data) != "* TODO Old\n* TODO From phone\n" {
		t.Errorf("disk = %q", data)
	}
}

func TestPollingShowsWhatBlocksThePhonesBranch(t *testing.T) {
	a, _, dir := twoInstances(t, map[string]string{"inbox.org": "* TODO Old\n"})
	pushMobile(t, dir, "inbox.org", "* TODO Old\n* TODO From phone\n")
	os.WriteFile(filepath.Join(dir, "inbox.org"), []byte("* TODO Old\n* TODO From emacs\n"), 0o644)

	a = poll(a)
	if got := a.gitStatus(); got != "mobile waits on outside edits to inbox.org — :w" {
		t.Errorf("gitStatus = %q", got)
	}
}

func TestPollingMergesThePhonesBranchWhenMainMovedOn(t *testing.T) {
	a, _, dir := twoInstances(t, map[string]string{"inbox.org": "* TODO Old\n", "todo.org": "* TODO Task\n"})
	pushMobile(t, dir, "inbox.org", "* TODO Old\n* TODO From phone\n")
	// Main moves on before the poll.
	a.cursor = findRow(t, a, "Task")
	a = setStatus(a, "n")
	if msg, ok := a.writeAllResult(false); !ok {
		t.Fatalf(":w = %q", msg)
	}

	a = poll(a)
	if !strings.Contains(a.message, "Merged changes from mobile into inbox.org") {
		t.Errorf("message = %q", a.message)
	}
	if got := a.gitStatus(); got != "" {
		t.Errorf("gitStatus = %q, want nothing", got)
	}
	findRow(t, a, "From phone")
	if keywordOf(t, a, "Task") != "NEXT" {
		t.Errorf("the desktop's save was lost")
	}
}

// phoneConflict sets up a phone change and a desktop save to the same
// line, and polls a, which finds the conflict.
func phoneConflict(t *testing.T) (a Model, dir string) {
	t.Helper()
	a, _, dir = twoInstances(t, map[string]string{"todo.org": "* TODO Shared\n* TODO Other\n", "inbox.org": "* TODO Old\n"})
	base := mainHead(t, dir)
	a.cursor = findRow(t, a, "Shared")
	a = setStatus(a, "n")
	if msg, ok := a.writeAllResult(false); !ok {
		t.Fatalf(":w = %q", msg)
	}
	pushMobileOn(t, dir, base, "todo.org", "* DONE Shared\n* TODO Other\n")
	a = poll(a)
	if got := a.gitStatus(); got != "phone changes conflict in todo.org — :resolve" {
		t.Fatalf("gitStatus = %q", got)
	}
	return a, dir
}

func TestPollingReportsAConflictWithThePhonesBranch(t *testing.T) {
	a, dir := phoneConflict(t)
	disk, _ := os.ReadFile(filepath.Join(dir, "todo.org"))
	if string(disk) != "* NEXT Shared\n* TODO Other\n" {
		t.Errorf("disk = %q, want it untouched", disk)
	}
	if keywordOf(t, a, "Shared") != "NEXT" {
		t.Errorf("Shared changed in memory")
	}
}

func TestResolveThePhonesBranchWithMergeTool(t *testing.T) {
	a, dir := phoneConflict(t)
	a.cfg.MergeTool = "true"
	if cmd := a.startResolve(); cmd == nil {
		t.Fatalf(":resolve launched nothing: %q", a.message)
	}
	scratch := filepath.Join(a.scratchDir(), "merge-"+a.git.store.ID())
	p := filepath.Join(scratch, "todo.org")
	data, err := os.ReadFile(p)
	if err != nil || !strings.Contains(string(data), "<<<<<<< mobile") || !strings.Contains(string(data), "* DONE Shared") {
		t.Fatalf("conflict file = %q, %v", data, err)
	}
	c := a.git.mobileConflict

	// The tool exits with markers left: nothing changes.
	updated, _ := a.Update(resolveFinishedMsg{dir: scratch, conflict: c.Files, branch: c})
	a = updated.(Model)
	if !strings.Contains(a.message, "still has conflict markers") || a.git.mobileConflict == nil {
		t.Fatalf("message = %q", a.message)
	}

	// Resolved by hand: merged straight away.
	os.WriteFile(p, []byte("* DONE Shared\n* TODO Other\n"), 0o644)
	updated, _ = a.Update(resolveFinishedMsg{dir: scratch, conflict: c.Files, branch: c})
	a = updated.(Model)
	if !strings.Contains(a.message, "Resolved; merged changes from mobile into todo.org") {
		t.Fatalf("message = %q", a.message)
	}
	disk, _ := os.ReadFile(filepath.Join(dir, "todo.org"))
	if string(disk) != "* DONE Shared\n* TODO Other\n" {
		t.Errorf("disk = %q", disk)
	}
	if a.git.mobileConflict != nil || a.gitStatus() != "" || keywordOf(t, a, "Shared") != "DONE" {
		t.Errorf("after resolving: conflict %v, status %q", a.git.mobileConflict, a.gitStatus())
	}
	if _, err := os.Stat(scratch); !os.IsNotExist(err) {
		t.Errorf("scratch folder left behind: %v", err)
	}
	a = poll(a)
	if a.gitStatus() != "" {
		t.Errorf("gitStatus after the next poll = %q", a.gitStatus())
	}
}

func TestMobileNoteForUnmanagedChangesGivesTheCommand(t *testing.T) {
	got := mobileNote(gitstore.BranchResult{Unmanaged: []string{"notes.txt"}})
	if got != "mobile changes notes.txt — run git merge mobile" {
		t.Errorf("mobileNote = %q", got)
	}
}
