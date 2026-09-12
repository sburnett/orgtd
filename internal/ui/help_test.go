package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// helpLines returns the text of every row in m.rows, ANSI codes
// stripped, for content assertions below — :help's rows are rendered
// markdown (see appendHelpRows), so exact-string comparisons against
// the source markdown no longer make sense.
func helpLines(m Model) []string {
	lines := make([]string, len(m.rows))
	for i, r := range m.rows {
		lines[i] = stripANSI(r.text)
	}
	return lines
}

func TestHelpCommandSwitchesToHelpView(t *testing.T) {
	ws := agendaFixture(t, "* TODO Something\n")
	m := New(ws, WithReadme("# orgtd\n\nSome docs.\n"))

	m = sendKey(m, ":")
	m = typeKeys(m, "help")
	m, _ = sendKeyCmd(m, "enter")

	if m.view != helpView {
		t.Fatalf("view after :help = %v, want helpView", m.view)
	}
}

func TestHelpViewRendersMarkdownContent(t *testing.T) {
	ws := agendaFixture(t, "* TODO Something\n")
	m := New(ws, WithReadme("# orgtd\n\nSome docs.\n"))

	m.switchToView(helpView)

	lines := helpLines(m)
	if !containsSubstring(lines, "orgtd") {
		t.Errorf("rows = %#v, want the heading's text to appear", lines)
	}
	if !containsSubstring(lines, "Some docs.") {
		t.Errorf("rows = %#v, want the paragraph's text to appear", lines)
	}
}

// TestHelpViewRendersTablesWithAlignedColumns is the whole point of
// switching to glamour in the first place: a markdown table, unreadable
// as raw pipe-delimited text, should come out as an actual aligned grid
// with every cell's content intact.
func TestHelpViewRendersTablesWithAlignedColumns(t *testing.T) {
	md := "| Command | Action |\n|---|---|\n| :w | Write |\n"
	ws := agendaFixture(t, "* TODO Something\n")
	m := New(ws, WithReadme(md))
	m.width = 60

	m.switchToView(helpView)

	lines := helpLines(m)
	if !containsSubstring(lines, "│") {
		t.Errorf("rows = %#v, want glamour's table column borders to appear", lines)
	}
	if !containsSubstring(lines, "Command") || !containsSubstring(lines, "Action") {
		t.Errorf("rows = %#v, want the table's header cells to appear", lines)
	}
	if !containsSubstring(lines, ":w") || !containsSubstring(lines, "Write") {
		t.Errorf("rows = %#v, want the table's data cells to appear", lines)
	}
}

func TestHelpViewWordWrapsToTerminalWidth(t *testing.T) {
	long := strings.Repeat("word ", 40)
	ws := agendaFixture(t, "* TODO Something\n")

	narrow := New(ws, WithReadme(long))
	narrow.width = 20
	narrow.switchToView(helpView)

	wide := New(ws, WithReadme(long))
	wide.width = 200
	wide.switchToView(helpView)

	if len(narrow.rows) <= len(wide.rows) {
		t.Errorf("narrow (width 20) rows = %d, wide (width 200) rows = %d, want the narrower wrap to take more rows", len(narrow.rows), len(wide.rows))
	}
}

// TestHelpViewRewrapsOnWindowResize guards the special case in Update's
// tea.WindowSizeMsg handler: help view's rows are wrapped once, at
// rebuild time (unlike every other view), so a resize while it's open
// has to explicitly rebuild or the old wrap width would stick around
// until the next :help.
func TestHelpViewRewrapsOnWindowResize(t *testing.T) {
	long := strings.Repeat("word ", 40)
	ws := agendaFixture(t, "* TODO Something\n")
	m := New(ws, WithReadme(long))
	m.width, m.height = 20, 24
	m.switchToView(helpView)
	narrowRowCount := len(m.rows)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 200, Height: 24})
	m = updated.(Model)

	if len(m.rows) >= narrowRowCount {
		t.Errorf("rows after widening = %d, want fewer than the narrow-width count %d", len(m.rows), narrowRowCount)
	}
}

func TestHelpViewShowsPlaceholderWhenNoReadmeConfigured(t *testing.T) {
	ws := agendaFixture(t, "* TODO Something\n")
	m := New(ws)

	m.switchToView(helpView)

	if len(m.rows) != 1 || m.rows[0].text != "No help available." {
		t.Errorf("rows = %#v, want a single placeholder row", m.rows)
	}
}

func TestHelpStatusLineShowsPlace(t *testing.T) {
	ws := agendaFixture(t, "* TODO Something\n")
	m := New(ws, WithReadme("docs"))

	m.switchToView(helpView)

	lines := m.normalStatusLines()
	if len(lines) == 0 || !containsSubstring(lines, "help") {
		t.Errorf("status line = %#v, want it to mention \"help\"", lines)
	}
}

// TestHelpViewRendersBlankLinesWithoutPanicking guards a real bug: a
// blank line in the README became row{text: ""}, and renderRowWithBg
// used to decide "is this a plain text row?" by checking text != "" —
// so a blank line fell through to the headline-rendering branch with a
// nil headline and panicked. Building rows (as the other tests above
// do) doesn't exercise that path; only actually rendering does. This
// also exercises the cursor's own row, so it doubles as a smoke test
// for the ANSI-stripping fix below with real glamour output.
func TestHelpViewRendersBlankLinesWithoutPanicking(t *testing.T) {
	ws := agendaFixture(t, "* TODO Something\n")
	m := New(ws, WithReadme("# orgtd\n\nSome docs.\n\nMore docs.\n"))
	m.width, m.height = 80, 24

	m.switchToView(helpView)
	_ = m.View()
}

// TestTextLineWithEmbeddedANSIGetsFullBackgroundHighlight guards the
// fix alongside :help: a plain-text row's content can now already carry
// its own ANSI styling (glamour-rendered markdown), which — like a
// headline row's own styled segments — would cut an outer background
// short at its first reset code unless stripped first. Constructs the
// row directly (rather than going through glamour) so this pins the
// exact behavior independently of whatever glamour's own output happens
// to look like.
func TestTextLineWithEmbeddedANSIGetsFullBackgroundHighlight(t *testing.T) {
	ws := agendaFixture(t, "* TODO Something\n")
	m := New(ws)
	r := row{isTextLine: true, text: "\x1b[31mred\x1b[0mplain"}

	got := m.renderRowWithBg(r, cursorBg)
	want := highlightMatches("redplain", "", lipgloss.NewStyle().Background(cursorBg))
	if got != want {
		t.Errorf("renderRowWithBg(highlighted) = %q, want %q (embedded ANSI stripped before the background is applied)", got, want)
	}

	// Unhighlighted rendering must NOT strip it — that's the whole point
	// of glamour's styling being there in the first place.
	if plain := m.renderRow(r); plain != r.text {
		t.Errorf("renderRow(unhighlighted) = %q, want the original styled text preserved (%q)", plain, r.text)
	}
}

func TestHelpDoesNotRequireExternalFile(t *testing.T) {
	// WithReadme is passed a plain string, not a file path — nothing in
	// the UI package ever touches disk for :help, so it works the same
	// whether or not README.md happens to exist next to the binary.
	ws := agendaFixture(t, "* TODO Something\n")
	m := New(ws, WithReadme("embedded content, not read from disk"))

	m.switchToView(helpView)

	if !containsSubstring(helpLines(m), "embedded content, not read from disk") {
		t.Errorf("rows = %#v, want the string passed to WithReadme to appear", helpLines(m))
	}
}
