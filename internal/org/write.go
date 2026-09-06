package org

import (
	"fmt"
	"os"
	"path/filepath"
)

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

	_, writeErr := tmp.WriteString(RenderFile(f))
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
	return nil
}
