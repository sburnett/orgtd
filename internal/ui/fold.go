package ui

import (
	"github.com/sburnett/orgtd/internal/org"
)

// toggleFold ("za"/Tab) toggles whether the current headline's own
// children are hidden, one level.
func (m *Model) toggleFold() {
	h := m.currentHeadline()
	if h == nil || !hasFoldableContent(h) {
		return
	}
	m.collapsed[h] = !m.collapsed[h]
	m.rebuildRows()
}

// foldOpen ("zo") reveals the current headline's own children, one level.
func (m *Model) foldOpen() {
	h := m.currentHeadline()
	if h == nil || !hasFoldableContent(h) {
		return
	}
	m.collapsed[h] = false
	m.rebuildRows()
}

// foldClose ("zc") hides the current headline's own children, one level.
func (m *Model) foldClose() {
	h := m.currentHeadline()
	if h == nil || !hasFoldableContent(h) {
		return
	}
	m.collapsed[h] = true
	m.rebuildRows()
}

// foldOpenAll ("zO") reveals the current headline's entire subtree,
// recursively.
func (m *Model) foldOpenAll() {
	h := m.currentHeadline()
	if h == nil || !hasFoldableContent(h) {
		return
	}
	setCollapsedRecursive(m.collapsed, h, false)
	m.rebuildRows()
}

// foldCloseAll ("zC") hides the current headline's entire subtree,
// recursively — every descendant with children is marked collapsed too,
// so a later single-level zo doesn't reveal an inconsistent half-open
// state.
func (m *Model) foldCloseAll() {
	h := m.currentHeadline()
	if h == nil || !hasFoldableContent(h) {
		return
	}
	setCollapsedRecursive(m.collapsed, h, true)
	m.rebuildRows()
}

// foldToggleAll ("zA") opens the current headline's entire subtree
// recursively if it's currently folded, or closes it entirely
// recursively otherwise.
func (m *Model) foldToggleAll() {
	h := m.currentHeadline()
	if h == nil || !hasFoldableContent(h) {
		return
	}
	setCollapsedRecursive(m.collapsed, h, !m.collapsed[h])
	m.rebuildRows()
}

// setCollapsedRecursive sets collapsed[x] = value for h and every
// descendant of h that has foldable content (a headline with neither
// children nor a body has nothing to fold, so it's left out of the map).
func setCollapsedRecursive(collapsed map[*org.Headline]bool, h *org.Headline, value bool) {
	if hasFoldableContent(h) {
		collapsed[h] = value
	}
	for _, c := range h.Children {
		setCollapsedRecursive(collapsed, c, value)
	}
}
