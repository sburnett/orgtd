package gitstore

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/sburnett/orgtd/internal/merge3"
	"github.com/sburnett/orgtd/internal/org"
)

// BranchResult is what ApplyBranch or ResolveBranch did.
type BranchResult struct {
	// Moved reports whether the main branch moved; Head and Files are
	// then its new state, as from Save.
	Moved bool
	Result

	// Merged reports that the move was a merge commit (main and the
	// branch's tip as its parents), not a fast-forward.
	Merged bool

	// Changed is the managed paths the move changed, when Moved.
	Changed []string

	// Blocked is the managed paths the move would change that have
	// uncommitted edits on disk, when that's all that stopped it. It
	// waits for them to be committed (see CommitOutsideEdits).
	Blocked []string

	// Unmanaged is the paths the branch changes that orgtd doesn't
	// manage, which stop it for good: `git merge <branch>` in the org
	// directory finishes the job.
	Unmanaged []string

	// Conflict is set when the branch can't be merged cleanly. Nothing
	// moved; ResolveBranch takes the resolved files.
	Conflict *BranchConflict
}

// BranchConflict is a merge of a branch into main that collided.
type BranchConflict struct {
	Branch string
	Tip    plumbing.Hash // the branch's tip the merge was of
	Main   plumbing.Hash // the main branch it was merged with

	// Files is each path that collided: Merged has conflict markers
	// (Ours is the branch's side), Theirs is main's version.
	Files map[string]Conflict
}

// ErrBranchMoved is returned by ResolveBranch when the branch moved
// since the conflict was found: the next ApplyBranch merges it afresh.
var ErrBranchMoved = errors.New("the branch moved since its conflict was found")

// Labels used in conflict markers between a branch and main.
func branchLabels(branch string) merge3.Labels {
	return merge3.Labels{Ours: branch, Theirs: "main"}
}

// branchPair is a main/branch pair whose outcome ApplyBranch has worked
// out from the commits alone, and that outcome.
type branchPair struct {
	main, tip plumbing.Hash
	res       BranchResult
}

// ApplyBranch brings branch (a local branch, such as the one the iOS app
// pushes its changes to) into the main branch. When main is an ancestor
// of the branch's tip, it fast-forwards: main moves to the tip, with no
// new commit. Otherwise, it three-way merges the branch into main (from
// their merge base) and, if that's clean, commits the result with main
// and the tip as parents. Either way the tip ends up an ancestor of main,
// which is how the iOS app tells its changes have landed. If the merge
// collides, nothing moves and Conflict says where, for ResolveBranch.
//
// It does nothing if the branch is missing or already merged; if it
// changes paths orgtd doesn't manage, which it would leave stale on disk
// (listed in Unmanaged); or if it would change files with uncommitted
// edits on disk (listed in Blocked). If another instance is saving, it
// doesn't wait: the next call tries again.
//
// The working tree, the index and the branch are brought up to date as a
// save's are (see apply), files first, so a crash partway leaves the
// files looking like outside edits with the new content — which the next
// call finds already in place and finishes.
func (s *Store) ApplyBranch(branch string) (BranchResult, error) {
	if err := s.CheckBranch(); err != nil {
		return BranchResult{}, err
	}
	repo, err := s.open()
	if err != nil {
		return BranchResult{}, err
	}
	// Cheap checks first: this runs every couple of seconds.
	tip, err := headOf(repo, plumbing.NewBranchReferenceName(branch))
	if err != nil || tip.IsZero() {
		return BranchResult{}, err
	}
	m, err := headOf(repo, s.mainRef())
	if err != nil || m.IsZero() || m == tip {
		return BranchResult{}, err
	}
	if s.notFF.main == m && s.notFF.tip == tip {
		return s.notFF.res, nil
	}

	ok, err := s.mergeLock.TryLock()
	if err != nil || !ok {
		return BranchResult{}, err
	}
	defer s.mergeLock.Unlock()
	if m, err = headOf(repo, s.mainRef()); err != nil {
		return BranchResult{}, err
	}
	if m == tip {
		return BranchResult{}, nil
	}
	remember := func(res BranchResult) (BranchResult, error) {
		s.notFF = branchPair{main: m, tip: tip, res: res}
		return res, nil
	}
	if merged, err := isAncestor(repo, tip, m); err != nil {
		return BranchResult{}, err
	} else if merged {
		return remember(BranchResult{})
	}
	ff, err := isAncestor(repo, m, tip)
	if err != nil {
		return BranchResult{}, err
	}
	mb, err := s.mergeBranch(repo, branch, m, tip, nil)
	if err != nil {
		return BranchResult{}, err
	}
	if mb.unmanaged != nil {
		return remember(BranchResult{Unmanaged: mb.unmanaged})
	}
	if mb.conflicts != nil {
		return remember(BranchResult{Conflict: &BranchConflict{Branch: branch, Tip: tip, Main: m, Files: mb.conflicts}})
	}
	res, err := s.moveTo(repo, branch, m, tip, ff, mb)
	if errors.Is(err, errMainMoved) {
		return BranchResult{}, nil // the next call looks again
	}
	return res, err
}

// ResolveBranch finishes a merge that ApplyBranch found conflicted, with
// resolved holding the resolved content of each of c's files. If main
// moved since, the resolved files are merged with it in turn (from main
// as c had it), which can conflict anew; paths the branch changes that
// didn't conflict are merged afresh. It returns ErrBranchMoved if the
// branch moved since c, and an error if a conflicted path is missing
// from resolved. Unlike ApplyBranch it waits for the merge lock.
func (s *Store) ResolveBranch(c *BranchConflict, resolved map[string][]byte) (BranchResult, error) {
	if err := s.CheckBranch(); err != nil {
		return BranchResult{}, err
	}
	for p := range c.Files {
		if _, ok := resolved[p]; !ok {
			return BranchResult{}, fmt.Errorf("no resolution for %s", p)
		}
	}
	repo, err := s.open()
	if err != nil {
		return BranchResult{}, err
	}
	if err := s.lockMerge(); err != nil {
		return BranchResult{}, err
	}
	defer s.mergeLock.Unlock()
	for attempt := 0; ; attempt++ {
		tip, err := headOf(repo, plumbing.NewBranchReferenceName(c.Branch))
		if err != nil {
			return BranchResult{}, err
		}
		if tip != c.Tip {
			return BranchResult{}, ErrBranchMoved
		}
		m, err := headOf(repo, s.mainRef())
		if err != nil {
			return BranchResult{}, err
		}
		if merged, err := isAncestor(repo, tip, m); err != nil {
			return BranchResult{}, err
		} else if merged {
			return BranchResult{}, nil // another instance got there first
		}
		mb, err := s.mergeBranch(repo, c.Branch, m, tip, &resolutions{conflict: c, files: resolved})
		if err != nil {
			return BranchResult{}, err
		}
		if mb.unmanaged != nil {
			return BranchResult{Unmanaged: mb.unmanaged}, nil
		}
		if mb.conflicts != nil {
			return BranchResult{Conflict: &BranchConflict{Branch: c.Branch, Tip: tip, Main: m, Files: mb.conflicts}}, nil
		}
		res, err := s.moveTo(repo, c.Branch, m, tip, false, mb)
		if errors.Is(err, errMainMoved) && attempt < 3 {
			continue
		}
		return res, err
	}
}

// resolutions are a conflict's resolved files, for mergeBranch.
type resolutions struct {
	conflict *BranchConflict
	files    map[string][]byte
}

// branchMerge is mergeBranch's outcome: either unmanaged paths that stop
// it, conflicts, or the merged content of every changed path.
type branchMerge struct {
	unmanaged []string
	conflicts map[string]Conflict
	mFiles    files             // main's managed files
	changes   map[string][]byte // path → content main moves to; nil: delete
}

// mergeBranch three-way merges the managed files tip changed since its
// merge base with m into m's, taking res's resolution of any path it
// covers (merged in turn with m, if m changed that path since). It
// changes nothing.
func (s *Store) mergeBranch(repo *git.Repository, branch string, m, tip plumbing.Hash, res *resolutions) (branchMerge, error) {
	base, err := mergeBase(repo, m, tip)
	if err != nil {
		return branchMerge{}, err
	}
	trees := map[plumbing.Hash]plumbing.Hash{}
	for _, c := range []plumbing.Hash{base, m, tip} {
		if c.IsZero() {
			continue
		}
		if trees[c], err = treeOf(repo, c); err != nil {
			return branchMerge{}, err
		}
	}
	touched, err := changedPaths(repo, trees[base], trees[tip])
	if err != nil {
		return branchMerge{}, err
	}
	var unmanaged []string
	for _, p := range touched {
		if !s.IsManaged(p) {
			unmanaged = append(unmanaged, p)
		}
	}
	if len(unmanaged) > 0 {
		return branchMerge{unmanaged: unmanaged}, nil
	}
	baseFiles, err := s.managedFiles(repo, trees[base])
	if err != nil {
		return branchMerge{}, err
	}
	mFiles, err := s.managedFiles(repo, trees[m])
	if err != nil {
		return branchMerge{}, err
	}
	tipFiles, err := s.managedFiles(repo, trees[tip])
	if err != nil {
		return branchMerge{}, err
	}

	out := branchMerge{mFiles: mFiles, changes: map[string][]byte{}}
	conflict := func(p string, c Conflict) {
		if out.conflicts == nil {
			out.conflicts = map[string]Conflict{}
		}
		out.conflicts[p] = c
	}
	for _, p := range touched {
		theirs, theirsOK := mFiles[p]
		if res != nil {
			if r, ok := res.files[p]; ok {
				then := res.conflict.Files[p].Theirs
				switch {
				case theirsOK == (then != nil) && bytes.Equal(theirs, then):
					out.changes[p] = r // main didn't change it since
				case !theirsOK:
					conflict(p, Conflict{Merged: r}) // deleted on main since
				default:
					merged, n := merge3.Merge(then, r, theirs, merge3.Labels{Ours: "resolved", Theirs: "main"})
					if n > 0 {
						conflict(p, Conflict{Merged: merged, Theirs: theirs, Count: n})
					} else {
						out.changes[p] = merged
					}
				}
				continue
			}
		}
		b, baseOK := baseFiles[p]
		ours, oursOK := tipFiles[p]
		switch {
		case !oursOK: // deleted on the branch
			if !theirsOK {
				continue // and on main
			}
			if baseOK && bytes.Equal(theirs, b) {
				out.changes[p] = nil
				continue
			}
			conflict(p, Conflict{Merged: theirs, Theirs: theirs})
		case !theirsOK && baseOK: // deleted on main, changed on the branch
			conflict(p, Conflict{Merged: ours})
		default:
			merged, n := merge3.Merge(b, ours, theirs, branchLabels(branch))
			if n > 0 {
				conflict(p, Conflict{Merged: merged, Theirs: theirs, Count: n})
				continue
			}
			out.changes[p] = nonNil(merged)
		}
	}
	if out.conflicts != nil {
		out.changes = nil
	}
	return out, nil
}

// moveTo moves main from m to tip (ff) or to a merge of tip with
// mb's content, unless uncommitted outside edits are in the way. It
// returns errMainMoved, having changed nothing, if main moved under it.
func (s *Store) moveTo(repo *git.Repository, branch string, m, tip plumbing.Hash, ff bool, mb branchMerge) (BranchResult, error) {
	changes := map[string][]byte{}
	var diffs []org.FileDiff
	for p, d := range mb.changes {
		old, had := mb.mFiles[p]
		if d == nil && !had || d != nil && had && bytes.Equal(d, old) {
			continue
		}
		changes[p] = d
	}
	for _, p := range sortedKeys(changes) {
		diffs = append(diffs, org.FileDiff{Path: p, Old: mb.mFiles[p], New: changes[p], Deleted: changes[p] == nil})
	}
	outside, err := s.outsideEdits(mb.mFiles)
	if err != nil {
		return BranchResult{}, err
	}
	var blocked []string
	for _, p := range sortedKeys(changes) {
		if d, dirty := outside[p]; dirty && !bytes.Equal(d, changes[p]) {
			blocked = append(blocked, p)
		}
	}
	if len(blocked) > 0 {
		return BranchResult{Blocked: blocked}, nil
	}

	r := tip
	msg := fmt.Sprintf("orgtd: fast-forward to %s", branch)
	if !ff {
		mTree, err := treeOf(repo, m)
		if err != nil {
			return BranchResult{}, err
		}
		tree, err := editTree(repo, mTree, changes)
		if err != nil {
			return BranchResult{}, err
		}
		msg = "orgtd: merge " + branch
		if len(diffs) > 0 {
			msg = org.CommitMessage(msg+": ", diffs)
		}
		if r, err = commit(repo, tree, m, msg, tip); err != nil {
			return BranchResult{}, err
		}
	}
	if err := s.apply(repo, m, r, mb.mFiles, changes, msg); err != nil {
		return BranchResult{}, err
	}
	rFiles := files{}
	for p, d := range mb.mFiles {
		rFiles[p] = d
	}
	for p, d := range changes {
		if d == nil {
			delete(rFiles, p)
		} else {
			rFiles[p] = d
		}
	}
	return BranchResult{
		Moved:   true,
		Merged:  !ff,
		Result:  Result{Head: r, Files: rFiles},
		Changed: sortedKeys(changes),
	}, nil
}

// mergeBase is a best common ancestor of a and b; zero if they have
// none.
func mergeBase(repo *git.Repository, a, b plumbing.Hash) (plumbing.Hash, error) {
	ca, err := repo.CommitObject(a)
	if err != nil {
		return plumbing.ZeroHash, err
	}
	cb, err := repo.CommitObject(b)
	if err != nil {
		return plumbing.ZeroHash, err
	}
	bases, err := ca.MergeBase(cb)
	if err != nil || len(bases) == 0 {
		return plumbing.ZeroHash, err
	}
	return bases[0].Hash, nil
}

// isAncestor reports whether a is b or one of its ancestors.
func isAncestor(repo *git.Repository, a, b plumbing.Hash) (bool, error) {
	if a == b {
		return true, nil
	}
	ca, err := repo.CommitObject(a)
	if err != nil {
		return false, err
	}
	cb, err := repo.CommitObject(b)
	if err != nil {
		return false, err
	}
	return ca.IsAncestor(cb)
}

// changedPaths lists every path, managed or not, that differs between
// two trees.
func changedPaths(repo *git.Repository, from, to plumbing.Hash) ([]string, error) {
	tree := func(h plumbing.Hash) (*object.Tree, error) {
		if h.IsZero() {
			return &object.Tree{}, nil
		}
		return repo.TreeObject(h)
	}
	a, err := tree(from)
	if err != nil {
		return nil, err
	}
	b, err := tree(to)
	if err != nil {
		return nil, err
	}
	diff, err := object.DiffTree(a, b)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, c := range diff {
		if c.From.Name != "" {
			out = append(out, c.From.Name)
		}
		if c.To.Name != "" && c.To.Name != c.From.Name {
			out = append(out, c.To.Name)
		}
	}
	return out, nil
}
