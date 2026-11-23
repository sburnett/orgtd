package gitstore

import (
	"bytes"
	"errors"
	"fmt"
	"sort"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"

	"github.com/sburnett/orgtd/internal/merge3"
	"github.com/sburnett/orgtd/internal/org"
)

// Edit is one file a save writes.
type Edit struct {
	Path string // slash-separated, relative to the org directory

	// Base is the content this edit started from — what the file held
	// when the instance last loaded, reloaded or saved it — and the
	// common ancestor of the three-way merge. HasBase is false for a
	// file that didn't exist then (a new file).
	Base    []byte
	HasBase bool

	Content []byte // the instance's version

	// Force takes Content as the result whatever the main branch has
	// (":w!" on a conflicted file, or a regenerated cache file).
	Force bool
}

// Conflict is a file a save couldn't merge.
type Conflict struct {
	Merged []byte // the merge result, with conflict markers
	Theirs []byte // the main branch's version; nil if deleted there
	Count  int    // how many conflict blocks Merged has; 0 for a delete/modify conflict
}

// Result is the outcome of Save or CommitOutsideEdits.
type Result struct {
	// Head is where the main branch is now: the instance's new base.
	// For a conflicted save, the main branch the conflict was computed
	// against (it didn't move).
	Head plumbing.Hash

	// Files is every managed file at Head, for the caller to reload
	// whatever differs from what it has in memory.
	Files map[string][]byte

	// Written is the content now on disk for every edited path that was
	// saved: the merge result, which may differ from the edit's Content
	// when the main branch changed it too. It includes paths git
	// ignores, which are written straight to disk rather than committed.
	Written map[string][]byte

	// Conflicts is set when the save couldn't merge cleanly. Then
	// nothing was written or committed (beyond the pending ref and any
	// outside edits), and Written is nil.
	Conflicts map[string]Conflict
}

// Labels used in conflict markers.
var conflictLabels = merge3.Labels{Ours: "yours (this orgtd)", Theirs: "main"}

// Save commits edits to the main branch, merging them with whatever
// reached it since base (the commit this instance last loaded or saved
// from; zero if none) — other instances' saves and edits made outside
// orgtd alike. msg is the commit's subject. Steps (see DESIGN.md, "Saving
// through git"):
//
//  1. Commit the edits on top of base: the save commit, S.
//  2. Point the pending ref refs/orgtd/<ID> at S, so a crash can't lose
//     them.
//  3. Take the merge lock; read the main branch, M.
//  4. Commit any outside edits (working tree ≠ M) on top of M.
//  5. Three-way merge each edit with M's version of its path. On a
//     conflict, stop: the pending ref stays, nothing else changes.
//  6. Commit the merged tree with M as its only parent: R.
//  7. Take .git/index.lock, write every file that differs between M and
//     R, write the new index, move the main branch from M to R, and
//     install the index. Files go to disk before the branch moves, so a
//     crash in between leaves them looking like outside edits with the
//     merged content, never like a revert.
//  8. Delete the pending ref, release the merge lock.
func (s *Store) Save(base plumbing.Hash, edits []Edit, msg string) (_ Result, err error) {
	if err := s.CheckBranch(); err != nil {
		return Result{}, err
	}
	repo, err := s.open()
	if err != nil {
		return Result{}, err
	}

	// Steps 1-2. Only the edits git tracks go in the save commit.
	baseTree, err := treeOf(repo, base)
	if err != nil {
		return Result{}, err
	}
	baseFiles, err := s.managedFiles(repo, baseTree)
	if err != nil {
		return Result{}, err
	}
	saveChanges := map[string][]byte{}
	for _, e := range edits {
		if !s.IsManaged(e.Path) {
			return Result{}, fmt.Errorf("%s is not a file orgtd manages", e.Path)
		}
		if !s.isUntrackedIgnored(e.Path, baseFiles) {
			saveChanges[e.Path] = nonNil(e.Content)
		}
	}
	var saveCommit plumbing.Hash
	if len(saveChanges) > 0 {
		t, err := editTree(repo, baseTree, saveChanges)
		if err != nil {
			return Result{}, err
		}
		if saveCommit, err = commit(repo, t, base, "orgtd: pending save"); err != nil {
			return Result{}, err
		}
		if err := s.setPending(repo, saveCommit); err != nil {
			return Result{}, err
		}
	}
	if err := s.step("pinned"); err != nil {
		return Result{}, err
	}

	// Step 3.
	if err := s.lockMerge(); err != nil {
		return Result{}, err
	}
	// A simulated crash leaves the merge lock held, as a dead process's
	// flock stays held until the OS drops it (Close, in tests), but this
	// goroutine is gone either way: let Close have mergeMu.
	defer func() {
		if errors.Is(err, errCrash) {
			s.mergeMu.Unlock()
		}
	}()
	if err := s.step("locked"); err != nil {
		return Result{}, err
	}

	for attempt := 0; ; attempt++ {
		res, err := s.saveLocked(repo, edits, saveCommit, msg)
		if errors.Is(err, errMainMoved) && attempt < 3 {
			continue
		}
		if err != nil {
			if !errors.Is(err, errCrash) {
				s.unlockMerge()
			}
			return Result{}, err
		}
		if res.Conflicts != nil {
			s.unlockMerge()
			return res, nil
		}
		// Step 8.
		if !saveCommit.IsZero() {
			if err := s.clearPending(repo, s.id); err != nil {
				s.unlockMerge()
				return Result{}, err
			}
		}
		if err := s.step("cleaned"); err != nil {
			return Result{}, err
		}
		s.unlockMerge()
		return res, nil
	}
}

// saveLocked is Save's steps 3-7, run with the merge lock held. It
// returns errMainMoved if the main branch moved under it, having put
// back any files it wrote, so Save can start again from step 3.
func (s *Store) saveLocked(repo *git.Repository, edits []Edit, saveCommit plumbing.Hash, msg string) (Result, error) {
	m, mFiles, err := s.commitOutsideLocked(repo)
	if err != nil {
		return Result{}, err
	}
	if err := s.step("outside-committed"); err != nil {
		return Result{}, err
	}

	// Step 5.
	changes := map[string][]byte{} // tracked paths: new content, nil = delete
	direct := map[string][]byte{}  // untracked, ignored paths: written straight to disk
	written := map[string][]byte{}
	var conflicts map[string]Conflict
	for _, e := range edits {
		content := nonNil(e.Content)
		if s.isUntrackedIgnored(e.Path, mFiles) {
			direct[e.Path] = content
			written[e.Path] = content
			continue
		}
		theirs, theirsExists := mFiles[e.Path]
		var merged []byte
		var n int
		switch {
		case e.Force:
			merged = content
		case !theirsExists && e.HasBase:
			// Deleted on the main branch since this instance loaded it.
			if bytes.Equal(content, e.Base) {
				continue // unchanged here too: stays deleted
			}
			if conflicts == nil {
				conflicts = map[string]Conflict{}
			}
			conflicts[e.Path] = Conflict{Merged: content}
			continue
		default:
			merged, n = merge3.Merge(e.Base, content, theirs, conflictLabels)
		}
		if n > 0 {
			if conflicts == nil {
				conflicts = map[string]Conflict{}
			}
			conflicts[e.Path] = Conflict{Merged: merged, Theirs: theirs, Count: n}
			continue
		}
		if !theirsExists || !bytes.Equal(merged, theirs) {
			changes[e.Path] = merged
		}
		written[e.Path] = merged
	}
	if conflicts != nil {
		return Result{Head: m, Files: mFiles, Conflicts: conflicts}, nil
	}
	if err := s.step("merged"); err != nil {
		return Result{}, err
	}

	// Step 6.
	r := m
	rFiles := mFiles
	if len(changes) > 0 {
		mTree, err := treeOf(repo, m)
		if err != nil {
			return Result{}, err
		}
		rTree, err := editTree(repo, mTree, changes)
		if err != nil {
			return Result{}, err
		}
		full := msg
		if !saveCommit.IsZero() {
			full += "\n\n" + saveTrailer + saveCommit.String() + "\n"
		}
		if r, err = commit(repo, rTree, m, full); err != nil {
			return Result{}, err
		}
		rFiles = files{}
		for p, d := range mFiles {
			rFiles[p] = d
		}
		for p, d := range changes {
			if d == nil {
				delete(rFiles, p)
			} else {
				rFiles[p] = d
			}
		}
	}

	// Step 7.
	if err := s.apply(repo, m, r, mFiles, changes, msg); err != nil {
		return Result{}, err
	}
	for p, d := range direct {
		if err := s.writeFile(p, d); err != nil {
			return Result{}, err
		}
	}
	return Result{Head: r, Files: rFiles, Written: written}, nil
}

// apply is step 7: bring the working tree, the index and the main branch
// from m to r, where changes is what differs. mFiles is m's managed
// files, to put back if the branch moved meanwhile.
func (s *Store) apply(repo *git.Repository, m, r plumbing.Hash, mFiles files, changes map[string][]byte, msg string) error {
	if m == r {
		return nil
	}
	lock, err := s.lockIndex()
	if err != nil {
		return err
	}
	if cur, err := headOf(repo, s.mainRef()); err != nil || cur != m {
		s.unlockIndex(lock)
		if err != nil {
			return err
		}
		return errMainMoved
	}
	paths := sortedKeys(changes)
	for _, p := range paths {
		if err := s.writeFile(p, changes[p]); err != nil {
			s.restore(mFiles, paths)
			s.unlockIndex(lock)
			return err
		}
	}
	if err := s.step("files-written"); err != nil {
		return err
	}
	if err := s.writeIndex(repo, lock, changes); err != nil {
		s.restore(mFiles, paths)
		s.unlockIndex(lock)
		return err
	}
	if err := s.step("index-written"); err != nil {
		return err
	}
	if err := s.moveMain(repo, m, r, msg); err != nil {
		// Put the files back as the branch has them, so retrying sees no
		// phantom outside edits.
		s.restore(mFiles, paths)
		s.unlockIndex(lock)
		return err
	}
	if err := s.step("ref-moved"); err != nil {
		return err
	}
	return s.commitIndex(lock)
}

// restore writes m's version of each path back to disk (removing paths m
// doesn't have), undoing a half-applied save.
func (s *Store) restore(mFiles files, paths []string) {
	for _, p := range paths {
		s.writeFile(p, mFiles[p])
	}
}

// CommitOutsideEdits commits any managed file that differs between the
// working tree and the main branch (Emacs, a hand edit, a :run command),
// as its own commit, and returns the main branch's new state. Nothing
// is written to the working tree. With no outside edits, it just
// reports the main branch.
func (s *Store) CommitOutsideEdits() (Result, error) {
	if err := s.CheckBranch(); err != nil {
		return Result{}, err
	}
	repo, err := s.open()
	if err != nil {
		return Result{}, err
	}
	if err := s.lockMerge(); err != nil {
		return Result{}, err
	}
	defer s.unlockMerge()
	for attempt := 0; ; attempt++ {
		head, headFiles, err := s.commitOutsideLocked(repo)
		if errors.Is(err, errMainMoved) && attempt < 3 {
			continue
		}
		if err != nil {
			return Result{}, err
		}
		return Result{Head: head, Files: headFiles}, nil
	}
}

// commitOutsideLocked reads the main branch and commits outside edits on
// top of it (see CommitOutsideEdits), with the merge lock held. It
// returns the resulting head and its managed files.
func (s *Store) commitOutsideLocked(repo *git.Repository) (plumbing.Hash, files, error) {
	m, err := headOf(repo, s.mainRef())
	if err != nil {
		return plumbing.ZeroHash, nil, err
	}
	mTree, err := treeOf(repo, m)
	if err != nil {
		return plumbing.ZeroHash, nil, err
	}
	mFiles, err := s.managedFiles(repo, mTree)
	if err != nil {
		return plumbing.ZeroHash, nil, err
	}
	outside, err := s.outsideEdits(mFiles)
	if err != nil {
		return plumbing.ZeroHash, nil, err
	}
	final := files{}
	for p, d := range mFiles {
		final[p] = d
	}
	for p, d := range outside {
		if d == nil {
			delete(final, p)
		} else {
			final[p] = d
		}
	}
	// Index entries to update: the outside edits, plus any managed path
	// whose entry doesn't match the branch — left stale by a save that
	// died between moving the branch and installing its index.
	indexChanges := map[string][]byte{}
	for p, d := range outside {
		indexChanges[p] = d
	}
	for p, d := range final {
		if h, ok := indexHash(repo, p); !ok || h != blobHash(d) {
			indexChanges[p] = d
		}
	}
	if len(indexChanges) == 0 {
		return m, mFiles, nil
	}
	lock, err := s.lockIndex()
	if err != nil {
		return plumbing.ZeroHash, nil, err
	}
	if err := s.writeIndex(repo, lock, indexChanges); err != nil {
		s.unlockIndex(lock)
		return plumbing.ZeroHash, nil, err
	}
	head := m
	if len(outside) > 0 {
		tree, err := editTree(repo, mTree, outside)
		if err == nil {
			var diffs []org.FileDiff
			for _, p := range sortedKeys(outside) {
				diffs = append(diffs, org.FileDiff{Path: p, Old: mFiles[p], New: outside[p], Deleted: outside[p] == nil})
			}
			msg := org.CommitMessage("orgtd: outside edits to ", diffs)
			if head, err = commit(repo, tree, m, msg); err == nil {
				err = s.moveMain(repo, m, head, msg)
			}
		}
		if err != nil {
			s.unlockIndex(lock)
			return plumbing.ZeroHash, nil, err
		}
	}
	if err := s.commitIndex(lock); err != nil {
		return plumbing.ZeroHash, nil, err
	}
	return head, final, nil
}

// errCrash wraps an error from failAt: a simulated crash, after which
// Save leaves everything as a dying process would.
var errCrash = errors.New("simulated crash")

func (s *Store) step(name string) error {
	if s.failAt == nil {
		return nil
	}
	if err := s.failAt(name); err != nil {
		return fmt.Errorf("%w at %s: %v", errCrash, name, err)
	}
	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// nonNil turns nil content into empty content: nil means "deleted" in
// editTree's changes, and an edit always has content, if only none.
func nonNil(b []byte) []byte {
	if b == nil {
		return []byte{}
	}
	return b
}
