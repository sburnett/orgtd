package ui

import (
	"fmt"
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

func TestLinksInTitleExtractsURLs(t *testing.T) {
	got := linksInTitle("Compare [[https://a.example.com][A]] and [[https://b.example.com]]")
	want := []string{"https://a.example.com", "https://b.example.com"}
	if len(got) != len(want) {
		t.Fatalf("linksInTitle = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("linksInTitle[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestLinksInTitleNilWhenNoLinks(t *testing.T) {
	if got := linksInTitle("Just a plain title"); got != nil {
		t.Errorf("linksInTitle = %v, want nil", got)
	}
}

func TestStatusBarShowsURLWhenCursorOnEntryWithLink(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.width, m.height = 100, len(m.rows)+5
	idx := findRow(t, m, "Call the vet about Fido's checkup")
	m.cursor = idx
	h := m.currentHeadline()
	h.Title = "Call the vet, see [[https://example.com/vet][Vet Site]] for hours"

	out := stripANSI(m.View())
	lines := strings.Split(out, "\n")
	last := lines[len(lines)-1]

	if !strings.Contains(last, "https://example.com/vet") {
		t.Errorf("status bar = %q, want it to show the raw, clickable URL", last)
	}
	if strings.Contains(last, "[[") {
		t.Errorf("status bar = %q, want the raw URL, not org-link syntax", last)
	}
}

func TestStatusBarOmitsURLWhenEntryHasNoLink(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.width, m.height = 100, len(m.rows)+5
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")

	out := stripANSI(m.View())
	lines := strings.Split(out, "\n")
	last := lines[len(lines)-1]

	if strings.Contains(last, "http") {
		t.Errorf("status bar = %q, should not mention a URL for a plain entry", last)
	}
}

func TestStatusBarShowsMultipleURLsWhenEntryHasMultipleLinks(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.width, m.height = 100, len(m.rows)+5
	idx := findRow(t, m, "Call the vet about Fido's checkup")
	m.cursor = idx
	h := m.currentHeadline()
	h.Title = "See [[https://a.example.com][A]] and [[https://b.example.com][B]]"

	out := stripANSI(m.View())
	lines := strings.Split(out, "\n")
	last := lines[len(lines)-1]

	for _, url := range []string{"https://a.example.com", "https://b.example.com"} {
		if !strings.Contains(last, url) {
			t.Errorf("status bar = %q, missing %q", last, url)
		}
	}
}

func TestStatusBarSplitsLinkOntoItsOwnLineWhenTooWide(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	idx := findRow(t, m, "Call the vet about Fido's checkup")
	m.cursor = idx
	h := m.currentHeadline()
	h.Title = "Call the vet, see [[https://example.com/a-rather-long-path/for-testing][Vet Site]] for hours"

	// Wide enough for the plain status line or the link alone, but not
	// both combined on one line.
	m.width, m.height = 40, len(m.rows)+5

	out := stripANSI(m.View())
	lines := strings.Split(out, "\n")
	if len(lines) != m.height {
		t.Fatalf("got %d lines, want %d (height)\n---\n%s", len(lines), m.height, out)
	}

	statusLine := lines[len(lines)-2]
	linkLine := lines[len(lines)-1]

	if strings.Contains(statusLine, "http") {
		t.Errorf("status line = %q, should not also carry the URL", statusLine)
	}
	if !strings.Contains(statusLine, "item") {
		t.Errorf("status line = %q, missing the usual item count", statusLine)
	}
	if !strings.Contains(linkLine, "https://example.com/a-rather-long-path/for-testing") {
		t.Errorf("link line = %q, want the full URL on its own line", linkLine)
	}
}

func TestStatusBarKeepsOneLineWhenLinkFits(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	idx := findRow(t, m, "Call the vet about Fido's checkup")
	m.cursor = idx
	h := m.currentHeadline()
	h.Title = "Call the vet, see [[https://example.com/vet][Vet Site]] for hours"

	m.width, m.height = 200, len(m.rows)+5

	out := stripANSI(m.View())
	lines := strings.Split(out, "\n")
	if len(lines) != m.height {
		t.Fatalf("got %d lines, want %d (height)\n---\n%s", len(lines), m.height, out)
	}

	last := lines[len(lines)-1]
	if !strings.Contains(last, "item") || !strings.Contains(last, "https://example.com/vet") {
		t.Errorf("last line = %q, want both the item count and the URL on one line", last)
	}
}

func TestStatusBarSplitIgnoredWhenWidthUnknown(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	idx := findRow(t, m, "Call the vet about Fido's checkup")
	m.cursor = idx
	h := m.currentHeadline()
	h.Title = "Call the vet, see [[https://example.com/a-rather-long-path/for-testing][Vet Site]] for hours"

	// Width unset (zero value): never split, since we can't tell whether
	// it would actually overflow.
	m.height = len(m.rows) + 5

	out := stripANSI(m.View())
	lines := strings.Split(out, "\n")
	if len(lines) != m.height {
		t.Fatalf("got %d lines, want %d (height)\n---\n%s", len(lines), m.height, out)
	}
	last := lines[len(lines)-1]
	if !strings.Contains(last, "https://example.com/a-rather-long-path/for-testing") {
		t.Errorf("last line = %q, want the URL still shown (unsplit) when width is unknown", last)
	}
}

func TestStatusBarPutsMultipleLongURLsOnSeparateLinesWhenTheyStillDontFitTogether(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	idx := findRow(t, m, "Call the vet about Fido's checkup")
	m.cursor = idx
	h := m.currentHeadline()
	urlA := "https://example.com/a-rather-long-path/for-testing-one"
	urlB := "https://example.com/a-rather-long-path/for-testing-two"
	h.Title = fmt.Sprintf("See [[%s][A]] and [[%s][B]]", urlA, urlB)

	// Wide enough for either URL alone, but not for both together on one
	// line (nor combined with the status text).
	m.width, m.height = 60, len(m.rows)+5

	out := stripANSI(m.View())
	lines := strings.Split(out, "\n")
	if len(lines) != m.height {
		t.Fatalf("got %d lines, want %d (height)\n---\n%s", len(lines), m.height, out)
	}

	statusLine := lines[len(lines)-3]
	lineA := lines[len(lines)-2]
	lineB := lines[len(lines)-1]

	if strings.Contains(statusLine, "http") {
		t.Errorf("status line = %q, should not carry either URL", statusLine)
	}
	if !strings.Contains(lineA, urlA) || strings.Contains(lineA, urlB) {
		t.Errorf("line = %q, want just %q on it", lineA, urlA)
	}
	if !strings.Contains(lineB, urlB) || strings.Contains(lineB, urlA) {
		t.Errorf("line = %q, want just %q on it", lineB, urlB)
	}
}
