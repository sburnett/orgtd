package gitsync

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/sburnett/orgtd/internal/capture"
	"github.com/sburnett/orgtd/internal/capturequeue"
)

const initialInbox = "* Existing item\n"

var macSig = &object.Signature{Name: "Mac", Email: "mac@example.com", When: time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)}

// fixture is a bare "GitHub" remote, a desktop clone of it ("mac"), and
// the phone's Repo.
type fixture struct {
	t      *testing.T
	remote string
	macDir string
	mac    *git.Repository
	phone  *Repo
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	f := &fixture{t: t, remote: filepath.Join(root, "remote.git"), macDir: filepath.Join(root, "mac")}
	if _, err := git.PlainInitWithOptions(f.remote, &git.PlainInitOptions{Bare: true, InitOptions: git.InitOptions{DefaultBranch: plumbing.Main}}); err != nil {
		t.Fatal(err)
	}
	mac, err := git.PlainInitWithOptions(f.macDir, &git.PlainInitOptions{InitOptions: git.InitOptions{DefaultBranch: plumbing.Main}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mac.CreateRemote(&gitconfig.RemoteConfig{Name: "origin", URLs: []string{f.remote}}); err != nil {
		t.Fatal(err)
	}
	f.mac = mac
	f.macCommit("inbox.org", initialInbox, "initial")
	f.macCommit("work.org", "* TODO Work thing\n", "work")
	f.macPush()

	f.phone = &Repo{
		Dir:       filepath.Join(root, "phone", "repo"),
		QueuePath: filepath.Join(root, "phone", "captures.json"),
		StatePath: filepath.Join(root, "phone", "sync-state.json"),
		Author:    Author{Name: "Phone", Email: "phone@example.com"},
	}
	os.MkdirAll(filepath.Join(root, "phone"), 0o755)
	if err := f.phone.Clone(f.remote, HostKey{}); err != nil {
		t.Fatalf("Clone: %v", err)
	}
	return f
}

func (f *fixture) macCommit(name, content, msg string) plumbing.Hash {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.macDir, name), []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
	wt, _ := f.mac.Worktree()
	wt.Add(name)
	h, err := wt.Commit(msg, &git.CommitOptions{Author: macSig, Committer: macSig})
	if err != nil {
		f.t.Fatal(err)
	}
	return h
}

func (f *fixture) macPush() {
	f.t.Helper()
	err := f.mac.Push(&git.PushOptions{RemoteName: "origin", RefSpecs: []gitconfig.RefSpec{"+refs/heads/main:refs/heads/main"}})
	if err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
		f.t.Fatal(err)
	}
}

func (f *fixture) macFetch() {
	f.t.Helper()
	err := f.mac.Fetch(&git.FetchOptions{RemoteName: "origin", RefSpecs: []gitconfig.RefSpec{"+refs/heads/*:refs/remotes/origin/*"}, Prune: true})
	if err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
		f.t.Fatal(err)
	}
}

// macMerge merges commit tip into the mac's main the way a real merge
// would come out when the phone's side only appended to the inbox: the
// merged inbox is main's plus the lines tip appended, and the commit
// has both parents.
func (f *fixture) macMerge(tip plumbing.Hash) {
	f.t.Helper()
	f.macFetch()
	head, _ := f.mac.Head()
	tipInbox := f.fileAt(f.mac, tip, "inbox.org")
	mainInbox := f.fileAt(f.mac, head.Hash(), "inbox.org")
	base := f.fileAt(f.mac, f.mergeBase(head.Hash(), tip), "inbox.org")
	merged := mainInbox + strings.TrimPrefix(tipInbox, base)
	os.WriteFile(filepath.Join(f.macDir, "inbox.org"), []byte(merged), 0o644)
	wt, _ := f.mac.Worktree()
	wt.Add("inbox.org")
	if _, err := wt.Commit("Merge mobile", &git.CommitOptions{Author: macSig, Committer: macSig, Parents: []plumbing.Hash{head.Hash(), tip}}); err != nil {
		f.t.Fatal(err)
	}
	f.macPush()
}

// macMergeWith merges commit tip into the mac's main with file's
// content taken as given (the user's resolution), as desktop orgtd's
// merge does: a commit with both parents.
func (f *fixture) macMergeWith(tip plumbing.Hash, file, content string) {
	f.t.Helper()
	f.macFetch()
	head, _ := f.mac.Head()
	wt, _ := f.mac.Worktree()
	if err := wt.Reset(&git.ResetOptions{Commit: head.Hash(), Mode: git.HardReset}); err != nil {
		f.t.Fatal(err)
	}
	os.WriteFile(filepath.Join(f.macDir, file), []byte(content), 0o644)
	wt.Add(file)
	if _, err := wt.Commit("orgtd: merge mobile", &git.CommitOptions{Author: macSig, Committer: macSig, Parents: []plumbing.Hash{head.Hash(), tip}}); err != nil {
		f.t.Fatal(err)
	}
	f.macPush()
}

func (f *fixture) mergeBase(a, b plumbing.Hash) plumbing.Hash {
	ca, _ := f.mac.CommitObject(a)
	cb, _ := f.mac.CommitObject(b)
	bases, err := ca.MergeBase(cb)
	if err != nil || len(bases) == 0 {
		f.t.Fatalf("no merge base: %v", err)
	}
	return bases[0].Hash
}

func (f *fixture) fileAt(repo *git.Repository, commit plumbing.Hash, name string) string {
	f.t.Helper()
	c, err := repo.CommitObject(commit)
	if err != nil {
		f.t.Fatal(err)
	}
	file, err := c.File(name)
	if err != nil {
		f.t.Fatal(err)
	}
	s, _ := file.Contents()
	return s
}

func (f *fixture) remoteRef(branch string) (plumbing.Hash, bool) {
	r, err := git.PlainOpen(f.remote)
	if err != nil {
		f.t.Fatal(err)
	}
	ref, err := r.Reference(plumbing.NewBranchReferenceName(branch), true)
	if err != nil {
		return plumbing.ZeroHash, false
	}
	return ref.Hash(), true
}

func (f *fixture) capture(title string, minute int) string {
	f.t.Helper()
	id, err := capturequeue.Add(f.phone.QueuePath, capture.Capture{
		Title:     title,
		CreatedAt: time.Date(2026, 11, 14, 9, minute, 0, 0, time.UTC),
	})
	if err != nil {
		f.t.Fatal(err)
	}
	if err := f.phone.Rebuild(); err != nil {
		f.t.Fatal(err)
	}
	return id
}

func (f *fixture) sync() {
	f.t.Helper()
	if err := f.phone.Sync(); err != nil {
		f.t.Fatalf("Sync: %v", err)
	}
}

func (f *fixture) queueTitles() []string {
	cs, err := capturequeue.Load(f.phone.QueuePath)
	if err != nil {
		f.t.Fatal(err)
	}
	var out []string
	for _, c := range cs {
		out = append(out, c.Title)
	}
	return out
}

// assertAppendOnly checks origin/mobile differs from origin/main only by
// lines appended to the end of inbox.org.
func (f *fixture) assertAppendOnly(wantAppended ...string) {
	f.t.Helper()
	f.macFetch()
	mobile, ok := f.remoteRef(MobileBranch)
	if !ok {
		f.t.Fatal("no origin/mobile")
	}
	main, _ := f.remoteRef("main")
	mainCommit, _ := f.mac.CommitObject(main)
	mobileCommit, _ := f.mac.CommitObject(mobile)
	if ok, _ := mainCommit.IsAncestor(mobileCommit); !ok {
		f.t.Errorf("origin/mobile isn't based on origin/main")
	}
	patch, err := mainCommit.Patch(mobileCommit)
	if err != nil {
		f.t.Fatal(err)
	}
	for _, fp := range patch.FilePatches() {
		from, to := fp.Files()
		if from == nil || to == nil || from.Path() != "inbox.org" {
			f.t.Errorf("origin/mobile changes a file other than inbox.org: %v -> %v", from, to)
		}
	}
	mainInbox := f.fileAt(f.mac, main, "inbox.org")
	mobileInbox := f.fileAt(f.mac, mobile, "inbox.org")
	if !strings.HasPrefix(mobileInbox, mainInbox) {
		f.t.Fatalf("mobile inbox doesn't start with main's:\n%s\n---\n%s", mainInbox, mobileInbox)
	}
	appended := strings.TrimPrefix(mobileInbox, mainInbox)
	for _, title := range wantAppended {
		if !strings.Contains(appended, "* "+title+"\n") {
			f.t.Errorf("appended text %q is missing %q", appended, title)
		}
	}
	if got := strings.Count(appended, "\n* "); got+boolInt(strings.HasPrefix(appended, "* ")) != len(wantAppended) {
		f.t.Errorf("appended %q, want exactly %d entries", appended, len(wantAppended))
	}
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func TestCloneChecksOutMobileAtMain(t *testing.T) {
	f := newFixture(t)
	repo, _ := git.PlainOpen(f.phone.Dir)
	head, _ := repo.Head()
	main, _ := f.remoteRef("main")
	if head.Name().Short() != MobileBranch || head.Hash() != main {
		t.Errorf("HEAD = %s at %s, want mobile at main %s", head.Name(), head.Hash(), main)
	}
	s, _ := f.phone.LoadState()
	if s.BaseBranch != "main" {
		t.Errorf("BaseBranch = %q", s.BaseBranch)
	}
}

func TestCaptureCommitsLocallyWithoutPushing(t *testing.T) {
	f := newFixture(t)
	f.capture("Call the plumber", 1)
	data, _ := os.ReadFile(filepath.Join(f.phone.Dir, "inbox.org"))
	if !strings.HasPrefix(string(data), initialInbox) || !strings.Contains(string(data), "* Call the plumber\n") {
		t.Errorf("working tree inbox = %q", data)
	}
	if _, ok := f.remoteRef(MobileBranch); ok {
		t.Error("capture pushed origin/mobile; only Sync should")
	}
}

func TestRebuildIsDeterministic(t *testing.T) {
	f := newFixture(t)
	f.capture("One", 1)
	repo, _ := git.PlainOpen(f.phone.Dir)
	first, _ := repo.Head()
	if err := f.phone.Rebuild(); err != nil {
		t.Fatal(err)
	}
	second, _ := repo.Head()
	if first.Hash() != second.Hash() {
		t.Errorf("rebuild changed the tip: %s -> %s", first.Hash(), second.Hash())
	}
}

func TestSyncPushesAppendOnlyMobile(t *testing.T) {
	f := newFixture(t)
	f.capture("Call the plumber", 1)
	f.capture("Buy milk", 2)
	f.sync()
	f.assertAppendOnly("Call the plumber", "Buy milk")
	if got := f.queueTitles(); len(got) != 2 {
		t.Errorf("queue = %v; captures stay pending until merged", got)
	}
}

func TestSyncRebuildsOnAdvancedMain(t *testing.T) {
	f := newFixture(t)
	f.capture("Phone item", 1)
	f.sync()
	f.macCommit("work.org", "* TODO Work thing\n* TODO Another\n", "more work")
	f.macCommit("inbox.org", initialInbox+"* Desktop capture\n", "desktop capture")
	f.macPush()
	f.sync()
	f.assertAppendOnly("Phone item")
}

func TestMergingAnOlderTipOnlyDropsItsCaptures(t *testing.T) {
	f := newFixture(t)
	f.capture("First", 1)
	f.sync()
	older, _ := f.remoteRef(MobileBranch)
	f.capture("Second", 2)
	f.sync()

	f.macMerge(older)
	f.sync()
	if got := f.queueTitles(); len(got) != 1 || got[0] != "Second" {
		t.Errorf("queue after merging the older tip = %v, want [Second]", got)
	}
	f.assertAppendOnly("Second")
}

func TestMergingTheLatestTipEmptiesQueueAndDeletesMobile(t *testing.T) {
	f := newFixture(t)
	f.capture("First", 1)
	f.capture("Second", 2)
	f.sync()
	tip, _ := f.remoteRef(MobileBranch)
	f.macMerge(tip)
	f.sync()
	if got := f.queueTitles(); len(got) != 0 {
		t.Errorf("queue = %v, want empty", got)
	}
	if _, ok := f.remoteRef(MobileBranch); ok {
		t.Error("origin/mobile still exists with nothing pending")
	}
	s, _ := f.phone.LoadState()
	if len(s.PushedTips) != 0 || s.LastPushed != "" {
		t.Errorf("state = %+v, want no tips", s)
	}
}

func TestFastForwardMergeAlsoCounts(t *testing.T) {
	f := newFixture(t)
	f.capture("Only", 1)
	f.sync()
	tip, _ := f.remoteRef(MobileBranch)
	f.macFetch()
	f.mac.Storer.SetReference(plumbing.NewHashReference(plumbing.NewBranchReferenceName("main"), tip))
	f.macPush()
	f.sync()
	if got := f.queueTitles(); len(got) != 0 {
		t.Errorf("queue = %v, want empty", got)
	}
}

func TestSomeoneElseMovingMobileFailsTheLease(t *testing.T) {
	f := newFixture(t)
	f.capture("Mine", 1)
	f.sync()
	f.macFetch()
	mobile, _ := f.remoteRef(MobileBranch)
	wt, _ := f.mac.Worktree()
	if err := wt.Checkout(&git.CheckoutOptions{Hash: mobile, Branch: plumbing.NewBranchReferenceName("other"), Create: true}); err != nil {
		t.Fatal(err)
	}
	theirs := f.macCommit("work.org", "* changed\n", "someone else")
	if err := f.mac.Push(&git.PushOptions{RemoteName: "origin", RefSpecs: []gitconfig.RefSpec{"+refs/heads/other:refs/heads/mobile"}}); err != nil {
		t.Fatal(err)
	}

	f.capture("Another", 2)
	err := f.phone.Sync()
	if !errors.Is(err, ErrLease) {
		t.Fatalf("Sync = %v, want ErrLease", err)
	}
	if got, _ := f.remoteRef(MobileBranch); got != theirs {
		t.Errorf("origin/mobile = %s, want it left at %s", got, theirs)
	}
	s, _ := f.phone.LoadState()
	if s.LastError == "" {
		t.Error("lease failure not recorded in state")
	}
}

func TestRefreshWorksOfflineAndReportsTheFetchError(t *testing.T) {
	f := newFixture(t)
	f.capture("Offline", 1)
	os.Rename(f.remote, f.remote+".gone")
	err := f.phone.Refresh()
	if err == nil {
		t.Fatal("Refresh with no remote succeeded")
	}
	data, _ := os.ReadFile(filepath.Join(f.phone.Dir, "inbox.org"))
	if !strings.Contains(string(data), "* Offline\n") {
		t.Errorf("offline refresh lost the capture: %q", data)
	}
}

func TestInboxFileComesFromRepoConfig(t *testing.T) {
	f := newFixture(t)
	f.macCommit(".orgtd.toml", "inbox_file = \"in.org\"\n", "config")
	f.macCommit("in.org", "", "new inbox")
	f.macPush()
	f.capture("Elsewhere", 1)
	f.sync()
	f.macFetch()
	mobile, _ := f.remoteRef(MobileBranch)
	if got := f.fileAt(f.mac, mobile, "in.org"); !strings.Contains(got, "* Elsewhere") {
		t.Errorf("in.org on mobile = %q", got)
	}
}

func TestGenerateKeyRoundTrips(t *testing.T) {
	pub, priv, err := GenerateKey("orgtd-iphone")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(pub, "ssh-ed25519 ") || !strings.HasSuffix(pub, " orgtd-iphone") {
		t.Errorf("public key = %q", pub)
	}
	derived, err := PublicKey(priv)
	if err != nil || !strings.HasPrefix(pub, derived) {
		t.Errorf("PublicKey = %q, %v; want prefix of %q", derived, err, pub)
	}
}

// TestLeaseCatchesAMoveBetweenFetchAndPush covers the race the pre-push
// check can't see: origin/mobile moves after the phone's fetch.
func TestLeaseCatchesAMoveBetweenFetchAndPush(t *testing.T) {
	f := newFixture(t)
	f.capture("Mine", 1)
	f.sync()
	f.macFetch()
	mobile, _ := f.remoteRef(MobileBranch)
	wt, _ := f.mac.Worktree()
	wt.Checkout(&git.CheckoutOptions{Hash: mobile, Branch: plumbing.NewBranchReferenceName("other"), Create: true})
	theirs := f.macCommit("work.org", "* changed\n", "someone else")
	if err := f.mac.Push(&git.PushOptions{RemoteName: "origin", RefSpecs: []gitconfig.RefSpec{"+refs/heads/other:refs/heads/mobile"}}); err != nil {
		t.Fatal(err)
	}
	f.capture("Another", 2) // rebuilds locally; no fetch
	if err := f.phone.Publish(); err == nil {
		t.Fatal("Publish over a moved origin/mobile succeeded")
	}
	if got, _ := f.remoteRef(MobileBranch); got != theirs {
		t.Errorf("origin/mobile = %s, want it left at %s", got, theirs)
	}
}

// setRemoteURL points the phone's clone at url.
func setRemoteURL(t *testing.T, f *fixture, url string) {
	t.Helper()
	repo, err := git.PlainOpen(f.phone.Dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := repo.Config()
	cfg.Remotes["origin"].URLs = []string{url}
	if err := repo.SetConfig(cfg); err != nil {
		t.Fatal(err)
	}
}

func capturequeueAdd(f *fixture, title string) (string, error) {
	return capturequeue.Add(f.phone.QueuePath, capture.Capture{Title: title, CreatedAt: time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)})
}

func plumbingRef(branch string, h plumbing.Hash) *plumbing.Reference {
	return plumbing.NewHashReference(plumbing.NewBranchReferenceName(branch), h)
}
