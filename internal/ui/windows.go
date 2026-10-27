package ui

import (
	"slices"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// window is the state each window keeps for itself, as in vim: which view
// it shows, that view's rows, its cursor and scroll position, and its own
// jump list. Everything else — the workspace, undo history, folds,
// registers, marks, search — is shared by every window, so an edit made
// in one shows up in all of them.
//
// Model embeds the active window's state, so code that works on "the"
// cursor or rows works on the active window without knowing splits exist.
// The other windows are parked in the layout tree (see layoutNode) and
// swapped in when they're switched to (see enterWindow) or need updating
// (see withWindow).
type window struct {
	view viewKind
	rows []row

	cursor int
	offset int // index of the first visible row (for scrolling)

	// jumpList/jumpPos implement vim's own jumplist (ctrl-o/"gi" here —
	// see jumpBack/jumpForward): jumpList holds one entry per recorded
	// "before a large move" position, oldest first; jumpPos is where in
	// that list the cursor currently sits — equal to len(jumpList) means
	// "live", i.e. not currently navigating the list at all. See pushJump
	// for how entries get added.
	jumpList []jumpEntry
	jumpPos  int

	// builtWidth is the width rows was last built for (see buildRows), so
	// syncWindows can tell when a static view's rows need rebuilding.
	builtWidth int
}

// clone returns a copy of w that shares no slices with it, for a new
// window opened on the same spot (see splitWindow) — rebuildRows reuses
// rows' backing array, so a shallow copy would be overwritten.
func (w window) clone() window {
	w.rows = slices.Clone(w.rows)
	w.jumpList = slices.Clone(w.jumpList)
	return w
}

// layoutNode is one node of the window layout tree: a leaf holding one
// window, or a split dividing its area evenly among two or more children
// — side by side for :vsplit, stacked for :split. A split never has
// fewer than two children, nor a child split the same way it is (see
// closeWindow, which collapses and merges them), so every level of the
// tree changes direction.
type layoutNode struct {
	parent   *layoutNode
	children []*layoutNode // a split's; nil for a leaf
	vertical bool          // a split's children sit side by side (:vsplit) rather than stacked (:split)

	// win is a leaf's parked window. Stale for the active leaf, whose
	// state lives in Model.window instead.
	win window
}

func (n *layoutNode) isLeaf() bool { return len(n.children) == 0 }

// leaves appends every leaf under n, in screen order (top to bottom,
// left to right), the order "ctrl-w w" cycles through.
func (n *layoutNode) leaves(dst []*layoutNode) []*layoutNode {
	if n.isLeaf() {
		return append(dst, n)
	}
	for _, c := range n.children {
		dst = c.leaves(dst)
	}
	return dst
}

// replaceChild puts repl in old's place among n's children — or, with n
// nil (old is the root), returns repl as the new root; otherwise root.
func (n *layoutNode) replaceChild(old, repl, root *layoutNode) *layoutNode {
	repl.parent = n
	if n == nil {
		return repl
	}
	n.children[slices.Index(n.children, old)] = repl
	return root
}

// pane is where one window sits on screen: its leaf, its top-left corner
// and its size. h counts the window's own info buffer and status line,
// its last lines.
type pane struct {
	leaf       *layoutNode
	x, y, w, h int
}

// splitSizes divides total cells among n children with gap cells between
// each neighboring pair, as evenly as possible — vim's 'equalalways',
// with the leftover cells going to the first children.
func splitSizes(total, n, gap int) []int {
	avail := max(total-gap*(n-1), 0)
	sizes := make([]int, n)
	for i := range sizes {
		sizes[i] = avail / n
		if i < avail%n {
			sizes[i]++
		}
	}
	return sizes
}

// layoutPanes appends the pane of every leaf under n, given the rectangle
// n fills. Side-by-side windows are separated by a one-column bar (see
// splitView); stacked ones need nothing, since each one's status line
// already divides it from the next.
func (n *layoutNode) layoutPanes(x, y, w, h int, dst []pane) []pane {
	if n.isLeaf() {
		return append(dst, pane{leaf: n, x: x, y: y, w: w, h: h})
	}
	if n.vertical {
		for i, size := range splitSizes(w, len(n.children), 1) {
			dst = n.children[i].layoutPanes(x, y, size, h, dst)
			x += size + 1
		}
		return dst
	}
	for i, size := range splitSizes(h, len(n.children), 0) {
		dst = n.children[i].layoutPanes(x, y, w, size, dst)
		y += size
	}
	return dst
}

// panes lays out every window over the screen above the command line.
// Each window's info buffer is inside its own pane (see paneLines), so
// the layout depends only on the terminal's size: a split stays put as
// the info buffers come and go. Nil without splits.
func (m *Model) panes() []pane {
	if m.layout == nil {
		return nil
	}
	return m.layout.layoutPanes(0, 0, m.width, m.height-1, nil)
}

// activePane returns the pane the embedded window is in (see fixedPane),
// or false without splits — the one window then has the whole screen.
func (m *Model) activePane() (pane, bool) {
	if m.fixedPane != nil {
		return *m.fixedPane, true
	}
	for _, p := range m.panes() {
		if p.leaf == m.activeWin {
			return p, true
		}
	}
	return pane{}, false
}

// listWidth is how wide the embedded window's rows are drawn: its pane's
// width with splits, otherwise the terminal's.
func (m *Model) listWidth() int {
	if p, ok := m.activePane(); ok && m.width > 0 {
		return p.w
	}
	return m.width
}

// rebuildKeepingCursor rebuilds the window's rows (see buildRows) and puts
// the cursor back on the row it was on, if that row is still there —
// otherwise it keeps its index, as vim does when the lines under the
// cursor go away. The scroll offset is kept on its row the same way, so
// rows added or removed above the window don't shift what it shows: if
// the top row itself went away, the first row after it that's still
// there becomes the top, as vim adjusts 'topline'. Only if nothing that
// was on screen is left does the offset keep its index. A row folded
// away (see relocateRow) counts as still there, on its fold's row.
func (m *Model) rebuildKeepingCursor() {
	var anchor row
	hasAnchor := m.cursor >= 0 && m.cursor < len(m.rows)
	if hasAnchor {
		anchor = m.rows[m.cursor]
	}
	// Copied, since buildRows reuses rows' backing array.
	var shown []row
	if m.offset >= 0 && m.offset < len(m.rows) {
		shown = append(shown, m.rows[m.offset:m.offset+m.visibleRowCount(m.offset)]...)
	}
	m.buildRows()
	if hasAnchor {
		if i := m.relocateRow(anchor); i >= 0 {
			m.cursor = i
		}
	}
	for _, r := range shown {
		if i := m.relocateRow(r); i >= 0 {
			m.offset = i
			break
		}
	}
	m.ensureVisible()
}

// relocateRow returns r's index in the rebuilt rows, or, if a fold now
// hides it, the index of the folded headline hiding it — as vim keeps
// the cursor on a closed fold's line. -1 if r is gone altogether.
func (m *Model) relocateRow(r row) int {
	if i := indexOfRow(m.rows, r); i >= 0 || r.headline == nil {
		return i
	}
	h := r.headline // a body line is hidden by its own headline's fold
	if r.kind != rowBody {
		h = h.Parent
	}
	for ; h != nil; h = h.Parent {
		if m.collapsed[h] {
			if i := indexOfRow(m.rows, row{headline: h}); i >= 0 {
				return i
			}
		}
	}
	return -1
}

// withWindow runs f with p's parked window swapped in as the embedded
// one, then parks it again. fixedPane says which pane it's in, for the
// duration; and the mode is normal, since a prompt's or a visual
// selection's state (and its info-buffer sections, which take up room in
// the pane) belong to the active window.
func (m *Model) withWindow(p pane, f func()) {
	active, mode := m.window, m.mode
	m.window = p.leaf.win
	m.fixedPane = &p
	m.mode = normalMode
	f()
	p.leaf.win = m.window
	m.window, m.mode = active, mode
	m.fixedPane = nil
}

// syncWindows brings every inactive window up to date after Update: the
// workspace may have changed under it, or its pane been resized. Each is
// rebuilt with its cursor kept on the same row (see rebuildKeepingCursor)
// — except a static view's (see viewSpec.static), whose rows don't
// depend on the workspace, unless its width changed.
func (m *Model) syncWindows() {
	for _, p := range m.panes() {
		if p.leaf == m.activeWin {
			continue
		}
		m.withWindow(p, func() {
			if m.spec().static && m.builtWidth == m.listWidth() {
				return
			}
			m.rebuildKeepingCursor()
		})
	}
}

// splitWindow is :split (vertical: :vsplit): opens a new window on the
// same view and entry as the current one, above it (to its left), and
// makes it active — as vim does without 'splitbelow'/'splitright'.
func (m *Model) splitWindow(vertical bool) {
	if m.layout == nil {
		m.layout = &layoutNode{}
		m.activeWin = m.layout
	}
	cur := m.activeWin
	fresh := &layoutNode{}
	if p := cur.parent; p != nil && p.vertical == vertical {
		fresh.parent = p
		p.children = slices.Insert(p.children, slices.Index(p.children, cur), fresh)
	} else {
		split := &layoutNode{vertical: vertical}
		m.layout = p.replaceChild(cur, split, m.layout)
		split.children = []*layoutNode{fresh, cur}
		fresh.parent, cur.parent = split, split
	}
	cur.win = m.window.clone()
	m.prevWin, m.activeWin = cur, fresh
	m.rebuildKeepingCursor() // narrower now, so body lines rewrap
}

// closeWindow is :close: closes the active window, giving its space — and
// the cursor — to the window before it (above, or to the left), or the
// one after if it was first. Refuses to close the last window.
func (m *Model) closeWindow() {
	if m.layout == nil {
		m.message = "Cannot close last window"
		return
	}
	cur := m.activeWin
	p := cur.parent
	i := slices.Index(p.children, cur)
	p.children = slices.Delete(p.children, i, i+1)

	// The nearest window on the side that takes over the space.
	next, fromBefore := p.children[max(i-1, 0)], i > 0
	for !next.isLeaf() {
		if fromBefore {
			next = next.children[len(next.children)-1]
		} else {
			next = next.children[0]
		}
	}

	if len(p.children) == 1 {
		// A split of one is no split: its child takes its place, merging
		// into the grandparent if they split the same way.
		only := p.children[0]
		gp := p.parent
		if gp != nil && !only.isLeaf() && only.vertical == gp.vertical {
			j := slices.Index(gp.children, p)
			gp.children = slices.Replace(gp.children, j, j+1, only.children...)
			for _, c := range only.children {
				c.parent = gp
			}
		} else {
			m.layout = gp.replaceChild(p, only, m.layout)
		}
	}

	m.window = next.win
	next.win = window{}
	m.activeWin = next
	if m.prevWin == cur {
		m.prevWin = nil
	}
	if m.layout.isLeaf() {
		m.layout, m.activeWin, m.prevWin = nil, nil, nil
	}
	m.rebuildKeepingCursor() // wider now
}

// onlyWindow is :only: closes every window but the active one.
func (m *Model) onlyWindow() {
	if m.layout == nil {
		return
	}
	m.layout, m.activeWin, m.prevWin = nil, nil, nil
	m.rebuildKeepingCursor()
}

// enterWindow makes target the active window, parking the current one.
func (m *Model) enterWindow(target *layoutNode) {
	if target == nil || target == m.activeWin {
		return
	}
	m.activeWin.win = m.window
	m.window = target.win
	target.win = window{}
	m.prevWin, m.activeWin = m.activeWin, target
	m.ensureVisible()
}

// cycleWindow is "ctrl-w w" (delta 1) and "ctrl-w W" (delta -1): the
// next/previous window in screen order, wrapping around.
func (m *Model) cycleWindow(delta int) {
	if m.layout == nil {
		return
	}
	all := m.layout.leaves(nil)
	i := slices.Index(all, m.activeWin)
	m.enterWindow(all[(i+delta+len(all))%len(all)])
}

// edgeWindow is "ctrl-w t" (first) and "ctrl-w b" (last): the top-left
// or bottom-right window.
func (m *Model) edgeWindow(last bool) {
	if m.layout == nil {
		return
	}
	all := m.layout.leaves(nil)
	if last {
		m.enterWindow(all[len(all)-1])
	} else {
		m.enterWindow(all[0])
	}
}

// moveToWindow is "ctrl-w h/j/k/l": the window next to the active one in
// that direction (dx, dy each -1, 0 or 1). Like vim, where several
// windows border that side it picks the one level with the cursor's own
// row (for h/l) or the window's left edge (for j/k). A no-op at the edge
// of the screen.
func (m *Model) moveToWindow(dx, dy int) {
	panes := m.panes()
	i := slices.IndexFunc(panes, func(p pane) bool { return p.leaf == m.activeWin })
	if i < 0 {
		return
	}
	cur := panes[i]
	refX := cur.x
	refY := cur.y + min(max(m.cursor-m.offset, 0), max(cur.h-2, 0))
	for _, p := range panes {
		var ok bool
		switch {
		case dx > 0:
			ok = p.x == cur.x+cur.w+1 && p.y <= refY && refY < p.y+p.h
		case dx < 0:
			ok = p.x+p.w+1 == cur.x && p.y <= refY && refY < p.y+p.h
		case dy > 0:
			ok = p.y == cur.y+cur.h && p.x <= refX && refX <= p.x+p.w
		case dy < 0:
			ok = p.y+p.h == cur.y && p.x <= refX && refX <= p.x+p.w
		}
		if ok {
			m.enterWindow(p.leaf)
			return
		}
	}
}

// splitView is View with more than one window: each window's listing,
// info buffer and status line, side by side or stacked as the layout
// says, then the one command line below them all.
func (m Model) splitView() string {
	panes := m.panes()
	lines := make(map[*layoutNode][]string, len(panes))
	for _, p := range panes {
		lines[p.leaf] = m.paneLines(p)
	}
	var b strings.Builder
	for _, line := range m.composeLayout(m.layout, m.width, m.height-1, lines) {
		b.WriteString(line)
		b.WriteString("\n")
	}
	m.writeBottom(&b, true)
	return b.String()
}

// paneLines renders the window in p — its listing, then its info buffer
// and status line — as exactly p.h lines, each exactly p.w cells wide.
// If the info buffer leaves no room for the listing at all, the listing
// gives way, then the top of the info buffer; never the status line.
func (m Model) paneLines(p pane) []string {
	active := p.leaf == m.activeWin
	if !active {
		m.window = p.leaf.win
		m.mode = normalMode // prompts and visual selections belong to the active window
	}
	m.width = p.w
	m.fixedPane = &p
	var b strings.Builder
	m.writeInfoAndStatus(&b, active)
	bottom := strings.Split(strings.TrimSuffix(b.String(), "\n"), "\n")
	listing := strings.Split(strings.TrimSuffix(m.listing(active), "\n"), "\n")
	lines := append(listing[:min(len(listing), max(p.h-len(bottom), 0))], bottom...)
	lines = lines[max(len(lines)-p.h, 0):]
	for i := range lines {
		lines[i] = fitCells(lines[i], p.w)
	}
	for len(lines) < p.h {
		lines = append(lines, strings.Repeat(" ", p.w))
	}
	return lines
}

// composeLayout lays out n's windows' already-rendered lines (see
// paneLines) into w×h, splitting it the same way layoutPanes does.
func (m Model) composeLayout(n *layoutNode, w, h int, lines map[*layoutNode][]string) []string {
	if n.isLeaf() {
		return lines[n]
	}
	if !n.vertical {
		var out []string
		for i, size := range splitSizes(h, len(n.children), 0) {
			out = append(out, m.composeLayout(n.children[i], w, size, lines)...)
		}
		return out
	}
	sep := m.statusStyle().Render("│")
	out := make([]string, h)
	for i, size := range splitSizes(w, len(n.children), 1) {
		for y, line := range m.composeLayout(n.children[i], size, h, lines) {
			if i > 0 {
				out[y] += sep
			}
			out[y] += line
		}
	}
	return out
}

// fitCells pads line with spaces, or truncates it, to exactly w cells, so
// side-by-side windows line up.
func fitCells(line string, w int) string {
	if cw := lipgloss.Width(line); cw < w {
		return line + strings.Repeat(" ", w-cw)
	} else if cw > w {
		return ansi.Truncate(line, w, "")
	}
	return line
}
