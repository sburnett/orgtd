// Package gitsync is the iOS app's git layer (see ios/DESIGN.md §4): it
// clones the org repository, fetches, rebuilds the local "mobile" branch
// as origin's base branch plus the still-pending captures, and pushes it
// with a lease. The phone never merges or resolves a conflict, and never
// drops a queued change: "mobile" is the base branch plus the queued
// captures and state changes, replayed onto it — or, when a state
// change's entry changed on the base branch since, the commit the queue
// was made on plus the queue (see Rebuild), for the desktop to merge.
// Desktop orgtd fast-forwards or merges it when the phone pushes into
// the org directory itself (gitstore.Store.ApplyBranch), with the user
// resolving any conflict there; otherwise the user merges it.
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
	"strings"
	"time"

	git "github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"

	"github.com/sburnett/orgtd/internal/atomicfile"
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
	OpsPath   string               // state-ops.json: queued state changes ("" for none)
	StatePath string               // sync-state.json (see State)
	Auth      transport.AuthMethod // nil for local (file://) remotes
	Author    Author               // who capture commits are by
	Now       func() time.Time     // clock for State timestamps; time.Now if nil
}

// Author names who capture commits are by.
type Author struct{ Name, Email string }

// PushedTip is one commit pushed as origin/mobile, and the queued items
// it contained: captures and state changes. Once Tip is an ancestor of
// the base branch, the user has merged it, and exactly those items leave
// their queues.
type PushedTip struct {
	Tip        string   `json:"tip"`
	CaptureIDs []string `json:"captureIds"`     // capture and state-change IDs (one namespace)
	Base       string   `json:"base,omitempty"` // the commit Tip was built on (State.Base then)
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
	// Base is the commit local "mobile" was last built on: origin's
	// base branch, when every queued change applied to it, or else the
	// earlier commit they were made on (see Rebuild).
	Base string `json:"base,omitempty"`
	// Waiting is the titles of queued changes that don't apply to
	// origin's base branch as last fetched, because their entries changed
	// there too: "mobile" is built on Base instead, for the desktop to
	// merge (see Rebuild). Empty when it's built on the base branch.
	Waiting []string `json:"waiting,omitempty"`
	// Dropped are state changes a rebuild could no longer apply, until
	// the app has shown them (DismissDropped). Only an older version
	// dropped changes; it's kept so those still get shown.
	Dropped []DroppedOp `json:"dropped,omitempty"`
}

func (r *Repo) loadOps() ([]capturequeue.StateOp, error) {
	if r.OpsPath == "" {
		return nil, nil
	}
	return capturequeue.LoadOps(r.OpsPath)
}

// queuedIDs is every queued capture and state change, oldest first
// (captures, then state changes).
func (r *Repo) queuedIDs() ([]string, error) {
	queue, err := capturequeue.Load(r.QueuePath)
	if err != nil {
		return nil, err
	}
	ops, err := r.loadOps()
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, c := range queue {
		ids = append(ids, c.ID)
	}
	for _, o := range ops {
		ids = append(ids, o.ID)
	}
	return ids, nil
}

// DismissDropped forgets the dropped state changes once they've been shown.
func (r *Repo) DismissDropped() error {
	s, err := r.LoadState()
	if err != nil {
		return err
	}
	s.Dropped = nil
	return r.saveState(s)
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
	if err := atomicfile.Write(r.StatePath, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("gitsync: writing state: %w", err)
	}
	return nil
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
	ids, err := r.queuedIDs()
	if err != nil {
		return nil, err
	}
	queued := map[string]bool{}
	for _, id := range ids {
		queued[id] = true
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
			if t.Base != "" && t.Base == s.Base {
				// The rest of the queue was made on top of T's changes,
				// which T is exactly: Base plus them.
				s.Base = t.Tip
			}
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
	if r.OpsPath != "" {
		if _, err := capturequeue.RemoveOps(r.OpsPath, removed); err != nil {
			return nil, err
		}
	}
	s.PushedTips = kept
	return removed, r.saveState(s)
}

// Rebuild resets local "mobile" (and the working tree) to origin's base
// branch and commits each pending capture on top of it, oldest first, by
// appending it to the inbox file (§4.3 steps 3–4), then each queued
// state change, oldest first. Captures go first so a state change made
// to a still-pending capture finds it. It needs no network.
//
// A state change whose entry changed on the base branch since it was
// made no longer applies there. Rebuild never drops one: it builds
// "mobile" instead on State.Base, the commit the queue was made on, and
// lists those changes in State.Waiting. "mobile" then isn't a
// fast-forward of the base branch, and the desktop merges it (with the
// user resolving any conflict); once that merge is fetched, the changes
// leave the queue as merged (DropMerged). If the queue doesn't apply to
// Base either, Rebuild returns an error and keeps every change queued.
func (r *Repo) Rebuild() error {
	repo, s, err := r.open()
	if err != nil {
		return err
	}
	baseRef, err := repo.Reference(remoteRef(s.BaseBranch), true)
	if err != nil {
		return fmt.Errorf("gitsync: no origin/%s: %w", s.BaseBranch, err)
	}
	main := baseRef.Hash()
	if s.Base == "" {
		s.Base = r.priorBase(repo, main).String()
	}
	waiting, err := r.buildOn(repo, main)
	if err != nil {
		return err
	}
	if len(waiting) == 0 {
		s.Base, s.Waiting = main.String(), nil
		return r.saveState(s)
	}
	if s.Base == main.String() {
		return fmt.Errorf("gitsync: %s no longer applies; nothing was dropped", strings.Join(quoted(waiting), ", "))
	}
	if still, err := r.buildOn(repo, plumbing.NewHash(s.Base)); err != nil {
		return err
	} else if len(still) > 0 {
		return fmt.Errorf("gitsync: %s no longer applies; nothing was dropped", strings.Join(quoted(still), ", "))
	}
	s.Waiting = waiting
	return r.saveState(s)
}

// priorBase is what an older version's "mobile" was built on, which
// didn't record it: where local "mobile" left origin's base branch, or
// main itself if there's no "mobile".
func (r *Repo) priorBase(repo *git.Repository, main plumbing.Hash) plumbing.Hash {
	local, err := repo.Reference(plumbing.NewBranchReferenceName(MobileBranch), true)
	if err != nil {
		return main
	}
	a, err := repo.CommitObject(local.Hash())
	if err != nil {
		return main
	}
	b, err := repo.CommitObject(main)
	if err != nil {
		return main
	}
	bases, err := a.MergeBase(b)
	if err != nil || len(bases) == 0 {
		return main
	}
	return bases[0].Hash
}

func quoted(titles []string) []string {
	out := make([]string, len(titles))
	for i, t := range titles {
		out[i] = fmt.Sprintf("%q", t)
	}
	return out
}

// buildOn resets local "mobile" and the working tree to base and commits
// the queue on top (see Rebuild). It returns the titles of the state
// changes that didn't apply there, having skipped them.
func (r *Repo) buildOn(repo *git.Repository, base plumbing.Hash) ([]string, error) {
	mobile := plumbing.NewBranchReferenceName(MobileBranch)
	if err := repo.Storer.SetReference(plumbing.NewHashReference(mobile, base)); err != nil {
		return nil, err
	}
	if err := repo.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, mobile)); err != nil {
		return nil, err
	}
	wt, err := repo.Worktree()
	if err != nil {
		return nil, err
	}
	if err := wt.Reset(&git.ResetOptions{Commit: base, Mode: git.HardReset}); err != nil {
		return nil, fmt.Errorf("gitsync: resetting to %s: %w", base, err)
	}

	queue, err := capturequeue.Load(r.QueuePath)
	if err != nil {
		return nil, err
	}
	inbox, err := InboxFile(r.Dir)
	if err != nil {
		return nil, err
	}
	for _, c := range queue {
		text, err := capture.Render(c)
		if err != nil {
			return nil, fmt.Errorf("gitsync: capture %q: %w", c.Title, err)
		}
		if err := capture.AppendEntry(filepath.Join(r.Dir, inbox), text); err != nil {
			return nil, err
		}
		if _, err := wt.Add(inbox); err != nil {
			return nil, fmt.Errorf("gitsync: staging %s: %w", inbox, err)
		}
		sig := &object.Signature{Name: r.Author.Name, Email: r.Author.Email, When: c.CreatedAt}
		if _, err := wt.Commit("Capture: "+c.Title, &git.CommitOptions{Author: sig, Committer: sig}); err != nil {
			return nil, fmt.Errorf("gitsync: committing capture %q: %w", c.Title, err)
		}
	}
	return r.replayOps(wt)
}

// replayOps applies and commits each queued state change (see Rebuild),
// returning the titles of those whose entry changed, which it skips.
func (r *Repo) replayOps(wt *git.Worktree) ([]string, error) {
	ops, err := r.loadOps()
	if err != nil || len(ops) == 0 {
		return nil, err
	}
	var skipped []string
	for _, op := range ops {
		if err := applyOp(r.Dir, op); err != nil {
			if !errors.Is(err, ErrEntryChanged) {
				return nil, fmt.Errorf("gitsync: setting %q to %s: %w", op.Title(), op.To, err)
			}
			skipped = append(skipped, op.NewTitle())
			continue
		}
		if _, err := wt.Add(op.File); err != nil {
			return nil, fmt.Errorf("gitsync: staging %s: %w", op.File, err)
		}
		if op.Move != nil && op.Move.ToFile != "" && op.Move.ToFile != op.File {
			if _, err := wt.Add(op.Move.ToFile); err != nil {
				return nil, fmt.Errorf("gitsync: staging %s: %w", op.Move.ToFile, err)
			}
		}
		sig := &object.Signature{Name: r.Author.Name, Email: r.Author.Email, When: op.CreatedAt}
		msg := fmt.Sprintf("Set %s: %s → %s", op.Title(), orNone(op.From), orNone(op.To))
		if op.Edit != nil {
			msg = "Edit: " + op.NewTitle()
		}
		if op.Insert != nil {
			msg = "Add: " + op.NewTitle()
		}
		if op.Delete != nil {
			msg = "Delete: " + op.Title()
		}
		if op.Move != nil {
			switch {
			case op.Move.Delta > 0:
				msg = "Demote: " + op.Title()
			case op.Move.Delta < 0:
				msg = "Promote: " + op.Title()
			case op.Move.ToFile != "" && op.Move.ToFile != op.File:
				msg = "Move: " + op.Title() + " → " + op.Move.ToFile
			default:
				msg = "Move: " + op.Title()
			}
		}
		if _, err := wt.Commit(msg, &git.CommitOptions{Author: sig, Committer: sig}); err != nil {
			return nil, fmt.Errorf("gitsync: committing state change %q: %w", op.Title(), err)
		}
	}
	return skipped, nil
}

func orNone(keyword string) string {
	if keyword == "" {
		return "(none)"
	}
	return keyword
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
	ids, err := r.queuedIDs()
	if err != nil {
		return err
	}
	tip := PushedTip{Tip: local.Hash().String(), CaptureIDs: ids, Base: s.Base}
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
