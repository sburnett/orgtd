package ui

// moveCursor moves the cursor by delta entries — not delta rows — for
// j/k and the half-page scroll (ctrl+d/ctrl+u) alike: an entry's body
// lines are part of the entry, not separately steppable rows of their
// own, so they're always skipped over and never landed on.
func (m *Model) moveCursor(delta int) {
	if len(m.rows) == 0 {
		return
	}
	step := 1
	n := delta
	if delta < 0 {
		step = -1
		n = -delta
	}
	cur := m.cursor
	for n > 0 {
		next := cur + step
		if next < 0 || next >= len(m.rows) {
			break
		}
		cur = next
		if m.rows[cur].kind != rowBody {
			n--
		}
	}
	// Only reachable by hitting the very end of the list mid-body (the
	// last entry's trailing body line can be the last row overall) —
	// snap to its owning headline rather than resting on it.
	m.cursor = m.entryStart(cur)
}

// entryStart returns the row where the entry owning row i actually
// begins: i itself, or — if i is one of that entry's own body lines —
// the entry's title row.
func (m *Model) entryStart(i int) int {
	for i > 0 && m.rows[i].kind == rowBody {
		i--
	}
	return i
}

// rowLevel returns the indentation level of the row at index i: 0 for a
// file header, or the headline's level otherwise.
// rowLevel returns the indentation/structural level of the row at index
// i — h.Level for an outline headline row, 0 for a file/section-header
// row, 1 for an agenda item row (see row.level).
func (m *Model) rowLevel(i int) int {
	return m.rows[i].level
}

// moveToLevel moves the cursor to the next (dir>0) or previous (dir<0)
// row at level lvl, skipping over any deeper rows along the way. If none
// remain at lvl, this lands on the next shallower row instead — "hopping
// up" progressively until it finds one (or runs off the end/start of the
// list, in which case it's a no-op).
func (m *Model) moveToLevel(dir, lvl int) {
	if len(m.rows) == 0 {
		return
	}
	i := m.cursor + dir
	for i >= 0 && i < len(m.rows) && (m.rowLevel(i) > lvl || m.rows[i].kind == rowBody) {
		// A body line is never a valid stopping point here — it's not a
		// sibling or a hop-up target, just supplementary text — even on
		// the rare occasion its level happens to coincide with lvl (an
		// unrelated, shallower headline's body).
		i += dir
	}
	if i >= 0 && i < len(m.rows) {
		m.cursor = i
	}
}

// moveSiblingLevel moves the cursor to the next (dir>0) or previous
// (dir<0) row at the same indentation level as the current row (see
// moveToLevel). Used as the fallback for l/h when there's no
// deeper/shallower row to move into.
func (m *Model) moveSiblingLevel(dir int) {
	if len(m.rows) == 0 {
		return
	}
	m.moveToLevel(dir, m.rowLevel(m.cursor))
}

// jumpParagraph ("}"/"{", mirroring vim's paragraph motions) is like
// moveSiblingLevel, except a leaf (no children) is always treated as one
// level shallower than it actually is, so it hops up immediately rather
// than stepping through remaining leaf siblings one at a time — j/k
// already move between those just as well, one row at a time.
func (m *Model) jumpParagraph(dir int) {
	if len(m.rows) == 0 {
		return
	}
	m.pushJump()
	lvl := m.rowLevel(m.cursor)
	if r := m.rows[m.cursor]; r.headline != nil && len(r.headline.Children) == 0 {
		lvl--
	}
	m.moveToLevel(dir, lvl)
}

// moveDeeper moves the cursor into the next deeper indentation level
// (i.e. the current row's first visible child), or if there is none,
// falls back to moveSiblingLevel(1).
func (m *Model) moveDeeper() {
	if len(m.rows) == 0 {
		return
	}
	// Skip over any body lines right after the cursor — they're part of
	// the current entry, not something to move "into" — to find the
	// first real child, if any.
	next := m.cursor + 1
	for next < len(m.rows) && m.rows[next].kind == rowBody {
		next++
	}
	if next < len(m.rows) && m.rowLevel(next) > m.rowLevel(m.cursor) {
		m.cursor = next
		return
	}
	m.moveSiblingLevel(1)
}

// moveShallower moves the cursor one structural level up — regardless of
// where that lands relative to the current row, mirroring moveDeeper's
// directness: to the current headline's parent if it's nested, or to its
// file's header row if it's top-level. On a file row already (nothing
// shallower than a file), it moves to the previous file's header row
// instead, so h never just leaves the cursor stuck in place.
func (m *Model) moveShallower() {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return
	}
	if m.rows[m.cursor].file != nil {
		// Nothing shallower than a file row: hop to the previous file
		// instead of leaving the cursor stuck in place.
		m.moveSiblingLevel(-1)
		return
	}
	// A headline row's parent (or file, if top-level) is always its
	// nearest shallower row (jumpToSubtreeTop) — and for a body-line
	// row (level h.Level+1), that's h's own row, which is exactly what
	// "one level up from inside h's body" should mean.
	m.jumpToSubtreeTop()
}

// jumpToSubtreeTop ("^") moves the cursor to the nearest preceding row
// with a shallower level than the current row — the enclosing parent
// headline row in outline view (or the file/section header), and the
// enclosing section header in agenda view — a no-op if already at the
// shallowest level present (a file or section-header row).
//
// This is a row scan rather than a tree-pointer lookup (parent.Level,
// etc.) so it works uniformly across outline and agenda rows: a visible
// row's nearest shallower predecessor is always its logical container,
// since a row is only visible when every ancestor row before it is too.
func (m *Model) jumpToSubtreeTop() {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return
	}
	cur := m.rowLevel(m.cursor)
	for i := m.cursor - 1; i >= 0; i-- {
		if m.rowLevel(i) < cur {
			m.cursor = i
			return
		}
	}
}

// jumpToSubtreeBottom ("$") moves the cursor to the last row exactly one
// level deeper than the current row, within the current row's own span
// (its last direct child in outline view, or its section's last item in
// agenda view) — a no-op if there's no such row. This is the exact
// inverse of jumpToSubtreeTop ("^"): each press moves exactly one level,
// so pressing "$" repeatedly drills progressively deeper, bottoming out
// once it reaches a leaf. See jumpToSubtreeTop for why this is a row
// scan rather than a tree-pointer lookup.
func (m *Model) jumpToSubtreeBottom() {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return
	}
	cur := m.rowLevel(m.cursor)
	last := -1
	for i := m.cursor + 1; i < len(m.rows) && m.rowLevel(i) > cur; i++ {
		// Body lines don't count as a "last child" to land on — an
		// entry with a body but no real children has nothing deeper to
		// drill into, same as a plain leaf.
		if m.rowLevel(i) == cur+1 && m.rows[i].kind != rowBody {
			last = i
		}
	}
	if last >= 0 {
		m.cursor = last
	}
}

// pageSize is a rough, static estimate of how many rows a page holds,
// reserving a full-list worst-case amount of room for section-separator
// blank lines (see sectionSeparatorBudget) alongside the bottom status/
// command-line area (see statusHeight). Used only as a scroll-jump
// size (ctrl-d/ctrl-u/PageUp/PageDown) — close enough for "move roughly
// one screen's worth of rows". The actual visible window (used by
// View(), ensureVisible, and scrollView) is computed precisely instead,
// by contentBudget/visibleRowCount: pageSize's static reservation is
// only ever a lower bound on what really fits (safe for a jump size,
// since jumping a little short of a full screen is harmless), but it
// can be far too conservative when a view has many more section
// boundaries than fit on one page (e.g. :calendar with more days than
// rows available) — most of them live on other pages, so reserving
// room here for every boundary in the whole list would under-fill the
// actual screen.
func (m *Model) pageSize() int {
	n := m.height - m.statusHeight() - m.sectionSeparatorBudget() - m.infoBufferHeight()
	if n < 1 {
		n = 1
	}
	return n
}

// contentBudget is exactly how many terminal lines the scrollable
// content area may occupy: the screen height minus the info buffer and
// the bottom status/command-line area — with no separate reservation
// for section-separator blank lines, unlike pageSize. Used by
// visibleRowCount to work out precisely how many rows fit from a given
// starting row, and directly as the padding target in View().
func (m *Model) contentBudget() int {
	n := m.height - m.statusHeight() - m.infoBufferHeight()
	if n < 1 {
		n = 1
	}
	return n
}

// visibleRowCount returns how many rows, starting at start, actually
// fit within contentBudget — counting the blank separator line View()
// prints before every section-header row after the first one shown (2
// lines total for such a row, 1 for every other row) — so a page always
// shows as many rows as truly fit, rather than reserving room for every
// section boundary in the whole list up front (see pageSize). 0 if
// start is out of range.
func (m *Model) visibleRowCount(start int) int {
	if start < 0 || start >= len(m.rows) {
		return 0
	}
	budget := m.contentBudget()
	used, count := 0, 0
	for i := start; i < len(m.rows); i++ {
		cost := 1
		if i > start && m.rows[i].kind == rowSection {
			cost = 2 // its own line, plus the blank separator before it
		}
		if used+cost > budget {
			break
		}
		used += cost
		count++
	}
	return count
}

// ensureVisible scrolls so the cursor's whole entry — its own row plus
// any of its own body lines (see entryEnd), the same span View()
// highlights as one unit — fits on screen when possible, not just the
// cursor's own row. If the entry itself is taller than a page, showing
// all of it is impossible either way, so this falls back to keeping at
// least the cursor's own row visible, rather than scrolling past it to
// chase an unreachable tail.
func (m *Model) ensureVisible() {
	// Enforced here, the one chokepoint every key handler in
	// updateNormalMode passes through before returning: the cursor never
	// rests on a body line, regardless of which command moved it — an
	// entry's body is part of the entry, not a separately-landable row,
	// for every command alike (not just j/k).
	m.cursor = m.entryStart(m.cursor)

	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	// Nudge offset forward one row at a time until the cursor's entry
	// end is within the window visibleRowCount(offset) actually shows
	// from there — precise, since (unlike a static pageSize-based jump)
	// it accounts for however many section-separator blank lines really
	// fall within that specific window. Capped at m.cursor: if the
	// entry itself is taller than a page, showing all of it is
	// impossible either way, so this stops advancing once the cursor's
	// own row would be pushed out, rather than scrolling past it to
	// chase an unreachable tail.
	end := m.entryEnd(m.cursor)
	for m.offset < m.cursor && m.offset+m.visibleRowCount(m.offset) <= end {
		m.offset++
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

// entryEnd returns the last row belonging to the same entry as row i —
// i itself, plus any of its own body lines immediately following it.
func (m *Model) entryEnd(i int) int {
	if i < 0 || i >= len(m.rows) {
		return i
	}
	ch := m.rows[i].headline
	end := i
	for end+1 < len(m.rows) && m.rows[end+1].kind == rowBody && m.rows[end+1].headline == ch {
		end++
	}
	return end
}

// centerOnCursor scrolls the viewport so the cursor's row sits as close to
// the middle of the screen as the top/bottom of the row list allows —
// unlike ensureVisible, which only nudges the offset the minimum amount
// needed to bring the cursor back on screen. Used where landing in the
// middle of the page, rather than merely somewhere on it, matters (e.g.
// enterCalendarView's in-progress-meeting cursor).
func (m *Model) centerOnCursor() {
	if len(m.rows) == 0 {
		return
	}
	offset := m.cursor - m.contentBudget()/2
	if offset < 0 {
		offset = 0
	}
	if max := len(m.rows) - 1; offset > max {
		offset = max
	}
	m.offset = offset
}

// scrollView shifts the viewport by delta lines (positive scrolls the view
// down, negative scrolls it up) independently of the cursor — Ctrl-E and
// Ctrl-Y, like vim, move the window a single line at a time and leave the
// cursor right where it was, only dragging it along when the scroll would
// otherwise push it off the newly visible window (off the top when
// scrolling down, off the bottom when scrolling up). Callers must skip the
// usual cursor-driven m.ensureVisible() afterward, since that would just
// recompute the offset from the cursor and undo the scroll.
func (m *Model) scrollView(delta int) {
	if len(m.rows) == 0 {
		return
	}
	offset := m.offset + delta
	if offset < 0 {
		offset = 0
	}
	if max := len(m.rows) - 1; offset > max {
		offset = max
	}
	m.offset = offset

	bottom := m.offset + m.visibleRowCount(m.offset) - 1
	if m.cursor < m.offset {
		m.cursor = m.entryStart(m.offset)
	} else if m.entryEnd(m.cursor) > bottom {
		m.cursor = m.entryStart(bottom)
	}
}
