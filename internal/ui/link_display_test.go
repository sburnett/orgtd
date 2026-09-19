package ui

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/sburnett/orgtd/internal/org"
)

func TestRenderTitleForDisplayShowsDescriptionUnderlined(t *testing.T) {
	got := (Model{}).renderTitleForDisplay("Read the [[https://example.com/rfc][RFC]] before Monday", lipgloss.NewStyle(), "")

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
	got := (Model{}).renderTitleForDisplay("See [[https://example.com/page]] for details", lipgloss.NewStyle(), "")

	plain := stripANSI(got)
	if plain != "See https://example.com/page for details" {
		t.Errorf("plain text = %q, want the bare url shown as the display text", plain)
	}
}

func TestRenderTitleForDisplayLeavesPlainTitleUnaffected(t *testing.T) {
	got := (Model{}).renderTitleForDisplay("Just a plain title", lipgloss.NewStyle(), "")
	if got != "Just a plain title" {
		t.Errorf("plain title unexpectedly changed: %q", got)
	}
}

func TestRenderTitleForDisplayHandlesMultipleLinks(t *testing.T) {
	got := (Model{}).renderTitleForDisplay("Compare [[https://a.example.com][A]] and [[https://b.example.com][B]]", lipgloss.NewStyle(), "")
	plain := stripANSI(got)
	if plain != "Compare A and B" {
		t.Errorf("plain text = %q, want %q", plain, "Compare A and B")
	}
}

func TestRenderTitleForDisplayComposesWithBaseStyleWithoutBreakingIt(t *testing.T) {
	base := lipgloss.NewStyle().Strikethrough(true)
	got := (Model{}).renderTitleForDisplay("Read [[https://example.com][it]] later", base, "")

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

// TestOrgLinkReMatchesDescriptionContainingBrackets guards a real bug: a
// description with a literal bracket in it (e.g. a formatter's output
// for a page titled "Bracket [disambiguation]") is perfectly valid
// org-mode link syntax — org-mode itself just looks for the nearest
// following "]]", it doesn't forbid brackets in the description. Our
// own orgLinkRe used to exclude them outright, so a link like this
// never matched at all, leaving its url looking "bare" on every
// subsequent scan and sending it through the formatter again.
func TestOrgLinkReMatchesDescriptionContainingBrackets(t *testing.T) {
	title := "See [[https://example.com][Bracket [disambiguation] page]] for context"
	matches := orgLinkRe.FindAllStringSubmatch(title, -1)
	if len(matches) != 1 {
		t.Fatalf("matches = %#v, want exactly 1", matches)
	}
	if url := matches[0][1]; url != "https://example.com" {
		t.Errorf("url = %q, want https://example.com", url)
	}
	if desc := matches[0][2]; desc != "Bracket [disambiguation] page" {
		t.Errorf("description = %q, want %q", desc, "Bracket [disambiguation] page")
	}
}

// TestOrgLinkReStopsAtNearestClosingBracketsAcrossMultipleLinks guards
// against the opposite failure mode: the now-permissive, non-greedy
// description shouldn't swallow past its own link's "]]" into whatever
// follows, even when a second link comes right after.
func TestOrgLinkReStopsAtNearestClosingBracketsAcrossMultipleLinks(t *testing.T) {
	title := "[[https://a.example.com][A [bracketed] note]] and [[https://b.example.com][B]]"
	matches := orgLinkRe.FindAllStringSubmatch(title, -1)
	if len(matches) != 2 {
		t.Fatalf("matches = %#v, want exactly 2", matches)
	}
	if got := matches[0][2]; got != "A [bracketed] note" {
		t.Errorf("first description = %q, want %q", got, "A [bracketed] note")
	}
	if got := matches[1][1]; got != "https://b.example.com" {
		t.Errorf("second url = %q, want https://b.example.com", got)
	}
	if got := matches[1][2]; got != "B" {
		t.Errorf("second description = %q, want %q", got, "B")
	}
}

func TestRenderTitleForDisplayHandlesBracketedDescription(t *testing.T) {
	got := (Model{}).renderTitleForDisplay("See [[https://example.com][Bracket [disambiguation] page]] now", lipgloss.NewStyle(), "")
	plain := stripANSI(got)
	if plain != "See Bracket [disambiguation] page now" {
		t.Errorf("plain text = %q, want %q", plain, "See Bracket [disambiguation] page now")
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
	m.width, m.height = 100, len(m.rows)+10
	idx := findRow(t, m, "Call the vet about Fido's checkup")
	m.cursor = idx
	h := m.currentHeadline()
	h.Title = "Call the vet, see [[https://example.com/vet][Vet Site]] for hours"

	out := stripANSI(m.View())
	lines := strings.Split(out, "\n")

	if !strings.Contains(out, "https://example.com/vet") {
		t.Errorf("view = %q, want it to show the raw, clickable URL", out)
	}
	if strings.Contains(out, "[[") {
		t.Errorf("view = %q, want the raw URL, not org-link syntax", out)
	}
	if !containsSubstring(lines, "Links:") {
		t.Errorf("view missing the info buffer's \"Links:\" section:\n%s", out)
	}
}

func TestStatusBarOmitsURLWhenEntryHasNoLink(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.width, m.height = 100, len(m.rows)+5
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")

	// Checked against just the info buffer, not the whole view: the
	// fixture's own outline content has unrelated rows mentioning "http"
	// elsewhere on screen.
	if len(m.infoBufferLines()) != 0 {
		t.Errorf("infoBufferLines = %#v, want none for a plain entry", m.infoBufferLines())
	}
}

func TestStatusBarShowsCalendarEventLinkWithMeetingNameAsTitle(t *testing.T) {
	ws := loadFixture(t)
	calHeadline := &org.Headline{Level: 1, Title: "Q3 planning sync"}
	calHeadline.SetProperty("GCAL_EVENT_ID", "abc123")
	calHeadline.SetProperty("GCAL_HTML_LINK", "https://calendar.google.com/event?eid=abc123")
	ws.Files = append(ws.Files, &org.File{
		Path:      filepath.Join(ws.Dir, "calendar.org"),
		Headlines: []*org.Headline{calHeadline},
	})
	m := New(ws)
	m.width, m.height = 200, len(m.rows)+10
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	m.currentHeadline().SetProperty("GCAL_EVENT_IDS", "abc123")

	out := stripANSI(m.View())
	lines := strings.Split(out, "\n")

	if !strings.Contains(out, "Q3 planning sync") {
		t.Errorf("view = %q, want the meeting name as the link's title", out)
	}
	if !strings.Contains(out, "https://calendar.google.com/event?eid=abc123") {
		t.Errorf("view = %q, want the calendar event's URL", out)
	}
	if !containsSubstring(lines, "Meeting:") {
		t.Errorf("view missing the info buffer's \"Meeting:\" section:\n%s", out)
	}
}

func TestStatusBarOmitsCalendarEventLinkWhenNoLinkEverRecorded(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.width, m.height = 200, len(m.rows)+5
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	// A GCAL_EVENT_IDS hand-attached to some entry (see DESIGN.md's
	// project<->meeting association) with no GCAL_EVENT_LINKS and no
	// matching calendar.org headline to fall back to: nothing to show.
	m.currentHeadline().SetProperty("GCAL_EVENT_IDS", "stale-id")

	// Checked against just the info buffer, not the whole view: the
	// fixture's own outline content has unrelated rows mentioning "http"
	// elsewhere on screen.
	if len(m.infoBufferLines()) != 0 {
		t.Errorf("infoBufferLines = %#v, want none for an unresolvable link", m.infoBufferLines())
	}
}

// TestStatusBarCalendarEventLinkSurvivesCalendarOrgAgingOut exercises
// resolveMeetingLinks' durable path directly, by setting GCAL_EVENT_LINKS
// by hand rather than through "gM" (which now writes it too, for a
// one-off event — see TestGMAttachOneOffEventUsesEventProperties in
// meeting_picker_test.go): a hand-set property (or one from an older
// orgtd version, before gC's now-removed auto-attach behavior was
// replaced by "gM" — see TestCaptureDoesNotAttachCalendarMeetingInfo in
// capture_test.go) should resolve the same durable way regardless of
// how it got there.
func TestStatusBarCalendarEventLinkSurvivesCalendarOrgAgingOut(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	ws.Files = append(ws.Files, &org.File{
		Path: filepath.Join(ws.Dir, "calendar.org"),
		Headlines: []*org.Headline{
			calendarEventHeadline("abc123", now.Add(-15*time.Minute), now.Add(45*time.Minute)),
		},
	})
	m := New(ws)
	m.cursor = findRow(t, m, "Learn Go generics")
	target := m.currentHeadline()
	target.SetProperty("GCAL_EVENT_LINKS", "[[https://calendar.google.com/event?eid=abc123][Meeting abc123]]")

	// Simulate :sync-calendar re-syncing calendar.org after the meeting has
	// aged out of its window: the cached headline for "abc123" is gone.
	for i, f := range m.ws.Files {
		if strings.HasSuffix(f.Path, "calendar.org") {
			m.ws.Files[i] = &org.File{Path: f.Path}
		}
	}
	m.rebuildRows()
	for i, r := range m.rows {
		if r.headline == target {
			m.cursor = i
			break
		}
	}
	m.width, m.height = 200, len(m.rows)+10

	out := stripANSI(m.View())

	if !strings.Contains(out, "Meeting abc123") {
		t.Errorf("view = %q, want the meeting name even though calendar.org no longer has it cached", out)
	}
	if !strings.Contains(out, "https://calendar.google.com/event?eid=abc123") {
		t.Errorf("view = %q, want the meeting's URL even though calendar.org no longer has it cached", out)
	}
}

func TestStatusBarRecurringMeetingLinkSurvivesSeriesDroppingOffCalendar(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	ws.Files = append(ws.Files, &org.File{
		Path: filepath.Join(ws.Dir, "calendar.org"),
		Headlines: []*org.Headline{
			recurringCalendarEventHeadline("standup-1", "series-standup", "Weekly Standup", now.Add(time.Hour), now.Add(90*time.Minute)),
		},
	})
	m := New(ws)
	m.cursor = findRow(t, m, "Learn Go generics")
	target := m.currentHeadline()

	// Attach via gM, same as a real user would: this is what bakes
	// GCAL_RECURRING_EVENT_LINKS onto the entry.
	m = sendKey(m, "g")
	m = sendKey(m, "M")
	m, _ = sendKeyCmd(m, "enter")
	if target.Properties["GCAL_RECURRING_EVENT_LINKS"] == "" {
		t.Fatalf("gM didn't record GCAL_RECURRING_EVENT_LINKS; can't test durability")
	}

	// Simulate the series dropping off the calendar entirely (deleted,
	// or simply aged out of every future sync): calendar.org no longer
	// has any headline for it.
	for i, f := range m.ws.Files {
		if strings.HasSuffix(f.Path, "calendar.org") {
			m.ws.Files[i] = &org.File{Path: f.Path}
		}
	}
	m.rebuildRows()
	for i, r := range m.rows {
		if r.headline == target {
			m.cursor = i
			break
		}
	}
	m.width, m.height = 200, len(m.rows)+10

	out := stripANSI(m.View())

	if !strings.Contains(out, "Weekly Standup") {
		t.Errorf("view = %q, want the meeting name even though it's gone from calendar.org", out)
	}
	if !strings.Contains(out, "https://calendar.google.com/event?eid=standup-1") {
		t.Errorf("view = %q, want the meeting's URL even though it's gone from calendar.org", out)
	}
}

// TestStatusBarShowsMultipleURLsWhenEntryHasMultipleLinks also guards
// the info buffer's "always one per line" rule (see infoBufferLines):
// unlike the old squeeze-to-fit status line, links never share a line
// with each other or with the status line's own "item N/M" text,
// regardless of how many there are or how wide the terminal is.
func TestStatusBarShowsMultipleURLsWhenEntryHasMultipleLinks(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.width, m.height = 100, len(m.rows)+10
	idx := findRow(t, m, "Call the vet about Fido's checkup")
	m.cursor = idx
	h := m.currentHeadline()
	h.Title = "See [[https://a.example.com][A]] and [[https://b.example.com][B]]"

	out := stripANSI(m.View())
	lines := strings.Split(out, "\n")

	urlA, urlB := "https://a.example.com", "https://b.example.com"
	var lineA, lineB string
	for _, l := range lines {
		if strings.Contains(l, urlA) {
			lineA = l
		}
		if strings.Contains(l, urlB) {
			lineB = l
		}
		if strings.Contains(l, "item") && (strings.Contains(l, urlA) || strings.Contains(l, urlB)) {
			t.Errorf("status line = %q, should not also carry a URL", l)
		}
	}
	if lineA == "" {
		t.Errorf("view missing %q:\n%s", urlA, out)
	}
	if lineB == "" {
		t.Errorf("view missing %q:\n%s", urlB, out)
	}
	if lineA == lineB {
		t.Errorf("both URLs shared a line (%q), want one per line", lineA)
	}
}

// TestStatusBarLinksIgnoreTerminalWidth guards against reintroducing the
// old width-dependent squeeze/split behavior: the "Links:" section
// always shows one URL per line, and the status line never carries a
// URL, regardless of m.width (narrow, wide, or unset).
func TestStatusBarLinksIgnoreTerminalWidth(t *testing.T) {
	url := "https://example.com/a-rather-long-path/for-testing"

	for _, width := range []int{0, 40, 200} {
		ws := loadFixture(t)
		m := New(ws)
		idx := findRow(t, m, "Call the vet about Fido's checkup")
		m.cursor = idx
		h := m.currentHeadline()
		h.Title = fmt.Sprintf("Call the vet, see [[%s][Vet Site]] for hours", url)
		m.width, m.height = width, len(m.rows)+10

		out := stripANSI(m.View())
		lines := strings.Split(out, "\n")

		var found bool
		for _, l := range lines {
			if strings.Contains(l, "item") && strings.Contains(l, url) {
				t.Errorf("width=%d: status line = %q, should not also carry the URL", width, l)
			}
			if strings.Contains(l, url) {
				found = true
			}
		}
		if !found {
			t.Errorf("width=%d: view missing the URL:\n%s", width, out)
		}
	}
}
