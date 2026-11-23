package mobilecore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sburnett/orgtd/internal/gitstore"
	"github.com/sburnett/orgtd/internal/org"
)

// The phone pushes straight into the desktop's org directory, where
// desktop orgtd's store (internal/gitstore) brings "mobile" into main,
// as its polling does: these tests cover a phone change to an entry the
// desktop changed too, which the phone used to drop.

const vetTitle = "Call the vet about Fido's checkup"

// setupDesktop clones the fixture repository straight onto the phone,
// as when the phone pushes into the org directory itself, and opens
// desktop orgtd's store on that directory.
func setupDesktop(t *testing.T) (groupDir, desktop string, store *gitstore.Store) {
	t.Helper()
	root := t.TempDir()
	desktop = fixtureRepo(t, root)
	groupDir = filepath.Join(root, "group")
	if err := Clone(groupDir, desktop, nil, ""); err != nil {
		t.Fatalf("Clone: %v", err)
	}
	store, err := gitstore.Open(desktop, "main", nil, org.DefaultKeywords())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return groupDir, desktop, store
}

// desktopEdit saves file with old replaced by new, as a desktop :w.
func desktopEdit(t *testing.T, store *gitstore.Store, desktop, file, old, new string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(desktop, file))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), old) {
		t.Fatalf("%s has no %q", file, old)
	}
	head, err := store.Head()
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(data), old, new, 1)
	res, err := store.Save(head, []gitstore.Edit{{Path: file, Base: data, HasBase: true, Content: []byte(edited)}}, "desktop")
	if err != nil || res.Conflicts != nil {
		t.Fatalf("desktop save: %+v, %v", res, err)
	}
}

func phoneSync(t *testing.T, g string) statusResult {
	t.Helper()
	if err := Sync(g, nil, "Phone", "p@x"); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	return decode[statusResult](t)(Status(g))
}

func readFile(t *testing.T, dir, file string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, file))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestPhoneEditToAnEntryTheDesktopChangedTooIsMerged(t *testing.T) {
	g, desktop, store := setupDesktop(t)
	e := findEntry(t, g, vetTitle)
	form := formFor(e)
	form["body"] = "Ask about the vaccine"
	if err := EditEntry(g, e.ID, editJSON(t, form), fixtureNow, "P", "p@x"); err != nil {
		t.Fatal(err)
	}
	// The desktop tags the same entry before the phone syncs.
	desktopEdit(t, store, desktop, "personal.org", "** NEXT "+vetTitle+"\n", "** NEXT "+vetTitle+" :pets:\n")

	st := phoneSync(t, g)
	if st.PendingStates != 1 || len(st.Waiting) != 1 || st.Waiting[0] != vetTitle || len(st.Dropped) != 0 {
		t.Fatalf("status after sync = %+v, want the edit waiting, not dropped", st)
	}

	res, err := store.ApplyBranch("mobile")
	if err != nil || !res.Merged {
		t.Fatalf("desktop ApplyBranch = %+v, %v; want a clean merge", res, err)
	}
	got := readFile(t, desktop, "personal.org")
	if !strings.Contains(got, "** NEXT "+vetTitle+" :pets:\n   SCHEDULED: <2026-10-07 Wed>\n   Ask about the vaccine\n") {
		t.Errorf("personal.org on the desktop =\n%s\nwant both the tag and the phone's notes", got)
	}

	st = phoneSync(t, g)
	if st.PendingStates != 0 || len(st.Waiting) != 0 || st.Unpushed != 0 {
		t.Errorf("status after the merge = %+v, want nothing pending", st)
	}
	after := findEntry(t, g, vetTitle)
	if after.PendingState || after.Body != "Ask about the vaccine" || strings.Join(after.Tags, ",") != "pets" {
		t.Errorf("entry on the phone after the merge = %+v", after)
	}
}

func TestConflictingPhoneChangeIsResolvedOnTheDesktop(t *testing.T) {
	g, desktop, store := setupDesktop(t)
	e := findEntry(t, g, vetTitle)
	if err := SetState(g, e.ID, "DONE", "", fixtureNow, "P", "p@x"); err != nil {
		t.Fatal(err)
	}
	phoneVersion := readFile(t, repoDir(g), "personal.org")
	desktopEdit(t, store, desktop, "personal.org", "** NEXT "+vetTitle+"\n", "** TODO "+vetTitle+"\n")

	if st := phoneSync(t, g); len(st.Waiting) != 1 || st.PendingStates != 1 {
		t.Fatalf("status after sync = %+v, want the change waiting", st)
	}
	res, err := store.ApplyBranch("mobile")
	if err != nil || res.Conflict == nil {
		t.Fatalf("desktop ApplyBranch = %+v, %v; want a conflict", res, err)
	}
	c := res.Conflict.Files["personal.org"]
	if !strings.Contains(string(c.Merged), "<<<<<<< mobile\n** DONE "+vetTitle) {
		t.Errorf("conflict =\n%s", c.Merged)
	}

	// Nothing is lost while it waits: the phone keeps its change through
	// another sync.
	if st := phoneSync(t, g); st.PendingStates != 1 {
		t.Fatalf("status while the desktop's conflict waits = %+v", st)
	}

	// The user takes the phone's side.
	res, err = store.ResolveBranch(res.Conflict, map[string][]byte{"personal.org": []byte(phoneVersion)})
	if err != nil || !res.Merged {
		t.Fatalf("ResolveBranch = %+v, %v", res, err)
	}
	st := phoneSync(t, g)
	if st.PendingStates != 0 || len(st.Waiting) != 0 {
		t.Errorf("status after resolving = %+v, want nothing pending", st)
	}
	if after := findEntry(t, g, vetTitle); after.Keyword != "DONE" || after.PendingState {
		t.Errorf("entry on the phone = %+v", after)
	}
}

// The clock means "the phone still has something to do": once a change
// is sent, it goes, though the change stays queued until it's merged.
func TestSentChangesNeedNothingMoreFromThePhone(t *testing.T) {
	g, _, store := setupDesktop(t)
	if _, err := Capture(g, `{"title":"Call the plumber","createdAt":"2026-11-14T09:12:00-08:00"}`, "P", "p@x"); err != nil {
		t.Fatal(err)
	}
	vet := findEntry(t, g, vetTitle)
	if err := SetState(g, vet.ID, "DONE", "", fixtureNow, "P", "p@x"); err != nil {
		t.Fatal(err)
	}
	in := inboxEntries(t, g)
	if c := in[len(in)-1]; !c.Unsent || c.CaptureID == "" {
		t.Errorf("capture before Sync = %+v, want unsent and editable in the queue", c)
	}
	if e := findEntry(t, g, vetTitle); !e.Unsent {
		t.Errorf("state change before Sync = %+v, want unsent", e)
	}

	st := phoneSync(t, g)
	if st.Sent != 2 || st.Unpushed != 0 || st.Pending != 1 || st.PendingStates != 1 {
		t.Errorf("status after Sync = %+v, want both sent and still queued", st)
	}
	in = inboxEntries(t, g)
	if c := in[len(in)-1]; c.Unsent || !c.Pending || c.CaptureID != "" {
		t.Errorf("capture after Sync = %+v, want sent: pending, no clock, no capture ID", c)
	}
	if e := findEntry(t, g, vetTitle); e.Unsent || !e.PendingState {
		t.Errorf("state change after Sync = %+v, want sent: pending, no clock", e)
	}

	// The desktop applies it; the phone's next fetch clears the queue.
	if res, err := store.ApplyBranch("mobile"); err != nil || !res.Moved {
		t.Fatalf("desktop ApplyBranch = %+v, %v", res, err)
	}
	if err := Refresh(g, nil, "P", "p@x"); err != nil {
		t.Fatal(err)
	}
	if st := decode[statusResult](t)(Status(g)); st.Pending != 0 || st.PendingStates != 0 || st.Sent != 0 {
		t.Errorf("status after the desktop applied it = %+v, want nothing queued", st)
	}
}

// A sent capture is edited as an entry, not in the queue: the desktop
// may have merged it as sent before the phone fetches, and the phone
// would then drop the queue item, edit and all.
func TestEditingASentCaptureIsNotLost(t *testing.T) {
	g, desktop, store := setupDesktop(t)
	id, err := Capture(g, `{"title":"Call the plumber","createdAt":"2026-11-14T09:12:00-08:00"}`, "P", "p@x")
	if err != nil {
		t.Fatal(err)
	}
	phoneSync(t, g)
	if res, err := store.ApplyBranch("mobile"); err != nil || !res.Moved {
		t.Fatalf("desktop ApplyBranch = %+v, %v", res, err)
	}
	// The phone hasn't fetched the merge yet.
	if err := UpdatePendingCapture(g, `{"id":"`+id+`","title":"Edited in the queue"}`, "P", "p@x"); err == nil {
		t.Error("UpdatePendingCapture of a sent capture accepted")
	}
	if err := DeletePendingCapture(g, id, "P", "p@x"); err == nil {
		t.Error("DeletePendingCapture of a sent capture accepted")
	}
	in := inboxEntries(t, g)
	c := in[len(in)-1]
	form := formFor(c)
	form["title"] = "Call the plumber about the leak"
	if err := EditEntry(g, c.ID, editJSON(t, form), fixtureNow, "P", "p@x"); err != nil {
		t.Fatal(err)
	}
	phoneSync(t, g)
	if _, err := store.ApplyBranch("mobile"); err != nil {
		t.Fatal(err)
	}
	if st := phoneSync(t, g); st.Pending != 0 || st.PendingStates != 0 {
		t.Errorf("status at the end = %+v, want nothing queued", st)
	}
	if inbox := readFile(t, desktop, "inbox.org"); !strings.Contains(inbox, "* Call the plumber about the leak\n") || strings.Contains(inbox, "* Call the plumber\n") {
		t.Errorf("desktop inbox.org =\n%s\nwant the edited title only", inbox)
	}
}

// A change waiting for the desktop to merge it keeps its clock: it was
// sent, but on the old base, and the banner names it.
func TestWaitingChangesKeepTheirClock(t *testing.T) {
	g, desktop, store := setupDesktop(t)
	e := findEntry(t, g, vetTitle)
	if err := SetState(g, e.ID, "DONE", "", fixtureNow, "P", "p@x"); err != nil {
		t.Fatal(err)
	}
	desktopEdit(t, store, desktop, "personal.org", "** NEXT "+vetTitle+"\n", "** TODO "+vetTitle+"\n")
	if st := phoneSync(t, g); len(st.Waiting) != 1 || st.Unpushed != 0 {
		t.Fatalf("status = %+v, want the change sent and waiting", st)
	}
	if after := findEntry(t, g, vetTitle); !after.Unsent {
		t.Errorf("waiting entry = %+v, want the clock kept", after)
	}
}
