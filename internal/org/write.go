package org

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// hashContent fingerprints a file's bytes for ChangedOnDisk.
func hashContent(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// ChangedOnDisk reports whether f.Path no longer holds what orgtd last
// read from or wrote to it (f.DiskHash) — e.g. a `git pull` or another
// editor rewrote it since — so that writing f would silently clobber
// that change. Compares content rather than mtime, so a touch or a
// checkout that leaves the bytes the same isn't a change. A file that's
// gone from disk counts as changed. A file with no recorded hash (never
// read from or written to disk) is never reported as changed.
func ChangedOnDisk(f *File) (bool, error) {
	if f.DiskHash == "" {
		return false, nil
	}
	data, err := os.ReadFile(f.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("org: checking %s: %w", f.Path, err)
	}
	return hashContent(data) != f.DiskHash, nil
}

// WriteFile renders f and writes it to f.Path atomically: the new
// content is written to a temp file in the same directory, then renamed
// over the original, so a crash or error mid-write can never leave a
// truncated or corrupted file on disk.
func WriteFile(f *File) error {
	dir := filepath.Dir(f.Path)

	tmp, err := os.CreateTemp(dir, ".orgtd-write-*.tmp")
	if err != nil {
		return fmt.Errorf("org: creating temp file for %s: %w", f.Path, err)
	}
	tmpPath := tmp.Name()

	content := RenderFile(f)
	_, writeErr := tmp.WriteString(content)
	closeErr := tmp.Close()
	if writeErr != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("org: writing %s: %w", f.Path, writeErr)
	}
	if closeErr != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("org: writing %s: %w", f.Path, closeErr)
	}

	if err := os.Rename(tmpPath, f.Path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("org: replacing %s: %w", f.Path, err)
	}
	f.DiskHash = hashContent([]byte(content))
	return nil
}
