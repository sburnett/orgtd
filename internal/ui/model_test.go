package ui

import (
	"errors"
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

func TestIKeyNoopOnFileRow(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = 0
	if m.rows[0].file == nil {
		t.Fatalf("fixture assumption broken: row 0 is not a file row")
	}
	_, cmd := sendKeyCmd(m, "i")
	if cmd != nil {
		t.Errorf("expected no edit command when cursor is on a file row")
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
	// The rebuilt row list should walk into the new subtree's children too.
	if m.rows[idx+1].headline == nil || m.rows[idx+1].headline.Title != old.Children[0].Title {
		t.Errorf("row after edited entry = %#v, want first child %q", m.rows[idx+1], old.Children[0].Title)
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

	// The dirty marker sits in column 0 for both the file row and the
	// changed item, regardless of the item's indentation depth.
	if r := []rune(fileLine); len(r) == 0 || r[0] != '+' {
		t.Errorf("file row does not start with the dirty marker: %q", fileLine)
	}
	if r := []rune(itemLine); len(r) == 0 || r[0] != '+' {
		t.Errorf("changed item row does not start with the dirty marker: %q", itemLine)
	}
	// An unrelated, more deeply nested row is unaffected and keeps a
	// blank gutter column in the same position.
	if r := []rune(nestedLine); len(r) == 0 || r[0] != ' ' {
		t.Errorf("unrelated nested row should have a blank gutter, got: %q", nestedLine)
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
