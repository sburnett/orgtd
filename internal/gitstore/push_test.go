package gitstore

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	format "github.com/go-git/go-git/v5/plumbing/format/config"
)

// noUserConfig keeps the user's own git config out of a test.
func noUserConfig(t *testing.T) {
	t.Helper()
	old := userConfigPaths
	userConfigPaths = func() []string { return nil }
	t.Cleanup(func() { userConfigPaths = old })
}

func parseConfig(t *testing.T, text string) gitConfig {
	t.Helper()
	raw := format.New()
	if err := format.NewDecoder(strings.NewReader(text)).Decode(raw); err != nil {
		t.Fatal(err)
	}
	return gitConfig{raw}
}

const originRemote = `
[remote "origin"]
	url = ssh://example/org.git
	fetch = +refs/heads/*:refs/remotes/origin/*
[remote "backup"]
	url = ssh://example/backup.git
	fetch = +refs/heads/*:refs/remotes/backup/*
`

const mainTracksOrigin = `
[branch "main"]
	remote = origin
	merge = refs/heads/main
`

func TestPushDest(t *testing.T) {
	tests := []struct {
		name   string
		config string
		want   string // "remote branch tracking", or "" for nowhere
	}{
		{"no remote at all", "", ""},
		{"origin, but no upstream", originRemote, ""},
		{"no upstream, autoSetupRemote", originRemote + "[push]\n\tautoSetupRemote = true\n",
			"origin main refs/remotes/origin/main"},
		{"upstream", originRemote + mainTracksOrigin, "origin main refs/remotes/origin/main"},
		{"upstream with another name, simple", originRemote + "[branch \"main\"]\n\tremote = origin\n\tmerge = refs/heads/trunk\n", ""},
		{"upstream with another name, push.default=upstream",
			originRemote + "[branch \"main\"]\n\tremote = origin\n\tmerge = refs/heads/trunk\n[push]\n\tdefault = upstream\n",
			"origin trunk refs/remotes/origin/trunk"},
		{"push.default=nothing", originRemote + mainTracksOrigin + "[push]\n\tdefault = nothing\n", ""},
		{"push.default=current, no upstream", originRemote + "[push]\n\tdefault = current\n",
			"origin main refs/remotes/origin/main"},
		{"remote.pushDefault: triangular, as current", originRemote + mainTracksOrigin + "[remote]\n\tpushDefault = backup\n",
			"backup main refs/remotes/backup/main"},
		{"branch pushRemote beats remote.pushDefault",
			originRemote + mainTracksOrigin + "[remote]\n\tpushDefault = nowhere\n[branch \"main\"]\n\tpushRemote = backup\n",
			"backup main refs/remotes/backup/main"},
		{"push remote not configured", originRemote + mainTracksOrigin + "[remote]\n\tpushDefault = nowhere\n", ""},
		{"upstream in this repository", "[branch \"main\"]\n\tremote = .\n\tmerge = refs/heads/other\n", ""},
		{"push refspec", originRemote + mainTracksOrigin + "[remote \"origin\"]\n\tpush = refs/heads/main:refs/heads/desk\n",
			"origin desk refs/remotes/origin/desk"},
		{"push refspec not matching main", originRemote + mainTracksOrigin + "[remote \"origin\"]\n\tpush = refs/heads/x:refs/heads/x\n", ""},
		{"no fetch refspec: destination, but no tracking ref",
			"[remote \"origin\"]\n\turl = ssh://example/org.git\n" + mainTracksOrigin, "origin main "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, ok := pushDest(parseConfig(t, tt.config), "main")
			got := ""
			if ok {
				got = p.Remote + " " + p.Branch + " " + string(p.Tracking)
			}
			if got != tt.want {
				t.Errorf("pushDest = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPushDestLaterConfigOverrides(t *testing.T) {
	global := parseConfig(t, "[push]\n\tdefault = nothing\n")
	local := parseConfig(t, originRemote+mainTracksOrigin+"[push]\n\tdefault = simple\n")
	if p, ok := pushDest(append(global, local...), "main"); !ok || p.Remote != "origin" {
		t.Errorf("local push.default didn't override the global one: %+v, %v", p, ok)
	}
	if _, ok := pushDest(append(local, global...), "main"); ok {
		t.Errorf("a later push.default=nothing didn't win")
	}
}

// appendConfig adds text to dir's .git/config.
func appendConfig(t *testing.T, dir, text string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(dir, ".git", "config"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
}

func setRef(t *testing.T, dir, name string, h plumbing.Hash) {
	t.Helper()
	repo, err := git.PlainOpen(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Storer.SetReference(plumbing.NewHashReference(plumbing.ReferenceName(name), h)); err != nil {
		t.Fatal(err)
	}
}

func TestPushStatusCountsCommitsToPush(t *testing.T) {
	noUserConfig(t)
	dir := newRepo(t, map[string]string{"todo.org": "* TODO A\n"})
	s := open(t, dir)
	if p, err := s.PushStatus(); err != nil || p != (PushStatus{}) {
		t.Fatalf("with no remote: %+v, %v", p, err)
	}

	appendConfig(t, dir, originRemote+mainTracksOrigin)
	p, err := s.PushStatus()
	if err != nil || p.Remote != "origin" || p.Known {
		t.Fatalf("before any fetch: %+v, %v", p, err)
	}

	pushed := head(t, s)
	setRef(t, dir, "refs/remotes/origin/main", pushed)
	if p, err := s.PushStatus(); err != nil || !p.Known || p.Ahead != 0 || p.Name() != "origin/main" {
		t.Fatalf("in sync: %+v, %v", p, err)
	}

	for i, c := range []string{"* TODO A\n* TODO B\n", "* TODO A\n* TODO B\n* TODO C\n"} {
		base := readDisk(t, dir, "todo.org")
		if _, err := s.Save(head(t, s), []Edit{edit("todo.org", base, c)}, "save"); err != nil {
			t.Fatal(err)
		}
		if p, err := s.PushStatus(); err != nil || p.Ahead != i+1 {
			t.Fatalf("after %d saves: %+v, %v", i+1, p, err)
		}
	}
	if n := gitAhead(t, dir); n >= 0 && n != 2 {
		t.Errorf("git rev-list --count main@{push}..main = %d, want 2", n)
	}

	setRef(t, dir, "refs/remotes/origin/main", head(t, s)) // pushed
	if p, err := s.PushStatus(); err != nil || p.Ahead != 0 {
		t.Fatalf("after pushing: %+v, %v", p, err)
	}
}

// gitAhead asks real git, when it's installed, how many commits a bare
// git push would send, with the user's own config kept out; -1 without
// git.
func gitAhead(t *testing.T, dir string) int {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		return -1
	}
	cmd := exec.Command("git", "rev-list", "--count", "main@{push}..main")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git rev-list: %v\n%s", err, out)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatal(err)
	}
	return n
}
