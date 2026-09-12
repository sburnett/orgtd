package ui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/sburnett/orgtd/internal/org"
	"github.com/sburnett/orgtd/internal/workspace"
)

var ansiEscapeRe = regexp.MustCompile("\x1b\\[[0-9;]*m")

// stripANSI removes SGR escape codes so tests can check visible text
// without depending on lipgloss's exact styling sequences.
func stripANSI(s string) string {
	return ansiEscapeRe.ReplaceAllString(s, "")
}

// TestMain forces a color profile so styling assertions in View() tests
// are meaningful even though go test's stdout isn't a terminal (lipgloss
// would otherwise auto-detect "no color" and strip all SGR codes).
func TestMain(m *testing.M) {
	lipgloss.SetColorProfile(termenv.ANSI)
	os.Exit(m.Run())
}

func loadFixture(t *testing.T) *workspace.Workspace {
	t.Helper()
	ws, err := workspace.Load("../../testdata/orgdir")
	if err != nil {
		t.Fatalf("workspace.Load: %v", err)
	}
	return ws
}

// loadFixtureCopy loads a workspace over a scratch copy of testdata/orgdir,
// so tests that actually write to disk (:w) never touch the checked-in
// fixtures.
func loadFixtureCopy(t *testing.T) *workspace.Workspace {
	t.Helper()
	src := "../../testdata/orgdir"
	dst := t.TempDir()

	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), data, 0644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}

	ws, err := workspace.Load(dst)
	if err != nil {
		t.Fatalf("workspace.Load: %v", err)
	}
	return ws
}

func countHeadlines(ws *workspace.Workspace) int {
	n := 0
	for _, f := range ws.Files {
		org.Walk(f.Headlines, func(*org.Headline) { n++ })
	}
	return n
}

// countVisibleBodyLines returns how many body-line rows every headline
// across ws would contribute if fully expanded (see visibleBodyLines).
func countVisibleBodyLines(ws *workspace.Workspace) int {
	n := 0
	for _, f := range ws.Files {
		org.Walk(f.Headlines, func(h *org.Headline) { n += len(visibleBodyLines(h)) })
	}
	return n
}

// subtreeRowCount returns how many rows h occupies when fully expanded:
// its own row, one per body line, and the same recursively for every
// child — i.e. how many rows deleting h (dd) removes, or how many a
// fresh copy of it (yy/p) adds.
func subtreeRowCount(h *org.Headline) int {
	n := 1 + len(visibleBodyLines(h))
	for _, c := range h.Children {
		n += subtreeRowCount(c)
	}
	return n
}

// findRow returns the index of the row whose headline has the given
// title (or, for a file row, whose base filename equals title).
func findRow(t *testing.T, m Model, title string) int {
	t.Helper()
	for i, r := range m.rows {
		if r.headline != nil && !r.isBodyLine && r.headline.Title == title {
			return i
		}
	}
	t.Fatalf("no row with title %q", title)
	return -1
}

func keyMsgFor(key string) tea.KeyMsg {
	switch key {
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	}
}

func sendKey(m Model, key string) Model {
	updated, _ := m.Update(keyMsgFor(key))
	return updated.(Model)
}

func sendKeyCmd(m Model, key string) (Model, tea.Cmd) {
	updated, cmd := m.Update(keyMsgFor(key))
	return updated.(Model), cmd
}

// typeKeys sends each rune of s as a separate key press, as a real
// keyboard would.
func typeKeys(m Model, s string) Model {
	for _, r := range s {
		m = sendKey(m, string(r))
	}
	return m
}

func TestNewLoadsAllRows(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	want := len(ws.Files) + countHeadlines(ws) + countVisibleBodyLines(ws)
	if len(m.rows) != want {
		t.Fatalf("rows = %d, want %d", len(m.rows), want)
	}
	if m.cursor != 0 {
		t.Errorf("cursor = %d, want 0", m.cursor)
	}
}

func TestCursorMovementClamps(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.height = 20

	m = sendKey(m, "k")
	if m.cursor != 0 {
		t.Errorf("cursor after k at top = %d, want 0", m.cursor)
	}

	for i := 0; i < len(m.rows)+5; i++ {
		m = sendKey(m, "j")
	}
	// j/k move entry by entry (body lines are skipped, part of their
	// owning entry rather than separately steppable rows), so this
	// clamps at the last row that isn't a body line.
	want := len(m.rows) - 1
	for want > 0 && m.rows[want].isBodyLine {
		want--
	}
	if m.cursor != want {
		t.Errorf("cursor after many j = %d, want %d (last entry)", m.cursor, want)
	}
}

// TestJKMoveOneRowAtATime exercises j/k: plain up/down by one visible
// row, the same as navigating an ordinary file — no skipping over
// descendants and no level-awareness (that's "{"/"}" now).
func TestJKMoveOneRowAtATime(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = 0

	m = sendKey(m, "j")
	if m.cursor != 1 {
		t.Errorf("cursor after j = %d, want 1", m.cursor)
	}
	m = sendKey(m, "j")
	if m.cursor != 2 {
		t.Errorf("cursor after second j = %d, want 2", m.cursor)
	}
	m = sendKey(m, "k")
	if m.cursor != 1 {
		t.Errorf("cursor after k = %d, want 1", m.cursor)
	}
}

// TestSiblingNavigationSkipsDescendantsAndHopsUp exercises "{"/"}"
// (mirroring vim's paragraph motions): they move between rows at the same
// indentation level, skipping over any deeper (descendant) rows, and
// "hop up" to a shallower row once there are no more rows at the current
// level.
func TestSiblingNavigationSkipsDescendantsAndHopsUp(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.height = 30

	m.cursor = findRow(t, m, "Ship orgtd v0.1")

	m = sendKey(m, "}")
	if got, want := m.cursor, findRow(t, m, "Quarterly planning"); got != want {
		t.Fatalf("} from 'Ship orgtd v0.1' = row %d, want %d ('Quarterly planning')", got, want)
	}

	m = sendKey(m, "}")
	if got, want := m.cursor, findRow(t, m, "Learn Go generics"); got != want {
		t.Fatalf("} from 'Quarterly planning' = row %d, want %d ('Learn Go generics')", got, want)
	}

	// 'Learn Go generics' is the last top-level project; there is
	// nothing more at level 1 and nothing shallower after it, so } is a
	// no-op here.
	before := m.cursor
	m = sendKey(m, "}")
	if m.cursor != before {
		t.Errorf("} past the last sibling moved cursor to %d, want no-op at %d", m.cursor, before)
	}

	m = sendKey(m, "{")
	if got, want := m.cursor, findRow(t, m, "Quarterly planning"); got != want {
		t.Fatalf("{ back = row %d, want %d ('Quarterly planning')", got, want)
	}

	m = sendKey(m, "{")
	if got, want := m.cursor, findRow(t, m, "Ship orgtd v0.1"); got != want {
		t.Fatalf("{ back = row %d, want %d ('Ship orgtd v0.1')", got, want)
	}

	// One more { hops up: there's no sibling before 'Ship orgtd v0.1',
	// so we land on its parent file header.
	m = sendKey(m, "{")
	if got, want := m.cursor, findFileRow(t, m, "projects.org"); got != want {
		t.Fatalf("{ hop-up = row %d, want %d (projects.org file header)", got, want)
	}
}

// TestParagraphMotionHopsUpImmediatelyFromALeaf covers the leaf-specific
// behavior: on a leaf, "}"/"{" jump straight to the next/previous row at
// the *parent's* level, skipping the current leaf's remaining siblings
// entirely — those are already reachable one at a time via plain j/k.
func TestParagraphMotionHopsUpImmediatelyFromALeaf(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)

	// "Write the design document" is the first of four leaf children of
	// "Ship orgtd v0.1". Without the leaf special-case, } would land on
	// the next leaf sibling ("Implement the org file parser"); instead
	// it should skip all remaining siblings and land on the next
	// top-level project.
	m.cursor = findRow(t, m, "Write the design document")
	m = sendKey(m, "}")
	if got, want := m.cursor, findRow(t, m, "Quarterly planning"); got != want {
		t.Errorf("} from a leaf = row %d, want %d ('Quarterly planning', skipping remaining leaf siblings)", got, want)
	}

	// Symmetric case for {: starting on the last leaf child of
	// "Quarterly planning", { should land directly on its own parent
	// ("Quarterly planning"), not an earlier leaf sibling.
	m.cursor = findRow(t, m, "Explore a rewrite of the reporting pipeline")
	m = sendKey(m, "{")
	if got, want := m.cursor, findRow(t, m, "Quarterly planning"); got != want {
		t.Errorf("{ from a leaf = row %d, want %d ('Quarterly planning', skipping remaining leaf siblings)", got, want)
	}
}

func findFileRow(t *testing.T, m Model, base string) int {
	t.Helper()
	for i, r := range m.rows {
		if r.file != nil && strings.HasSuffix(r.file.Path, base) {
			return i
		}
	}
	t.Fatalf("no file row for %q", base)
	return -1
}

// TestMoveDeeperAndShallower exercises l/h: l moves into a child (falling
// back to sibling-level navigation, the same movement as "{"/"}", when
// there's no deeper level); h moves directly to the parent, or the file
// header for a top-level headline — always one structural level up,
// regardless of sibling position.
func TestMoveDeeperAndShallower(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.height = 30

	project := findRow(t, m, "Ship orgtd v0.1")
	firstChild := findRow(t, m, "Write the design document")

	m.cursor = project
	m = sendKey(m, "l")
	if m.cursor != firstChild {
		t.Fatalf("l into a project with a body = row %d, want %d (first child, body skipped over entirely)", m.cursor, firstChild)
	}

	// firstChild has no children of its own, so l falls back to
	// sibling-level navigation (next child at the same depth).
	secondChild := findRow(t, m, "Implement the org file parser")
	m = sendKey(m, "l")
	if m.cursor != secondChild {
		t.Fatalf("l on a leaf = row %d, want %d (next sibling)", m.cursor, secondChild)
	}

	// h on a non-first child goes directly to the parent, mirroring l's
	// directness (it doesn't stop at the previous sibling first).
	m = sendKey(m, "h")
	if m.cursor != project {
		t.Fatalf("h on a non-first child = row %d, want %d (parent, directly)", m.cursor, project)
	}

	// h on a top-level project (no parent) goes directly to its file's
	// header row, regardless of whether it has a previous sibling.
	fileRow := findFileRow(t, m, "projects.org")
	m = sendKey(m, "h")
	if m.cursor != fileRow {
		t.Fatalf("h on the first project = row %d, want %d (file header)", m.cursor, fileRow)
	}

	// Confirm that directly: from a top-level project that DOES have a
	// previous sibling, h must still jump straight to the file header,
	// not to that previous sibling.
	m.cursor = findRow(t, m, "Learn Go generics")
	m = sendKey(m, "h")
	if m.cursor != fileRow {
		t.Fatalf("h on a non-first top-level project = row %d, want %d (file header, not the previous sibling)", m.cursor, fileRow)
	}
}

// TestMoveShallowerFromFileRowGoesToPreviousFile covers h pressed while
// already on a file's header row: rather than getting stuck in place, it
// should move to the previous file's header row. It looks up whichever
// file actually precedes "projects.org" rather than assuming it's
// "inbox.org", since the shared testdata/orgdir fixture directory may
// have other files in it too.
func TestMoveShallowerFromFileRowGoesToPreviousFile(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)

	idx := -1
	for i, f := range m.ws.Files {
		if filepath.Base(f.Path) == "projects.org" {
			idx = i
			break
		}
	}
	if idx <= 0 {
		t.Fatalf("fixture assumption broken: projects.org should not be the first file (idx=%d)", idx)
	}
	prevFile := filepath.Base(m.ws.Files[idx-1].Path)

	m.cursor = findFileRow(t, m, "projects.org")
	m = sendKey(m, "h")
	if want := findFileRow(t, m, prevFile); m.cursor != want {
		t.Errorf("h on projects.org's file row = %d, want %d (%s's file row)", m.cursor, want, prevFile)
	}
}

// TestMoveShallowerNoopOnFirstFileRow covers the boundary: h on the very
// first file's header row has nowhere left to go, so it's a no-op.
func TestMoveShallowerNoopOnFirstFileRow(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)

	m.cursor = findFileRow(t, m, "inbox.org")
	before := m.cursor
	m = sendKey(m, "h")
	if m.cursor != before {
		t.Errorf("h on the first file's row moved the cursor to %d, want no-op at %d", m.cursor, before)
	}
}

func TestGgAndG(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.height = 20

	// G goes all the way to the last entry, mirroring j/k's line-by-line
	// movement — snapping up to its title row if the very last row
	// happens to be one of its own body lines, so the whole entry (not
	// just its last line) ends up highlighted.
	m = sendKey(m, "G")
	want := m.entryStart(len(m.rows) - 1)
	if m.cursor != want {
		t.Fatalf("cursor after G = %d, want %d (the last entry's own row)", m.cursor, want)
	}

	m = sendKey(m, "g")
	if !m.pendingG {
		t.Fatalf("pendingG should be true after first g")
	}
	m = sendKey(m, "g")
	if m.cursor != 0 {
		t.Errorf("cursor after gg = %d, want 0", m.cursor)
	}
	if m.pendingG {
		t.Errorf("pendingG should be cleared after second g")
	}
}

func TestCaretJumpsToParentWhenNested(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Implement the org file parser") // child of "Ship orgtd v0.1"

	m = sendKey(m, "^")

	h := m.currentHeadline()
	if h == nil || h.Title != "Ship orgtd v0.1" {
		t.Errorf("cursor after ^ = %v, want the enclosing parent 'Ship orgtd v0.1'", h)
	}
}

func TestCaretFromTopLevelHeadlineJumpsToFileRow(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Follow up with finance about the Q3 budget doc") // top-level, in inbox.org

	m = sendKey(m, "^")

	want := findFileRow(t, m, "inbox.org")
	if m.cursor != want {
		t.Errorf("cursor after ^ = %d, want %d (inbox.org, not projects.org)", m.cursor, want)
	}
}

func TestCaretNoopAlreadyOnFileRow(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findFileRow(t, m, "projects.org")

	m = sendKey(m, "^")

	if m.cursor != findFileRow(t, m, "projects.org") {
		t.Errorf("^ moved off the file row it was already on")
	}
}

func TestDollarNoopOnLeafHeadline(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup") // a leaf

	m = sendKey(m, "$")

	if h := m.currentHeadline(); h == nil || h.Title != "Call the vet about Fido's checkup" {
		t.Errorf("$ on a leaf moved the cursor to %v, want a no-op", h)
	}
}

func TestDollarJumpsToOwnLastChild(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Ship orgtd v0.1")

	m = sendKey(m, "$")

	h := m.currentHeadline()
	if h == nil || h.Title != "Get feedback on the keybinding scheme" {
		t.Errorf("cursor after $ = %v, want the headline's own last child 'Get feedback on the keybinding scheme'", h)
	}
}

func TestDollarRepeatedPressesDrillDeeperOneLevelAtATime(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)

	// Give "Get feedback on the keybinding scheme" (the last child of
	// "Ship orgtd v0.1") a child of its own, so there's a third level to
	// drill into: inserting a sibling after it, then demoting that
	// sibling to nest under it.
	m.cursor = findRow(t, m, "Get feedback on the keybinding scheme")
	orig := m.currentHeadline()
	m = sendKey(m, "o")
	m = commitTentative(t, m, orig, "** TODO Sub-task of the feedback item\n")
	grandchild := m.currentHeadline()
	m = sendKey(m, ">")
	m = sendKey(m, ">")
	if grandchild.Parent == nil || grandchild.Parent.Title != "Get feedback on the keybinding scheme" {
		t.Fatalf("setup failed: grandchild parent = %v", grandchild.Parent)
	}

	// Each "$" should move exactly one level down, mirroring "^" moving
	// exactly one level up.
	m.cursor = findRow(t, m, "Ship orgtd v0.1")
	m = sendKey(m, "$")
	if h := m.currentHeadline(); h == nil || h.Title != "Get feedback on the keybinding scheme" {
		t.Fatalf("first $ = %v, want 'Get feedback on the keybinding scheme'", h)
	}

	m = sendKey(m, "$")
	if h := m.currentHeadline(); h != grandchild {
		t.Fatalf("second $ = %v, want the grandchild %v", h, grandchild)
	}

	m = sendKey(m, "$")
	if h := m.currentHeadline(); h != grandchild {
		t.Errorf("third $ = %v, want to stay on the leaf %v (no-op)", h, grandchild)
	}
}

func TestDollarFromFileRowJumpsToLastChild(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findFileRow(t, m, "inbox.org")

	m = sendKey(m, "$")

	h := m.currentHeadline()
	if h == nil || h.Title != "Follow up with finance about the Q3 budget doc" {
		t.Errorf("cursor after $ from the file row = %v, want the last top-level headline", h)
	}
}

func TestDollarFromFileRowThenRepeatedPressesDrillIntoChildren(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findFileRow(t, m, "projects.org")

	m = sendKey(m, "$")
	if h := m.currentHeadline(); h == nil || h.Title != "Learn Go generics" {
		t.Fatalf("$ from file row = %v, want 'Learn Go generics'", h)
	}

	m = sendKey(m, "$")
	if h := m.currentHeadline(); h == nil || h.Title != "Build a toy constraint-checker" {
		t.Fatalf("second $ = %v, want 'Build a toy constraint-checker' (last child of 'Learn Go generics')", h)
	}

	m = sendKey(m, "$")
	if h := m.currentHeadline(); h == nil || h.Title != "Build a toy constraint-checker" {
		t.Errorf("third $ = %v, want to stay put (leaf, no-op)", h)
	}
}

func TestCaretThenDollarAreInverseAtEachLevel(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Implement the org file parser") // child of "Ship orgtd v0.1"

	m = sendKey(m, "^")
	h := m.currentHeadline()
	if h == nil || h.Title != "Ship orgtd v0.1" {
		t.Fatalf("^ = %v, want the enclosing parent 'Ship orgtd v0.1'", h)
	}
	m = sendKey(m, "$")
	if h := m.currentHeadline(); h == nil || h.Title != "Get feedback on the keybinding scheme" {
		t.Errorf("$ after ^ = %v, want 'Get feedback on the keybinding scheme' (the parent's own last child)", h)
	}
}

func TestGRespectsFoldedContentAtTheEnd(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)

	// Fold the last top-level entry in the last file so its children are
	// no longer part of m.rows at all, then verify G lands on the last
	// row that's actually visible (the folded entry itself), not some
	// hidden descendant.
	m.cursor = findRow(t, m, "Learn Go generics")
	m = sendKey(m, "z")
	m = sendKey(m, "c")

	m = sendKey(m, "G")
	if want := len(m.rows) - 1; m.cursor != want {
		t.Fatalf("cursor after G = %d, want %d (the last visible row)", m.cursor, want)
	}
	if h := m.currentHeadline(); h == nil || h.Title != "Learn Go generics" {
		t.Errorf("G landed on %v, want the folded 'Learn Go generics' entry", h)
	}
}

func TestGIsNoopWithNoFiles(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.ws.Files = nil
	m.rebuildRows()
	before := m.cursor

	m = sendKey(m, "G")
	if m.cursor != before {
		t.Errorf("G with no files changed the cursor: %d -> %d", before, m.cursor)
	}
}

func TestFoldTogglesChildRows(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.height = 20

	// Row 0 is the inbox.org file header, row 1 is its first (childless)
	// TODO. The first headline with children is "Ship orgtd v0.1" in
	// projects.org; find it by walking rows.
	idx := -1
	for i, r := range m.rows {
		if r.headline != nil && !r.isBodyLine && len(r.headline.Children) > 0 {
			idx = i
			break
		}
	}
	if idx == -1 {
		t.Fatalf("fixture has no headline with children")
	}
	h := m.rows[idx].headline
	before := len(m.rows)
	hidden := subtreeRowCount(h) - 1 // everything but h's own row

	m.cursor = idx
	m = sendKey(m, "tab")
	if !m.collapsed[h] {
		t.Fatalf("expected headline to be collapsed")
	}
	if len(m.rows) != before-hidden {
		t.Errorf("rows after collapse = %d, want %d", len(m.rows), before-hidden)
	}

	m = sendKey(m, "tab")
	if m.collapsed[h] {
		t.Fatalf("expected headline to be expanded again")
	}
	if len(m.rows) != before {
		t.Errorf("rows after re-expand = %d, want %d", len(m.rows), before)
	}
}

func TestFoldOnLeafIsNoop(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.height = 20

	// Row 0 is a file header; row 1 is a leaf headline (no children).
	m.cursor = 1
	if m.rows[1].headline == nil || len(m.rows[1].headline.Children) != 0 {
		t.Fatalf("fixture assumption broken: row 1 is not a leaf headline")
	}
	before := len(m.rows)
	m = sendKey(m, "tab")
	if len(m.rows) != before {
		t.Errorf("rows changed after folding a leaf: %d != %d", len(m.rows), before)
	}
}

func TestRotateStatusCyclesThroughStates(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.height = 20
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	h := m.currentHeadline()
	if h.Keyword != "TODO" {
		t.Fatalf("fixture assumption broken: keyword = %q, want TODO", h.Keyword)
	}

	wantSequence := []string{"NEXT", "WAITING", "SOMEDAY", "DONE", "CANCELLED", "", "TODO"}
	for _, want := range wantSequence {
		m = sendKey(m, "r")
		if h.Keyword != want {
			t.Fatalf("after r, keyword = %q, want %q", h.Keyword, want)
		}
		switch want {
		case "DONE":
			if h.Closed == nil {
				t.Errorf("keyword = DONE, want CLOSED to be stamped")
			}
		case "":
			if h.Closed != nil {
				t.Errorf("keyword = %q (not done), want CLOSED cleared, got %v", want, h.Closed)
			}
		}
	}
}

func TestRotateStatusNoopOnFileRow(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.height = 20
	m.cursor = 0
	if m.rows[0].file == nil {
		t.Fatalf("fixture assumption broken: row 0 is not a file row")
	}
	// Should not panic, and should not change mode or cursor.
	m = sendKey(m, "r")
	if m.mode != normalMode || m.cursor != 0 {
		t.Errorf("r on a file row changed state: mode=%v cursor=%d", m.mode, m.cursor)
	}
}

func TestSelectModePreselectsCurrentStatus(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.height = 20
	m.cursor = findRow(t, m, "Get feedback on the keybinding scheme")
	h := m.currentHeadline()
	if h.Keyword != "WAITING" {
		t.Fatalf("fixture assumption broken: keyword = %q, want WAITING", h.Keyword)
	}

	m = sendKey(m, "R")
	if m.mode != selectMode {
		t.Fatalf("mode = %v, want selectMode", m.mode)
	}
	if got, want := statusCandidates[m.selectIndex].keyword, "WAITING"; got != want {
		t.Errorf("preselected keyword = %q, want %q", got, want)
	}
}

func TestSelectModeShortcutAppliesImmediately(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.height = 20
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	h := m.currentHeadline()

	m = sendKey(m, "R")
	m = sendKey(m, "d") // shortcut for DONE
	if m.mode != normalMode {
		t.Fatalf("mode after shortcut = %v, want normalMode (auto-apply)", m.mode)
	}
	if h.Keyword != "DONE" {
		t.Errorf("keyword = %q, want DONE", h.Keyword)
	}
	if h.Closed == nil {
		t.Errorf("expected CLOSED to be stamped")
	}
}

func TestSelectModeNoneShortcutClears(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.height = 20
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	h := m.currentHeadline()

	m = sendKey(m, "R")
	m = sendKey(m, "-")
	if h.Keyword != "" {
		t.Errorf("keyword = %q, want empty (none)", h.Keyword)
	}
	if m.mode != normalMode {
		t.Errorf("mode = %v, want normalMode", m.mode)
	}
}

func TestSelectModeInvalidKeyIgnored(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.height = 20
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	h := m.currentHeadline()

	m = sendKey(m, "R")
	m = sendKey(m, "x") // not a shortcut for any candidate
	if m.mode != selectMode {
		t.Fatalf("mode after invalid key = %v, want still selectMode", m.mode)
	}
	if m.selectFilter != "" {
		t.Errorf("selectFilter = %q, want unchanged (rejected)", m.selectFilter)
	}
	if h.Keyword != "TODO" {
		t.Errorf("keyword changed to %q on an invalid key", h.Keyword)
	}
}

func TestSelectModeNavigateAndEnter(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.height = 20
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	h := m.currentHeadline()

	m = sendKey(m, "R") // preselects TODO (index 0)
	m = sendKey(m, "j") // -> NEXT
	m = sendKey(m, "j") // -> WAITING
	m = sendKey(m, "enter")

	if m.mode != normalMode {
		t.Fatalf("mode after enter = %v, want normalMode", m.mode)
	}
	if h.Keyword != "WAITING" {
		t.Errorf("keyword = %q, want WAITING", h.Keyword)
	}
}

func TestSelectModeEscCancelsWithoutChange(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.height = 20
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	h := m.currentHeadline()

	m = sendKey(m, "R")
	m = sendKey(m, "j")
	m = sendKey(m, "esc")

	if m.mode != normalMode {
		t.Fatalf("mode after esc = %v, want normalMode", m.mode)
	}
	if h.Keyword != "TODO" {
		t.Errorf("keyword changed to %q after esc, want unchanged TODO", h.Keyword)
	}
}

func writeTempOrgFile(t *testing.T, content string) string {
	t.Helper()
	tmp, err := os.CreateTemp(t.TempDir(), "edit-*.org")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	if _, err := tmp.WriteString(content); err != nil {
		t.Fatalf("WriteString: %v", err)
	}
	if err := tmp.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return tmp.Name()
}

func TestFinishEditFileReloadsFromDisk(t *testing.T) {
	ws := loadFixtureCopy(t)
	m := New(ws)
	fileIdx := findFileRow(t, m, "inbox.org")
	m.cursor = fileIdx
	oldFile := m.rows[fileIdx].file

	// Simulate an external editor changing the file directly on disk.
	if err := os.WriteFile(oldFile.Path, []byte("#+TITLE: Inbox\n\n* TODO Edited directly on disk\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	updated, cmd := m.Update(fileEditFinishedMsg{target: oldFile})
	m = updated.(Model)
	if cmd != nil {
		t.Errorf("finishEditFile should not return a follow-up command")
	}

	newFile := m.ws.Files[0]
	if newFile == oldFile {
		t.Fatalf("expected the *org.File pointer to be replaced")
	}
	if len(newFile.Headlines) != 1 || newFile.Headlines[0].Title != "Edited directly on disk" {
		t.Fatalf("reloaded file = %#v, want the on-disk content", newFile.Headlines)
	}
	if m.rows[fileIdx].file != newFile {
		t.Errorf("row list wasn't rebuilt with the new file")
	}
	if m.cursor != fileIdx {
		t.Errorf("cursor = %d, want to stay on the file row %d", m.cursor, fileIdx)
	}
}

func TestFinishEditFileMarksFileClean(t *testing.T) {
	ws := loadFixtureCopy(t)
	m := New(ws)
	fileIdx := findFileRow(t, m, "inbox.org")
	oldFile := m.rows[fileIdx].file
	// Dirty it first, so we can confirm the reload clears that state
	// (the editor already wrote the file — nothing left for orgtd to
	// save).
	m.dirty[oldFile] = true

	if err := os.WriteFile(oldFile.Path, []byte("#+TITLE: Inbox\n\n* TODO Edited directly on disk\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	updated, _ := m.Update(fileEditFinishedMsg{target: oldFile})
	m = updated.(Model)

	if m.dirty[m.ws.Files[0]] {
		t.Errorf("reloaded file shows as dirty, want clean (already saved by the editor)")
	}
}

func TestFinishEditFileClearsMarksAndClarifyTargetOnThatFile(t *testing.T) {
	ws := loadFixtureCopy(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	m = sendKey(m, "m")
	m = sendKey(m, "a")
	m.enterClarifyView()
	oldFile := m.ws.Files[0]

	if err := os.WriteFile(oldFile.Path, []byte("#+TITLE: Inbox\n\n* TODO Something else entirely\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	updated, _ := m.Update(fileEditFinishedMsg{target: oldFile})
	m = updated.(Model)

	if _, ok := m.marks['a']; ok {
		t.Errorf("mark 'a' survived the file being reloaded out from under it")
	}
	if m.clarifyTarget != nil {
		t.Errorf("clarifyTarget = %v, want nil (its file was reloaded)", m.clarifyTarget)
	}
}

func TestFinishEditFileErrorShowsMessage(t *testing.T) {
	ws := loadFixtureCopy(t)
	m := New(ws)
	oldFile := m.ws.Files[0]

	updated, _ := m.Update(fileEditFinishedMsg{target: oldFile, err: errors.New("boom")})
	m = updated.(Model)

	if m.message == "" {
		t.Errorf("expected an error message when the editor exits with an error")
	}
	if m.ws.Files[0] != oldFile {
		t.Errorf("file was replaced despite the editor erroring out")
	}
}

func TestIKeyOnFileRowAsksForConfirmation(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = 0
	if m.rows[0].file == nil {
		t.Fatalf("fixture assumption broken: row 0 is not a file row")
	}
	m, cmd := sendKeyCmd(m, "i")
	if cmd != nil {
		t.Errorf("expected no command yet — should wait for confirmation first")
	}
	if m.mode != confirmMode {
		t.Fatalf("mode = %v, want confirmMode", m.mode)
	}
	if m.pendingFileEdit != m.rows[0].file {
		t.Errorf("pendingFileEdit = %v, want the file row's file", m.pendingFileEdit)
	}
	if m.confirmMessage == "" {
		t.Errorf("expected a non-empty confirmation message")
	}
}

func TestConfirmingFileEditLaunchesTheEditor(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = 0
	m = sendKey(m, "i")
	if m.mode != confirmMode {
		t.Fatalf("fixture assumption broken: expected confirmMode after i")
	}

	m, cmd := sendKeyCmd(m, "y")

	if m.mode != normalMode {
		t.Errorf("mode after y = %v, want normalMode", m.mode)
	}
	if cmd == nil {
		t.Errorf("expected the edit command to be returned after confirming")
	}
	if m.pendingFileEdit != nil {
		t.Errorf("pendingFileEdit = %v, want cleared after confirming", m.pendingFileEdit)
	}
}

func TestDecliningFileEditCancelsWithoutEditing(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = 0
	m = sendKey(m, "i")

	m, cmd := sendKeyCmd(m, "n")

	if m.mode != normalMode {
		t.Errorf("mode after n = %v, want normalMode", m.mode)
	}
	if cmd != nil {
		t.Errorf("expected no command after declining")
	}
	if m.pendingFileEdit != nil {
		t.Errorf("pendingFileEdit = %v, want cleared after declining", m.pendingFileEdit)
	}
}

func TestAnyOtherKeyDeclinesTheFileEditConfirmation(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = 0
	m = sendKey(m, "i")

	m, cmd := sendKeyCmd(m, "esc")

	if m.mode != normalMode || cmd != nil {
		t.Errorf("esc during confirmation: mode = %v, cmd = %v, want normalMode and nil", m.mode, cmd)
	}
}

func TestIKeyOnHeadlineReturnsEditCmd(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	_, cmd := sendKeyCmd(m, "i")
	if cmd == nil {
		t.Fatalf("expected a non-nil edit command")
	}
	// Deliberately not invoking cmd() — that would actually launch $EDITOR.
}

// TestAKeyOnHeadlineReturnsEditCmd mirrors TestIKeyOnHeadlineReturnsEditCmd:
// "A" goes through the exact same startEditWithPlacement path as "i",
// just with a different editorCursorPlacement, so it should behave
// identically at this level (mode, non-nil command).
func TestAKeyOnHeadlineReturnsEditCmd(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	_, cmd := sendKeyCmd(m, "A")
	if cmd == nil {
		t.Fatalf("expected a non-nil edit command")
	}
	// Deliberately not invoking cmd() — that would actually launch $EDITOR.
}

func TestAKeyOnFileRowAsksForConfirmation(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = 0
	if m.rows[0].file == nil {
		t.Fatalf("fixture assumption broken: row 0 is not a file row")
	}
	m, cmd := sendKeyCmd(m, "A")
	if cmd != nil {
		t.Errorf("expected no command yet — should wait for confirmation first")
	}
	if m.mode != confirmMode {
		t.Fatalf("mode = %v, want confirmMode", m.mode)
	}
	if m.pendingFileEdit != m.rows[0].file {
		t.Errorf("pendingFileEdit = %v, want the file row's file", m.pendingFileEdit)
	}
}

// TestZAStillTogglesFoldRecursively guards against a regression: "A" now
// also means "start editing with the cursor at the end of the first
// line" in normal mode, so the existing "zA" chord (fold toggle,
// recursive) must still take priority over that when 'z' was pressed
// first.
func TestZAStillTogglesFoldRecursively(t *testing.T) {
	ws := agendaFixture(t, "* TODO Parent\n** TODO Child\n")
	m := New(ws)
	m.cursor = findRow(t, m, "Parent")

	m = sendKey(m, "z")
	_, cmd := sendKeyCmd(m, "A")
	if cmd != nil {
		t.Error("zA should toggle folding, not launch the editor")
	}
}

func TestEditReplacesHeadlineContent(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.height = 20
	idx := findRow(t, m, "Call the vet about Fido's checkup")
	m.cursor = idx
	old := m.currentHeadline()

	path := writeTempOrgFile(t, "* NEXT Call the vet about Fido's checkup ASAP\n  :PROPERTIES:\n  :ID: new-id\n  :END:\n")

	updated, cmd := m.Update(editFinishedMsg{path: path, target: old})
	m = updated.(Model)
	if cmd != nil {
		t.Errorf("finishEdit should not return a follow-up command")
	}

	got := m.rows[idx].headline
	if got == old {
		t.Fatalf("expected the headline pointer to be replaced")
	}
	if got.Title != "Call the vet about Fido's checkup ASAP" {
		t.Errorf("title = %q", got.Title)
	}
	if got.Keyword != "NEXT" {
		t.Errorf("keyword = %q, want NEXT", got.Keyword)
	}
	if got.Properties["ID"] != "new-id" {
		t.Errorf("ID property = %q, want new-id", got.Properties["ID"])
	}
}

func TestEditSubtreeReplacesWholeTree(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.height = 30
	idx := findRow(t, m, "Ship orgtd v0.1")
	m.cursor = idx
	old := m.currentHeadline()
	if len(old.Children) == 0 {
		t.Fatalf("fixture assumption broken: expected 'Ship orgtd v0.1' to have children")
	}

	edited := strings.Replace(org.RenderHeadline(old), "Ship orgtd v0.1", "Ship orgtd v0.2", 1)
	path := writeTempOrgFile(t, edited)

	updated, _ := m.Update(editFinishedMsg{path: path, target: old})
	m = updated.(Model)

	got := m.rows[idx].headline
	if got.Title != "Ship orgtd v0.2" {
		t.Errorf("title = %q, want Ship orgtd v0.2", got.Title)
	}
	if len(got.Children) != len(old.Children) {
		t.Fatalf("children = %d, want %d", len(got.Children), len(old.Children))
	}
	for i := range old.Children {
		if got.Children[i].Title != old.Children[i].Title {
			t.Errorf("child %d title = %q, want %q", i, got.Children[i].Title, old.Children[i].Title)
		}
	}
	// The rebuilt row list should walk into the new subtree's children
	// too — right after any body line(s) the re-rendered entry kept.
	childRow := idx + 1 + len(visibleBodyLines(got))
	if m.rows[childRow].headline == nil || m.rows[childRow].headline.Title != old.Children[0].Title {
		t.Errorf("row after edited entry (and any body) = %#v, want first child %q", m.rows[childRow], old.Children[0].Title)
	}
}

func TestEditEmptyResultIsRejected(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.height = 20
	idx := findRow(t, m, "Call the vet about Fido's checkup")
	m.cursor = idx
	old := m.currentHeadline()

	path := writeTempOrgFile(t, "")

	updated, _ := m.Update(editFinishedMsg{path: path, target: old})
	m = updated.(Model)

	if m.rows[idx].headline != old {
		t.Errorf("headline was replaced despite an empty edit result")
	}
	if m.message == "" {
		t.Errorf("expected an error message explaining the empty edit result")
	}
}

func TestEditorErrorLeavesOriginalUnchanged(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.height = 20
	idx := findRow(t, m, "Call the vet about Fido's checkup")
	m.cursor = idx
	old := m.currentHeadline()

	updated, _ := m.Update(editFinishedMsg{path: "/nonexistent", target: old, err: errors.New("boom")})
	m = updated.(Model)

	if m.rows[idx].headline != old {
		t.Errorf("headline was replaced despite an editor error")
	}
	if !strings.Contains(m.message, "boom") {
		t.Errorf("message = %q, want it to mention the editor error", m.message)
	}
}

func isQuitCmd(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func TestBareQNoLongerQuits(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	before := m.cursor
	m, cmd := sendKeyCmd(m, "q")
	if isQuitCmd(cmd) {
		t.Fatalf("bare 'q' should no longer quit; use command mode (:q)")
	}
	if m.mode != normalMode || m.cursor != before {
		t.Errorf("bare 'q' should be a no-op in normal mode")
	}
}

func TestCtrlCAlwaysQuits(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if !isQuitCmd(cmd) {
		t.Fatalf("ctrl+c should always quit")
	}

	// Also from within command mode.
	m = sendKey(m, ":")
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if !isQuitCmd(cmd) {
		t.Fatalf("ctrl+c should quit even while typing a command")
	}
}

func TestColonEntersCommandMode(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m = sendKey(m, ":")
	if m.mode != commandMode {
		t.Fatalf("mode = %v, want commandMode", m.mode)
	}
	if m.commandInput != "" {
		t.Errorf("commandInput = %q, want empty", m.commandInput)
	}
}

func TestCommandModeShowsCursor(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.width, m.height = 100, 30

	m = sendKey(m, ":")
	m = typeKeys(m, "q")

	lines := strings.Split(m.View(), "\n")
	last := lines[len(lines)-1]

	if !strings.HasPrefix(last, ":q") {
		t.Fatalf("status line = %q, want it to start with the typed command", last)
	}
	// The caret is rendered as a reverse-video space (SGR 7) after the
	// typed text, like a terminal block cursor.
	if !strings.Contains(last, "\x1b[7m") {
		t.Errorf("status line = %q, want a reverse-video cursor caret", last)
	}
}

func TestCommandModeCaretStaysRightAfterInputWhenCompletionsShown(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.width, m.height = 100, 30

	m = sendKey(m, ":")
	m = typeKeys(m, "w")
	m = sendKey(m, "tab")
	if m.commandCompletions == "" {
		t.Fatalf("fixture assumption broken: expected a completion list after tab")
	}

	lines := strings.Split(m.View(), "\n")
	last := lines[len(lines)-1]

	// The caret must sit immediately after ":w" — not after the
	// completion list — so it still reads as "this is where your typed
	// input ends", with the completions shown as a hint past it.
	caretIdx := strings.Index(last, "\x1b[7m")
	completionsIdx := strings.Index(last, "w  wq  write")
	if caretIdx < 0 || completionsIdx < 0 {
		t.Fatalf("status line = %q, missing caret or completions", last)
	}
	if !strings.HasPrefix(last, ":w\x1b[7m") {
		t.Errorf("status line = %q, want the caret immediately after ':w'", last)
	}
	if caretIdx >= completionsIdx {
		t.Errorf("status line = %q, want the caret before the completion list", last)
	}
}

func TestCommandQuit(t *testing.T) {
	for _, cmdText := range []string{"q", "quit"} {
		t.Run(cmdText, func(t *testing.T) {
			ws := loadFixture(t)
			m := New(ws)
			m = sendKey(m, ":")
			m = typeKeys(m, cmdText)
			m, cmd := sendKeyCmd(m, "enter")
			if !isQuitCmd(cmd) {
				t.Fatalf(":%s should quit", cmdText)
			}
			if m.mode != normalMode {
				t.Errorf("mode after quitting = %v, want normalMode", m.mode)
			}
		})
	}
}

func TestCommandQuitBlockedByUnsavedChanges(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	m = sendKey(m, "r") // TODO -> NEXT, marks the file dirty

	for _, cmdText := range []string{"q", "quit"} {
		t.Run(cmdText, func(t *testing.T) {
			mm := m
			mm = sendKey(mm, ":")
			mm = typeKeys(mm, cmdText)
			mm, cmd := sendKeyCmd(mm, "enter")
			if isQuitCmd(cmd) {
				t.Fatalf(":%s should refuse to quit with unsaved changes", cmdText)
			}
			if mm.message == "" {
				t.Errorf("expected a message explaining why :%s refused", cmdText)
			}
		})
	}
}

func TestCommandForceQuitDiscardsChanges(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	m = sendKey(m, "r") // marks the file dirty

	for _, cmdText := range []string{"q!", "quit!"} {
		t.Run(cmdText, func(t *testing.T) {
			mm := m
			mm = sendKey(mm, ":")
			mm = typeKeys(mm, cmdText)
			mm, cmd := sendKeyCmd(mm, "enter")
			if !isQuitCmd(cmd) {
				t.Fatalf(":%s should force-quit despite unsaved changes", cmdText)
			}
		})
	}
}

func TestCommandWriteWritesDirtyFilesToDisk(t *testing.T) {
	ws := loadFixtureCopy(t)
	m := New(ws)
	idx := findRow(t, m, "Call the vet about Fido's checkup")
	m.cursor = idx
	filePath := ws.Files[0].Path // inbox.org, sorted first

	m = sendKey(m, "r") // TODO -> NEXT
	if len(m.dirty) != 1 {
		t.Fatalf("dirty files = %d, want 1", len(m.dirty))
	}

	m = sendKey(m, ":")
	m = typeKeys(m, "w")
	m, cmd := sendKeyCmd(m, "enter")
	if cmd != nil {
		t.Errorf(":w should not return a command")
	}
	if !strings.Contains(m.message, "Wrote") {
		t.Errorf("message = %q, want it to confirm the write", m.message)
	}
	if len(m.dirty) != 0 {
		t.Errorf("dirty files after :w = %d, want 0", len(m.dirty))
	}

	reparsed, err := org.ParseFile(filePath)
	if err != nil {
		t.Fatalf("ParseFile after :w: %v", err)
	}
	var found *org.Headline
	org.Walk(reparsed.Headlines, func(h *org.Headline) {
		if h.Title == "Call the vet about Fido's checkup" {
			found = h
		}
	})
	if found == nil {
		t.Fatalf("edited headline not found on disk after :w")
	}
	if found.Keyword != "NEXT" {
		t.Errorf("keyword on disk = %q, want NEXT", found.Keyword)
	}
}

func TestCommandWriteWithNoChangesReportsNothingToDo(t *testing.T) {
	ws := loadFixtureCopy(t)
	m := New(ws)
	m = sendKey(m, ":")
	m = typeKeys(m, "w")
	m = sendKey(m, "enter")
	if !strings.Contains(m.message, "No changes") {
		t.Errorf("message = %q, want it to say there was nothing to write", m.message)
	}
}

func TestCommandWqWritesThenQuits(t *testing.T) {
	ws := loadFixtureCopy(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	m = sendKey(m, "r")

	m = sendKey(m, ":")
	m = typeKeys(m, "wq")
	m, cmd := sendKeyCmd(m, "enter")
	if !isQuitCmd(cmd) {
		t.Fatalf(":wq should quit after a successful write")
	}
	if len(m.dirty) != 0 {
		t.Errorf("dirty files after :wq = %d, want 0", len(m.dirty))
	}
}

func TestFileRowShowsDirtyIndicatorAfterEdit(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	fileIdx := findFileRow(t, m, "inbox.org")
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")

	if strings.Contains(m.renderRow(m.rows[fileIdx]), "+") {
		t.Fatalf("dirty indicator shown before any edit")
	}

	m = sendKey(m, "r")
	if !strings.Contains(m.renderRow(m.rows[fileIdx]), "+") {
		t.Errorf("expected a dirty indicator on the file row after an edit")
	}
}

func TestHeadlineRowShowsDirtyMarkerAfterStatusChange(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	idx := findRow(t, m, "Call the vet about Fido's checkup")
	otherIdx := findRow(t, m, "Read the RFC linked in yesterday's design review")
	m.cursor = idx

	if strings.Contains(m.renderRow(m.rows[idx]), "+") {
		t.Fatalf("dirty marker shown before any edit")
	}

	m = sendKey(m, "r")

	h := m.currentHeadline()
	if !m.dirtyHeadlines[h] {
		t.Errorf("expected the edited headline to be marked dirty")
	}
	if !strings.Contains(m.renderRow(m.rows[idx]), "+") {
		t.Errorf("expected a dirty marker on the edited row")
	}
	if strings.Contains(m.renderRow(m.rows[otherIdx]), "+") {
		t.Errorf("unrelated row shows a dirty marker")
	}
}

func TestHeadlineDirtyMarkerClearedAfterWrite(t *testing.T) {
	ws := loadFixtureCopy(t)
	m := New(ws)
	idx := findRow(t, m, "Call the vet about Fido's checkup")
	m.cursor = idx
	m = sendKey(m, "r")

	h := m.currentHeadline()
	if !m.dirtyHeadlines[h] {
		t.Fatalf("expected headline to be dirty before :w")
	}

	m = sendKey(m, ":")
	m = typeKeys(m, "w")
	m = sendKey(m, "enter")

	if m.dirtyHeadlines[h] {
		t.Errorf("expected the dirty marker to clear after a successful :w")
	}
	if strings.Contains(m.renderRow(m.rows[idx]), "+") {
		t.Errorf("row still shows a dirty marker after :w")
	}
}

func TestEditMarksWholeSubtreeDirty(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	idx := findRow(t, m, "Ship orgtd v0.1")
	m.cursor = idx
	old := m.currentHeadline()
	if len(old.Children) == 0 {
		t.Fatalf("fixture assumption broken: expected children")
	}

	edited := strings.Replace(org.RenderHeadline(old), "Ship orgtd v0.1", "Ship orgtd v0.2", 1)
	path := writeTempOrgFile(t, edited)

	updated, _ := m.Update(editFinishedMsg{path: path, target: old})
	m = updated.(Model)

	newHead := m.rows[idx].headline
	if !m.dirtyHeadlines[newHead] {
		t.Errorf("expected the edited root to be marked dirty")
	}
	for i, c := range newHead.Children {
		if !m.dirtyHeadlines[c] {
			t.Errorf("expected child %d (%q) to be marked dirty too", i, c.Title)
		}
	}
	// The old (discarded) headline's dirty entry shouldn't linger.
	if m.dirtyHeadlines[old] {
		t.Errorf("stale entry for the replaced headline was not cleaned up")
	}
}

func TestCommandEscCancels(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m = sendKey(m, ":")
	m = typeKeys(m, "qui")
	m = sendKey(m, "esc")
	if m.mode != normalMode {
		t.Fatalf("mode after esc = %v, want normalMode", m.mode)
	}
	if m.commandInput != "" {
		t.Errorf("commandInput after esc = %q, want empty", m.commandInput)
	}
}

func TestCommandBackspace(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m = sendKey(m, ":")
	m = typeKeys(m, "qz")
	m = sendKey(m, "backspace")
	if m.commandInput != "q" {
		t.Fatalf("commandInput after backspace = %q, want %q", m.commandInput, "q")
	}
	m, cmd := sendKeyCmd(m, "enter")
	if !isQuitCmd(cmd) {
		t.Fatalf("expected :q (after backspacing off the typo) to quit")
	}
}

func TestCommandBackspaceOnEmptyInputExitsCommandMode(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	before := m.cursor

	m = sendKey(m, ":")
	m = sendKey(m, "backspace")

	if m.mode != normalMode {
		t.Fatalf("mode after backspace on empty command input = %v, want normalMode", m.mode)
	}
	if m.cursor != before {
		t.Errorf("cursor changed = %d, want unchanged %d", m.cursor, before)
	}
}

func TestCommandBackspaceWithTextDoesNotExitCommandMode(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m = sendKey(m, ":")
	m = typeKeys(m, "q")
	m = sendKey(m, "backspace")

	if m.mode != commandMode {
		t.Fatalf("mode after backspace with remaining text = %v, want commandMode", m.mode)
	}
	if m.commandInput != "" {
		t.Errorf("commandInput = %q, want empty", m.commandInput)
	}
}

func TestUnknownCommandShowsMessage(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m = sendKey(m, ":")
	m = typeKeys(m, "bogus")
	m, cmd := sendKeyCmd(m, "enter")
	if isQuitCmd(cmd) {
		t.Fatalf("unknown command should not quit")
	}
	if m.mode != normalMode {
		t.Errorf("mode after unknown command = %v, want normalMode", m.mode)
	}
	if !strings.Contains(m.message, "bogus") {
		t.Errorf("message = %q, want it to mention the unrecognized command", m.message)
	}

	// The message is transient: any further key press clears it.
	m = sendKey(m, "j")
	if m.message != "" {
		t.Errorf("message after next key press = %q, want cleared", m.message)
	}
}

func TestDirtyGutterIsLeftmostAndConsistentAcrossRows(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	fileIdx := findFileRow(t, m, "inbox.org")
	itemIdx := findRow(t, m, "Call the vet about Fido's checkup")
	nestedIdx := findRow(t, m, "Write the design document") // a level-2 headline, indented further

	m.cursor = itemIdx
	m = sendKey(m, "r")

	fileLine := stripANSI(m.renderRow(m.rows[fileIdx]))
	itemLine := stripANSI(m.renderRow(m.rows[itemIdx]))
	nestedLine := stripANSI(m.renderRow(m.rows[nestedIdx]))

	// Column 0 is the mark/clarify column, column 1 is the :format-links
	// lock column, column 2 is the dirty marker — three separate columns
	// (see markColumn/lockColumn/gutter) so a row that's both marked and
	// dirty (or locked, or all three) can show every indicator at once.
	// The dirty marker sits in column 2 for both the file row and the
	// changed item, regardless of the item's indentation depth; columns
	// 0 and 1 are blank here since nothing is marked or locked.
	if r := []rune(fileLine); len(r) < 3 || r[0] != ' ' || r[1] != ' ' || r[2] != '+' {
		t.Errorf("file row gutter columns wrong: %q", fileLine)
	}
	if r := []rune(itemLine); len(r) < 3 || r[0] != ' ' || r[1] != ' ' || r[2] != '+' {
		t.Errorf("changed item row gutter columns wrong: %q", itemLine)
	}
	// An unrelated, more deeply nested row is unaffected and keeps every
	// gutter column blank.
	if r := []rune(nestedLine); len(r) < 3 || r[0] != ' ' || r[1] != ' ' || r[2] != ' ' {
		t.Errorf("unrelated nested row should have blank gutter columns, got: %q", nestedLine)
	}
}

func TestViewRendersFileNamesAndKeywords(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	// Tall enough to show every row regardless of what else happens to be
	// in the (shared, possibly-extended) fixture directory.
	m.width, m.height = 100, len(m.rows)+5

	out := m.View()
	for _, want := range []string{"inbox.org", "projects.org", "NEXT", "WAITING", "DONE"} {
		if !strings.Contains(out, want) {
			t.Errorf("View() missing %q\n---\n%s", want, out)
		}
	}
}

func TestViewPadsStatusBarToBottomOfScreen(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	total := len(m.rows)
	m.width, m.height = 100, total+20 // taller than the fixture, whatever its size

	out := m.View()
	lines := strings.Split(out, "\n")

	// pageSize() reserves two lines at the bottom — the status line and,
	// below it, a command line (see statusHeight) — so the output should
	// have exactly m.height lines: (height-2) item/blank lines, then the
	// status line, then a blank (idle) command line.
	if len(lines) != m.height {
		t.Fatalf("got %d lines, want %d (height)\n---\n%s", len(lines), m.height, out)
	}

	statusLine := lines[len(lines)-2]
	wantStatus := fmt.Sprintf("item 1/%d", total)
	if !strings.Contains(statusLine, wantStatus) {
		t.Errorf("status line = %q, want it to contain %q", statusLine, wantStatus)
	}

	commandLine := lines[len(lines)-1]
	if commandLine != "" {
		t.Errorf("command line = %q, want blank (idle)", commandLine)
	}

	// Every line between the last row and the status line should be blank.
	for i := len(m.rows); i < m.height-2; i++ {
		if lines[i] != "" {
			t.Errorf("line %d = %q, want blank padding", i, lines[i])
		}
	}
}
