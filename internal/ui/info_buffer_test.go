package ui

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sburnett/orgtd/internal/org"
)

// TestInfoBufferEmptyWhenNothingApplies guards infoBufferHeight/
// infoBufferLines' "collapses to nothing" convention (mirroring
// pinnedHeaderHeight): a plain entry, in normal mode, with no ambiguous
// tag/command completion pending, contributes zero lines and zero
// height — so it never costs a permanent row on screen.
func TestInfoBufferEmptyWhenNothingApplies(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")

	if got := m.infoBufferLines(); got != nil {
		t.Errorf("infoBufferLines = %#v, want nil", got)
	}
	if got := m.infoBufferHeight(); got != 0 {
		t.Errorf("infoBufferHeight = %d, want 0", got)
	}
}

// TestInfoBufferLinksSectionCapsWithOverflowSummary guards the
// "Links:" section's maxInfoBufferLines cap: past that many links, the
// rest collapse into one "...and N more" summary line rather than
// pushing the outline listing off-screen.
func TestInfoBufferLinksSectionCapsWithOverflowSummary(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	idx := findRow(t, m, "Call the vet about Fido's checkup")
	m.cursor = idx
	h := m.currentHeadline()

	const extra = 2
	var b strings.Builder
	for i := 0; i < maxInfoBufferLines+extra; i++ {
		fmt.Fprintf(&b, "[[https://example.com/%d][L%d]] ", i, i)
	}
	h.Title = b.String()

	var bufLines []string
	for _, l := range m.infoBufferLines() {
		bufLines = append(bufLines, stripANSI(l))
	}

	var urlLines int
	for _, l := range bufLines {
		if strings.Contains(l, "https://example.com/") {
			urlLines++
		}
	}
	if urlLines != maxInfoBufferLines {
		t.Errorf("shown URL lines = %d, want %d (maxInfoBufferLines)", urlLines, maxInfoBufferLines)
	}
	if !containsSubstring(bufLines, fmt.Sprintf("...and %d more", extra)) {
		t.Errorf("info buffer missing overflow summary: %#v", bufLines)
	}
}

// TestInfoBufferMeetingSectionShowsTimeForSelfEvent guards the
// "Meeting:" section's name+time+link formatting (formatCalendarEventEntry)
// for the simplest case: a synced calendar event's own row, which always
// carries a live GCAL_START.
func TestInfoBufferMeetingSectionShowsTimeForSelfEvent(t *testing.T) {
	ws := loadFixture(t)
	start := time.Date(2026, 9, 18, 14, 0, 0, 0, time.Local)
	calHeadline := calendarEventHeadline("abc123", start, start.Add(time.Hour))
	calHeadline.Title = "Q3 planning sync"
	ws.Files = append(ws.Files, &org.File{
		Path:      filepath.Join(ws.Dir, "calendar.org"),
		Headlines: []*org.Headline{calHeadline},
	})
	m := New(ws)
	m.switchToView(calendarView)
	m.cursor = findRow(t, m, "Q3 planning sync")

	var bufLines []string
	for _, l := range m.infoBufferLines() {
		bufLines = append(bufLines, stripANSI(l))
	}
	want := start.Local().Format("2006-01-02 Mon 15:04")
	if !containsSubstring(bufLines, want) {
		t.Errorf("info buffer missing formatted meeting time %q: %#v", want, bufLines)
	}
}

// TestSelectModeShowsCandidatesInInfoBufferNotOnPromptLine guards the
// "Status:" section of the info buffer (see statusSelectorLines): the
// "R"/"r" status picker's candidate list lives there, one per line —
// the command line itself only ever shows the "Set status:" prefix and
// the typed filter.
func TestSelectModeShowsCandidatesInInfoBufferNotOnPromptLine(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.width, m.height = 100, 30
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")

	m = sendKey(m, "R")

	lines := strings.Split(m.View(), "\n")
	last := stripANSI(lines[len(lines)-1])
	if !strings.Contains(last, "Set status") {
		t.Errorf("command line = %q, want the \"Set status\" prefix", last)
	}
	for _, c := range statusCandidates {
		if strings.Contains(last, c.label) {
			t.Errorf("command line = %q, candidate %q should not appear here anymore", last, c.label)
		}
	}

	var bufLines []string
	for _, l := range m.infoBufferLines() {
		bufLines = append(bufLines, stripANSI(l))
	}
	if !containsSubstring(bufLines, "Status:") {
		t.Errorf("info buffer missing \"Status:\" section: %#v", bufLines)
	}
	for _, c := range statusCandidates {
		if !containsSubstring(bufLines, c.label) {
			t.Errorf("info buffer missing candidate %q: %#v", c.label, bufLines)
		}
	}
}

// TestSelectModeHighlightsCurrentCandidateInInfoBuffer guards that the
// preselected/highlighted candidate (see currentStatusIndex) is the one
// actually marked (reverse video) among the info buffer's "Status:"
// lines, not just tracked internally.
func TestSelectModeHighlightsCurrentCandidateInInfoBuffer(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.width, m.height = 100, 30
	m.cursor = findRow(t, m, "Get feedback on the keybinding scheme")
	if m.currentHeadline().Keyword != "WAITING" {
		t.Fatalf("fixture assumption broken: keyword = %q, want WAITING", m.currentHeadline().Keyword)
	}

	m = sendKey(m, "R")

	var highlightedLine string
	for _, l := range m.statusSelectorLines() {
		if strings.Contains(l, "\x1b[7m") {
			highlightedLine = l
		}
	}
	if highlightedLine == "" {
		t.Fatalf("no reverse-video candidate line found")
	}
	if !strings.Contains(stripANSI(highlightedLine), "WAITING") {
		t.Errorf("highlighted line = %q, want it to be WAITING", stripANSI(highlightedLine))
	}
}

// TestInfoBufferMeetingSectionOmitsTimeWhenUnresolvable guards
// formatCalendarEventEntry's graceful fallback: a "gM"-attached entry
// whose meeting has aged out of calendar.org still shows its name/link
// (from the *_LINKS snapshot) but no time, since there's nothing live
// left to resolve one from.
func TestInfoBufferMeetingSectionOmitsTimeWhenUnresolvable(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Learn Go generics")
	target := m.currentHeadline()
	target.SetProperty("GCAL_EVENT_LINKS", "[[https://calendar.google.com/event?eid=abc123][Meeting abc123]]")

	var bufLines []string
	for _, l := range m.infoBufferLines() {
		bufLines = append(bufLines, stripANSI(l))
	}
	if !containsSubstring(bufLines, "Meeting abc123") {
		t.Errorf("info buffer missing meeting name: %#v", bufLines)
	}
	for _, l := range bufLines {
		if strings.Contains(l, "Meeting abc123") {
			for _, day := range []string{"Mon ", "Tue ", "Wed ", "Thu ", "Fri ", "Sat ", "Sun "} {
				if strings.Contains(l, day) {
					t.Errorf("info buffer line %q unexpectedly carries a resolved time", l)
				}
			}
		}
	}
}
