// Package gitstore is the desktop's git layer: the org directory is the
// root of a git repository, every save is a commit on the main branch,
// and saves from several orgtd instances (or edits made outside orgtd)
// are reconciled by a three-way merge (internal/merge3). Everything goes
// through go-git; no git binary is needed. It knows nothing about the
// UI or the org format: files are bytes, named by slash-separated paths
// relative to the org directory. See DESIGN.md ("Saving through git").
package gitstore

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/gofrs/flock"

	"github.com/sburnett/orgtd/internal/org"
)

// ErrNotRepo is returned by Open when the directory isn't the root of a
// git repository.
var ErrNotRepo = errors.New("not the root of a git repository")

// Store is one orgtd instance's handle on the repository. Create it with
// Open; Close releases its locks. Its methods are safe to call from
// several goroutines at once (the UI polls on one of its own while saves
// run on another).
type Store struct {
	dir        string // the org directory: the repository's working tree
	gitDir     string // dir/.git
	mainBranch string
	id         string // this instance's ID; see newInstanceID
	ignore     []string
	keywords   org.Keywords // for parsing files to describe commits

	instanceLock *flock.Flock
	mergeLock    *flock.Flock

	// mergeMu is held along with mergeLock (see tryLockMerge). The flock
	// only keeps other processes out: it belongs to the open file, and
	// gofrs/flock's TryLock reports success when this Flock already holds
	// it, so it can't keep this process's goroutines from each other.
	mergeMu sync.Mutex

	// notFF is the last main/branch pair ApplyBranch found it can't
	// move main for, and why (Unmanaged, a Conflict, already merged), so
	// polling doesn't walk the history again until either moves. Nil
	// until then.
	notFF atomic.Pointer[branchPair]

	// moves counts this Store's moves of the main branch (see Moves).
	moves atomic.Uint64

	closeOnce sync.Once
	closeErr  error

	// failAt, if set, is called at each step of a save (see Save); an
	// error it returns makes Save stop right there, skipping all cleanup,
	// as if the process had died. Tests use it to simulate crashes.
	failAt func(step string) error
}

// Open checks that dir is the root of a git repository, and takes this
// instance's lock (held until Close or process exit; see Orphans).
// mainBranch is the branch saves go to. ignore holds the
// outline_ignore_files globs: matching files are never managed. kw is
// the keyword set commit messages parse files with (see
// org.CommitMessage).
//
// Open succeeds whatever branch HEAD is on; Save refuses unless it's
// mainBranch (see CheckBranch), since a save moves mainBranch and
// updates the working tree.
func Open(dir, mainBranch string, ignore []string, kw org.Keywords) (*Store, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	gitDir := filepath.Join(abs, ".git")
	if fi, err := os.Stat(gitDir); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("%s: %w (run `git init` there first)", abs, ErrNotRepo)
	}
	if _, err := git.PlainOpen(abs); err != nil {
		return nil, fmt.Errorf("%s: %w: %v", abs, ErrNotRepo, err)
	}
	if mainBranch == "" {
		mainBranch = "main"
	}
	if !plumbing.NewBranchReferenceName(mainBranch).IsBranch() || strings.ContainsAny(mainBranch, " ~^:?*[\\") {
		return nil, fmt.Errorf("invalid main_branch %q", mainBranch)
	}
	s := &Store{
		dir:        abs,
		gitDir:     gitDir,
		mainBranch: mainBranch,
		id:         newInstanceID(),
		ignore:     ignore,
		keywords:   kw,
	}
	if err := os.MkdirAll(filepath.Join(gitDir, "orgtd", "instances"), 0o755); err != nil {
		return nil, err
	}
	s.instanceLock = flock.New(s.instanceLockPath(s.id))
	if err := takeInstanceLock(s.instanceLock); err != nil {
		return nil, err
	}
	s.mergeLock = flock.New(filepath.Join(gitDir, "orgtd", "merge.lock"))
	return s, nil
}

// Close releases the instance and merge locks, as the OS would if the
// process exited, and removes the instance's lock file, so a clean exit
// leaves nothing behind. (A crashed instance's lock file is removed by
// the next Orphans that finds it free.) A merge under way on another
// goroutine (a poll bringing in the phone's branch) finishes first, so
// another instance can't start one while it's still writing; none can
// start after. The Store must not be used afterwards; closing it again
// does nothing.
func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		s.mergeMu.Lock() // never released: the Store is done
		s.mergeLock.Unlock()
		if err := s.instanceLock.Unlock(); err != nil {
			s.closeErr = err
			return
		}
		if err := os.Remove(s.instanceLock.Path()); err != nil && !errors.Is(err, fs.ErrNotExist) {
			s.closeErr = err
		}
	})
	return s.closeErr
}

// Moves is how many times this Store has moved the main branch: saves,
// and bringing in another branch (ApplyBranch, ResolveBranch). A caller
// that read the branch on another goroutine can compare it before and
// after to tell whether what it read might be older than this instance's
// own latest move.
func (s *Store) Moves() uint64 { return s.moves.Load() }

// Dir is the org directory, as an absolute path.
func (s *Store) Dir() string { return s.dir }

// ID is this instance's ID: "<host>-<pid>-<unix time>".
func (s *Store) ID() string { return s.id }

// MainBranch is the branch saves go to.
func (s *Store) MainBranch() string { return s.mainBranch }

var unsafeRefChars = regexp.MustCompile(`[^A-Za-z0-9-]+`)

func newInstanceID() string {
	host, _ := os.Hostname()
	host = strings.Trim(unsafeRefChars.ReplaceAllString(host, "-"), "-")
	if host == "" {
		host = "host"
	}
	id := fmt.Sprintf("%s-%d-%d", host, os.Getpid(), time.Now().Unix())
	// Several Stores in one process (tests) need distinct IDs too.
	if n := instanceCount.Add(1); n > 1 {
		id += fmt.Sprintf("-%d", n)
	}
	return id
}

var instanceCount atomic.Int64

// takeInstanceLock takes a new instance's lock, retrying briefly: the ID
// is unique to this run, so the lock can only be busy because another
// instance is test-locking it at that moment (see OtherInstances and
// Orphans), which lasts no longer than a system call or two.
func takeInstanceLock(fl *flock.Flock) error {
	for try := 0; ; try++ {
		ok, err := fl.TryLock()
		if err != nil {
			return fmt.Errorf("taking instance lock: %v", err)
		}
		if ok {
			return nil
		}
		if try == instanceLockTries-1 {
			return fmt.Errorf("taking instance lock: %s is locked", fl.Path())
		}
		time.Sleep(instanceLockRetry)
	}
}

const (
	instanceLockTries = 50
	instanceLockRetry = 10 * time.Millisecond
)

// OtherInstances counts the other orgtd instances running on this
// repository (on this machine: the instance locks are flock(2) locks),
// by test-locking each lock file in .git/orgtd/instances. A lock it can't
// take belongs to a live instance.
//
// Unlike Orphans, it never removes a lock file, even a crashed
// instance's: it can run while another instance is starting up, between
// that instance's Open creating its lock file and locking it, and
// removing the file then would leave the new instance holding a lock no
// one else can see (Orphans would then take it for dead). Nor does it
// create one: a file that vanishes between listing and locking (an
// instance closing) is skipped, not recreated.
func (s *Store) OtherInstances() (int, error) {
	dir := filepath.Join(s.gitDir, "orgtd", "instances")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".lock")
		if !ok || id == s.id || !e.Type().IsRegular() {
			continue
		}
		fl := flock.New(filepath.Join(dir, e.Name()), flock.SetFlag(os.O_RDONLY))
		locked, err := fl.TryLock()
		if err != nil {
			continue // gone since ReadDir: that instance closed
		}
		if locked {
			fl.Unlock()
		} else {
			n++
		}
	}
	return n, nil
}

func (s *Store) instanceLockPath(id string) string {
	return filepath.Join(s.gitDir, "orgtd", "instances", id+".lock")
}

func (s *Store) mainRef() plumbing.ReferenceName {
	return plumbing.NewBranchReferenceName(s.mainBranch)
}

func (s *Store) pendingRef(id string) plumbing.ReferenceName {
	return plumbing.ReferenceName("refs/orgtd/" + id)
}

// ErrWrongBranch is wrapped by CheckBranch's error.
var ErrWrongBranch = errors.New("HEAD is not on the main branch")

// CheckBranch returns an error (wrapping ErrWrongBranch) unless HEAD is
// on the main branch: saving moves that branch and rewrites the working
// tree, which would be wrong for any other checkout.
func (s *Store) CheckBranch() error {
	repo, err := s.open()
	if err != nil {
		return err
	}
	head, err := repo.Storer.Reference(plumbing.HEAD)
	if err != nil {
		return fmt.Errorf("reading HEAD: %w", err)
	}
	if head.Type() != plumbing.SymbolicReference {
		return fmt.Errorf("%w: HEAD is detached; check out %s to save", ErrWrongBranch, s.mainBranch)
	}
	if head.Target() != s.mainRef() {
		return fmt.Errorf("%w: HEAD is on %s; check out %s (or set main_branch) to save", ErrWrongBranch, head.Target().Short(), s.mainBranch)
	}
	return nil
}

// open opens the repository afresh. Every operation does, rather than
// keeping one open: go-git caches the list of pack files, and another
// process (git gc, a commit from another orgtd) may change what's on
// disk at any time.
func (s *Store) open() (*git.Repository, error) {
	return git.PlainOpen(s.dir)
}

// Head returns the commit the main branch points at, or the zero hash if
// the branch has no commits yet.
func (s *Store) Head() (plumbing.Hash, error) {
	repo, err := s.open()
	if err != nil {
		return plumbing.ZeroHash, err
	}
	return headOf(repo, s.mainRef())
}

func headOf(repo *git.Repository, name plumbing.ReferenceName) (plumbing.Hash, error) {
	ref, err := repo.Storer.Reference(name)
	if errors.Is(err, plumbing.ErrReferenceNotFound) {
		return plumbing.ZeroHash, nil
	}
	if err != nil {
		return plumbing.ZeroHash, err
	}
	if ref.Type() != plumbing.HashReference {
		return plumbing.ZeroHash, fmt.Errorf("%s is not a plain reference", name)
	}
	return ref.Hash(), nil
}

// IsManaged reports whether rel (slash-separated, relative to the org
// directory) is a file orgtd manages: a *.org file directly in the org
// directory or directly in reference/, whose base name doesn't match an
// outline_ignore_files glob. Only managed paths take part in merges;
// every other path in the repository is left as it is on the main
// branch.
func (s *Store) IsManaged(rel string) bool {
	if path.Ext(rel) != ".org" {
		return false
	}
	dir, name := path.Split(rel)
	if dir != "" && dir != "reference/" {
		return false
	}
	if strings.HasPrefix(name, ".") {
		return false
	}
	for _, g := range s.ignore {
		if ok, _ := path.Match(g, name); ok {
			return false
		}
	}
	return true
}

// Rel converts an absolute path inside the org directory to the
// slash-separated relative form the Store uses.
func (s *Store) Rel(abs string) (string, error) {
	rel, err := filepath.Rel(s.dir, abs)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("%s is outside %s", abs, s.dir)
	}
	return filepath.ToSlash(rel), nil
}

// Abs is the inverse of Rel.
func (s *Store) Abs(rel string) string {
	return filepath.Join(s.dir, filepath.FromSlash(rel))
}
