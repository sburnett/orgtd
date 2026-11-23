package ui

import (
	"sort"
	"strings"
	"testing"

	"github.com/sburnett/orgtd/internal/org"
)

// fakeAction is an undoAction that records nothing in any tree: the
// history only reads file() and affected(), so these stand in for real
// edits.
type fakeAction struct {
	f *org.File
	h *org.Headline
}

func (a *fakeAction) apply(*Model) *org.Headline  { return a.h }
func (a *fakeAction) revert(*Model) *org.Headline { return a.h }
func (a *fakeAction) file() *org.File             { return a.f }
func (a *fakeAction) affected() []*org.Headline   { return []*org.Headline{a.h} }

func fakeEdit(f *org.File, title string) *fakeAction {
	return &fakeAction{f: f, h: &org.Headline{Title: title}}
}

// historyUndo and historyRedo move the way Model.undo/redo do.
func historyUndo(t *testing.T, h *undoHistory) {
	t.Helper()
	if h.nextUndo() == nil {
		t.Fatal("nothing to undo")
	}
	h.stepBack()
}

func historyRedo(t *testing.T, h *undoHistory) {
	t.Helper()
	if h.nextRedo() == nil {
		t.Fatal("nothing to redo")
	}
	h.stepForward()
}

// describeChanges renders changes() as "file: title, title" lines,
// sorted, for comparison.
func describeChanges(h *undoHistory) string {
	files, headlines := h.changes()
	var lines []string
	for f := range files {
		lines = append(lines, "dirty "+f.Path)
	}
	for hl := range headlines {
		lines = append(lines, "headline "+hl.Title)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func wantChanges(t *testing.T, h *undoHistory, want ...string) {
	t.Helper()
	sort.Strings(want)
	if got := describeChanges(h); got != strings.Join(want, "\n") {
		t.Errorf("changes:\n%s\nwant:\n%s", got, strings.Join(want, "\n"))
	}
}

func TestUndoHistoryEmpty(t *testing.T) {
	h := newUndoHistory()
	if h.nextUndo() != nil || h.nextRedo() != nil {
		t.Error("empty history has something to undo or redo")
	}
	wantChanges(t, &h)
}

func TestUndoHistoryUndoRedoMovesThroughActions(t *testing.T) {
	f := &org.File{Path: "a.org"}
	a1, a2 := fakeEdit(f, "one"), fakeEdit(f, "two")
	h := newUndoHistory()
	h.push(a1)
	h.push(a2)

	if h.nextUndo() != a2 || h.nextRedo() != nil {
		t.Fatalf("after two pushes: undo %v redo %v", h.nextUndo(), h.nextRedo())
	}
	historyUndo(t, &h)
	if h.nextUndo() != a1 || h.nextRedo() != a2 {
		t.Fatalf("after one undo: undo %v redo %v", h.nextUndo(), h.nextRedo())
	}
	historyUndo(t, &h)
	if h.nextUndo() != nil || h.nextRedo() != a1 {
		t.Fatalf("after two undos: undo %v redo %v", h.nextUndo(), h.nextRedo())
	}
	historyRedo(t, &h)
	if h.nextUndo() != a1 || h.nextRedo() != a2 {
		t.Fatalf("after redo: undo %v redo %v", h.nextUndo(), h.nextRedo())
	}
}

func TestUndoHistoryPushDiscardsRedoTail(t *testing.T) {
	f := &org.File{Path: "a.org"}
	h := newUndoHistory()
	h.push(fakeEdit(f, "one"))
	h.push(fakeEdit(f, "two"))
	historyUndo(t, &h)
	three := fakeEdit(f, "three")
	h.push(three)
	if h.nextRedo() != nil || h.nextUndo() != three || len(h.actions) != 2 {
		t.Errorf("after undo then push: %d actions, undo %v, redo %v; want 2, three, nil",
			len(h.actions), h.nextUndo(), h.nextRedo())
	}
}

func TestUndoHistoryChangesFollowTheSavePoint(t *testing.T) {
	f := &org.File{Path: "a.org"}
	h := newUndoHistory()

	h.push(fakeEdit(f, "one"))
	wantChanges(t, &h, "dirty a.org", "headline one")

	h.markSaved(f)
	wantChanges(t, &h)

	h.push(fakeEdit(f, "two"))
	wantChanges(t, &h, "dirty a.org", "headline two")

	// Back to the save point: clean again.
	historyUndo(t, &h)
	wantChanges(t, &h)

	// Past it: "one" is on disk but no longer in memory.
	historyUndo(t, &h)
	wantChanges(t, &h, "dirty a.org", "headline one")

	historyRedo(t, &h)
	wantChanges(t, &h)
}

// Save, undo, then make a different edit: the count of applied actions
// is back to the saved one, but what's on disk is on discarded history,
// so the file has to stay unsaved — however far undo/redo goes — until
// it's written again.
func TestUndoHistoryNewEditAfterUndoPastSaveStaysDirty(t *testing.T) {
	f := &org.File{Path: "a.org"}
	h := newUndoHistory()
	h.push(fakeEdit(f, "saved"))
	h.markSaved(f)
	historyUndo(t, &h)
	h.push(fakeEdit(f, "new"))
	wantChanges(t, &h, "dirty a.org", "headline new", "headline saved")

	historyUndo(t, &h)
	wantChanges(t, &h, "dirty a.org", "headline saved")

	historyRedo(t, &h)
	h.markSaved(f)
	wantChanges(t, &h)
}

// Stranding one file's save point leaves another file's alone.
func TestUndoHistoryPushStrandsOnlyDiscardedFiles(t *testing.T) {
	a, b := &org.File{Path: "a.org"}, &org.File{Path: "b.org"}
	h := newUndoHistory()
	h.push(fakeEdit(a, "a1"))
	h.push(fakeEdit(b, "b1"))
	h.markSaved(a)
	h.markSaved(b)
	historyUndo(t, &h) // b1
	h.push(fakeEdit(a, "a2"))
	wantChanges(t, &h, "dirty a.org", "dirty b.org", "headline a2", "headline b1")

	h.markSaved(a)
	wantChanges(t, &h, "dirty b.org", "headline b1")
}

// Each file's save point counts only its own actions, so interleaved
// edits to another file don't make it look saved or unsaved.
func TestUndoHistorySavePointsArePerFile(t *testing.T) {
	a, b := &org.File{Path: "a.org"}, &org.File{Path: "b.org"}
	h := newUndoHistory()
	h.push(fakeEdit(a, "a1"))
	h.markSaved(a)
	h.push(fakeEdit(b, "b1"))
	h.push(fakeEdit(a, "a2"))
	wantChanges(t, &h, "dirty a.org", "dirty b.org", "headline a2", "headline b1")

	historyUndo(t, &h) // a2
	wantChanges(t, &h, "dirty b.org", "headline b1")
	historyUndo(t, &h) // b1
	wantChanges(t, &h)
}

func TestUndoHistoryForgetFileKeepsPositionAmongTheRest(t *testing.T) {
	a, b := &org.File{Path: "a.org"}, &org.File{Path: "b.org"}
	b1, b2 := fakeEdit(b, "b1"), fakeEdit(b, "b2")
	h := newUndoHistory()
	h.push(fakeEdit(a, "a1"))
	h.push(b1)
	h.push(fakeEdit(a, "a2"))
	h.markSaved(a)
	h.push(b2)
	h.push(fakeEdit(a, "a3"))
	historyUndo(t, &h) // a3
	historyUndo(t, &h) // b2

	h.forgetFile(a)
	if len(h.actions) != 2 || h.nextUndo() != b1 || h.nextRedo() != b2 {
		t.Errorf("after forgetFile: %d actions, undo %v, redo %v; want 2, b1, b2",
			len(h.actions), h.nextUndo(), h.nextRedo())
	}
	if _, ok := h.savedPos[a]; ok {
		t.Error("forgetFile kept a's save point")
	}
	wantChanges(t, &h, "dirty b.org", "headline b1")
}

func TestUndoHistoryForgetFileDropsAStrandedSavePoint(t *testing.T) {
	f := &org.File{Path: "a.org"}
	h := newUndoHistory()
	h.push(fakeEdit(f, "saved"))
	h.markSaved(f)
	historyUndo(t, &h)
	h.push(fakeEdit(f, "new"))
	h.forgetFile(f)
	wantChanges(t, &h)
}
