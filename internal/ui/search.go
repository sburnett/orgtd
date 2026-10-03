package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// activeSearchQuery is the search term row rendering should highlight
// (see highlightMatches): the query typed so far while actively
// searching, or the last confirmed search otherwise — matching vim's
// 'hlsearch', which keeps highlighting the last search until a new one
// starts or it's cleared (:noh).
func (m Model) activeSearchQuery() string {
	if m.mode == searchMode {
		return m.searchQuery
	}
	return m.lastSearchQuery
}

func (m Model) updateSearchMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.mode = normalMode
		m.cursor = m.searchOrigin
		m.searchQuery = ""
		m.message = ""
		m.ensureVisible()
		return m, nil

	case tea.KeyEnter:
		m.mode = normalMode
		if m.searchQuery != "" {
			m.lastSearchQuery = m.searchQuery
			m.lastSearchForward = m.searchForward
			if m.cursor != m.searchOrigin {
				// searchOrigin, not m.cursor: incremental search has
				// already moved the cursor to the match by now — what
				// belongs in the jump list is where the search started
				// from, not where it landed.
				m.pushJumpAt(m.searchOrigin)
			}
		}
		m.searchQuery = ""
		return m, nil

	case tea.KeyBackspace:
		// Vim exits command-line-like modes when backspace is pressed
		// with nothing left to delete (see updateCommandMode); mirrored
		// here, reverting the cursor like Esc does.
		r := []rune(m.searchQuery)
		if len(r) == 0 {
			m.mode = normalMode
			m.cursor = m.searchOrigin
			m.message = ""
			m.ensureVisible()
			return m, nil
		}
		m.searchQuery = string(r[:len(r)-1])
		m.performIncrementalSearch()
		return m, nil

	case tea.KeySpace:
		m.searchQuery += " "
		m.performIncrementalSearch()
		return m, nil

	case tea.KeyRunes:
		m.searchQuery += string(msg.Runes)
		m.performIncrementalSearch()
		return m, nil
	}
	return m, nil
}

// performIncrementalSearch re-jumps the cursor from searchOrigin to the
// nearest match of the query typed so far, in searchForward's direction
// — called after every keystroke in searchMode. Leaves the cursor at
// searchOrigin if the query is empty or matches nothing. A match inside
// a folded subtree is unfolded to reveal it (see revealRow), which
// rebuilds m.rows and so shifts row indices — searchOrigin is
// re-resolved by identity afterward so it keeps naming the same row
// (rather than whatever now sits at its old index) for the rest of the
// incremental search, and for Esc/backspace-to-empty reverting to it.
func (m *Model) performIncrementalSearch() {
	m.cursor = m.searchOrigin
	m.message = ""
	if m.searchQuery != "" && m.searchOrigin >= 0 && m.searchOrigin < len(m.rows) {
		origin := m.rows[m.searchOrigin]
		rows := m.searchRows()
		if target, ok := findMatch(rows, origin, m.searchQuery, m.searchForward); ok {
			m.revealRow(target)
			if idx := indexOfRow(m.rows, origin); idx >= 0 {
				m.searchOrigin = idx
			}
			if idx := indexOfRow(m.rows, target); idx >= 0 {
				m.cursor = idx
			}
			index, total := searchMatchStats(rows, m.searchQuery, target)
			m.message = fmt.Sprintf("[%d/%d]", index, total)
		} else {
			m.message = fmt.Sprintf("No match for %q", m.searchQuery)
		}
	}
	m.ensureVisible()
}

// repeatSearch ("n"/"N") repeats the last confirmed search from the
// current cursor position, in the given direction. Same reveal-then-
// relocate handling as performIncrementalSearch, for a match inside a
// folded subtree.
func (m *Model) repeatSearch(forward bool) {
	if m.lastSearchQuery == "" || m.cursor < 0 || m.cursor >= len(m.rows) {
		return
	}
	m.message = ""
	from := m.rows[m.cursor]
	rows := m.searchRows()
	if target, ok := findMatch(rows, from, m.lastSearchQuery, forward); ok {
		m.revealRow(target)
		if idx := indexOfRow(m.rows, target); idx >= 0 {
			m.cursor = idx
		}
		index, total := searchMatchStats(rows, m.lastSearchQuery, target)
		m.message = fmt.Sprintf("[%d/%d]", index, total)
	} else {
		m.message = fmt.Sprintf("No match for %q", m.lastSearchQuery)
	}
}

// searchRows returns the rows "/"/"?"/"n"/"N" scan for a match: the same
// rows currently on screen for a view with no folding (agenda, config,
// log, diff, help — appendAgendaRows/appendConfigRows/etc. never gate on
// collapsed), but a fully expanded copy of the outline — every fold
// treated as open, via appendHeadlines/appendCalendarRows's ignoreFold —
// for the outline/clarify and calendar views. Otherwise a match inside a
// folded subtree, or a folded calendar event's Location/description
// body, would be invisible to search simply because its row was never
// built, rather than because it didn't match — unlike vim, where folding
// is a display-only concept and "/" always searches the whole buffer.
// revealRow (below) then unfolds just enough of the real outline to
// bring whatever's found here onto the actual screen.
func (m *Model) searchRows() []row {
	spec := m.spec()
	if !spec.folds {
		return m.rows
	}
	var rows []row
	spec.build(m, &rows, true)
	return rows
}

// indexOfRow returns the index of the first row in rows identical to
// target (see sameRow), or -1 if it isn't there at all.
func indexOfRow(rows []row, target row) int {
	for i, r := range rows {
		if sameRow(r, target) {
			return i
		}
	}
	return -1
}

// revealRow unfolds whatever's necessary so target's row — found via
// searchRows, which searches the full outline regardless of fold state —
// actually appears in m.rows: every ancestor of its headline (so the
// headline's own row is reachable at all), and the headline itself too
// when target is one of its own body lines (gated on collapsed[h] the
// same as its children — see appendBodyLines/appendHeadlines). A no-op
// for a view with no folding to begin with (agenda, config, log, diff,
// help), and for the common case where target was already visible (no
// folds in the way). Deliberately doesn't restore folds it opens if the
// search is later cancelled (Esc) — same as vim, which leaves a fold
// opened by search open rather than closing it back up.
func (m *Model) revealRow(target row) {
	if !m.spec().folds {
		return
	}
	h := target.headline
	if h == nil {
		return
	}
	changed := false
	if target.kind == rowBody && m.collapsed[h] {
		m.collapsed[h] = false
		changed = true
	}
	for p := h.Parent; p != nil; p = p.Parent {
		if m.collapsed[p] {
			m.collapsed[p] = false
			changed = true
		}
	}
	if changed {
		m.rebuildRows()
	}
}

// searchMatchStats reports target's 1-based ordinal position among every
// row matching query in rows (see searchRows), in that slice's own
// (top-to-bottom, direction-independent) order, plus the total number of
// matches — shown alongside a confirmed search or "n"/"N" repeat as
// "[index/total]", mirroring vim's own search-count indicator. Takes
// rows rather than calling searchRows itself — see findMatch, above.
func searchMatchStats(rows []row, query string, target row) (index, total int) {
	q := strings.ToLower(query)
	for _, r := range rows {
		if strings.Contains(strings.ToLower(rowSearchText(r)), q) {
			total++
			if sameRow(r, target) {
				index = total
			}
		}
	}
	return index, total
}

// findMatch searches rows (see searchRows) for the nearest row —
// excluding from itself — whose searchable text (see rowSearchText)
// contains query, case-insensitively, moving forward or backward from
// from and wrapping around the ends (vim's default 'wrapscan' behavior).
// Takes rows rather than calling searchRows itself so a caller needing
// both a match and searchMatchStats (below) — every caller, today — only
// pays for rebuilding the full outline once rather than twice.
func findMatch(rows []row, from row, query string, forward bool) (row, bool) {
	n := len(rows)
	if n == 0 {
		return row{}, false
	}
	start := indexOfRow(rows, from)
	if start < 0 {
		start = 0
	}
	q := strings.ToLower(query)
	step := 1
	if !forward {
		step = -1
	}
	for i := 1; i <= n; i++ {
		idx := ((start+step*i)%n + n) % n
		if strings.Contains(strings.ToLower(rowSearchText(rows[idx])), q) {
			return rows[idx], true
		}
	}
	return row{}, false
}

// startSearch enters search mode ("/" for forward, "?" for backward),
// remembering where the cursor was so Esc can put it back.
func (m *Model) startSearch(forward bool) {
	m.mode = searchMode
	m.searchForward = forward
	m.searchOrigin = m.cursor
	m.searchQuery = ""
	m.message = ""
}
