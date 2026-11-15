// Package gitsync is the iOS app's git layer (see ios/DESIGN.md §4): it
// clones the org repository, fetches, rebuilds the local "mobile" branch
// as origin's base branch plus the still-pending captures, and pushes it
// with a lease. The phone never merges or resolves a conflict: "mobile"
// only ever differs from the base branch by entries appended to the end
// of the inbox file, and the user merges it by hand.
//
// The capture queue (internal/capturequeue) is the source of truth; the
// branch is derived from it on every refresh. Rebuilt commits take their
// author and committer time from each capture's CreatedAt, so rebuilding
// the same queue on the same base gives the same commit hashes.
package gitsync

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	git "github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"

	"github.com/sburnett/orgtd/internal/capture"
	"github.com/sburnett/orgtd/internal/capturequeue"
	"github.com/sburnett/orgtd/internal/config"
)

// MobileBranch is the branch every phone change lives on.
const MobileBranch = "mobile"

const remoteName = "origin"

// ErrLease is returned (wrapped) when origin/mobile was moved by someone
// other than this phone: the push is refused rather than overwrite it.
var ErrLease = errors.New("origin/mobile was changed by someone else; merge or delete it on your computer, then sync again")

// Repo is one phone-side clone plus its bookkeeping files.
type Repo struct {
	Dir       string               // the working clone (checked out at local "mobile")
	QueuePath string               // captures.json (see internal/capturequeue)
	StatePath string               // sync-state.json (see State)
	Auth      transport.AuthMethod // nil for local (file://) remotes
	Author    Author               // who capture commits are by
	Now       func() time.Time     // clock for State timestamps; time.Now if nil
}

// Author names who capture commits are by.
type Author struct{ Name, Email string }

// PushedTip is one commit pushed as origin/mobile, and the captures it
// contained. Once Tip is an ancestor of the base branch, the user has
// merged it, and exactly those captures leave the queue.
type PushedTip struct {
	Tip        string   `json:"tip"`
	CaptureIDs []string `json:"captureIds"`
}

// State is sync-state.json.
type State struct {
	BaseBranch  string      `json:"baseBranch"`           // the branch "mobile" is rebuilt on, e.g. "main"
	HostKey     HostKey     `json:"hostKey,omitzero"`     // the server's SSH host key, trusted at setup (none for local remotes)
	PushedTips  []PushedTip `json:"pushedTips,omitempty"` // oldest first
	LastPushed  string      `json:"lastPushed,omitempty"` // what this phone last left origin/mobile at ("" = deleted / never pushed)
	LastFetch   time.Time   `json:"lastFetch,omitzero"`
	LastPush    time.Time   `json:"lastPush,omitzero"`
	LastError   string      `json:"lastError,omitempty"`
	LastErrorAt time.Time   `json:"lastErrorAt,omitzero"`
}

func (r *Repo) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// LoadState reads the state file; a missing one is a zero State.
func (r *Repo) LoadState() (State, error) {
	var s State
	data, err := os.ReadFile(r.StatePath)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, fmt.Errorf("gitsync: reading state: %w", err)
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return s, fmt.Errorf("gitsync: parsing state: %w", err)
	}
	return s, nil
}

func (r *Repo) saveState(s State) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := r.StatePath + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("gitsync: writing state: %w", err)
	}
	return os.Rename(tmp, r.StatePath)
}

// recordError notes err (if any) in the state file and returns it
// unchanged, so the status screen can show the last failure.
func (r *Repo) recordError(err error) error {
	s, lerr := r.LoadState()
	if lerr != nil {
		return err
	}
	if err == nil {
		s.LastError, s.LastErrorAt = "", time.Time{}
	} else {
		s.LastError, s.LastErrorAt = err.Error(), r.now()
	}
	r.saveState(s)
	return err
}

// Clone makes the phone's clone of url in r.Dir (which must not already
// hold one, though it may be an empty directory) and records the
// remote's default branch as the base branch, and hostKey — the server's
// host key the user confirmed, which r.Auth must already be pinned to —
// for later connections.
func (r *Repo) Clone(url string, hostKey HostKey) error {
	ctx, cancel := withTimeout(CloneTimeout)
	defer cancel()
	repo, err := git.PlainCloneContext(ctx, r.Dir, false, &git.CloneOptions{URL: url, Auth: r.Auth, RemoteName: remoteName, ProxyOptions: r.proxy()})
	if err != nil {
		os.RemoveAll(r.Dir)
		os.Mkdir(r.Dir, 0o755) // keep the (possibly protection-classed) directory
		return fmt.Errorf("gitsync: cloning %s: %w", url, unreachable(err))
	}
	head, err := repo.Head()
	if err != nil {
		return fmt.Errorf("gitsync: clone has no HEAD (is the repository empty?): %w", err)
	}
	s, err := r.LoadState()
	if err != nil {
		return err
	}
	s.BaseBranch = head.Name().Short()
	s.HostKey = hostKey
	s.LastFetch = r.now()
	if err := r.saveState(s); err != nil {
		return err
	}
	// Check out "mobile" from the start, so the working tree is always
	// on it (Rebuild resets it to the base branch).
	return r.Rebuild()
}

func (r *Repo) open() (*git.Repository, State, error) {
	repo, err := git.PlainOpen(r.Dir)
	if err != nil {
		return nil, State{}, fmt.Errorf("gitsync: opening clone: %w", err)
	}
	s, err := r.LoadState()
	if err != nil {
		return nil, s, err
	}
	if s.BaseBranch == "" {
		s.BaseBranch = "main"
	}
	return repo, s, nil
}

func remoteRef(branch string) plumbing.ReferenceName {
	return plumbing.NewRemoteReferenceName(remoteName, branch)
}

// Fetch updates the remote-tracking branches (§4.3 step 1), pruning any
// deleted on the remote. It is read-only as far as the remote goes.
func (r *Repo) Fetch() error {
	repo, s, err := r.open()
	if err != nil {
		return err
	}
	ctx, cancel := withTimeout(OperationTimeout)
	defer cancel()
	err = repo.FetchContext(ctx, &git.FetchOptions{
		RemoteName:   remoteName,
		Auth:         r.Auth,
		ProxyOptions: r.proxy(),
		RefSpecs:     []gitconfig.RefSpec{"+refs/heads/*:refs/remotes/origin/*"},
		Prune:        true,
	})
	if err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
		return fmt.Errorf("gitsync: fetching: %w", unreachable(err))
	}
	s.LastFetch = r.now()
	return r.saveState(s)
}

// DropMerged removes from the queue the captures of every pushed tip that
// is now an ancestor of origin's base branch — i.e. the user merged it —
// and forgets those tips (§4.3 step 2). Tips whose captures have all left
// the queue some other way (deleted on the phone) are forgotten too. It
// returns the IDs it removed from the queue.
func (r *Repo) DropMerged() ([]string, error) {
	repo, s, err := r.open()
	if err != nil {
		return nil, err
	}
	baseRef, err := repo.Reference(remoteRef(s.BaseBranch), true)
	if err != nil {
		return nil, fmt.Errorf("gitsync: no origin/%s: %w", s.BaseBranch, err)
	}
	base, err := repo.CommitObject(baseRef.Hash())
	if err != nil {
		return nil, err
	}
	queue, err := capturequeue.Load(r.QueuePath)
	if err != nil {
		return nil, err
	}
	queued := map[string]bool{}
	for _, c := range queue {
		queued[c.ID] = true
	}

	var removed []string
	var kept []PushedTip
	for _, t := range s.PushedTips {
		merged := false
		if tip, err := repo.CommitObject(plumbing.NewHash(t.Tip)); err == nil {
			merged, _ = tip.IsAncestor(base)
			if tip.Hash == base.Hash {
				merged = true
			}
		}
		if merged {
			removed = append(removed, t.CaptureIDs...)
			continue
		}
		stillQueued := false
		for _, id := range t.CaptureIDs {
			stillQueued = stillQueued || queued[id]
		}
		if stillQueued {
			kept = append(kept, t)
		}
	}
	if _, err := capturequeue.Remove(r.QueuePath, removed); err != nil {
		return nil, err
	}
	s.PushedTips = kept
	return removed, r.saveState(s)
}

// Rebuild resets local "mobile" (and the working tree) to origin's base
// branch and commits each pending capture on top of it, oldest first, by
// appending it to the inbox file (§4.3 steps 3–4). It needs no network.
func (r *Repo) Rebuild() error {
	repo, s, err := r.open()
	if err != nil {
		return err
	}
	baseRef, err := repo.Reference(remoteRef(s.BaseBranch), true)
	if err != nil {
		return fmt.Errorf("gitsync: no origin/%s: %w", s.BaseBranch, err)
	}
	mobile := plumbing.NewBranchReferenceName(MobileBranch)
	if err := repo.Storer.SetReference(plumbing.NewHashReference(mobile, baseRef.Hash())); err != nil {
		return err
	}
	if err := repo.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, mobile)); err != nil {
		return err
	}
	wt, err := repo.Worktree()
	if err != nil {
		return err
	}
	if err := wt.Reset(&git.ResetOptions{Commit: baseRef.Hash(), Mode: git.HardReset}); err != nil {
		return fmt.Errorf("gitsync: resetting to origin/%s: %w", s.BaseBranch, err)
	}

	queue, err := capturequeue.Load(r.QueuePath)
	if err != nil {
		return err
	}
	inbox, err := InboxFile(r.Dir)
	if err != nil {
		return err
	}
	for _, c := range queue {
		text, err := capture.Render(c)
		if err != nil {
			return fmt.Errorf("gitsync: capture %q: %w", c.Title, err)
		}
		if err := capture.AppendEntry(filepath.Join(r.Dir, inbox), text); err != nil {
			return err
		}
		if _, err := wt.Add(inbox); err != nil {
			return fmt.Errorf("gitsync: staging %s: %w", inbox, err)
		}
		sig := &object.Signature{Name: r.Author.Name, Email: r.Author.Email, When: c.CreatedAt}
		if _, err := wt.Commit("Capture: "+c.Title, &git.CommitOptions{Author: sig, Committer: sig}); err != nil {
			return fmt.Errorf("gitsync: committing capture %q: %w", c.Title, err)
		}
	}
	return nil
}

// InboxFile is the inbox's base name for the checkout in dir: the
// committed .orgtd.toml's inbox_file, else the built-in default.
func InboxFile(dir string) (string, error) {
	cfg, _, err := config.LoadRepo(dir)
	if err != nil {
		return "", err
	}
	if cfg.InboxFile != "" {
		return cfg.InboxFile, nil
	}
	return config.Default().InboxFile, nil
}

// Publish makes origin/mobile match local "mobile" (§4.3 steps 5–6):
// deleting it when there's nothing pending, otherwise force-pushing with
// a lease, and records the pushed tip with the captures it holds. It
// refuses, with ErrLease, to touch an origin/mobile this phone didn't
// leave there.
func (r *Repo) Publish() error {
	repo, s, err := r.open()
	if err != nil {
		return err
	}
	baseRef, err := repo.Reference(remoteRef(s.BaseBranch), true)
	if err != nil {
		return err
	}
	local, err := repo.Reference(plumbing.NewBranchReferenceName(MobileBranch), true)
	if err != nil {
		return err
	}
	remote := plumbing.ZeroHash
	if ref, err := repo.Reference(remoteRef(MobileBranch), true); err == nil {
		remote = ref.Hash()
	}
	if !remote.IsZero() && remote.String() != s.LastPushed {
		if remote == local.Hash() {
			// Someone pushed exactly what we have (e.g. a lost state
			// file): adopt it rather than refuse.
			s.LastPushed = remote.String()
		} else {
			return fmt.Errorf("gitsync: %w", ErrLease)
		}
	}
	dst := plumbing.NewBranchReferenceName(MobileBranch)
	// go-git can't lease against a ref the remote doesn't have, so a
	// first push relies on the check above alone (a branch someone else
	// creates between our fetch and push would be overwritten).
	var lease *git.ForceWithLease
	if !remote.IsZero() {
		lease = &git.ForceWithLease{RefName: dst, Hash: remote}
	}

	if local.Hash() == baseRef.Hash() {
		if !remote.IsZero() {
			err := r.push(repo, &git.PushOptions{
				RemoteName:     remoteName,
				Auth:           r.Auth,
				RefSpecs:       []gitconfig.RefSpec{gitconfig.RefSpec(":" + dst.String())},
				ForceWithLease: lease,
			})
			if err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
				return fmt.Errorf("gitsync: deleting origin/%s: %w", MobileBranch, unreachable(err))
			}
			repo.Storer.RemoveReference(remoteRef(MobileBranch))
		}
		s.LastPushed = ""
		s.LastPush = r.now()
		return r.saveState(s)
	}

	if local.Hash() != remote {
		err := r.push(repo, &git.PushOptions{
			RemoteName:     remoteName,
			Auth:           r.Auth,
			RefSpecs:       []gitconfig.RefSpec{gitconfig.RefSpec("+" + local.Name().String() + ":" + dst.String())},
			ForceWithLease: lease,
		})
		if err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
			return fmt.Errorf("gitsync: pushing %s: %w", MobileBranch, unreachable(err))
		}
		if err := repo.Storer.SetReference(plumbing.NewHashReference(remoteRef(MobileBranch), local.Hash())); err != nil {
			return err
		}
	}
	queue, err := capturequeue.Load(r.QueuePath)
	if err != nil {
		return err
	}
	tip := PushedTip{Tip: local.Hash().String()}
	for _, c := range queue {
		tip.CaptureIDs = append(tip.CaptureIDs, c.ID)
	}
	if n := len(s.PushedTips); n == 0 || s.PushedTips[n-1].Tip != tip.Tip {
		s.PushedTips = append(s.PushedTips, tip)
	}
	s.LastPushed = tip.Tip
	s.LastPush = r.now()
	return r.saveState(s)
}

// push is repo.Push bounded by OperationTimeout and the connection
// deadlines.
func (r *Repo) push(repo *git.Repository, o *git.PushOptions) error {
	o.ProxyOptions = r.proxy()
	ctx, cancel := withTimeout(OperationTimeout)
	defer cancel()
	return repo.PushContext(ctx, o)
}

// proxy routes SSH connections through deadlineProxy (see network.go);
// local remotes, which tests use, need none.
func (r *Repo) proxy() transport.ProxyOptions {
	if r.Auth == nil {
		return transport.ProxyOptions{}
	}
	return sshProxy
}

// RefreshLocal is Refresh without the network: it drops any captures the
// last successful fetch showed were merged and rebuilds the working tree
// from what was last fetched. For when a fetch isn't worth attempting.
func (r *Repo) RefreshLocal() error {
	if _, err := r.DropMerged(); err != nil {
		return err
	}
	return r.Rebuild()
}

// Refresh brings the working tree up to date with origin's base branch
// plus the pending captures, without pushing (§4.3 steps 1–4). A fetch
// failure (e.g. offline) still rebuilds from what was last fetched, so
// the views reflect any new captures, and is then returned.
func (r *Repo) Refresh() error {
	fetchErr := r.Fetch()
	if fetchErr == nil {
		if _, err := r.DropMerged(); err != nil {
			return r.recordError(err)
		}
	}
	if err := r.Rebuild(); err != nil {
		return r.recordError(err)
	}
	return r.recordError(fetchErr)
}

// Sync is Refresh followed by Publish (§4.3, every step).
func (r *Repo) Sync() error {
	if err := r.Fetch(); err != nil {
		return r.recordError(err)
	}
	if _, err := r.DropMerged(); err != nil {
		return r.recordError(err)
	}
	if err := r.Rebuild(); err != nil {
		return r.recordError(err)
	}
	return r.recordError(r.Publish())
}
