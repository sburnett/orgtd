package ui

import (
	"strings"
	"testing"
)

// configLines returns the text of every row in m.rows (assumed to be
// configView's output), for substring assertions below.
func configLines(m Model) []string {
	lines := make([]string, len(m.rows))
	for i, r := range m.rows {
		lines[i] = r.text
	}
	return lines
}

// containsSubstring reports whether any of lines contains substr.
func containsSubstring(lines []string, substr string) bool {
	for _, l := range lines {
		if strings.Contains(l, substr) {
			return true
		}
	}
	return false
}

func TestConfigCommandSwitchesToConfigView(t *testing.T) {
	ws := agendaFixture(t, "* TODO Something\n")
	m := New(ws)

	m = sendKey(m, ":")
	m = typeKeys(m, "config")
	m, _ = sendKeyCmd(m, "enter")

	if m.view != configView {
		t.Fatalf("view after :config = %v, want configView", m.view)
	}
	if len(m.rows) == 0 {
		t.Fatal("config view should never be empty")
	}
}

func TestConfigViewReportsEffectiveSettings(t *testing.T) {
	ws := agendaFixture(t, "* TODO Something\n")
	m := New(ws,
		WithEditor("emacsclient -t"),
		WithURLFormatter("url2org"),
		WithURLFormatterPrefixes([]string{"bit.ly/", "go/"}),
		WithFormatLinksURLFormatter("batch-formatter"),
		WithAgendaDays(30),
		WithInboxFile("capture.org"),
		WithHideDoneAfterHours(48),
		WithDebug(true),
	)
	m.switchToView(configView)
	lines := configLines(m)

	for _, want := range []string{
		"Org directory: " + ws.Dir,
		"Editor: emacsclient -t",
		"URL formatter: url2org",
		"URL formatter prefixes: bit.ly/, go/",
		"Format-links URL formatter: batch-formatter",
		"Agenda window: 30 days",
		"Inbox file: capture.org",
		"Hide done after: 48 hours (currently on",
		"Debug logging: on",
	} {
		if !containsSubstring(lines, want) {
			t.Errorf("config view lines = %#v, want a line containing %q", lines, want)
		}
	}
}

func TestConfigViewReflectsHideDoneToggle(t *testing.T) {
	ws := agendaFixture(t, "* TODO Something\n")
	m := New(ws, WithHideDoneAfterHours(24))
	m.switchToView(configView)

	if !containsSubstring(configLines(m), "currently on") {
		t.Error("config view should report hide-done as on right after WithHideDoneAfterHours")
	}

	m.toggleHideDone()
	m.switchToView(configView)

	if !containsSubstring(configLines(m), "currently off") {
		t.Error("config view should reflect hide-done being toggled off")
	}
}

func TestConfigViewNotesFormatLinksFallsBackToURLFormatter(t *testing.T) {
	ws := agendaFixture(t, "* TODO Something\n")
	m := New(ws, WithURLFormatter("url2org")) // no WithFormatLinksURLFormatter
	m.switchToView(configView)

	if !containsSubstring(configLines(m), "Format-links URL formatter: url2org (same as URL formatter)") {
		t.Errorf("config view lines = %#v, want it to note the format-links formatter falls back to url_formatter", configLines(m))
	}
}

func TestConfigViewShowsDisabledURLFormatterAndDefaults(t *testing.T) {
	ws := agendaFixture(t, "* TODO Something\n")
	m := New(ws)
	m.switchToView(configView)
	lines := configLines(m)

	for _, want := range []string{
		"URL formatter: (disabled)",
		"URL formatter prefixes: (none)",
		"Format-links URL formatter: (disabled)",
		"Agenda window: 14 days",
		"Inbox file: inbox.org",
		"currently off",
		"Debug logging: off",
	} {
		if !containsSubstring(lines, want) {
			t.Errorf("config view lines = %#v, want a line containing %q", lines, want)
		}
	}
}
