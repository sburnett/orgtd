package gitstore

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/gofrs/flock"
)

// errMainMoved is returned by moveMain when the main branch no longer
// points where the caller expected.
var errMainMoved = errors.New("main branch moved")

// moveMain moves the main branch from old to new (zero old: the branch
// must not exist yet), the way git itself updates a ref: take
// refs/heads/<main>.lock, check the current value, write the new one into
// the lock file and rename it into place. Git's own commands take the
// same lock, so they can't interleave with this. Appends to the branch's
// and HEAD's reflogs, as git does, so `git reflog` shows orgtd's saves.
func (s *Store) moveMain(repo *git.Repository, old, new plumbing.Hash, msg string) error {
	refPath := filepath.Join(s.gitDir, filepath.FromSlash(s.mainRef().String()))
	if err := os.MkdirAll(filepath.Dir(refPath), 0o755); err != nil {
		return err
	}
	lockPath := refPath + ".lock"
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%s is locked by another git process (%s exists)", s.mainRef().Short(), lockPath)
	}
	if err != nil {
		return err
	}
	done := false
	defer func() {
		if !done {
			f.Close()
			os.Remove(lockPath)
		}
	}()
	cur, err := headOf(repo, s.mainRef())
	if err != nil {
		return err
	}
	if cur != old {
		return errMainMoved
	}
	if _, err := f.WriteString(new.String() + "\n"); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(lockPath, refPath); err != nil {
		return err
	}
	done = true
	s.appendReflog(repo, old, new, msg)
	return nil
}

// appendReflog records a move of the main branch in its reflog and, when
// HEAD points at it, HEAD's — unless core.logAllRefUpdates is off (it's
// on by default in a repository with a working tree). Best-effort: a
// reflog is a convenience for recovering by hand.
func (s *Store) appendReflog(repo *git.Repository, old, new plumbing.Hash, msg string) {
	sig := signature(repo)
	_, offset := sig.When.Zone()
	sign := "+"
	if offset < 0 {
		sign, offset = "-", -offset
	}
	line := fmt.Sprintf("%s %s %s <%s> %d %s%02d%02d\t%s\n",
		old, new, sig.Name, sig.Email, sig.When.Unix(), sign, offset/3600, (offset%3600)/60,
		strings.SplitN(msg, "\n", 2)[0])
	logs := []string{filepath.Join(s.gitDir, "logs", filepath.FromSlash(s.mainRef().String()))}
	if head, err := repo.Storer.Reference(plumbing.HEAD); err == nil && head.Target() == s.mainRef() {
		logs = append(logs, filepath.Join(s.gitDir, "logs", "HEAD"))
	}
	if cfg, err := repo.Config(); err == nil && cfg.Raw.Section("core").Option("logallrefupdates") == "false" {
		return
	}
	for _, p := range logs {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			continue
		}
		f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			continue
		}
		f.WriteString(line)
		f.Close()
	}
}

// setPending points this instance's pending ref at commit.
func (s *Store) setPending(repo *git.Repository, commit plumbing.Hash) error {
	return repo.Storer.SetReference(plumbing.NewHashReference(s.pendingRef(s.id), commit))
}

func (s *Store) clearPending(repo *git.Repository, id string) error {
	err := repo.Storer.RemoveReference(s.pendingRef(id))
	if errors.Is(err, plumbing.ErrReferenceNotFound) {
		return nil
	}
	return err
}

// tryLockMerge takes the merge lock if no one holds it: neither another
// orgtd process (mergeLock) nor another goroutine of this one (mergeMu).
// Release it with unlockMerge.
func (s *Store) tryLockMerge() (bool, error) {
	if !s.mergeMu.TryLock() {
		return false, nil
	}
	ok, err := s.mergeLock.TryLock()
	if !ok || err != nil {
		s.mergeMu.Unlock()
	}
	return ok, err
}

func (s *Store) unlockMerge() {
	s.mergeLock.Unlock()
	s.mergeMu.Unlock()
}

// lockMerge takes the merge lock, waiting up to a few seconds for
// another save (or a poll bringing in the phone's branch) to finish.
func (s *Store) lockMerge() error {
	deadline := time.Now().Add(10 * time.Second)
	for {
		ok, err := s.tryLockMerge()
		if err != nil {
			return fmt.Errorf("taking merge lock: %w", err)
		}
		if ok {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("another orgtd instance is still saving; try again")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// saveTrailer names, in a merged commit's message, the save commit it
// came from, so the startup check can tell a dead instance's pending ref
// was already merged (see Orphans).
const saveTrailer = "Orgtd-Save: "

// Orphan is a dead instance's pending ref: edits it was saving, or was
// stuck on a conflict with, when it exited.
type Orphan struct {
	ID     string        // the instance's ID
	Commit plumbing.Hash // its save commit
	Base   plumbing.Hash // the save commit's parent
	When   time.Time     // when it was saved
	Paths  []string      // managed paths the save changed
}

// orphanScanDepth is how far back the main branch's history Orphans looks
// for a save commit's trailer.
const orphanScanDepth = 1000

// Orphans lists the pending refs of dead instances: those whose instance
// lock nobody holds. A ref whose save already reached the main branch
// (the instance died between moving the branch and deleting the ref) is
// deleted on the spot; the others are returned, for Recover or Drop.
func (s *Store) Orphans() ([]Orphan, error) {
	repo, err := s.open()
	if err != nil {
		return nil, err
	}
	refs, err := repo.References()
	if err != nil {
		return nil, err
	}
	var candidates []*plumbing.Reference
	refs.ForEach(func(r *plumbing.Reference) error {
		name := r.Name().String()
		if strings.HasPrefix(name, "refs/orgtd/") && r.Type() == plumbing.HashReference {
			candidates = append(candidates, r)
		}
		return nil
	})
	if len(candidates) == 0 {
		return nil, nil
	}

	merged := map[plumbing.Hash]bool{}
	if head, err := headOf(repo, s.mainRef()); err == nil && !head.IsZero() {
		c, err := repo.CommitObject(head)
		for n := 0; err == nil && n < orphanScanDepth; n++ {
			for _, line := range strings.Split(c.Message, "\n") {
				if h, ok := strings.CutPrefix(line, saveTrailer); ok {
					merged[plumbing.NewHash(strings.TrimSpace(h))] = true
				}
			}
			if len(c.ParentHashes) == 0 {
				break
			}
			c, err = repo.CommitObject(c.ParentHashes[0])
		}
	}

	var out []Orphan
	for _, r := range candidates {
		id := strings.TrimPrefix(r.Name().String(), "refs/orgtd/")
		if id == s.id || !s.instanceDead(id) {
			continue
		}
		if merged[r.Hash()] {
			s.clearPending(repo, id)
			continue
		}
		c, err := repo.CommitObject(r.Hash())
		if err != nil {
			continue
		}
		o := Orphan{ID: id, Commit: r.Hash(), When: c.Author.When}
		if len(c.ParentHashes) > 0 {
			o.Base = c.ParentHashes[0]
		}
		before, after, err := s.commitFiles(repo, o.Base, o.Commit)
		if err != nil {
			continue
		}
		for p, data := range after {
			if old, ok := before[p]; !ok || string(old) != string(data) {
				o.Paths = append(o.Paths, p)
			}
		}
		out = append(out, o)
	}
	return out, nil
}

// instanceDead reports whether the instance with this ID has exited:
// nobody holds its instance lock.
func (s *Store) instanceDead(id string) bool {
	fl := flock.New(s.instanceLockPath(id))
	ok, err := fl.TryLock()
	if err != nil || !ok {
		return false
	}
	fl.Unlock()
	os.Remove(fl.Path())
	return true
}

func (s *Store) commitFiles(repo *git.Repository, base, commit plumbing.Hash) (before, after files, err error) {
	bt, err := treeOf(repo, base)
	if err != nil {
		return nil, nil, err
	}
	at, err := treeOf(repo, commit)
	if err != nil {
		return nil, nil, err
	}
	if before, err = s.managedFiles(repo, bt); err != nil {
		return nil, nil, err
	}
	if after, err = s.managedFiles(repo, at); err != nil {
		return nil, nil, err
	}
	return before, after, nil
}

// OrphanEdits returns the edits an orphan was saving: each path it
// changed, with its content before (the orphan's base) and after. Saving
// them (see Save) recovers the orphan; Drop it once that succeeds.
func (s *Store) OrphanEdits(o Orphan) ([]Edit, error) {
	repo, err := s.open()
	if err != nil {
		return nil, err
	}
	before, after, err := s.commitFiles(repo, o.Base, o.Commit)
	if err != nil {
		return nil, err
	}
	var edits []Edit
	for _, p := range o.Paths {
		old, hadBase := before[p]
		edits = append(edits, Edit{Path: p, Base: old, HasBase: hadBase, Content: after[p]})
	}
	return edits, nil
}

// Drop deletes an orphan's pending ref.
func (s *Store) Drop(o Orphan) error {
	repo, err := s.open()
	if err != nil {
		return err
	}
	return s.clearPending(repo, o.ID)
}

// History returns up to n of the main branch's most recent commits'
// subjects, newest first — for tests and diagnostics.
func (s *Store) History(n int) ([]string, error) {
	repo, err := s.open()
	if err != nil {
		return nil, err
	}
	head, err := headOf(repo, s.mainRef())
	if err != nil || head.IsZero() {
		return nil, err
	}
	var out []string
	iter, err := repo.Log(&git.LogOptions{From: head})
	if err != nil {
		return nil, err
	}
	iter.ForEach(func(c *object.Commit) error {
		if len(out) >= n {
			return errStop
		}
		out = append(out, strings.SplitN(c.Message, "\n", 2)[0])
		return nil
	})
	return out, nil
}

var errStop = errors.New("stop")
