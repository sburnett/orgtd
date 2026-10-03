package ui

import (
	"fmt"
	"sort"
	"time"

	"github.com/sburnett/orgtd/internal/org"
)

// deleteHeadlineCount implements a numeric-prefixed "dd" (e.g. "3dd"):
// deletes the current entry and the next n-1 entries. A no-op on a file
// row, matching plain dd. See deleteHeadlineSet for the shared mechanics.
func (m *Model) deleteHeadlineCount(n int) {
	if m.currentHeadline() == nil {
		return
	}
	headlines := org.Topmost(m.headlinesInRowRange(m.countRowRange(n)))
	m.deleteHeadlineSet(headlines)
}

// deleteHeadlineSet removes every headline in headlines (each with its
// own subtree) — the shared implementation behind bulk delete, whether
// the selection came from visual mode (deleteVisualSelection) or a
// numeric prefix (deleteHeadlineCount). headlines is assumed already
// topmost-filtered (see org.Topmost) — deleting an ancestor
// already removes its whole subtree, so a selected descendant needs no
// delete of its own. Deletions are grouped into one undo step per file
// touched (a batchAction — see undo.go), so a selection confined to a
// single file, overwhelmingly the common case, undoes in one step; a
// selection spanning files takes one step per file, since undo/dirty
// tracking is inherently per-file. Like plain dd, the whole set fills the
// paste register (top-to-bottom order preserved), so p/P pastes every
// deleted entry back as a group, in one call, at the destination. Any
// headline locked by :format-links is silently excluded first (see
// filterImmutable) rather than aborting the whole operation; the summary
// message notes how many, if any.
func (m *Model) deleteHeadlineSet(headlines []*org.Headline) {
	headlines, skipped := m.filterImmutable(headlines)
	if len(headlines) == 0 {
		if skipped > 0 {
			m.message = "All selected entries are locked by :format-links; nothing deleted"
		}
		return
	}

	type target struct {
		h      *org.Headline
		f      *org.File
		parent *org.Headline
		idx    int
	}
	var order []*org.File
	byFile := make(map[*org.File][]target)
	for _, h := range headlines {
		f, parent, idx := m.ws.Locate(h)
		if idx < 0 {
			continue
		}
		if _, ok := byFile[f]; !ok {
			order = append(order, f)
		}
		byFile[f] = append(byFile[f], target{h, f, parent, idx})
	}

	for _, f := range order {
		targets := byFile[f]
		// Descending index so removing one entry doesn't shift another
		// still-to-be-removed entry's already-captured index — safe
		// regardless of parent, since a splice only ever affects its own
		// parent's list.
		sort.SliceStable(targets, func(i, j int) bool { return targets[i].idx > targets[j].idx })
		actions := make([]undoAction, len(targets))
		for i, t := range targets {
			actions[i] = &deleteAction{spliceAction{f: t.f, parent: t.parent, index: t.idx, headlines: []*org.Headline{t.h}, inTree: true}}
		}
		m.pushUndo(&batchAction{actions: actions})
		// clarifyTarget/marks bookkeeping, same as dd's deleteHeadline —
		// done after the delete is actually applied (advanceClarifyTarget
		// must see the removal to skip past the deleted entry, not just
		// re-read the same one that's about to go).
		for _, t := range targets {
			if m.view == clarifyView && t.h == m.clarifyTarget {
				m.advanceClarifyTarget()
			}
			org.Walk([]*org.Headline{t.h}, m.clearMarksFor)
		}
	}
	m.register = headlines
	m.message = fmt.Sprintf("Deleted %d entries", len(headlines))
	if skipped > 0 {
		m.message += fmt.Sprintf(" (%d skipped: locked by :format-links)", skipped)
	}
}

// buildStatusChangeAction returns the undoAction that setting h's
// keyword to keyword would produce — a repeatAdvanceAction if h is
// completing a repeating item (see repeatAdvanceForCompletion), or a
// plain statusChangeAction otherwise — without applying or pushing it,
// so bulk operations (visual-mode R) can batch several of these into one
// undo step the same way applyStatus handles a single one.
func (m *Model) buildStatusChangeAction(h *org.Headline, keyword string) undoAction {
	if org.IsDoneKeyword(keyword) && !org.IsDoneKeyword(h.Keyword) {
		if a := m.repeatAdvanceForCompletion(h); a != nil {
			return a
		}
	}

	newClosed := h.Closed
	switch {
	case org.IsDoneKeyword(keyword) && !org.IsDoneKeyword(h.Keyword):
		newClosed = &org.Timestamp{Raw: time.Now().Format("2006-01-02 Mon 15:04")}
	case !org.IsDoneKeyword(keyword) && org.IsDoneKeyword(h.Keyword):
		newClosed = nil
	}

	return &statusChangeAction{
		h:          h,
		f:          m.ws.FileOf(h),
		oldKeyword: h.Keyword,
		newKeyword: keyword,
		oldClosed:  h.Closed,
		newClosed:  newClosed,
	}
}

// applyStatusToHeadlineSet sets keyword on every headline in headlines —
// the shared implementation behind bulk status change, whether the
// selection came from visual mode or a numeric-prefixed R (see
// applyChosenStatus). Unlike bulk delete, a status change never cascades
// to descendants on its own, so every headline given is changed
// independently, not just the topmost ones (callers don't
// topmost-filter). Grouped into one undo step per file touched, same as
// deleteHeadlineSet. In clarify view, also advances past the pinned
// target if it just became DONE/CANCELLED (see
// advanceClarifyTargetIfDone).
func (m *Model) applyStatusToHeadlineSet(headlines []*org.Headline, keyword, label string) {
	headlines, skipped := m.filterImmutable(headlines)
	if len(headlines) == 0 {
		if skipped > 0 {
			m.message = "All selected entries are locked by :format-links; nothing changed"
		}
		return
	}

	var order []*org.File
	byFile := make(map[*org.File][]undoAction)
	for _, h := range headlines {
		f := m.ws.FileOf(h)
		if _, ok := byFile[f]; !ok {
			order = append(order, f)
		}
		byFile[f] = append(byFile[f], m.buildStatusChangeAction(h, keyword))
	}
	for _, f := range order {
		m.pushUndo(&batchAction{actions: byFile[f]})
	}
	m.message = fmt.Sprintf("Set %d entries to %s", len(headlines), label)
	if skipped > 0 {
		m.message += fmt.Sprintf(" (%d skipped: locked by :format-links)", skipped)
	}
	m.advanceClarifyTargetIfDone()
}

// fileHasImmutableHeadline reports whether any headline in f is
// currently locked by an in-flight :format-links batch (see
// m.immutable) — used to refuse a whole-file edit, which would
// otherwise let the user rewrite an entry's raw text out from under the
// batch with no way for finishFormatLinks to detect it.
func (m *Model) fileHasImmutableHeadline(f *org.File) bool {
	found := false
	org.Walk(f.Headlines, func(h *org.Headline) {
		if m.immutable[h] {
			found = true
		}
	})
	return found
}

// refuseIfImmutable reports whether h is currently locked by an
// in-flight :format-links batch (see m.immutable), setting an
// explanatory status message if so. Every single-entry command that
// would mutate h in some way (delete, status change, deadline, edit,
// promote/demote) checks this before doing anything else.
func (m *Model) refuseIfImmutable(h *org.Headline) bool {
	if h == nil || !m.immutable[h] {
		return false
	}
	m.message = "This entry is being formatted by :format-links and can't be changed yet"
	return true
}

// filterImmutable removes any headline currently locked by an in-flight
// :format-links batch from headlines, for a bulk command (visual-mode or
// numeric-prefixed delete/status-change) to apply to the rest rather
// than refusing the whole operation outright. skipped is how many were
// removed, for the caller's summary message.
func (m *Model) filterImmutable(headlines []*org.Headline) (kept []*org.Headline, skipped int) {
	for _, h := range headlines {
		if m.immutable[h] {
			skipped++
			continue
		}
		kept = append(kept, h)
	}
	return kept, skipped
}

// deleteHeadline removes the current headline and its whole subtree
// ("dd"), storing a copy in the register so it can be pasted back with
// p/P. A no-op on file rows, or on an entry locked by :format-links.
// Matches vim's own dd: the cursor stays at the same screen position
// (see pushUndoKeepingCursor) rather than jumping to a tree-sibling.
func (m *Model) deleteHeadline() {
	h := m.currentHeadline()
	if h == nil || m.refuseIfImmutable(h) {
		return
	}
	f, parent, idx := m.ws.Locate(h)
	if idx < 0 {
		return
	}
	m.register = []*org.Headline{h}
	m.pushUndoKeepingCursor(&deleteAction{spliceAction{f: f, parent: parent, index: idx, headlines: []*org.Headline{h}, inTree: true}})

	if m.view == clarifyView && h == m.clarifyTarget {
		m.advanceClarifyTarget()
	}
	// dd removes h's whole subtree, so a mark on any descendant (not
	// just h itself) needs clearing too.
	org.Walk([]*org.Headline{h}, m.clearMarksFor)
}

// yankHeadline ("yy") copies the current headline (and its whole
// subtree) into the register for pasting elsewhere with p/P — unlike
// dd, it leaves the original untouched (in the outline, the agenda, or
// clarify view — wherever the cursor happens to be). The register holds
// an independent snapshot taken now, so later edits to the original
// before pasting aren't reflected in what gets pasted.
func (m *Model) yankHeadline() {
	h := m.currentHeadline()
	if h == nil {
		return
	}
	m.register = []*org.Headline{org.CloneHeadline(h)}
	m.message = "Yanked"
}

// pasteHeadline inserts a copy of the register's contents — one entry
// (dd/yy) or several, in the same order they were deleted/yanked in
// (visual-mode d, <N>dd) — after (before=false, "p") or before
// (before=true, "P") the current row (see resolveInsertPosition),
// adjusting each one's level (and its descendants', by the same amount)
// to fit the destination depth. The register itself is left untouched,
// so it can be pasted again.
func (m *Model) pasteHeadline(before bool) {
	if len(m.register) == 0 {
		m.message = "Nothing to paste"
		return
	}
	f, parent, idx, level, _, ok := m.resolveInsertPosition(before)
	if !ok {
		return
	}

	clones := make([]*org.Headline, len(m.register))
	for i, h := range m.register {
		clone := org.CloneHeadline(h)
		clone.ShiftLevel(level - clone.Level)
		clones[i] = clone
	}
	m.pushUndo(&insertAction{spliceAction{f: f, parent: parent, index: idx, headlines: clones}})
}

// demoteHeadline (">>") nests the current headline (and its whole
// subtree) one level deeper, making it the last child of its previous
// sibling — matching org-mode's own demote-subtree behavior, which is
// also the only way to increase Level while keeping Parent consistent
// with it. A no-op (with a status-line message) if there's no previous
// sibling to nest under, since there's nothing sensible to reparent
// onto.
func (m *Model) demoteHeadline() {
	h := m.currentHeadline()
	if h == nil || m.refuseIfImmutable(h) {
		return
	}
	f, parent, idx := m.ws.Locate(h)
	if idx <= 0 {
		m.message = "Cannot demote: no previous sibling to nest under"
		return
	}
	prevSibling := org.Siblings{File: f, Parent: parent}.List()[idx-1]

	m.pushUndo(&reparentAction{
		h: h, f: f,
		oldParent: parent, oldIndex: idx,
		newParent: prevSibling, newIndex: len(prevSibling.Children),
		delta: 1,
	})
}

// promoteHeadline ("<<") un-nests the current headline (and its
// whole subtree) one level shallower, making it the next sibling of its
// former parent — the exact inverse of demoteHeadline, and org-mode's
// own promote-subtree behavior. A no-op (with a message) if the
// headline is already top-level.
func (m *Model) promoteHeadline() {
	h := m.currentHeadline()
	if h == nil || m.refuseIfImmutable(h) {
		return
	}
	if h.Parent == nil {
		m.message = "Cannot promote: already at the top level"
		return
	}
	f, parent, idx := m.ws.Locate(h)
	grandparent := parent.Parent
	_, _, parentIdx := m.ws.Locate(parent)

	m.pushUndo(&reparentAction{
		h: h, f: f,
		oldParent: parent, oldIndex: idx,
		newParent: grandparent, newIndex: parentIdx + 1,
		delta: -1,
	})
}
