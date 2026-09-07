package ui

import "testing"

func TestDemoteBecomesChildOfPreviousSibling(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Implement the org file parser")
	h := m.currentHeadline()
	if h.Level != 2 {
		t.Fatalf("fixture assumption broken: expected level 2, got %d", h.Level)
	}

	m = sendKey(m, ">")
	m = sendKey(m, ">")

	if h.Level != 3 {
		t.Errorf("level after demote = %d, want 3", h.Level)
	}
	if h.Parent == nil || h.Parent.Title != "Write the design document" {
		t.Errorf("parent after demote = %v, want 'Write the design document'", h.Parent)
	}
	// It should be the LAST child of its new parent.
	newParent := h.Parent
	if got := newParent.Children[len(newParent.Children)-1]; got != h {
		t.Errorf("h is not the last child of its new parent")
	}
}

func TestDemoteRejectedWithoutPreviousSibling(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Write the design document") // first child of "Ship orgtd v0.1"
	h := m.currentHeadline()
	origLevel, origParent := h.Level, h.Parent

	m = sendKey(m, ">")
	m = sendKey(m, ">")

	if h.Level != origLevel || h.Parent != origParent {
		t.Errorf("demote changed a headline with no previous sibling: level=%d parent=%v", h.Level, h.Parent)
	}
	if m.message == "" {
		t.Errorf("expected a message explaining why demote was refused")
	}
}

func TestPromoteBecomesNextSiblingOfOldParent(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Implement the org file parser")
	h := m.currentHeadline()
	oldParent := h.Parent
	if oldParent == nil || oldParent.Title != "Ship orgtd v0.1" {
		t.Fatalf("fixture assumption broken: expected parent 'Ship orgtd v0.1', got %v", oldParent)
	}

	m = sendKey(m, "<")
	m = sendKey(m, "<")

	if h.Level != 1 {
		t.Errorf("level after promote = %d, want 1", h.Level)
	}
	if h.Parent != nil {
		t.Errorf("parent after promote = %v, want nil (top-level)", h.Parent)
	}
	// It should be the top-level entry immediately after its old parent
	// structurally (not necessarily the next visible row — the old
	// parent's remaining children still render in between).
	f := m.fileForHeadline(h)
	oldParentIdx := -1
	for i, top := range f.Headlines {
		if top == oldParent {
			oldParentIdx = i
			break
		}
	}
	if oldParentIdx < 0 {
		t.Fatalf("old parent not found among top-level headlines")
	}
	if got := f.Headlines[oldParentIdx+1]; got != h {
		t.Errorf("top-level headline after old parent = %v, want the promoted headline", got)
	}
}

func TestPromoteRejectedAtTopLevel(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Ship orgtd v0.1")
	h := m.currentHeadline()
	origLevel := h.Level

	m = sendKey(m, "<")
	m = sendKey(m, "<")

	if h.Level != origLevel || h.Parent != nil {
		t.Errorf("promote changed an already-top-level headline: level=%d parent=%v", h.Level, h.Parent)
	}
	if m.message == "" {
		t.Errorf("expected a message explaining why promote was refused")
	}
}

func TestDemoteShiftsChildrenLevelsToo(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Quarterly planning") // top-level, has 3 children
	h := m.currentHeadline()
	if len(h.Children) == 0 {
		t.Fatalf("fixture assumption broken: expected children")
	}
	childLevels := make([]int, len(h.Children))
	for i, c := range h.Children {
		childLevels[i] = c.Level
	}

	m = sendKey(m, ">")
	m = sendKey(m, ">")

	if h.Level != 2 {
		t.Errorf("level after demote = %d, want 2", h.Level)
	}
	for i, c := range h.Children {
		if c.Level != childLevels[i]+1 {
			t.Errorf("child %d level = %d, want %d", i, c.Level, childLevels[i]+1)
		}
	}
}

func TestDemoteThenPromoteRoundTrips(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	idx := findRow(t, m, "Quarterly planning")
	m.cursor = idx
	h := m.currentHeadline()
	origLevel := h.Level
	childLevels := make([]int, len(h.Children))
	for i, c := range h.Children {
		childLevels[i] = c.Level
	}

	m = sendKey(m, ">")
	m = sendKey(m, ">")
	m = sendKey(m, "<")
	m = sendKey(m, "<")

	if h.Level != origLevel {
		t.Errorf("level after demote+promote = %d, want %d", h.Level, origLevel)
	}
	if h.Parent != nil {
		t.Errorf("parent after demote+promote = %v, want nil", h.Parent)
	}
	for i, c := range h.Children {
		if c.Level != childLevels[i] {
			t.Errorf("child %d level after round trip = %d, want %d", i, c.Level, childLevels[i])
		}
	}
	if got := findRow(t, m, "Quarterly planning"); got != idx {
		t.Errorf("row after round trip = %d, want back at %d", got, idx)
	}
}

func TestDemotePromoteAreOneUndoStepEach(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Implement the org file parser")
	h := m.currentHeadline()
	origLevel, origParent := h.Level, h.Parent

	m = sendKey(m, ">")
	m = sendKey(m, ">")
	if h.Level == origLevel {
		t.Fatalf("demote did not apply")
	}

	m = sendKey(m, "u")
	if h.Level != origLevel || h.Parent != origParent {
		t.Errorf("undo did not fully revert demote in one step: level=%d parent=%v", h.Level, h.Parent)
	}

	m = sendKey(m, "ctrl+r")
	if h.Level != origLevel+1 {
		t.Errorf("redo did not reapply demote: level=%d", h.Level)
	}
}

func TestDemoteNoopOnFileRow(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = 0
	if m.rows[0].file == nil {
		t.Fatalf("fixture assumption broken: row 0 is not a file row")
	}
	before := len(m.rows)

	m = sendKey(m, ">")
	m = sendKey(m, ">")
	m = sendKey(m, "<")
	m = sendKey(m, "<")

	if len(m.rows) != before || m.cursor != 0 {
		t.Errorf("demote/promote on a file row changed state")
	}
}

func TestSingleAngleBracketIsPending(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Implement the org file parser")
	h := m.currentHeadline()
	origLevel := h.Level

	m = sendKey(m, ">")
	if h.Level != origLevel {
		t.Errorf("a single > already demoted the headline")
	}
	if !m.pendingGT {
		t.Errorf("expected pendingGT after a single >")
	}
}
