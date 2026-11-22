package gitstore

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// FastForwardResult is what FastForward did.
type FastForwardResult struct {
	// Moved reports whether the main branch moved; Head and Files are
	// then its new state, as from Save.
	Moved bool
	Result

	// Changed is the managed paths the fast-forward changed, when Moved.
	Changed []string

	// Blocked is the managed paths the branch changes that have
	// uncommitted edits on disk, when that's all that stopped it. It
	// waits for them to be committed (see CommitOutsideEdits).
	Blocked []string

	// Behind reports that the branch has commits main doesn't, but main
	// has moved on since the branch was made, so it isn't a
	// fast-forward: it waits for the branch to be rebuilt on main.
	Behind bool

	// Unmanaged is the paths the branch changes that orgtd doesn't
	// manage, which stop it for good. It's only reported when the
	// branch is otherwise a fast-forward, so `git merge --ff-only` can
	// finish the job.
	Unmanaged []string
}

// ffPair is a main/branch pair whose outcome FastForward has worked out
// from the commits alone, and that outcome.
type ffPair struct {
	main, tip plumbing.Hash
	res       FastForwardResult
}

// FastForward moves the main branch up to branch (a local branch, such
// as the one the iOS app pushes its changes to), when that's a
// fast-forward: when main is an ancestor of the branch's tip. It never
// merges: if main has moved on since the branch was made, it does
// nothing but report Behind, and waits for the branch to be rebuilt on
// the new main (the iOS app does on every sync). Nor does it do anything
// if the branch is missing or already merged; if it changes paths orgtd
// doesn't manage, which a fast-forward would leave stale on disk (listed
// in Unmanaged); or if it changes files with uncommitted edits on disk
// that it would overwrite (listed in Blocked). If another instance is
// saving, it doesn't wait: the next call tries again.
//
// The working tree, the index and the branch are brought up to date as a
// save's are (see apply), files first, so a crash partway leaves the
// files looking like outside edits with the branch's content — which the
// next call finds already in place and finishes.
func (s *Store) FastForward(branch string) (FastForwardResult, error) {
	if err := s.CheckBranch(); err != nil {
		return FastForwardResult{}, err
	}
	repo, err := s.open()
	if err != nil {
		return FastForwardResult{}, err
	}
	// Cheap checks first: this runs every couple of seconds.
	tip, err := headOf(repo, plumbing.NewBranchReferenceName(branch))
	if err != nil || tip.IsZero() {
		return FastForwardResult{}, err
	}
	m, err := headOf(repo, s.mainRef())
	if err != nil || m.IsZero() || m == tip {
		return FastForwardResult{}, err
	}
	if s.notFF.main == m && s.notFF.tip == tip {
		return s.notFF.res, nil
	}

	ok, err := s.mergeLock.TryLock()
	if err != nil || !ok {
		return FastForwardResult{}, err
	}
	defer s.mergeLock.Unlock()
	if m, err = headOf(repo, s.mainRef()); err != nil {
		return FastForwardResult{}, err
	}
	if m == tip {
		return FastForwardResult{}, nil
	}
	remember := func(res FastForwardResult) (FastForwardResult, error) {
		s.notFF = ffPair{main: m, tip: tip, res: res}
		return res, nil
	}
	if ff, err := isAncestor(repo, m, tip); err != nil {
		return FastForwardResult{}, err
	} else if !ff {
		merged, err := isAncestor(repo, tip, m)
		if err != nil {
			return FastForwardResult{}, err
		}
		return remember(FastForwardResult{Behind: !merged})
	}

	mTree, err := treeOf(repo, m)
	if err != nil {
		return FastForwardResult{}, err
	}
	tipTree, err := treeOf(repo, tip)
	if err != nil {
		return FastForwardResult{}, err
	}
	touched, err := changedPaths(repo, mTree, tipTree)
	if err != nil {
		return FastForwardResult{}, err
	}
	var unmanaged []string
	for _, p := range touched {
		if !s.IsManaged(p) {
			unmanaged = append(unmanaged, p)
		}
	}
	if len(unmanaged) > 0 {
		return remember(FastForwardResult{Unmanaged: unmanaged})
	}
	mFiles, err := s.managedFiles(repo, mTree)
	if err != nil {
		return FastForwardResult{}, err
	}
	tipFiles, err := s.managedFiles(repo, tipTree)
	if err != nil {
		return FastForwardResult{}, err
	}
	outside, err := s.outsideEdits(mFiles)
	if err != nil {
		return FastForwardResult{}, err
	}
	changes := map[string][]byte{}
	var blocked []string
	for _, p := range touched {
		changes[p] = tipFiles[p] // nil: deleted
		if d, dirty := outside[p]; dirty && !bytes.Equal(d, tipFiles[p]) {
			blocked = append(blocked, p)
		}
	}
	if len(blocked) > 0 {
		return FastForwardResult{Blocked: blocked}, nil
	}
	msg := fmt.Sprintf("orgtd: fast-forward to %s", branch)
	if err := s.apply(repo, m, tip, mFiles, changes, msg); err != nil {
		if errors.Is(err, errMainMoved) {
			return FastForwardResult{}, nil // the next call looks again
		}
		return FastForwardResult{}, err
	}
	return FastForwardResult{
		Moved:   true,
		Result:  Result{Head: tip, Files: tipFiles},
		Changed: sortedKeys(changes),
	}, nil
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
