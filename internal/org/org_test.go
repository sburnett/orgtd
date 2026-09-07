package org

import (
	"strings"
	"testing"
)

const sample = `#+TITLE: Projects

* Someday goal
  Some notes about the goal.
** NEXT Write the design doc
   SCHEDULED: <2026-09-10 Thu>
   :PROPERTIES:
   :ID:       8f3a1e2c-1234
   :CAPTURED_AT: [2026-09-06 Sun 14:32]
   :END:
   Body text for this task.
   Second line of body.
** WAITING Get sign-off       :blocked:review:
   DEADLINE: <2026-09-20 Sun>
* DONE Ship v1
  CLOSED: [2026-09-01 Tue 10:00]
`

func mustParse(t *testing.T) *File {
	t.Helper()
	f, err := Parse(strings.NewReader(sample), "test.org")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return f
}

func TestParseTopLevel(t *testing.T) {
	f := mustParse(t)
	want := []string{"#+TITLE: Projects", ""}
	if len(f.Preamble) != len(want) {
		t.Fatalf("preamble = %#v", f.Preamble)
	}
	for i := range want {
		if f.Preamble[i] != want[i] {
			t.Errorf("preamble[%d] = %q, want %q", i, f.Preamble[i], want[i])
		}
	}
	if len(f.Headlines) != 2 {
		t.Fatalf("got %d top-level headlines, want 2", len(f.Headlines))
	}
	if f.Headlines[0].Title != "Someday goal" {
		t.Errorf("title = %q", f.Headlines[0].Title)
	}
	if len(f.Headlines[0].Children) != 2 {
		t.Fatalf("got %d children, want 2", len(f.Headlines[0].Children))
	}
}

func TestParseKeywordAndScheduled(t *testing.T) {
	f := mustParse(t)
	task := f.Headlines[0].Children[0]
	if task.Keyword != "NEXT" {
		t.Errorf("keyword = %q, want NEXT", task.Keyword)
	}
	if task.Title != "Write the design doc" {
		t.Errorf("title = %q", task.Title)
	}
	if task.Scheduled == nil || task.Scheduled.Raw != "2026-09-10 Thu" || !task.Scheduled.Active {
		t.Errorf("scheduled = %#v", task.Scheduled)
	}
}

func TestParseScheduledAndDeadlineOnSameLine(t *testing.T) {
	f, err := Parse(strings.NewReader("* NEXT Both dates\n  SCHEDULED: <2026-09-10 Thu> DEADLINE: <2026-09-12 Sat>\n"), "test.org")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	h := f.Headlines[0]
	if h.Scheduled == nil || h.Scheduled.Raw != "2026-09-10 Thu" {
		t.Errorf("scheduled = %#v, want 2026-09-10 Thu", h.Scheduled)
	}
	if h.Deadline == nil || h.Deadline.Raw != "2026-09-12 Sat" {
		t.Errorf("deadline = %#v, want 2026-09-12 Sat", h.Deadline)
	}
}

func TestPlanningLineMustStartTheLine(t *testing.T) {
	f, err := Parse(strings.NewReader("* TODO Talk to Bob\n  A note about the DEADLINE: shift, not an actual planning line.\n"), "test.org")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	h := f.Headlines[0]
	if h.Deadline != nil {
		t.Errorf("deadline = %#v, want nil (DEADLINE: appears mid-sentence, not at line start)", h.Deadline)
	}
	if len(h.Body) == 0 || !strings.Contains(h.Body[0], "DEADLINE:") {
		t.Errorf("body = %#v, want the line preserved as ordinary content", h.Body)
	}
}

func TestParseProperties(t *testing.T) {
	f := mustParse(t)
	task := f.Headlines[0].Children[0]
	if got := task.Properties["ID"]; got != "8f3a1e2c-1234" {
		t.Errorf("ID = %q", got)
	}
	if got := task.Properties["CAPTURED_AT"]; got != "[2026-09-06 Sun 14:32]" {
		t.Errorf("CAPTURED_AT = %q", got)
	}
	wantOrder := []string{"ID", "CAPTURED_AT"}
	if len(task.PropertyOrder) != len(wantOrder) {
		t.Fatalf("PropertyOrder = %#v", task.PropertyOrder)
	}
	for i, k := range wantOrder {
		if task.PropertyOrder[i] != k {
			t.Errorf("PropertyOrder[%d] = %q, want %q", i, task.PropertyOrder[i], k)
		}
	}
}

func TestParseBody(t *testing.T) {
	f := mustParse(t)
	task := f.Headlines[0].Children[0]
	want := []string{"   Body text for this task.", "   Second line of body."}
	if len(task.Body) != len(want) {
		t.Fatalf("body = %#v", task.Body)
	}
	for i := range want {
		if task.Body[i] != want[i] {
			t.Errorf("body[%d] = %q, want %q", i, task.Body[i], want[i])
		}
	}
}

func TestParseTags(t *testing.T) {
	f := mustParse(t)
	task := f.Headlines[0].Children[1]
	if task.Keyword != "WAITING" {
		t.Errorf("keyword = %q", task.Keyword)
	}
	if task.Title != "Get sign-off" {
		t.Errorf("title = %q", task.Title)
	}
	want := []string{"blocked", "review"}
	if len(task.Tags) != len(want) {
		t.Fatalf("tags = %#v", task.Tags)
	}
	for i := range want {
		if task.Tags[i] != want[i] {
			t.Errorf("tags[%d] = %q, want %q", i, task.Tags[i], want[i])
		}
	}
	if task.Deadline == nil || task.Deadline.Raw != "2026-09-20 Sun" {
		t.Errorf("deadline = %#v", task.Deadline)
	}
}

func TestParseDoneClosed(t *testing.T) {
	f := mustParse(t)
	done := f.Headlines[1]
	if done.Keyword != "DONE" {
		t.Errorf("keyword = %q", done.Keyword)
	}
	if done.Closed == nil || done.Closed.Active {
		t.Errorf("closed = %#v", done.Closed)
	}
	if done.Closed.Raw != "2026-09-01 Tue 10:00" {
		t.Errorf("closed raw = %q", done.Closed.Raw)
	}
}

func TestWalk(t *testing.T) {
	f := mustParse(t)
	var titles []string
	Walk(f.Headlines, func(h *Headline) { titles = append(titles, h.Title) })
	want := []string{"Someday goal", "Write the design doc", "Get sign-off", "Ship v1"}
	if len(titles) != len(want) {
		t.Fatalf("titles = %#v", titles)
	}
	for i := range want {
		if titles[i] != want[i] {
			t.Errorf("titles[%d] = %q, want %q", i, titles[i], want[i])
		}
	}
}
