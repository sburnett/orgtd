// Package gitrepo runs the handful of git commands orgtd uses (diff,
// ls-files, add, commit, push) against the org directory, logging each
// through execlog so :log shows exactly what ran.
//
// Every mutating command (Add, Commit, Push) refuses unless Dir is itself
// the *root* of its git repository — see RootRefusal for why. Nothing in
// this package knows about the UI.
package gitrepo

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/sburnett/orgtd/internal/execlog"
)

// Repo is a git working tree orgtd operates on: Dir is the org
// directory, and Log (which may be nil, disabling logging) records every
// command run. It is a plain value, safe to capture and use from a
// background goroutine without reaching back into UI state.
type Repo struct {
	Dir string
	Log *execlog.Log
}

// run runs git with args, logged.
func (r Repo) run(args ...string) (string, error) {
	return execlog.Run(r.Log, "git", args, "")
}

// Root returns the git repository root that contains Dir — via `git
// rev-parse --show-toplevel` — or "" if Dir isn't inside a git
// repository at all (or git itself failed).
func (r Repo) Root() string {
	out, err := r.run("-C", r.Dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// RootRefusal reports why mutating git commands (Add, Commit, Push — see
// RequireRoot) must refuse to run against Dir, or "" if they're fine to
// run. They're refused unless Dir is itself the *root* of its git
// repository, not merely somewhere inside one: git add/commit are scoped
// to specific files, so on their own a nested workspace wouldn't be too
// dangerous, but git push is not scoped to files at all — it pushes the
// *whole* current branch — and a workspace that's really just a
// subdirectory of some larger, unrelated repository (e.g. orgtd's own
// testdata/minimal, nested inside this very repo) must never have that run
// against it. Paths are compared after resolving symlinks (see
// filepath.EvalSymlinks) since e.g. macOS routes /tmp and /var through
// symlinks into /private — comparing raw paths would otherwise misreport
// plenty of genuinely rooted workspaces (anything under the system's temp
// dir included) as nested elsewhere.
func (r Repo) RootRefusal() string {
	root := r.Root()
	if root == "" {
		return "the org directory isn't inside a git repository"
	}
	wsResolved, wsErr := filepath.EvalSymlinks(r.Dir)
	rootResolved, rootErr := filepath.EvalSymlinks(root)
	if wsErr != nil || rootErr != nil || wsResolved != rootResolved {
		return fmt.Sprintf("the org directory isn't the root of its git repository (root is %s)", root)
	}
	return ""
}

// RequireRoot is the guard every mutating operation (Add, Commit, Push)
// checks before doing anything — see RootRefusal for why. Checked inside
// those operations themselves (rather than only at the higher-level call
// sites that ask about it first, for a better error message) so there's
// no way to reach an actual mutation without passing it, regardless of how
// it's eventually called.
func (r Repo) RequireRoot() error {
	if reason := r.RootRefusal(); reason != "" {
		return fmt.Errorf("%s", reason)
	}
	return nil
}

// Diff runs `git diff HEAD` scoped to paths, with git itself pointed at
// Dir (via -C, rather than relying on orgtd's own working directory) so a
// repository rooted there or above is found either way. Diffed against
// HEAD rather than a plain `git diff` (which only shows unstaged changes)
// so a file that was `git add`ed but not yet committed still shows up as
// an addition, instead of looking like nothing happened.
func (r Repo) Diff(paths []string) (string, error) {
	args := append([]string{"-C", r.Dir, "diff", "HEAD", "--"}, paths...)
	return r.run(args...)
}

// Untracked returns the paths, among paths, that git doesn't track at all
// yet — via `git ls-files --others --exclude-standard`, scoped to just
// those paths so files elsewhere in the repo (or gitignored entirely)
// never show up. Returns (nil, nil) if paths is empty or nothing is
// untracked; the error return is only for a genuine failure to even ask
// (not a git repository, git missing, ...) — callers treat that the same
// as "nothing untracked" and let the diff/commit that follows surface the
// real problem instead.
func (r Repo) Untracked(paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	args := append([]string{"-C", r.Dir, "ls-files", "--others", "--exclude-standard", "--"}, paths...)
	out, err := r.run(args...)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(out) == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// Add runs `git add` for exactly paths, so accepting an "add untracked
// files?" prompt never stages anything beyond what it named. Refuses
// unless Dir is its repository's root — see RequireRoot.
func (r Repo) Add(paths []string) (string, error) {
	if err := r.RequireRoot(); err != nil {
		return "", err
	}
	args := append([]string{"-C", r.Dir, "add", "--"}, paths...)
	return r.run(args...)
}

// Commit commits paths with message, from within Dir. Refuses unless Dir
// is its repository's root — see RequireRoot. Callers can tell git's
// harmless "nothing to commit" failure apart from a real one with
// IsNothingToCommit on the returned stdout.
func (r Repo) Commit(paths []string, message string) (string, error) {
	if err := r.RequireRoot(); err != nil {
		return "", err
	}
	args := append([]string{"-C", r.Dir, "commit", "-m", message, "--"}, paths...)
	return r.run(args...)
}

// Push runs a plain `git push` from within Dir. Unlike diff and commit, a
// push isn't scoped to particular files (there's no such thing as pushing
// only some files' history), so it just pushes the current branch to its
// configured upstream. This is exactly why RequireRoot matters most here:
// a push affects the whole repository's history, not just the org files
// orgtd knows about.
func (r Repo) Push() (string, error) {
	if err := r.RequireRoot(); err != nil {
		return "", err
	}
	return r.run("-C", r.Dir, "push")
}

// IsNothingToCommit reports whether stdout from a failed `git commit` is
// just git's own "nothing to commit" message rather than a real failure.
func IsNothingToCommit(stdout string) bool {
	return strings.Contains(stdout, "nothing to commit")
}

// ErrorText extracts the most useful message from a failed git
// invocation: git's own stderr (e.g. "fatal: not a git repository...")
// when there is one, else the raw error (e.g. "git" not being installed at
// all).
func ErrorText(err error) string {
	if exitErr, ok := err.(*exec.ExitError); ok {
		if msg := strings.TrimSpace(string(exitErr.Stderr)); msg != "" {
			return msg
		}
	}
	return err.Error()
}
