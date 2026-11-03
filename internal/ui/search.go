package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/sburnett/orgtd/internal/org"
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
		m.closeSearchFolds()
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
		m.searchOpened = nil // confirmed: the folds it opened stay open
		return m, nil

	case tea.KeyBackspace:
		// Vim exits command-line-like modes when backspace is pressed
		// with nothing left to delete (see updateCommandMode); mirrored
		// here, reverting the cursor like Esc does.
		r := []rune(m.searchQuery)
		if len(r) == 0 {
			m.mode = normalMode
			m.closeSearchFolds()
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
// searchOrigin if the query is empty or matches nothing.
//
// A match inside a folded subtree is unfolded to reveal it, but only
// provisionally: the folds opened are remembered in searchOpened, and
// any of them the next keystroke's match doesn't also need are closed
// again — so a short prefix like "e" matching something unrelated
// doesn't leave its subtree sprawled open once the query has moved on.
// Enter keeps whatever the final match needed open; Esc closes them all.
// Opening and closing folds rebuilds m.rows and so shifts row indices —
// searchOrigin is re-resolved by identity afterward so it keeps naming
// the same row (rather than whatever now sits at its old index) for the
// rest of the incremental search, and for Esc/backspace-to-empty
// reverting to it.
func (m *Model) performIncrementalSearch() {
	m.message = ""
	if m.searchOrigin < 0 || m.searchOrigin >= len(m.rows) {
		m.cursor = m.searchOrigin
		m.ensureVisible()
		return
	}
	origin := m.rows[m.searchOrigin]
	var target row
	found := false
	if m.searchQuery != "" {
		rows := m.searchRows()
		if target, found = findMatch(rows, origin, m.searchQuery, m.searchForward); found {
			index, total := searchMatchStats(rows, m.searchQuery, target)
			m.message = fmt.Sprintf("[%d/%d]", index, total)
		} else {
			m.message = fmt.Sprintf("No match for %q", m.searchQuery)
		}
	}

	// Work out the folds this match needs open — its collapsed
	// ancestors, plus any this search already opened for it — before
	// touching any, so a fold that stays needed from one keystroke to the
	// next is left alone rather than closed and reopened (each change
	// costs a rebuild).
	opened := map[*org.Headline]bool{}
	if found && m.spec().folds && target.headline != nil {
		for p := target.headline.Parent; p != nil; p = p.Parent {
			if m.collapsed[p] || m.searchOpened[p] {
				opened[p] = true
			}
		}
	}
	changed := false
	for h := range m.searchOpened {
		if !opened[h] {
			m.collapsed[h] = true
			changed = true
		}
	}
	for h := range opened {
		if m.collapsed[h] {
			m.collapsed[h] = false
			changed = true
		}
	}
	m.searchOpened = opened
	if changed {
		m.rebuildRows()
		if idx := indexOfRow(m.rows, origin); idx >= 0 {
			m.searchOrigin = idx
		}
	}

	m.cursor = m.searchOrigin
	if found {
		if idx := indexOfRow(m.rows, target); idx >= 0 {
			m.cursor = idx
		}
	}
	m.ensureVisible()
}

// closeSearchFolds closes every fold the in-progress incremental search
// opened (see searchOpened) — for Esc and backspace-to-empty, which
// abandon the search — re-resolving searchOrigin by identity since the
// rebuild shifts row indices.
func (m *Model) closeSearchFolds() {
	if len(m.searchOpened) == 0 {
		m.searchOpened = nil
		return
	}
	var origin row
	hasOrigin := m.searchOrigin >= 0 && m.searchOrigin < len(m.rows)
	if hasOrigin {
		origin = m.rows[m.searchOrigin]
	}
	for h := range m.searchOpened {
		m.collapsed[h] = true
	}
	m.searchOpened = nil
	m.rebuildRows()
	if hasOrigin {
		if idx := indexOfRow(m.rows, origin); idx >= 0 {
			m.searchOrigin = idx
		}
	}
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
		m.revealBodyMatch(target, m.lastSearchQuery)
		if idx := indexOfRow(m.rows, target); idx >= 0 {
			m.cursor = idx
		}
		index, total := searchMatchStats(rows, m.lastSearchQuery, target)
		m.message = fmt.Sprintf("[%d/%d]", index, total)
		m.ensureVisible()
	} else {
		m.message = fmt.Sprintf("No match for %q", m.lastSearchQuery)
	}
}

// searchRows returns the rows "/"/"?"/"n"/"N" scan for a match: the same
// rows currently on screen for a view with no folding (agenda, config,
// log, diff, help — appendAgendaRows/appendConfigRows/etc. never gate on
// collapsed), but a fully expanded copy of the outline — every fold
// treated as open, via appendHeadlines/appendCalendarRows's ignoreFold —
// for the outline/review and calendar views. Otherwise a match inside a
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
	// Cached until the next buildRows (see window.search), which follows
	// every change that could alter these rows. Callers only read them.
	c := m.search
	if c != nil && c.valid && c.width == m.listWidth() {
		return c.rows
	}
	var rows []row
	spec.build(m, &rows, true)
	if c != nil {
		*c = searchCache{rows: rows, width: m.listWidth(), valid: true}
	}
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
// headline's own row is reachable at all; see revealBodyMatch for the
// entry's own body). A no-op for a view with no folding to begin with
// (agenda, config, log, diff, help), and for the common case where
// target was already visible (no folds in the way). Folds opened this
// way stay open; incremental search instead opens them provisionally
// (see performIncrementalSearch).
func (m *Model) revealRow(target row) {
	hiding := m.foldsHiding(target)
	for _, h := range hiding {
		m.collapsed[h] = false
	}
	if len(hiding) > 0 {
		m.rebuildRows()
	}
}

// foldsHiding returns the collapsed ancestors of target's headline — the
// folds that must open for its row to appear in m.rows (see revealRow).
// None for a view with no folding, or a row with no headline.
func (m *Model) foldsHiding(target row) []*org.Headline {
	if !m.spec().folds || target.headline == nil {
		return nil
	}
	var hiding []*org.Headline
	for p := target.headline.Parent; p != nil; p = p.Parent {
		if m.collapsed[p] {
			hiding = append(hiding, p)
		}
	}
	return hiding
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
	m.searchOpened = nil
	m.message = ""
}

// revealBodyMatch unfolds target's own entry when query matches only its
// body text, not what its title line shows — the match would otherwise be
// invisible, folded away under a title that doesn't contain it. An entry
// whose title matches stays as folded as it was.
func (m *Model) revealBodyMatch(target row, query string) {
	h := target.headline
	if h == nil || target.bodySearch == "" || !m.collapsed[h] || !m.spec().folds {
		return
	}
	q := strings.ToLower(query)
	if strings.Contains(strings.ToLower(rowTitleSearchText(target)), q) ||
		!strings.Contains(strings.ToLower(target.bodySearch), q) {
		return
	}
	m.collapsed[h] = false
	m.rebuildRows()
}
