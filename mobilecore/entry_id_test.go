package mobilecore

import (
	"slices"
	"testing"
)

func TestParseEntryIDRoundTrips(t *testing.T) {
	for _, tc := range []struct {
		in   string
		file string
		path []int
	}{
		{"todo.org:0", "todo.org", []int{0}},
		{"todo.org:3.0.12", "todo.org", []int{3, 0, 12}},
		{"reference/books.org:1.2", "reference/books.org", []int{1, 2}},
		// The file is everything before the last ":", as Swift splits it.
		{"odd:name.org:4", "odd:name.org", []int{4}},
	} {
		id, err := parseEntryID(tc.in)
		if err != nil {
			t.Errorf("parseEntryID(%q): %v", tc.in, err)
			continue
		}
		if id.file != tc.file || !slices.Equal(id.path, tc.path) {
			t.Errorf("parseEntryID(%q) = %q %v, want %q %v", tc.in, id.file, id.path, tc.file, tc.path)
		}
		if got := id.String(); got != tc.in {
			t.Errorf("parseEntryID(%q).String() = %q", tc.in, got)
		}
	}
}

func TestParseEntryIDRejectsMalformedIDs(t *testing.T) {
	for _, in := range []string{
		"", "todo.org", "todo.org:", "todo.org:1.", "todo.org:.1", "todo.org:1..2",
		"todo.org:-1", "todo.org:x", "todo.org:1.x", "todo.org:+1", "todo.org:01", "todo.org: 1",
	} {
		if id, err := parseEntryID(in); err == nil {
			t.Errorf("parseEntryID(%q) = %v, want an error", in, id)
		}
	}
}

func TestEntryIDNavigation(t *testing.T) {
	id, err := parseEntryID("todo.org:2.5")
	if err != nil {
		t.Fatal(err)
	}
	if got := id.index(); got != 5 {
		t.Errorf("index() = %d, want 5", got)
	}
	p, ok := id.parent()
	if !ok || p.String() != "todo.org:2" {
		t.Errorf("parent() = %v, %v, want todo.org:2", p, ok)
	}
	if _, ok := p.parent(); ok {
		t.Error("a top-level entry has a parent")
	}
	if got := id.sibling(7).String(); got != "todo.org:2.7" {
		t.Errorf("sibling(7) = %q", got)
	}
	if got := id.child(0).String(); got != "todo.org:2.5.0" {
		t.Errorf("child(0) = %q", got)
	}
	if got := topLevelID("inbox.org", 3).String(); got != "inbox.org:3" {
		t.Errorf("topLevelID = %q", got)
	}
	// Deriving IDs never writes through to the original's path.
	_ = p.child(9)
	_ = id.sibling(0)
	if id.String() != "todo.org:2.5" {
		t.Errorf("deriving IDs changed the original to %q", id)
	}
}

func TestEntryIDIsSiblingOf(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"todo.org:1.2", "todo.org:1.7", true},
		{"todo.org:1", "todo.org:4", true},
		{"todo.org:1.2", "todo.org:2.2", false},
		{"todo.org:1.2", "todo.org:1", false},
		{"todo.org:1", "inbox.org:1", false},
	} {
		a, _ := parseEntryID(tc.a)
		b, _ := parseEntryID(tc.b)
		if got := a.isSiblingOf(b); got != tc.want {
			t.Errorf("%s.isSiblingOf(%s) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}
