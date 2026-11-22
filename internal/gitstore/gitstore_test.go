package gitstore

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/gofrs/flock"
)

// newRepo makes an empty repository on branch main in a temp dir, with
// the given files committed (if any) by a first Store save.
func newRepo(t *testing.T, initial map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if _, err := git.PlainInitWithOptions(dir, &git.PlainInitOptions{
		InitOptions: git.InitOptions{DefaultBranch: plumbing.Main},
	}); err != nil {
		t.Fatal(err)
	}
	if len(initial) > 0 {
		for p, c := range initial {
			writeDisk(t, dir, p, c)
		}
		s := open(t, dir)
		if _, err := s.CommitOutsideEdits(); err != nil {
			t.Fatal(err)
		}
		s.Close()
	}
	return dir
}

func open(t *testing.T, dir string) *Store {
	t.Helper()
	s, err := Open(dir, "main", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func writeDisk(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readDisk(t *testing.T, dir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func head(t *testing.T, s *Store) plumbing.Hash {
	t.Helper()
	h, err := s.Head()
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func edit(path, base, content string) Edit {
	return Edit{Path: path, Base: []byte(base), HasBase: true, Content: []byte(content)}
}

// gitCheck runs real git, when it's installed, to confirm it agrees: the
// repository is sound and the working tree and index match the branch.
func gitCheck(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		return
	}
	run := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	run("fsck", "--strict", "--no-dangling")
	if out := run("status", "--porcelain", "--untracked-files=no"); out != "" {
		t.Errorf("git status --porcelain = %q, want clean", out)
	}
	// And the index itself, not just the stat-refreshed view.
	if out := run("diff", "--cached", "--name-only"); out != "" {
		t.Errorf("index differs from HEAD: %q", out)
	}
}

func TestOpenRefusesNonRepository(t *testing.T) {
	_, err := Open(t.TempDir(), "main", nil)
	if !errors.Is(err, ErrNotRepo) {
		t.Errorf("Open = %v, want ErrNotRepo", err)
	}
}

func TestSaveOnUnbornBranch(t *testing.T) {
	dir := newRepo(t, nil)
	s := open(t, dir)
	res, err := s.Save(plumbing.ZeroHash, []Edit{{Path: "todo.org", Content: []byte("* TODO a\n")}}, "orgtd: save todo.org")
	if err != nil {
		t.Fatal(err)
	}
	if res.Conflicts != nil || res.Head.IsZero() || head(t, s) != res.Head {
		t.Fatalf("Save = %+v", res)
	}
	if got := readDisk(t, dir, "todo.org"); got != "* TODO a\n" {
		t.Errorf("disk = %q", got)
	}
	if string(res.Files["todo.org"]) != "* TODO a\n" {
		t.Errorf("Files = %q", res.Files)
	}
	gitCheck(t, dir)
}

func TestSaveFastForwardAndReferenceDir(t *testing.T) {
	dir := newRepo(t, map[string]string{"todo.org": "* a\n", "notes.txt": "keep me\n"})
	s := open(t, dir)
	base := head(t, s)
	res, err := s.Save(base, []Edit{
		edit("todo.org", "* a\n", "* a\n* b\n"),
		{Path: "reference/wifi.org", Content: []byte("* Wifi\n")},
	}, "orgtd: save")
	if err != nil || res.Conflicts != nil {
		t.Fatalf("Save = %+v, %v", res, err)
	}
	if got := readDisk(t, dir, "reference/wifi.org"); got != "* Wifi\n" {
		t.Errorf("reference file = %q", got)
	}
	if hist, _ := s.History(5); len(hist) != 2 || hist[0] != "orgtd: save" {
		t.Errorf("history = %q", hist)
	}
	snap, _ := s.Snapshot(res.Head)
	if _, ok := snap["notes.txt"]; ok {
		t.Errorf("Snapshot includes an unmanaged file")
	}
	gitCheck(t, dir)
}

func TestTwoInstancesMergeCleanly(t *testing.T) {
	dir := newRepo(t, map[string]string{"todo.org": "* a\n* b\n* c\n* d\n"})
	a, b := open(t, dir), open(t, dir)
	base := head(t, a)

	if _, err := a.Save(base, []Edit{edit("todo.org", "* a\n* b\n* c\n* d\n", "* A\n* b\n* c\n* d\n")}, "from a"); err != nil {
		t.Fatal(err)
	}
	res, err := b.Save(base, []Edit{edit("todo.org", "* a\n* b\n* c\n* d\n", "* a\n* b\n* c\n* D\n")}, "from b")
	if err != nil || res.Conflicts != nil {
		t.Fatalf("Save = %+v, %v", res, err)
	}
	want := "* A\n* b\n* c\n* D\n"
	if got := readDisk(t, dir, "todo.org"); got != want {
		t.Errorf("disk = %q, want %q", got, want)
	}
	if string(res.Written["todo.org"]) != want {
		t.Errorf("Written = %q", res.Written["todo.org"])
	}
	if hist, _ := b.History(5); !slices.Equal(hist[:2], []string{"from b", "from a"}) {
		t.Errorf("history = %q, want linear b on a", hist)
	}
	// History stays linear: one parent each.
	repo, _ := git.PlainOpen(dir)
	c, _ := repo.CommitObject(res.Head)
	if len(c.ParentHashes) != 1 {
		t.Errorf("merged commit has %d parents", len(c.ParentHashes))
	}
	gitCheck(t, dir)
}

func TestConflictLeavesEverythingAlone(t *testing.T) {
	dir := newRepo(t, map[string]string{"todo.org": "* a\n"})
	a, b := open(t, dir), open(t, dir)
	base := head(t, a)
	if _, err := a.Save(base, []Edit{edit("todo.org", "* a\n", "* from a\n")}, "a"); err != nil {
		t.Fatal(err)
	}
	mainBefore := head(t, a)

	res, err := b.Save(base, []Edit{edit("todo.org", "* a\n", "* from b\n")}, "b")
	if err != nil {
		t.Fatal(err)
	}
	c, ok := res.Conflicts["todo.org"]
	if !ok || c.Count != 1 || string(c.Theirs) != "* from a\n" || !strings.Contains(string(c.Merged), "<<<<<<<") {
		t.Fatalf("Conflicts = %+v", res.Conflicts)
	}
	if res.Head != mainBefore || head(t, b) != mainBefore {
		t.Errorf("main moved on a conflict")
	}
	if got := readDisk(t, dir, "todo.org"); got != "* from a\n" {
		t.Errorf("disk = %q, want a's version untouched", got)
	}
	repo, _ := git.PlainOpen(dir)
	if _, err := repo.Reference(b.pendingRef(b.ID()), false); err != nil {
		t.Errorf("pending ref gone after a conflict: %v", err)
	}
	gitCheck(t, dir)

	// Forcing takes b's version whole, and clears the pending ref.
	f := edit("todo.org", "* a\n", "* from b\n")
	f.Force = true
	if _, err := b.Save(base, []Edit{f}, "b again"); err != nil {
		t.Fatal(err)
	}
	if got := readDisk(t, dir, "todo.org"); got != "* from b\n" {
		t.Errorf("disk = %q after force", got)
	}
	if _, err := repo.Reference(b.pendingRef(b.ID()), false); err == nil {
		t.Errorf("pending ref still there after a successful save")
	}
	gitCheck(t, dir)
}

func TestOutsideEditsAreCommittedFirst(t *testing.T) {
	dir := newRepo(t, map[string]string{"todo.org": "* a\n", "inbox.org": "* x\n", "gone.org": "* g\n"})
	s := open(t, dir)
	base := head(t, s)
	writeDisk(t, dir, "inbox.org", "* x\n* captured in emacs\n")
	writeDisk(t, dir, "new.org", "* new\n")
	os.Remove(filepath.Join(dir, "gone.org"))

	res, err := s.Save(base, []Edit{edit("todo.org", "* a\n", "* b\n")}, "save todo")
	if err != nil || res.Conflicts != nil {
		t.Fatalf("Save = %+v, %v", res, err)
	}
	hist, _ := s.History(3)
	if hist[0] != "save todo" || hist[1] != `orgtd: outside edits to gone.org: delete file; inbox.org: add "captured in emacs"; new.org: add "new"` {
		t.Errorf("history = %q", hist)
	}
	if string(res.Files["inbox.org"]) != "* x\n* captured in emacs\n" || string(res.Files["new.org"]) != "* new\n" {
		t.Errorf("Files = %q", res.Files)
	}
	if _, ok := res.Files["gone.org"]; ok {
		t.Errorf("deleted file still in Files")
	}
	gitCheck(t, dir)
}

func TestOutsideEditToTheSavedFileMerges(t *testing.T) {
	dir := newRepo(t, map[string]string{"todo.org": "* a\n* b\n* c\n"})
	s := open(t, dir)
	base := head(t, s)
	writeDisk(t, dir, "todo.org", "* a\n* b\n* c\n* from emacs\n")
	res, err := s.Save(base, []Edit{edit("todo.org", "* a\n* b\n* c\n", "* A\n* b\n* c\n")}, "save")
	if err != nil || res.Conflicts != nil {
		t.Fatalf("Save = %+v, %v", res, err)
	}
	if got := readDisk(t, dir, "todo.org"); got != "* A\n* b\n* c\n* from emacs\n" {
		t.Errorf("disk = %q", got)
	}
	gitCheck(t, dir)
}

func TestCommitOutsideEdits(t *testing.T) {
	dir := newRepo(t, map[string]string{"todo.org": "* a\n"})
	s := open(t, dir)
	before := head(t, s)
	res, err := s.CommitOutsideEdits()
	if err != nil || res.Head != before {
		t.Fatalf("no outside edits: %+v, %v", res, err)
	}
	writeDisk(t, dir, "todo.org", "* b\n")
	writeDisk(t, dir, "readme.txt", "not managed\n")
	res, err = s.CommitOutsideEdits()
	if err != nil || res.Head == before || string(res.Files["todo.org"]) != "* b\n" {
		t.Fatalf("CommitOutsideEdits = %+v, %v", res, err)
	}
	if _, err := exec.LookPath("git"); err == nil {
		cmd := exec.Command("git", "status", "--porcelain")
		cmd.Dir = dir
		out, _ := cmd.Output()
		if string(out) != "?? readme.txt\n" {
			t.Errorf("git status = %q, want only the unmanaged file", out)
		}
	}
}

func TestIgnoredFilesAreWrittenNotCommitted(t *testing.T) {
	dir := newRepo(t, map[string]string{"todo.org": "* a\n"})
	writeDisk(t, dir, ".gitignore", "calendar.org\n")
	s := open(t, dir)
	base := head(t, s)
	res, err := s.Save(base, []Edit{{Path: "calendar.org", Content: []byte("* meeting\n"), Force: true}}, "sync")
	if err != nil {
		t.Fatal(err)
	}
	if res.Head != base {
		t.Errorf("an ignored file made a commit")
	}
	if got := readDisk(t, dir, "calendar.org"); got != "* meeting\n" {
		t.Errorf("disk = %q", got)
	}
	if string(res.Written["calendar.org"]) != "* meeting\n" {
		t.Errorf("Written = %q", res.Written)
	}
	// And it isn't an outside edit either.
	if res, _ := s.CommitOutsideEdits(); res.Head != base {
		t.Errorf("ignored file committed as an outside edit")
	}
}

func TestOutlineIgnoreFilesAreNotManaged(t *testing.T) {
	dir := newRepo(t, nil)
	s, err := Open(dir, "main", []string{"archive*.org"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for rel, want := range map[string]bool{
		"todo.org": true, "reference/x.org": true, "archive2025.org": false,
		"scratch/2026/x.org": false, "notes.txt": false, ".hidden.org": false,
	} {
		if got := s.IsManaged(rel); got != want {
			t.Errorf("IsManaged(%q) = %v, want %v", rel, got, want)
		}
	}
}

func TestSaveRefusesOtherBranch(t *testing.T) {
	dir := newRepo(t, map[string]string{"todo.org": "* a\n"})
	repo, _ := git.PlainOpen(dir)
	repo.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, "refs/heads/feature"))
	s := open(t, dir)
	_, err := s.Save(head(t, s), []Edit{edit("todo.org", "* a\n", "* b\n")}, "x")
	if !errors.Is(err, ErrWrongBranch) || !strings.Contains(err.Error(), "feature") {
		t.Errorf("Save = %v, want ErrWrongBranch naming the branch", err)
	}
}

func TestSaveRefusesWhileIndexLocked(t *testing.T) {
	dir := newRepo(t, map[string]string{"todo.org": "* a\n"})
	s := open(t, dir)
	writeDisk(t, dir, ".git/index.lock", "")
	_, err := s.Save(head(t, s), []Edit{edit("todo.org", "* a\n", "* b\n")}, "x")
	if !errors.Is(err, ErrIndexLocked) {
		t.Errorf("Save = %v, want ErrIndexLocked", err)
	}
	if got := readDisk(t, dir, "todo.org"); got != "* a\n" {
		t.Errorf("disk changed: %q", got)
	}
}

func TestReflogRecordsSaves(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-q", "-b", "main")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	s := open(t, dir)
	if _, err := s.Save(plumbing.ZeroHash, []Edit{{Path: "todo.org", Content: []byte("* a\n")}}, "first save"); err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command("git", "reflog", "main")
	cmd.Dir = dir
	out, _ := cmd.CombinedOutput()
	if !strings.Contains(string(out), "first save") {
		t.Errorf("reflog = %q", out)
	}
	gitCheck(t, dir)
}

// TestCrashAtEveryStep simulates orgtd dying at each step of a save, and
// checks the recovery path (see Orphans) gets the edit into main.
func TestCrashAtEveryStep(t *testing.T) {
	steps := []string{"pinned", "locked", "outside-committed", "merged", "files-written", "index-written", "ref-moved", "cleaned"}
	for _, step := range steps {
		t.Run(step, func(t *testing.T) {
			dir := newRepo(t, map[string]string{"todo.org": "* a\n* b\n* c\n"})
			crashing := open(t, dir)
			other := open(t, dir)
			base := head(t, crashing)
			// Another instance saved meanwhile, so the crashing save merges.
			if _, err := other.Save(base, []Edit{edit("todo.org", "* a\n* b\n* c\n", "* a\n* b\n* C\n")}, "other"); err != nil {
				t.Fatal(err)
			}
			crashing.failAt = func(s string) error {
				if s == step {
					return errors.New("boom")
				}
				return nil
			}
			_, err := crashing.Save(base, []Edit{edit("todo.org", "* a\n* b\n* c\n", "* A\n* b\n* c\n")}, "crashing")
			if !errors.Is(err, errCrash) {
				t.Fatalf("Save = %v, want the simulated crash", err)
			}
			crashing.Close() // the process is gone: the OS drops its locks

			// What a user does on seeing git's message.
			os.Remove(filepath.Join(dir, ".git", "index.lock"))

			recoverer := open(t, dir)
			// Picks up files a crash left on disk as outside edits.
			if _, err := recoverer.CommitOutsideEdits(); err != nil {
				t.Fatal(err)
			}
			orphans, err := recoverer.Orphans()
			if err != nil {
				t.Fatal(err)
			}
			for _, o := range orphans {
				edits, err := recoverer.OrphanEdits(o)
				if err != nil {
					t.Fatal(err)
				}
				res, err := recoverer.Save(head(t, recoverer), edits, "recover")
				if err != nil || res.Conflicts != nil {
					t.Fatalf("recovering: %+v, %v", res, err)
				}
				recoverer.Drop(o)
			}
			want := "* A\n* b\n* C\n"
			if got := readDisk(t, dir, "todo.org"); got != want {
				t.Errorf("disk = %q, want %q", got, want)
			}
			snap, _ := recoverer.Snapshot(head(t, recoverer))
			if string(snap["todo.org"]) != want {
				t.Errorf("main = %q, want %q", snap["todo.org"], want)
			}
			if again, _ := recoverer.Orphans(); len(again) != 0 {
				t.Errorf("orphans left after recovery: %+v", again)
			}
			gitCheck(t, dir)
		})
	}
}

func TestOrphansIgnoresLiveInstances(t *testing.T) {
	dir := newRepo(t, map[string]string{"todo.org": "* a\n"})
	a, b := open(t, dir), open(t, dir)
	base := head(t, a)
	if _, err := a.Save(base, []Edit{edit("todo.org", "* a\n", "* from a\n")}, "a"); err != nil {
		t.Fatal(err)
	}
	// b conflicts and stays alive: its pending ref isn't an orphan.
	if res, _ := b.Save(base, []Edit{edit("todo.org", "* a\n", "* from b\n")}, "b"); res.Conflicts == nil {
		t.Fatal("expected a conflict")
	}
	c := open(t, dir)
	if o, _ := c.Orphans(); len(o) != 0 {
		t.Errorf("Orphans = %+v while b is alive", o)
	}
	b.Close()
	o, _ := c.Orphans()
	if len(o) != 1 || o[0].ID != b.ID() || !slices.Equal(o[0].Paths, []string{"todo.org"}) {
		t.Fatalf("Orphans = %+v after b exited", o)
	}
	// Recovering it conflicts the same way; dropping it removes it.
	if err := c.Drop(o[0]); err != nil {
		t.Fatal(err)
	}
	if o, _ := c.Orphans(); len(o) != 0 {
		t.Errorf("Orphans = %+v after Drop", o)
	}
}

// TestConcurrentSaves runs two instances saving at once, each to its own
// file, and checks every save lands and history stays linear.
func TestConcurrentSaves(t *testing.T) {
	dir := newRepo(t, map[string]string{"a.org": "", "b.org": ""})
	const n = 15
	errs := make(chan error, 2)
	for _, name := range []string{"a.org", "b.org"} {
		s := open(t, dir)
		go func() {
			base, content := head(t, s), ""
			for i := range n {
				next := content + "* " + name + " " + string(rune('a'+i)) + "\n"
				res, err := s.Save(base, []Edit{edit(name, content, next)}, "save "+name)
				if err == nil && res.Conflicts != nil {
					err = errors.New("unexpected conflict")
				}
				if err != nil {
					errs <- err
					return
				}
				base, content = res.Head, string(res.Written[name])
			}
			errs <- nil
		}()
	}
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	s := open(t, dir)
	hist, _ := s.History(100)
	if len(hist) != 2*n+1 {
		t.Errorf("history has %d commits, want %d", len(hist), 2*n+1)
	}
	for _, name := range []string{"a.org", "b.org"} {
		if got := strings.Count(readDisk(t, dir, name), "\n"); got != n {
			t.Errorf("%s has %d lines, want %d", name, got, n)
		}
	}
	gitCheck(t, dir)
}

func TestCloseRemovesInstanceLockFile(t *testing.T) {
	dir := newRepo(t, nil)
	s, err := Open(dir, "main", nil)
	if err != nil {
		t.Fatal(err)
	}
	lock := s.instanceLockPath(s.ID())
	if _, err := os.Stat(lock); err != nil {
		t.Fatalf("no lock file while open: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Errorf("lock file left after Close: %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, ".git", "orgtd", "instances"))
	if len(entries) != 0 {
		t.Errorf("instances/ holds %d files after Close", len(entries))
	}
}

func TestOtherInstances(t *testing.T) {
	dir := newRepo(t, map[string]string{"todo.org": "* a\n"})
	count := func(s *Store) int {
		t.Helper()
		n, err := s.OtherInstances()
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	a := open(t, dir)
	if n := count(a); n != 0 {
		t.Errorf("alone: OtherInstances = %d, want 0", n)
	}
	b, c := open(t, dir), open(t, dir)
	for _, s := range []*Store{a, b, c} {
		if n := count(s); n != 2 {
			t.Errorf("%s: OtherInstances = %d, want 2", s.ID(), n)
		}
	}
	// Another repository's instances don't count.
	open(t, newRepo(t, nil))
	if n := count(a); n != 2 {
		t.Errorf("with another repo open: OtherInstances = %d, want 2", n)
	}

	b.Close()
	if n := count(a); n != 1 {
		t.Errorf("after b closed: OtherInstances = %d, want 1", n)
	}

	// A crashed instance's leftover file isn't counted, and isn't removed
	// either (see OtherInstances): that's left to Orphans.
	stale := a.instanceLockPath("crashed-1-1")
	if err := os.WriteFile(stale, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if n := count(a); n != 1 {
		t.Errorf("with a stale lock file: OtherInstances = %d, want 1", n)
	}
	if _, err := os.Stat(stale); err != nil {
		t.Errorf("stale lock file: %v, want it left alone", err)
	}
}

// TestOpenWaitsOutATestLock checks that Open retries when its new lock
// file is briefly test-locked by another instance (OtherInstances or
// Orphans) instead of failing.
func TestOpenWaitsOutATestLock(t *testing.T) {
	dir := newRepo(t, map[string]string{"todo.org": "* a\n"})
	a := open(t, dir)
	fl := flock.New(a.instanceLockPath("starting"))
	if ok, err := fl.TryLock(); err != nil || !ok {
		t.Fatalf("TryLock = %v, %v", ok, err)
	}
	go func() {
		time.Sleep(3 * instanceLockRetry)
		fl.Unlock()
	}()
	if err := takeInstanceLock(flock.New(a.instanceLockPath("starting"))); err != nil {
		t.Fatalf("takeInstanceLock = %v, want it to wait out the test lock", err)
	}
}
