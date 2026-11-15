package gitsync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	got := patchEntry(own, f.Headlines[0], "NEXT", opTime)
	want := []string{"** NEXT Odd", "   DEADLINE:    <2026-11-20 Fri>", "   body"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("patched = %q", got)
	}
}
