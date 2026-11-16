package gitsync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/sburnett/orgtd/internal/capture"
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

// queueInsert queues adding an entry titled title under the entry titled
// parent ("" for top-level), first or last, and rebuilds.
func (f *fixture) queueInsert(file, parent, title string, last bool) {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.phone.Dir, file))
	if err != nil {
		f.t.Fatal(err)
	}
	of, _ := org.Parse(strings.NewReader(string(data)), file)
	var p *org.Headline
	if parent != "" {
		org.Walk(of.Headlines, func(x *org.Headline) {
			if x.Title == parent {
				p = x
			}
		})
		if p == nil {
			f.t.Fatalf("no entry %q in %s", parent, file)
		}
	}
	c := capture.Capture{CreatedAt: opTime, Keyword: "TODO", Title: title, Body: "a note"}
	if _, err := capturequeue.AddOp(f.phone.OpsPath, NewInsertOp(file, p, last, c)); err != nil {
		f.t.Fatal(err)
	}
	if err := f.phone.Rebuild(); err != nil {
		f.t.Fatal(err)
	}
}

func TestInsertAddsOnlyTheNewEntryAtEachPosition(t *testing.T) {
	entry := func(stars, title string) string {
		indent := strings.Repeat(" ", len(stars)+1)
		return stars + " TODO " + title + "\n" + indent + ":PROPERTIES:\n" + indent + ":CREATED: [2026-11-14 Sat 09:30]\n" + indent + ":END:\n" + indent + "a note\n"
	}
	for _, tc := range []struct {
		name, parent string
		last         bool
		before       string // the line the new entry goes before ("" for the end)
		stars        string
	}{
		{"top first", "", false, "* Project      :proj:\n", "*"},
		{"top last", "", true, "", "*"},
		{"child first", "Project", false, "** TODO Write report\n", "**"},
		{"child last", "Project", true, "*   Other   heading\n", "**"},
		{"under a leaf", "Water plants", true, "*   Other   heading\n", "***"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newOpsFixture(t)
			f.queueInsert("work.org", tc.parent, "New thing", tc.last)
			data, _ := os.ReadFile(filepath.Join(f.phone.Dir, "work.org"))
			want := workOrg + entry(tc.stars, "New thing")
			if tc.before != "" {
				want = strings.Replace(workOrg, tc.before, entry(tc.stars, "New thing")+tc.before, 1)
			}
			if string(data) != want {
				t.Errorf("work.org =\n%s\nwant\n%s", data, want)
			}
		})
	}
}

func TestInsertKeepsBlankLineSpacing(t *testing.T) {
	f := newFixture(t)
	f.macCommit("spaced.org", "* One\n\n* Two\n", "spaced")
	f.macPush()
	f.phone.OpsPath = filepath.Join(filepath.Dir(f.phone.QueuePath), "state-ops.json")
	if err := f.phone.Refresh(); err != nil {
		t.Fatal(err)
	}
	f.queueInsert("spaced.org", "One", "Half", false) // under One: before Two, after the blank
	f.queueInsert("spaced.org", "", "Three", true)
	data, _ := os.ReadFile(filepath.Join(f.phone.Dir, "spaced.org"))
	want := "* One\n\n** TODO Half\n   :PROPERTIES:\n   :CREATED: [2026-11-14 Sat 09:30]\n   :END:\n   a note\n\n* Two\n" +
		"* TODO Three\n  :PROPERTIES:\n  :CREATED: [2026-11-14 Sat 09:30]\n  :END:\n  a note\n"
	if string(data) != want {
		t.Errorf("spaced.org =\n%q\nwant\n%q", data, want)
	}
}

func TestInsertUnderAParentEditedOnTheDesktopIsDropped(t *testing.T) {
	f := newOpsFixture(t)
	f.queueInsert("work.org", "Write report", "Draft outline", false)
	f.macCommit("work.org", strings.Replace(workOrg, "** TODO Write report", "** NEXT Write report", 1), "next")
	f.macPush()
	f.sync()
	if ops := f.ops(); len(ops) != 0 {
		t.Errorf("ops = %+v, want the stale one dropped", ops)
	}
	s, _ := f.phone.LoadState()
	if len(s.Dropped) != 1 || s.Dropped[0].Title != "Draft outline" || !s.Dropped[0].Added {
		t.Errorf("dropped = %+v", s.Dropped)
	}
}

func TestInsertLeavesQueueWhenMerged(t *testing.T) {
	f := newOpsFixture(t)
	f.queueInsert("work.org", "Project", "Draft outline", true)
	f.sync()
	tip, _ := f.remoteRef(MobileBranch)
	f.macFetch()
	f.mac.Storer.SetReference(plumbingRef("main", tip))
	f.macPush()
	f.sync()
	if ops := f.ops(); len(ops) != 0 {
		t.Errorf("ops after merge = %+v", ops)
	}
	data, _ := os.ReadFile(filepath.Join(f.phone.Dir, "work.org"))
	if n := strings.Count(string(data), "Draft outline"); n != 1 {
		t.Errorf("new entry appears %d times after merge:\n%s", n, data)
	}
}

// queueMove queues demoting (1) or promoting (-1) the entry titled title,
// as mobilecore.ShiftEntry does, and rebuilds.
func (f *fixture) queueMove(file, title string, delta int) {
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
	path := TitlePath(h)
	to := path // a move that can't apply; replay drops it
	siblings := of.Headlines
	if h.Parent != nil {
		siblings = h.Parent.Children
	}
	i := org.Siblings{File: of, Parent: h.Parent}.IndexOf(h)
	switch {
	case delta > 0 && i > 0:
		to = append(append(append([]string{}, path[:len(path)-1]...), siblings[i-1].Title), title)
	case delta < 0 && len(path) > 1:
		to = append(append([]string{}, path[:len(path)-2]...), title)
	}
	op := NewStateOp(file, h, h.Keyword, opTime)
	op.Move = &capturequeue.EntryMove{Delta: delta, To: to}
	if _, err := capturequeue.AddOp(f.phone.OpsPath, op); err != nil {
		f.t.Fatal(err)
	}
	if err := f.phone.Rebuild(); err != nil {
		f.t.Fatal(err)
	}
}

func TestMoveChangesOnlyStarsOrMovesTheSubtree(t *testing.T) {
	for _, tc := range []struct {
		name, title string
		delta       int
		want        string
	}{
		{"demote", "Water plants", 1, strings.Replace(workOrg, "** TODO Water plants", "*** TODO Water plants", 1)},
		{"demote a top-level entry", "Other   heading", 1, strings.Replace(workOrg, "*   Other", "**   Other", 1)},
		{"promote the last child", "Water plants", -1, strings.Replace(workOrg, "** TODO Water plants", "* TODO Water plants", 1)},
		{"promote past later siblings", "Write report", -1, `#+TITLE:   Work   notes
* Project      :proj:
  Some   body   text, spaced oddly.
** TODO Water plants
   SCHEDULED: <2026-11-10 Tue +1w>
* TODO Write report
   :PROPERTIES:
   :CREATED:  [2026-11-01 Sun 10:00]
   :END:
*   Other   heading
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newOpsFixture(t)
			f.queueMove("work.org", tc.title, tc.delta)
			data, _ := os.ReadFile(filepath.Join(f.phone.Dir, "work.org"))
			if string(data) != tc.want {
				t.Errorf("work.org =\n%s\nwant\n%s", data, tc.want)
			}
		})
	}
}

func TestMovingAParentMovesItsSubtree(t *testing.T) {
	f := newOpsFixture(t)
	f.queueMove("work.org", "Other   heading", 1)
	f.queueMove("work.org", "Project", 1) // under nothing: there's no previous sibling
	if err := f.phone.Rebuild(); err != nil {
		t.Fatal(err)
	}
	s, _ := f.phone.LoadState()
	if len(s.Dropped) != 1 || s.Dropped[0].Title != "Project" || s.Dropped[0].Move != 1 {
		t.Errorf("dropped = %+v", s.Dropped)
	}
	f.queueMove("work.org", "Other   heading", -1) // back out, after Project's subtree
	data, _ := os.ReadFile(filepath.Join(f.phone.Dir, "work.org"))
	if string(data) != workOrg {
		t.Errorf("work.org after demote and promote =\n%s\nwant\n%s", data, workOrg)
	}
}

func TestDemoteUnderAChangedSiblingIsDropped(t *testing.T) {
	f := newOpsFixture(t)
	f.queueMove("work.org", "Water plants", 1)
	f.macCommit("work.org", strings.Replace(workOrg, "** TODO Write report", "** TODO Write the report", 1), "renamed")
	f.macPush()
	f.sync()
	if ops := f.ops(); len(ops) != 0 {
		t.Errorf("ops = %+v, want the move dropped", ops)
	}
	data, _ := os.ReadFile(filepath.Join(f.phone.Dir, "work.org"))
	if !strings.Contains(string(data), "\n** TODO Water plants\n") {
		t.Errorf("work.org =\n%s", data)
	}
}

func TestInsertBesideASibling(t *testing.T) {
	entry := func(stars string) string {
		indent := strings.Repeat(" ", len(stars)+1)
		return stars + " TODO New thing\n" + indent + ":PROPERTIES:\n" + indent + ":CREATED: [2026-11-14 Sat 09:30]\n" + indent + ":END:\n" + indent + "a note\n"
	}
	for _, tc := range []struct {
		name, sibling string
		after         bool
		before, stars string // the line the new entry goes before ("" for the end)
	}{
		{"above a child", "Water plants", false, "** TODO Water plants\n", "**"},
		{"below a child", "Write report", true, "** TODO Water plants\n", "**"},
		{"above a top-level entry", "Project", false, "* Project      :proj:\n", "*"},
		{"below a parent's subtree", "Project", true, "*   Other   heading\n", "*"},
		{"below the last entry", "Other   heading", true, "", "*"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newOpsFixture(t)
			data, _ := os.ReadFile(filepath.Join(f.phone.Dir, "work.org"))
			of, _ := org.Parse(strings.NewReader(string(data)), "work.org")
			var s *org.Headline
			org.Walk(of.Headlines, func(x *org.Headline) {
				if x.Title == tc.sibling {
					s = x
				}
			})
			c := capture.Capture{CreatedAt: opTime, Keyword: "TODO", Title: "New thing", Body: "a note"}
			if _, err := capturequeue.AddOp(f.phone.OpsPath, NewInsertBesideOp("work.org", s, tc.after, c)); err != nil {
				t.Fatal(err)
			}
			if err := f.phone.Rebuild(); err != nil {
				t.Fatal(err)
			}
			data, _ = os.ReadFile(filepath.Join(f.phone.Dir, "work.org"))
			want := workOrg + entry(tc.stars)
			if tc.before != "" {
				want = strings.Replace(workOrg, tc.before, entry(tc.stars)+tc.before, 1)
			}
			if string(data) != want {
				t.Errorf("work.org =\n%s\nwant\n%s", data, want)
			}
		})
	}
}

// queueDelete queues deleting the entry titled title, and rebuilds.
func (f *fixture) queueDelete(file, title string) {
	f.t.Helper()
	data, _ := os.ReadFile(filepath.Join(f.phone.Dir, file))
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
	if _, err := capturequeue.AddOp(f.phone.OpsPath, NewDeleteOp(file, h, opTime)); err != nil {
		f.t.Fatal(err)
	}
	if err := f.phone.Rebuild(); err != nil {
		f.t.Fatal(err)
	}
}

func TestDeleteRemovesOnlyTheSubtree(t *testing.T) {
	for _, tc := range []struct{ title, want string }{
		{"Write report", strings.Replace(workOrg, "** TODO Write report\n   :PROPERTIES:\n   :CREATED:  [2026-11-01 Sun 10:00]\n   :END:\n", "", 1)},
		{"Project", "#+TITLE:   Work   notes\n*   Other   heading\n"},
		{"Other   heading", strings.Replace(workOrg, "*   Other   heading\n", "", 1)},
	} {
		t.Run(tc.title, func(t *testing.T) {
			f := newOpsFixture(t)
			f.queueDelete("work.org", tc.title)
			data, _ := os.ReadFile(filepath.Join(f.phone.Dir, "work.org"))
			if string(data) != tc.want {
				t.Errorf("work.org =\n%s\nwant\n%s", data, tc.want)
			}
		})
	}
}

func TestDeleteOfAParentWhoseChildChangedIsDropped(t *testing.T) {
	f := newOpsFixture(t)
	f.queueDelete("work.org", "Project")
	f.macCommit("work.org", strings.Replace(workOrg, "** TODO Water plants", "** NEXT Water plants", 1), "child changed")
	f.macPush()
	f.sync()
	if ops := f.ops(); len(ops) != 0 {
		t.Errorf("ops = %+v, want the delete dropped", ops)
	}
	s, _ := f.phone.LoadState()
	if len(s.Dropped) != 1 || s.Dropped[0].Title != "Project" || !s.Dropped[0].Deleted {
		t.Errorf("dropped = %+v", s.Dropped)
	}
	data, _ := os.ReadFile(filepath.Join(f.phone.Dir, "work.org"))
	if !strings.Contains(string(data), "* Project") {
		t.Errorf("work.org =\n%s", data)
	}
}

// queueReorder queues and replays moving title (in file) right before
// the sibling anchor or, with after, right after it.
func (f *fixture) queueReorder(file, title, anchor string, after bool) {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.phone.Dir, file))
	if err != nil {
		f.t.Fatal(err)
	}
	of, _ := org.Parse(strings.NewReader(string(data)), file)
	var h, a *org.Headline
	org.Walk(of.Headlines, func(x *org.Headline) {
		switch x.Title {
		case title:
			h = x
		case anchor:
			a = x
		}
	})
	if h == nil || a == nil {
		f.t.Fatalf("no entry %q or %q in %s", title, anchor, file)
	}
	op := NewStateOp(file, h, h.Keyword, opTime)
	op.Move = &capturequeue.EntryMove{To: TitlePath(h), Anchor: a.Title, AnchorFingerprint: Fingerprint(a), After: after}
	if _, err := capturequeue.AddOp(f.phone.OpsPath, op); err != nil {
		f.t.Fatal(err)
	}
	if err := f.phone.Rebuild(); err != nil {
		f.t.Fatal(err)
	}
}

func TestReorderMovesTheSubtreeAmongItsSiblings(t *testing.T) {
	for _, tc := range []struct {
		name, title, anchor string
		after               bool
		want                string
	}{
		{"a child up", "Water plants", "Write report", false, `#+TITLE:   Work   notes
* Project      :proj:
  Some   body   text, spaced oddly.
** TODO Water plants
   SCHEDULED: <2026-11-10 Tue +1w>
** TODO Write report
   :PROPERTIES:
   :CREATED:  [2026-11-01 Sun 10:00]
   :END:
*   Other   heading
`},
		{"a child down", "Write report", "Water plants", true, `#+TITLE:   Work   notes
* Project      :proj:
  Some   body   text, spaced oddly.
** TODO Water plants
   SCHEDULED: <2026-11-10 Tue +1w>
** TODO Write report
   :PROPERTIES:
   :CREATED:  [2026-11-01 Sun 10:00]
   :END:
*   Other   heading
`},
		{"a parent below the last entry", "Project", "Other   heading", true, `#+TITLE:   Work   notes
*   Other   heading
* Project      :proj:
  Some   body   text, spaced oddly.
** TODO Write report
   :PROPERTIES:
   :CREATED:  [2026-11-01 Sun 10:00]
   :END:
** TODO Water plants
   SCHEDULED: <2026-11-10 Tue +1w>
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newOpsFixture(t)
			f.queueReorder("work.org", tc.title, tc.anchor, tc.after)
			data, _ := os.ReadFile(filepath.Join(f.phone.Dir, "work.org"))
			if string(data) != tc.want {
				t.Errorf("work.org =\n%s\nwant\n%s", data, tc.want)
			}
		})
	}
}

func TestReorderNextToAChangedSiblingIsDropped(t *testing.T) {
	f := newOpsFixture(t)
	f.queueReorder("work.org", "Water plants", "Write report", false)
	f.macCommit("work.org", strings.Replace(workOrg, "** TODO Write report", "** TODO Write the report", 1), "renamed")
	f.macPush()
	f.sync()
	if ops := f.ops(); len(ops) != 0 {
		t.Errorf("ops = %+v, want the move dropped", ops)
	}
	s, _ := f.phone.LoadState()
	if len(s.Dropped) != 1 || !s.Dropped[0].Reordered {
		t.Errorf("dropped = %+v", s.Dropped)
	}
}

func TestReorderKeepsBlankLineSpacingEven(t *testing.T) {
	for _, tc := range []struct {
		name           string
		in             []string
		start, end, at int
		want           []string
	}{
		{"last entry to the top", []string{"* A", "", "* B", "", "* C"}, 4, 5, 0, []string{"* C", "", "* A", "", "* B"}},
		{"first entry to the end", []string{"* A", "", "* B", "", "* C"}, 0, 2, 5, []string{"* B", "", "* C", "", "* A"}},
		{"middle entry up", []string{"* A", "", "* B", "", "* C"}, 2, 4, 0, []string{"* B", "", "* A", "", "* C"}},
		{"no blank lines", []string{"* A", "* B", "* C"}, 2, 3, 0, []string{"* C", "* A", "* B"}},
		{"after a preamble", []string{"#+TITLE: x", "* A", "", "* B"}, 3, 4, 1, []string{"#+TITLE: x", "* B", "", "* A"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := reorder(tc.in, tc.start, tc.end, tc.at)
			if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
				t.Errorf("reorder = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFileMoveIsDroppedIfItsAnchorChanged(t *testing.T) {
	f := newOpsFixture(t)
	f.macCommit("other.org", "* First\n* Second\n", "other")
	f.macPush()
	f.sync()
	data, _ := os.ReadFile(filepath.Join(f.phone.Dir, "work.org"))
	of, _ := org.Parse(strings.NewReader(string(data)), "work.org")
	odata, _ := os.ReadFile(filepath.Join(f.phone.Dir, "other.org"))
	oo, _ := org.Parse(strings.NewReader(string(odata)), "other.org")
	h, second := of.Headlines[1], oo.Headlines[1] // "Other   heading", "Second"
	op := NewStateOp("work.org", h, h.Keyword, opTime)
	op.Move = &capturequeue.EntryMove{To: []string{h.Title}, ToFile: "other.org", Anchor: second.Title, AnchorFingerprint: Fingerprint(second)}
	if _, err := capturequeue.AddOp(f.phone.OpsPath, op); err != nil {
		t.Fatal(err)
	}
	if err := f.phone.Rebuild(); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(f.phone.Dir, "other.org"))
	if string(got) != "* First\n*   Other   heading\n* Second\n" {
		t.Errorf("other.org =\n%s", got)
	}
	if got, _ := os.ReadFile(filepath.Join(f.phone.Dir, "work.org")); strings.Contains(string(got), "Other") {
		t.Errorf("work.org still has the entry:\n%s", got)
	}

	f.macCommit("other.org", "* First\n* Second, renamed\n", "renamed")
	f.macPush()
	f.sync()
	if ops := f.ops(); len(ops) != 0 {
		t.Errorf("ops = %+v, want the move dropped", ops)
	}
	if got, _ := os.ReadFile(filepath.Join(f.phone.Dir, "work.org")); !strings.Contains(string(got), "*   Other   heading") {
		t.Errorf("work.org lost the entry when its move was dropped:\n%s", got)
	}
}

func TestPasteSubtreeIntoAnEmptyFileAddsNoBlankLines(t *testing.T) {
	if got := pasteSubtree(nil, 0, []string{"* A"}, 1); strings.Join(got, "\n") != "* A" {
		t.Errorf("pasteSubtree = %q", got)
	}
}

// queueClose queues and replays setting title (in work.org) to keyword
// with a closing note.
func (f *fixture) queueClose(title, keyword, note string) string {
	f.t.Helper()
	data, _ := os.ReadFile(filepath.Join(f.phone.Dir, "work.org"))
	of, _ := org.Parse(strings.NewReader(string(data)), "work.org")
	var h *org.Headline
	org.Walk(of.Headlines, func(x *org.Headline) {
		if x.Title == title {
			h = x
		}
	})
	op := NewStateOp("work.org", h, keyword, opTime)
	op.Note = note
	if _, err := capturequeue.AddOp(f.phone.OpsPath, op); err != nil {
		f.t.Fatal(err)
	}
	if err := f.phone.Rebuild(); err != nil {
		f.t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(f.phone.Dir, "work.org"))
	return string(out)
}

func TestClosingNoteIsLoggedAtTheTopOfTheBody(t *testing.T) {
	stamp := opTime.Format("2006-01-02 Mon 15:04") // as CLOSED is: the op's own time
	t.Run("after the property drawer", func(t *testing.T) {
		got := newOpsFixture(t).queueClose("Write report", "DONE", "  Sent   it\nto the team ")
		want := "** DONE Write report\n   CLOSED: [" + stamp + "]\n   :PROPERTIES:\n   :CREATED:  [2026-11-01 Sun 10:00]\n   :END:\n" +
			"   - CLOSING NOTE [" + stamp + "] \\\\\n     Sent it to the team\n** TODO Water plants\n"
		if !strings.Contains(got, want) {
			t.Errorf("work.org =\n%s\nwant it to contain\n%s", got, want)
		}
	})
	t.Run("a repeating item logs a state change", func(t *testing.T) {
		got := newOpsFixture(t).queueClose("Water plants", "DONE", "Done for the week")
		want := "   - State \"DONE\"       from \"TODO\"       [" + stamp + "] \\\\\n     Done for the week\n"
		if !strings.Contains(got, want) || !strings.Contains(got, "** TODO Water plants") {
			t.Errorf("work.org =\n%s\nwant it to contain\n%s", got, want)
		}
	})
	t.Run("ignored when not closing", func(t *testing.T) {
		got := newOpsFixture(t).queueClose("Write report", "NEXT", "not a close")
		if strings.Contains(got, "not a close") {
			t.Errorf("a note was logged without a close:\n%s", got)
		}
	})
}
