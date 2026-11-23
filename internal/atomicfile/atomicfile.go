// Package atomicfile replaces a file's content atomically and durably:
// readers see either the old content or the new, never a mix or a
// truncated file, and once Write returns the new content survives a
// crash or power loss.
package atomicfile

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// Write replaces the content of the file at path with data. It writes a
// temp file with a unique name in the same directory, fsyncs it, renames
// it over path, and fsyncs the directory so the rename itself is on
// disk. On any error the temp file is removed and path is left as it
// was.
//
// An existing file keeps its permission bits. A new file is created
// with perm, less the umask, as os.WriteFile would. The directory must
// already exist. If path is a symlink, the link itself is replaced by a
// regular file.
func Write(path string, data []byte, perm fs.FileMode) error {
	tmp, err := createTemp(path, perm)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()

	if fi, err := os.Stat(path); err == nil {
		if err := tmp.Chmod(fi.Mode().Perm()); err != nil {
			return err
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	ok = true
	return syncDir(filepath.Dir(path))
}

// createTemp creates a new file next to path, named after it but hidden
// and unique, with perm (less the umask). os.CreateTemp isn't used
// because it always creates the file 0600.
func createTemp(path string, perm fs.FileMode) (*os.File, error) {
	dir, base := filepath.Split(path)
	for range 100 {
		var b [8]byte
		rand.Read(b[:])
		name := filepath.Join(dir, "."+base+"."+hex.EncodeToString(b[:])+".tmp")
		f, err := os.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, perm)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		return f, err
	}
	return nil, &fs.PathError{Op: "createtemp", Path: path, Err: fs.ErrExist}
}

// syncDir fsyncs dir, making a rename within it durable. Filesystems
// that can't fsync a directory (EINVAL, ENOTSUP) are let through: the
// rename has already happened, and there's nothing more to do.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = d.Sync()
	cerr := d.Close()
	if errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOTSUP) {
		err = nil
	}
	if err != nil {
		return err
	}
	return cerr
}
