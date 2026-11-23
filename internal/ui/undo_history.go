package ui

import "github.com/sburnett/orgtd/internal/org"

// undoHistory is the global undo stack plus each file's save point: the
// bookkeeping half of undo, with no knowledge of the tree, rows or
// cursor. It never applies or reverts an action itself — the Model does
// that (see pushUndo, undo, redo) and tells the history which way it
// moved — so it can be tested with stand-in actions.
//
// Undo history is a single stack spanning every file, unlike vim's
// per-buffer stacks, but each file still gets vim's behavior of reading
// as clean again when undo/redo returns it to its save point: a file's
// save point is how many of its own actions were applied when it was
// last written (savedPos), and it's unsaved whenever that count differs.
//
// Like the Model holding it, an undoHistory is copied by value; the
// copies share their maps, so only the newest copy should be used.
type undoHistory struct {
	actions []undoAction // actions[:pos] applied, actions[pos:] available to redo
	pos     int

	// savedPos is, per file, the count of its applied actions at its last
	// successful write. A file with no entry has never been written since
	// it was loaded, so its save point is 0.
	savedPos map[*org.File]int
	// savedFork holds files whose last write is on history since
	// discarded, with the headlines that write touched; see push.
	savedFork map[*org.File][]*org.Headline
}

func newUndoHistory() undoHistory {
	return undoHistory{
		savedPos:  make(map[*org.File]int),
		savedFork: make(map[*org.File][]*org.Headline),
	}
}

// push records a as the newest applied action, discarding the redo
// tail, as every new edit does. The caller applies a itself, before or
// after.
//
// Discarding the redo tail can strand a file's save point: save, undo,
// then edit, and what's on disk is now on a branch of history that
// undo/redo can no longer reach. Counting applied actions alone would
// miss that (the new edit brings the count back to the saved one), so
// such a file's savedPos is moved back to where the branches split, and
// the headlines touched by the discarded-but-saved actions are kept in
// savedFork: they differ from disk however far undo/redo goes, so the
// file stays unsaved, with those entries marked, until its next write.
func (h *undoHistory) push(a undoAction) {
	applied := h.appliedCounts()
	running := make(map[*org.File]int)
	for _, b := range h.actions[h.pos:] {
		f := b.file()
		running[f]++
		if applied[f]+running[f] <= h.savedPos[f] {
			h.savedFork[f] = append(h.savedFork[f], b.affected()...)
		}
	}
	for f := range running {
		if h.savedPos[f] > applied[f] {
			h.savedPos[f] = applied[f]
		}
	}
	h.actions = append(h.actions[:h.pos], a)
	h.pos = len(h.actions)
}

// nextUndo returns the action undo would revert, or nil at the oldest
// change. It doesn't move; call stepBack once the action is reverted.
func (h *undoHistory) nextUndo() undoAction {
	if h.pos == 0 {
		return nil
	}
	return h.actions[h.pos-1]
}

// nextRedo returns the action redo would re-apply, or nil at the newest
// change. It doesn't move; call stepForward once the action is applied.
func (h *undoHistory) nextRedo() undoAction {
	if h.pos >= len(h.actions) {
		return nil
	}
	return h.actions[h.pos]
}

// stepBack records that nextUndo's action has been reverted.
func (h *undoHistory) stepBack() { h.pos-- }

// stepForward records that nextRedo's action has been applied.
func (h *undoHistory) stepForward() { h.pos++ }

// markSaved records that f on disk now matches f in memory.
func (h *undoHistory) markSaved(f *org.File) {
	h.savedPos[f] = h.appliedCounts()[f]
	delete(h.savedFork, f)
}

// forgetFile drops every action on f, and f's save point, keeping the
// position pointing at the same place among the rest. Used when f's
// tree is replaced wholesale (a reload): its actions all refer to
// headlines that no longer exist, so undoing one would silently edit a
// file nobody can see.
func (h *undoHistory) forgetFile(f *org.File) {
	kept := h.actions[:0:0]
	pos := 0
	for i, a := range h.actions {
		if a.file() == f {
			continue
		}
		kept = append(kept, a)
		if i < h.pos {
			pos++
		}
	}
	h.actions, h.pos = kept, pos
	delete(h.savedPos, f)
	delete(h.savedFork, f)
}

// changes reports which files, and which headlines in them, differ from
// their save points: files whose applied-action count isn't the saved
// one or whose save point is stranded (see push), and the headlines
// affected by every action between a file's save point and where it is
// now, in either direction.
func (h *undoHistory) changes() (files map[*org.File]bool, headlines map[*org.Headline]bool) {
	files = make(map[*org.File]bool)
	headlines = make(map[*org.Headline]bool)

	applied := h.appliedCounts()
	for f, n := range applied {
		if n != h.savedPos[f] {
			files[f] = true
		}
	}
	for f, n := range h.savedPos {
		if n != applied[f] {
			files[f] = true
		}
	}

	// Saved on a branch of history since discarded (see push).
	for f, hs := range h.savedFork {
		files[f] = true
		for _, hl := range hs {
			headlines[hl] = true
		}
	}

	// Forward-unsaved: applied actions beyond each file's save point.
	running := make(map[*org.File]int)
	for _, a := range h.actions[:h.pos] {
		f := a.file()
		running[f]++
		if running[f] > h.savedPos[f] {
			for _, hl := range a.affected() {
				headlines[hl] = true
			}
		}
	}

	// Backward-unsaved: reverted actions that were part of the last
	// save — undone past a save point, so memory now differs from disk
	// in the other direction.
	running = make(map[*org.File]int)
	for _, a := range h.actions[h.pos:] {
		f := a.file()
		running[f]++
		if applied[f]+running[f] <= h.savedPos[f] {
			for _, hl := range a.affected() {
				headlines[hl] = true
			}
		}
	}
	return files, headlines
}

// appliedCounts returns how many of each file's actions are applied.
func (h *undoHistory) appliedCounts() map[*org.File]int {
	n := make(map[*org.File]int)
	for _, a := range h.actions[:h.pos] {
		n[a.file()]++
	}
	return n
}
