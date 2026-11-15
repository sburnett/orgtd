package gitsync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/sburnett/orgtd/internal/capturequeue"
	"github.com/sburnett/orgtd/internal/org"
)

// A work file with irregular spacing the desktop's renderer would
// normalize, to prove a state change rewrites only its own entry.
const workOrg = `#+TITLE:   Work   notes
* Project      :proj:
  Some   body   text, spaced oddly.
** TODO Write report
   :PROPERTIES:
   :CREATED:  [2026-11-01 Sun 10:00]
   :END:
** TODO Water plants
   SCHEDULED: <2026-11-10 Tue +1w>
*   Other   heading
`

var opTime = time.Date(2026, 11, 14, 9, 30, 0, 0, time.UTC)

func newOpsFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	f.macCommit("work.org", workOrg, "work")
	f.macPush()
	f.phone.OpsPath = filepath.Join(filepath.Dir(f.phone.QueuePath), "state-ops.json")
	if err := f.phone.Refresh(); err != nil {
		t.Fatal(err)
	}
	return f
}

// queueOp queues changing the entry titled title (in the phone's working
// tree) to keyword, and rebuilds.
func (f *fixture) queueOp(file, title, keyword string) {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.phone.Dir, file))
	if err != nil {
		f.t.Fatal(err)
	}
	of, _ := org.Parse(strings.NewReader(string(data)), file)
	var h *org.Headline
	org.Walk(of.Headlines, func(x *org.Headline) {
		if x.Title == title {
			h = x
		}
	})
	if h == nil {
		f.t.Fatalf("no entry %q in %s", title, file)
	}
	if _, err := capturequeue.AddOp(f.phone.OpsPath, NewStateOp(file, h, keyword, opTime)); err != nil {
		f.t.Fatal(err)
	}
	if err := f.phone.Rebuild(); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) ops() []capturequeue.StateOp {
	ops, err := capturequeue.LoadOps(f.phone.OpsPath)
	if err != nil {
		f.t.Fatal(err)
	}
	return ops
}

func TestStateChangeRewritesOnlyItsEntry(t *testing.T) {
	f := newOpsFixture(t)
	f.queueOp("work.org", "Write report", "DONE")
	f.sync()
	f.macFetch()
	mobile, _ := f.remoteRef(MobileBranch)
	got := f.fileAt(f.mac, mobile, "work.org")
	want := strings.Replace(workOrg, "** TODO Write report\n", "** DONE Write report\n   CLOSED: [2026-11-14 Sat 09:30]\n", 1)
	if got != want {
		t.Errorf("work.org on mobile =\n%s\nwant\n%s", got, want)
	}
}

func TestCompletingARepeaterAdvancesItOnceAcrossRebuilds(t *testing.T) {
	f := newOpsFixture(t)
	f.queueOp("work.org", "Water plants", "DONE")
	for i := 0; i < 3; i++ {
		if err := f.phone.Refresh(); err != nil {
			t.Fatal(err)
		}
	}
	data, _ := os.ReadFile(filepath.Join(f.phone.Dir, "work.org"))
	if !strings.Contains(string(data), "** TODO Water plants\n   SCHEDULED: <2026-11-17 Tue +1w>\n   :PROPERTIES:\n   :LAST_REPEAT: [2026-11-14 Sat 09:30]\n   :END:\n") {
		t.Errorf("work.org =\n%s", data)
	}
}

func TestStateChangeLeavesQueueWhenMerged(t *testing.T) {
	f := newOpsFixture(t)
	f.queueOp("work.org", "Write report", "NEXT")
	f.sync()
	tip, _ := f.remoteRef(MobileBranch)
	f.macFetch()
	f.mac.Storer.SetReference(plumbingRef("main", tip))
	f.macPush()
	f.sync()
	if ops := f.ops(); len(ops) != 0 {
		t.Errorf("ops after merge = %+v", ops)
	}
	if _, ok := f.remoteRef(MobileBranch); ok {
		t.Error("origin/mobile left behind with nothing pending")
	}
}

func TestStateChangeOnAnEntryEditedOnTheDesktopIsDropped(t *testing.T) {
	f := newOpsFixture(t)
	f.queueOp("work.org", "Write report", "DONE")
	f.macCommit("work.org", strings.Replace(workOrg, "** TODO Write report", "** TODO Write the report", 1), "renamed")
	f.macPush()
	f.sync()
	if ops := f.ops(); len(ops) != 0 {
		t.Errorf("ops = %+v, want the stale one dropped", ops)
	}
	s, _ := f.phone.LoadState()
	if len(s.Dropped) != 1 || s.Dropped[0].Title != "Write report" || s.Dropped[0].To != "DONE" {
		t.Errorf("dropped = %+v", s.Dropped)
	}
	if err := f.phone.DismissDropped(); err != nil {
		t.Fatal(err)
	}
	if s, _ := f.phone.LoadState(); len(s.Dropped) != 0 {
		t.Error("DismissDropped kept them")
	}
}

func TestStateChangeOnAPendingCapture(t *testing.T) {
	f := newOpsFixture(t)
	f.capture("Buy milk", 1)
	f.queueOp("inbox.org", "Buy milk", "DONE")
	f.sync()
	f.macFetch()
	mobile, _ := f.remoteRef(MobileBranch)
	if got := f.fileAt(f.mac, mobile, "inbox.org"); !strings.Contains(got, "* DONE Buy milk\n  CLOSED: [2026-11-14 Sat 09:30]\n") {
		t.Errorf("inbox.org = %s", got)
	}
}

func TestUnionMerged(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".gitattributes"), []byte("# notes\ninbox.org merge=union\n*.md -merge\nsub/x.org merge=union\n"), 0o644)
	for file, want := range map[string]bool{"inbox.org": true, "work.org": false, "sub/x.org": true, "other/x.org": false} {
		if got := UnionMerged(dir, file); got != want {
			t.Errorf("UnionMerged(%s) = %v", file, got)
		}
	}
}

func TestKeywordOnlyChangeKeepsThePlanningLineAsWritten(t *testing.T) {
	own := []string{"** TODO Odd", "   DEADLINE:    <2026-11-20 Fri>", "   body"}
	f, _ := org.Parse(strings.NewReader(strings.Join(own, "\n")+"\n"), "x.org")
	got, _ := patchEntry(own, f.Headlines[0], capturequeue.StateOp{From: "TODO", To: "NEXT", CreatedAt: opTime})
	want := []string{"** NEXT Odd", "   DEADLINE:    <2026-11-20 Fri>", "   body"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("patched = %q", got)
	}
}

// TestStateChangeUnderASkippedLevel: an entry's level needn't equal its
// depth ("*" then "***" is valid org), so matching is by title path.
func TestStateChangeUnderASkippedLevel(t *testing.T) {
	f := newOpsFixture(t)
	f.macCommit("deep.org", "* Parent\n*** TODO Skipped level\n", "deep")
	f.macPush()
	if err := f.phone.Refresh(); err != nil {
		t.Fatal(err)
	}
	f.queueOp("deep.org", "Skipped level", "DONE")
	data, _ := os.ReadFile(filepath.Join(f.phone.Dir, "deep.org"))
	if !strings.Contains(string(data), "*** DONE Skipped level\n") {
		t.Errorf("deep.org = %q", data)
	}
	if s, _ := f.phone.LoadState(); len(s.Dropped) != 0 {
		t.Errorf("dropped = %+v", s.Dropped)
	}
}

func (f *fixture) queueEdit(file, title string, edit capturequeue.EntryEdit, keyword string) {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.phone.Dir, file))
	if err != nil {
		f.t.Fatal(err)
	}
	of, _ := org.Parse(strings.NewReader(string(data)), file)
	var h *org.Headline
	org.Walk(of.Headlines, func(x *org.Headline) {
		if x.Title == title {
			h = x
		}
	})
	if h == nil {
		f.t.Fatalf("no entry %q", title)
	}
	if keyword == "" {
		keyword = h.Keyword
	}
	op := NewStateOp(file, h, keyword, opTime)
	op.Edit = &edit
	if _, err := capturequeue.AddOp(f.phone.OpsPath, op); err != nil {
		f.t.Fatal(err)
	}
	if err := f.phone.Rebuild(); err != nil {
		f.t.Fatal(err)
	}
}

func ptr[T any](v T) *T { return &v }

func TestEditRewritesOnlyWhatChanged(t *testing.T) {
	f := newOpsFixture(t)
	f.queueEdit("work.org", "Write report", capturequeue.EntryEdit{
		Title: ptr("Write the quarterly report"),
		Tags:  ptr([]string{"q4", "writing"}),
		Body:  ptr("First draft by Friday\n  - outline\n  - numbers"),
	}, "")
	data, _ := os.ReadFile(filepath.Join(f.phone.Dir, "work.org"))
	want := strings.Replace(workOrg, `** TODO Write report
   :PROPERTIES:
   :CREATED:  [2026-11-01 Sun 10:00]
   :END:
`, `** TODO Write the quarterly report   :q4:writing:
   :PROPERTIES:
   :CREATED:  [2026-11-01 Sun 10:00]
   :END:
   First draft by Friday
     - outline
     - numbers
`, 1)
	if string(data) != want {
		t.Errorf("work.org =\n%s\nwant\n%s", data, want)
	}
	repo := gitLog(t, f.phone.Dir)
	if !strings.Contains(repo, "Edit: Write the quarterly report") {
		t.Errorf("commits: %s", repo)
	}
}

func TestEditingADateKeepsItsRepeater(t *testing.T) {
	f := newOpsFixture(t)
	f.queueEdit("work.org", "Water plants", capturequeue.EntryEdit{Scheduled: ptr("2026-11-21")}, "")
	data, _ := os.ReadFile(filepath.Join(f.phone.Dir, "work.org"))
	if !strings.Contains(string(data), "** TODO Water plants\n   SCHEDULED: <2026-11-21 Sat +1w>\n") {
		t.Errorf("work.org = %s", data)
	}
}

func TestEditRemovingADateAndChangingKeyword(t *testing.T) {
	f := newOpsFixture(t)
	f.queueEdit("work.org", "Water plants", capturequeue.EntryEdit{Scheduled: ptr("")}, "NEXT")
	data, _ := os.ReadFile(filepath.Join(f.phone.Dir, "work.org"))
	if !strings.Contains(string(data), "** NEXT Water plants\n*   Other   heading\n") {
		t.Errorf("work.org = %s", data)
	}
}

func gitLog(t *testing.T, dir string) string {
	t.Helper()
	repo, err := git.PlainOpen(dir)
	if err != nil {
		t.Fatal(err)
	}
	iter, _ := repo.Log(&git.LogOptions{})
	var msgs []string
	iter.ForEach(func(c *object.Commit) error { msgs = append(msgs, c.Message); return nil })
	return strings.Join(msgs, "\n")
}
