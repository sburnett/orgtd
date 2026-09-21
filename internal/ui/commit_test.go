package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

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

// runCommitCmd drives a tea.Cmd returned by startCommit (directly, or via
// the ":commit" command line) to completion, the same way a real
// bubbletea event loop would: running it off the main goroutine like
// applyCommit's own doc comment describes, then feeding the resulting
// commitPushMsg back through Update. Fails the test if cmd is nil (no
// commit actually started) or didn't yield a commitPushMsg.
func runCommitCmd(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	if cmd == nil {
		t.Fatalf("startCommit returned a nil tea.Cmd — no commit actually started")
	}
	msg, ok := cmd().(commitPushMsg)
	if !ok {
		t.Fatalf("cmd() = %#v, want a commitPushMsg", msg)
	}
	updated, _ := m.Update(msg)
	return updated.(Model)
}

func TestCommitOnlyWorksFromDiffView(t *testing.T) {
	ws := gitRepoFixture(t, "todo.org", "* TODO Old title\n", "* TODO New title\n")
	m := New(ws)

	m = sendKey(m, ":")
	m = typeKeys(m, "commit")
	m, _ = sendKeyCmd(m, "enter")

	if !strings.Contains(m.message, "diff view") {
		t.Errorf("message = %q, want it to explain :commit needs diff view", m.message)
	}
	out, err := m.runGitDiff()
	if err != nil {
		t.Fatalf("runGitDiff: %v", err)
	}
	if !strings.Contains(out, "New title") {
		t.Errorf("diff after refused :commit = %q, want the uncommitted change still present", out)
	}
}

func TestCommitFromDiffViewCommitsWithStockMessage(t *testing.T) {
	ws := gitRepoFixtureWithRemote(t, "todo.org", "* TODO Old title\n", "* TODO New title\n")
	m := New(ws)
	m.showDiff()

	m = sendKey(m, ":")
	m = typeKeys(m, "commit")
	m, cmd := sendKeyCmd(m, "enter")
	m = runCommitCmd(t, m, cmd)

	if m.message != "Committed and pushed" {
		t.Errorf("message = %q, want \"Committed and pushed\"", m.message)
	}
	if entries := m.execLog.snapshot(); !gitLogMentions(entries, "commit", "-m", stockCommitMessage) {
		t.Errorf("execLog = %#v, want the commit invocation to use stockCommitMessage %q", entries, stockCommitMessage)
	}
}

func TestCommitFailureLeavesChangesUncommittedAndSkipsPush(t *testing.T) {
	// Nothing to commit (dirty == committed): `git commit` itself will
	// fail with "nothing to commit", and that failure should stop before
	// ever attempting a push.
	ws := gitRepoFixture(t, "todo.org", "* TODO Something\n", "")
	m := New(ws)
	m.showDiff()
	cmd := m.startCommit()
	m = runCommitCmd(t, m, cmd)

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
	cmd := m.startCommit()
	m = runCommitCmd(t, m, cmd)

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
	cmd := m.startCommit()
	m = runCommitCmd(t, m, cmd)

	if entries := m.execLog.snapshot(); !gitLogMentions(entries, "commit", "todo.org") {
		t.Errorf("execLog = %#v, want the commit invocation to name todo.org", entries)
	}
}

func TestCommitExcludesCalendarFile(t *testing.T) {
	remote := t.TempDir()
	runGit(t, remote, "init", "--bare", "-q")

	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test")
	runGit(t, dir, "remote", "add", "origin", remote)

	todoPath := filepath.Join(dir, "todo.org")
	calPath := filepath.Join(dir, "calendar.org")
	if err := os.WriteFile(todoPath, []byte("* TODO Old title\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.WriteFile(calPath, []byte("* Old event\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	runGit(t, dir, "add", "todo.org", "calendar.org")
	runGit(t, dir, "commit", "-q", "-m", "initial")
	runGit(t, dir, "push", "-q", "-u", "origin", "HEAD")

	if err := os.WriteFile(todoPath, []byte("* TODO New title\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.WriteFile(calPath, []byte("* New event\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	ws, err := workspace.Load(dir)
	if err != nil {
		t.Fatalf("workspace.Load: %v", err)
	}
	m := New(ws)
	m.showDiff()
	cmd := m.startCommit()
	m = runCommitCmd(t, m, cmd)

	if entries := m.execLog.snapshot(); gitLogMentions(entries, "commit", "calendar.org") {
		t.Errorf("execLog = %#v, want the commit invocation to never name calendar.org", entries)
	}

	out, err := m.runGitCommit("noop")
	if err == nil || !strings.Contains(out, "no changes added to commit") {
		t.Fatalf("runGitCommit after :commit = (%q, %v), want its stdout to report nothing staged, since calendar.org's edit was never staged", out, err)
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
	cmd := m.startCommit()
	m = runCommitCmd(t, m, cmd)

	if !strings.Contains(m.message, "Committed, but git push failed") {
		t.Errorf("message = %q, want it to report the commit succeeded but the push failed", m.message)
	}
	if len(m.rows) != 1 || !strings.Contains(m.rows[0].text, "No changes") {
		t.Errorf("rows after commit (push failed) = %#v, want a refreshed 'No changes' diff — the commit itself went through", m.rows)
	}
}

func TestCommitRunsGitInTheBackgroundWithAStatusMessage(t *testing.T) {
	ws := gitRepoFixtureWithRemote(t, "todo.org", "* TODO Old title\n", "* TODO New title\n")
	m := New(ws)
	m.showDiff()

	cmd := m.startCommit()

	if cmd == nil {
		t.Fatalf("startCommit returned a nil tea.Cmd, want the commit+push to run in the background")
	}
	if !m.gitRunning {
		t.Errorf("gitRunning = false right after startCommit, want true until the background commit+push finishes")
	}
	if !strings.Contains(m.message, "background") {
		t.Errorf("message = %q, want it to say the commit+push is running in the background", m.message)
	}

	m = runCommitCmd(t, m, cmd)
	if m.gitRunning {
		t.Errorf("gitRunning = true after the commit+push finished, want false")
	}
}

func TestSecondCommitRefusesWhileOneIsStillRunning(t *testing.T) {
	ws := gitRepoFixtureWithRemote(t, "todo.org", "* TODO Old title\n", "* TODO New title\n")
	m := New(ws)
	m.showDiff()

	first := m.startCommit()
	if first == nil {
		t.Fatalf("startCommit returned a nil tea.Cmd for the first :commit")
	}

	second := m.startCommit()
	if second != nil {
		t.Errorf("second startCommit while one is running should return a nil tea.Cmd")
	}
	if !strings.Contains(m.message, "already") && !strings.Contains(m.message, "still running") {
		t.Errorf("message = %q, want it to explain a commit is already running", m.message)
	}

	// The first commit's own result should still apply normally once it
	// finishes — the refused second attempt shouldn't have wedged
	// gitRunning permanently on.
	m = runCommitCmd(t, m, first)
	if m.gitRunning {
		t.Errorf("gitRunning = true after the (first, only real) commit+push finished, want false")
	}
}

func TestWriteRefusesWhileGitIsRunning(t *testing.T) {
	ws := gitRepoFixtureWithRemote(t, "todo.org", "* TODO Old title\n", "* TODO New title\n")
	m := New(ws)
	m.showDiff()
	cmd := m.startCommit()
	if cmd == nil {
		t.Fatalf("startCommit returned a nil tea.Cmd, want the commit+push to be running in the background")
	}

	// An edit made after the commit+push has already started running in
	// the background — :w should still refuse this on its own (see
	// gitRunning in writeAllResult), independent of the unsaved-changes
	// check :commit itself uses before it'll even start (see
	// anyGitFileDirty), which only guards against *starting* a commit,
	// not editing while one already is one.
	m.switchToView(outlineView)
	m.cursor = findRow(t, m, "New title")
	m = setStatus(m, "n") // TODO -> NEXT, so the file is dirty and :w has something to write

	m = sendKey(m, ":")
	m = typeKeys(m, "w")
	m, _ = sendKeyCmd(m, "enter")

	if !strings.Contains(m.message, "running") {
		t.Errorf("message = %q, want :w to refuse while git is running in the background", m.message)
	}
	if len(m.dirty) == 0 {
		t.Errorf("dirty = %v, want the file to remain unwritten (still dirty) while :w was refused", m.dirty)
	}

	// Once the background commit+push actually finishes, :w should work
	// again.
	m = runCommitCmd(t, m, cmd)
	m = sendKey(m, ":")
	m = typeKeys(m, "w")
	m, _ = sendKeyCmd(m, "enter")
	if !strings.Contains(m.message, "Wrote") {
		t.Errorf("message = %q, want :w to succeed once git is no longer running", m.message)
	}
}

// TestCommitFinishingDoesNotYankAwayFromAViewNavigatedToInTheMeantime
// guards against finishCommitPush reaching for showDiff/runDiffNow,
// which unconditionally switch to diff view — fine for :diff itself,
// but wrong here: the background commit+push can finish well after the
// user has moved on to some other view, and completing shouldn't hijack
// their navigation back to a view they deliberately left.
func TestCommitFinishingDoesNotYankAwayFromAViewNavigatedToInTheMeantime(t *testing.T) {
	ws := gitRepoFixtureWithRemote(t, "todo.org", "* TODO Old title\n", "* TODO New title\n")
	m := New(ws)
	m.showDiff()
	cmd := m.startCommit()
	if cmd == nil {
		t.Fatalf("startCommit returned a nil tea.Cmd")
	}

	// Navigate away from diff view while the commit+push is still
	// running in the background.
	m.switchToView(outlineView)

	m = runCommitCmd(t, m, cmd)

	if m.view != outlineView {
		t.Errorf("view after the background commit finished = %v, want outlineView (the view navigated to while it was running)", m.view)
	}
	if !strings.Contains(m.message, "Committed and pushed") {
		t.Errorf("message = %q, want it to still report the commit's own result", m.message)
	}
}

// TestCommitFinishingRefreshesDiffDataEvenWhenNotShowingIt guards the
// other half: even though finishing a background :commit shouldn't
// force diff view back open (see the test above), it must still update
// the underlying diff data — so that if the user does go back to diff
// view later, they see the post-commit diff (here, no changes left)
// rather than a stale snapshot from before the commit ran.
func TestCommitFinishingRefreshesDiffDataEvenWhenNotShowingIt(t *testing.T) {
	ws := gitRepoFixtureWithRemote(t, "todo.org", "* TODO Old title\n", "* TODO New title\n")
	m := New(ws)
	m.showDiff()
	if !strings.Contains(m.diffOutput, "New title") {
		t.Fatalf("fixture assumption broken: diff before committing should show the pending change")
	}
	cmd := m.startCommit()
	if cmd == nil {
		t.Fatalf("startCommit returned a nil tea.Cmd")
	}

	m.switchToView(agendaView)
	m = runCommitCmd(t, m, cmd)
	if m.view != agendaView {
		t.Fatalf("view after commit finished = %v, want to stay in agendaView", m.view)
	}

	m.switchToView(diffView)
	if strings.Contains(m.diffOutput, "New title") {
		t.Errorf("diffOutput after returning to diff view = %q, want it refreshed to no longer show the now-committed change", m.diffOutput)
	}
	if len(m.rows) != 1 || !strings.Contains(m.rows[0].text, "No changes") {
		t.Errorf("rows after returning to diff view = %#v, want a fresh 'No changes' diff", m.rows)
	}
}

// TestCommitRefusesWithUnsavedChanges guards the same concern as
// TestDiffRefusesWithUnsavedChanges, but for :commit specifically:
// diff view's own rows aren't re-diffed on every keystroke (see
// refreshDiffData), so an edit made after :diff already ran — while
// still sitting in diff view — must still block :commit from
// committing disk content it knows is now stale, rather than relying
// solely on showDiff's own guard at entry.
func TestCommitRefusesWithUnsavedChanges(t *testing.T) {
	ws := gitRepoFixtureWithRemote(t, "todo.org", "* TODO Old title\n", "* TODO New title\n")
	m := New(ws)
	m.showDiff()

	// An edit made without leaving diff view's own data stale-refreshed:
	// switchToView only rebuilds rows from the diff already captured, it
	// doesn't rerun `git diff`.
	m.switchToView(outlineView)
	m.cursor = findRow(t, m, "New title")
	m = setStatus(m, "n") // TODO -> NEXT
	m.switchToView(diffView)

	cmd := m.startCommit()

	if cmd != nil {
		t.Errorf("startCommit with unsaved changes should return a nil tea.Cmd")
	}
	if !strings.Contains(m.message, "Unsaved changes") {
		t.Errorf("message = %q, want it to explain there are unsaved changes", m.message)
	}
	for _, e := range m.execLog.snapshot() {
		if e.kind == execLogStart && strings.Contains(e.text, "commit") {
			t.Errorf("execLog = %#v, git commit should never run with unsaved changes present", m.execLog.snapshot())
		}
	}
}
