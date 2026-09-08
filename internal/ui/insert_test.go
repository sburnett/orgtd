package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

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

func TestInsertPrefillsCreatedProperty(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")

	before := time.Now()
	m = sendKey(m, "o")
	after := time.Now()

	tentative := m.currentHeadline()
	created, ok := tentative.Properties["CREATED"]
	if !ok {
		t.Fatalf("tentative headline has no CREATED property: %#v", tentative.Properties)
	}

	raw := strings.TrimSuffix(strings.TrimPrefix(created, "["), "]")
	got, err := time.ParseInLocation("2006-01-02 Mon 15:04", raw, time.Local)
	if err != nil {
		t.Fatalf("CREATED = %q, not a parseable inactive timestamp: %v", created, err)
	}
	// Minute-granularity, so allow a one-minute window on either side of
	// the actual call rather than comparing to the second.
	if got.Before(before.Add(-time.Minute)) || got.After(after.Add(time.Minute)) {
		t.Errorf("CREATED = %v, want close to now (%v)", got, before)
	}
}

// TestInsertCreatedPropertyIsInTheActualTemplate exercises the same
// org.RenderHeadline call launchEditor uses to build the editor buffer,
// confirming CREATED is actually part of what the user sees and can
// edit — not just set on the in-memory struct without reaching the
// template.
func TestInsertCreatedPropertyIsInTheActualTemplate(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")

	m = sendKey(m, "o")
	tentative := m.currentHeadline()

	rendered := org.RenderHeadline(tentative)
	if !strings.Contains(rendered, ":CREATED:") {
		t.Errorf("rendered template = %q, missing :CREATED:", rendered)
	}
}

func TestInsertCreatedPropertyCanBeOverriddenOrRemoved(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	orig := m.currentHeadline()

	m = sendKey(m, "o")
	// The user edits the template down to something with no CREATED at
	// all (or a different one) before saving — nothing should force it
	// back in afterward.
	m = commitTentative(t, m, orig, "* TODO Buy dog treats\n")

	committed := m.rows[findRow(t, m, "Buy dog treats")].headline
	if _, exists := committed.Properties["CREATED"]; exists {
		t.Errorf("CREATED = %q, want absent (user's edited content had none)", committed.Properties["CREATED"])
	}
}

func TestEditingExistingHeadlineDoesNotAddCreatedProperty(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	h := m.currentHeadline()
	if _, exists := h.Properties["CREATED"]; exists {
		t.Fatalf("fixture assumption broken: expected no CREATED property yet")
	}

	m = sendKey(m, "i")
	if _, exists := h.Properties["CREATED"]; exists {
		t.Errorf("plain i-edit of an existing headline gained a CREATED property; it should only be prefilled for a new (o/O) entry")
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

func TestIsBlankHeadlineTitle(t *testing.T) {
	cases := []struct {
		title string
		want  bool
	}{
		{"", true},
		{"   ", true},
		{"-", true},
		{"*", true},
		{"+", true},
		{"•", true},
		{"- ", true},
		{"-*-", true},
		{"- - ", true},
		{"**", true},
		{"Buy dog treats", false},
		{"-1 lap penalty", false},
		{"Learn C++", false},
		{"- Buy dog treats", false}, // real text after the bullet
		{"* not actually blank", false},
	}
	for _, c := range cases {
		if got := isBlankHeadlineTitle(c.title); got != c.want {
			t.Errorf("isBlankHeadlineTitle(%q) = %v, want %v", c.title, got, c.want)
		}
	}
}

func TestInsertRollbackOnBlankTitle(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"stars and keyword only", "* TODO\n"},
		{"stars and whitespace only", "*    \n"},
		{"lone dash bullet", "* -\n"},
		{"lone asterisk", "* *\n"},
		{"keyword plus bullet", "* TODO -\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ws := loadFixture(t)
			m := New(ws)
			m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
			orig := m.currentHeadline()
			before := len(m.rows)
			beforeUndoPos := m.undoPos

			m = sendKey(m, "o")
			m = commitTentative(t, m, orig, c.body)

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
		})
	}
}

func TestInsertNotRollbackWhenTitleStartsWithBulletButHasRealText(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	orig := m.currentHeadline()

	m = sendKey(m, "o")
	m = commitTentative(t, m, orig, "* TODO - Buy dog treats\n")

	idx := findRow(t, m, "- Buy dog treats")
	if m.rows[idx].headline.Title != "- Buy dog treats" {
		t.Errorf("expected the entry to be committed, not rolled back")
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

func TestInsertAtEndOfFile(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	fileIdx := findFileRow(t, m, "inbox.org")
	m.cursor = fileIdx
	f := m.ws.Files[0] // inbox.org, sorted first
	lastTitle := f.Headlines[len(f.Headlines)-1].Title
	lastIdx := findRow(t, m, lastTitle)

	m = sendKey(m, "o")
	tentative := m.currentHeadline()
	if tentative == nil {
		t.Fatalf("expected cursor to move to a new tentative headline")
	}
	if tentative.Level != 1 || tentative.Parent != nil {
		t.Errorf("tentative = level %d parent %v, want a top-level (level 1, nil parent) headline", tentative.Level, tentative.Parent)
	}
	if m.cursor != lastIdx+1 {
		t.Errorf("tentative row = %d, want %d (right after the last existing top-level headline)", m.cursor, lastIdx+1)
	}

	m = commitTentative(t, m, nil, "* TODO New inbox item\n")
	if got := m.rows[lastIdx+1].headline; got == nil || got.Title != "New inbox item" {
		t.Errorf("row after the last existing item = %#v, want the new committed headline", got)
	}
}

func TestInsertAtBeginningOfFile(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	fileIdx := findFileRow(t, m, "inbox.org")
	m.cursor = fileIdx

	m = sendKey(m, "O")
	tentative := m.currentHeadline()
	if tentative == nil || tentative.Level != 1 || tentative.Parent != nil {
		t.Fatalf("expected a top-level tentative headline, got %v", tentative)
	}
	if m.cursor != fileIdx+1 {
		t.Errorf("tentative row = %d, want %d (right after the file header)", m.cursor, fileIdx+1)
	}

	m = commitTentative(t, m, nil, "* TODO New inbox item\n")

	newIdx := findRow(t, m, "New inbox item")
	if newIdx != fileIdx+1 {
		t.Errorf("new headline row = %d, want %d (first item in the file)", newIdx, fileIdx+1)
	}
	if origIdx := findRow(t, m, "Call the vet about Fido's checkup"); origIdx != newIdx+1 {
		t.Errorf("original first item now at row %d, want %d (right after the new one)", origIdx, newIdx+1)
	}
}

func TestInsertOnFileRowRollbackFocusesFileRow(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	fileIdx := findFileRow(t, m, "inbox.org")
	m.cursor = fileIdx
	before := len(m.rows)

	m = sendKey(m, "o")
	m = commitTentative(t, m, nil, "") // empty result -> rollback

	if len(m.rows) != before {
		t.Errorf("rows after rollback = %d, want %d", len(m.rows), before)
	}
	if m.cursor != fileIdx {
		t.Errorf("cursor after rollback = %d, want %d (back on the file row)", m.cursor, fileIdx)
	}
}

func TestUndoTopLevelInsertFocusesPreviousSibling(t *testing.T) {
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
	// Matches vim: undoing an insert lands on whatever's now adjacent,
	// not some unrelated file marker.
	if h := m.currentHeadline(); h != orig {
		t.Errorf("expected cursor back on %q after undo, got %v", orig.Title, h)
	}
}

func TestUndoInsertOnEmptiedFileFocusesFileRow(t *testing.T) {
	ws := loadFixtureCopy(t) // a scratch copy, since this file ends up empty
	m := New(ws)
	fileIdx := findFileRow(t, m, "inbox.org")

	// Delete every existing top-level headline in inbox.org so the file
	// is empty, then insert+undo the one remaining case: nothing left to
	// focus but the file row.
	for {
		row := m.rows[fileIdx+1]
		if row.file != nil {
			break
		}
		m.cursor = fileIdx + 1
		m = sendKey(m, "d")
		m = sendKey(m, "d")
	}

	m.cursor = fileIdx
	m = sendKey(m, "o")
	m = commitTentative(t, m, nil, "* Only item\n")

	m = sendKey(m, "u")
	if m.rows[m.cursor].file == nil {
		t.Errorf("expected cursor on the file row once the file is empty again, got %#v", m.rows[m.cursor])
	}
}
