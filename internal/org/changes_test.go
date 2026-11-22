package org

import (
	"reflect"
	"strings"
	"testing"
)

func TestDiffEntries(t *testing.T) {
	tests := []struct {
		name     string
		old, new string
		want     []string
	}{
		{"nothing", "* a\n* b\n", "* a\n* b\n", nil},
		{"preamble only", "#+TITLE: x\n* a\n", "#+TITLE: y\n* a\n", nil},
		{"mark done", "* TODO a\n* b\n", "* DONE a\n  CLOSED: [2026-11-21 Sat 22:00]\n* b\n", []string{`mark DONE "a"`}},
		{"unmark", "* TODO a\n", "* a\n", []string{`unmark TODO "a"`}},
		{"add subtree reports its top", "* a\n", "* a\n** b\n*** c\n*** d\n", []string{`add "b"`}},
		{"remove subtree reports its top", "* a\n** b\n*** c\n* d\n", "* a\n* d\n", []string{`remove "b"`}},
		{"edit body", "* a\n  one\n* b\n", "* a\n  two\n* b\n", []string{`edit "a"`}},
		{"edit tags", "* a\n", "* a   :work:\n", []string{`edit "a"`}},
		{"retitle matched by CREATED", "* TODO a\n  :PROPERTIES:\n  :CREATED: [2026-11-21 Sat]\n  :END:\n",
			"* TODO a2\n  :PROPERTIES:\n  :CREATED: [2026-11-21 Sat]\n  :END:\n", []string{`edit "a2"`}},
		{"retitle without an id", "* a\n", "* b\n", []string{`add "b"`, `remove "a"`}},
		{"refile moves only the entry, not its children",
			"* inbox\n** x\n*** y\n* projects\n", "* inbox\n* projects\n** x\n*** y\n", []string{`move "x"`}},
		{"reorder", "* a\n* b\n* c\n", "* c\n* a\n* b\n", []string{`move "c"`}},
		{"insert is not a reorder", "* a\n* c\n", "* a\n* b\n* c\n", []string{`add "b"`}},
		{"duplicate titles", "* x\n* x\n", "* x\n* DONE x\n", []string{`mark DONE "x"`}},
		{"new file", "", "* a\n** b\n* c\n", []string{`add "a"`, `add "c"`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			for _, c := range DiffEntries([]byte(tt.old), []byte(tt.new)) {
				got = append(got, c.String())
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCommitMessage(t *testing.T) {
	one := CommitMessage("orgtd: ", []FileDiff{{Path: "todo.org", Old: []byte("* TODO a\n"), New: []byte("* DONE a\n")}})
	if one != `orgtd: todo.org: mark DONE "a"` {
		t.Errorf("one change = %q", one)
	}

	many := CommitMessage("orgtd: ", []FileDiff{
		{Path: "inbox.org", Old: []byte("* x\n* y\n"), New: []byte("* y\n")},
		{Path: "todo.org", Old: []byte("* TODO a\n* b\n"), New: []byte("* DONE a\n* b\n  note\n* x\n* z\n")},
	})
	want := `orgtd: inbox.org: remove "x"; todo.org: mark 1, add 2, edit 1

inbox.org:
  remove "x"

todo.org:
  mark DONE "a"
  edit "b"
  add "x"
  add "z"
`
	if many != want {
		t.Errorf("many changes =\n%s\nwant\n%s", many, want)
	}

	if got := CommitMessage("orgtd: ", []FileDiff{{Path: "a.org", Old: []byte("x\n* a\n"), New: []byte("y\n* a\n")}}); got != "orgtd: a.org: update" {
		t.Errorf("no entry changes = %q", got)
	}

	long := strings.Repeat("word ", 30)
	got := CommitMessage("orgtd: ", []FileDiff{{Path: "a.org", New: []byte("* " + long + "\n")}})
	if !strings.HasSuffix(got, `…"`) || len(got) > 100 {
		t.Errorf("long title = %q", got)
	}

	var big strings.Builder
	for i := 0; i < maxBodyChanges+5; i++ {
		big.WriteString("* e" + strings.Repeat("x", i) + "\n")
	}
	got = CommitMessage("orgtd: ", []FileDiff{{Path: "a.org", New: []byte(big.String())}})
	if !strings.HasSuffix(got, "… and 5 more\n") {
		t.Errorf("capped body ends %q", got[len(got)-40:])
	}
}
