package mobilecore

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// fixtureNow is a moment the testdata/orgdir dates are written around.
var fixtureNow = time.Date(2026, 10, 7, 10, 0, 0, 0, time.Local).Unix()

// setup commits testdata/orgdir's org files to a repo, makes a bare
// "remote" of it, and clones that into a fresh group directory.
func setup(t *testing.T) (groupDir, remote string) {
	t.Helper()
	root := t.TempDir()
	src := filepath.Join(root, "src")
	repo, err := git.PlainInitWithOptions(src, &git.PlainInitOptions{InitOptions: git.InitOptions{DefaultBranch: plumbing.Main}})
	if err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join("..", "testdata", "orgdir")
	err = filepath.WalkDir(fixture, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(fixture, p)
		if d.IsDir() {
			if rel != "." && rel != "reference" {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(src, rel), 0o755)
		}
		if filepath.Ext(p) != ".org" {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(src, rel), data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	wt, _ := repo.Worktree()
	if err := wt.AddGlob("."); err != nil {
		t.Fatal(err)
	}
	sig := &object.Signature{Name: "t", Email: "t@t", When: time.Unix(fixtureNow, 0)}
	if _, err := wt.Commit("fixture", &git.CommitOptions{Author: sig, Committer: sig}); err != nil {
		t.Fatal(err)
	}
	remote = filepath.Join(root, "remote.git")
	if _, err := git.PlainClone(remote, true, &git.CloneOptions{URL: src}); err != nil {
		t.Fatal(err)
	}
	groupDir = filepath.Join(root, "group")
	if err := Clone(groupDir, remote, nil, ""); err != nil {
		t.Fatalf("Clone: %v", err)
	}
	return groupDir, remote
}

// decode returns a function that unmarshals a (json, error) result.
func decode[T any](t *testing.T) func(string, error) T {
	return func(s string, err error) T {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		var v T
		if err := json.Unmarshal([]byte(s), &v); err != nil {
			t.Fatalf("decoding %s: %v", s, err)
		}
		return v
	}
}

func TestAgendaMatchesDesktopBuckets(t *testing.T) {
	g, _ := setup(t)
	a := decode[agendaResult](t)(Agenda(g, fixtureNow))
	if a.Today != "2026-10-07" {
		t.Errorf("today = %q", a.Today)
	}
	var names []string
	for _, s := range a.Sections {
		names = append(names, s.Name)
	}
	if strings.Join(names, ",") != "Overdue,Due Today,Upcoming,Next Actions" {
		t.Errorf("sections = %v", names)
	}
	for _, s := range a.Sections {
		for _, it := range s.Items {
			if it.Entry.File == "calendar.org" || strings.HasPrefix(it.Entry.File, "reference/") {
				t.Errorf("%s lists %s from %s", s.Name, it.Entry.Title, it.Entry.File)
			}
			if s.Name != "Next Actions" && it.Date == "" {
				t.Errorf("%s item %q has no date", s.Name, it.Entry.Title)
			}
		}
	}
	due := a.Sections[1]
	found := false
	for _, it := range due.Items {
		found = found || it.Entry.Title == "Call the vet about Fido's checkup"
	}
	if !found {
		t.Errorf("Due Today = %+v, want the vet call (SCHEDULED 2026-10-07)", due.Items)
	}
}

func TestCaptureShowsAsPendingInInboxAndSurvivesRefresh(t *testing.T) {
	g, _ := setup(t)
	id, err := Capture(g, `{"title":"Call the plumber","tags":["home"],"deadline":"2026-11-18","createdAt":"2026-11-14T09:12:00-08:00"}`, "Phone", "p@x")
	if err != nil {
		t.Fatal(err)
	}
	in := decode[inboxResult](t)(Inbox(g))
	last := in.Entries[len(in.Entries)-1]
	if last.Title != "Call the plumber" || !last.Pending || last.CaptureID != id || last.Deadline != "2026-11-18 Wed" {
		t.Errorf("last inbox entry = %+v", last)
	}
	for _, e := range in.Entries[:len(in.Entries)-1] {
		if e.Pending {
			t.Errorf("%q marked pending", e.Title)
		}
	}
	if err := Refresh(g, nil, "Phone", "p@x"); err != nil {
		t.Fatal(err)
	}
	st := decode[statusResult](t)(Status(g))
	if st.Pending != 1 || st.Unpushed != 1 || !st.Cloned || st.BaseBranch != "main" {
		t.Errorf("status = %+v", st)
	}
	if err := Sync(g, nil, "Phone", "p@x"); err != nil {
		t.Fatal(err)
	}
	st = decode[statusResult](t)(Status(g))
	if st.Pending != 1 || st.Unpushed != 0 || st.LastPush == "" || st.LastError != "" {
		t.Errorf("status after sync = %+v", st)
	}
}

func TestEditAndDeletePendingCapture(t *testing.T) {
	g, _ := setup(t)
	id, err := Capture(g, `{"title":"Typo"}`, "P", "p@x")
	if err != nil {
		t.Fatal(err)
	}
	if err := UpdatePendingCapture(g, `{"id":"`+id+`","title":"Fixed"}`, "P", "p@x"); err != nil {
		t.Fatal(err)
	}
	c := decode[map[string]any](t)(PendingCapture(g, id))
	if c["title"] != "Fixed" || c["createdAt"] == "0001-01-01T00:00:00Z" {
		t.Errorf("updated capture = %v (createdAt should be kept)", c)
	}
	in := decode[inboxResult](t)(Inbox(g))
	if in.Entries[len(in.Entries)-1].Title != "Fixed" {
		t.Errorf("inbox didn't pick up the edit")
	}
	if err := DeletePendingCapture(g, id, "P", "p@x"); err != nil {
		t.Fatal(err)
	}
	in = decode[inboxResult](t)(Inbox(g))
	for _, e := range in.Entries {
		if e.Pending || e.Title == "Fixed" {
			t.Errorf("deleted capture still in inbox: %+v", e)
		}
	}
}

func TestCaptureRejectsMissingTitle(t *testing.T) {
	g, _ := setup(t)
	if _, err := Capture(g, `{"title":" "}`, "P", "p@x"); err == nil {
		t.Error("Capture with a blank title succeeded")
	}
}

func TestShareExtensionCapturesAreImportedOnRefresh(t *testing.T) {
	g, _ := setup(t)
	os.MkdirAll(incomingDir(g), 0o755)
	os.WriteFile(filepath.Join(incomingDir(g), "ABC.json"), []byte(`{"title":"Shared link","body":"https://example.com","createdAt":"2026-11-14T10:00:00Z"}`), 0o644)
	if err := Refresh(g, nil, "P", "p@x"); err != nil {
		t.Fatal(err)
	}
	in := decode[inboxResult](t)(Inbox(g))
	last := in.Entries[len(in.Entries)-1]
	if last.Title != "Shared link" || last.CaptureID != "ABC" || last.Body != "https://example.com" {
		t.Errorf("imported entry = %+v", last)
	}
}

func TestOutlineHidesStaleClosedUnlessAsked(t *testing.T) {
	g, _ := setup(t)
	count := func(o outlineResult) (n int) {
		var walk func([]entry)
		walk = func(es []entry) {
			for _, e := range es {
				n++
				walk(e.Children)
			}
		}
		for _, f := range o.Files {
			walk(f.Entries)
		}
		return n
	}
	hidden := decode[outlineResult](t)(Outline(g, false, fixtureNow))
	shown := decode[outlineResult](t)(Outline(g, true, fixtureNow))
	if count(hidden) >= count(shown) {
		t.Errorf("hiding closed: %d entries vs %d shown", count(hidden), count(shown))
	}
	for _, f := range shown.Files {
		if f.File == "calendar.org" || f.File == "meeting-tags.org" || strings.HasPrefix(f.File, "reference/") {
			t.Errorf("outline includes %s", f.File)
		}
	}
	if len(shown.Reference) != 1 || shown.Reference[0].File != "reference/runbooks.org" {
		t.Errorf("reference = %+v", shown.Reference)
	}
}

func TestSearchIsCaseInsensitiveOverTitleTagsAndBody(t *testing.T) {
	g, _ := setup(t)
	for q, want := range map[string]string{
		"STAGING DEPLOY": "Look into why the staging deploy took 40 minutes",
		"image cache":    "Look into why the staging deploy took 40 minutes",
		"@priya":         "Finalize the versioning scheme decision",
	} {
		res := decode[searchResult](t)(Search(g, q))
		found := false
		for _, e := range res.Entries {
			found = found || e.Title == want
		}
		if !found {
			t.Errorf("Search(%q) = %+v, want %q", q, res.Entries, want)
		}
	}
}

func TestEntryIDsAndAncestors(t *testing.T) {
	g, _ := setup(t)
	res := decode[searchResult](t)(Search(g, "Add version middleware"))
	if len(res.Entries) != 1 {
		t.Fatalf("results = %+v", res.Entries)
	}
	e := res.Entries[0]
	if !strings.HasPrefix(e.ID, "work.org:") || strings.Count(e.ID, ".") != 4 || len(e.Ancestors) != 3 {
		t.Errorf("entry = id %q ancestors %v", e.ID, e.Ancestors)
	}
}

func TestTagsAndStatusKeywords(t *testing.T) {
	g, _ := setup(t)
	tags := decode[[]string](t)(Tags(g))
	has := map[string]bool{}
	for _, tag := range tags {
		has[tag] = true
	}
	if !has["ideas"] || has["recurring"] {
		t.Errorf("tags = %v (want ideas, not calendar's recurring)", tags)
	}
	st := decode[statusResult](t)(Status(g))
	if len(st.Keywords) != 6 || st.Keywords[0].Name != "TODO" || st.Keywords[0].Color != "#d7005f" {
		t.Errorf("keywords = %+v", st.Keywords)
	}
}

func TestParseDateAndTimeZone(t *testing.T) {
	if err := SetTimeZone("America/Los_Angeles"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { SetTimeZone("Local") })
	// 2026-11-15 06:00 UTC is still the 14th in Los Angeles.
	got, err := ParseDate("1d", time.Date(2026, 11, 15, 6, 0, 0, 0, time.UTC).Unix())
	if err != nil || got != "2026-11-15" {
		t.Errorf("ParseDate = %q, %v", got, err)
	}
}

func TestGenerateSSHKey(t *testing.T) {
	kp, err := GenerateSSHKey()
	if err != nil {
		t.Fatal(err)
	}
	pub, err := PublicKeyFor(kp.PrivateKeyPEM)
	if err != nil || !strings.HasPrefix(kp.PublicKey, pub) {
		t.Errorf("PublicKeyFor = %q, %v vs %q", pub, err, kp.PublicKey)
	}
}

func TestStatusBeforeClone(t *testing.T) {
	st := decode[statusResult](t)(Status(t.TempDir()))
	if st.Cloned || len(st.Keywords) != 6 {
		t.Errorf("status = %+v", st)
	}
	if _, err := Inbox(t.TempDir()); err == nil {
		t.Error("Inbox with no clone succeeded")
	}
}

// TestResultsHaveNoNullArrays guards the Swift side, whose Codable
// structs declare these fields as non-optional arrays.
func TestResultsHaveNoNullArrays(t *testing.T) {
	g, _ := setup(t)
	Capture(g, `{"title":"x"}`, "P", "p@x")
	check := func(name string, s string, err error) {
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(s, ":null") {
			t.Errorf("%s contains a null: %.300s", name, s[strings.Index(s, ":null")-100:])
		}
	}
	s, err := Agenda(g, fixtureNow)
	check("Agenda", s, err)
	s, err = Inbox(g)
	check("Inbox", s, err)
	s, err = Outline(g, true, fixtureNow)
	check("Outline", s, err)
	s, err = Search(g, "a")
	check("Search", s, err)
	s, err = Status(g)
	check("Status", s, err)
}

// TestCaptureWorksOfflineWithAnSSHRemote: capturing, editing and deleting
// only rebuild locally, so they must not need (or try to load) SSH auth.
func TestCaptureWorksOfflineWithAnSSHRemote(t *testing.T) {
	g, _ := setup(t)
	repo, err := git.PlainOpen(repoDir(g))
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := repo.Config()
	cfg.Remotes["origin"].URLs = []string{"ssh://sam@nas.example:2222/srv/org.git"}
	if err := repo.SetConfig(cfg); err != nil {
		t.Fatal(err)
	}
	id, err := Capture(g, `{"title":"Offline"}`, "P", "p@x")
	if err != nil {
		t.Fatalf("Capture with an SSH remote: %v", err)
	}
	if err := UpdatePendingCapture(g, `{"id":"`+id+`","title":"Edited"}`, "P", "p@x"); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if err := DeletePendingCapture(g, id, "P", "p@x"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func TestCloneOverSSHNeedsAConfirmedHostKey(t *testing.T) {
	err := Clone(t.TempDir(), "sam@nas.example:org.git", []byte("unused"), "")
	if err == nil || !strings.Contains(err.Error(), "host key") {
		t.Errorf("Clone without a host key = %v", err)
	}
}
