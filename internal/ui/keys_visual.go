package ui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/sburnett/orgtd/internal/org"
)

// updateVisualMode handles "V" (visual line selection): plain navigation
// keys extend the selection (from visualAnchor to the cursor, snapped to
// whole entries — see visualRange) exactly as they move the cursor in
// normal mode, while "d", "y", and "r"/"R" act on every entry currently
// selected. Only a subset of normal mode's keys apply here — anything that
// isn't navigation or one of the bulk operations (editing a single entry,
// folding, marks, paste, ...) has no obvious bulk meaning and is left
// unbound rather than guessed at.
func (m Model) updateVisualMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	wasPendingG := m.pendingG
	m.pendingG = false
	m.message = ""

	switch key {
	case "esc", "V":
		m.exitVisualMode()

	case "j", "down":
		m.moveCursor(1)

	case "k", "up":
		m.moveCursor(-1)

	case "}":
		m.jumpParagraph(1)

	case "{":
		m.jumpParagraph(-1)

	case "l":
		m.moveDeeper()

	case "h":
		m.moveShallower()

	case "g":
		if wasPendingG {
			m.cursor = 0
		} else {
			m.pendingG = true
		}

	case "G":
		if n := len(m.rows); n > 0 {
			m.cursor = m.entryStart(n - 1)
		}

	case "^":
		m.jumpToSubtreeTop()

	case "$":
		m.jumpToSubtreeBottom()

	case "ctrl+d":
		m.moveCursor(m.pageSize() / 2)

	case "ctrl+u":
		m.moveCursor(-m.pageSize() / 2)

	case "pgdown":
		m.moveCursor(m.pageSize())

	case "pgup":
		m.moveCursor(-m.pageSize())

	case "ctrl+e":
		m.scrollView(1)
		return m, nil

	case "ctrl+y":
		m.scrollView(-1)
		return m, nil

	case "d":
		m.deleteVisualSelection()

	case "y":
		m.yankVisualSelection()

	case "r", "R":
		if headlines := m.visualSelectedHeadlines(); len(headlines) > 0 {
			m.mode = selectMode
			m.selectModeTargets = headlines
			m.selectFilter = ""
			m.selectIndex = m.currentStatusIndex()
		}
	}

	m.ensureVisible()
	return m, nil
}

// exitVisualMode leaves visual selection and returns to normal mode —
// used by Esc/V (cancel) and once a bulk operation (d, or R after a
// status is chosen) completes.
func (m *Model) exitVisualMode() {
	m.mode = normalMode
	m.selectModeTargets = nil
}

// visualRange returns the current visual selection's row range,
// inclusive, snapped (via entryStart/entryEnd, the same snapping the
// single-cursor highlight uses) so it always covers whole entries —
// never starting or ending mid-body.
func (m *Model) visualRange() (start, end int) {
	a, b := m.visualAnchor, m.cursor
	if a > b {
		a, b = b, a
	}
	return m.entryStart(a), m.entryEnd(b)
}

// countRowRange returns the row range covering n consecutive entries
// starting at the cursor's own entry, which counts as the first of the
// n — giving "dd"/"r"/"R" a numeric prefix (e.g. "3dd", "2R") the same
// row-range shape as a visual selection, so both can share
// headlinesInRowRange and the bulk operations built on it. n < 1 is
// treated as 1 (just the current entry, i.e. no prefix). Running out of
// rows before reaching n just stops at the last entry there is, the same
// way vim's own counted commands clamp at the end of the buffer.
func (m *Model) countRowRange(n int) (start, end int) {
	if n < 1 {
		n = 1
	}
	start = m.entryStart(m.cursor)
	end = start
	counted := 1
	for i := start + 1; i < len(m.rows) && counted < n; i++ {
		if m.rows[i].kind == rowBody {
			continue
		}
		end = i
		counted++
	}
	return start, m.entryEnd(end)
}

// visualSelectedHeadlines returns every distinct headline with a row
// inside the current visual selection — see headlinesInRowRange.
func (m *Model) visualSelectedHeadlines() []*org.Headline {
	return m.headlinesInRowRange(m.visualRange())
}

// headlinesInRowRange returns every distinct headline with a row inside
// [start, end], in top-to-bottom order — shared by visualSelectedHeadlines
// (a visual-mode selection) and the numeric-prefix commands (via
// countRowRange). A selected headline may contribute several rows (its
// body, its children), but appears once here regardless; rows with no
// headline (file/section/:config rows) are skipped.
func (m *Model) headlinesInRowRange(start, end int) []*org.Headline {
	seen := make(map[*org.Headline]bool)
	var out []*org.Headline
	for i := start; i <= end && i >= 0 && i < len(m.rows); i++ {
		h := m.rows[i].headline
		if h == nil || seen[h] {
			continue
		}
		seen[h] = true
		out = append(out, h)
	}
	return out
}

// visualTopmostHeadlines filters headlines down to those not descended
// from another headline also in headlines — used by bulk delete, since
// deleting an ancestor already removes its whole subtree (see
// deleteHeadline), so a selected descendant needs no delete of its own
// (and, by the time headlines' own deletes actually run, attempting one
// would either be redundant or operate on a detached, no-longer-visible
// copy).
func visualTopmostHeadlines(headlines []*org.Headline) []*org.Headline {
	selected := make(map[*org.Headline]bool, len(headlines))
	for _, h := range headlines {
		selected[h] = true
	}
	var out []*org.Headline
	for _, h := range headlines {
		underSelectedAncestor := false
		for p := h.Parent; p != nil; p = p.Parent {
			if selected[p] {
				underSelectedAncestor = true
				break
			}
		}
		if !underSelectedAncestor {
			out = append(out, h)
		}
	}
	return out
}

// deleteVisualSelection removes every top-level selected entry (and its
// subtree) — the visual-mode equivalent of dd. See deleteHeadlineSet for
// the shared mechanics.
func (m *Model) deleteVisualSelection() {
	headlines := visualTopmostHeadlines(m.visualSelectedHeadlines())
	m.exitVisualMode()
	m.deleteHeadlineSet(headlines)
}

// yankVisualSelection copies every top-level selected entry (and its
// subtree) into the register — the visual-mode equivalent of yy — leaving
// the originals untouched. Like deleteVisualSelection, a selected entry
// whose ancestor is also selected contributes nothing separately, since
// the ancestor's own clone already carries its whole subtree along.
func (m *Model) yankVisualSelection() {
	headlines := visualTopmostHeadlines(m.visualSelectedHeadlines())
	m.exitVisualMode()
	if len(headlines) == 0 {
		return
	}
	clones := make([]*org.Headline, len(headlines))
	for i, h := range headlines {
		clones[i] = org.CloneHeadline(h)
	}
	m.register = clones
	m.message = "Yanked"
}
