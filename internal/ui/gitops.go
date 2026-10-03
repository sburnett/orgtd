package ui

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sburnett/orgtd/internal/execlog"
	"github.com/sburnett/orgtd/internal/org"
)

// gitFiles returns m.ws.Files minus the calendar file (see
// WithCalendarFile): every git operation (diff/add/commit) that scopes
// itself to "the files currently open in the outline" uses this instead
// of m.ws.Files directly, since the calendar file is :sync-calendar's own
// output — regenerated locally from Google Calendar, not something
// meant to be versioned or committed alongside the rest of the org
// directory.
func (m *Model) gitFiles() []*org.File {
	files := make([]*org.File, 0, len(m.ws.Files))
	for _, f := range m.ws.Files {
		if filepath.Base(f.Path) == m.calendarFile {
			continue
		}
		files = append(files, f)
	}
	return files
}

// appendDiffRows populates m.rows for :diff: the working-tree diff (see
// showDiff/runGitDiff) for every file currently open in the outline
// (excluding the calendar file — see gitFiles), one row per line, verbatim (like :log's output lines, no further parsing
// or styling) — or a placeholder if there's nothing to show, no files
// are open, or the diff itself failed despite the workspace being a
// proper git repository root (showDiff already refuses before this is
// ever reached otherwise) — e.g. a repository with no commits yet at
// all, so there's no HEAD to diff against.
func (m *Model) appendDiffRows() {
	if m.diffErr != "" {
		m.rows = append(m.rows, row{isTextLine: true, text: fmt.Sprintf("git diff failed: %s", m.diffErr)})
		return
	}
	if len(m.ws.Files) == 0 {
		m.rows = append(m.rows, row{isTextLine: true, text: "No files open in the outline."})
		return
	}
	if strings.TrimSpace(m.diffOutput) == "" {
		m.rows = append(m.rows, row{isTextLine: true, text: "No changes."})
		return
	}
	for _, line := range strings.Split(m.diffOutput, "\n") {
		m.rows = append(m.rows, row{isTextLine: true, text: line})
	}
}

// anyGitFileDirty reports whether any file :diff/:commit would actually
// touch (see gitFiles) has unsaved in-memory changes. Both commands
// only ever see what's on disk (`git diff`/`git commit` read the
// working tree, not orgtd's in-memory org.File), so a dirty file's
// on-disk content is stale until :w — scoped to gitFiles() rather than
// m.dirty as a whole so an unrelated dirty calendar file (never part of
// what :diff/:commit show or commit — see gitFiles) doesn't block
// either.
func (m *Model) anyGitFileDirty() bool {
	for _, f := range m.gitFiles() {
		if m.dirty[f] {
			return true
		}
	}
	return false
}

// showDiff (":diff") shows the result of `git diff` for every file
// currently open in the outline — refusing outright if any of them has
// unsaved changes (see anyGitFileDirty), since otherwise the diff shown
// would silently be missing them, looking like they were never made at
// all. Also refuses unless the workspace is the root of its git
// repository (see gitRepoRootRefusal), same as :commit: a diff run from
// some subdirectory of a larger repo (or outside a repo entirely) would
// never be able to offer adding an untracked file either, so showing it
// at all would be misleading about what :commit could actually do with
// it. Otherwise, first checks whether any open file isn't tracked by
// git yet (see requestAddUntracked); if so, this pauses on that
// question and only actually runs the diff (via runDiffNow) once it's
// answered.
func (m *Model) showDiff() {
	if m.anyGitFileDirty() {
		m.message = "Unsaved changes — :w first, since :diff only shows what's actually on disk"
		return
	}
	if len(m.gitFiles()) > 0 {
		if reason := m.gitRepoRootRefusal(); reason != "" {
			m.message = fmt.Sprintf("Refusing to diff: %s", reason)
			return
		}
		if untracked, err := m.untrackedFiles(); err == nil && len(untracked) > 0 {
			m.requestAddUntracked(untracked, func(m *Model) tea.Cmd { m.runDiffNow(); return nil })
			return
		}
	}
	m.runDiffNow()
}

// runDiffNow does showDiff's actual work once there's nothing left to
// ask about: refreshes the diff data (see refreshDiffData) and switches
// to diff view to show the result. Run synchronously — unlike
// :format-links' potentially slow, arbitrary external formatter, `git
// diff` on a handful of local org files is fast, so there's no need for
// the async tea.Cmd/Msg dance that keeps the app responsive during a
// longer-running command.
func (m *Model) runDiffNow() {
	m.refreshDiffData()
	m.switchToView(diffView)
}

// refreshDiffData is runDiffNow's data half on its own, without the
// switch to diff view: runs `git diff` (see runGitDiff), updating
// m.diffOutput/m.diffErr, and — only if diff view happens to be showing
// already — rebuilds m.rows so it's visibly current too. Used by
// finishCommitPush once a background :commit finishes, so the diff
// reflects the commit it just made without forcing the user back into
// diff view if they've since navigated elsewhere themselves; if they're
// still there, they see it refresh in place.
func (m *Model) refreshDiffData() {
	m.diffOutput, m.diffErr = "", ""
	if len(m.gitFiles()) > 0 {
		out, err := m.runGitDiff()
		if err != nil {
			m.diffErr = gitErrorText(err)
		} else {
			m.diffOutput = out
		}
	}
	if m.view == diffView {
		m.rebuildRows()
	}
}

// runGitDiff runs `git diff HEAD` scoped to every file currently open in
// the outline except the calendar file (see gitFiles), with git itself pointed at the workspace
// directory (via -C, rather than relying on orgtd's own working
// directory) so a repository rooted there or above is found either way.
// Diffed against HEAD rather than a plain `git diff` (which only shows
// unstaged changes) so a file `git add`ed via requestAddUntracked but
// not yet committed still shows up as an addition here, instead of
// looking like nothing happened. Logged like any other external
// command — see execlog.Run.
func (m *Model) runGitDiff() (string, error) {
	args := []string{"-C", m.ws.Dir, "diff", "HEAD", "--"}
	for _, f := range m.gitFiles() {
		args = append(args, f.Path)
	}
	return execlog.Run(m.execLog, "git", args, "")
}

// untrackedFiles returns the paths, among m.gitFiles(), that git doesn't
// track at all yet — via `git ls-files --others --exclude-standard`,
// scoped to just those paths so files elsewhere in the repo (or
// gitignored entirely) never show up. Returns (nil, nil) if there are
// no open files or nothing is untracked; the error return is only for a
// genuine failure to even ask (not a git repository, git missing,
// ...) — callers treat that the same as "nothing untracked" and let the
// diff/commit that follows surface the real problem instead.
func (m *Model) untrackedFiles() ([]string, error) {
	gitFiles := m.gitFiles()
	if len(gitFiles) == 0 {
		return nil, nil
	}
	args := []string{"-C", m.ws.Dir, "ls-files", "--others", "--exclude-standard", "--"}
	for _, f := range gitFiles {
		args = append(args, f.Path)
	}
	out, err := execlog.Run(m.execLog, "git", args, "")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(out) == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// gitRepoRoot returns the git repository root that contains dir — via
// `git rev-parse --show-toplevel` — or "" if dir isn't inside a git
// repository at all (or git itself failed). Logged like any other
// external command. A free function, rather than a *Model method, so
// applyCommit's background goroutine can call it directly with values
// captured up front instead of reaching back into a Model that a
// concurrently running Update call could be mutating — see
// applyCommit.
func gitRepoRoot(elog *execlog.Log, dir string) string {
	out, err := execlog.Run(elog, "git", []string{"-C", dir, "rev-parse", "--show-toplevel"}, "")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// gitRepoRootRefusal reports why mutating git commands (add, commit,
// push — see requireGitRepoRoot) must refuse to run against dir, or ""
// if they're fine to run. They're refused unless dir is itself the
// *root* of its git repository, not merely somewhere inside one: git
// add/commit are scoped to specific files, so on their own a nested
// workspace wouldn't be too dangerous, but git push is not scoped to
// files at all — it pushes the *whole* current branch — and a workspace
// that's really just a subdirectory of some larger, unrelated repository
// (e.g. orgtd's own testdata/orgdir, nested inside this very repo) must
// never have that run against it. Paths are compared after resolving
// symlinks (see filepath.EvalSymlinks) since e.g. macOS routes /tmp and
// /var through symlinks into /private — comparing raw paths would
// otherwise misreport plenty of genuinely rooted workspaces (anything
// under the system's temp dir included) as nested elsewhere. A free
// function for the same reason as gitRepoRoot.
func gitRepoRootRefusal(elog *execlog.Log, dir string) string {
	root := gitRepoRoot(elog, dir)
	if root == "" {
		return "the org directory isn't inside a git repository"
	}
	wsResolved, wsErr := filepath.EvalSymlinks(dir)
	rootResolved, rootErr := filepath.EvalSymlinks(root)
	if wsErr != nil || rootErr != nil || wsResolved != rootResolved {
		return fmt.Sprintf("the org directory isn't the root of its git repository (root is %s)", root)
	}
	return ""
}

// requireGitRepoRoot is the guard every mutating git operation (gitAdd,
// runGitCommit, runGitPush) checks before doing anything — see
// gitRepoRootRefusal for why. Checked there directly (rather than only
// at the higher-level call sites that ask about it first, for a better
// error message — see showDiff/startCommit) so there's no way to reach
// an actual mutation without passing this, regardless of how it's
// eventually called. A free function for the same reason as
// gitRepoRoot.
func requireGitRepoRoot(elog *execlog.Log, dir string) error {
	if reason := gitRepoRootRefusal(elog, dir); reason != "" {
		return fmt.Errorf("%s", reason)
	}
	return nil
}

func (m *Model) gitRepoRoot() string        { return gitRepoRoot(m.execLog, m.ws.Dir) }
func (m *Model) gitRepoRootRefusal() string { return gitRepoRootRefusal(m.execLog, m.ws.Dir) }
func (m *Model) requireGitRepoRoot() error  { return requireGitRepoRoot(m.execLog, m.ws.Dir) }

// requestAddUntracked interrupts :diff/:commit with a y/N confirmation
// (reusing confirmMode, alongside its existing file-edit prompt — see
// pendingUntrackedFiles/pendingUntrackedThen) when untrackedFiles found
// something. then runs either way once answered (see
// updateConfirmMode): `git add`ing untracked first if accepted, or
// completely unchanged if declined — a deliberately untracked file
// shouldn't block diffing/committing everything else. then returns a
// tea.Cmd so :commit's caller (unlike :diff's) can hand back a
// background commit+push to run once the question is settled (see
// applyCommit).
func (m *Model) requestAddUntracked(untracked []string, then func(m *Model) tea.Cmd) {
	m.mode = confirmMode
	m.confirmMessage = fmt.Sprintf("Not tracked by git: %s. Add to git? [y/N]", strings.Join(untracked, ", "))
	m.pendingUntrackedFiles = untracked
	m.pendingUntrackedThen = then
}

// gitAdd runs `git add` for exactly the given paths (as returned by
// untrackedFiles — already suitable as pathspecs from within the
// workspace directory), so accepting requestAddUntracked's prompt never
// stages anything beyond what it named. Refuses outside the workspace's
// own git repository root — see requireGitRepoRoot. Logged like any
// other external command.
func (m *Model) gitAdd(paths []string) (string, error) {
	if err := m.requireGitRepoRoot(); err != nil {
		return "", err
	}
	args := append([]string{"-C", m.ws.Dir, "add", "--"}, paths...)
	return execlog.Run(m.execLog, "git", args, "")
}

// gitErrorText extracts the most useful message from a failed git
// invocation (diff, commit, or push): git's own stderr (e.g. "fatal: not
// a git repository...") when there is one, else the raw error (e.g.
// "git" not being installed at all).
func gitErrorText(err error) string {
	if exitErr, ok := err.(*exec.ExitError); ok {
		if msg := strings.TrimSpace(string(exitErr.Stderr)); msg != "" {
			return msg
		}
	}
	return err.Error()
}

// stockCommitMessage is the fixed message every :commit uses — see
// applyCommit. orgtd commits are frequent, small, and scoped to exactly
// what :diff already showed, so a per-commit message would mostly just
// restate that; a stock message keeps :commit a single keystroke rather
// than a prompt to fill in each time.
const stockCommitMessage = "orgtd commit"

// startCommit (":commit") commits and pushes what :diff shows (see
// applyCommit), restricted to diff view: :commit only makes sense once
// you've actually looked at what's about to be committed via :diff, and
// reusing that view's own file scope — rather than letting :commit
// imply some other set of files — keeps "what :diff shows" and "what
// :commit commits" the same thing. Refuses outright if a previous
// :commit's git commit/push is still running in the background (see
// gitRunning) — starting a second one concurrently would race the
// first over the same working tree. Also refuses if any file :commit
// would touch has unsaved changes (see anyGitFileDirty): entering diff
// view already refuses this (see showDiff), but diff view's own content
// isn't re-diffed on every keystroke, so an edit made — or a jump back
// into a stale diff view via the jump list — after :diff ran could
// otherwise let :commit commit disk content that's missing whatever's
// still only in memory. Otherwise, since :commit always ends in a
// mutating git add/commit/push, it refuses altogether unless the
// workspace is safe to run those against (see gitRepoRootRefusal) —
// checked up front, before even asking about untracked files, so
// declining that question is never even on the table when the real
// problem is the repository itself. Otherwise, as with :diff, first
// checks for files git doesn't track at all yet (see
// requestAddUntracked) — `git commit -- <pathspec>` silently skips a
// file that was never even `git add`ed once, so without this an
// untracked org file would just never make it into a commit.
func (m *Model) startCommit() tea.Cmd {
	if m.view != diffView {
		m.message = ":commit only works in diff view — see :diff"
		return nil
	}
	if m.gitRunning {
		m.message = "git is still running in the background from a previous :commit"
		return nil
	}
	if m.anyGitFileDirty() {
		m.message = "Unsaved changes — :w first, since :commit only commits what's actually on disk"
		return nil
	}
	if reason := m.gitRepoRootRefusal(); reason != "" {
		m.message = fmt.Sprintf("Refusing to commit: %s", reason)
		return nil
	}
	if untracked, err := m.untrackedFiles(); err == nil && len(untracked) > 0 {
		m.requestAddUntracked(untracked, func(m *Model) tea.Cmd { return m.applyCommit() })
		return nil
	}
	return m.applyCommit()
}

// commitPushMsg reports that :commit's git commit + git push (see
// applyCommit) finished running in the background. commitErr, if set,
// means `git commit` itself failed for a real reason and push never even
// ran; pushErr, if set, means the push that followed didn't succeed
// (whether or not the commit itself made a new commit). nothingToCommit
// means `git commit` found nothing to commit (e.g. :commit run twice in
// a row, or after committing by hand outside orgtd) — not a real
// failure, so the push still ran. All of commitErr/pushErr/nothingToCommit
// zero/false means both the commit and push succeeded normally.
type commitPushMsg struct {
	commitErr       error
	pushErr         error
	nothingToCommit bool
}

// isNothingToCommit reports whether stdout from a failed `git commit` is
// just git's own "nothing to commit" message rather than a real failure.
// applyCommit treats this as harmless and still runs the push — useful
// on its own right after a push failure (say, a transient network
// error): rerunning :commit should retry the push even though the
// earlier :commit already made the commit itself.
func isNothingToCommit(stdout string) bool {
	return strings.Contains(stdout, "nothing to commit")
}

// applyCommit commits every file currently open in the outline except
// the calendar file (the same scope :diff shows) with stockCommitMessage,
// then pushes — both run on their own goroutine (see
// startFormatLinks/startSyncCalendar for the same pattern) so the rest
// of the app stays usable while git runs, including however long the
// remote takes to respond to the push. Every value the goroutine needs
// (elog, dir, the file paths) is captured here, before it starts,
// rather than read from m inside it — m keeps being mutated by the main
// goroutine's own Update calls while this runs, and gitFiles() in
// particular walks m.ws.Files, which finishSyncCalendar/finishEditFile
// can replace out from under it. m.gitRunning is set immediately, for
// the same reason plus so a repeated :commit or :w can refuse right
// away rather than racing the goroutine — finishCommitPush clears it
// once the result comes back. A commit failing for a real reason (see
// isNothingToCommit) leaves the working tree untouched and never
// attempts the push; "nothing to commit" isn't treated as a failure at
// all, so the push still runs (there may be earlier local commits not
// yet pushed); a failed push still leaves the commit in place, so the
// diff view is refreshed either way (in finishCommitPush) to show
// whatever actually happened.
func (m *Model) applyCommit() tea.Cmd {
	m.gitRunning = true
	m.message = "Running git commit and git push in the background..."

	elog := m.execLog
	dir := m.ws.Dir
	gitFiles := m.gitFiles()
	paths := make([]string, 0, len(gitFiles))
	for _, f := range gitFiles {
		paths = append(paths, f.Path)
	}

	return func() tea.Msg {
		nothingToCommit := false
		if stdout, err := runGitCommit(elog, dir, paths, stockCommitMessage); err != nil {
			if !isNothingToCommit(stdout) {
				return commitPushMsg{commitErr: err}
			}
			nothingToCommit = true
		}
		if _, err := runGitPush(elog, dir); err != nil {
			return commitPushMsg{pushErr: err, nothingToCommit: nothingToCommit}
		}
		return commitPushMsg{nothingToCommit: nothingToCommit}
	}
}

// finishCommitPush applies a completed :commit run (see applyCommit and
// commitPushMsg) — clears m.gitRunning either way, then reports success
// or failure. On anything but an outright failed commit (where nothing
// changed, so the diff already shown is still accurate), refreshes the
// diff data (see refreshDiffData) so it's never left stale — but
// deliberately via refreshDiffData, not showDiff/runDiffNow: this runs
// whenever the background commit+push happens to finish, possibly well
// after the user has navigated away from diff view to do something
// else, and it shouldn't yank them back to it just because a commit
// they started earlier finally completed.
func (m Model) finishCommitPush(msg commitPushMsg) (tea.Model, tea.Cmd) {
	m.gitRunning = false

	if msg.commitErr != nil {
		m.message = fmt.Sprintf("git commit failed: %s", gitErrorText(msg.commitErr))
		return m, nil
	}
	if msg.pushErr != nil {
		if msg.nothingToCommit {
			m.message = fmt.Sprintf("Nothing to commit, but git push failed: %s", gitErrorText(msg.pushErr))
		} else {
			m.message = fmt.Sprintf("Committed, but git push failed: %s", gitErrorText(msg.pushErr))
		}
		m.refreshDiffData()
		return m, nil
	}

	if msg.nothingToCommit {
		m.message = "Nothing to commit; pushed"
	} else {
		m.message = "Committed and pushed"
	}
	m.refreshDiffData()
	return m, nil
}

// runGitCommit commits every file currently open in the outline except
// the calendar file (m.gitFiles() — the same scope runGitDiff uses) with
// message, from within the workspace directory. Refuses outside the
// workspace's own git repository root — see requireGitRepoRoot. Logged
// like any other external command — see execlog.Run.
func (m *Model) runGitCommit(message string) (string, error) {
	gitFiles := m.gitFiles()
	paths := make([]string, 0, len(gitFiles))
	for _, f := range gitFiles {
		paths = append(paths, f.Path)
	}
	return runGitCommit(m.execLog, m.ws.Dir, paths, message)
}

// runGitCommit is runGitCommit's free-function core (see gitRepoRoot for
// why): commits paths, from within dir, with message.
func runGitCommit(elog *execlog.Log, dir string, paths []string, message string) (string, error) {
	if err := requireGitRepoRoot(elog, dir); err != nil {
		return "", err
	}
	args := []string{"-C", dir, "commit", "-m", message, "--"}
	args = append(args, paths...)
	return execlog.Run(elog, "git", args, "")
}

// runGitPush runs a plain `git push` from within the workspace
// directory — unlike diff and commit, a push isn't scoped to particular
// files (there's no such thing as pushing only some files' history), so
// it just pushes the current branch to its configured upstream. This is
// exactly why requireGitRepoRoot matters most here: a push affects the
// whole repository's history, not just the org files orgtd knows about.
// Logged like any other external command — see execlog.Run.
func (m *Model) runGitPush() (string, error) {
	return runGitPush(m.execLog, m.ws.Dir)
}

// runGitPush is runGitPush's free-function core (see gitRepoRoot for
// why).
func runGitPush(elog *execlog.Log, dir string) (string, error) {
	if err := requireGitRepoRoot(elog, dir); err != nil {
		return "", err
	}
	return execlog.Run(elog, "git", []string{"-C", dir, "push"}, "")
}
