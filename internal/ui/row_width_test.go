package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/sburnett/orgtd/internal/org"
)

func TestFitRowLineNoOpWhenWidthUnknown(t *testing.T) {
	got := fitRowLine("prefix", "suffix", 0, lipgloss.NoColor{})
	if got != "prefixsuffix" {
		t.Errorf("fitRowLine with width<=0 = %q, want the unchanged concatenation", got)
	}
}

func TestFitRowLineNoOpWhenItAlreadyFits(t *testing.T) {
	got := fitRowLine("abc", "def", 10, lipgloss.NoColor{})
	if got != "abcdef" {
		t.Errorf("fitRowLine = %q, want unchanged when it already fits", got)
	}
}

func TestFitRowLineTruncatesPrefixKeepingSuffixIntact(t *testing.T) {
	prefix := strings.Repeat("x", 20)
	suffix := "SUFFIX"
	got := fitRowLine(prefix, suffix, 10, lipgloss.NoColor{})
	if !strings.HasSuffix(got, suffix) {
		t.Errorf("fitRowLine = %q, want it to end with the untouched suffix %q", got, suffix)
	}
	if !strings.Contains(got, "…") {
		t.Errorf("fitRowLine = %q, want an ellipsis marking the cut", got)
	}
	if w := lipgloss.Width(got); w > 10 {
		t.Errorf("fitRowLine width = %d, want <= 10 (the given width)", w)
	}
}

// TestFitRowLineDegenerateSuffixAloneExceedsWidth covers the edge case
// where suffix by itself is already wider than width: prefix must
// vanish entirely (not even an ellipsis — there's no room for one)
// rather than also being cut, since suffix is the part that must never
// disappear.
func TestFitRowLineDegenerateSuffixAloneExceedsWidth(t *testing.T) {
	suffix := strings.Repeat("y", 20)
	got := fitRowLine("prefix", suffix, 5, lipgloss.NoColor{})
	if got != suffix {
		t.Errorf("fitRowLine = %q, want just the suffix (prefix dropped, no ellipsis)", got)
	}
}

// TestFitRowLinePreservesANSIStyling is the reason fitRowLine uses
// ansi.Truncate rather than plain rune slicing: prefix here carries
// SGR escape codes (as every real row does — see bgSpan/lipgloss.Render
// throughout model.go), and naively slicing runes would either count
// invisible escape bytes toward the width budget or risk cutting an
// escape sequence in half, corrupting every subsequent row's colors.
func TestFitRowLinePreservesANSIStyling(t *testing.T) {
	styled := lipgloss.NewStyle().Bold(true).Render(strings.Repeat("x", 20))
	got := fitRowLine(styled, "END", 10, lipgloss.NoColor{})
	if !strings.HasSuffix(stripANSI(got), "END") {
		t.Errorf("fitRowLine = %q, want it to end with the suffix", got)
	}
	if w := lipgloss.Width(got); w > 10 {
		t.Errorf("fitRowLine width = %d, want <= 10 (ANSI codes shouldn't count against the budget)", w)
	}
}

// TestOutlineRowTruncatesLongTitleKeepingTagsAndTimestampVisible is the
// reported bug: a headline whose title alone is wider than the terminal
// used to push its tags and timestamp off the right edge entirely, with
// no indication anything was cut.
func TestOutlineRowTruncatesLongTitleKeepingTagsAndTimestampVisible(t *testing.T) {
	h := &org.Headline{
		Level:   1,
		Keyword: "TODO",
		Title:   strings.Repeat("a very long title indeed ", 10),
		Tags:    []string{"work"},
	}
	m := Model{width: 40}
	line := m.renderRow(row{headline: h})
	plain := stripANSI(line)

	if !strings.Contains(plain, ":work:") {
		t.Errorf("rendered row = %q, want the tag still visible despite the long title", plain)
	}
	if !strings.Contains(plain, "…") {
		t.Errorf("rendered row = %q, want an ellipsis marking the truncated title", plain)
	}
	if w := lipgloss.Width(line); w > m.width {
		t.Errorf("rendered row width = %d, want <= %d", w, m.width)
	}
}

// TestAgendaItemRowTruncatesLongTitleKeepingPlaceAndDateVisible mirrors
// the outline case for the agenda view, where the trailing [file ›
// parent]  Label: date tag is exactly the "other information" the user
// reported losing.
func TestAgendaItemRowTruncatesLongTitleKeepingPlaceAndDateVisible(t *testing.T) {
	now := truncateToDate(time.Now())
	orgText := fmt.Sprintf("* NEXT %s\n  DEADLINE: <%s>\n", strings.Repeat("a very long title indeed ", 10), ts(now))
	ws := agendaFixture(t, orgText)
	m := New(ws)
	m.width = 60
	m.switchToView(agendaView)

	line := m.renderRow(m.rows[1])
	plain := stripANSI(line)

	if !strings.Contains(plain, "[agenda.org]") {
		t.Errorf("rendered agenda row = %q, want the [file] tag still visible despite the long title", plain)
	}
	if !strings.Contains(plain, "Deadline:") {
		t.Errorf("rendered agenda row = %q, want the Deadline label still visible", plain)
	}
	if !strings.Contains(plain, "…") {
		t.Errorf("rendered agenda row = %q, want an ellipsis marking the truncated title", plain)
	}
	if w := lipgloss.Width(line); w > m.width {
		t.Errorf("rendered agenda row width = %d, want <= %d", w, m.width)
	}
}
