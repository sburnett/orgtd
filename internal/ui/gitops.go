package ui

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sburnett/orgtd/internal/gitrepo"
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
		if filepath.Base(f.Path) == m.cfg.CalendarFile {
			continue
		}
		files = append(files, f)
	}
	return files
}

// gitPaths returns the on-disk paths of gitFiles(), the pathspec every
// diff/ls-files/commit scopes itself to.
func (m *Model) gitPaths() []string {
	files := m.gitFiles()
	paths := make([]string, 0, len(files))
	for _, f := range files {
		paths = append(paths, f.Path)
	}
	return paths
}

// repo returns the git repository handle for the workspace directory.
// It's a plain value, so a background goroutine can capture it up front
// (see applyCommit) instead of reaching back into a Model that a
// concurrently running Update call could be mutating.
func (m *Model) repo() gitrepo.Repo {
	return gitrepo.Repo{Dir: m.ws.Dir, Log: m.execLog}
}

// appendDiffRows appends the rows for :diff: the working-tree diff (see
// showDiff/refreshDiffData) for every file currently open in the outline
// (excluding the calendar file — see gitFiles), one row per line, verbatim (like :log's output lines, no further parsing
// or styling) — or a placeholder if there's nothing to show, no files
// are open, or the diff itself failed despite the workspace being a
// proper git repository root (showDiff already refuses before this is
// ever reached otherwise) — e.g. a repository with no commits yet at
// all, so there's no HEAD to diff against.
func (m *Model) appendDiffRows(dst *[]row) {
	if m.diffErr != "" {
		*dst = append(*dst, row{kind: rowText, text: fmt.Sprintf("git diff failed: %s", m.diffErr)})
		return
	}
	if len(m.ws.Files) == 0 {
		*dst = append(*dst, row{kind: rowText, text: "No files open in the outline."})
		return
	}
	if strings.TrimSpace(m.diffOutput) == "" {
		*dst = append(*dst, row{kind: rowText, text: "No changes."})
		return
	}
	for _, line := range strings.Split(m.diffOutput, "\n") {
		*dst = append(*dst, row{kind: rowText, text: line})
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
// repository (see gitrepo.Repo.RootRefusal), same as :commit: a diff run from
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
		if reason := m.repo().RootRefusal(); reason != "" {
			m.message = fmt.Sprintf("Refusing to diff: %s", reason)
			return
		}
		if untracked, err := m.repo().Untracked(m.gitPaths()); err == nil && len(untracked) > 0 {
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
// switch to diff view: runs `git diff` (see gitrepo.Repo.Diff), updating
// m.diffOutput/m.diffErr, and — only if diff view happens to be showing
// already — rebuilds m.rows so it's visibly current too. Used by
// finishCommitPush once a background :commit finishes, so the diff
// reflects the commit it just made without forcing the user back into
// diff view if they've since navigated elsewhere themselves; if they're
// still there, they see it refresh in place.
func (m *Model) refreshDiffData() {
	m.diffOutput, m.diffErr = "", ""
	if len(m.gitFiles()) > 0 {
		out, err := m.repo().Diff(m.gitPaths())
		if err != nil {
			m.diffErr = gitrepo.ErrorText(err)
		} else {
			m.diffOutput = out
		}
	}
	if m.view == diffView {
		m.rebuildRows()
	}
}

// requestAddUntracked interrupts :diff/:commit with a y/N confirmation
// (reusing confirmMode, alongside its existing file-edit prompt — see
// pendingUntrackedFiles/pendingUntrackedThen) when gitrepo.Repo.Untracked found
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
// workspace is safe to run those against (see gitrepo.Repo.RootRefusal) —
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
	if reason := m.repo().RootRefusal(); reason != "" {
		m.message = fmt.Sprintf("Refusing to commit: %s", reason)
		return nil
	}
	if untracked, err := m.repo().Untracked(m.gitPaths()); err == nil && len(untracked) > 0 {
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

	repo := m.repo()
	paths := m.gitPaths()

	return func() tea.Msg {
		nothingToCommit := false
		if stdout, err := repo.Commit(paths, stockCommitMessage); err != nil {
			if !gitrepo.IsNothingToCommit(stdout) {
				return commitPushMsg{commitErr: err}
			}
			nothingToCommit = true
		}
		if _, err := repo.Push(); err != nil {
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
		m.message = fmt.Sprintf("git commit failed: %s", gitrepo.ErrorText(msg.commitErr))
		return m, nil
	}
	if msg.pushErr != nil {
		if msg.nothingToCommit {
			m.message = fmt.Sprintf("Nothing to commit, but git push failed: %s", gitrepo.ErrorText(msg.pushErr))
		} else {
			m.message = fmt.Sprintf("Committed, but git push failed: %s", gitrepo.ErrorText(msg.pushErr))
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
