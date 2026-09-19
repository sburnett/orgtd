package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// TestWithColorsOverridesKeywordColor covers WithColors actually
// changing what keywordStyle resolves to, not just the config file's own
// parsing/merging — mirrors TestMeetingColumnUsesCustomIcon's role for
// WithMeetingIcon.
func TestWithColorsOverridesKeywordColor(t *testing.T) {
	ws := agendaFixture(t, "* TODO Something\n")
	m := New(ws, WithColors(ColorOverrides{TODO: "#123456"}))

	style, ok := m.keywordStyle("TODO")
	if !ok {
		t.Fatalf("keywordStyle(TODO) ok = false, want true")
	}
	want := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#123456")).Render("TODO")
	if got := style.Render("TODO"); got != want {
		t.Errorf("keywordStyle(TODO).Render = %q, want %q", got, want)
	}
}

// TestWithColorsLeavesOtherKeywordsAtTheirDefault guards against a
// too-broad override: setting TODO's color must not disturb NEXT's own
// (still built-in) default.
func TestWithColorsLeavesOtherKeywordsAtTheirDefault(t *testing.T) {
	ws := agendaFixture(t, "* TODO Something\n")
	m := New(ws, WithColors(ColorOverrides{TODO: "#123456"}))

	style, ok := m.keywordStyle("NEXT")
	if !ok {
		t.Fatalf("keywordStyle(NEXT) ok = false, want true")
	}
	want := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(defaultNextColor)).Render("NEXT")
	if got := style.Render("NEXT"); got != want {
		t.Errorf("keywordStyle(NEXT).Render = %q, want the built-in default %q", got, want)
	}
}

// TestNoWithColorsUsesWildcharmDefaults guards the zero-value case: a
// Model built with no WithColors option at all (or, as most tests do, no
// options whatsoever) still renders with the built-in wildcharm-dark
// palette rather than some blank/zero color.
func TestNoWithColorsUsesWildcharmDefaults(t *testing.T) {
	ws := agendaFixture(t, "* TODO Something\n")
	m := New(ws)

	style, ok := m.keywordStyle("TODO")
	if !ok {
		t.Fatalf("keywordStyle(TODO) ok = false, want true")
	}
	want := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(defaultTODOColor)).Render("TODO")
	if got := style.Render("TODO"); got != want {
		t.Errorf("keywordStyle(TODO).Render = %q, want the built-in default %q", got, want)
	}
}

// TestWithColorsOverridesPanelAndStatusBarBackgrounds spot-checks two of
// the background-only colors (see overlayBg/statusBarBg) actually
// reaching the rendered view, not just the keyword-color styles above.
func TestWithColorsOverridesPanelAndStatusBarBackgrounds(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws, WithColors(ColorOverrides{PanelBg: "#123456", StatusBarBg: "#abcdef"}))
	m.width, m.height = 100, 30
	m = sendKey(m, "j")
	m = sendKey(m, "m")
	m = sendKey(m, "a")

	out := m.View()
	panelSGR := ansiEscapeRe.FindString(bgSpan(m.overlayBg(), "x"))
	statusBarSGR := ansiEscapeRe.FindString(bgSpan(m.statusBarBg(), "x"))
	if !strings.Contains(out, panelSGR) {
		t.Errorf("view missing overridden panel background %q:\n%s", panelSGR, out)
	}
	if !strings.Contains(out, statusBarSGR) {
		t.Errorf("view missing overridden status bar background %q:\n%s", statusBarSGR, out)
	}
}
