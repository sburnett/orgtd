package gitrepo

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sburnett/orgtd/internal/execlog"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// newRepo creates a git repository at a temp dir with one committed file
// (todo.org) and returns its directory.
func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	git(t, dir, "config", "user.email", "test@example.com")
	git(t, dir, "config", "user.name", "Test")
	write(t, filepath.Join(dir, "todo.org"), "* TODO Old\n")
	git(t, dir, "add", "todo.org")
	git(t, dir, "commit", "-q", "-m", "initial")
	return dir
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func TestRootRefusalEmptyAtRepoRoot(t *testing.T) {
	r := Repo{Dir: newRepo(t)}
	if reason := r.RootRefusal(); reason != "" {
		t.Errorf("RootRefusal() = %q, want empty at the repo root", reason)
	}
	if err := r.RequireRoot(); err != nil {
		t.Errorf("RequireRoot() = %v, want nil", err)
	}
}

func TestRootRefusalWhenNestedInALargerRepo(t *testing.T) {
	root := newRepo(t)
	sub := filepath.Join(root, "orgs")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	r := Repo{Dir: sub}
	if reason := r.RootRefusal(); !strings.Contains(reason, "root of its git repository") {
		t.Errorf("RootRefusal() = %q, want it to explain Dir isn't the repo root", reason)
	}
}

func TestRootRefusalOutsideAnyRepo(t *testing.T) {
	r := Repo{Dir: t.TempDir()}
	if reason := r.RootRefusal(); !strings.Contains(reason, "isn't inside a git repository") {
		t.Errorf("RootRefusal() = %q, want it to say there's no git repository", reason)
	}
	if r.Root() != "" {
		t.Errorf("Root() = %q, want empty outside a repository", r.Root())
	}
}

func TestMutatingCommandsRefuseOutsideRepoRootAndNeverRun(t *testing.T) {
	root := newRepo(t)
	sub := filepath.Join(root, "orgs")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	log := &execlog.Log{}
	r := Repo{Dir: sub, Log: log}

	if _, err := r.Add([]string{"x.org"}); err == nil {
		t.Error("Add: err = nil, want a refusal")
	}
	if _, err := r.Commit([]string{"x.org"}, "msg"); err == nil {
		t.Error("Commit: err = nil, want a refusal")
	}
	if _, err := r.Push(); err == nil {
		t.Error("Push: err = nil, want a refusal")
	}
	for _, e := range log.Snapshot() {
		if e.Kind != execlog.Start {
			continue
		}
		for _, mutating := range []string{" add ", " commit ", " push"} {
			if strings.Contains(e.Text, mutating) {
				t.Errorf("logged %q, a mutating git command should never have run", e.Text)
			}
		}
	}
}

func TestDiffShowsWorkingTreeChangesAgainstHEAD(t *testing.T) {
	dir := newRepo(t)
	write(t, filepath.Join(dir, "todo.org"), "* TODO New\n")
	out, err := Repo{Dir: dir}.Diff([]string{filepath.Join(dir, "todo.org")})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !strings.Contains(out, "-* TODO Old") || !strings.Contains(out, "+* TODO New") {
		t.Errorf("Diff = %q, want it to show the changed line", out)
	}
}

func TestUntrackedAndAdd(t *testing.T) {
	dir := newRepo(t)
	newFile := filepath.Join(dir, "new.org")
	write(t, newFile, "* TODO New\n")
	r := Repo{Dir: dir}
	paths := []string{filepath.Join(dir, "todo.org"), newFile}

	got, err := r.Untracked(paths)
	if err != nil {
		t.Fatalf("Untracked: %v", err)
	}
	if len(got) != 1 || !strings.HasSuffix(got[0], "new.org") {
		t.Fatalf("Untracked = %v, want just new.org", got)
	}
	if _, err := r.Add(got); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if got, _ := r.Untracked(paths); len(got) != 0 {
		t.Errorf("Untracked after Add = %v, want none", got)
	}
	if got, err := r.Untracked(nil); got != nil || err != nil {
		t.Errorf("Untracked(nil) = (%v, %v), want (nil, nil)", got, err)
	}
}

func TestCommitNothingToCommitIsRecognizable(t *testing.T) {
	dir := newRepo(t)
	stdout, err := Repo{Dir: dir}.Commit([]string{filepath.Join(dir, "todo.org")}, "noop")
	if err == nil {
		t.Fatal("Commit with nothing changed: err = nil, want git's failure")
	}
	if !IsNothingToCommit(stdout) {
		t.Errorf("IsNothingToCommit(%q) = false, want true", stdout)
	}
	if IsNothingToCommit("error: pathspec did not match") {
		t.Error("IsNothingToCommit true for an unrelated failure")
	}
}

func TestErrorTextPrefersGitStderr(t *testing.T) {
	// A repository with no commits has no HEAD to diff against.
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	_, err := Repo{Dir: dir}.Diff([]string{"x"})
	if err == nil {
		t.Fatal("Diff with no HEAD: err = nil, want failure")
	}
	text := ErrorText(err)
	if !strings.Contains(text, "HEAD") || text == err.Error() {
		t.Errorf("ErrorText = %q (raw %q), want git's own stderr mentioning HEAD", text, err.Error())
	}
}
