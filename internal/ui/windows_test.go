package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// newSizedModel returns a model over the fixture workspace with a known
// terminal size, since window layout depends on it.
func newSizedModel(t *testing.T, w, h int) Model {
	t.Helper()
	m := New(loadFixture(t))
	updated, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return updated.(Model)
}

// runColon types ":"+cmd and Enter.
func runColon(m Model, cmd string) Model {
	m = typeKeys(m, ":"+cmd)
	return sendKey(m, "enter")
}

// ctrlW sends "ctrl-w" followed by key, vim's window-command prefix.
func ctrlW(m Model, key string) Model {
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlW})
	return sendKey(updated.(Model), key)
}

func windowCount(m Model) int {
	if m.layout == nil {
		return 1
	}
	return len(m.layout.leaves(nil))
}

func TestSplitOpensSecondWindowOnSameEntry(t *testing.T) {
	m := newSizedModel(t, 300, 30) // wide enough for the temp dir's path on the status line
	m = sendKey(sendKey(m, "j"), "j")
	want := m.currentHeadline()

	m = runColon(m, "split")
	if got := windowCount(m); got != 2 {
		t.Fatalf("windows = %d, want 2", got)
	}
	if m.currentHeadline() != want {
		t.Errorf("new window's cursor on %v, want %v", m.currentHeadline(), want)
	}
	// The new window is above the old one, and active.
	if all := m.layout.leaves(nil); all[0] != m.activeWin {
		t.Errorf("new window isn't the top one")
	}

	lines := strings.Split(m.View(), "\n")
	if len(lines) != 30 {
		t.Fatalf("view has %d lines, want 30", len(lines))
	}
	statusLines := 0
	for _, l := range lines {
		if strings.Contains(stripANSI(l), "item 3/") {
			statusLines++
		}
	}
	if statusLines != 2 {
		t.Errorf("found %d status lines showing item 3, want one per window:\n%s", statusLines, stripANSI(m.View()))
	}
}

func TestWindowsKeepIndependentCursorsAndViews(t *testing.T) {
	m := newSizedModel(t, 80, 30)
	m = runColon(m, "split")
	m = typeKeys(m, "jjj")
	top := m.currentHeadline()

	m = ctrlW(m, "j")
	if m.cursor != 0 {
		t.Errorf("bottom window's cursor = %d, want 0 (unmoved)", m.cursor)
	}
	m = runColon(m, "agenda")

	m = ctrlW(m, "k")
	if m.currentHeadline() != top {
		t.Errorf("top window's cursor moved: on %v, want %v", m.currentHeadline(), top)
	}
	if m.view != outlineView {
		t.Errorf("top window's view = %v, want the outline", m.view)
	}
	if !strings.Contains(stripANSI(m.View()), "agenda  —  item") {
		t.Errorf("bottom window's status line doesn't show the agenda:\n%s", stripANSI(m.View()))
	}
}

func TestEditInOneWindowShowsInTheOther(t *testing.T) {
	m := newSizedModel(t, 80, 40)
	// Bottom window on "Entry 9", top window deletes an entry above it.
	m = runColon(m, "split")
	m = ctrlW(m, "j")
	m.cursor = findRow(t, m, "Entry 12")
	target := m.currentHeadline()
	m = ctrlW(m, "k")
	m.cursor = findRow(t, m, "Entry 9")
	deleted := m.currentHeadline()
	m = sendKey(sendKey(m, "d"), "d")

	m = ctrlW(m, "j")
	for _, r := range m.rows {
		if r.headline == deleted {
			t.Fatalf("deleted entry still listed in the other window")
		}
	}
	if m.currentHeadline() != target {
		t.Errorf("other window's cursor on %v, want it to stay on Entry 12", m.currentHeadline())
	}
}

func TestVsplitDrawsWindowsSideBySide(t *testing.T) {
	m := newSizedModel(t, 81, 20)
	m = runColon(m, "vsplit")
	if m.layout == nil || !m.layout.vertical {
		t.Fatalf("layout = %+v, want a vertical split", m.layout)
	}
	if got := m.listWidth(); got != 40 {
		t.Errorf("active window width = %d, want 40", got)
	}
	lines := strings.Split(m.View(), "\n")
	if len(lines) != 20 {
		t.Fatalf("view has %d lines, want 20", len(lines))
	}
	for i, l := range lines[:20-1-m.infoBufferHeight()] {
		if w := lipgloss.Width(l); w != 81 {
			t.Errorf("line %d is %d cells wide, want 81: %q", i, w, stripANSI(l))
		}
		if !strings.Contains(l, "│") {
			t.Errorf("line %d has no separator: %q", i, stripANSI(l))
		}
	}
}

func TestCloseAndOnly(t *testing.T) {
	m := newSizedModel(t, 80, 30)
	m = runColon(m, "close")
	if m.message != "Cannot close last window" {
		t.Errorf("message = %q, want a refusal", m.message)
	}

	m = runColon(m, "split")
	m = runColon(m, "vsplit")
	if got := windowCount(m); got != 3 {
		t.Fatalf("windows = %d, want 3", got)
	}
	m = ctrlW(m, "c")
	if got := windowCount(m); got != 2 {
		t.Fatalf("after ctrl-w c, windows = %d, want 2", got)
	}
	// Closing one of the side-by-side pair leaves a plain two-way stack.
	if m.layout.vertical || len(m.layout.children) != 2 || !m.layout.children[0].isLeaf() {
		t.Errorf("layout not collapsed back to a stack of two: %+v", m.layout)
	}
	m = runColon(m, "split")
	m = ctrlW(m, "o")
	if m.layout != nil {
		t.Errorf("after ctrl-w o, still split")
	}
}

func TestQuitClosesWindowUntilTheLast(t *testing.T) {
	m := newSizedModel(t, 80, 30)
	m = typeKeys(m, "jdd") // unsaved change
	if len(m.dirty) == 0 {
		t.Fatalf("dd didn't leave an unsaved change")
	}
	m = runColon(m, "split")

	m, cmd := sendKeyCmd(typeKeys(m, ":q"), "enter")
	if cmd != nil || m.layout != nil {
		t.Fatalf(":q with two windows should close one, not quit")
	}
	m, cmd = sendKeyCmd(typeKeys(m, ":q"), "enter")
	if cmd != nil {
		t.Fatalf(":q in the last window quit despite unsaved changes")
	}

	m = runColon(m, "split")
	if _, cmd = sendKeyCmd(typeKeys(m, ":qa!"), "enter"); cmd == nil {
		t.Errorf(":qa! didn't quit")
	}
}

func TestWindowNavigationInMixedLayout(t *testing.T) {
	m := newSizedModel(t, 80, 30)
	m = runColon(m, "vsplit") // [left | right]
	right := m.layout.children[1]
	m = runColon(m, "split") // [[top-left / bottom-left] | right]
	topLeft := m.activeWin

	m = ctrlW(m, "l")
	if m.activeWin != right {
		t.Fatalf("ctrl-w l didn't reach the right-hand window")
	}
	m = ctrlW(m, "h")
	if m.activeWin != topLeft {
		t.Errorf("ctrl-w h from the right went to the wrong window")
	}
	m = ctrlW(m, "j")
	bottomLeft := m.activeWin
	if bottomLeft == topLeft || bottomLeft == right {
		t.Fatalf("ctrl-w j didn't reach the bottom-left window")
	}
	m = ctrlW(m, "j") // nothing below: stays
	if m.activeWin != bottomLeft {
		t.Errorf("ctrl-w j at the bottom edge moved")
	}
	m = ctrlW(m, "p")
	if m.activeWin != topLeft {
		t.Errorf("ctrl-w p didn't return to the previous window")
	}

	seen := map[*layoutNode]bool{}
	for range 3 {
		m = ctrlW(m, "w")
		seen[m.activeWin] = true
	}
	if len(seen) != 3 {
		t.Errorf("ctrl-w w visited %d windows in 3 presses, want all 3", len(seen))
	}
}

func TestInfoBufferIsPerWindowAndSplitStaysPut(t *testing.T) {
	m := newSizedModel(t, 300, 30)
	m = runColon(m, "split")
	// Rows split evenly: 29 above the command line, so the top window's
	// status line is its 15th line, whatever its info buffer shows.
	topStatus := func(m Model) string { return stripANSI(strings.Split(m.View(), "\n")[14]) }

	if s := topStatus(m); !strings.Contains(s, "item 1/") {
		t.Fatalf("line 15 = %q, want the top window's status line", s)
	}
	for i, r := range m.rows {
		if r.headline != nil && strings.HasPrefix(r.headline.Title, "Entry 2 ") {
			m.cursor = i
		}
	}
	m = sendKey(sendKey(m, "j"), "k") // re-clamp scrolling to the new info buffer
	if len(m.infoBufferLines()) == 0 {
		t.Fatalf("Entry 2's links didn't fill the info buffer")
	}
	if s := topStatus(m); !strings.Contains(s, "  —  item") {
		t.Errorf("with links shown, line 15 = %q, want the top window's status line still", s)
	}
	top := strings.Join(strings.Split(stripANSI(m.View()), "\n")[:15], "\n")
	if !strings.Contains(top, "Links:") {
		t.Errorf("top window's links aren't in the top window:\n%s", top)
	}
	if n := strings.Count(stripANSI(m.View()), "Links:"); n != 1 {
		t.Errorf("the bottom window (not on a linked entry) shows links too")
	}
}

func TestActiveWindowStandsOut(t *testing.T) {
	m := newSizedModel(t, 80, 30)
	m = runColon(m, "split")
	active := m.statusLine(true)
	inactive := m.statusLine(false)
	if active == inactive {
		t.Fatalf("active and inactive status lines look the same")
	}
	bgSGR := func(c lipgloss.TerminalColor) string {
		return ansiEscapeRe.FindString(lipgloss.NewStyle().Background(c).Render("x"))
	}
	if bgSGR(m.cursorBg()) == bgSGR(m.inactiveCursorBg()) {
		t.Fatalf("cursor-row colors indistinguishable under the test color profile")
	}
	view := m.View()
	if n := strings.Count(view, bgSGR(m.cursorBg())); n == 0 {
		t.Errorf("active window's cursor row isn't brightly highlighted")
	}
	if n := strings.Count(view, bgSGR(m.inactiveCursorBg())); n == 0 {
		t.Errorf("inactive window's cursor row isn't faintly highlighted")
	}
}
