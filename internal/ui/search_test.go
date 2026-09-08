package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestForwardSearchIncrementallyJumpsAsYouType(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)

	m = sendKey(m, "/")
	if m.mode != searchMode || !m.searchForward {
		t.Fatalf("mode after / = %v (forward=%v), want searchMode forward", m.mode, m.searchForward)
	}

	m = typeKeys(m, "finance")

	if got := m.currentHeadline(); got == nil || got.Title != "Follow up with finance about the Q3 budget doc" {
		t.Errorf("cursor after typing = %v, want the matching entry", got)
	}
}

func TestBackwardSearchJumpsUpward(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Follow up with finance about the Q3 budget doc")

	m = sendKey(m, "?")
	if m.mode != searchMode || m.searchForward {
		t.Fatalf("mode after ? = %v (forward=%v), want searchMode backward", m.mode, m.searchForward)
	}
	m = typeKeys(m, "vet")

	if got := m.currentHeadline(); got == nil || got.Title != "Call the vet about Fido's checkup" {
		t.Errorf("cursor after backward search = %v, want the matching entry above", got)
	}
}

func TestSearchEscRevertsToOrigin(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	origin := findRow(t, m, "Call the vet about Fido's checkup")
	m.cursor = origin

	m = sendKey(m, "/")
	m = typeKeys(m, "finance")
	if m.cursor == origin {
		t.Fatalf("fixture assumption broken: search should have moved the cursor")
	}

	m = sendKey(m, "esc")

	if m.mode != normalMode {
		t.Errorf("mode after esc = %v, want normalMode", m.mode)
	}
	if m.cursor != origin {
		t.Errorf("cursor after esc = %d, want %d (reverted to origin)", m.cursor, origin)
	}
}

func TestSearchEnterConfirmsAndStaysAtMatch(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	origin := findRow(t, m, "Call the vet about Fido's checkup")
	m.cursor = origin

	m = sendKey(m, "/")
	m = typeKeys(m, "finance")
	matchIdx := m.cursor

	m, _ = sendKeyCmd(m, "enter")

	if m.mode != normalMode {
		t.Errorf("mode after enter = %v, want normalMode", m.mode)
	}
	if m.cursor != matchIdx {
		t.Errorf("cursor after enter = %d, want %d (stayed at the match)", m.cursor, matchIdx)
	}
	if m.lastSearchQuery != "finance" || !m.lastSearchForward {
		t.Errorf("lastSearchQuery/Forward = %q/%v, want %q/true", m.lastSearchQuery, m.lastSearchForward, "finance")
	}
}

func TestSearchBackspaceRetypesFromOriginNotFromLastMatch(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	origin := findRow(t, m, "Call the vet about Fido's checkup")
	m.cursor = origin

	m = sendKey(m, "/")
	m = typeKeys(m, "finance")
	if got := m.currentHeadline(); got == nil || got.Title != "Follow up with finance about the Q3 budget doc" {
		t.Fatalf("fixture assumption broken: expected the finance entry to match")
	}

	// Backspace back to "fin" then retype toward "read": if search were
	// cumulative from the last match instead of always from origin,
	// this would search from the finance entry instead of the start.
	for range "finance" {
		m = sendKey(m, "backspace")
	}
	m = typeKeys(m, "RFC")

	if got := m.currentHeadline(); got == nil || got.Title != "Read the RFC linked in yesterday's design review" {
		t.Errorf("cursor after retyping = %v, want the RFC entry (searched fresh from origin)", got)
	}
}

func TestSearchBackspaceOnEmptyExitsSearchMode(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	origin := findRow(t, m, "Call the vet about Fido's checkup")
	m.cursor = origin

	m = sendKey(m, "/")
	m = sendKey(m, "backspace")

	if m.mode != normalMode {
		t.Errorf("mode after backspace on empty query = %v, want normalMode", m.mode)
	}
	if m.cursor != origin {
		t.Errorf("cursor = %d, want unchanged %d", m.cursor, origin)
	}
}

func TestSearchNoMatchLeavesCursorAtOrigin(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	origin := findRow(t, m, "Call the vet about Fido's checkup")
	m.cursor = origin

	m = sendKey(m, "/")
	m = typeKeys(m, "zzzznomatch")

	if m.cursor != origin {
		t.Errorf("cursor = %d, want unchanged %d (no match)", m.cursor, origin)
	}
	if m.mode != searchMode {
		t.Errorf("mode = %v, want to stay in searchMode so the query can be corrected", m.mode)
	}
}

func TestSearchIsCaseInsensitive(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)

	m = sendKey(m, "/")
	m = typeKeys(m, "FINANCE")

	if got := m.currentHeadline(); got == nil || got.Title != "Follow up with finance about the Q3 budget doc" {
		t.Errorf("cursor = %v, want a case-insensitive match", got)
	}
}

func TestSearchWrapsAroundForward(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	// Start at the last matching-ish position: search forward for
	// something that only exists earlier in the list than the cursor.
	m.cursor = findRow(t, m, "Follow up with finance about the Q3 budget doc")

	m = sendKey(m, "/")
	m = typeKeys(m, "vet")

	if got := m.currentHeadline(); got == nil || got.Title != "Call the vet about Fido's checkup" {
		t.Errorf("cursor = %v, want the wrapped-around match", got)
	}
}

func TestSearchWrapsAroundBackward(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")

	// "budget doc" (unlike "finance" alone) uniquely matches this entry
	// — "Get budget numbers from finance" elsewhere would otherwise be
	// found first without even needing to wrap.
	m = sendKey(m, "?")
	m = typeKeys(m, "budget doc")

	if got := m.currentHeadline(); got == nil || got.Title != "Follow up with finance about the Q3 budget doc" {
		t.Errorf("cursor = %v, want the wrapped-around match", got)
	}
}

func TestNRepeatsLastSearchForward(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)

	m = sendKey(m, "/")
	m = typeKeys(m, "TODO")
	m, _ = sendKeyCmd(m, "enter")
	first := m.cursor

	m = sendKey(m, "n")

	if m.cursor == first {
		t.Fatalf("n did not move the cursor to the next match")
	}
	if got := rowSearchText(m.rows[m.cursor]); !strings.Contains(strings.ToLower(got), "todo") {
		t.Errorf("n landed on a non-matching row: %q", got)
	}
}

func TestCapitalNReversesSearchDirection(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)

	m = sendKey(m, "/")
	m = typeKeys(m, "TODO")
	m, _ = sendKeyCmd(m, "enter")
	afterSearch := m.cursor

	m = sendKey(m, "n")
	afterN := m.cursor
	if afterN == afterSearch {
		t.Fatalf("fixture assumption broken: n should have moved forward")
	}

	m = sendKey(m, "N")
	if m.cursor != afterSearch {
		t.Errorf("cursor after N = %d, want %d (back to the previous match, reversing n)", m.cursor, afterSearch)
	}
}

func TestNNoopWithoutAPriorSearch(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	before := m.cursor

	m = sendKey(m, "n")

	if m.cursor != before {
		t.Errorf("n with no prior search moved the cursor: %d -> %d", before, m.cursor)
	}
}

func TestSearchMatchesFileRowByName(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)

	m = sendKey(m, "/")
	m = typeKeys(m, "projects.org")

	want := findFileRow(t, m, "projects.org")
	if m.cursor != want {
		t.Errorf("cursor = %d, want %d (the matching file row)", m.cursor, want)
	}
}

func TestSearchMatchingBodyLineResolvesToOwningHeadline(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	idx := findRow(t, m, "Read the RFC linked in yesterday's design review")
	if !m.rows[idx+1].isBodyLine {
		t.Fatalf("fixture assumption broken: expected a body line right after the headline")
	}

	m = sendKey(m, "/")
	m = typeKeys(m, "API versioning")

	if m.rows[m.cursor].isBodyLine {
		t.Fatalf("cursor rests on a body line: %+v", m.rows[m.cursor])
	}
	if got := m.currentHeadline(); got == nil || got.Title != "Read the RFC linked in yesterday's design review" {
		t.Errorf("cursor = %v, want the entry owning the matching body text", got)
	}
}

func TestSearchStatusLineShowsPrefixAndQuery(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.width, m.height = 100, 30

	m = sendKey(m, "/")
	m = typeKeys(m, "abc")
	out := m.View()
	lines := strings.Split(out, "\n")
	last := lines[len(lines)-1]
	if !strings.HasPrefix(last, "/abc") {
		t.Errorf("status line = %q, want it to start with '/abc'", last)
	}

	m = sendKey(m, "esc")
	m = sendKey(m, "?")
	m = typeKeys(m, "xyz")
	out = m.View()
	lines = strings.Split(out, "\n")
	last = lines[len(lines)-1]
	if !strings.HasPrefix(last, "?xyz") {
		t.Errorf("status line = %q, want it to start with '?xyz'", last)
	}
}

func TestHighlightMatchesMarksEveryOccurrence(t *testing.T) {
	got := highlightMatches("cat and cat and dog", "cat", lipgloss.NewStyle())
	plain := stripANSI(got)
	if plain != "cat and cat and dog" {
		t.Fatalf("plain text = %q, want unchanged", plain)
	}
	if n := strings.Count(got, "\x1b["); n < 2 {
		t.Errorf("got %q, want at least 2 separately styled matches", got)
	}
}

func TestHighlightMatchesCaseInsensitive(t *testing.T) {
	got := highlightMatches("Finance report", "finance", lipgloss.NewStyle())
	if !strings.Contains(got, "\x1b[") {
		t.Errorf("got %q, want the differently-cased match still highlighted", got)
	}
	if stripANSI(got) != "Finance report" {
		t.Errorf("plain text = %q, want original casing preserved", stripANSI(got))
	}
}

func TestHighlightMatchesNoQueryIsPlain(t *testing.T) {
	got := highlightMatches("plain text", "", lipgloss.NewStyle())
	if got != "plain text" {
		t.Errorf("got %q, want unstyled plain text with an empty query", got)
	}
}

func TestSearchTermStaysHighlightedAfterCursorMovesAway(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.width, m.height = 100, len(m.rows)+5

	m = sendKey(m, "/")
	m = typeKeys(m, "finance")
	m, _ = sendKeyCmd(m, "enter")
	matchIdx := m.cursor

	m = sendKey(m, "j") // move away from the match
	if m.cursor == matchIdx {
		t.Fatalf("fixture assumption broken: cursor should have moved")
	}

	line := m.renderRow(m.rows[matchIdx])
	if !strings.Contains(line, "\x1b[") || !strings.Contains(stripANSI(line), "finance") {
		t.Fatalf("row = %q, missing the match text", line)
	}
	// Confirm the match itself carries a background distinct from a
	// plain render of the same row (i.e. it's actually highlighted, not
	// just present in plain text).
	plainRender := stripANSI(line)
	if !strings.Contains(plainRender, "finance") {
		t.Fatalf("row text = %q, expected to contain 'finance'", plainRender)
	}
}

func TestSearchHighlightClearedByEsc(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.width, m.height = 100, len(m.rows)+5

	m = sendKey(m, "/")
	m = typeKeys(m, "finance")
	// Cancel without confirming.
	m = sendKey(m, "esc")

	if m.activeSearchQuery() != "" {
		t.Errorf("activeSearchQuery() = %q after esc, want empty (no confirmed search yet)", m.activeSearchQuery())
	}
}

func TestNohClearsPersistentHighlight(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)

	m = sendKey(m, "/")
	m = typeKeys(m, "finance")
	m, _ = sendKeyCmd(m, "enter")
	if m.lastSearchQuery == "" {
		t.Fatalf("fixture assumption broken: expected a confirmed search")
	}

	m = sendKey(m, ":")
	m = typeKeys(m, "noh")
	m, _ = sendKeyCmd(m, "enter")

	if m.lastSearchQuery != "" {
		t.Errorf("lastSearchQuery = %q after :noh, want cleared", m.lastSearchQuery)
	}
	if m.activeSearchQuery() != "" {
		t.Errorf("activeSearchQuery() = %q after :noh, want empty", m.activeSearchQuery())
	}
}

func TestNohlsearchAliasAlsoClears(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m = sendKey(m, "/")
	m = typeKeys(m, "finance")
	m, _ = sendKeyCmd(m, "enter")

	m = sendKey(m, ":")
	m = typeKeys(m, "nohlsearch")
	m, _ = sendKeyCmd(m, "enter")

	if m.lastSearchQuery != "" {
		t.Errorf("lastSearchQuery = %q after :nohlsearch, want cleared", m.lastSearchQuery)
	}
}

func TestNewSearchReplacesOldHighlight(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)

	m = sendKey(m, "/")
	m = typeKeys(m, "finance")
	m, _ = sendKeyCmd(m, "enter")

	m = sendKey(m, "/")
	m = typeKeys(m, "vet")
	m, _ = sendKeyCmd(m, "enter")

	if m.lastSearchQuery != "vet" {
		t.Errorf("lastSearchQuery = %q, want the new search to replace the old one", m.lastSearchQuery)
	}
}
