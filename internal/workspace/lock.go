package workspace

import (
	"fmt"
	"path/filepath"

	"github.com/gofrs/flock"
)

// LockFileName is the advisory lock file AcquireLock creates directly
// inside the org directory — not a ".org" file, so Load never picks it
// up as workspace content.
const LockFileName = ".orgtd.lock"

// Lock is an acquired advisory lock on an org directory, held for as
// long as the orgtd process holding it is running. See AcquireLock.
type Lock struct {
	fl *flock.Flock
}

// AcquireLock takes an exclusive, OS-level advisory lock on dir (via a
// small file directly inside it — see LockFileName), so a second orgtd
// instance started against the same directory can detect the first one
// and refuse to run, rather than both loading the directory into memory
// and silently racing each other to overwrite the same files with
// whichever happened to :w last.
//
// Unlike a plain lock file that just records a PID, an OS-level
// advisory lock is released automatically the moment the holding
// process exits for any reason — including a crash — so there's no
// stale-lock case to detect or clean up by hand; a leftover
// LockFileName file with nobody holding its lock is simply acquirable
// again. The trade-off is the reverse of that convenience: advisory
// locks aren't reliably enforced on every filesystem (older NFS in
// particular), so this isn't a substitute for actual multi-user
// coordination — it's a best-effort guard against the common case of
// one person accidentally opening the same directory twice.
//
// ok is false (with a nil error) if another process already holds the
// lock — a normal, expected outcome, not a failure. err reports an
// actual problem acquiring it (e.g. the directory itself is
// unwritable).
func AcquireLock(dir string) (lock *Lock, ok bool, err error) {
	fl := flock.New(filepath.Join(dir, LockFileName))
	locked, err := fl.TryLock()
	if err != nil {
		return nil, false, fmt.Errorf("workspace: locking %s: %w", dir, err)
	}
	if !locked {
		return nil, false, nil
	}
	return &Lock{fl: fl}, true, nil
}

// Release releases l, allowing another orgtd instance to acquire the
// lock for the same directory. Safe to call on a nil *Lock. Callers
// don't strictly need to call this before exiting — the OS releases the
// underlying lock on its own once the process is gone either way — but
// it lets a long-running process (the normal case) give up the lock
// without exiting entirely, if that's ever needed.
func (l *Lock) Release() error {
	if l == nil {
		return nil
	}
	return l.fl.Unlock()
}
