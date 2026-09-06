package ui

import (
	"strings"
	"testing"

	"github.com/sburnett/orgtd/internal/org"
)

func TestUndoRedoStatusChange(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	h := m.currentHeadline()
	if h.Keyword != "TODO" {
		t.Fatalf("fixture assumption broken: keyword = %q, want TODO", h.Keyword)
	}

	m = sendKey(m, "r") // TODO -> NEXT
	if h.Keyword != "NEXT" {
		t.Fatalf("keyword after r = %q, want NEXT", h.Keyword)
	}

	m = sendKey(m, "u")
	if h.Keyword != "TODO" {
		t.Errorf("keyword after u = %q, want TODO", h.Keyword)
	}
	if m.dirtyHeadlines[h] {
		t.Errorf("expected no dirty marker after undoing back to the original (never-saved) state")
	}

	m = sendKey(m, "ctrl+r")
	if h.Keyword != "NEXT" {
		t.Errorf("keyword after redo = %q, want NEXT", h.Keyword)
	}
	if !m.dirtyHeadlines[h] {
		t.Errorf("expected a dirty marker after redo re-applied the change")
	}
}

func TestUndoRestoresClosedTimestamp(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	h := m.currentHeadline()

	// TODO -> NEXT -> WAITING -> SOMEDAY -> DONE (stamps CLOSED)
	for i := 0; i < 4; i++ {
		m = sendKey(m, "r")
	}
	if h.Keyword != "DONE" || h.Closed == nil {
		t.Fatalf("fixture assumption broken: keyword=%q closed=%v", h.Keyword, h.Closed)
	}

	m = sendKey(m, "u")
	if h.Keyword != "SOMEDAY" {
		t.Errorf("keyword after undo = %q, want SOMEDAY", h.Keyword)
	}
	if h.Closed != nil {
		t.Errorf("CLOSED should be cleared again after undoing the DONE transition, got %v", h.Closed)
	}

	m = sendKey(m, "ctrl+r")
	if h.Keyword != "DONE" || h.Closed == nil {
		t.Errorf("redo should restore DONE with CLOSED stamped: keyword=%q closed=%v", h.Keyword, h.Closed)
	}
}

func TestUndoAtOldestChangeShowsMessage(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m = sendKey(m, "u")
	if m.message != "Already at oldest change" {
		t.Errorf("message = %q, want %q", m.message, "Already at oldest change")
	}
}

func TestRedoAtNewestChangeShowsMessage(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	m = sendKey(m, "r")
	m = sendKey(m, "ctrl+r")
	if m.message != "Already at newest change" {
		t.Errorf("message = %q, want %q", m.message, "Already at newest change")
	}
}

func TestNewEditAfterUndoDiscardsRedo(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	h := m.currentHeadline()

	m = sendKey(m, "r") // TODO -> NEXT
	m = sendKey(m, "u") // back to TODO, redo available
	m = sendKey(m, "r") // a fresh edit: TODO -> NEXT again

	if h.Keyword != "NEXT" {
		t.Fatalf("keyword = %q, want NEXT", h.Keyword)
	}

	m = sendKey(m, "ctrl+r")
	if m.message != "Already at newest change" {
		t.Errorf("expected the old redo entry to be discarded, message = %q", m.message)
	}
}

func TestUndoJumpsCursorToAffectedItem(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	idx := findRow(t, m, "Call the vet about Fido's checkup")
	m.cursor = idx
	m = sendKey(m, "r")

	// Move away, then undo — cursor should jump back.
	m = sendKey(m, "G")
	if m.cursor == idx {
		t.Fatalf("fixture assumption broken: G did not move the cursor away")
	}

	m = sendKey(m, "u")
	if m.cursor != idx {
		t.Errorf("cursor after undo = %d, want %d (back on the affected item)", m.cursor, idx)
	}
}

func TestUndoRedoSubtreeEdit(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	idx := findRow(t, m, "Ship orgtd v0.1")
	m.cursor = idx
	old := m.currentHeadline()
	childCount := len(old.Children)

	edited := strings.Replace(org.RenderHeadline(old), "Ship orgtd v0.1", "Ship orgtd v0.2", 1)
	path := writeTempOrgFile(t, edited)
	updated, _ := m.Update(editFinishedMsg{path: path, target: old})
	m = updated.(Model)

	newHead := m.rows[idx].headline
	if newHead.Title != "Ship orgtd v0.2" {
		t.Fatalf("edit did not apply: title = %q", newHead.Title)
	}

	m = sendKey(m, "u")
	restored := m.rows[idx].headline
	if restored != old {
		t.Fatalf("undo did not restore the original headline pointer")
	}
	if restored.Title != "Ship orgtd v0.1" || len(restored.Children) != childCount {
		t.Errorf("restored headline = title %q with %d children, want %q with %d",
			restored.Title, len(restored.Children), "Ship orgtd v0.1", childCount)
	}
	if m.dirtyHeadlines[restored] {
		t.Errorf("expected no dirty marker after undoing back to the original, never-saved state")
	}

	m = sendKey(m, "ctrl+r")
	redone := m.rows[idx].headline
	if redone != newHead {
		t.Fatalf("redo did not restore the edited headline pointer")
	}
	if redone.Title != "Ship orgtd v0.2" {
		t.Errorf("redone title = %q, want Ship orgtd v0.2", redone.Title)
	}
	for i, c := range redone.Children {
		if !m.dirtyHeadlines[c] {
			t.Errorf("expected redo to re-mark child %d (%q) dirty", i, c.Title)
		}
	}
}

func TestUndoPastSavePointReDirties(t *testing.T) {
	ws := loadFixtureCopy(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	h := m.currentHeadline()

	m = sendKey(m, "r") // TODO -> NEXT
	m = sendKey(m, ":")
	m = typeKeys(m, "w")
	m = sendKey(m, "enter") // save: file and item both clean now

	if m.dirty[m.ws.Files[0]] || m.dirtyHeadlines[h] {
		t.Fatalf("expected a clean state right after :w")
	}

	m = sendKey(m, "u") // undo past the save point: memory (TODO) now differs from disk (NEXT)
	if h.Keyword != "TODO" {
		t.Fatalf("keyword after undo = %q, want TODO", h.Keyword)
	}
	if !m.dirty[m.ws.Files[0]] {
		t.Errorf("expected the file to be dirty after undoing past its save point")
	}
	if !m.dirtyHeadlines[h] {
		t.Errorf("expected the item to be dirty after undoing past its save point")
	}

	m = sendKey(m, "ctrl+r") // redo back to the saved state: clean again
	if m.dirty[m.ws.Files[0]] {
		t.Errorf("expected the file to be clean again after redoing back to the saved state")
	}
	if m.dirtyHeadlines[h] {
		t.Errorf("expected the item to be clean again after redoing back to the saved state")
	}
}

func TestUndoIsGlobalAcrossFiles(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)

	m.cursor = findRow(t, m, "Call the vet about Fido's checkup") // inbox.org
	inboxHeadline := m.currentHeadline()
	m = sendKey(m, "r") // TODO -> NEXT

	m.cursor = findRow(t, m, "Draft the Q4 goals doc") // projects.org
	projectsHeadline := m.currentHeadline()
	m = sendKey(m, "r") // NEXT -> WAITING

	// u is global: the single most recent action, regardless of file,
	// is the projects.org edit.
	m = sendKey(m, "u")
	if projectsHeadline.Keyword != "NEXT" {
		t.Errorf("projects.org headline keyword = %q, want NEXT (undone)", projectsHeadline.Keyword)
	}
	if inboxHeadline.Keyword != "NEXT" {
		t.Errorf("inbox.org headline keyword = %q, want NEXT (untouched by this undo)", inboxHeadline.Keyword)
	}

	m = sendKey(m, "u")
	if inboxHeadline.Keyword != "TODO" {
		t.Errorf("inbox.org headline keyword = %q, want TODO (now undone)", inboxHeadline.Keyword)
	}
}

func TestCommandUndoRedoAliases(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	h := m.currentHeadline()

	m = sendKey(m, "r")
	m = sendKey(m, ":")
	m = typeKeys(m, "undo")
	m = sendKey(m, "enter")
	if h.Keyword != "TODO" {
		t.Errorf(":undo did not revert the change, keyword = %q", h.Keyword)
	}

	m = sendKey(m, ":")
	m = typeKeys(m, "redo")
	m = sendKey(m, "enter")
	if h.Keyword != "NEXT" {
		t.Errorf(":redo did not re-apply the change, keyword = %q", h.Keyword)
	}
}
