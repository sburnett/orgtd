package org

import (
	"fmt"
	"os"
	"path/filepath"
)

// WriteFile renders f and writes it to f.Path atomically: the new
// content is written to a temp file in the same directory, then renamed
// over the original, so a crash or error mid-write can never leave a
// truncated or corrupted file on disk. A New file's directory is
// created first if it's missing.
func WriteFile(f *File) error {
	dir := filepath.Dir(f.Path)
	if f.New {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("org: creating %s: %w", dir, err)
		}
	}

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
	f.Base = []byte(content)
	f.New = false
	return nil
}
