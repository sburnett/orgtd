package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sburnett/orgtd/internal/workspace"
)

// gitRepoFixtureWithRemote is like gitRepoFixture, but also creates a
// bare "origin" remote and pushes the initial commit to it with upstream
// tracking set up — so a later plain `git push` (as runGitPush issues,
// with no explicit remote/branch) has somewhere to go and succeeds.
func gitRepoFixtureWithRemote(t *testing.T, name, committed, dirty string) *workspace.Workspace {
	t.Helper()
	remote := t.TempDir()
	runGit(t, remote, "init", "--bare", "-q")

	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test")
	runGit(t, dir, "remote", "add", "origin", remote)

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(committed), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	runGit(t, dir, "add", name)
	runGit(t, dir, "commit", "-q", "-m", "initial")
	runGit(t, dir, "push", "-q", "-u", "origin", "HEAD")

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

func TestCommitOnlyWorksFromDiffView(t *testing.T) {
	ws := gitRepoFixture(t, "todo.org", "* TODO Old title\n", "* TODO New title\n")
	m := New(ws)

	m = sendKey(m, ":")
	m = typeKeys(m, "commit")
	m, _ = sendKeyCmd(m, "enter")

	if m.mode == commitMessageMode {
		t.Fatalf("mode = commitMessageMode, want :commit to refuse outside diff view")
	}
	if !strings.Contains(m.message, "diff view") {
		t.Errorf("message = %q, want it to explain :commit needs diff view", m.message)
	}
}

func TestCommitEntersCommitMessagePromptFromDiffView(t *testing.T) {
	ws := gitRepoFixture(t, "todo.org", "* TODO Old title\n", "* TODO New title\n")
	m := New(ws)
	m.showDiff()

	m = sendKey(m, ":")
	m = typeKeys(m, "commit")
	m, _ = sendKeyCmd(m, "enter")

	if m.mode != commitMessageMode {
		t.Fatalf("mode after :commit in diff view = %v, want commitMessageMode", m.mode)
	}
}

func TestCommitMessagePromptEscCancelsWithoutCommitting(t *testing.T) {
	ws := gitRepoFixture(t, "todo.org", "* TODO Old title\n", "* TODO New title\n")
	m := New(ws)
	m.showDiff()
	m.startCommit()

	m = typeKeys(m, "abandoned message")
	m = sendKey(m, "esc")

	if m.mode != normalMode {
		t.Fatalf("mode after Esc = %v, want normalMode", m.mode)
	}
	if m.commitMessageInput != "" {
		t.Errorf("commitMessageInput after Esc = %q, want cleared", m.commitMessageInput)
	}
	out, err := m.runGitDiff()
	if err != nil {
		t.Fatalf("runGitDiff: %v", err)
	}
	if !strings.Contains(out, "New title") {
		t.Errorf("diff after cancelling = %q, want the uncommitted change still present", out)
	}
}

func TestCommitRequiresANonEmptyMessage(t *testing.T) {
	ws := gitRepoFixture(t, "todo.org", "* TODO Old title\n", "* TODO New title\n")
	m := New(ws)
	m.showDiff()
	m.startCommit()

	m, _ = sendKeyCmd(m, "enter")

	if m.mode != commitMessageMode {
		t.Fatalf("mode after empty Enter = %v, want to stay in commitMessageMode", m.mode)
	}
	if !strings.Contains(m.message, "empty") {
		t.Errorf("message = %q, want it to mention the message can't be empty", m.message)
	}
}

func TestCommitFailureLeavesChangesUncommittedAndSkipsPush(t *testing.T) {
	// Nothing to commit (dirty == committed): `git commit` itself will
	// fail with "nothing to commit", and that failure should stop before
	// ever attempting a push.
	ws := gitRepoFixture(t, "todo.org", "* TODO Something\n", "")
	m := New(ws)
	m.showDiff()
	m.startCommit()

	m = typeKeys(m, "a message")
	updated, cmd := sendKeyCmd(m, "enter")
	m = updated
	if cmd != nil {
		t.Fatalf("expected no tea.Cmd from a synchronous commit")
	}

	if m.mode != normalMode {
		t.Fatalf("mode after failed commit = %v, want normalMode", m.mode)
	}
	if !strings.Contains(m.message, "git commit failed") {
		t.Errorf("message = %q, want it to report the commit failure", m.message)
	}
	for _, e := range m.execLog.snapshot() {
		if e.kind == execLogStart && strings.Contains(e.text, "push") {
			t.Errorf("execLog = %#v, push should never run after a failed commit", m.execLog.snapshot())
		}
	}
}

func TestCommitAndPushSucceeds(t *testing.T) {
	ws := gitRepoFixtureWithRemote(t, "todo.org", "* TODO Old title\n", "* TODO New title\n")
	m := New(ws)
	m.showDiff()
	m.startCommit()

	m = typeKeys(m, "Update title")
	updated, _ := sendKeyCmd(m, "enter")
	m = updated

	if m.message != "Committed and pushed" {
		t.Errorf("message = %q, want \"Committed and pushed\"", m.message)
	}
	if m.mode != normalMode {
		t.Errorf("mode = %v, want normalMode", m.mode)
	}
	if m.view != diffView {
		t.Errorf("view = %v, want to stay in diffView", m.view)
	}
	if len(m.rows) != 1 || !strings.Contains(m.rows[0].text, "No changes") {
		t.Errorf("rows after commit+push = %#v, want a refreshed 'No changes' diff", m.rows)
	}
}

func TestCommitScopesToFilesOpenInTheOutline(t *testing.T) {
	ws := gitRepoFixtureWithRemote(t, "todo.org", "* TODO Old title\n", "* TODO New title\n")
	m := New(ws)
	m.showDiff()
	m.startCommit()

	m = typeKeys(m, "Update title")
	updated, _ := sendKeyCmd(m, "enter")
	m = updated

	if entries := m.execLog.snapshot(); !gitLogMentions(entries, "commit", "todo.org") {
		t.Errorf("execLog = %#v, want the commit invocation to name todo.org", entries)
	}
}

func gitLogMentions(entries []execLogEntry, substrs ...string) bool {
	for _, e := range entries {
		if e.kind != execLogStart {
			continue
		}
		all := true
		for _, s := range substrs {
			if !strings.Contains(e.text, s) {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}

func TestCommitPushFailureStillLeavesTheCommitInPlace(t *testing.T) {
	// gitRepoFixture (no remote) means the commit succeeds but the push
	// has nowhere to go.
	ws := gitRepoFixture(t, "todo.org", "* TODO Old title\n", "* TODO New title\n")
	m := New(ws)
	m.showDiff()
	m.startCommit()

	m = typeKeys(m, "Update title")
	updated, _ := sendKeyCmd(m, "enter")
	m = updated

	if !strings.Contains(m.message, "Committed, but git push failed") {
		t.Errorf("message = %q, want it to report the commit succeeded but the push failed", m.message)
	}
	if len(m.rows) != 1 || !strings.Contains(m.rows[0].text, "No changes") {
		t.Errorf("rows after commit (push failed) = %#v, want a refreshed 'No changes' diff — the commit itself went through", m.rows)
	}
}
