package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/sburnett/orgtd/internal/org"
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
	if m.rows[idx+1].kind != rowBody {
		t.Fatalf("fixture assumption broken: expected a body line right after the headline")
	}

	m = sendKey(m, "/")
	m = typeKeys(m, "API versioning")

	if m.rows[m.cursor].kind == rowBody {
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
	got := (Model{}).highlightMatches("cat and cat and dog", "cat", lipgloss.NewStyle())
	plain := stripANSI(got)
	if plain != "cat and cat and dog" {
		t.Fatalf("plain text = %q, want unchanged", plain)
	}
	if n := strings.Count(got, "\x1b["); n < 2 {
		t.Errorf("got %q, want at least 2 separately styled matches", got)
	}
}

func TestHighlightMatchesCaseInsensitive(t *testing.T) {
	got := (Model{}).highlightMatches("Finance report", "finance", lipgloss.NewStyle())
	if !strings.Contains(got, "\x1b[") {
		t.Errorf("got %q, want the differently-cased match still highlighted", got)
	}
	if stripANSI(got) != "Finance report" {
		t.Errorf("plain text = %q, want original casing preserved", stripANSI(got))
	}
}

func TestHighlightMatchesNoQueryIsPlain(t *testing.T) {
	got := (Model{}).highlightMatches("plain text", "", lipgloss.NewStyle())
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

func TestSearchFindsHeadlineInsideFoldedSubtree(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Ship orgtd v0.1")
	m = sendKey(m, "tab")
	if !m.collapsed[m.currentHeadline()] {
		t.Fatalf("fixture assumption broken: fold didn't collapse")
	}

	m = sendKey(m, "/")
	m = typeKeys(m, "Bubble Tea viewer")

	if got := m.currentHeadline(); got == nil || got.Title != "Implement the Bubble Tea viewer" {
		t.Errorf("cursor = %v, want the match inside the folded subtree", got)
	}
}

func TestSearchFindsBodyLineInsideFoldedSubtree(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Ship orgtd v0.1")
	m = sendKey(m, "tab")

	m = sendKey(m, "/")
	m = typeKeys(m, "planning lines, properties")

	got := m.currentHeadline()
	if got == nil || got.Title != "Implement the org file parser" {
		t.Fatalf("cursor = %v, want the entry owning the matching (folded-away) body text", got)
	}
	if m.collapsed[got] {
		t.Errorf("matched entry is still folded — its body line couldn't actually be visible")
	}
}

func TestSearchInHelpViewFindsReadmeText(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws, WithReadme("# orgtd\n\nSome unique-marker-xyz docs.\n"))
	m = sendKey(m, ":")
	m = typeKeys(m, "help")
	m, _ = sendKeyCmd(m, "enter")

	before := m.cursor
	m = sendKey(m, "/")
	m = typeKeys(m, "unique-marker-xyz")

	if m.cursor == before {
		t.Fatalf("search in help view found nothing")
	}
	if m.rows[m.cursor].kind != rowText || !strings.Contains(m.rows[m.cursor].text, "unique-marker-xyz") {
		t.Errorf("cursor landed on %+v, want the matching help row", m.rows[m.cursor])
	}
}

func TestWrappedBodyLineHighlightsMatchAcrossWrapPoint(t *testing.T) {
	var rows []row
	m := &Model{width: 30}
	h := &org.Headline{Title: "x", Level: 1, Body: []string{"alpha beta gamma delta epsilon zeta eta theta"}}
	m.appendBodyLines(&rows, h)
	// Find a wrap point and query the two words on either side of it.
	first, second := strings.Fields(rows[0].text), strings.Fields(rows[1].text)
	query := first[len(first)-1] + " " + second[0]

	got0 := rows[0].wrap.matchRanges(0, query)
	got1 := rows[0].wrap.matchRanges(1, query)
	if len(got0) != 1 || len(got1) != 1 {
		t.Fatalf("ranges = %v / %v, want one in each row", got0, got1)
	}
	if s := rows[0].text[got0[0][0]:got0[0][1]]; s != first[len(first)-1] {
		t.Errorf("row 0 highlights %q, want %q", s, first[len(first)-1])
	}
	if s := rows[1].text[got1[0][0]:got1[0][1]]; s != second[0] {
		t.Errorf("row 1 highlights %q, want %q", s, second[0])
	}
	if r := rows[0].wrap.matchRanges(0, "zzz"); len(r) != 0 {
		t.Errorf("unrelated query highlighted %v", r)
	}
}

// The cursor never rests on a body line (it snaps to the owning entry's
// title row), so a match inside an entry's body used to leave "n"
// restarting from the title row and finding the same body line forever.
func TestRepeatSearchStepsPastMatchesInEntryBodies(t *testing.T) {
	m := New(loadDemo(t))
	m = sendKey(m, "/")
	m = typeKeys(m, "the")
	m = sendKey(m, "enter")

	var total int
	if _, err := fmt.Sscanf(m.message, "[1/%d]", &total); err != nil {
		t.Fatalf("message after search = %q, want [1/N]", m.message)
	}
	for want := 2; want <= total+1; want++ {
		m = sendKey(m, "n")
		wantMsg := fmt.Sprintf("[%d/%d]", (want-1)%total+1, total)
		if m.message != wantMsg {
			t.Fatalf("after %d presses of n, message = %q, want %q (stuck on an entry body match?)", want-1, m.message, wantMsg)
		}
	}
}

func TestEntryWithManyMatchesIsOneSearchMatch(t *testing.T) {
	m := &Model{width: 30, collapsed: map[*org.Headline]bool{}}
	h := &org.Headline{Title: "gamma title", Level: 1, Body: []string{
		"alpha beta gamma delta epsilon zeta eta theta",
		"gamma",
		"gamma",
	}}
	other := &org.Headline{Title: "unrelated", Level: 1, Body: []string{"nothing here"}}
	var rows []row
	m.appendHeadlines(&rows, []*org.Headline{h, other}, true)

	// An entry is one search match, however many of its lines (title or
	// body, wrapped or not) contain the query.
	for _, q := range []string{"gamma", "beta gamma", "gamma delta epsilon", "theta"} {
		if _, total := searchMatchStats(rows, q, rows[0]); total != 1 {
			t.Errorf("query %q matched %d rows, want 1 (one entry)", q, total)
		}
	}
	next, ok := findMatch(rows, rows[0], "gamma", true)
	if !ok || !sameRow(next, rows[0]) {
		t.Errorf("repeat from the only matching entry = %+v (ok=%v), want that same entry", next.headline, ok)
	}
}
