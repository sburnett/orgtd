package org

import (
	"strings"
	"testing"
)

// treeFixture parses a small outline:
//
//   - A
//     ** A1
//     ** A2
//   - B
//   - C
func treeFixture(t *testing.T) *File {
	t.Helper()
	f, err := Parse(strings.NewReader("* A\n** A1\n** A2\n* B\n* C\n"), "t.org")
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func titles(hs []*Headline) string {
	var out []string
	for _, h := range hs {
		out = append(out, h.Title)
	}
	return strings.Join(out, ",")
}

func TestSiblingsListsParentChildrenOrFileTopLevel(t *testing.T) {
	f := treeFixture(t)
	a := f.Headlines[0]
	if got := titles(Siblings{File: f}.List()); got != "A,B,C" {
		t.Errorf("top-level List = %q, want A,B,C", got)
	}
	if got := titles(Siblings{File: f, Parent: a}.List()); got != "A1,A2" {
		t.Errorf("children List = %q, want A1,A2", got)
	}
	// File is optional once Parent addresses the list.
	if got := titles(Siblings{Parent: a}.List()); got != "A1,A2" {
		t.Errorf("List without a File = %q, want A1,A2", got)
	}
	if (Siblings{}).List() != nil {
		t.Error("zero Siblings should be an empty list")
	}
}

func TestSiblingsIndexOf(t *testing.T) {
	f := treeFixture(t)
	a, b := f.Headlines[0], f.Headlines[1]
	top := Siblings{File: f}
	if top.IndexOf(a) != 0 || top.IndexOf(b) != 1 {
		t.Errorf("IndexOf = %d, %d; want 0, 1", top.IndexOf(a), top.IndexOf(b))
	}
	if top.IndexOf(a.Children[0]) != -1 {
		t.Error("IndexOf found a nested headline in the top-level list")
	}
}

func TestSpliceBuildsANewSliceAndLeavesOldOneAlone(t *testing.T) {
	f := treeFixture(t)
	old := f.Headlines
	x := &Headline{Title: "X"}
	Siblings{File: f}.Splice(1, 1, []*Headline{x, {Title: "Y"}})
	if got := titles(f.Headlines); got != "A,X,Y,C" {
		t.Errorf("after Splice = %q, want A,X,Y,C", got)
	}
	if got := titles(old); got != "A,B,C" {
		t.Errorf("the previous slice changed to %q; Splice must not edit in place (undo records hold it)", got)
	}
	Siblings{File: f}.Splice(0, 4, nil)
	if len(f.Headlines) != 0 {
		t.Errorf("removing everything left %q", titles(f.Headlines))
	}
}

func TestSpliceIgnoresASiblingsWithNoListToEdit(t *testing.T) {
	Siblings{}.Splice(0, 0, []*Headline{{Title: "X"}}) // must not panic
}

func TestNearestTo(t *testing.T) {
	f := treeFixture(t)
	s := Siblings{File: f}
	if s.NearestTo(1).Title != "B" {
		t.Error("NearestTo(1) should be B, the headline now at that index")
	}
	if s.NearestTo(3).Title != "C" {
		t.Error("NearestTo past the end should fall back to the last headline")
	}
	if s.NearestTo(10) != nil {
		t.Error("NearestTo far past the end should be nil")
	}
	if (Siblings{File: &File{}}).NearestTo(0) != nil {
		t.Error("NearestTo on an empty list should be nil")
	}
}

func TestNeighbors(t *testing.T) {
	f := treeFixture(t)
	s := Siblings{File: f}
	prev, next, earlier := s.Neighbors(0)
	if prev != nil || next.Title != "B" || earlier != 0 {
		t.Errorf("Neighbors(0) = %v %v %d", prev, next, earlier)
	}
	prev, next, earlier = s.Neighbors(2)
	if prev.Title != "B" || next != nil || earlier != 1 {
		t.Errorf("Neighbors(2) = %v %v %d, want prev B, no next, 1 earlier (A)", prev, next, earlier)
	}
}

func TestMoveReparentsShiftsLevelsAndKeepsTheTreeConsistent(t *testing.T) {
	f := treeFixture(t)
	a, b := f.Headlines[0], f.Headlines[1]
	b.Children = []*Headline{{Level: 2, Title: "B1", Parent: b}}

	// Demote B under A (as ">>" does): last child of A, one level deeper.
	if !f.Move(b, a, len(a.Children), 1) {
		t.Fatal("Move reported B not found")
	}
	if got := titles(f.Headlines); got != "A,C" {
		t.Errorf("top level = %q, want A,C", got)
	}
	if got := titles(a.Children); got != "A1,A2,B" {
		t.Errorf("A's children = %q, want A1,A2,B", got)
	}
	if b.Parent != a || b.Level != 2 || b.Children[0].Level != 3 {
		t.Errorf("B parent/level = %v/%d, B1 level = %d; want A/2/3", b.Parent, b.Level, b.Children[0].Level)
	}

	// And back out (as "<<" does): top level again, after A.
	if !f.Move(b, nil, 1, -1) {
		t.Fatal("Move back reported B not found")
	}
	if got := titles(f.Headlines); got != "A,B,C" || b.Parent != nil || b.Level != 1 || b.Children[0].Level != 2 {
		t.Errorf("after moving back: top level %q, B parent %v level %d, B1 level %d", got, b.Parent, b.Level, b.Children[0].Level)
	}
}

func TestMoveOfAHeadlineNotInTheFileChangesNothing(t *testing.T) {
	f := treeFixture(t)
	stray := &Headline{Level: 1, Title: "Stray"}
	if f.Move(stray, nil, 0, 0) {
		t.Error("Move of a headline that isn't in the file reported success")
	}
	if got := titles(f.Headlines); got != "A,B,C" {
		t.Errorf("top level = %q, want it unchanged", got)
	}
}

func TestShiftLevelShiftsTheWholeSubtree(t *testing.T) {
	f := treeFixture(t)
	a := f.Headlines[0]
	a.ShiftLevel(2)
	if a.Level != 3 || a.Children[0].Level != 4 || a.Children[1].Level != 4 {
		t.Errorf("levels = %d, %d, %d; want 3, 4, 4", a.Level, a.Children[0].Level, a.Children[1].Level)
	}
	a.ShiftLevel(0) // a no-op
	if a.Level != 3 {
		t.Errorf("ShiftLevel(0) changed the level to %d", a.Level)
	}
}

func TestTopmostDropsDescendantsOfOtherSelectedHeadlines(t *testing.T) {
	f := treeFixture(t)
	a, b := f.Headlines[0], f.Headlines[1]
	a1 := a.Children[0]
	got := Topmost([]*Headline{a, a1, b})
	if titles(got) != "A,B" {
		t.Errorf("Topmost = %q, want A,B (A1 is covered by its selected parent A)", titles(got))
	}
	// A selected child whose parent isn't selected stays.
	if got := Topmost([]*Headline{a1, b}); titles(got) != "A1,B" {
		t.Errorf("Topmost = %q, want A1,B", titles(got))
	}
	if Topmost(nil) != nil {
		t.Error("Topmost(nil) != nil")
	}
}

func TestSetOrDeletePropertyAndRestore(t *testing.T) {
	h := &Headline{}
	h.SetOrDeleteProperty("K", "v")
	if h.Properties["K"] != "v" || len(h.PropertyOrder) != 1 {
		t.Fatalf("SetOrDeleteProperty(K, v) -> %v / %v", h.Properties, h.PropertyOrder)
	}
	h.SetOrDeleteProperty("K", "")
	if _, has := h.Properties["K"]; has || len(h.PropertyOrder) != 0 {
		t.Errorf("an empty value should delete the property: %v / %v", h.Properties, h.PropertyOrder)
	}

	h.RestoreProperty("K", true, "old")
	if h.Properties["K"] != "old" {
		t.Errorf("RestoreProperty(had=true) = %q, want old", h.Properties["K"])
	}
	h.RestoreProperty("K", false, "ignored")
	if _, has := h.Properties["K"]; has {
		t.Error("RestoreProperty(had=false) should remove the property")
	}
}

func TestTrimmedBodyDropsOnlyTrailingBlankLines(t *testing.T) {
	h := &Headline{Body: []string{"first", "", "second", "  ", ""}}
	got := h.TrimmedBody()
	if len(got) != 3 || got[2] != "second" {
		t.Errorf("TrimmedBody = %q, want first, blank, second (inner blank kept)", got)
	}
	if got := (&Headline{Body: []string{"", " "}}).TrimmedBody(); len(got) != 0 {
		t.Errorf("an all-blank body trimmed to %q, want nothing", got)
	}
	if got := (&Headline{}).TrimmedBody(); len(got) != 0 {
		t.Errorf("no body trimmed to %q", got)
	}
}
