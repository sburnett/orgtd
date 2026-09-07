package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestRenderTitleForDisplayShowsDescriptionUnderlined(t *testing.T) {
	got := renderTitleForDisplay("Read the [[https://example.com/rfc][RFC]] before Monday", lipgloss.NewStyle())

	if strings.Contains(got, "[[") || strings.Contains(got, "]]") {
		t.Errorf("rendered title still contains raw link syntax: %q", got)
	}
	plain := stripANSI(got)
	if plain != "Read the RFC before Monday" {
		t.Errorf("plain text = %q, want %q", plain, "Read the RFC before Monday")
	}
	if !strings.Contains(got, "\x1b[") {
		t.Errorf("expected the link's display text to carry an ANSI (underline) style: %q", got)
	}
}

func TestRenderTitleForDisplayFallsBackToURLWithoutDescription(t *testing.T) {
	got := renderTitleForDisplay("See [[https://example.com/page]] for details", lipgloss.NewStyle())

	plain := stripANSI(got)
	if plain != "See https://example.com/page for details" {
		t.Errorf("plain text = %q, want the bare url shown as the display text", plain)
	}
}

func TestRenderTitleForDisplayLeavesPlainTitleUnaffected(t *testing.T) {
	got := renderTitleForDisplay("Just a plain title", lipgloss.NewStyle())
	if got != "Just a plain title" {
		t.Errorf("plain title unexpectedly changed: %q", got)
	}
}

func TestRenderTitleForDisplayHandlesMultipleLinks(t *testing.T) {
	got := renderTitleForDisplay("Compare [[https://a.example.com][A]] and [[https://b.example.com][B]]", lipgloss.NewStyle())
	plain := stripANSI(got)
	if plain != "Compare A and B" {
		t.Errorf("plain text = %q, want %q", plain, "Compare A and B")
	}
}

func TestRenderTitleForDisplayComposesWithBaseStyleWithoutBreakingIt(t *testing.T) {
	base := lipgloss.NewStyle().Strikethrough(true)
	got := renderTitleForDisplay("Read [[https://example.com][it]] later", base)

	plain := stripANSI(got)
	if plain != "Read it later" {
		t.Errorf("plain text = %q, want %q", plain, "Read it later")
	}
	// Both the base style (strikethrough, SGR 9) and the link's added
	// underline (SGR 4) must appear: the text around the link keeps the
	// base style (rendered via its own, independent base.Render() call,
	// not nested inside the link's), while the link itself gets both.
	if !strings.Contains(got, "\x1b[9m") {
		t.Errorf("base (strikethrough) style missing from surrounding text: %q", got)
	}
	if !strings.Contains(got, "4;") && !strings.Contains(got, ";4") {
		t.Errorf("link segment missing the added underline style: %q", got)
	}
}

func TestRowRenderingUsesDisplayTextForLinks(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	idx := findRow(t, m, "Call the vet about Fido's checkup")
	m.cursor = idx
	h := m.currentHeadline()
	h.Title = "Call the vet, see [[https://example.com/vet][Vet Site]] for hours"

	line := m.renderRow(m.rows[idx])
	if strings.Contains(line, "[[") {
		t.Errorf("rendered row still shows raw link syntax: %q", line)
	}
	if !strings.Contains(stripANSI(line), "Vet Site") {
		t.Errorf("rendered row missing the link's display text: %q", line)
	}
}
