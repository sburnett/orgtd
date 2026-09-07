package ui

import "testing"

func TestDeleteRemovesEntryAndSubtree(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	idx := findRow(t, m, "Ship orgtd v0.1") // has 4 children
	m.cursor = idx
	before := len(m.rows)
	removed := subtreeRowCount(m.currentHeadline())

	m = sendKey(m, "d")
	m = sendKey(m, "d")

	if len(m.rows) != before-removed {
		t.Fatalf("rows after dd = %d, want %d (entry, its children, and any body lines gone)", len(m.rows), before-removed)
	}
	if h := m.rows[idx].headline; h == nil || h.Title == "Ship orgtd v0.1" {
		t.Errorf("row %d still shows the deleted entry: %#v", idx, m.rows[idx])
	}
}

func TestDeleteIsSingleD(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	before := len(m.rows)

	m = sendKey(m, "d")
	if len(m.rows) != before {
		t.Fatalf("a single d deleted something: rows = %d, want %d", len(m.rows), before)
	}
	// An unrelated key cancels the pending d.
	m = sendKey(m, "j")
	m = sendKey(m, "d")
	if len(m.rows) != before {
		t.Errorf("d after an intervening key still deleted: rows = %d, want %d", len(m.rows), before)
	}
}

func TestDeleteNoopOnFileRow(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	fileIdx := findFileRow(t, m, "inbox.org")
	m.cursor = fileIdx
	before := len(m.rows)

	m = sendKey(m, "d")
	m = sendKey(m, "d")
	if len(m.rows) != before {
		t.Errorf("dd on a file row changed row count: %d, want %d", len(m.rows), before)
	}
}

func TestDeleteIsOneUndoStep(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	idx := findRow(t, m, "Call the vet about Fido's checkup")
	m.cursor = idx
	before := len(m.rows)

	m = sendKey(m, "d")
	m = sendKey(m, "d")
	if len(m.rows) != before-1 {
		t.Fatalf("rows after dd = %d, want %d", len(m.rows), before-1)
	}

	m = sendKey(m, "u")
	if len(m.rows) != before {
		t.Errorf("rows after undo = %d, want %d (fully restored in one step)", len(m.rows), before)
	}
	if h := m.currentHeadline(); h == nil || h.Title != "Call the vet about Fido's checkup" {
		t.Errorf("expected undo to restore and focus the deleted headline, got %v", h)
	}

	m = sendKey(m, "ctrl+r")
	if len(m.rows) != before-1 {
		t.Errorf("rows after redo = %d, want %d", len(m.rows), before-1)
	}
}

func TestDeleteFocusesNextSiblingThenPrevious(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	// Middle sibling: deleting it should focus the next one.
	m.cursor = findRow(t, m, "Read the RFC linked in yesterday's design review")
	m = sendKey(m, "d")
	m = sendKey(m, "d")
	if h := m.currentHeadline(); h == nil || h.Title != "Follow up with finance about the Q3 budget doc" {
		t.Errorf("after deleting the middle item, focus = %v, want the next sibling", h)
	}

	// Now the last remaining item: deleting it should focus the previous one.
	m = sendKey(m, "d")
	m = sendKey(m, "d")
	if h := m.currentHeadline(); h == nil || h.Title != "Call the vet about Fido's checkup" {
		t.Errorf("after deleting the last item, focus = %v, want the previous sibling", h)
	}
}

func TestPasteAfterAndBefore(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	deleted := m.currentHeadline()
	m = sendKey(m, "d")
	m = sendKey(m, "d")

	m.cursor = findRow(t, m, "Follow up with finance about the Q3 budget doc")
	before := len(m.rows)

	m = sendKey(m, "p") // paste after
	pasted := m.currentHeadline()
	if pasted == deleted {
		t.Fatalf("pasted headline is the same pointer as the deleted one; register was not cloned")
	}
	if pasted.Title != deleted.Title {
		t.Errorf("pasted title = %q, want %q", pasted.Title, deleted.Title)
	}
	if len(m.rows) != before+1 {
		t.Fatalf("rows after p = %d, want %d", len(m.rows), before+1)
	}

	// Paste again (P, before current) — register isn't consumed by p.
	beforeIdx := findRow(t, m, "Follow up with finance about the Q3 budget doc")
	m.cursor = beforeIdx
	m = sendKey(m, "P")
	if got := m.currentHeadline(); got == pasted || got.Title != deleted.Title {
		t.Errorf("second paste = %v, want a fresh independent copy of %q", got, deleted.Title)
	}
	pastedIdx := m.cursor
	if pastedIdx != beforeIdx {
		t.Errorf("P placed the paste at row %d, want %d (immediately before)", pastedIdx, beforeIdx)
	}
}

func TestPasteWithNothingDeletedShowsMessage(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")

	m = sendKey(m, "p")
	if m.message != "Nothing to paste" {
		t.Errorf("message = %q, want %q", m.message, "Nothing to paste")
	}
}

func TestPasteIsUndoable(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	m = sendKey(m, "d")
	m = sendKey(m, "d")

	m.cursor = findRow(t, m, "Follow up with finance about the Q3 budget doc")
	before := len(m.rows)
	m = sendKey(m, "p")
	if len(m.rows) != before+1 {
		t.Fatalf("rows after paste = %d, want %d", len(m.rows), before+1)
	}

	m = sendKey(m, "u")
	if len(m.rows) != before {
		t.Errorf("rows after undoing the paste = %d, want %d", len(m.rows), before)
	}
}

func TestPasteAdjustsLevelToDestination(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	// Delete a level-2 task (a child of "Ship orgtd v0.1").
	m.cursor = findRow(t, m, "Write the design document")
	deleted := m.currentHeadline()
	if deleted.Level != 2 {
		t.Fatalf("fixture assumption broken: expected level 2, got %d", deleted.Level)
	}
	m = sendKey(m, "d")
	m = sendKey(m, "d")

	// Paste it at the top level (after a top-level project).
	m.cursor = findRow(t, m, "Ship orgtd v0.1")
	m = sendKey(m, "p")

	pasted := m.currentHeadline()
	if pasted.Level != 1 {
		t.Errorf("pasted level = %d, want 1 (adapted to the top-level destination)", pasted.Level)
	}
	if pasted.Parent != nil {
		t.Errorf("pasted headline has a parent, want top-level (nil)")
	}
}

func TestPasteOnFileRowGoesToEndOrBeginning(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	m = sendKey(m, "d")
	m = sendKey(m, "d")

	fileIdx := findFileRow(t, m, "projects.org")
	m.cursor = fileIdx
	m = sendKey(m, "P") // paste at the beginning of projects.org

	if m.cursor != fileIdx+1 {
		t.Errorf("paste row = %d, want %d (right after the file header)", m.cursor, fileIdx+1)
	}
	if h := m.currentHeadline(); h == nil || h.Level != 1 {
		t.Errorf("pasted headline = %v, want a top-level entry", h)
	}
}
