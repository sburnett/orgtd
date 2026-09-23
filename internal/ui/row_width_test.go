package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/sburnett/orgtd/internal/org"
	"github.com/sburnett/orgtd/internal/workspace"
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
	m := Model{width: 40, ws: &workspace.Workspace{}}
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

// TestOutlineRowCapsLongTagListKeepingTitleVisible is the mirror image
// of TestOutlineRowTruncatesLongTitleKeepingTagsAndTimestampVisible: a
// headline with a short title but a very long tag list (e.g. a meeting
// with a couple dozen "@attendee" tags — see calendarDisplayTags) used
// to render the tag list in full via fitRowLine's "suffix always wins"
// rule, which could crowd even a short title down to nothing. The tag
// list itself is now capped (see renderTagsSuffix/maxTagsSuffixWidth),
// so the title stays visible on any reasonably sized terminal.
func TestOutlineRowCapsLongTagListKeepingTitleVisible(t *testing.T) {
	var tags []string
	for i := 0; i < 20; i++ {
		tags = append(tags, fmt.Sprintf("attendee-name-number-%d", i))
	}
	h := &org.Headline{
		Level:   1,
		Keyword: "TODO",
		Title:   "Standup",
		Tags:    tags,
	}
	m := Model{width: 100, ws: &workspace.Workspace{}}
	line := m.renderRow(row{headline: h})
	plain := stripANSI(line)

	if !strings.Contains(plain, "Standup") {
		t.Errorf("rendered row = %q, want the short title still visible despite the long tag list", plain)
	}
	if !strings.Contains(plain, "…") {
		t.Errorf("rendered row = %q, want an ellipsis marking the truncated tag list", plain)
	}
	if strings.Contains(plain, "attendee-name-number-19") {
		t.Errorf("rendered row = %q, want the tag list capped well short of all 20 tags", plain)
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

// TestAgendaItemRowTruncatesLongParentTitleInPlaceTag covers the parent
// headline's own title (not the agenda item's) being long: unlike the
// item's title, the "[file › parent]" tag is part of fitRowLine's
// suffix, which is otherwise always shown in full — so a long parent
// title needs its own budget (see renderAgendaItemRowWithBg) or it
// would push the item's own title, and even the date/label next to the
// tag, off the edge.
func TestAgendaItemRowTruncatesLongParentTitleInPlaceTag(t *testing.T) {
	now := truncateToDate(time.Now())
	orgText := fmt.Sprintf("* Project %s\n** NEXT Do the thing\n   DEADLINE: <%s>\n", strings.Repeat("a very long parent title ", 10), ts(now))
	ws := agendaFixture(t, orgText)
	m := New(ws)
	m.width = 90
	m.switchToView(agendaView)

	line := m.renderRow(m.rows[1])
	plain := stripANSI(line)

	if !strings.Contains(plain, "Do the thing") {
		t.Errorf("rendered agenda row = %q, want the item's own title still visible despite the long parent title", plain)
	}
	if !strings.Contains(plain, "Deadline:") {
		t.Errorf("rendered agenda row = %q, want the Deadline label still visible", plain)
	}
	if !strings.Contains(plain, "…") {
		t.Errorf("rendered agenda row = %q, want an ellipsis marking the truncated parent title", plain)
	}
	if w := lipgloss.Width(line); w > m.width {
		t.Errorf("rendered agenda row width = %d, want <= %d", w, m.width)
	}
}

// TestAgendaItemRowShowsFullParentTitleWhenRowFits ensures the parent
// tag isn't clipped to some fixed length regardless of context: with a
// short item title and a parent title long enough that a fixed cap
// would have ellipsized it, but a terminal wide enough for the whole
// row to fit anyway, the parent title should show in full — space is
// only ever taken from it when the row actually needs it (see
// TestAgendaItemRowTruncatesLongParentTitleInPlaceTag for the case
// where it does).
func TestAgendaItemRowShowsFullParentTitleWhenRowFits(t *testing.T) {
	now := truncateToDate(time.Now())
	orgText := fmt.Sprintf("* A moderately long project name for planning\n** NEXT Do the thing\n   DEADLINE: <%s>\n", ts(now))
	ws := agendaFixture(t, orgText)
	m := New(ws)
	m.width = 200
	m.switchToView(agendaView)

	line := m.renderRow(m.rows[1])
	plain := stripANSI(line)

	if !strings.Contains(plain, "A moderately long project name for planning") {
		t.Errorf("rendered agenda row = %q, want the full parent title visible since the row already fits", plain)
	}
	if strings.Contains(plain, "…") {
		t.Errorf("rendered agenda row = %q, want no ellipsis when nothing needed truncating", plain)
	}
}
