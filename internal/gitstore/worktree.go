package gitstore

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-git/go-billy/v5/osfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
	"github.com/go-git/go-git/v5/plumbing/format/index"

	"github.com/sburnett/orgtd/internal/atomicfile"
)

// workingFiles reads every managed file in the working tree that git
// would track: one already in tracked (the main branch's managed files),
// or one not ignored by .gitignore, .git/info/exclude or the global
// excludes file. A managed file that git ignores and doesn't track, such
// as a calendar cache, isn't part of the repository at all (see
// isUntrackedIgnored).
func (s *Store) workingFiles(tracked files) (files, error) {
	out := files{}
	matcher := s.ignoreMatcher()
	for _, dir := range []string{"", "reference/"} {
		entries, err := os.ReadDir(filepath.Join(s.dir, filepath.FromSlash(dir)))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			rel := dir + e.Name()
			if !e.Type().IsRegular() || !s.IsManaged(rel) {
				continue
			}
			if _, ok := tracked[rel]; !ok && matcher.Match(strings.Split(rel, "/"), false) {
				continue
			}
			data, err := os.ReadFile(s.Abs(rel))
			if err != nil {
				return nil, err
			}
			out[rel] = data
		}
	}
	return out, nil
}

// isUntrackedIgnored reports whether rel is ignored by git and not in
// tracked: a file git leaves out of the repository, which a save writes
// straight to disk instead of committing.
func (s *Store) isUntrackedIgnored(rel string, tracked files) bool {
	if _, ok := tracked[rel]; ok {
		return false
	}
	return s.ignoreMatcher().Match(strings.Split(rel, "/"), false)
}

// ignoreMatcher reads the ignore rules that can apply to a managed path:
// .git/info/exclude, the global excludes file, and the .gitignore files
// in the org directory and reference/. (A managed path is never deeper,
// so no other .gitignore can apply.)
func (s *Store) ignoreMatcher() gitignore.Matcher {
	var ps []gitignore.Pattern
	if global, err := gitignore.LoadGlobalPatterns(osfs.New("/")); err == nil {
		ps = append(ps, global...)
	}
	readFile := func(p string, domain []string) {
		f, err := os.Open(p)
		if err != nil {
			return
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
				continue
			}
			ps = append(ps, gitignore.ParsePattern(line, domain))
		}
	}
	readFile(filepath.Join(s.gitDir, "info", "exclude"), nil)
	readFile(filepath.Join(s.dir, ".gitignore"), nil)
	readFile(filepath.Join(s.dir, "reference", ".gitignore"), []string{"reference"})
	return gitignore.NewMatcher(ps)
}

// outsideEdits compares the working tree to tracked (the main branch's
// managed files) and returns every difference as a change for editTree:
// new or changed content, or nil for a tracked file deleted from disk.
func (s *Store) outsideEdits(tracked files) (map[string][]byte, error) {
	working, err := s.workingFiles(tracked)
	if err != nil {
		return nil, err
	}
	changes := map[string][]byte{}
	for p, data := range working {
		if old, ok := tracked[p]; !ok || !bytes.Equal(old, data) {
			changes[p] = data
		}
	}
	for p := range tracked {
		if _, ok := working[p]; !ok {
			changes[p] = nil
		}
	}
	return changes, nil
}

// writeFile writes data to rel atomically (see internal/atomicfile),
// creating the directory if need be. nil data removes the file.
func (s *Store) writeFile(rel string, data []byte) error {
	p := s.Abs(rel)
	if data == nil {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := atomicfile.Write(p, data, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", rel, err)
	}
	return nil
}

// ErrIndexLocked is returned when .git/index.lock already exists: a git
// command is running, or one crashed and left it behind.
var ErrIndexLocked = errors.New(".git/index.lock exists — another git process seems to be running; if not, remove the file and try again")

// lockIndex creates .git/index.lock, git's own lock on the index, so a
// git command that writes the index or the working tree (commit, pull,
// checkout) fails fast instead of racing a save. The new index is
// written into it (see writeIndex) and it's renamed over .git/index by
// commitIndex, as git does.
func (s *Store) lockIndex() (*os.File, error) {
	f, err := os.OpenFile(filepath.Join(s.gitDir, "index.lock"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return nil, ErrIndexLocked
	}
	return f, err
}

func (s *Store) unlockIndex(f *os.File) {
	f.Close()
	os.Remove(f.Name())
}

// writeIndex writes the current index, with every path in changes
// updated to its new content (nil: removed), into lock. The cached-tree
// extension is dropped, since it would describe trees that no longer
// match; git rebuilds it.
func (s *Store) writeIndex(repo *git.Repository, lock *os.File, changes map[string][]byte) error {
	idx, err := repo.Storer.Index()
	if err != nil {
		idx = &index.Index{Version: 2}
	}
	if idx.Version == 0 {
		idx.Version = 2
	}
	byName := map[string]*index.Entry{}
	for _, e := range idx.Entries {
		if e.Stage == index.Merged {
			byName[e.Name] = e
		}
	}
	for rel, data := range changes {
		// An unmerged path (stages 1-3) is resolved by this change.
		kept := idx.Entries[:0]
		for _, e := range idx.Entries {
			if e.Name != rel || e.Stage == index.Merged {
				kept = append(kept, e)
			}
		}
		idx.Entries = kept
		if data == nil {
			idx.Entries = removeEntry(idx.Entries, rel)
			continue
		}
		e, ok := byName[rel]
		if !ok {
			e = idx.Add(rel)
		}
		e.Hash = blobHash(data)
		e.Mode = filemode.Regular
		e.Size = uint32(len(data))
		e.IntentToAdd = false
		if fi, err := os.Stat(s.Abs(rel)); err == nil {
			e.ModifiedAt = fi.ModTime()
			e.CreatedAt = fi.ModTime()
			e.Size = uint32(fi.Size())
			if fi.Mode()&0o111 != 0 {
				e.Mode = filemode.Executable
			}
		}
	}
	sort.Slice(idx.Entries, func(i, j int) bool {
		if idx.Entries[i].Name != idx.Entries[j].Name {
			return idx.Entries[i].Name < idx.Entries[j].Name
		}
		return idx.Entries[i].Stage < idx.Entries[j].Stage
	})
	idx.Cache = nil
	idx.EndOfIndexEntry = nil
	if err := index.NewEncoder(lock).Encode(idx); err != nil {
		return fmt.Errorf("writing index: %w", err)
	}
	return lock.Sync()
}

func removeEntry(entries []*index.Entry, name string) []*index.Entry {
	out := entries[:0]
	for _, e := range entries {
		if e.Name != name {
			out = append(out, e)
		}
	}
	return out
}

// commitIndex renames the written index.lock over .git/index.
func (s *Store) commitIndex(lock *os.File) error {
	if err := lock.Close(); err != nil {
		return err
	}
	return os.Rename(lock.Name(), filepath.Join(s.gitDir, "index"))
}

// indexHash returns rel's blob hash in the index, if it has one.
func indexHash(repo *git.Repository, rel string) (plumbing.Hash, bool) {
	idx, err := repo.Storer.Index()
	if err != nil {
		return plumbing.ZeroHash, false
	}
	e, err := idx.Entry(rel)
	if err != nil {
		return plumbing.ZeroHash, false
	}
	return e.Hash, true
}
