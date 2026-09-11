package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sburnett/orgtd/internal/workspace"
)

// gitRepoWithUntrackedFile creates a fresh git repo (one committed file,
// so there's a HEAD to diff against) plus a second, brand-new .org file
// that's never been `git add`ed — for testing the "ask to add untracked
// files" confirmation. Returns a workspace loaded over the repo.
func gitRepoWithUntrackedFile(t *testing.T) *workspace.Workspace {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test")

	if err := os.WriteFile(filepath.Join(dir, "todo.org"), []byte("* TODO Something\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	runGit(t, dir, "add", "todo.org")
	runGit(t, dir, "commit", "-q", "-m", "initial")

	if err := os.WriteFile(filepath.Join(dir, "new.org"), []byte("* TODO Brand new\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	ws, err := workspace.Load(dir)
	if err != nil {
		t.Fatalf("workspace.Load: %v", err)
	}
	return ws
}

func TestDiffAsksToAddUntrackedFiles(t *testing.T) {
	ws := gitRepoWithUntrackedFile(t)
	m := New(ws)

	m.showDiff()

	if m.mode != confirmMode {
		t.Fatalf("mode after :diff with an untracked file = %v, want confirmMode", m.mode)
	}
	if !strings.Contains(m.confirmMessage, "new.org") {
		t.Errorf("confirmMessage = %q, want it to name new.org", m.confirmMessage)
	}
	if m.view == diffView {
		t.Errorf("view = %v, want :diff to pause on the question rather than showing the diff yet", m.view)
	}
}

func TestDiffAcceptingAddsFileAndShowsItInTheDiff(t *testing.T) {
	ws := gitRepoWithUntrackedFile(t)
	m := New(ws)
	m.showDiff()

	m, _ = sendKeyCmd(m, "y")

	if m.mode != normalMode {
		t.Fatalf("mode after accepting = %v, want normalMode", m.mode)
	}
	if m.view != diffView {
		t.Fatalf("view after accepting = %v, want diffView", m.view)
	}
	var sawNewFile bool
	for _, r := range m.rows {
		if strings.Contains(r.text, "Brand new") {
			sawNewFile = true
		}
	}
	if !sawNewFile {
		t.Errorf("rows = %#v, want the newly added file's content to show up in the diff", m.rows)
	}

	untracked, err := m.untrackedFiles()
	if err != nil {
		t.Fatalf("untrackedFiles: %v", err)
	}
	if len(untracked) != 0 {
		t.Errorf("untrackedFiles after accepting = %v, want none left", untracked)
	}
}

func TestDiffDecliningSkipsAddButStillShowsDiff(t *testing.T) {
	ws := gitRepoWithUntrackedFile(t)
	m := New(ws)
	m.showDiff()

	m, _ = sendKeyCmd(m, "n")

	if m.mode != normalMode {
		t.Fatalf("mode after declining = %v, want normalMode", m.mode)
	}
	if m.view != diffView {
		t.Fatalf("view after declining = %v, want diffView (declining still runs the diff)", m.view)
	}
	for _, r := range m.rows {
		if strings.Contains(r.text, "Brand new") {
			t.Errorf("rows = %#v, want the declined file to stay out of the diff", m.rows)
		}
	}

	untracked, err := m.untrackedFiles()
	if err != nil {
		t.Fatalf("untrackedFiles: %v", err)
	}
	if len(untracked) != 1 || untracked[0] != "new.org" {
		t.Errorf("untrackedFiles after declining = %v, want new.org still untracked", untracked)
	}
}

func TestCommitAsksToAddUntrackedFiles(t *testing.T) {
	ws := gitRepoWithUntrackedFile(t)
	m := New(ws)
	m.switchToView(diffView) // bypass showDiff's own untracked-file question, to isolate startCommit's

	m.startCommit()

	if m.mode != confirmMode {
		t.Fatalf("mode after :commit with an untracked file = %v, want confirmMode", m.mode)
	}
	if !strings.Contains(m.confirmMessage, "new.org") {
		t.Errorf("confirmMessage = %q, want it to name new.org", m.confirmMessage)
	}
}

func TestCommitAcceptingAddsFileThenOpensMessagePrompt(t *testing.T) {
	ws := gitRepoWithUntrackedFile(t)
	m := New(ws)
	m.switchToView(diffView)
	m.startCommit()

	m, _ = sendKeyCmd(m, "y")

	if m.mode != commitMessageMode {
		t.Fatalf("mode after accepting = %v, want commitMessageMode", m.mode)
	}

	m = typeKeys(m, "Add new file")
	updated, _ := sendKeyCmd(m, "enter")
	m = updated

	if !strings.Contains(m.message, "Committed") {
		t.Errorf("message = %q, want the commit to have gone through", m.message)
	}
	if strings.Contains(m.message, "git commit failed") {
		t.Errorf("message = %q, want the newly added file to actually be committed", m.message)
	}
}

func TestCommitDecliningStillOpensMessagePromptWithoutAdding(t *testing.T) {
	ws := gitRepoWithUntrackedFile(t)
	m := New(ws)
	m.switchToView(diffView)
	m.startCommit()

	m, _ = sendKeyCmd(m, "n")

	if m.mode != commitMessageMode {
		t.Fatalf("mode after declining = %v, want commitMessageMode (commit still proceeds, just without the new file)", m.mode)
	}

	untracked, err := m.untrackedFiles()
	if err != nil {
		t.Fatalf("untrackedFiles: %v", err)
	}
	if len(untracked) != 1 || untracked[0] != "new.org" {
		t.Errorf("untrackedFiles after declining = %v, want new.org still untracked", untracked)
	}
}

func TestUntrackedFilesEmptyWhenEverythingIsTracked(t *testing.T) {
	ws := gitRepoFixture(t, "todo.org", "* TODO Something\n", "* TODO Changed\n")
	m := New(ws)

	untracked, err := m.untrackedFiles()
	if err != nil {
		t.Fatalf("untrackedFiles: %v", err)
	}
	if len(untracked) != 0 {
		t.Errorf("untrackedFiles = %v, want none — the file is tracked, just modified", untracked)
	}
}

func TestGitAddIsRecordedInTheExecLog(t *testing.T) {
	ws := gitRepoWithUntrackedFile(t)
	m := New(ws)
	m.showDiff()
	m, _ = sendKeyCmd(m, "y")
	m.switchToView(logView)

	var sawAdd bool
	for _, r := range m.rows {
		if strings.Contains(r.text, "START") && strings.Contains(r.text, "add") && strings.Contains(r.text, "new.org") {
			sawAdd = true
		}
	}
	if !sawAdd {
		t.Errorf("rows = %#v, want git add's invocation logged with new.org named", m.rows)
	}
}
