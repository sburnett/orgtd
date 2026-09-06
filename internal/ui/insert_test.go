package ui

import (
	"errors"
	"strings"
	"testing"

	"github.com/sburnett/orgtd/internal/org"
)

// commitTentative simulates the external editor finishing on the
// tentative headline the cursor is currently on (as left by o/O),
// replacing its content with body. origin is the headline the cursor
// was on before o/O was pressed (used by rollbackInsert if body parses
// to nothing), matching what insertHeadline itself records.
func commitTentative(t *testing.T, m Model, origin *org.Headline, body string) Model {
	t.Helper()
	tentative := m.currentHeadline()
	if tentative == nil {
		t.Fatalf("cursor is not on a headline")
	}
	f, parent, idx := m.insertPosition(tentative)
	ctx := insertContext{f: f, parent: parent, index: idx, origin: origin}
	path := writeTempOrgFile(t, body)
	updated, _ := m.Update(editFinishedMsg{path: path, target: tentative, insert: &ctx})
	return updated.(Model)
}

func TestInsertAfterCreatesSiblingAfter(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	idx := findRow(t, m, "Call the vet about Fido's checkup")
	m.cursor = idx
	orig := m.currentHeadline()

	m = sendKey(m, "o")
	tentative := m.currentHeadline()
	if tentative == orig {
		t.Fatalf("expected cursor to move to a new tentative headline")
	}
	if tentative.Level != orig.Level {
		t.Errorf("tentative level = %d, want %d (a sibling)", tentative.Level, orig.Level)
	}
	if m.cursor != idx+1 {
		t.Fatalf("tentative placed at row %d, want %d (immediately after)", m.cursor, idx+1)
	}

	m = commitTentative(t, m, orig, "* TODO Buy dog treats\n")

	if m.rows[idx].headline != orig {
		t.Errorf("original headline moved unexpectedly")
	}
	if got := m.rows[idx+1].headline; got == nil || got.Title != "Buy dog treats" {
		t.Errorf("row after original = %#v, want the committed 'Buy dog treats' headline", got)
	}
}

func TestInsertBeforeCreatesSiblingBefore(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	orig := m.currentHeadline()

	m = sendKey(m, "O")
	m = commitTentative(t, m, orig, "* TODO Buy dog treats\n")

	newIdx := findRow(t, m, "Buy dog treats")
	origIdx := findRow(t, m, "Call the vet about Fido's checkup")
	if origIdx != newIdx+1 {
		t.Errorf("new headline row = %d, original row = %d; want new immediately before original", newIdx, origIdx)
	}
}

func TestInsertGoesAfterWholeSubtree(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Ship orgtd v0.1") // has children
	orig := m.currentHeadline()
	lastChild := orig.Children[len(orig.Children)-1]
	lastChildIdx := findRow(t, m, lastChild.Title)

	m = sendKey(m, "o")
	tentative := m.currentHeadline()

	if m.cursor != lastChildIdx+1 {
		t.Errorf("tentative row = %d, want %d (right after the last child, not among the children)", m.cursor, lastChildIdx+1)
	}
	if tentative.Level != orig.Level {
		t.Errorf("tentative level = %d, want %d (sibling of the parent, not a child)", tentative.Level, orig.Level)
	}
}

func TestInsertAtTopLevel(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Learn Go generics")
	orig := m.currentHeadline()
	if orig.Parent != nil {
		t.Fatalf("fixture assumption broken: expected a top-level headline")
	}

	m = sendKey(m, "o")
	tentative := m.currentHeadline()
	if tentative.Parent != nil {
		t.Errorf("expected the new headline to be top-level too")
	}
	if tentative.Level != orig.Level {
		t.Errorf("level = %d, want %d", tentative.Level, orig.Level)
	}
}

func TestInsertCommittedAsSingleUndoStep(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	orig := m.currentHeadline()
	before := len(m.rows)

	m = sendKey(m, "o")
	m = commitTentative(t, m, orig, "* TODO Buy dog treats\n")
	if len(m.rows) != before+1 {
		t.Fatalf("rows after insert = %d, want %d", len(m.rows), before+1)
	}

	m = sendKey(m, "u")
	if len(m.rows) != before {
		t.Errorf("rows after a single undo = %d, want %d (removed entirely in one step)", len(m.rows), before)
	}

	m = sendKey(m, "ctrl+r")
	if len(m.rows) != before+1 {
		t.Errorf("rows after redo = %d, want %d", len(m.rows), before+1)
	}
	if h := m.currentHeadline(); h == nil || h.Title != "Buy dog treats" {
		t.Errorf("expected redo to restore and focus the inserted headline, got %v", h)
	}
	if h := m.currentHeadline(); !m.dirtyHeadlines[h] {
		t.Errorf("expected the redone insert to be marked dirty")
	}
}

func TestInsertRollbackOnEmptyResult(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	orig := m.currentHeadline()
	before := len(m.rows)
	beforeUndoPos := m.undoPos

	m = sendKey(m, "o")
	m = commitTentative(t, m, orig, "") // empty file: editor produced nothing

	if len(m.rows) != before {
		t.Errorf("rows after rollback = %d, want %d (no trace left)", len(m.rows), before)
	}
	if m.undoPos != beforeUndoPos {
		t.Errorf("undoPos changed despite rollback: %d, want %d", m.undoPos, beforeUndoPos)
	}
	if !strings.Contains(strings.ToLower(m.message), "cancel") {
		t.Errorf("message = %q, want it to mention the insert was cancelled", m.message)
	}
	if h := m.currentHeadline(); h == nil || h.Title != "Call the vet about Fido's checkup" {
		t.Errorf("expected cursor back on the original headline, got %v", h)
	}
}

func TestInsertRollbackOnEditorError(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	orig := m.currentHeadline()
	before := len(m.rows)
	beforeUndoPos := m.undoPos

	m = sendKey(m, "o")
	tentative := m.currentHeadline()
	f, parent, idx := m.insertPosition(tentative)
	ctx := insertContext{f: f, parent: parent, index: idx, origin: orig}

	updated, _ := m.Update(editFinishedMsg{path: "/nonexistent", target: tentative, insert: &ctx, err: errors.New("boom")})
	m = updated.(Model)

	if len(m.rows) != before {
		t.Errorf("rows after rollback = %d, want %d", len(m.rows), before)
	}
	if m.undoPos != beforeUndoPos {
		t.Errorf("undoPos changed despite rollback: %d, want %d", m.undoPos, beforeUndoPos)
	}
}

func TestInsertNoopOnFileRow(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = 0
	if m.rows[0].file == nil {
		t.Fatalf("fixture assumption broken: row 0 is not a file row")
	}
	before := len(m.rows)

	_, cmd := sendKeyCmd(m, "o")
	if cmd != nil {
		t.Errorf("expected no command when inserting on a file row")
	}
	m2 := sendKey(m, "O")
	if len(m2.rows) != before {
		t.Errorf("rows changed after O on a file row")
	}
}

func TestUndoTopLevelInsertFocusesFileRow(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Learn Go generics")
	orig := m.currentHeadline()
	if orig.Parent != nil {
		t.Fatalf("fixture assumption broken: expected a top-level headline")
	}

	m = sendKey(m, "o")
	m = commitTentative(t, m, orig, "* New project\n")

	m = sendKey(m, "u")
	if m.rows[m.cursor].file == nil {
		t.Errorf("expected cursor on a file row after undoing a top-level insert, got %#v", m.rows[m.cursor])
	}
}
