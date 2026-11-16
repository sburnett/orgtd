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

	"github.com/sburnett/orgtd/internal/org"
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

func TestCaptureShowsAsPendingAndSurvivesRefresh(t *testing.T) {
	g, _ := setup(t)
	id, err := Capture(g, `{"title":"Call the plumber","tags":["home"],"deadline":"2026-11-18","createdAt":"2026-11-14T09:12:00-08:00"}`, "Phone", "p@x")
	if err != nil {
		t.Fatal(err)
	}
	in := inboxEntries(t, g)
	last := in[len(in)-1]
	if last.Title != "Call the plumber" || !last.Pending || last.CaptureID != id || last.Deadline != "2026-11-18 Wed" {
		t.Errorf("last inbox entry = %+v", last)
	}
	for _, e := range in[:len(in)-1] {
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

// inboxEntries is inbox.org's top-level entries, as the outline has them.
func inboxEntries(t *testing.T, g string) []entry {
	t.Helper()
	for _, f := range decode[outlineResult](t)(Outline(g, true, fixtureNow)).Files {
		if f.File == "inbox.org" {
			return f.Entries
		}
	}
	t.Fatal("no inbox.org in the outline")
	return nil
}

func TestPendingCapturesAreMarkedAmongAddedEntries(t *testing.T) {
	g, _ := setup(t)
	for _, add := range []struct {
		title string
		last  bool
	}{{"Added first", false}, {"Added last", true}} {
		if _, err := AddEntry(g, "inbox.org", "", add.last, captureJSON(t, "", add.title), fixtureNow, "P", "p@x"); err != nil {
			t.Fatal(err)
		}
	}
	id, err := Capture(g, `{"title":"Captured"}`, "P", "p@x")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range inboxEntries(t, g) {
		if (e.Title == "Captured") != (e.CaptureID == id) || e.Pending != (e.Title == "Captured") {
			t.Errorf("%q: pending %v, captureId %q", e.Title, e.Pending, e.CaptureID)
		}
	}
	if e := findEntry(t, g, "Captured"); e.CaptureID != id {
		t.Errorf("search result for the capture = %+v", e)
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
	in := inboxEntries(t, g)
	if in[len(in)-1].Title != "Fixed" {
		t.Errorf("inbox didn't pick up the edit")
	}
	if err := DeletePendingCapture(g, id, "P", "p@x"); err != nil {
		t.Fatal(err)
	}
	in = inboxEntries(t, g)
	for _, e := range in {
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
	in := inboxEntries(t, g)
	last := in[len(in)-1]
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
	if _, err := Outline(t.TempDir(), true, fixtureNow); err == nil {
		t.Error("Outline with no clone succeeded")
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

func TestCaptureKeywordMustBeAConfiguredActiveKeyword(t *testing.T) {
	g, _ := setup(t)
	if _, err := Capture(g, `{"title":"Done already","keyword":"DONE"}`, "P", "p@x"); err == nil {
		t.Error("capturing with a done keyword succeeded")
	}
	if _, err := Capture(g, `{"title":"Typo","keyword":"TOOD"}`, "P", "p@x"); err == nil {
		t.Error("capturing with an unknown keyword succeeded")
	}
	id, err := Capture(g, `{"title":"Next thing","keyword":"NEXT"}`, "P", "p@x")
	if err != nil {
		t.Fatal(err)
	}
	in := inboxEntries(t, g)
	last := in[len(in)-1]
	if last.Keyword != "NEXT" || last.Title != "Next thing" || last.CaptureID != id {
		t.Errorf("captured entry = %+v", last)
	}
	if err := UpdatePendingCapture(g, `{"id":"`+id+`","title":"Next thing","keyword":"SHIPPED"}`, "P", "p@x"); err == nil {
		t.Error("updating to an unknown keyword succeeded")
	}
}

func findEntry(t *testing.T, g, title string) entry {
	t.Helper()
	res := decode[searchResult](t)(Search(g, title))
	for _, e := range res.Entries {
		if e.Title == title {
			return e
		}
	}
	t.Fatalf("no entry %q", title)
	return entry{}
}

func TestSetStateMarksDoneAndPending(t *testing.T) {
	g, _ := setup(t)
	e := findEntry(t, g, "Call the vet about Fido's checkup")
	if err := SetState(g, e.ID, "DONE", "", fixtureNow, "P", "p@x"); err != nil {
		t.Fatal(err)
	}
	after := findEntry(t, g, "Call the vet about Fido's checkup")
	if after.Keyword != "DONE" || after.Closed == "" || !after.PendingState || after.ID != e.ID {
		t.Errorf("after SetState: %+v", after)
	}
	a := decode[agendaResult](t)(Agenda(g, fixtureNow))
	for _, s := range a.Sections {
		for _, it := range s.Items {
			if it.Entry.Title == e.Title {
				t.Errorf("done entry still in %s", s.Name)
			}
		}
	}
	st := decode[statusResult](t)(Status(g))
	if st.PendingStates != 1 || st.Unpushed != 1 || len(st.Dropped) != 0 {
		t.Errorf("status = %+v", st)
	}
	if err := Sync(g, nil, "P", "p@x"); err != nil {
		t.Fatal(err)
	}
	if st := decode[statusResult](t)(Status(g)); st.Unpushed != 0 || st.PendingStates != 1 {
		t.Errorf("status after sync = %+v", st)
	}
}

func TestSetStateRefusals(t *testing.T) {
	g, _ := setup(t)
	e := findEntry(t, g, "Call the vet about Fido's checkup")
	if err := SetState(g, e.ID, "SHIPPED", "", fixtureNow, "P", "p@x"); err == nil {
		t.Error("unknown keyword accepted")
	}
	if err := SetState(g, "work.org:99", "DONE", "", fixtureNow, "P", "p@x"); err == nil {
		t.Error("bad id accepted")
	}
	ref := decode[outlineResult](t)(Outline(g, true, fixtureNow)).Reference[0].Entries[0]
	if err := SetState(g, ref.ID, "DONE", "", fixtureNow, "P", "p@x"); err == nil {
		t.Error("state change in reference/ accepted")
	}
	os.WriteFile(filepath.Join(repoDir(g), ".gitattributes"), []byte("personal.org merge=union\n"), 0o644)
	err := SetState(g, e.ID, "DONE", "", fixtureNow, "P", "p@x")
	if err == nil || !strings.Contains(err.Error(), "merge=union") {
		t.Errorf("SetState in a union-merged file = %v", err)
	}
}

func formFor(e entry) map[string]any {
	return map[string]any{
		"keyword": e.Keyword, "title": e.Title, "body": e.Body, "tags": e.Tags,
		"scheduled": e.ScheduledInput, "deadline": e.DeadlineInput,
	}
}

func editJSON(t *testing.T, form map[string]any) string {
	t.Helper()
	data, err := json.Marshal(form)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestEditEntryUnchangedFormIsANoOp(t *testing.T) {
	g, _ := setup(t)
	e := findEntry(t, g, "Pay credit card")
	if err := EditEntry(g, e.ID, editJSON(t, formFor(e)), fixtureNow, "P", "p@x"); err != nil {
		t.Fatal(err)
	}
	if st := decode[statusResult](t)(Status(g)); st.PendingStates != 0 {
		t.Errorf("an unchanged form queued %d changes", st.PendingStates)
	}
}

func TestEditEntryChangesOnlyWhatChanged(t *testing.T) {
	g, _ := setup(t)
	e := findEntry(t, g, "Pay credit card")
	form := formFor(e)
	form["title"] = "Pay the credit card bill"
	form["tags"] = []string{"money"}
	form["body"] = "Autopay is off this month"
	if err := EditEntry(g, e.ID, editJSON(t, form), fixtureNow, "P", "p@x"); err != nil {
		t.Fatal(err)
	}
	after := findEntry(t, g, "Pay the credit card bill")
	if after.Scheduled != e.Scheduled || !strings.Contains(after.Scheduled, "+1m") {
		t.Errorf("scheduled = %q, want it untouched (%q)", after.Scheduled, e.Scheduled)
	}
	if strings.Join(after.Tags, ",") != "money" || after.Body != "Autopay is off this month" || !after.PendingState {
		t.Errorf("after edit: %+v", after)
	}
	data, _ := os.ReadFile(filepath.Join(repoDir(g), "personal.org"))
	if !strings.Contains(string(data), "** TODO Pay the credit card bill   :money:\n   SCHEDULED: <2026-10-20 Tue +1m>\n   Autopay is off this month\n") {
		t.Errorf("personal.org = %s", data)
	}
}

func TestEditEntryRefusals(t *testing.T) {
	g, _ := setup(t)
	e := findEntry(t, g, "Pay credit card")
	for name, change := range map[string]map[string]any{
		"blank title":     {"title": "  "},
		"unknown keyword": {"keyword": "SHIPPED"},
		"bad date":        {"deadline": "someday"},
	} {
		form := formFor(e)
		for k, v := range change {
			form[k] = v
		}
		if err := EditEntry(g, e.ID, editJSON(t, form), fixtureNow, "P", "p@x"); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	form := formFor(e)
	form["keyword"] = "DONE" // done keywords are fine when editing (not when capturing)
	if err := EditEntry(g, e.ID, editJSON(t, form), fixtureNow, "P", "p@x"); err != nil {
		t.Errorf("DONE via edit: %v", err)
	}
}

func TestDateInputSkipsWeekdayAndCookies(t *testing.T) {
	for raw, want := range map[string]string{
		"2026-10-20 Tue":            "2026-10-20",
		"2026-10-20 Tue +1m":        "2026-10-20",
		"2026-10-20 Tue 14:00 .+1w": "2026-10-20 14:00",
		"2026-10-20 14:00 -3d":      "2026-10-20 14:00",
		"2026-10-20":                "2026-10-20",
	} {
		if got := dateInput(&org.Timestamp{Raw: raw}); got != want {
			t.Errorf("dateInput(%q) = %q, want %q", raw, got, want)
		}
	}
}

func captureJSON(t *testing.T, keyword, title string) string {
	t.Helper()
	return editJSON(t, map[string]any{"keyword": keyword, "title": title, "tags": []string{}})
}

func TestAddEntryFirstAndLastAtEveryLevel(t *testing.T) {
	g, _ := setup(t)
	fan := findEntry(t, g, "Replace the bathroom fan") // personal.org:0.0, three children
	for _, tc := range []struct {
		file, parent string
		last         bool
		title, id    string
	}{
		{"personal.org", fan.ID, false, "Find the old receipt", fan.ID + ".0"},
		{"personal.org", fan.ID, true, "Recaulk around it", fan.ID + ".4"}, // after the first add
		{"personal.org", "", false, "Top of personal", "personal.org:0"},
		{"work.org", "", true, "Bottom of work", ""},
	} {
		id, err := AddEntry(g, tc.file, tc.parent, tc.last, captureJSON(t, "TODO", tc.title), fixtureNow, "P", "p@x")
		if err != nil {
			t.Fatalf("%s: %v", tc.title, err)
		}
		e := findEntry(t, g, tc.title)
		if tc.id != "" && id != tc.id {
			t.Errorf("%s: returned id %q, want %q", tc.title, id, tc.id)
		}
		if e.ID != id || !e.PendingNew || !e.PendingState || e.Keyword != "TODO" {
			t.Errorf("%s: returned %q, entry %+v", tc.title, id, e)
		}
	}
	// The top-level add moved the fan to personal.org:1.0.
	o := decode[outlineResult](t)(Outline(g, true, fixtureNow))
	var kids []string
	for _, f := range o.Files {
		if f.File != "personal.org" {
			continue
		}
		for _, c := range f.Entries[1].Children[0].Children {
			kids = append(kids, c.Title)
		}
		if f.Entries[0].Title != "Top of personal" {
			t.Errorf("personal.org's first entry = %q", f.Entries[0].Title)
		}
	}
	want := "Find the old receipt|Measure the duct opening|Order a 110 CFM fan|Install it (needs a second pair of hands)|Recaulk around it"
	if strings.Join(kids, "|") != want {
		t.Errorf("fan's children = %v", kids)
	}
	if st := decode[statusResult](t)(Status(g)); st.PendingStates != 4 {
		t.Errorf("pendingStates = %d", st.PendingStates)
	}
}

func TestAddedEntryCanBeEditedBeforeMerging(t *testing.T) {
	g, _ := setup(t)
	id, err := AddEntry(g, "work.org", "", true, captureJSON(t, "", "Draft"), fixtureNow, "P", "p@x")
	if err != nil {
		t.Fatal(err)
	}
	e := findEntry(t, g, "Draft")
	if e.ID != id {
		t.Fatalf("id %q vs %q", e.ID, id)
	}
	form := formFor(e)
	form["title"] = "Final"
	form["keyword"] = "NEXT"
	if err := EditEntry(g, e.ID, editJSON(t, form), fixtureNow, "P", "p@x"); err != nil {
		t.Fatal(err)
	}
	if err := RefreshLocal(g, "P", "p@x"); err != nil {
		t.Fatal(err)
	}
	if after := findEntry(t, g, "Final"); after.Keyword != "NEXT" || !after.PendingState {
		t.Errorf("after edit: %+v", after)
	}
}

func TestAddEntryRefusals(t *testing.T) {
	g, _ := setup(t)
	ref := decode[outlineResult](t)(Outline(g, true, fixtureNow)).Reference[0]
	if _, err := AddEntry(g, ref.File, "", true, captureJSON(t, "TODO", "x"), fixtureNow, "P", "p@x"); err == nil {
		t.Error("keyword in reference/ accepted")
	}
	if _, err := AddEntry(g, ref.File, "", true, captureJSON(t, "", "A note"), fixtureNow, "P", "p@x"); err != nil {
		t.Errorf("plain entry in reference/: %v", err)
	}
	for name, args := range map[string][2]string{
		"blank title":   {"work.org", ""},
		"no such file":  {"nope.org", "x"},
		"calendar file": {"calendar.org", "x"},
	} {
		if _, err := AddEntry(g, args[0], "", false, captureJSON(t, "", args[1]), fixtureNow, "P", "p@x"); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, err := AddEntry(g, "work.org", "personal.org:0", false, captureJSON(t, "", "x"), fixtureNow, "P", "p@x"); err == nil {
		t.Error("parent in another file accepted")
	}
	if _, err := AddEntry(g, "work.org", "", false, captureJSON(t, "DONE", "x"), fixtureNow, "P", "p@x"); err == nil {
		t.Error("done keyword accepted")
	}
}

func TestShiftEntryReturnsTheNewIDAndMarksItPending(t *testing.T) {
	g, _ := setup(t)
	for _, tc := range []struct {
		title  string
		delta  int
		wantID string
		level  int
	}{
		// personal.org:0.0 is "Replace the bathroom fan", with three children.
		{"Schedule furnace maintenance before it gets cold", 1, "personal.org:0.0.3", 3},
		{"Order a 110 CFM fan", -1, "personal.org:0.1", 2},
	} {
		e := findEntry(t, g, tc.title)
		id, err := ShiftEntry(g, e.ID, tc.delta, fixtureNow, "P", "p@x")
		if err != nil {
			t.Fatalf("%s: %v", tc.title, err)
		}
		after := findEntry(t, g, tc.title)
		if id != tc.wantID || after.ID != id || after.Level != tc.level || !after.PendingState {
			t.Errorf("%s: returned %q, entry %+v", tc.title, id, after)
		}
	}
	if st := decode[statusResult](t)(Status(g)); st.PendingStates != 2 {
		t.Errorf("pendingStates = %d", st.PendingStates)
	}
}

func TestShiftEntryRefusals(t *testing.T) {
	g, _ := setup(t)
	if _, err := ShiftEntry(g, "personal.org:0", 1, fixtureNow, "P", "p@x"); err == nil {
		t.Error("demoting a first entry accepted")
	}
	if _, err := ShiftEntry(g, "personal.org:0", -1, fixtureNow, "P", "p@x"); err == nil {
		t.Error("promoting a top-level entry accepted")
	}
	if _, err := ShiftEntry(g, "personal.org:0.1", 2, fixtureNow, "P", "p@x"); err == nil {
		t.Error("moving two levels accepted")
	}
	if st := decode[statusResult](t)(Status(g)); st.PendingStates != 0 {
		t.Errorf("refusals queued %d changes", st.PendingStates)
	}
}

func TestAddEntryBesideReturnsTheNewID(t *testing.T) {
	g, _ := setup(t)
	furnace := findEntry(t, g, "Schedule furnace maintenance before it gets cold") // personal.org:0.1
	for _, tc := range []struct {
		after         bool
		title, wantID string
	}{
		{false, "Above the furnace", "personal.org:0.1"},
		{true, "Below the furnace", "personal.org:0.3"}, // the furnace is now 0.2
	} {
		anchor := findEntry(t, g, furnace.Title)
		id, err := AddEntryBeside(g, anchor.ID, tc.after, captureJSON(t, "TODO", tc.title), fixtureNow, "P", "p@x")
		if err != nil {
			t.Fatal(err)
		}
		e := findEntry(t, g, tc.title)
		if id != tc.wantID || e.ID != id || e.Level != 2 || !e.PendingNew {
			t.Errorf("%s: returned %q, entry %+v", tc.title, id, e)
		}
	}
}

func TestPendingCaptureStaysMarkedWithAnEntryAddedBelowIt(t *testing.T) {
	g, _ := setup(t)
	id, err := Capture(g, `{"title":"Captured","createdAt":"2026-10-07T09:00:00-07:00"}`, "P", "p@x")
	if err != nil {
		t.Fatal(err)
	}
	c := findEntry(t, g, "Captured")
	if _, err := AddEntryBeside(g, c.ID, true, captureJSON(t, "", "Below it"), fixtureNow, "P", "p@x"); err != nil {
		t.Fatal(err)
	}
	if _, err := AddEntryBeside(g, c.ID, false, captureJSON(t, "", "Above it"), fixtureNow, "P", "p@x"); err != nil {
		t.Fatal(err)
	}
	for _, e := range inboxEntries(t, g) {
		if (e.Title == "Captured") != (e.CaptureID == id) {
			t.Errorf("%q: captureId %q", e.Title, e.CaptureID)
		}
	}
}

func TestDeleteEntryRemovesItsSubtree(t *testing.T) {
	g, _ := setup(t)
	fan := findEntry(t, g, "Replace the bathroom fan")
	if fan.Descendants != 3 {
		t.Errorf("descendants = %d, want 3", fan.Descendants)
	}
	if err := DeleteEntry(g, fan.ID, fixtureNow, "P", "p@x"); err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"Replace the bathroom fan", "Order a 110 CFM fan"} {
		if res := decode[searchResult](t)(Search(g, title)); len(res.Entries) != 0 {
			t.Errorf("%q still there: %+v", title, res.Entries)
		}
	}
	if after := findEntry(t, g, "Schedule furnace maintenance before it gets cold"); after.ID != fan.ID {
		t.Errorf("next sibling's id = %q, want %q", after.ID, fan.ID)
	}
	if st := decode[statusResult](t)(Status(g)); st.PendingStates != 1 {
		t.Errorf("pendingStates = %d", st.PendingStates)
	}
	if err := DeleteEntry(g, "personal.org:99", fixtureNow, "P", "p@x"); err == nil {
		t.Error("bad id accepted")
	}
}

func TestMoveEntryReturnsTheNewIDAndMarksItPending(t *testing.T) {
	g, _ := setup(t)
	// personal.org:0 is "Home": 0.0 the bathroom fan (with children),
	// 0.1 furnace, 0.2 gutters, 0.3 fence quote, 0.4 standing desk.
	for _, tc := range []struct {
		title, anchor string
		after         bool
		wantID        string
	}{
		{"Build a standing desk for the office", "Replace the bathroom fan", false, "personal.org:0.0"},
		{"Replace the bathroom fan", "Clean the gutters", true, "personal.org:0.3"},
	} {
		e, a := findEntry(t, g, tc.title), findEntry(t, g, tc.anchor)
		id, err := MoveEntry(g, e.ID, a.ID, tc.after, fixtureNow, "P", "p@x")
		if err != nil {
			t.Fatalf("%s: %v", tc.title, err)
		}
		moved := findEntry(t, g, tc.title)
		if id != tc.wantID || moved.ID != id || moved.Level != e.Level || !moved.PendingState {
			t.Errorf("%s: returned %q, entry %+v", tc.title, id, moved)
		}
	}
	if fan := findEntry(t, g, "Order a 110 CFM fan"); fan.ID != "personal.org:0.3.1" {
		t.Errorf("the fan's child is %q, want it moved with its parent", fan.ID)
	}
	if st := decode[statusResult](t)(Status(g)); st.PendingStates != 2 {
		t.Errorf("pendingStates = %d", st.PendingStates)
	}
}

func TestMoveEntryRefusals(t *testing.T) {
	g, _ := setup(t)
	if _, err := MoveEntry(g, "personal.org:0.1", "personal.org:1", false, fixtureNow, "P", "p@x"); err == nil {
		t.Error("moving next to a non-sibling accepted")
	}
	if _, err := MoveEntry(g, "personal.org:0.1", "personal.org:0.1", false, fixtureNow, "P", "p@x"); err == nil {
		t.Error("moving next to itself accepted")
	}
	if id, err := MoveEntry(g, "personal.org:0.1", "personal.org:0.2", false, fixtureNow, "P", "p@x"); err != nil || id != "personal.org:0.1" {
		t.Errorf("a move to where it already is: %q, %v", id, err)
	}
	if st := decode[statusResult](t)(Status(g)); st.PendingStates != 0 {
		t.Errorf("refusals queued %d changes", st.PendingStates)
	}
}

func TestMoveEntryToFileMovesTheSubtreeBetweenFiles(t *testing.T) {
	g, _ := setup(t)
	flights := findEntry(t, g, "Book flights for the November offsite")
	q4 := findEntry(t, g, "Q4 planning") // work.org:1
	id, err := MoveEntryToFile(g, flights.ID, "work.org", q4.ID, false, fixtureNow, "P", "p@x")
	if err != nil {
		t.Fatal(err)
	}
	moved := findEntry(t, g, flights.Title)
	if id != "work.org:1" || moved.ID != id || moved.File != "work.org" || !moved.PendingState {
		t.Errorf("returned %q, entry %+v", id, moved)
	}
	if q := findEntry(t, g, "Q4 planning"); q.ID != "work.org:2" {
		t.Errorf("Q4 planning is %q, want it after the moved entry", q.ID)
	}
	inbox, _ := os.ReadFile(filepath.Join(repoDir(g), "inbox.org"))
	work, _ := os.ReadFile(filepath.Join(repoDir(g), "work.org"))
	if strings.Contains(string(inbox), flights.Title) {
		t.Errorf("inbox.org still has the entry:\n%s", inbox)
	}
	want := "* TODO Book flights for the November offsite\n  :PROPERTIES:\n  :CREATED: [2026-10-03 Sat 09:05]\n  :END:\n* Q4 planning"
	if !strings.Contains(string(work), want) {
		t.Errorf("work.org doesn't have the entry right before Q4 planning:\n%s", work)
	}

	// With no anchor, it goes at the end of the file, subtree and all.
	q4 = findEntry(t, g, "Q4 planning")
	id, err = MoveEntryToFile(g, q4.ID, "personal.org", "", false, fixtureNow, "P", "p@x")
	if err != nil {
		t.Fatal(err)
	}
	if moved := findEntry(t, g, "Q4 planning"); id != "personal.org:5" || moved.ID != id || moved.Descendants == 0 {
		t.Errorf("returned %q, entry %+v", id, moved)
	}
}

func TestMoveEntryToFileRefusals(t *testing.T) {
	g, _ := setup(t)
	if _, err := MoveEntryToFile(g, "personal.org:0.1", "work.org", "", false, fixtureNow, "P", "p@x"); err == nil {
		t.Error("moving a child to another file accepted")
	}
	if _, err := MoveEntryToFile(g, "personal.org:0", "personal.org", "", false, fixtureNow, "P", "p@x"); err == nil {
		t.Error("moving to the same file accepted")
	}
	if _, err := MoveEntryToFile(g, "personal.org:0", "calendar.org", "", false, fixtureNow, "P", "p@x"); err == nil {
		t.Error("moving to the calendar file accepted")
	}
	if _, err := MoveEntryToFile(g, "personal.org:0", "work.org", "work.org:0.1", false, fixtureNow, "P", "p@x"); err == nil {
		t.Error("anchoring to a child accepted")
	}
	if st := decode[statusResult](t)(Status(g)); st.PendingStates != 0 {
		t.Errorf("refusals queued %d changes", st.PendingStates)
	}
}

func TestSetStateLogsAClosingNote(t *testing.T) {
	g, _ := setup(t)
	e := findEntry(t, g, "Book flights for the November offsite")
	if err := SetState(g, e.ID, "DONE", "  Booked via the travel desk ", fixtureNow, "P", "p@x"); err != nil {
		t.Fatal(err)
	}
	inbox, _ := os.ReadFile(filepath.Join(repoDir(g), "inbox.org"))
	if !strings.Contains(string(inbox), "- CLOSING NOTE [") || !strings.Contains(string(inbox), "\n    Booked via the travel desk\n") {
		t.Errorf("inbox.org has no closing note:\n%s", inbox)
	}
	if after := findEntry(t, g, e.Title); !strings.HasPrefix(after.Body, "- CLOSING NOTE [") {
		t.Errorf("body = %q, want the note first", after.Body)
	}
}
