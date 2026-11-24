package gitstore

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	format "github.com/go-git/go-git/v5/plumbing/format/config"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// PushStatus is what a bare `git push`, run on the main branch, would
// push it to, and how far the main branch is ahead of what this
// repository last saw there. It's worked out from git's config the way
// git push does (see pushDest); nothing is fetched, so it's only as
// fresh as the last fetch or push.
type PushStatus struct {
	// Remote and Branch are where git push would push the main branch
	// (its remote's name and the branch on it, e.g. "origin" and "main");
	// both empty if it wouldn't push it anywhere: no remote, no upstream
	// where push.default needs one, push.default=nothing, ...
	Remote, Branch string

	// Tracking is the remote-tracking ref that remembers Branch, e.g.
	// refs/remotes/origin/main; empty if the remote's fetch refspecs
	// don't map Branch to one.
	Tracking plumbing.ReferenceName

	// Known reports whether Tracking exists; if not (never fetched or
	// pushed there), Ahead isn't known.
	Known bool

	// Ahead is how many commits on the main branch Tracking doesn't
	// have: what git push would send.
	Ahead int
}

// Name is the remote-tracking ref's short name, as git status says it
// ("origin/main"), or remote/branch if there's none.
func (p PushStatus) Name() string {
	if p.Tracking != "" {
		return strings.TrimPrefix(p.Tracking.String(), "refs/remotes/")
	}
	return p.Remote + "/" + p.Branch
}

// aheadCount is a cached Ahead for one main/tracking pair, so polling
// doesn't walk the history again until either moves.
type aheadCount struct {
	main, tracking plumbing.Hash
	n              int
}

// PushStatus reports where a bare `git push` would push the main branch
// and how many commits it would send (see PushStatus). The zero value,
// with no error, means it wouldn't push it anywhere.
func (s *Store) PushStatus() (PushStatus, error) {
	repo, err := s.open()
	if err != nil {
		return PushStatus{}, err
	}
	cfg, err := loadGitConfig(repo)
	if err != nil {
		return PushStatus{}, err
	}
	p, ok := pushDest(cfg, s.mainBranch)
	if !ok {
		return PushStatus{}, nil
	}
	if p.Tracking == "" {
		return p, nil
	}
	tracking, err := headOf(repo, p.Tracking)
	if err != nil || tracking.IsZero() {
		return p, err
	}
	p.Known = true
	main, err := headOf(repo, s.mainRef())
	if err != nil || main.IsZero() {
		return p, err
	}
	if c := s.ahead.Load(); c != nil && c.main == main && c.tracking == tracking {
		p.Ahead = c.n
		return p, nil
	}
	if p.Ahead, err = countAhead(repo, main, tracking); err != nil {
		return p, err
	}
	s.ahead.Store(&aheadCount{main: main, tracking: tracking, n: p.Ahead})
	return p, nil
}

// countAhead counts the commits reachable from main but not from
// tracking (git rev-list --count tracking..main).
func countAhead(repo *git.Repository, main, tracking plumbing.Hash) (int, error) {
	have := map[plumbing.Hash]bool{}
	if c, err := repo.CommitObject(tracking); err != nil {
		if !errors.Is(err, plumbing.ErrObjectNotFound) {
			return 0, err
		}
		// A tracking ref naming a commit that isn't here (a shallow or
		// broken clone): count as if the remote had nothing.
	} else if err := object.NewCommitPreorderIter(c, nil, nil).ForEach(func(c *object.Commit) error {
		have[c.Hash] = true
		return nil
	}); err != nil {
		return 0, err
	}
	c, err := repo.CommitObject(main)
	if err != nil {
		return 0, err
	}
	n := 0
	err = object.NewCommitPreorderIter(c, have, nil).ForEach(func(*object.Commit) error {
		n++
		return nil
	})
	return n, err
}

// gitConfig is git's configuration as git itself would see it: the
// system, global and repository files, a later one overriding an
// earlier one, key by key. (go-git's ConfigScoped merges only the
// fields it models, not push.default and the like.) Include directives
// aren't followed.
type gitConfig []*format.Config

// get is the last value set for section.name (subsection, if not
// empty), as git reads a single-valued key.
func (c gitConfig) get(section, subsection, name string) string {
	v := ""
	for _, f := range c {
		if vs := c.values(f, section, subsection, name); len(vs) > 0 {
			v = vs[len(vs)-1]
		}
	}
	return v
}

// all is every value set for a multi-valued key, in order.
func (c gitConfig) all(section, subsection, name string) []string {
	var out []string
	for _, f := range c {
		out = append(out, c.values(f, section, subsection, name)...)
	}
	return out
}

func (gitConfig) values(f *format.Config, section, subsection, name string) []string {
	if !f.HasSection(section) {
		return nil
	}
	s := f.Section(section)
	if subsection == "" {
		return s.Options.GetAll(name)
	}
	if !s.HasSubsection(subsection) {
		return nil
	}
	return s.Subsection(subsection).Options.GetAll(name)
}

// userConfigPaths lists the system and global git config files, in the
// order git reads them. A variable so tests can keep the user's own
// config out.
var userConfigPaths = func() []string {
	var paths []string
	if os.Getenv("GIT_CONFIG_NOSYSTEM") == "" {
		paths = append(paths, "/etc/gitconfig")
	}
	if g := os.Getenv("GIT_CONFIG_GLOBAL"); g != "" {
		return append(paths, g)
	}
	xdg := os.Getenv("XDG_CONFIG_HOME")
	home, _ := os.UserHomeDir()
	if xdg == "" && home != "" {
		xdg = filepath.Join(home, ".config")
	}
	if xdg != "" {
		paths = append(paths, filepath.Join(xdg, "git", "config"))
	}
	if home != "" {
		paths = append(paths, filepath.Join(home, ".gitconfig"))
	}
	return paths
}

// loadGitConfig reads the system and global config files that exist,
// then the repository's own.
func loadGitConfig(repo *git.Repository) (gitConfig, error) {
	var cfg gitConfig
	for _, p := range userConfigPaths() {
		f, err := os.Open(p)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		raw := format.New()
		err = format.NewDecoder(f).Decode(raw)
		f.Close()
		if err != nil {
			return nil, err
		}
		cfg = append(cfg, raw)
	}
	local, err := repo.Config()
	if err != nil {
		return nil, err
	}
	return append(cfg, local.Raw), nil
}

// pushDest works out where a bare `git push` on branch would push it,
// following git's own rules (builtin/push.c): the remote is
// branch.<name>.pushRemote, else remote.pushDefault, else the branch's
// upstream remote, else origin. If that remote has push refspecs, they
// decide the destination branch; otherwise push.default (simple unless
// set) does. ok is false where git push wouldn't push branch: the
// remote isn't configured, or push.default needs an upstream it doesn't
// have.
func pushDest(cfg gitConfig, branch string) (p PushStatus, ok bool) {
	ref := plumbing.NewBranchReferenceName(branch)
	fetchRemote := cfg.get("branch", branch, "remote")
	upstream := plumbing.ReferenceName(cfg.get("branch", branch, "merge"))
	remote := cfg.get("branch", branch, "pushRemote")
	if remote == "" {
		remote = cfg.get("remote", "", "pushDefault")
	}
	if remote == "" {
		remote = fetchRemote
	}
	if remote == "" {
		remote = "origin"
	}
	if cfg.get("remote", remote, "url") == "" && cfg.get("remote", remote, "pushurl") == "" {
		return p, false // "." (this repository), a bare URL, or no remote at all
	}
	if fetchRemote == "" {
		fetchRemote = "origin"
	}

	var dst plumbing.ReferenceName
	if specs := cfg.all("remote", remote, "push"); len(specs) > 0 {
		rs, found := matchRefSpec(specs, ref)
		if !found {
			return p, false
		}
		dst = rs.Dst(ref)
	} else {
		hasUpstream := upstream != "" && cfg.get("branch", branch, "remote") == remote
		autoSetup := strings.EqualFold(cfg.get("push", "", "autoSetupRemote"), "true")
		switch mode := strings.ToLower(cfg.get("push", "", "default")); mode {
		case "nothing":
			return p, false
		case "current", "matching":
			dst = ref
		case "upstream", "tracking":
			switch {
			case hasUpstream:
				dst = upstream
			case upstream == "" && autoSetup:
				dst = ref
			default:
				return p, false
			}
		default: // "simple", or unset
			switch {
			case remote != fetchRemote: // a triangular workflow: as current
				dst = ref
			case hasUpstream && upstream == ref:
				dst = upstream
			case upstream == "" && autoSetup:
				dst = ref
			default:
				return p, false // no upstream, or one with another name
			}
		}
	}
	if !dst.IsBranch() {
		return p, false
	}
	p.Remote, p.Branch = remote, dst.Short()
	if rs, found := matchRefSpec(cfg.all("remote", remote, "fetch"), dst); found {
		p.Tracking = rs.Dst(dst)
	}
	return p, true
}

// matchRefSpec is the first of specs (refspec strings, as in a remote's
// push or fetch config) that maps ref, skipping negative ones and any
// that don't parse.
func matchRefSpec(specs []string, ref plumbing.ReferenceName) (config.RefSpec, bool) {
	for _, s := range specs {
		rs := config.RefSpec(s)
		if strings.HasPrefix(s, "^") || rs.Validate() != nil {
			continue
		}
		if rs.Match(ref) {
			return rs, true
		}
	}
	return "", false
}
