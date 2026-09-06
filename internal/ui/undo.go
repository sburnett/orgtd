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

// insertContext records where an o/O-inserted headline lives: which
// file, which parent (nil if top-level), and its index within that
// parent's children (or the file's top-level list).
type insertContext struct {
	f      *org.File
	parent *org.Headline
	index  int
	origin *org.Headline // headline the cursor was on before o/O; refocused on rollback
}

// spliceAction is the shared machinery behind insertAction and
// deleteAction: headlines occupy (or once occupied) index within
// parent's children (or f's top-level list, if parent is nil). insert
// and remove perform the two directions; insertAction and deleteAction
// each just wire apply/revert to one or the other, in opposite polarity.
type spliceAction struct {
	f         *org.File
	parent    *org.Headline
	index     int
	headlines []*org.Headline
	inTree    bool // whether headlines currently occupy their position
}

func (a *spliceAction) insert(m *Model) *org.Headline {
	for _, h := range a.headlines {
		h.Parent = a.parent
	}
	if a.parent != nil {
		a.parent.Children = spliceHeadlines(a.parent.Children, a.index, 0, a.headlines)
	} else {
		a.f.Headlines = spliceHeadlines(a.f.Headlines, a.index, 0, a.headlines)
	}
	a.inTree = true
	return a.headlines[0]
}

// remove splices headlines back out and returns a sensible headline to
// focus afterward: whatever now sits at the same position, else the
// previous sibling, else nil (the caller falls back to the file row).
func (a *spliceAction) remove(m *Model) *org.Headline {
	if a.parent != nil {
		a.parent.Children = spliceHeadlines(a.parent.Children, a.index, len(a.headlines), nil)
	} else {
		a.f.Headlines = spliceHeadlines(a.f.Headlines, a.index, len(a.headlines), nil)
	}
	org.Walk(a.headlines, func(h *org.Headline) { delete(m.collapsed, h) })
	a.inTree = false
	return m.siblingAt(a.parent, a.f, a.index)
}

func (a *spliceAction) file() *org.File { return a.f }

// affectedIfInTree returns the headlines while they're in the tree, or
// nothing while they're not (there's nothing visible to mark dirty).
func (a *spliceAction) affectedIfInTree() []*org.Headline {
	if !a.inTree {
		return nil
	}
	var out []*org.Headline
	org.Walk(a.headlines, func(h *org.Headline) { out = append(out, h) })
	return out
}

// siblingAt returns the headline now at index within parent's children
// (or f's top-level list), or the one before it, or nil if the list is
// empty at that point — used to pick a focus target after removing
// something at that position.
func (m *Model) siblingAt(parent *org.Headline, f *org.File, index int) *org.Headline {
	list := f.Headlines
	if parent != nil {
		list = parent.Children
	}
	if index >= 0 && index < len(list) {
		return list[index]
	}
	if index-1 >= 0 && index-1 < len(list) {
		return list[index-1]
	}
	return nil
}

// insertAction records an o/O insert once it's been committed (see
// commitInsert), or a p/P paste: headlines were inserted at index within
// parent's children (or f's top-level list, if parent is nil).
type insertAction struct{ spliceAction }

func (a *insertAction) apply(m *Model) *org.Headline  { return a.insert(m) }
func (a *insertAction) revert(m *Model) *org.Headline { return a.remove(m) }
func (a *insertAction) affected() []*org.Headline     { return a.affectedIfInTree() }

// deleteAction records a dd: headlines were removed from index within
// parent's children (or f's top-level list, if parent is nil). It's an
// insertAction with apply/revert swapped — deleting is just inserting
// run backward.
type deleteAction struct{ spliceAction }

func (a *deleteAction) apply(m *Model) *org.Headline  { return a.remove(m) }
func (a *deleteAction) revert(m *Model) *org.Headline { return a.insert(m) }
func (a *deleteAction) affected() []*org.Headline     { return a.affectedIfInTree() }

// insertPosition returns the file, parent (nil if top-level), and index
// of h within its parent's children (or its file's top-level list).
func (m *Model) insertPosition(h *org.Headline) (f *org.File, parent *org.Headline, index int) {
	f = m.fileForHeadline(h)
	parent = h.Parent
	list := f.Headlines
	if parent != nil {
		list = parent.Children
	}
	for i, c := range list {
		if c == h {
			return f, parent, i
		}
	}
	return f, parent, -1
}

// commitInsert finalizes an o/O insert session: it swaps the tentative
// placeholder headline for the final edited content in a single tree
// mutation, then records the whole session (open + edit) as one undo
// step — matching vim treating "o, type, Esc" as a single undo unit.
func (m *Model) commitInsert(ctx insertContext, tentative *org.Headline, final []*org.Headline) {
	m.spliceReplace([]*org.Headline{tentative}, final)
	m.undoStack = append(m.undoStack[:m.undoPos], &insertAction{spliceAction{
		f: ctx.f, parent: ctx.parent, index: ctx.index, headlines: final, inTree: true,
	}})
	m.undoPos = len(m.undoStack)
	m.rebuildRows()
	m.focusHeadline(final[0])
	m.recomputeDirty()
}

// rollbackInsert removes a tentative o/O placeholder that never got
// committed (the editor failed to run, or the user emptied it out),
// leaving no trace in the tree and no entry in undo history.
func (m *Model) rollbackInsert(ctx insertContext, tentative *org.Headline) {
	m.spliceReplace([]*org.Headline{tentative}, nil)
	m.rebuildRows()
	m.focusTarget(ctx.origin, ctx.f)
	m.recomputeDirty()
}

// focusFile moves the cursor to f's file-header row.
func (m *Model) focusFile(f *org.File) {
	for i, r := range m.rows {
		if r.file == f {
			m.cursor = i
			m.ensureVisible()
			return
		}
	}
}

// focusTarget moves the cursor to h if it's non-nil, otherwise falls
// back to f's file-header row (used when an action's apply/revert has no
// headline to point at, e.g. undoing a top-level insert).
func (m *Model) focusTarget(h *org.Headline, f *org.File) {
	if h != nil {
		m.focusHeadline(h)
		return
	}
	if f != nil {
		m.focusFile(f)
	}
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
	m.focusTarget(target, a.file())
	m.recomputeDirty()
}

// undo reverts the most recently applied action, if any.
func (m *Model) undo() {
	if m.undoPos == 0 {
		m.message = "Already at oldest change"
		return
	}
	m.undoPos--
	a := m.undoStack[m.undoPos]
	target := a.revert(m)
	m.rebuildRows()
	m.focusTarget(target, a.file())
	m.recomputeDirty()
	m.message = "1 change undone"
}

// redo re-applies the next available action, if any.
func (m *Model) redo() {
	if m.undoPos >= len(m.undoStack) {
		m.message = "Already at newest change"
		return
	}
	a := m.undoStack[m.undoPos]
	target := a.apply(m)
	m.undoPos++
	m.rebuildRows()
	m.focusTarget(target, a.file())
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
