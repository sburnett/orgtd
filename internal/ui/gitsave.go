package ui

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/go-git/go-git/v5/plumbing"

	"github.com/sburnett/orgtd/internal/gitstore"
	"github.com/sburnett/orgtd/internal/org"
)

// gitState is everything the Model keeps about the git repository its
// files are saved to (see internal/gitstore and DESIGN.md, "Saving
// through git"). It's a pointer on Model, shared by every copy Bubble
// Tea makes, like the Model's maps.
type gitState struct {
	store   *gitstore.Store
	openErr error // why the store couldn't be opened; then saving is refused
	tried   bool  // whether opening has been attempted (see Model.gitStore)

	// seen is the main branch commit whose files this instance has fully
	// caught up with: saved onto, or reloaded from. Polling (see
	// pollMain) compares the branch against it.
	seen plumbing.Hash

	// mustSave holds files with nothing to undo that still need saving:
	// ones replaced wholesale by :resolve or :recover.
	mustSave map[*org.File]bool

	// conflict is the outcome of the last save if it couldn't merge;
	// nil otherwise. See :resolve, :w! and :e!.
	conflict *saveConflict

	// behind lists files with unsaved edits that the main branch has
	// changed too (another instance saved them): the next :w merges.
	// Shown on the status line.
	behind []string

	// others is how many other orgtd instances are running on this org
	// directory, as of the last pollMain (see gitstore.OtherInstances).
	// Shown on the status line.
	others int

	// mobile is why pollMain couldn't bring in the phone's branch on its
	// last try (see gitstore.ApplyBranch), as the status line says it;
	// empty if it could, or had nothing to do.
	mobile string

	// mobileConflict is the phone's branch's merge with the main branch
	// that collided, as of the last pollMain, for :resolve; nil if none.
	mobileConflict *gitstore.BranchConflict

	// orphans are dead instances' unsaved edits found at startup (see
	// :recover); recovering are the ones :recover has loaded into memory,
	// to drop once a save gets them into the main branch.
	orphans    []gitstore.Orphan
	recovering []gitstore.Orphan
}

// saveConflict is a save that couldn't merge cleanly.
type saveConflict struct {
	files map[string]gitstore.Conflict // by path relative to the org directory
}

func (c *saveConflict) paths() []string {
	var out []string
	for p := range c.files {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func newGitState() *gitState {
	return &gitState{mustSave: map[*org.File]bool{}}
}

// gitStore returns the store files are saved to, opening it on first
// use if WithStore didn't provide one (tests): main opens it at startup,
// refusing to run outside a git repository.
func (m *Model) gitStore() (*gitstore.Store, error) {
	g := m.git
	if g.store != nil || g.tried {
		return g.store, g.openErr
	}
	g.tried = true
	g.store, g.openErr = gitstore.Open(m.ws.Dir, m.cfg.MainBranch, m.cfg.OutlineIgnoreFiles)
	if g.openErr == nil {
		g.seen, g.openErr = g.store.Head()
		g.others, _ = g.store.OtherInstances()
	}
	if g.openErr != nil {
		g.store = nil
	}
	return g.store, g.openErr
}

// relPath is f's path relative to the org directory, slash-separated, as
// the store names files.
func (m *Model) relPath(f *org.File) string {
	rel, err := filepath.Rel(m.ws.Dir, f.Path)
	if err != nil {
		return filepath.ToSlash(f.Path)
	}
	return filepath.ToSlash(rel)
}

// unsaved reports whether f has edits not yet saved.
func (m *Model) unsaved(f *org.File) bool {
	return m.dirty[f] || m.git.mustSave[f]
}

// writeAllResult saves every file with unsaved changes (":w"), returning
// a status-line summary and whether the save went through. With force
// (":w!"), a file that can't be merged is saved as this instance has it,
// whatever the main branch has; every other file still merges.
func (m *Model) writeAllResult(force bool) (string, bool) {
	var files []*org.File
	for _, f := range m.ws.Files {
		if m.unsaved(f) {
			files = append(files, f)
		}
	}
	if len(files) == 0 {
		return "No changes to write", true
	}
	msg, ok := m.saveFiles(files, nil)
	if ok || !force || m.git.conflict == nil {
		return msg, ok
	}
	forced := map[string]bool{}
	for p := range m.git.conflict.files {
		forced[p] = true
	}
	return m.saveFiles(files, forced)
}

// saveFiles saves files through the store (see gitstore.Save), merging
// them with whatever reached the main branch since they were loaded, and
// brings the workspace up to date with the result: files the merge
// changed are reloaded, as are other files the main branch changed (see
// reloadChanged). Paths in forced are saved as they are here, whatever
// the main branch has.
func (m *Model) saveFiles(files []*org.File, forced map[string]bool) (string, bool) {
	store, err := m.gitStore()
	if err != nil {
		return "Can't save: " + err.Error(), false
	}
	var edits []gitstore.Edit
	rendered := map[*org.File]string{}
	var names []string
	var diffs []org.FileDiff
	for _, f := range files {
		rel := m.relPath(f)
		content := org.RenderFile(f)
		rendered[f] = content
		names = append(names, rel)
		diffs = append(diffs, org.FileDiff{Path: rel, Old: f.Base, New: []byte(content)})
		edits = append(edits, gitstore.Edit{
			Path:    rel,
			Base:    f.Base,
			HasBase: !f.New,
			Content: []byte(content),
			Force:   forced[rel],
		})
	}
	res, err := store.Save(m.git.seen, edits, org.CommitMessage("orgtd: ", diffs))
	if err != nil {
		return "Failed to save: " + err.Error(), false
	}
	if res.Conflicts != nil {
		m.git.conflict = &saveConflict{files: res.Conflicts}
		return conflictMessage(m.git.conflict), false
	}
	m.git.conflict = nil

	anchor := m.cursorAnchor()
	var merged []string
	saved := map[*org.File]bool{}
	for _, f := range files {
		rel := m.relPath(f)
		written, ok := res.Written[rel]
		delete(m.git.mustSave, f)
		saved[f] = true
		if !ok {
			// Deleted on the main branch, and unchanged here: it stays
			// deleted, and there's nothing of ours to keep.
			m.markSaved(f)
			continue
		}
		if string(written) == rendered[f] {
			f.Base, f.New = written, false
			m.markSaved(f)
			continue
		}
		// The merge brought in another instance's changes: show them.
		newFile, err := org.ParseContent(written, f.Path)
		if err != nil {
			m.markSaved(f)
			continue
		}
		m.swapFile(f, newFile)
		merged = append(merged, rel)
	}
	m.recomputeDirty()
	if len(merged) > 0 {
		m.rebuildRows()
		m.restoreCursor(anchor)
	}
	reloaded, _ := m.reloadChanged(res.Head, res.Files, saved)
	m.dropRecovered()

	msg := "Wrote " + strings.Join(names, ", ")
	if len(merged) > 0 {
		msg += "; merged changes from " + store.MainBranch() + " into " + strings.Join(merged, ", ")
	}
	if len(reloaded) > 0 {
		msg += "; reloaded " + strings.Join(reloaded, ", ")
	}
	return msg, true
}

func conflictMessage(c *saveConflict) string {
	return "Conflict in " + strings.Join(c.paths(), ", ") + " — :resolve to fix, :w! to keep yours, :e! to take theirs"
}

// reloadChanged brings the loaded files up to date with snapshot, the
// managed files at head: each one whose content there differs from what
// it was loaded from (its Base) is reloaded, unless it has unsaved
// edits — those are listed in behind, for the next :w to merge — or
// :format-links is still working on it. Files in skip are left alone
// (just saved). Managed files new on the branch are loaded.
//
// Nothing is reloaded while it isn't safe to (see safeToReload); head is
// recorded as seen only once everything is caught up, so polling tries
// again.
func (m *Model) reloadChanged(head plumbing.Hash, snapshot map[string][]byte, skip map[*org.File]bool) (reloaded, behind []string) {
	if !m.safeToReload() {
		return nil, m.git.behind
	}
	anchor := m.cursorAnchor()
	caughtUp := true
	loaded := map[string]bool{}
	for _, f := range append([]*org.File(nil), m.ws.Files...) {
		rel := m.relPath(f)
		loaded[rel] = true
		content, ok := snapshot[rel]
		if !ok || skip[f] || (!f.New && bytes.Equal(content, f.Base)) {
			continue
		}
		if m.unsaved(f) {
			behind = append(behind, rel)
			continue
		}
		if m.fileHasImmutableHeadline(f) {
			caughtUp = false
			continue
		}
		newFile, err := org.ParseContent(content, f.Path)
		if err != nil {
			caughtUp = false
			continue
		}
		m.swapFile(f, newFile)
		reloaded = append(reloaded, rel)
	}
	var added bool
	for rel, content := range snapshot {
		if loaded[rel] || !m.loadable(rel) {
			continue
		}
		f, err := org.ParseContent(content, filepath.Join(m.ws.Dir, filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		m.ws.Files = append(m.ws.Files, f)
		added = true
		reloaded = append(reloaded, rel)
	}
	if added {
		sort.Slice(m.ws.Files, func(i, j int) bool { return m.ws.Files[i].Path < m.ws.Files[j].Path })
	}
	sort.Strings(reloaded)
	m.git.behind = behind
	if caughtUp {
		m.git.seen = head
	}
	if len(reloaded) > 0 {
		m.rebuildRows()
		m.restoreCursor(anchor)
		m.recomputeDirty()
	}
	return reloaded, behind
}

// loadable reports whether rel, a managed file on the main branch that
// isn't loaded, should be: it's in the org directory or reference/
// (gitstore only reports those), so anything but a dotfile.
func (m *Model) loadable(rel string) bool {
	return !strings.HasPrefix(filepath.Base(rel), ".")
}

// safeToReload reports whether files can be swapped out from under the
// user right now: in normal mode with no command half-typed. Anywhere
// else — insert mode, a picker, a prompt, visual mode — something may be
// holding on to an entry of the file being replaced.
func (m *Model) safeToReload() bool {
	return m.mode == normalMode && m.chord == "" && m.pendingCount == 0 && !m.inInsertSession()
}

// cursorAnchor records where the cursor is, by entry rather than by row,
// so it can be put back on the same entry once files are reloaded (see
// restoreCursor).
type cursorAnchor struct {
	file   string   // the entry's file, or the header row's
	titles []string // the entry's ancestors' titles, then its own
	row    int      // fallback: the row index
}

func (m *Model) cursorAnchor() cursorAnchor {
	a := cursorAnchor{row: m.cursor}
	if f := m.currentRowFile(); f != nil {
		a.file = f.Path
	}
	if h := m.currentHeadline(); h != nil {
		for p := h; p != nil; p = p.Parent {
			a.titles = append([]string{p.Title}, a.titles...)
		}
	}
	return a
}

func (m *Model) restoreCursor(a cursorAnchor) {
	f := m.loadedFile(a.file)
	if f != nil && len(a.titles) > 0 {
		level := f.Headlines
		var found *org.Headline
		for _, t := range a.titles {
			found = nil
			for _, h := range level {
				if h.Title == t {
					found = h
					break
				}
			}
			if found == nil {
				break
			}
			level = found.Children
		}
		if found != nil {
			m.focusHeadline(found)
			return
		}
	}
	m.cursor = min(max(a.row, 0), max(len(m.rows)-1, 0))
}

// reloadFromMain runs ":e!": commit any outside edits (so a change made
// in another editor is what's reloaded), then replace f with the main
// branch's version, discarding its unsaved edits, undo history and
// marks. A file the branch doesn't have is reloaded from disk (one git
// ignores), or emptied if it was never saved.
func (m *Model) reloadFromMain(f *org.File) (*org.File, error) {
	store, err := m.gitStore()
	if err != nil {
		return nil, err
	}
	res, err := store.CommitOutsideEdits()
	if err != nil && !errors.Is(err, gitstore.ErrWrongBranch) {
		return nil, err
	}
	rel := m.relPath(f)
	var newFile *org.File
	if content, ok := res.Files[rel]; ok {
		newFile, err = org.ParseContent(content, f.Path)
	} else {
		newFile, err = org.ParseFile(f.Path)
		if f.New && err != nil {
			newFile, err = &org.File{Path: f.Path, New: true}, nil
		}
	}
	if err != nil {
		return nil, err
	}
	m.swapFile(f, newFile)
	delete(m.git.mustSave, f)
	if c := m.git.conflict; c != nil {
		delete(c.files, rel)
		if len(c.files) == 0 {
			m.git.conflict = nil
		}
	}
	return newFile, nil
}

// dropRecovered deletes the pending refs of orphans :recover loaded,
// once a save has got their edits into the main branch.
func (m *Model) dropRecovered() {
	if len(m.git.recovering) == 0 {
		return
	}
	for _, o := range m.git.recovering {
		m.git.store.Drop(o)
	}
	m.git.recovering = nil
}

// gitStatus is the status line's note about the repository, if any: a
// pending conflict, or unsaved files the main branch changed too.
func (m *Model) gitStatus() string {
	switch {
	case m.git.conflict != nil:
		return "conflict in " + strings.Join(m.git.conflict.paths(), ", ") + " — :resolve"
	case len(m.git.behind) > 0:
		branch := m.cfg.MainBranch
		if m.git.store != nil {
			branch = m.git.store.MainBranch()
		}
		return fmt.Sprintf("%s moved — :w merges", branch)
	case m.git.mobile != "":
		return m.git.mobile
	}
	return ""
}

// instancesStatus is the status line's note about other orgtd instances
// running on the same org directory, if there are any.
func (m *Model) instancesStatus() string {
	switch n := m.git.others; n {
	case 0:
		return ""
	case 1:
		return "1 other instance"
	default:
		return fmt.Sprintf("%d other instances", n)
	}
}

// mobileBranch is the branch the iOS app pushes its changes to
// (gitsync.MobileBranch; the desktop doesn't import gitsync), which
// pollMain brings into the main branch.
const mobileBranch = "mobile"

// mobileNote is the status line's note on why the phone's branch
// wasn't brought in, and what to do about it; empty if it was, or there
// was nothing to do.
func mobileNote(res gitstore.BranchResult) string {
	switch {
	case res.Conflict != nil:
		return "phone changes conflict in " + strings.Join(sortedKeys(res.Conflict.Files), ", ") + " — :resolve"
	case len(res.Blocked) > 0:
		return mobileBranch + " waits on outside edits to " + strings.Join(res.Blocked, ", ") + " — :w"
	case len(res.Unmanaged) > 0:
		return mobileBranch + " changes " + strings.Join(res.Unmanaged, ", ") + " — run git merge " + mobileBranch
	}
	return ""
}

// noteMobile records res, from ApplyBranch or ResolveBranch, for the
// status line and :resolve.
func (m *Model) noteMobile(res gitstore.BranchResult) {
	m.git.mobile = mobileNote(res)
	m.git.mobileConflict = res.Conflict
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// pollInterval is how often pollMain checks the main branch.
const pollInterval = 2 * time.Second

// gitPollMsg asks pollMain to check the main branch.
type gitPollMsg struct{}

func pollTick() tea.Cmd {
	return tea.Tick(pollInterval, func(time.Time) tea.Msg { return gitPollMsg{} })
}

// pollMain runs on every gitPollMsg: it recounts the other instances
// running on the org directory (see instancesStatus), brings the iOS
// app's branch into the main branch — a fast-forward, or a merge — if it
// can (see gitstore.ApplyBranch), and if the main branch moved since this
// instance last caught up with it (another instance saved, the phone's
// changes came in, or a commit or pull from outside), reload the files
// it changed (see reloadChanged).
// Files with unsaved edits aren't reloaded; the status line says the
// branch moved, and the next :w merges. Edits made outside orgtd and not
// yet committed aren't picked up until a save or :run commits them.
func (m Model) pollMain() (tea.Model, tea.Cmd) {
	next := pollTick()
	store := m.git.store
	if store == nil {
		return m, next
	}
	if n, err := store.OtherInstances(); err == nil {
		m.git.others = n
	}
	// Errors (HEAD on another branch, a git command holding the index)
	// just mean trying again next time.
	ff, _ := store.ApplyBranch(mobileBranch)
	m.noteMobile(ff)
	head, err := store.Head()
	if err != nil || head == m.git.seen {
		return m, next
	}
	snapshot, err := store.Snapshot(head)
	if err != nil {
		return m, next
	}
	reloaded, _ := m.reloadChanged(head, snapshot, nil)
	switch {
	case ff.Merged:
		m.message = "Merged changes from " + mobileBranch + " into " + strings.Join(ff.Changed, ", ")
	case ff.Moved:
		m.message = "Applied changes from " + mobileBranch + " to " + strings.Join(ff.Changed, ", ")
	case len(reloaded) > 0:
		m.message = "Reloaded " + strings.Join(reloaded, ", ") + " (changed on " + store.MainBranch() + ")"
	}
	return m, next
}
