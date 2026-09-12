package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sburnett/orgtd/internal/workspace"
)

// gitRepoWithNestedWorkspace creates a git repository rooted at a temp
// dir, with its one tracked org file inside a subdirectory ("orgs/") —
// so the *workspace* (which only ever points at that subdirectory) is
// nested inside a larger repository, not the root of it. Mirrors, for
// example, this very project's testdata/orgdir sitting inside the
// orgtd repo itself.
func gitRepoWithNestedWorkspace(t *testing.T, committed, dirty string) *workspace.Workspace {
	t.Helper()
	root := t.TempDir()
	runGit(t, root, "init", "-q")
	runGit(t, root, "config", "user.email", "test@example.com")
	runGit(t, root, "config", "user.name", "Test")

	sub := filepath.Join(root, "orgs")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	path := filepath.Join(sub, "todo.org")
	if err := os.WriteFile(path, []byte(committed), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	runGit(t, root, "add", "orgs/todo.org")
	runGit(t, root, "commit", "-q", "-m", "initial")

	if dirty != "" {
		if err := os.WriteFile(path, []byte(dirty), 0644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}

	ws, err := workspace.Load(sub)
	if err != nil {
		t.Fatalf("workspace.Load: %v", err)
	}
	return ws
}

func TestGitRepoRootRefusalEmptyWhenWorkspaceIsTheRepoRoot(t *testing.T) {
	ws := gitRepoFixture(t, "todo.org", "* TODO Something\n", "")
	m := New(ws)

	if reason := m.gitRepoRootRefusal(); reason != "" {
		t.Errorf("gitRepoRootRefusal() = %q, want empty — the workspace is the repo root", reason)
	}
}

func TestGitRepoRootRefusalWhenWorkspaceIsNestedInALargerRepo(t *testing.T) {
	ws := gitRepoWithNestedWorkspace(t, "* TODO Something\n", "")
	m := New(ws)

	reason := m.gitRepoRootRefusal()
	if reason == "" {
		t.Fatal("gitRepoRootRefusal() = \"\", want a refusal — the workspace is only a subdirectory of the repo")
	}
	if !strings.Contains(reason, "root of its git repository") {
		t.Errorf("gitRepoRootRefusal() = %q, want it to explain the workspace isn't the repo root", reason)
	}
}

func TestGitRepoRootRefusalWhenNotAGitRepositoryAtAll(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "todo.org"), []byte("* TODO Something\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	ws, err := workspace.Load(dir)
	if err != nil {
		t.Fatalf("workspace.Load: %v", err)
	}
	m := New(ws)

	reason := m.gitRepoRootRefusal()
	if !strings.Contains(reason, "isn't inside a git repository") {
		t.Errorf("gitRepoRootRefusal() = %q, want it to explain there's no git repository at all", reason)
	}
}

func TestDiffStillWorksWhenWorkspaceIsNestedInALargerRepo(t *testing.T) {
	ws := gitRepoWithNestedWorkspace(t, "* TODO Old title\n", "* TODO New title\n")
	m := New(ws)

	m.showDiff()

	if m.mode != normalMode {
		t.Fatalf("mode after :diff nested in a larger repo = %v, want normalMode (diff itself is read-only, never blocked)", m.mode)
	}
	if m.view != diffView {
		t.Fatalf("view = %v, want diffView", m.view)
	}
	var sawChange bool
	for _, r := range m.rows {
		if strings.Contains(r.text, "New title") {
			sawChange = true
		}
	}
	if !sawChange {
		t.Errorf("rows = %#v, want the tracked file's change to still show up", m.rows)
	}
}

func TestDiffDoesNotOfferToAddUntrackedFilesWhenNestedInALargerRepo(t *testing.T) {
	ws := gitRepoWithNestedWorkspace(t, "* TODO Something\n", "")
	newPath := filepath.Join(ws.Dir, "new.org")
	if err := os.WriteFile(newPath, []byte("* TODO Brand new\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	ws, err := workspace.Load(ws.Dir)
	if err != nil {
		t.Fatalf("workspace.Load: %v", err)
	}
	m := New(ws)

	m.showDiff()

	if m.mode == confirmMode {
		t.Fatalf("mode = confirmMode, want :diff never to offer adding a file it can't safely git add")
	}
	if m.view != diffView {
		t.Fatalf("view = %v, want diffView — the diff itself should still run", m.view)
	}
}

func TestCommitRefusesWhenNestedInALargerRepo(t *testing.T) {
	ws := gitRepoWithNestedWorkspace(t, "* TODO Old title\n", "* TODO New title\n")
	m := New(ws)
	m.switchToView(diffView)

	m.startCommit()

	if m.mode == commitMessageMode {
		t.Fatalf("mode = commitMessageMode, want :commit to refuse rather than prompt")
	}
	if !strings.Contains(m.message, "root of its git repository") {
		t.Errorf("message = %q, want it to explain the workspace isn't the repo root", m.message)
	}
}

func TestCommitRefusesWhenNotAGitRepositoryAtAll(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "todo.org"), []byte("* TODO Something\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	ws, err := workspace.Load(dir)
	if err != nil {
		t.Fatalf("workspace.Load: %v", err)
	}
	m := New(ws)
	m.switchToView(diffView)

	m.startCommit()

	if m.mode == commitMessageMode {
		t.Fatalf("mode = commitMessageMode, want :commit to refuse rather than prompt")
	}
	if !strings.Contains(m.message, "isn't inside a git repository") {
		t.Errorf("message = %q, want it to explain there's no git repository at all", m.message)
	}
}

func TestMutatingGitHelpersRefuseWhenNotAtRepoRoot(t *testing.T) {
	ws := gitRepoWithNestedWorkspace(t, "* TODO Old title\n", "* TODO New title\n")
	m := New(ws)

	if _, err := m.gitAdd([]string{"todo.org"}); err == nil || !strings.Contains(err.Error(), "root of its git repository") {
		t.Errorf("gitAdd err = %v, want a repo-root refusal", err)
	}
	if _, err := m.runGitCommit("a message"); err == nil || !strings.Contains(err.Error(), "root of its git repository") {
		t.Errorf("runGitCommit err = %v, want a repo-root refusal", err)
	}
	if _, err := m.runGitPush(); err == nil || !strings.Contains(err.Error(), "root of its git repository") {
		t.Errorf("runGitPush err = %v, want a repo-root refusal", err)
	}

	for _, e := range m.execLog.snapshot() {
		if e.kind == execLogStart {
			for _, mutating := range []string{" add ", " commit ", " push"} {
				if strings.Contains(e.text, mutating) {
					t.Errorf("execLog entry = %q, a mutating git command should never actually run", e.text)
				}
			}
		}
	}
}
