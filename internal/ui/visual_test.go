package ui

import (
	"strings"
	"testing"

	"github.com/sburnett/orgtd/internal/org"
	"github.com/sburnett/orgtd/internal/workspace"
)

func visualFixtureModel(t *testing.T) Model {
	t.Helper()
	ws := agendaFixture(t, `* TODO First
* NEXT Second
** TODO Child of second
* WAITING Third
`)
	return New(ws)
}

func TestVisualModeEntersOnVAndCancelsOnEsc(t *testing.T) {
	m := visualFixtureModel(t)
	m.cursor = findRow(t, m, "First")

	m = sendKey(m, "V")
	if m.mode != visualMode {
		t.Fatalf("mode after V = %v, want visualMode", m.mode)
	}
	if m.visualAnchor != m.cursor {
		t.Errorf("visualAnchor = %d, want %d (the row V was pressed on)", m.visualAnchor, m.cursor)
	}

	m = sendKey(m, "esc")
	if m.mode != normalMode {
		t.Errorf("mode after Esc = %v, want normalMode", m.mode)
	}
}

func TestVisualModePressingVAgainCancels(t *testing.T) {
	m := visualFixtureModel(t)
	m.cursor = findRow(t, m, "First")
	m = sendKey(m, "V")
	m = sendKey(m, "V")
	if m.mode != normalMode {
		t.Errorf("mode after V,V = %v, want normalMode (V toggles off)", m.mode)
	}
}

func TestVisualModeSelectionSpansAnchorToCursor(t *testing.T) {
	m := visualFixtureModel(t)
	m.cursor = findRow(t, m, "First")
	m = sendKey(m, "V")
	m = sendKey(m, "j")
	m = sendKey(m, "j")

	headlines := m.visualSelectedHeadlines()
	var titles []string
	for _, h := range headlines {
		titles = append(titles, h.Title)
	}
	want := []string{"First", "Second", "Child of second"}
	if len(titles) != len(want) {
		t.Fatalf("selected titles = %v, want %v", titles, want)
	}
	for i, w := range want {
		if titles[i] != w {
			t.Errorf("selected[%d] = %q, want %q", i, titles[i], w)
		}
	}
}

func TestVisualModeDeleteRemovesTopmostSelectedEntriesInOneUndoStep(t *testing.T) {
	m := visualFixtureModel(t)
	m.cursor = findRow(t, m, "First")
	m = sendKey(m, "V")
	m = sendKey(m, "j")
	m = sendKey(m, "j") // selection now covers First, Second, Child of second

	undoDepthBefore := m.undoPos
	m = sendKey(m, "d")

	if m.mode != normalMode {
		t.Errorf("mode after visual d = %v, want normalMode", m.mode)
	}
	if rowIndex(m, "First") >= 0 {
		t.Error("First should have been deleted")
	}
	if rowIndex(m, "Second") >= 0 {
		t.Error("Second should have been deleted")
	}
	if rowIndex(m, "Child of second") >= 0 {
		t.Error("Child of second should have been deleted along with its parent Second")
	}
	if rowIndex(m, "Third") < 0 {
		t.Error("Third was not selected and should remain")
	}
	if got := m.undoPos - undoDepthBefore; got != 1 {
		t.Errorf("undo steps pushed by the bulk delete = %d, want 1 (a single batched step)", got)
	}

	m.undo()
	for _, title := range []string{"First", "Second", "Child of second", "Third"} {
		if rowIndex(m, title) < 0 {
			t.Errorf("after undo, %q should be restored", title)
		}
	}
}

func TestVisualModeSetStatusAppliesToEveryRowIndependently(t *testing.T) {
	m := visualFixtureModel(t)
	m.cursor = findRow(t, m, "First")
	m = sendKey(m, "V")
	m = sendKey(m, "j")
	m = sendKey(m, "j") // First, Second, Child of second

	undoDepthBefore := m.undoPos
	m = sendKey(m, "R")
	if m.mode != selectMode || !m.selectModeVisual {
		t.Fatalf("mode after visual R = %v (selectModeVisual=%v), want selectMode with selectModeVisual=true", m.mode, m.selectModeVisual)
	}
	m = sendKey(m, "d") // "d" uniquely filters to the DONE candidate and auto-applies

	if m.mode != normalMode {
		t.Errorf("mode after choosing a status = %v, want normalMode", m.mode)
	}
	for _, title := range []string{"First", "Second", "Child of second"} {
		h := findHeadlineByTitle(t, m, title)
		if h.Keyword != "DONE" {
			t.Errorf("%q keyword = %q, want DONE", title, h.Keyword)
		}
	}
	if h := findHeadlineByTitle(t, m, "Third"); h.Keyword != "WAITING" {
		t.Errorf("Third keyword = %q, want unchanged WAITING (it wasn't selected)", h.Keyword)
	}
	if got := m.undoPos - undoDepthBefore; got != 1 {
		t.Errorf("undo steps pushed by the bulk status change = %d, want 1 (a single batched step)", got)
	}

	m.undo()
	for _, title := range []string{"First", "Child of second"} {
		if h := findHeadlineByTitle(t, m, title); h.Keyword != "TODO" {
			t.Errorf("after undo, %q keyword = %q, want restored TODO", title, h.Keyword)
		}
	}
	if h := findHeadlineByTitle(t, m, "Second"); h.Keyword != "NEXT" {
		t.Errorf("after undo, Second keyword = %q, want restored NEXT", h.Keyword)
	}
}

func TestVisualModeEscFromStatusPickerAppliesNoChange(t *testing.T) {
	m := visualFixtureModel(t)
	m.cursor = findRow(t, m, "First")
	m = sendKey(m, "V")
	m = sendKey(m, "j")
	m = sendKey(m, "R")
	m = sendKey(m, "esc")

	if m.mode != normalMode {
		t.Errorf("mode after Esc from the picker = %v, want normalMode", m.mode)
	}
	if h := findHeadlineByTitle(t, m, "First"); h.Keyword != "TODO" {
		t.Errorf("First keyword = %q, want unchanged TODO", h.Keyword)
	}
	if h := findHeadlineByTitle(t, m, "Second"); h.Keyword != "NEXT" {
		t.Errorf("Second keyword = %q, want unchanged NEXT", h.Keyword)
	}
}

func TestVisualModeDeleteAcrossFilesUsesOneBatchPerFile(t *testing.T) {
	fileA, err := org.Parse(strings.NewReader("* TODO A1\n"), "a.org")
	if err != nil {
		t.Fatalf("org.Parse: %v", err)
	}
	fileB, err := org.Parse(strings.NewReader("* TODO B1\n"), "b.org")
	if err != nil {
		t.Fatalf("org.Parse: %v", err)
	}
	ws := &workspace.Workspace{Dir: "multi-file-fixture", Files: []*org.File{fileA, fileB}}
	m := New(ws)
	m.cursor = findRow(t, m, "A1")

	m = sendKey(m, "V")
	m.cursor = findRow(t, m, "B1")

	undoDepthBefore := m.undoPos
	m = sendKey(m, "d")

	if rowIndex(m, "A1") >= 0 || rowIndex(m, "B1") >= 0 {
		t.Error("both A1 and B1 should have been deleted")
	}
	if got := m.undoPos - undoDepthBefore; got != 2 {
		t.Errorf("undo steps pushed by a cross-file bulk delete = %d, want 2 (one per file touched)", got)
	}

	m.undo()
	if rowIndex(m, "A1") >= 0 {
		t.Error("one undo should only restore the most recently deleted file's entry (B1's), not A1's yet")
	}
	if rowIndex(m, "B1") < 0 {
		t.Error("one undo should have restored B1")
	}
	m.undo()
	if rowIndex(m, "A1") < 0 {
		t.Error("a second undo should have restored A1")
	}
}

// TestVisualModeDeleteInClarifyViewAdvancesClarifyTarget guards against a
// real ordering bug hit during development: advanceClarifyTarget (which
// just re-reads the inbox's current first top-level headline) must run
// after the bulk delete is actually applied, not before — otherwise it
// re-pins the very entry that's about to be removed instead of skipping
// past it.
func TestVisualModeDeleteInClarifyViewAdvancesClarifyTarget(t *testing.T) {
	inbox, err := org.Parse(strings.NewReader("* A\n* B\n* C\n"), "inbox.org")
	if err != nil {
		t.Fatalf("org.Parse: %v", err)
	}
	ws := &workspace.Workspace{Dir: "clarify-fixture", Files: []*org.File{inbox}}
	m := New(ws)
	m.enterClarifyView()
	if m.clarifyTarget == nil || m.clarifyTarget.Title != "A" {
		t.Fatalf("clarifyTarget = %v, want A", m.clarifyTarget)
	}

	m.cursor = findRow(t, m, "A")
	m = sendKey(m, "V")
	m = sendKey(m, "j") // select A and B
	m = sendKey(m, "d")

	if m.clarifyTarget == nil || m.clarifyTarget.Title != "C" {
		t.Errorf("clarifyTarget after deleting A and B = %v, want C", m.clarifyTarget)
	}
}

// findHeadlineByTitle returns the headline with the given title among
// m's current rows, failing the test if there's no such row.
func findHeadlineByTitle(t *testing.T, m Model, title string) *org.Headline {
	t.Helper()
	for _, r := range m.rows {
		if r.headline != nil && !r.isBodyLine && r.headline.Title == title {
			return r.headline
		}
	}
	t.Fatalf("no row with title %q", title)
	return nil
}
