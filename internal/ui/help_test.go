package ui

import "testing"

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

func TestHelpViewShowsReadmeContentLineByLine(t *testing.T) {
	ws := agendaFixture(t, "* TODO Something\n")
	m := New(ws, WithReadme("# orgtd\n\nSome docs.\n"))

	m.switchToView(helpView)

	want := []string{"# orgtd", "", "Some docs."}
	if len(m.rows) != len(want) {
		t.Fatalf("rows = %#v, want %d rows", m.rows, len(want))
	}
	for i, line := range want {
		if m.rows[i].text != line {
			t.Errorf("row %d = %q, want %q", i, m.rows[i].text, line)
		}
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
// do) doesn't exercise that path; only actually rendering does.
func TestHelpViewRendersBlankLinesWithoutPanicking(t *testing.T) {
	ws := agendaFixture(t, "* TODO Something\n")
	m := New(ws, WithReadme("# orgtd\n\nSome docs.\n\nMore docs.\n"))
	m.width, m.height = 80, 24

	m.switchToView(helpView)
	_ = m.View()
}

func TestHelpDoesNotRequireExternalFile(t *testing.T) {
	// WithReadme is passed a plain string, not a file path — nothing in
	// the UI package ever touches disk for :help, so it works the same
	// whether or not README.md happens to exist next to the binary.
	ws := agendaFixture(t, "* TODO Something\n")
	m := New(ws, WithReadme("embedded content, not read from disk"))

	m.switchToView(helpView)

	if len(m.rows) != 1 || m.rows[0].text != "embedded content, not read from disk" {
		t.Errorf("rows = %#v, want the exact string passed to WithReadme", m.rows)
	}
}
