package ui

import (
	"github.com/sburnett/orgtd/internal/org"
)

// takeCount consumes the pending numeric prefix, defaulting to 1.
func (m *Model) takeCount() int {
	n := m.pendingCount
	m.pendingCount = 0
	if n < 1 {
		return 1
	}
	return n
}

// toggleFold ("za"/Tab) is vim's za: on a closed fold it opens it (see
// foldOpen), otherwise it closes (see foldClose). A count is passed through
// to whichever of the two it picks.
func (m *Model) toggleFold() {
	h := m.currentHeadline()
	if h != nil && m.hasFoldableContent(h) && m.collapsed[h] {
		m.foldOpen()
		return
	}
	m.foldClose()
}

// foldOpen ("zo") reveals the current headline's own children, one level.
// With a count N, opens N levels: the headline and its descendants down to
// N-1 levels below it.
func (m *Model) foldOpen() {
	n := m.takeCount()
	h := m.currentHeadline()
	if h == nil || !m.hasFoldableContent(h) {
		return
	}
	openLevels(m.collapsed, h, n)
	m.rebuildRows()
}

// foldClose ("zc") hides the current headline's own children, one level.
// Like vim, a headline with nothing open to close (already folded, or with
// nothing to fold) closes its parent instead, moving the cursor there. A
// count N repeats that N times, so it closes the headline and then walks
// up through its ancestors.
func (m *Model) foldClose() {
	for n := m.takeCount(); n > 0; n-- {
		h := m.currentHeadline()
		if h == nil {
			return
		}
		if !m.hasFoldableContent(h) || m.collapsed[h] {
			h = h.Parent
			if h == nil {
				return
			}
		}
		m.collapsed[h] = true
		m.rebuildRows()
		m.cursorToHeadline(h)
	}
}

// cursorToHeadline moves the cursor to h's own row, if it's visible.
func (m *Model) cursorToHeadline(h *org.Headline) {
	for i, r := range m.rows {
		if r.headline == h {
			m.cursor = i
			return
		}
	}
}

// openLevels uncollapses h and, while levels remain, its descendants.
func openLevels(collapsed map[*org.Headline]bool, h *org.Headline, levels int) {
	if hasAnyContent(h) {
		collapsed[h] = false
	}
	if levels > 1 {
		for _, c := range h.Children {
			openLevels(collapsed, c, levels-1)
		}
	}
}

// subtreeFoldTargets returns the subtrees the recursive fold commands
// (zO/zC/zA) act on: the current headline's own, if it has anything to
// fold, or on a file's header row, each of the file's top-level
// headlines that does — the file itself can't be folded, so the
// one-level commands (zo/zc/za) do nothing there.
func (m *Model) subtreeFoldTargets() []*org.Headline {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return nil
	}
	r := m.rows[m.cursor]
	candidates := []*org.Headline{r.headline}
	if r.kind == rowFile {
		candidates = r.file.Headlines
	}
	var targets []*org.Headline
	for _, h := range candidates {
		if h != nil && !m.hiddenAsStaleDone(h) && m.hasFoldableContent(h) {
			targets = append(targets, h)
		}
	}
	return targets
}

// setSubtreeFolds sets every subtree subtreeFoldTargets returns entirely
// open or closed (see setCollapsedRecursive).
func (m *Model) setSubtreeFolds(targets []*org.Headline, collapsed bool) {
	m.pendingCount = 0
	if len(targets) == 0 {
		return
	}
	for _, h := range targets {
		setCollapsedRecursive(m.collapsed, h, collapsed)
	}
	m.rebuildRows()
}

// foldOpenAll ("zO") reveals the current headline's entire subtree,
// recursively — on a file's row, every one of its headlines' subtrees.
func (m *Model) foldOpenAll() {
	m.setSubtreeFolds(m.subtreeFoldTargets(), false)
}

// foldCloseAll ("zC") hides the current headline's entire subtree,
// recursively — every descendant with children is marked collapsed too,
// so a later single-level zo doesn't reveal an inconsistent half-open
// state. On a file's row, it closes every one of its headlines' subtrees.
func (m *Model) foldCloseAll() {
	m.setSubtreeFolds(m.subtreeFoldTargets(), true)
}

// foldToggleAll ("zA") opens the current headline's entire subtree
// recursively if it's currently folded, or closes it entirely
// recursively otherwise. On a file's row, it opens every one of its
// headlines' subtrees if all of them are folded, and closes them all
// otherwise, so the file ends up uniformly one way or the other.
func (m *Model) foldToggleAll() {
	targets := m.subtreeFoldTargets()
	allFolded := true
	for _, h := range targets {
		allFolded = allFolded && m.collapsed[h]
	}
	m.setSubtreeFolds(targets, !allFolded)
}

// setCollapsedRecursive sets collapsed[x] = value for h and every
// descendant of h that has foldable content (a headline with neither
// children nor a body has nothing to fold, so it's left out of the map).
func setCollapsedRecursive(collapsed map[*org.Headline]bool, h *org.Headline, value bool) {
	if hasAnyContent(h) {
		collapsed[h] = value
	}
	for _, c := range h.Children {
		setCollapsedRecursive(collapsed, c, value)
	}
}
