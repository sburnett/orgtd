package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sburnett/orgtd/internal/org"
	"github.com/sburnett/orgtd/internal/workspace"
)

func loadFixture(t *testing.T) *workspace.Workspace {
	t.Helper()
	ws, err := workspace.Load("../../testdata/orgdir")
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

// findRow returns the index of the row whose headline has the given
// title (or, for a file row, whose base filename equals title).
func findRow(t *testing.T, m Model, title string) int {
	t.Helper()
	for i, r := range m.rows {
		if r.headline != nil && r.headline.Title == title {
			return i
		}
	}
	t.Fatalf("no row with title %q", title)
	return -1
}

func sendKey(m Model, key string) Model {
	var msg tea.KeyMsg
	switch key {
	case "tab":
		msg = tea.KeyMsg{Type: tea.KeyTab}
	case "up":
		msg = tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		msg = tea.KeyMsg{Type: tea.KeyDown}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	}
	updated, _ := m.Update(msg)
	return updated.(Model)
}

func TestNewLoadsAllRows(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	want := len(ws.Files) + countHeadlines(ws)
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
	// From row 0 (a file header, level 0), j only ever visits other
	// file-header rows (there are two fixture files), and stops at the
	// last one rather than descending into headlines or running off
	// the end of the list.
	if want := findFileRow(t, m, "projects.org"); m.cursor != want {
		t.Errorf("cursor after many j from row 0 = %d, want %d (projects.org file header)", m.cursor, want)
	}
}

// TestSiblingNavigationSkipsDescendantsAndHopsUp exercises j/k: they move
// between rows at the same indentation level, skipping over any deeper
// (descendant) rows, and "hop up" to a shallower row once there are no
// more rows at the current level.
func TestSiblingNavigationSkipsDescendantsAndHopsUp(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.height = 30

	m.cursor = findRow(t, m, "Ship orgtd v0.1")

	m = sendKey(m, "j")
	if got, want := m.cursor, findRow(t, m, "Quarterly planning"); got != want {
		t.Fatalf("j from 'Ship orgtd v0.1' = row %d, want %d ('Quarterly planning')", got, want)
	}

	m = sendKey(m, "j")
	if got, want := m.cursor, findRow(t, m, "Learn Go generics"); got != want {
		t.Fatalf("j from 'Quarterly planning' = row %d, want %d ('Learn Go generics')", got, want)
	}

	// 'Learn Go generics' is the last top-level project; there is
	// nothing more at level 1 and nothing shallower after it, so j is a
	// no-op here.
	before := m.cursor
	m = sendKey(m, "j")
	if m.cursor != before {
		t.Errorf("j past the last sibling moved cursor to %d, want no-op at %d", m.cursor, before)
	}

	m = sendKey(m, "k")
	if got, want := m.cursor, findRow(t, m, "Quarterly planning"); got != want {
		t.Fatalf("k back = row %d, want %d ('Quarterly planning')", got, want)
	}

	m = sendKey(m, "k")
	if got, want := m.cursor, findRow(t, m, "Ship orgtd v0.1"); got != want {
		t.Fatalf("k back = row %d, want %d ('Ship orgtd v0.1')", got, want)
	}

	// One more k hops up: there's no sibling before 'Ship orgtd v0.1',
	// so we land on its parent file header.
	m = sendKey(m, "k")
	if got, want := m.cursor, findFileRow(t, m, "projects.org"); got != want {
		t.Fatalf("k hop-up = row %d, want %d (projects.org file header)", got, want)
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

// TestMoveDeeperAndShallower exercises l/h: they move into a child /
// out to the parent, falling back to sibling-level navigation (j/k)
// when there's no deeper/shallower row to move to.
func TestMoveDeeperAndShallower(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.height = 30

	project := findRow(t, m, "Ship orgtd v0.1")
	firstChild := findRow(t, m, "Write the design document")

	m.cursor = project
	m = sendKey(m, "l")
	if m.cursor != firstChild {
		t.Fatalf("l into project = row %d, want %d (first child)", m.cursor, firstChild)
	}

	// firstChild has no children of its own, so l falls back to
	// sibling-level navigation (next child at the same depth).
	secondChild := findRow(t, m, "Implement the org file parser")
	m = sendKey(m, "l")
	if m.cursor != secondChild {
		t.Fatalf("l on a leaf = row %d, want %d (next sibling)", m.cursor, secondChild)
	}

	// h on a non-first child falls back to sibling-level navigation
	// (previous sibling), not all the way up to the parent.
	m = sendKey(m, "h")
	if m.cursor != firstChild {
		t.Fatalf("h on a non-first child = row %d, want %d (previous sibling)", m.cursor, firstChild)
	}

	// h on the first child goes to the parent.
	m = sendKey(m, "h")
	if m.cursor != project {
		t.Fatalf("h on first child = row %d, want %d (parent)", m.cursor, project)
	}

	// h on a top-level project falls back to sibling-level navigation;
	// with no previous project, that hops up to the file header.
	fileRow := findFileRow(t, m, "projects.org")
	m = sendKey(m, "h")
	if m.cursor != fileRow {
		t.Fatalf("h on the first project = row %d, want %d (file header)", m.cursor, fileRow)
	}
}

func TestGgAndG(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.height = 20

	m = sendKey(m, "G")
	if m.cursor != len(m.rows)-1 {
		t.Fatalf("cursor after G = %d, want %d", m.cursor, len(m.rows)-1)
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

func TestFoldTogglesChildRows(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.height = 20

	// Row 0 is the inbox.org file header, row 1 is its first (childless)
	// TODO. The first headline with children is "Ship orgtd v0.1" in
	// projects.org; find it by walking rows.
	idx := -1
	for i, r := range m.rows {
		if r.headline != nil && len(r.headline.Children) > 0 {
			idx = i
			break
		}
	}
	if idx == -1 {
		t.Fatalf("fixture has no headline with children")
	}
	h := m.rows[idx].headline
	before := len(m.rows)

	m.cursor = idx
	m = sendKey(m, "tab")
	if !m.collapsed[h] {
		t.Fatalf("expected headline to be collapsed")
	}
	if len(m.rows) != before-len(h.Children) {
		t.Errorf("rows after collapse = %d, want %d", len(m.rows), before-len(h.Children))
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

func TestQuitReturnsQuitCmd(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd == nil {
		t.Fatalf("expected a quit command")
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Errorf("cmd() = %#v, want tea.QuitMsg", msg)
	}
}

func TestViewRendersFileNamesAndKeywords(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.width, m.height = 100, 30

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
	m.width, m.height = 100, 50 // taller than the 17-row fixture

	out := m.View()
	lines := strings.Split(out, "\n")

	// pageSize() reserves one line for the status bar, so the output
	// should have exactly m.height lines: (height-1) item/blank lines
	// followed by the status line.
	if len(lines) != m.height {
		t.Fatalf("got %d lines, want %d (height)\n---\n%s", len(lines), m.height, out)
	}

	last := lines[len(lines)-1]
	if !strings.Contains(last, "item 1/17") {
		t.Errorf("last line = %q, want it to contain the status bar", last)
	}

	// Every line between the last row and the status bar should be blank.
	for i := len(m.rows); i < m.height-1; i++ {
		if lines[i] != "" {
			t.Errorf("line %d = %q, want blank padding", i, lines[i])
		}
	}
}
