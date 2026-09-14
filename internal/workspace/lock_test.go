package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAcquireLockSucceedsOnce(t *testing.T) {
	dir := t.TempDir()
	lock, ok, err := AcquireLock(dir)
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	if !ok {
		t.Fatalf("AcquireLock ok = false, want true (nothing else holds it)")
	}
	defer lock.Release()

	if _, err := os.Stat(filepath.Join(dir, LockFileName)); err != nil {
		t.Errorf("lock file not created: %v", err)
	}
}

func TestAcquireLockRefusesWhileHeld(t *testing.T) {
	dir := t.TempDir()
	first, ok, err := AcquireLock(dir)
	if err != nil || !ok {
		t.Fatalf("first AcquireLock: ok=%v err=%v", ok, err)
	}
	defer first.Release()

	second, ok, err := AcquireLock(dir)
	if err != nil {
		t.Fatalf("second AcquireLock: unexpected error %v", err)
	}
	if ok {
		t.Fatalf("second AcquireLock ok = true, want false (already held)")
	}
	if second != nil {
		t.Errorf("second AcquireLock lock = %v, want nil", second)
	}
}

func TestAcquireLockSucceedsAfterRelease(t *testing.T) {
	dir := t.TempDir()
	first, ok, err := AcquireLock(dir)
	if err != nil || !ok {
		t.Fatalf("first AcquireLock: ok=%v err=%v", ok, err)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}

	second, ok, err := AcquireLock(dir)
	if err != nil {
		t.Fatalf("second AcquireLock: %v", err)
	}
	if !ok {
		t.Fatalf("second AcquireLock ok = false, want true (lock was released)")
	}
	defer second.Release()
}

func TestLockReleaseOnNilIsNoop(t *testing.T) {
	var l *Lock
	if err := l.Release(); err != nil {
		t.Errorf("Release on nil *Lock = %v, want nil", err)
	}
}

func TestAcquireLockErrorsOnNonexistentDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does-not-exist")
	_, ok, err := AcquireLock(dir)
	if err == nil {
		t.Fatalf("AcquireLock on a nonexistent directory: want an error, got ok=%v", ok)
	}
}
