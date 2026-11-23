package gitstore

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"

	"github.com/sburnett/orgtd/internal/org"
)

// branchFrom commits changes (nil content: delete) on top of parent and
// points refs/heads/<name> at the result, as the iOS app's push does.
func branchFrom(t *testing.T, dir, name string, parent plumbing.Hash, changes map[string]string) plumbing.Hash {
	t.Helper()
	c, err := pushBranch(dir, name, parent, changes)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// pushBranch is branchFrom for goroutines other than the test's own,
// which mustn't call t.Fatal.
func pushBranch(dir, name string, parent plumbing.Hash, changes map[string]string) (plumbing.Hash, error) {
	repo, err := git.PlainOpen(dir)
	if err != nil {
		return plumbing.ZeroHash, err
	}
	tree, err := treeOf(repo, parent)
	if err != nil {
		return plumbing.ZeroHash, err
	}
	edits := map[string][]byte{}
	for p, c := range changes {
		edits[p] = []byte(c)
	}
	if tree, err = editTree(repo, tree, edits); err != nil {
		return plumbing.ZeroHash, err
	}
	c, err := commit(repo, tree, parent, "phone change")
	if err != nil {
		return plumbing.ZeroHash, err
	}
	if err := repo.Storer.SetReference(plumbing.NewHashReference(plumbing.NewBranchReferenceName(name), c)); err != nil {
		return plumbing.ZeroHash, err
	}
	return c, nil
}

func applyBranch(t *testing.T, s *Store) BranchResult {
	t.Helper()
	res, err := s.ApplyBranch("mobile")
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestApplyBranchMovesMainToTheBranch(t *testing.T) {
	dir := newRepo(t, map[string]string{"inbox.org": "* a\n", "todo.org": "* t\n"})
	s := open(t, dir)
	tip := branchFrom(t, dir, "mobile", head(t, s), map[string]string{"inbox.org": "* a\n* from phone\n"})

	res := applyBranch(t, s)
	if !res.Moved || res.Head != tip || !slices.Equal(res.Changed, []string{"inbox.org"}) {
		t.Fatalf("FastForward = %+v, want a move to %s changing inbox.org", res, tip)
	}
	if got := head(t, s); got != tip {
		t.Errorf("main = %s, want the branch tip %s (no new commit)", got, tip)
	}
	if got := readDisk(t, dir, "inbox.org"); got != "* a\n* from phone\n" {
		t.Errorf("disk = %q", got)
	}
	if string(res.Files["todo.org"]) != "* t\n" {
		t.Errorf("Files = %v, want every managed file", res.Files)
	}
	gitCheck(t, dir)

	if again := applyBranch(t, s); again.Moved {
		t.Errorf("second FastForward moved again: %+v", again)
	}
}

func TestApplyBranchMergesWhenMainMovedOn(t *testing.T) {
	dir := newRepo(t, map[string]string{"inbox.org": "* a\n", "todo.org": "* t\n"})
	s := open(t, dir)
	base := head(t, s)
	tip := branchFrom(t, dir, "mobile", base, map[string]string{"inbox.org": "* a\n* from phone\n"})
	if _, err := s.Save(base, []Edit{edit("todo.org", "* t\n", "* T\n")}, "desktop"); err != nil {
		t.Fatal(err)
	}
	moved := head(t, s)

	res := applyBranch(t, s)
	if !res.Moved || !res.Merged || !slices.Equal(res.Changed, []string{"inbox.org"}) {
		t.Fatalf("ApplyBranch = %+v, want a merge changing inbox.org", res)
	}
	assertMerge(t, dir, res.Head, moved, tip)
	if got := readDisk(t, dir, "inbox.org"); got != "* a\n* from phone\n" {
		t.Errorf("inbox.org = %q", got)
	}
	if got := readDisk(t, dir, "todo.org"); got != "* T\n" {
		t.Errorf("todo.org = %q, want the desktop's save kept", got)
	}
	if string(res.Files["todo.org"]) != "* T\n" || string(res.Files["inbox.org"]) != "* a\n* from phone\n" {
		t.Errorf("Files = %v", res.Files)
	}
	gitCheck(t, dir)
	if again := applyBranch(t, s); again.Moved {
		t.Errorf("second ApplyBranch moved again: %+v", again)
	}
}

// assertMerge checks that c is a merge commit of main and tip, in that
// order, and that main is at c.
func assertMerge(t *testing.T, dir string, c, main, tip plumbing.Hash) {
	t.Helper()
	repo, err := git.PlainOpen(dir)
	if err != nil {
		t.Fatal(err)
	}
	obj, err := repo.CommitObject(c)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(obj.ParentHashes, []plumbing.Hash{main, tip}) {
		t.Errorf("merge parents = %v, want [%s %s]", obj.ParentHashes, main, tip)
	}
	if !strings.HasPrefix(obj.Message, "orgtd: merge mobile") {
		t.Errorf("merge message = %q", obj.Message)
	}
	ref, err := repo.Reference(plumbing.Main, true)
	if err != nil || ref.Hash() != c {
		t.Errorf("main = %v (%v), want the merge %s", ref, err, c)
	}
}

func TestApplyBranchMergesEditsToTheSameFile(t *testing.T) {
	dir := newRepo(t, map[string]string{"todo.org": "* a\n  notes\n* b\n"})
	s := open(t, dir)
	base := head(t, s)
	tip := branchFrom(t, dir, "mobile", base, map[string]string{"todo.org": "* a\n  phone notes\n* b\n"})
	if _, err := s.Save(base, []Edit{edit("todo.org", "* a\n  notes\n* b\n", "* a\n  notes\n* b :tag:\n")}, "desktop"); err != nil {
		t.Fatal(err)
	}
	moved := head(t, s)
	res := applyBranch(t, s)
	if !res.Merged {
		t.Fatalf("ApplyBranch = %+v, want a merge", res)
	}
	assertMerge(t, dir, res.Head, moved, tip)
	if got := readDisk(t, dir, "todo.org"); got != "* a\n  phone notes\n* b :tag:\n" {
		t.Errorf("todo.org = %q, want both sides' edits", got)
	}
	gitCheck(t, dir)
}

// conflicted sets up a phone branch and a desktop save that change the
// same line of todo.org, and returns the store, main and the tip.
func conflicted(t *testing.T) (dir string, s *Store, main, tip plumbing.Hash) {
	t.Helper()
	dir = newRepo(t, map[string]string{"todo.org": "* a\n* b\n", "inbox.org": "* i\n"})
	s = open(t, dir)
	base := head(t, s)
	tip = branchFrom(t, dir, "mobile", base, map[string]string{"todo.org": "* a phone\n* b\n", "inbox.org": "* i\n* from phone\n"})
	if _, err := s.Save(base, []Edit{edit("todo.org", "* a\n* b\n", "* a desktop\n* b\n")}, "desktop"); err != nil {
		t.Fatal(err)
	}
	return dir, s, head(t, s), tip
}

func TestApplyBranchReportsAConflictAndMovesNothing(t *testing.T) {
	dir, s, main, tip := conflicted(t)
	res := applyBranch(t, s)
	c := res.Conflict
	if res.Moved || c == nil || c.Tip != tip || c.Main != main || c.Branch != "mobile" {
		t.Fatalf("ApplyBranch = %+v, want a conflict", res)
	}
	if len(c.Files) != 1 {
		t.Fatalf("conflicted files = %v, want only todo.org", c.Files)
	}
	f := c.Files["todo.org"]
	if f.Count != 1 || string(f.Theirs) != "* a desktop\n* b\n" || !strings.Contains(string(f.Merged), "<<<<<<< mobile\n* a phone\n") {
		t.Errorf("todo.org conflict = %+v\n%s", f, f.Merged)
	}
	if got := head(t, s); got != main {
		t.Errorf("main moved")
	}
	if got := readDisk(t, dir, "inbox.org"); got != "* i\n" {
		t.Errorf("inbox.org = %q: nothing should be written on a conflict", got)
	}
	if again := applyBranch(t, s); again.Conflict == nil {
		t.Errorf("second ApplyBranch (cached) = %+v, want the conflict still", again)
	}
}

func TestResolveBranchCommitsTheResolution(t *testing.T) {
	dir, s, main, tip := conflicted(t)
	c := applyBranch(t, s).Conflict
	res, err := s.ResolveBranch(c, map[string][]byte{"todo.org": []byte("* a both\n* b\n")})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Moved || !res.Merged {
		t.Fatalf("ResolveBranch = %+v, want a merge", res)
	}
	assertMerge(t, dir, res.Head, main, tip)
	if got := readDisk(t, dir, "todo.org"); got != "* a both\n* b\n" {
		t.Errorf("todo.org = %q", got)
	}
	if got := readDisk(t, dir, "inbox.org"); got != "* i\n* from phone\n" {
		t.Errorf("inbox.org = %q, want the phone's clean change too", got)
	}
	gitCheck(t, dir)
	if again := applyBranch(t, s); again.Moved || again.Conflict != nil {
		t.Errorf("ApplyBranch after resolving = %+v, want nothing", again)
	}
}

func TestResolveBranchMergesWithMainThatMovedSince(t *testing.T) {
	dir, s, _, tip := conflicted(t)
	c := applyBranch(t, s).Conflict
	if _, err := s.Save(c.Main, []Edit{edit("todo.org", "* a desktop\n* b\n", "* a desktop\n* b\n* c\n")}, "desktop again"); err != nil {
		t.Fatal(err)
	}
	moved := head(t, s)
	res, err := s.ResolveBranch(c, map[string][]byte{"todo.org": []byte("* a both\n* b\n")})
	if err != nil || !res.Merged {
		t.Fatalf("ResolveBranch = %+v, %v", res, err)
	}
	assertMerge(t, dir, res.Head, moved, tip)
	if got := readDisk(t, dir, "todo.org"); got != "* a both\n* b\n* c\n" {
		t.Errorf("todo.org = %q", got)
	}

	// A resolution that collides with main's newer change conflicts anew.
	dir, s, _, _ = conflicted(t)
	c = applyBranch(t, s).Conflict
	if _, err := s.Save(c.Main, []Edit{edit("todo.org", "* a desktop\n* b\n", "* a desktop 2\n* b\n")}, "desktop again"); err != nil {
		t.Fatal(err)
	}
	moved = head(t, s)
	res, err = s.ResolveBranch(c, map[string][]byte{"todo.org": []byte("* a both\n* b\n")})
	if err != nil || res.Moved || res.Conflict == nil || res.Conflict.Main != moved {
		t.Fatalf("ResolveBranch = %+v, %v; want a new conflict against %s", res, err, moved)
	}
	if got := readDisk(t, dir, "todo.org"); got != "* a desktop 2\n* b\n" {
		t.Errorf("todo.org = %q, want it untouched", got)
	}
}

func TestResolveBranchRefusesWhenTheBranchMoved(t *testing.T) {
	dir, s, main, _ := conflicted(t)
	c := applyBranch(t, s).Conflict
	branchFrom(t, dir, "mobile", c.Tip, map[string]string{"inbox.org": "* i\n* from phone\n* more\n"})
	if _, err := s.ResolveBranch(c, map[string][]byte{"todo.org": []byte("x\n")}); !errors.Is(err, ErrBranchMoved) {
		t.Errorf("ResolveBranch = %v, want ErrBranchMoved", err)
	}
	if _, err := s.ResolveBranch(&BranchConflict{Branch: "mobile", Tip: c.Tip, Main: c.Main, Files: c.Files}, nil); err == nil {
		t.Errorf("ResolveBranch with no resolution succeeded")
	}
	if got := head(t, s); got != main {
		t.Errorf("main moved")
	}
}

// After a merge, the phone pushes more on top of the merged tip: the
// merged tip is the new merge base, so what was already merged (and
// resolved) doesn't conflict again.
func TestApplyBranchMergesFromTheLastMergedTip(t *testing.T) {
	dir, s, _, tip := conflicted(t)
	c := applyBranch(t, s).Conflict
	if _, err := s.ResolveBranch(c, map[string][]byte{"todo.org": []byte("* a both\n* b\n")}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save(head(t, s), []Edit{edit("inbox.org", "* i\n* from phone\n", "* i desktop\n* from phone\n")}, "desktop"); err != nil {
		t.Fatal(err)
	}
	moved := head(t, s)
	next := branchFrom(t, dir, "mobile", tip, map[string]string{"todo.org": "* a phone\n* b phone\n"})
	res := applyBranch(t, s)
	if !res.Merged || res.Conflict != nil {
		t.Fatalf("ApplyBranch = %+v, want a clean merge", res)
	}
	assertMerge(t, dir, res.Head, moved, next)
	if got := readDisk(t, dir, "todo.org"); got != "* a both\n* b phone\n" {
		t.Errorf("todo.org = %q", got)
	}
	gitCheck(t, dir)
}

func TestApplyBranchMergeBlockedByOutsideEdit(t *testing.T) {
	dir := newRepo(t, map[string]string{"inbox.org": "* a\n", "todo.org": "* t\n"})
	s := open(t, dir)
	base := head(t, s)
	branchFrom(t, dir, "mobile", base, map[string]string{"inbox.org": "* a\n* from phone\n"})
	if _, err := s.Save(base, []Edit{edit("todo.org", "* t\n", "* T\n")}, "desktop"); err != nil {
		t.Fatal(err)
	}
	moved := head(t, s)
	writeDisk(t, dir, "inbox.org", "* a\n* from emacs\n")
	if res := applyBranch(t, s); res.Moved || !slices.Equal(res.Blocked, []string{"inbox.org"}) {
		t.Fatalf("ApplyBranch = %+v, want blocked on inbox.org", res)
	}
	if got := head(t, s); got != moved {
		t.Errorf("main moved")
	}
}

func TestApplyBranchWithNoBranchOrNothingNew(t *testing.T) {
	dir := newRepo(t, map[string]string{"inbox.org": "* a\n"})
	s := open(t, dir)
	if res := applyBranch(t, s); res.Moved {
		t.Errorf("no branch: %+v", res)
	}
	// A branch main already has (the phone hasn't synced since).
	branchFrom(t, dir, "mobile", head(t, s), map[string]string{"inbox.org": "* a\n* b\n"})
	applyBranch(t, s)
	if res := applyBranch(t, s); res.Moved {
		t.Errorf("already merged: %+v", res)
	}
	// Main moved on from a merged branch: nothing to report.
	if _, err := s.Save(head(t, s), []Edit{edit("inbox.org", "* a\n* b\n", "* A\n* b\n")}, "desktop"); err != nil {
		t.Fatal(err)
	}
	if res := applyBranch(t, s); res.Moved || res.Conflict != nil || res.Blocked != nil || res.Unmanaged != nil {
		t.Errorf("merged branch, main moved on: %+v, want nothing", res)
	}
}

func TestApplyBranchBlockedByOutsideEditToAChangedFile(t *testing.T) {
	dir := newRepo(t, map[string]string{"inbox.org": "* a\n", "todo.org": "* t\n"})
	s := open(t, dir)
	base := head(t, s)
	branchFrom(t, dir, "mobile", base, map[string]string{"inbox.org": "* a\n* from phone\n"})
	writeDisk(t, dir, "inbox.org", "* a\n* from emacs\n")

	res := applyBranch(t, s)
	if res.Moved || !slices.Equal(res.Blocked, []string{"inbox.org"}) {
		t.Fatalf("FastForward = %+v, want blocked on inbox.org", res)
	}
	if got := readDisk(t, dir, "inbox.org"); got != "* a\n* from emacs\n" {
		t.Errorf("outside edit overwritten: %q", got)
	}
	if got := head(t, s); got != base {
		t.Errorf("main moved")
	}
}

func TestApplyBranchKeepsOutsideEditsToOtherFiles(t *testing.T) {
	dir := newRepo(t, map[string]string{"inbox.org": "* a\n", "todo.org": "* t\n"})
	s := open(t, dir)
	tip := branchFrom(t, dir, "mobile", head(t, s), map[string]string{"inbox.org": "* a\n* from phone\n"})
	writeDisk(t, dir, "todo.org", "* t\n* from emacs\n")

	if res := applyBranch(t, s); !res.Moved || res.Head != tip {
		t.Fatalf("FastForward = %+v", res)
	}
	if got := readDisk(t, dir, "todo.org"); got != "* t\n* from emacs\n" {
		t.Errorf("outside edit lost: %q", got)
	}
	if _, err := exec.LookPath("git"); err == nil {
		cmd := exec.Command("git", "status", "--porcelain", "--untracked-files=no")
		cmd.Dir = dir
		out, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.TrimSpace(string(out)); got != "M todo.org" {
			t.Errorf("git status = %q, want only the outside edit", got)
		}
	}
	// And it's still committed as an outside edit later.
	if res, err := s.CommitOutsideEdits(); err != nil || string(res.Files["todo.org"]) != "* t\n* from emacs\n" {
		t.Fatalf("CommitOutsideEdits = %+v, %v", res, err)
	}
	gitCheck(t, dir)
}

func TestApplyBranchLeavesUnmanagedChangesAlone(t *testing.T) {
	dir := newRepo(t, map[string]string{"inbox.org": "* a\n"})
	s := open(t, dir)
	base := head(t, s)
	branchFrom(t, dir, "mobile", base, map[string]string{"inbox.org": "* a\n* b\n", "notes.txt": "hi\n"})
	if res := applyBranch(t, s); res.Moved || !slices.Equal(res.Unmanaged, []string{"notes.txt"}) {
		t.Fatalf("FastForward = %+v, want Unmanaged notes.txt", res)
	}
	if got := head(t, s); got != base {
		t.Errorf("main moved")
	}
	// What the status line tells the user to run.
	if _, err := exec.LookPath("git"); err == nil {
		cmd := exec.Command("git", "merge", "--ff-only", "mobile")
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git merge --ff-only mobile: %v\n%s", err, out)
		}
		if res := applyBranch(t, s); res.Moved || res.Unmanaged != nil {
			t.Errorf("after the merge: %+v, want nothing", res)
		}
		gitCheck(t, dir)
	}
}

func TestApplyBranchRefusesOtherBranch(t *testing.T) {
	dir := newRepo(t, map[string]string{"inbox.org": "* a\n"})
	s, err := Open(dir, "trunk", nil, org.DefaultKeywords())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.ApplyBranch("mobile"); !errors.Is(err, ErrWrongBranch) {
		t.Errorf("FastForward = %v, want ErrWrongBranch", err)
	}
}

func TestApplyBranchCrashAtEveryStep(t *testing.T) {
	for _, step := range []string{"files-written", "index-written", "ref-moved"} {
		t.Run(step, func(t *testing.T) {
			dir := newRepo(t, map[string]string{"inbox.org": "* a\n"})
			crashing := open(t, dir)
			tip := branchFrom(t, dir, "mobile", head(t, crashing), map[string]string{"inbox.org": "* a\n* b\n"})
			crashing.failAt = func(s string) error {
				if s == step {
					return errors.New("boom")
				}
				return nil
			}
			if _, err := crashing.ApplyBranch("mobile"); !errors.Is(err, errCrash) {
				t.Fatalf("FastForward = %v, want the simulated crash", err)
			}
			crashing.Close()
			os.Remove(filepath.Join(dir, ".git", "index.lock"))

			// The next instance's poll finishes the job.
			next := open(t, dir)
			next.ApplyBranch("mobile")
			if got := head(t, next); got != tip {
				t.Errorf("main = %s, want %s", got, tip)
			}
			if got := readDisk(t, dir, "inbox.org"); got != "* a\n* b\n" {
				t.Errorf("disk = %q", got)
			}
			// Nothing left over to commit as an outside edit.
			if res, err := next.CommitOutsideEdits(); err != nil || res.Head != tip {
				t.Errorf("CommitOutsideEdits = %+v, %v; want main left at %s", res, err, tip)
			}
			gitCheck(t, dir)
		})
	}
}

func TestApplyBranchMergeCrashAtEveryStep(t *testing.T) {
	for _, step := range []string{"files-written", "index-written", "ref-moved"} {
		t.Run(step, func(t *testing.T) {
			dir := newRepo(t, map[string]string{"inbox.org": "* a\n", "todo.org": "* t\n"})
			crashing := open(t, dir)
			base := head(t, crashing)
			tip := branchFrom(t, dir, "mobile", base, map[string]string{"inbox.org": "* a\n* b\n"})
			if _, err := crashing.Save(base, []Edit{edit("todo.org", "* t\n", "* T\n")}, "desktop"); err != nil {
				t.Fatal(err)
			}
			crashing.failAt = func(s string) error {
				if s == step {
					return errors.New("boom")
				}
				return nil
			}
			if _, err := crashing.ApplyBranch("mobile"); !errors.Is(err, errCrash) {
				t.Fatalf("ApplyBranch = %v, want the simulated crash", err)
			}
			crashing.Close()
			os.Remove(filepath.Join(dir, ".git", "index.lock"))

			// The next instance's poll finishes the job: the phone's tip
			// ends up on main either way.
			next := open(t, dir)
			next.ApplyBranch("mobile")
			repo, err := git.PlainOpen(dir)
			if err != nil {
				t.Fatal(err)
			}
			if ok, err := isAncestor(repo, tip, head(t, next)); err != nil || !ok {
				t.Errorf("tip %s not on main %s (%v)", tip, head(t, next), err)
			}
			if got := readDisk(t, dir, "inbox.org"); got != "* a\n* b\n" {
				t.Errorf("disk = %q", got)
			}
			if res, err := next.CommitOutsideEdits(); err != nil || res.Head != head(t, next) {
				t.Errorf("CommitOutsideEdits = %+v, %v; want nothing left to commit", res, err)
			}
			gitCheck(t, dir)
		})
	}
}

func TestMergeLockExcludesWithinProcess(t *testing.T) {
	s := open(t, newRepo(t, map[string]string{"inbox.org": "* a\n"}))
	if ok, err := s.tryLockMerge(); !ok || err != nil {
		t.Fatalf("first tryLockMerge = %v, %v", ok, err)
	}
	if ok, err := s.tryLockMerge(); ok || err != nil {
		t.Fatalf("second tryLockMerge = %v, %v; want false: the lock is held", ok, err)
	}
	s.unlockMerge()
	if ok, err := s.tryLockMerge(); !ok || err != nil {
		t.Fatalf("tryLockMerge after unlock = %v, %v", ok, err)
	}
	s.unlockMerge()
}

// TestPollWhileSaving uses one Store from two goroutines, as the UI does
// with polling off its own goroutine: one saves, the other polls (the
// phone's branch keeps moving), and neither may trip over the other.
func TestPollWhileSaving(t *testing.T) {
	dir := newRepo(t, map[string]string{"a.org": "", "b.org": ""})
	s := open(t, dir)
	const n = 15
	saved := make(chan error, 1)
	go func() {
		base, content := head(t, s), ""
		for i := range n {
			next := content + "* a " + string(rune('a'+i)) + "\n"
			res, err := s.Save(base, []Edit{edit("a.org", content, next)}, "save a.org")
			if err == nil && res.Conflicts != nil {
				err = errors.New("unexpected conflict")
			}
			if err != nil {
				saved <- err
				return
			}
			base, content = res.Head, string(res.Written["a.org"])
		}
		saved <- nil
	}()

	tip, phone := head(t, s), ""
	for i := 0; ; i++ {
		select {
		case err := <-saved:
			if err != nil {
				t.Fatalf("Save: %v", err)
			}
			saved = nil
		default:
		}
		if saved == nil {
			break
		}
		phone += "* b " + string(rune('a'+i%26)) + "\n"
		var err error
		if tip, err = pushBranch(dir, "mobile", tip, map[string]string{"b.org": phone}); err != nil {
			t.Fatal(err)
		}
		if res, err := s.ApplyBranch("mobile"); err != nil || res.Conflict != nil || len(res.Blocked) > 0 {
			t.Fatalf("ApplyBranch = %+v, %v", res, err)
		}
		if _, err := s.OtherInstances(); err != nil {
			t.Fatal(err)
		}
		if h, err := s.Head(); err != nil {
			t.Fatal(err)
		} else if _, err := s.Snapshot(h); err != nil {
			t.Fatal(err)
		}
	}
	// The last push may have found the lock busy; the next poll brings it in.
	applyBranch(t, s)

	if got := strings.Count(readDisk(t, dir, "a.org"), "\n"); got != n {
		t.Errorf("a.org has %d lines, want %d", got, n)
	}
	if got := readDisk(t, dir, "b.org"); got != phone {
		t.Errorf("b.org = %q, want the phone's last push %q", got, phone)
	}
	gitCheck(t, dir)
}
