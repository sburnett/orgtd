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
)

// branchFrom commits changes (nil content: delete) on top of parent and
// points refs/heads/<name> at the result, as the iOS app's push does.
func branchFrom(t *testing.T, dir, name string, parent plumbing.Hash, changes map[string]string) plumbing.Hash {
	t.Helper()
	repo, err := git.PlainOpen(dir)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := treeOf(repo, parent)
	if err != nil {
		t.Fatal(err)
	}
	edits := map[string][]byte{}
	for p, c := range changes {
		edits[p] = []byte(c)
	}
	if tree, err = editTree(repo, tree, edits); err != nil {
		t.Fatal(err)
	}
	c, err := commit(repo, tree, parent, "phone change")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Storer.SetReference(plumbing.NewHashReference(plumbing.NewBranchReferenceName(name), c)); err != nil {
		t.Fatal(err)
	}
	return c
}

func fastForward(t *testing.T, s *Store) FastForwardResult {
	t.Helper()
	res, err := s.FastForward("mobile")
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestFastForwardMovesMainToTheBranch(t *testing.T) {
	dir := newRepo(t, map[string]string{"inbox.org": "* a\n", "todo.org": "* t\n"})
	s := open(t, dir)
	tip := branchFrom(t, dir, "mobile", head(t, s), map[string]string{"inbox.org": "* a\n* from phone\n"})

	res := fastForward(t, s)
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

	if again := fastForward(t, s); again.Moved {
		t.Errorf("second FastForward moved again: %+v", again)
	}
}

func TestFastForwardWaitsWhenMainMovedOn(t *testing.T) {
	dir := newRepo(t, map[string]string{"inbox.org": "* a\n", "todo.org": "* t\n"})
	s := open(t, dir)
	base := head(t, s)
	branchFrom(t, dir, "mobile", base, map[string]string{"inbox.org": "* a\n* from phone\n"})
	if _, err := s.Save(base, []Edit{edit("todo.org", "* t\n", "* T\n")}, "desktop"); err != nil {
		t.Fatal(err)
	}
	moved := head(t, s)

	if res := fastForward(t, s); res.Moved || res.Blocked != nil || !res.Behind {
		t.Fatalf("FastForward = %+v, want Behind: main moved on", res)
	}
	if res := fastForward(t, s); !res.Behind {
		t.Errorf("second FastForward (cached) = %+v, want Behind still", res)
	}
	if got := head(t, s); got != moved {
		t.Errorf("main = %s, want it left at %s", got, moved)
	}
	if got := readDisk(t, dir, "inbox.org"); got != "* a\n" {
		t.Errorf("disk = %q, want it untouched", got)
	}

	// The phone's next sync rebuilds the branch on the new main.
	tip := branchFrom(t, dir, "mobile", moved, map[string]string{"inbox.org": "* a\n* from phone\n"})
	if res := fastForward(t, s); !res.Moved || res.Head != tip {
		t.Fatalf("FastForward after the rebuild = %+v", res)
	}
	gitCheck(t, dir)
}

func TestFastForwardWithNoBranchOrNothingNew(t *testing.T) {
	dir := newRepo(t, map[string]string{"inbox.org": "* a\n"})
	s := open(t, dir)
	if res := fastForward(t, s); res.Moved {
		t.Errorf("no branch: %+v", res)
	}
	// A branch main already has (the phone hasn't synced since).
	branchFrom(t, dir, "mobile", head(t, s), map[string]string{"inbox.org": "* a\n* b\n"})
	fastForward(t, s)
	if res := fastForward(t, s); res.Moved {
		t.Errorf("already merged: %+v", res)
	}
	// Main moved on from a merged branch: nothing to report.
	if _, err := s.Save(head(t, s), []Edit{edit("inbox.org", "* a\n* b\n", "* A\n* b\n")}, "desktop"); err != nil {
		t.Fatal(err)
	}
	if res := fastForward(t, s); res.Moved || res.Behind || res.Blocked != nil || res.Unmanaged != nil {
		t.Errorf("merged branch, main moved on: %+v, want nothing", res)
	}
}

func TestFastForwardBlockedByOutsideEditToAChangedFile(t *testing.T) {
	dir := newRepo(t, map[string]string{"inbox.org": "* a\n", "todo.org": "* t\n"})
	s := open(t, dir)
	base := head(t, s)
	branchFrom(t, dir, "mobile", base, map[string]string{"inbox.org": "* a\n* from phone\n"})
	writeDisk(t, dir, "inbox.org", "* a\n* from emacs\n")

	res := fastForward(t, s)
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

func TestFastForwardKeepsOutsideEditsToOtherFiles(t *testing.T) {
	dir := newRepo(t, map[string]string{"inbox.org": "* a\n", "todo.org": "* t\n"})
	s := open(t, dir)
	tip := branchFrom(t, dir, "mobile", head(t, s), map[string]string{"inbox.org": "* a\n* from phone\n"})
	writeDisk(t, dir, "todo.org", "* t\n* from emacs\n")

	if res := fastForward(t, s); !res.Moved || res.Head != tip {
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

func TestFastForwardLeavesUnmanagedChangesAlone(t *testing.T) {
	dir := newRepo(t, map[string]string{"inbox.org": "* a\n"})
	s := open(t, dir)
	base := head(t, s)
	branchFrom(t, dir, "mobile", base, map[string]string{"inbox.org": "* a\n* b\n", "notes.txt": "hi\n"})
	if res := fastForward(t, s); res.Moved || !slices.Equal(res.Unmanaged, []string{"notes.txt"}) {
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
		if res := fastForward(t, s); res.Moved || res.Unmanaged != nil {
			t.Errorf("after the merge: %+v, want nothing", res)
		}
		gitCheck(t, dir)
	}
}

func TestFastForwardRefusesOtherBranch(t *testing.T) {
	dir := newRepo(t, map[string]string{"inbox.org": "* a\n"})
	s, err := Open(dir, "trunk", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.FastForward("mobile"); !errors.Is(err, ErrWrongBranch) {
		t.Errorf("FastForward = %v, want ErrWrongBranch", err)
	}
}

func TestFastForwardCrashAtEveryStep(t *testing.T) {
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
			if _, err := crashing.FastForward("mobile"); !errors.Is(err, errCrash) {
				t.Fatalf("FastForward = %v, want the simulated crash", err)
			}
			crashing.Close()
			os.Remove(filepath.Join(dir, ".git", "index.lock"))

			// The next instance's poll finishes the job.
			next := open(t, dir)
			next.FastForward("mobile")
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
