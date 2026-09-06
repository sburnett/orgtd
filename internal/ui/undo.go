package ui

import "github.com/sburnett/orgtd/internal/org"

// undoAction is one undoable edit. apply/revert perform the tree
// mutation for their direction and return the headline the cursor
// should land on afterward. file identifies which org file the action
// belongs to (fixed for the life of the action — no edit moves a
// headline to a different file). affected returns the headline(s)
// that are "showing" as a result of whichever direction is currently
// active, used to derive per-item dirty markers.
type undoAction interface {
	apply(m *Model) *org.Headline
	revert(m *Model) *org.Headline
	file() *org.File
	affected() []*org.Headline
}

// statusChangeAction records a TODO-keyword change (r/R), including any
// CLOSED stamping that went with it. The mutation is in place, so the
// same headline pointer is what's "affected" regardless of direction.
type statusChangeAction struct {
	h                      *org.Headline
	f                      *org.File
	oldKeyword, newKeyword string
	oldClosed, newClosed   *org.Timestamp
}

func (a *statusChangeAction) apply(m *Model) *org.Headline {
	a.h.Keyword = a.newKeyword
	a.h.Closed = a.newClosed
	return a.h
}

func (a *statusChangeAction) revert(m *Model) *org.Headline {
	a.h.Keyword = a.oldKeyword
	a.h.Closed = a.oldClosed
	return a.h
}

func (a *statusChangeAction) file() *org.File           { return a.f }
func (a *statusChangeAction) affected() []*org.Headline { return []*org.Headline{a.h} }

// subtreeReplaceAction records an `i` edit: oldSet (almost always a
// single headline and its subtree) was replaced by newSet (one or more
// headlines, e.g. if the edit split the entry into siblings). applied
// tracks which set is currently spliced into the tree.
type subtreeReplaceAction struct {
	f              *org.File
	oldSet, newSet []*org.Headline
	applied        bool
}

func (a *subtreeReplaceAction) apply(m *Model) *org.Headline {
	m.spliceReplace(a.oldSet, a.newSet)
	a.applied = true
	return a.newSet[0]
}

func (a *subtreeReplaceAction) revert(m *Model) *org.Headline {
	m.spliceReplace(a.newSet, a.oldSet)
	a.applied = false
	return a.oldSet[0]
}

func (a *subtreeReplaceAction) file() *org.File { return a.f }

func (a *subtreeReplaceAction) affected() []*org.Headline {
	set := a.oldSet
	if a.applied {
		set = a.newSet
	}
	var out []*org.Headline
	org.Walk(set, func(h *org.Headline) { out = append(out, h) })
	return out
}

// spliceReplace replaces the contiguous run oldSet — which must
// currently occupy consecutive positions in a single parent's children
// (or a file's top-level list) — with newSet, carrying over oldSet's own
// fold state to the first element of newSet. Used for both directions of
// a subtreeReplaceAction: forward (old -> new) and its exact inverse
// (new -> old).
func (m *Model) spliceReplace(oldSet, newSet []*org.Headline) {
	if len(oldSet) == 0 {
		return
	}
	first := oldSet[0]

	wasCollapsed := m.collapsed[first]
	org.Walk(oldSet, func(h *org.Headline) { delete(m.collapsed, h) })

	for _, n := range newSet {
		n.Parent = first.Parent
	}
	if wasCollapsed && len(newSet) > 0 {
		m.collapsed[newSet[0]] = true
	}

	if first.Parent != nil {
		for i, c := range first.Parent.Children {
			if c == first {
				first.Parent.Children = spliceHeadlines(first.Parent.Children, i, len(oldSet), newSet)
				return
			}
		}
		return
	}
	for _, f := range m.ws.Files {
		for i, top := range f.Headlines {
			if top == first {
				f.Headlines = spliceHeadlines(f.Headlines, i, len(oldSet), newSet)
				return
			}
		}
	}
}

// spliceHeadlines returns a copy of list with the removeCount elements
// starting at idx replaced by replacements (which may contain zero, one,
// or several headlines).
func spliceHeadlines(list []*org.Headline, idx, removeCount int, replacements []*org.Headline) []*org.Headline {
	out := make([]*org.Headline, 0, len(list)-removeCount+len(replacements))
	out = append(out, list[:idx]...)
	out = append(out, replacements...)
	out = append(out, list[idx+removeCount:]...)
	return out
}

// pushUndo performs a new edit: it discards any redo tail, applies the
// action, records it, and refreshes the cursor and dirty state. Every
// mutating command (status change, entry edit) goes through here so undo
// history stays complete.
func (m *Model) pushUndo(a undoAction) {
	m.undoStack = append(m.undoStack[:m.undoPos], a)
	m.undoPos = len(m.undoStack)
	target := a.apply(m)
	m.rebuildRows()
	m.focusHeadline(target)
	m.recomputeDirty()
}

// undo reverts the most recently applied action, if any.
func (m *Model) undo() {
	if m.undoPos == 0 {
		m.message = "Already at oldest change"
		return
	}
	m.undoPos--
	target := m.undoStack[m.undoPos].revert(m)
	m.rebuildRows()
	m.focusHeadline(target)
	m.recomputeDirty()
	m.message = "1 change undone"
}

// redo re-applies the next available action, if any.
func (m *Model) redo() {
	if m.undoPos >= len(m.undoStack) {
		m.message = "Already at newest change"
		return
	}
	target := m.undoStack[m.undoPos].apply(m)
	m.undoPos++
	m.rebuildRows()
	m.focusHeadline(target)
	m.recomputeDirty()
	m.message = "1 change redone"
}

// focusHeadline moves the cursor to h's row. h should always be visible
// right after a rebuildRows following apply/revert.
func (m *Model) focusHeadline(h *org.Headline) {
	if h == nil {
		return
	}
	for i, r := range m.rows {
		if r.headline == h {
			m.cursor = i
			m.ensureVisible()
			return
		}
	}
}

// appliedCountForFile returns how many of f's actions are currently
// applied, in f's own relative order (i.e. ignoring other files'
// actions interleaved in the global stack).
func (m *Model) appliedCountForFile(f *org.File) int {
	n := 0
	for _, a := range m.undoStack[:m.undoPos] {
		if a.file() == f {
			n++
		}
	}
	return n
}

// recomputeDirty derives which files and headlines currently differ from
// what's on disk, purely from the undo stack's position relative to each
// file's last-saved position (savedPos). This gives vim's behavior of
// clearing (or restoring) the "modified" state when undo/redo crosses a
// save point, even though undo history here is a single stack spanning
// every file rather than vim's per-buffer stacks.
func (m *Model) recomputeDirty() {
	m.dirty = make(map[*org.File]bool)
	m.dirtyHeadlines = make(map[*org.Headline]bool)

	applied := make(map[*org.File]int)
	for _, a := range m.undoStack[:m.undoPos] {
		applied[a.file()]++
	}
	for _, f := range m.ws.Files {
		if applied[f] != m.savedPos[f] {
			m.dirty[f] = true
		}
	}

	// Forward-unsaved: applied actions beyond each file's saved position.
	running := make(map[*org.File]int)
	for _, a := range m.undoStack[:m.undoPos] {
		f := a.file()
		running[f]++
		if running[f] > m.savedPos[f] {
			for _, h := range a.affected() {
				m.dirtyHeadlines[h] = true
			}
		}
	}

	// Backward-unsaved: reverted actions that were part of the last
	// save — i.e. we've undone past a save point, so memory now differs
	// from disk in the other direction.
	running = make(map[*org.File]int)
	for _, a := range m.undoStack[m.undoPos:] {
		f := a.file()
		running[f]++
		if applied[f]+running[f] <= m.savedPos[f] {
			for _, h := range a.affected() {
				m.dirtyHeadlines[h] = true
			}
		}
	}
}
