package ui

import (
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"

	"github.com/sburnett/orgtd/internal/gitstore"
)

// initRepo makes dir the root of a git repository on branch main, with
// every managed file already in it committed — what saving needs (see
// gitstore). Safe to call on a directory that already is one.
func initRepo(t *testing.T, dir string) {
	t.Helper()
	if _, err := git.PlainInitWithOptions(dir, &git.PlainInitOptions{
		InitOptions: git.InitOptions{DefaultBranch: plumbing.Main},
	}); err != nil && err != git.ErrRepositoryAlreadyExists {
		t.Fatal(err)
	}
	s, err := gitstore.Open(dir, "main", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.CommitOutsideEdits(); err != nil {
		t.Fatal(err)
	}
}
