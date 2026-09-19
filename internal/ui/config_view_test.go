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
		WithCalendarFile("my-calendar.org"),
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
		"Calendar file: my-calendar.org",
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
		"Calendar file: calendar.org",
		"currently off",
		"Debug logging: off",
		"Calendar sync: (not configured — see README's Calendar sync section)",
		`Gutter icons: dirty "+" (#0087d7), mark (#ff87ff), clarify "●" (#ff87ff), lock "◆" (#ffaf00), meeting "▣" (#00afff)`,
		"Colors: file (#00afff), todo (#d7005f), next (#ffaf00), waiting (#875fff), someday (#767676), done (#00d75f), cancelled (#585858), tag (#00d7d7), done-title (#767676), status (#767676), timestamp (#ff87ff), error (#d7005f), body (#767676), caret (#000000 on #ffffff), highlight (#585858), panel (#303030), status-bar (#000000 on #9e9e9e), cursor-row (#204060), visual-selection (#102030), search-highlight (#3a4a3a)",
	} {
		if !containsSubstring(lines, want) {
			t.Errorf("config view lines = %#v, want a line containing %q", lines, want)
		}
	}
}

func TestConfigViewReportsAttendeeTagDomains(t *testing.T) {
	ws := agendaFixture(t, "* TODO Something\n")
	m := New(ws,
		WithGcalOAuthClient("id", "secret"),
		WithGcalAttendeeTagDomains([]string{"example.com", "example.org"}),
	)
	m.switchToView(configView)

	if !containsSubstring(configLines(m), "Attendee tag domains: example.com, example.org") {
		t.Errorf("config view lines = %#v, want the configured attendee tag domains", configLines(m))
	}
}

func TestConfigViewReportsNoAttendeeTagDomainsRestriction(t *testing.T) {
	ws := agendaFixture(t, "* TODO Something\n")
	m := New(ws, WithGcalOAuthClient("id", "secret"))
	m.switchToView(configView)

	if !containsSubstring(configLines(m), "Attendee tag domains: (none — every confirmed attendee is tagged)") {
		t.Errorf("config view lines = %#v, want the no-restriction message", configLines(m))
	}
}

func TestConfigViewReportsCustomIcons(t *testing.T) {
	ws := agendaFixture(t, "* TODO Something\n")
	m := New(ws,
		WithDirtyIcon("*", "1"),
		WithMarkColor("2"),
		WithClarifyIcon("@", "3"),
		WithLockIcon("#", "4"),
		WithMeetingIcon("%", "5"),
	)
	m.switchToView(configView)

	if !containsSubstring(configLines(m), `Gutter icons: dirty "*" (1), mark (2), clarify "@" (3), lock "#" (4), meeting "%" (5)`) {
		t.Errorf("config view lines = %#v, want it to reflect the custom gutter icons", configLines(m))
	}
}

func TestConfigViewReportsCustomColors(t *testing.T) {
	ws := agendaFixture(t, "* TODO Something\n")
	m := New(ws, WithColors(ColorOverrides{TODO: "9", PanelBg: "233"}))
	m.switchToView(configView)

	if !containsSubstring(configLines(m), "Colors: file (#00afff), todo (9)") {
		t.Errorf("config view lines = %#v, want it to reflect the custom todo color", configLines(m))
	}
	if !containsSubstring(configLines(m), "panel (233)") {
		t.Errorf("config view lines = %#v, want it to reflect the custom panel color", configLines(m))
	}
}
