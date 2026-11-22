package gitstore

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/storer"
)

// files maps a managed path to its content.
type files map[string][]byte

// treeOf returns commit's tree hash, or the zero hash for the zero
// commit (an unborn branch: an empty tree).
func treeOf(repo *git.Repository, commit plumbing.Hash) (plumbing.Hash, error) {
	if commit.IsZero() {
		return plumbing.ZeroHash, nil
	}
	c, err := repo.CommitObject(commit)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("reading commit %s: %w", commit, err)
	}
	return c.TreeHash, nil
}

// managedFiles reads every managed path in tree (see IsManaged).
func (s *Store) managedFiles(repo *git.Repository, tree plumbing.Hash) (files, error) {
	out := files{}
	if tree.IsZero() {
		return out, nil
	}
	t, err := repo.TreeObject(tree)
	if err != nil {
		return nil, fmt.Errorf("reading tree %s: %w", tree, err)
	}
	read := func(prefix string, t *object.Tree) error {
		for _, e := range t.Entries {
			rel := prefix + e.Name
			if !e.Mode.IsFile() || !s.IsManaged(rel) {
				continue
			}
			data, err := readBlob(repo, e.Hash)
			if err != nil {
				return fmt.Errorf("reading %s: %w", rel, err)
			}
			out[rel] = data
		}
		return nil
	}
	if err := read("", t); err != nil {
		return nil, err
	}
	for _, e := range t.Entries {
		if e.Name == "reference" && e.Mode == filemode.Dir {
			sub, err := repo.TreeObject(e.Hash)
			if err != nil {
				return nil, err
			}
			if err := read("reference/", sub); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// Snapshot returns the content of every managed file in commit (the
// zero hash: none).
func (s *Store) Snapshot(commit plumbing.Hash) (map[string][]byte, error) {
	repo, err := s.open()
	if err != nil {
		return nil, err
	}
	tree, err := treeOf(repo, commit)
	if err != nil {
		return nil, err
	}
	return s.managedFiles(repo, tree)
}

func readBlob(repo *git.Repository, h plumbing.Hash) ([]byte, error) {
	b, err := repo.BlobObject(h)
	if err != nil {
		return nil, err
	}
	r, err := b.Reader()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

func writeBlob(st storer.EncodedObjectStorer, data []byte) (plumbing.Hash, error) {
	obj := st.NewEncodedObject()
	obj.SetType(plumbing.BlobObject)
	w, err := obj.Writer()
	if err != nil {
		return plumbing.ZeroHash, err
	}
	if _, err := w.Write(data); err != nil {
		return plumbing.ZeroHash, err
	}
	if err := w.Close(); err != nil {
		return plumbing.ZeroHash, err
	}
	return st.SetEncodedObject(obj)
}

// blobHash is the hash git gives data as a blob, without storing it.
func blobHash(data []byte) plumbing.Hash {
	return plumbing.ComputeHash(plumbing.BlobObject, data)
}

// editTree returns the hash of tree with changes applied: each path gets
// the given content, or is removed if its content is nil. Paths may be
// nested ("reference/x.org"). A directory left empty is removed. The
// zero hash stands for an empty tree, in and out alike.
func editTree(repo *git.Repository, tree plumbing.Hash, changes map[string][]byte) (plumbing.Hash, error) {
	var entries []object.TreeEntry
	if !tree.IsZero() {
		t, err := repo.TreeObject(tree)
		if err != nil {
			return plumbing.ZeroHash, fmt.Errorf("reading tree %s: %w", tree, err)
		}
		entries = append(entries, t.Entries...)
	}

	direct := map[string][]byte{}
	nested := map[string]map[string][]byte{}
	for p, data := range changes {
		if dir, rest, ok := strings.Cut(p, "/"); ok {
			if nested[dir] == nil {
				nested[dir] = map[string][]byte{}
			}
			nested[dir][rest] = data
		} else {
			direct[p] = data
		}
	}

	byName := map[string]int{}
	for i, e := range entries {
		byName[e.Name] = i
	}
	set := func(e object.TreeEntry, remove bool) {
		i, ok := byName[e.Name]
		switch {
		case remove && ok:
			entries[i].Name = "" // dropped below
		case remove:
		case ok:
			entries[i] = e
		default:
			byName[e.Name] = len(entries)
			entries = append(entries, e)
		}
	}
	for name, data := range direct {
		if data == nil {
			set(object.TreeEntry{Name: name}, true)
			continue
		}
		h, err := writeBlob(repo.Storer, data)
		if err != nil {
			return plumbing.ZeroHash, err
		}
		mode := filemode.Regular
		if i, ok := byName[name]; ok && entries[i].Mode == filemode.Executable {
			mode = filemode.Executable
		}
		set(object.TreeEntry{Name: name, Mode: mode, Hash: h}, false)
	}
	for dir, sub := range nested {
		var subTree plumbing.Hash
		if i, ok := byName[dir]; ok && entries[i].Mode == filemode.Dir {
			subTree = entries[i].Hash
		}
		h, err := editTree(repo, subTree, sub)
		if err != nil {
			return plumbing.ZeroHash, err
		}
		set(object.TreeEntry{Name: dir, Mode: filemode.Dir, Hash: h}, h.IsZero())
	}

	kept := entries[:0]
	for _, e := range entries {
		if e.Name != "" {
			kept = append(kept, e)
		}
	}
	if len(kept) == 0 {
		return plumbing.ZeroHash, nil
	}
	// Git orders entries by name, comparing a directory as if its name
	// ended in "/".
	key := func(e object.TreeEntry) string {
		if e.Mode == filemode.Dir {
			return e.Name + "/"
		}
		return e.Name
	}
	sort.Slice(kept, func(i, j int) bool { return key(kept[i]) < key(kept[j]) })

	obj := repo.Storer.NewEncodedObject()
	if err := (&object.Tree{Entries: kept}).Encode(obj); err != nil {
		return plumbing.ZeroHash, err
	}
	return repo.Storer.SetEncodedObject(obj)
}

// emptyTree stores git's empty tree and returns its hash, for a commit
// that has no files.
func emptyTree(repo *git.Repository) (plumbing.Hash, error) {
	obj := repo.Storer.NewEncodedObject()
	if err := (&object.Tree{}).Encode(obj); err != nil {
		return plumbing.ZeroHash, err
	}
	return repo.Storer.SetEncodedObject(obj)
}

// commit stores a commit of tree (zero: empty) with the given parent
// (zero: none) and message, authored now by the user's git identity.
func commit(repo *git.Repository, tree, parent plumbing.Hash, msg string) (plumbing.Hash, error) {
	if tree.IsZero() {
		var err error
		if tree, err = emptyTree(repo); err != nil {
			return plumbing.ZeroHash, err
		}
	}
	sig := signature(repo)
	c := &object.Commit{
		Author:    sig,
		Committer: sig,
		Message:   msg,
		TreeHash:  tree,
	}
	if !parent.IsZero() {
		c.ParentHashes = []plumbing.Hash{parent}
	}
	obj := repo.Storer.NewEncodedObject()
	if err := c.Encode(obj); err != nil {
		return plumbing.ZeroHash, err
	}
	return repo.Storer.SetEncodedObject(obj)
}

// signature is the user's git identity (user.name and user.email from
// the repository's or the global git config), or "orgtd" if unset.
func signature(repo *git.Repository) object.Signature {
	sig := object.Signature{Name: "orgtd", Email: "orgtd@localhost", When: time.Now()}
	cfg, err := repo.ConfigScoped(config.GlobalScope)
	if err != nil {
		return sig
	}
	if cfg.User.Name != "" {
		sig.Name = cfg.User.Name
	}
	if cfg.User.Email != "" {
		sig.Email = cfg.User.Email
	}
	return sig
}

// sameFiles reports whether a and b hold the same paths with the same
// contents.
func sameFiles(a, b files) bool {
	if len(a) != len(b) {
		return false
	}
	for p, x := range a {
		y, ok := b[p]
		if !ok || !bytes.Equal(x, y) {
			return false
		}
	}
	return true
}

var errNotFound = errors.New("not found")
