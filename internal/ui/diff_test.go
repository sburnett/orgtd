package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sburnett/orgtd/internal/workspace"
)

// gitRepoFixture creates a fresh git repository in a temp dir, commits a
// single org file with the given content, then (unless dirty is empty)
// overwrites that file with dirty — leaving an uncommitted change for
// `git diff` to show. Returns a workspace loaded over the repo.
func gitRepoFixture(t *testing.T, name, committed, dirty string) *workspace.Workspace {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test")

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(committed), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	runGit(t, dir, "add", name)
	runGit(t, dir, "commit", "-q", "-m", "initial")

	if dirty != "" {
		if err := os.WriteFile(path, []byte(dirty), 0644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}

	ws, err := workspace.Load(dir)
	if err != nil {
		t.Fatalf("workspace.Load: %v", err)
	}
	return ws
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestDiffCommandSwitchesToDiffView(t *testing.T) {
	ws := gitRepoFixture(t, "todo.org", "* TODO Something\n", "")
	m := New(ws)

	m = sendKey(m, ":")
	m = typeKeys(m, "diff")
	m, _ = sendKeyCmd(m, "enter")

	if m.view != diffView {
		t.Fatalf("view after :diff = %v, want diffView", m.view)
	}
}

func TestDiffShowsChangesToOpenFiles(t *testing.T) {
	ws := gitRepoFixture(t, "todo.org", "* TODO Old title\n", "* TODO New title\n")
	m := New(ws)

	m.showDiff()

	var sawOld, sawNew bool
	for _, r := range m.rows {
		if strings.Contains(r.text, "-* TODO Old title") {
			sawOld = true
		}
		if strings.Contains(r.text, "+* TODO New title") {
			sawNew = true
		}
	}
	if !sawOld || !sawNew {
		t.Errorf("rows = %#v, want lines showing the old and new titles", m.rows)
	}
}

func TestDiffShowsNoChangesPlaceholderWhenClean(t *testing.T) {
	ws := gitRepoFixture(t, "todo.org", "* TODO Something\n", "")
	m := New(ws)

	m.showDiff()

	if len(m.rows) != 1 || !strings.Contains(m.rows[0].text, "No changes") {
		t.Errorf("rows = %#v, want a single 'No changes' placeholder row", m.rows)
	}
}

func TestDiffShowsPlaceholderWhenNoFilesOpen(t *testing.T) {
	dir := t.TempDir()
	ws, err := workspace.Load(dir)
	if err != nil {
		t.Fatalf("workspace.Load: %v", err)
	}
	m := New(ws)

	m.showDiff()

	if len(m.rows) != 1 || !strings.Contains(m.rows[0].text, "No files open") {
		t.Errorf("rows = %#v, want a single 'No files open' placeholder row", m.rows)
	}
	if entries := m.execLog.snapshot(); len(entries) != 0 {
		t.Errorf("execLog = %#v, want git never invoked when there's nothing to diff", entries)
	}
}

func TestDiffShowsErrorWhenNotAGitRepository(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "todo.org"), []byte("* TODO Something\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	ws, err := workspace.Load(dir)
	if err != nil {
		t.Fatalf("workspace.Load: %v", err)
	}
	m := New(ws)

	m.showDiff()

	if len(m.rows) != 1 || !strings.Contains(m.rows[0].text, "git diff failed") {
		t.Errorf("rows = %#v, want a single 'git diff failed' row", m.rows)
	}
}

func TestDiffIsRecordedInTheExecLog(t *testing.T) {
	ws := gitRepoFixture(t, "todo.org", "* TODO Old title\n", "* TODO New title\n")
	m := New(ws)

	m.showDiff()
	m.switchToView(logView)

	var sawStart, sawExit bool
	for _, r := range m.rows {
		if strings.Contains(r.text, "START") && strings.Contains(r.text, "git") && strings.Contains(r.text, "diff") {
			sawStart = true
		}
		if strings.Contains(r.text, "EXIT") && strings.Contains(r.text, "exit code 0") {
			sawExit = true
		}
	}
	if !sawStart || !sawExit {
		t.Errorf("rows = %#v, missing git diff's START/EXIT entries", m.rows)
	}
}

func TestDiffStatusLineShowsPlace(t *testing.T) {
	ws := gitRepoFixture(t, "todo.org", "* TODO Something\n", "")
	m := New(ws)

	m.showDiff()

	lines := m.normalStatusLines()
	if len(lines) == 0 || !strings.Contains(lines[0], "diff") {
		t.Errorf("status line = %#v, want it to mention \"diff\"", lines)
	}
}
