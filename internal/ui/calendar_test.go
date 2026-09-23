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

func TestCalendarFileExcludedFromOutlineView(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	ws.Files = append(ws.Files, &org.File{
		Path: filepath.Join(ws.Dir, "calendar.org"),
		Headlines: []*org.Headline{
			calendarEventHeadline("abc123", now, now.Add(time.Hour)),
		},
	})
	m := New(ws)

	for _, r := range m.rows {
		if r.file != nil && filepath.Base(r.file.Path) == "calendar.org" {
			t.Fatalf("calendar.org's file row appears in the outline view")
		}
		if r.headline != nil && r.headline.Title == "Meeting abc123" {
			t.Fatalf("calendar.org's headline appears in the outline view")
		}
	}
}

func TestCalendarViewGroupsEventsByDayChronologically(t *testing.T) {
	ws := loadFixture(t)
	now := truncateToDate(time.Now()).Add(9 * time.Hour) // 09:00 today, safely mid-day
	dayAfter := now.Add(48 * time.Hour)
	tomorrow := now.Add(24 * time.Hour)
	ws.Files = append(ws.Files, &org.File{
		Path: filepath.Join(ws.Dir, "calendar.org"),
		Headlines: []*org.Headline{
			// Deliberately out of chronological file order.
			calendarEventHeadline("day-after", dayAfter, dayAfter.Add(time.Hour)),
			calendarEventHeadline("today", now, now.Add(time.Hour)),
			calendarEventHeadline("tomorrow", tomorrow, tomorrow.Add(time.Hour)),
		},
	})
	m := New(ws)
	m.switchToView(calendarView)

	var sections []string
	var order []string
	for _, r := range m.rows {
		if r.section != "" {
			sections = append(sections, r.section)
		}
		if r.headline != nil && !r.isBodyLine {
			order = append(order, r.headline.Title)
		}
	}

	wantSections := []string{
		truncateToDate(now).Format("2006-01-02 Mon"),
		truncateToDate(tomorrow).Format("2006-01-02 Mon"),
		truncateToDate(dayAfter).Format("2006-01-02 Mon"),
	}
	if len(sections) != len(wantSections) {
		t.Fatalf("day sections = %v, want %v", sections, wantSections)
	}
	for i := range wantSections {
		if sections[i] != wantSections[i] {
			t.Errorf("sections[%d] = %q, want %q", i, sections[i], wantSections[i])
		}
	}

	wantOrder := []string{"Meeting today", "Meeting tomorrow", "Meeting day-after"}
	if len(order) != len(wantOrder) {
		t.Fatalf("event order = %v, want %v", order, wantOrder)
	}
	for i := range wantOrder {
		if order[i] != wantOrder[i] {
			t.Errorf("order[%d] = %q, want %q", i, order[i], wantOrder[i])
		}
	}
}

func TestCalendarViewOrdersEventsWithinADayByStartTime(t *testing.T) {
	ws := loadFixture(t)
	base := truncateToDate(time.Now()).Add(9 * time.Hour)
	ws.Files = append(ws.Files, &org.File{
		Path: filepath.Join(ws.Dir, "calendar.org"),
		Headlines: []*org.Headline{
			calendarEventHeadline("later", base.Add(2*time.Hour), base.Add(3*time.Hour)),
			calendarEventHeadline("earlier", base, base.Add(time.Hour)),
		},
	})
	m := New(ws)
	m.switchToView(calendarView)

	var order []string
	for _, r := range m.rows {
		if r.headline != nil && !r.isBodyLine {
			order = append(order, r.headline.Title)
		}
	}
	want := []string{"Meeting earlier", "Meeting later"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Errorf("order[%d] = %q, want %q", i, order[i], want[i])
		}
	}
}

func TestCalendarViewOmitsHeadlinesWithoutGCALStart(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	stray := &org.Headline{Level: 1, Title: "Not a synced event"}
	ws.Files = append(ws.Files, &org.File{
		Path: filepath.Join(ws.Dir, "calendar.org"),
		Headlines: []*org.Headline{
			calendarEventHeadline("abc123", now, now.Add(time.Hour)),
			stray,
		},
	})
	m := New(ws)
	m.switchToView(calendarView)

	for _, r := range m.rows {
		if r.headline == stray {
			t.Fatalf("a headline with no GCAL_START appeared in calendar view")
		}
	}
}

func TestCalendarViewEmptyShowsFriendlyMessageAndStatusLines(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.width, m.height = 100, 20
	m.switchToView(calendarView)

	out := m.View()
	if !strings.Contains(out, "No calendar events found") {
		t.Errorf("View() = %q, want a friendly empty-calendar message", out)
	}
	lines := strings.Split(out, "\n")
	if len(lines) != m.height {
		t.Fatalf("got %d lines, want %d (height)\n---\n%s", len(lines), m.height, out)
	}
	status := lines[len(lines)-2]
	if !strings.Contains(status, "calendar") {
		t.Errorf("status line = %q, want it to mention the calendar view", status)
	}
}

func TestCalendarCommandSwitchesViewAndBack(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")

	m = sendKey(m, ":")
	m = typeKeys(m, "calendar")
	m, _ = sendKeyCmd(m, "enter")

	if m.view != calendarView {
		t.Fatalf("view after :calendar = %v, want calendarView", m.view)
	}

	m = sendKey(m, ":")
	m = typeKeys(m, "outline")
	m, _ = sendKeyCmd(m, "enter")

	if m.view != outlineView {
		t.Fatalf("view after :outline = %v, want outlineView", m.view)
	}
}

func TestCalendarCommandPositionsCursorOnInProgressMeeting(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	ws.Files = append(ws.Files, &org.File{
		Path: filepath.Join(ws.Dir, "calendar.org"),
		Headlines: []*org.Headline{
			calendarEventHeadline("past", now.Add(-2*time.Hour), now.Add(-1*time.Hour)),
			calendarEventHeadline("current", now.Add(-15*time.Minute), now.Add(15*time.Minute)),
			calendarEventHeadline("future", now.Add(time.Hour), now.Add(2*time.Hour)),
		},
	})
	m := New(ws)
	m.enterCalendarView()

	if got, want := m.currentHeadline().Title, "Meeting current"; got != want {
		t.Errorf("cursor landed on %q, want %q (the in-progress meeting)", got, want)
	}
}

func TestCalendarCommandPositionsCursorOnPriorMeetingWhenNoneInProgress(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	ws.Files = append(ws.Files, &org.File{
		Path: filepath.Join(ws.Dir, "calendar.org"),
		Headlines: []*org.Headline{
			calendarEventHeadline("older", now.Add(-4*time.Hour), now.Add(-3*time.Hour)),
			calendarEventHeadline("prior", now.Add(-2*time.Hour), now.Add(-1*time.Hour)),
			calendarEventHeadline("future", now.Add(time.Hour), now.Add(2*time.Hour)),
		},
	})
	m := New(ws)
	m.enterCalendarView()

	if got, want := m.currentHeadline().Title, "Meeting prior"; got != want {
		t.Errorf("cursor landed on %q, want %q (the most recent past meeting)", got, want)
	}
}

func TestCalendarCommandLeavesCursorAtTopWhenNothingHasStarted(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	ws.Files = append(ws.Files, &org.File{
		Path: filepath.Join(ws.Dir, "calendar.org"),
		Headlines: []*org.Headline{
			calendarEventHeadline("soon", now.Add(time.Hour), now.Add(2*time.Hour)),
			calendarEventHeadline("later", now.Add(3*time.Hour), now.Add(4*time.Hour)),
		},
	})
	m := New(ws)
	m.enterCalendarView()

	if m.cursor != 0 {
		t.Errorf("cursor = %d, want 0 (no in-progress or prior meeting to land on)", m.cursor)
	}
}

func TestWithCalendarFileOptionUsesCustomName(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	ws.Files = append(ws.Files, &org.File{
		Path: filepath.Join(ws.Dir, "my-calendar.org"),
		Headlines: []*org.Headline{
			calendarEventHeadline("abc123", now, now.Add(time.Hour)),
		},
	})
	m := New(ws, WithCalendarFile("my-calendar.org"))

	for _, r := range m.rows {
		if r.headline != nil && r.headline.Title == "Meeting abc123" {
			t.Fatalf("custom calendar file's headline appears in the outline view")
		}
	}

	m.switchToView(calendarView)
	found := false
	for _, r := range m.rows {
		if r.headline != nil && r.headline.Title == "Meeting abc123" {
			found = true
		}
	}
	if !found {
		t.Errorf("custom calendar file's event didn't appear in calendar view")
	}
}

// manyDaysCalendarFile builds a calendar.org-shaped file with n days,
// one event each, for exercising pagination in calendarView (10 days ->
// 20 rows: 10 day-header sections + 10 events).
func manyDaysCalendarFile(dir string, n int) *org.File {
	base := truncateToDate(time.Now()).Add(9 * time.Hour)
	var headlines []*org.Headline
	for i := 0; i < n; i++ {
		day := base.Add(time.Duration(i) * 24 * time.Hour)
		headlines = append(headlines, calendarEventHeadline(
			strings.Repeat("x", i+1), day, day.Add(time.Hour)))
	}
	return &org.File{Path: filepath.Join(dir, "calendar.org"), Headlines: headlines}
}

// TestCalendarViewStatusBarStaysAtBottomWhenPaginated is a regression
// test: with more day-separators than fit on one screen, only some of
// them actually render on any given page, which used to be fewer than
// sectionSeparatorBudget() reserved room for — the padding loop filled
// only up to `page` (computed using the full-list budget) rather than
// the content area's true remaining space, so the rendered output fell
// short of m.height and the status bar sat above the bottom of the
// screen instead of on its last line. See the View() code around
// sepShown/contentTarget.
func TestCalendarViewStatusBarStaysAtBottomWhenPaginated(t *testing.T) {
	ws := loadFixture(t)
	// Deliberately many: far more rows than the small height below can
	// show at once, forcing pagination so most day-separators fall
	// outside whatever single page is rendered.
	ws.Files = append(ws.Files, manyDaysCalendarFile(ws.Dir, 10))
	m := New(ws)
	m.switchToView(calendarView)
	m.width, m.height = 100, 10

	out := m.View()
	lines := strings.Split(out, "\n")
	if len(lines) != m.height {
		t.Fatalf("got %d lines, want %d (height) — status bar isn't pinned to the bottom\n---\n%s", len(lines), m.height, out)
	}

	status := stripANSI(lines[len(lines)-2])
	if !strings.Contains(status, "calendar") {
		t.Errorf("second-to-last line = %q, want the calendar status line there", status)
	}
	command := lines[len(lines)-1]
	if command != "" {
		t.Errorf("last line = %q, want the blank (idle) command line", command)
	}
}

// TestCalendarViewFillsScreenWhenPaginated is a regression test: fixing
// the status bar's position (see TestCalendarViewStatusBarStaysAtBottomWhenPaginated)
// by padding to contentBudget alone left the underlying problem
// unfixed — the render window itself (end, in View()) was still sized
// off pageSize()/sectionSeparatorBudget(), a full-list worst-case
// reservation that shrinks drastically once a view has many more
// section boundaries than fit on one page, so far fewer real rows were
// shown than the screen could actually hold (the rest of the space
// became blank padding instead of more entries). visibleRowCount fixes
// this by measuring what actually fits from a given starting row,
// rather than reserving room for every boundary in the whole list.
func TestCalendarViewFillsScreenWhenPaginated(t *testing.T) {
	ws := loadFixture(t)
	ws.Files = append(ws.Files, manyDaysCalendarFile(ws.Dir, 10))
	m := New(ws)
	m.switchToView(calendarView)
	m.width, m.height = 100, 10 // contentBudget = 10 - statusHeight(2) - infoBufferHeight(0) = 8

	if got := m.visibleRowCount(0); got != 6 {
		t.Fatalf("visibleRowCount(0) = %d, want 6 (3 full days: 3 section rows + 3 event rows, using the 8-line budget exactly)", got)
	}

	out := stripANSI(m.View())
	events := strings.Count(out, "Meeting ")
	if events < 3 {
		t.Errorf("events rendered = %d, want at least 3 (the screen should fill with entries, not mostly blank padding); output:\n%s", events, out)
	}
}

func TestVisibleRowCountOutOfRangeStartIsZero(t *testing.T) {
	ws := loadFixture(t)
	ws.Files = append(ws.Files, manyDaysCalendarFile(ws.Dir, 3))
	m := New(ws)
	m.switchToView(calendarView)
	m.width, m.height = 100, 10

	if got := m.visibleRowCount(-1); got != 0 {
		t.Errorf("visibleRowCount(-1) = %d, want 0", got)
	}
	if got := m.visibleRowCount(len(m.rows)); got != 0 {
		t.Errorf("visibleRowCount(len(rows)) = %d, want 0", got)
	}
}

func TestVisibleRowCountCoversWholeListWhenItFits(t *testing.T) {
	ws := loadFixture(t)
	ws.Files = append(ws.Files, manyDaysCalendarFile(ws.Dir, 2))
	m := New(ws)
	m.switchToView(calendarView)
	m.width, m.height = 100, len(m.rows)+m.sectionSeparatorBudget()+3

	if got := m.visibleRowCount(0); got != len(m.rows) {
		t.Errorf("visibleRowCount(0) = %d, want %d (the whole list, since the screen is tall enough)", got, len(m.rows))
	}
}

func TestCalendarViewSupportsOrdinaryHeadlineCommands(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	ws.Files = append(ws.Files, &org.File{
		Path: filepath.Join(ws.Dir, "calendar.org"),
		Headlines: []*org.Headline{
			calendarEventHeadline("abc123", now, now.Add(time.Hour)),
		},
	})
	m := New(ws)
	m.switchToView(calendarView)
	m.cursor = findRow(t, m, "Meeting abc123")

	m = sendKey(m, "d")
	m = sendKey(m, "d")

	for _, r := range m.rows {
		if r.headline != nil && r.headline.Title == "Meeting abc123" {
			t.Fatalf("dd didn't delete the calendar event")
		}
	}
}

func TestCalendarViewShowsTimeBeforeTitle(t *testing.T) {
	ws := loadFixture(t)
	base := truncateToDate(time.Now()).Add(14 * time.Hour)
	ws.Files = append(ws.Files, &org.File{
		Path: filepath.Join(ws.Dir, "calendar.org"),
		Headlines: []*org.Headline{
			calendarEventHeadline("abc123", base, base.Add(30*time.Minute)),
		},
	})
	m := New(ws)
	m.switchToView(calendarView)
	m.cursor = findRow(t, m, "Meeting abc123")

	rendered := stripANSI(m.renderRow(m.rows[m.cursor]))
	timeIdx := strings.Index(rendered, "14:00-14:30")
	titleIdx := strings.Index(rendered, "Meeting abc123")
	if timeIdx < 0 {
		t.Fatalf("rendered row = %q, want the event's time (14:00-14:30)", rendered)
	}
	if titleIdx < 0 {
		t.Fatalf("rendered row = %q, want the event's title", rendered)
	}
	if timeIdx >= titleIdx {
		t.Errorf("rendered row = %q, want the time before the title", rendered)
	}
}

func TestCalendarViewAllDayEventShowsAllDayInsteadOfTime(t *testing.T) {
	ws := loadFixture(t)
	day := truncateToDate(time.Now())
	h := &org.Headline{Level: 1, Title: "Offsite"}
	h.SetProperty("GCAL_EVENT_ID", "allday1")
	h.SetProperty("GCAL_START", day.Format(time.RFC3339))
	h.SetProperty("GCAL_END", day.AddDate(0, 0, 1).Format(time.RFC3339))
	ws.Files = append(ws.Files, &org.File{
		Path:      filepath.Join(ws.Dir, "calendar.org"),
		Headlines: []*org.Headline{h},
	})
	m := New(ws)
	m.switchToView(calendarView)
	m.cursor = findRow(t, m, "Offsite")

	rendered := stripANSI(m.renderRow(m.rows[m.cursor]))
	if !strings.Contains(rendered, "All day") {
		t.Errorf("rendered row = %q, want \"All day\" instead of a time range", rendered)
	}
}

func TestCalendarEventsAreFoldedByDefault(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	h := calendarEventHeadline("abc123", now, now.Add(time.Hour))
	h.Body = []string{"  Location: Room 5"}
	ws.Files = append(ws.Files, &org.File{
		Path:      filepath.Join(ws.Dir, "calendar.org"),
		Headlines: []*org.Headline{h},
	})
	m := New(ws)
	m.switchToView(calendarView)

	for _, r := range m.rows {
		if r.isBodyLine {
			t.Fatalf("event body is visible by default; rows should start folded")
		}
	}
	if !m.collapsed[h] {
		t.Errorf("m.collapsed[event] = false, want true (folded by default)")
	}

	// Expanding it should stick across a rebuild, not reset back to
	// folded every time.
	m.cursor = findRow(t, m, "Meeting abc123")
	m = sendKey(m, "tab")
	if m.collapsed[h] {
		t.Fatalf("Tab didn't unfold the event")
	}
	m.rebuildRows()
	if m.collapsed[h] {
		t.Errorf("event re-folded itself on rebuild after the user explicitly unfolded it")
	}
	found := false
	for _, r := range m.rows {
		if r.isBodyLine && r.headline == h {
			found = true
		}
	}
	if !found {
		t.Errorf("body line not shown after unfolding")
	}
}

func TestCalendarViewShowsItemsAttachedToAMeetingByDefault(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	event := calendarEventHeadline("abc123", now, now.Add(time.Hour))
	linked := linkedToOneOffMeetings("Follow up on budget", "abc123")
	ws.Files = append(ws.Files,
		&org.File{Path: filepath.Join(ws.Dir, "calendar.org"), Headlines: []*org.Headline{event}},
		&org.File{Path: filepath.Join(ws.Dir, "projects.org"), Headlines: []*org.Headline{linked}},
	)
	m := New(ws)
	m.switchToView(calendarView)

	if !m.collapsed[event] {
		t.Fatalf("event unexpectedly starts unfolded")
	}

	found := false
	for _, r := range m.rows {
		if r.headline == linked {
			found = true
			if !r.isCalendarLinkedItem {
				t.Errorf("linked item row isn't marked isCalendarLinkedItem")
			}
		}
	}
	if !found {
		t.Errorf("linked item %q not shown under its meeting, even though the event is still folded", linked.Title)
	}
}

// TestCalendarLinkedItemRowTruncatesLongParentTitleInPlaceTag mirrors
// TestAgendaItemRowTruncatesLongParentTitleInPlaceTag (row_width_test.go):
// renderCalendarLinkedItemRowWithBg's "[file › parent]" tag is built the
// same way as an agenda row's (see agendaPlace), so an unbounded parent
// title needs the same budget-driven truncation or it pushes the item's
// own title, and even its tags, off the edge.
func TestCalendarLinkedItemRowTruncatesLongParentTitleInPlaceTag(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	event := calendarEventHeadline("abc123", now, now.Add(time.Hour))
	orgText := fmt.Sprintf("* Project %s\n** Follow up on budget\n", strings.Repeat("a very long parent title ", 10))
	f, err := org.Parse(strings.NewReader(orgText), "projects.org")
	if err != nil {
		t.Fatalf("org.Parse: %v", err)
	}
	linked := f.Headlines[0].Children[0]
	linked.SetProperty("GCAL_EVENT_IDS", "abc123")
	ws.Files = append(ws.Files,
		&org.File{Path: filepath.Join(ws.Dir, "calendar.org"), Headlines: []*org.Headline{event}},
		f,
	)
	m := New(ws)
	m.width = 60
	m.switchToView(calendarView)

	line := m.renderRow(m.rows[findRow(t, m, "Follow up on budget")])
	plain := stripANSI(line)

	if !strings.Contains(plain, "Follow up on budget") {
		t.Errorf("rendered calendar linked-item row = %q, want the item's own title still visible despite the long parent title", plain)
	}
	if !strings.Contains(plain, "…") {
		t.Errorf("rendered calendar linked-item row = %q, want an ellipsis marking the truncated parent title", plain)
	}
	if w := lipgloss.Width(line); w > m.width {
		t.Errorf("rendered calendar linked-item row width = %d, want <= %d", w, m.width)
	}
}

// TestCalendarViewCapsLongAttendeeTagListKeepingTitleVisible is the
// reported bug: a meeting with a large invite list carries one
// "@attendee" tag per attendee (see attendeeTags in
// internal/calendarsync/convert.go), and renderCalendarItemRowWithBg
// used to show that list in full via fitRowLine's "suffix always wins"
// rule, which could crowd even a short meeting title off the row
// entirely. The tag list is now capped (see renderTagsSuffix), so the
// title stays visible.
func TestCalendarViewCapsLongAttendeeTagListKeepingTitleVisible(t *testing.T) {
	ws := loadFixture(t)
	base := truncateToDate(time.Now()).Add(9 * time.Hour)
	h := calendarEventHeadline("abc123", base, base.Add(30*time.Minute))
	h.Title = "Standup"
	var tags []string
	for i := 0; i < 20; i++ {
		tags = append(tags, fmt.Sprintf("attendee-name-number-%d", i))
	}
	h.Tags = tags
	ws.Files = append(ws.Files, &org.File{
		Path:      filepath.Join(ws.Dir, "calendar.org"),
		Headlines: []*org.Headline{h},
	})
	m := New(ws)
	m.width = 100
	m.switchToView(calendarView)

	line := m.renderRow(m.rows[findRow(t, m, "Standup")])
	plain := stripANSI(line)

	if !strings.Contains(plain, "Standup") {
		t.Errorf("rendered calendar row = %q, want the meeting title still visible despite the long attendee tag list", plain)
	}
	if !strings.Contains(plain, "…") {
		t.Errorf("rendered calendar row = %q, want an ellipsis marking the truncated tag list", plain)
	}
	if strings.Contains(plain, "attendee-name-number-19") {
		t.Errorf("rendered calendar row = %q, want the tag list capped well short of all 20 attendees", plain)
	}
}

// TestCalendarViewUnfoldedBodyShowsBeforeLinkedItems is a regression
// test: appendCalendarHeadlines used to append an event's linked items
// (see linkedMeetingItems) unconditionally right after the event row,
// before its own body/children — so unfolding an event to see its
// Location/description/link pushed that detail down below whatever was
// attached, instead of showing it directly beneath the event as
// expected.
func TestCalendarViewUnfoldedBodyShowsBeforeLinkedItems(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	event := calendarEventHeadline("abc123", now, now.Add(time.Hour))
	event.Body = []string{"  Location: Room 5"}
	linked := linkedToOneOffMeetings("Follow up on budget", "abc123")
	ws.Files = append(ws.Files,
		&org.File{Path: filepath.Join(ws.Dir, "calendar.org"), Headlines: []*org.Headline{event}},
		&org.File{Path: filepath.Join(ws.Dir, "projects.org"), Headlines: []*org.Headline{linked}},
	)
	m := New(ws)
	m.switchToView(calendarView)
	m.cursor = findRow(t, m, "Meeting abc123")
	m = sendKey(m, "tab")

	var order []string
	for _, r := range m.rows {
		switch {
		case r.headline == event && r.isCalendarItem:
			order = append(order, "event")
		case r.isBodyLine && r.headline == event:
			order = append(order, "body")
		case r.headline == linked:
			order = append(order, "linked")
		}
	}
	want := []string{"event", "body", "linked"}
	if len(order) != len(want) {
		t.Fatalf("row order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Errorf("row order = %v, want %v", order, want)
			break
		}
	}
}

// leadingIndent returns the run of spaces starting at the given rune
// offset (past the fixed 5-column mark/lock/meeting/dirty-gutter+
// separator prefix every headline row shares — see markColumn/
// lockColumn/meetingColumn/gutter, whose icons, like "▣", can be
// multi-byte, hence counting in runes rather than bytes) up to the next
// non-space rune (a fold glyph or the start of the title).
func leadingIndent(rendered string, offset int) string {
	rest := string([]rune(rendered)[offset:])
	return rest[:len(rest)-len(strings.TrimLeft(rest, " "))]
}

func TestCalendarViewLinkedItemIndentedDeeperThanEvent(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	event := calendarEventHeadline("abc123", now, now.Add(time.Hour))
	linked := linkedToOneOffMeetings("Follow up on budget", "abc123")
	ws.Files = append(ws.Files,
		&org.File{Path: filepath.Join(ws.Dir, "calendar.org"), Headlines: []*org.Headline{event}},
		&org.File{Path: filepath.Join(ws.Dir, "projects.org"), Headlines: []*org.Headline{linked}},
	)
	m := New(ws)
	m.switchToView(calendarView)

	eventRendered := stripANSI(m.renderRow(m.rows[findRow(t, m, "Meeting abc123")]))
	itemRendered := stripANSI(m.renderRow(m.rows[findRow(t, m, "Follow up on budget")]))

	eventIndent := leadingIndent(eventRendered, 5)
	itemIndent := leadingIndent(itemRendered, 5)
	if len(itemIndent) != len(eventIndent)+2 {
		t.Errorf("item indent = %d spaces, event indent = %d spaces; want the item indented exactly one level (2 spaces) deeper", len(itemIndent), len(eventIndent))
	}
}

func TestCalendarViewNoFoldGlyphForMeetingWithNothingAttached(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	event := calendarEventHeadline("abc123", now, now.Add(time.Hour)) // no Body, nothing attached
	ws.Files = append(ws.Files, &org.File{
		Path:      filepath.Join(ws.Dir, "calendar.org"),
		Headlines: []*org.Headline{event},
	})
	m := New(ws)
	m.switchToView(calendarView)
	m.cursor = findRow(t, m, "Meeting abc123")

	rendered := stripANSI(m.renderRow(m.rows[m.cursor]))
	if strings.Contains(rendered, "▶") || strings.Contains(rendered, "▼") {
		t.Errorf("rendered row = %q, want no fold glyph (nothing to fold)", rendered)
	}
}

func TestEnterJumpsToSourceFromCalendarLinkedItem(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	event := calendarEventHeadline("abc123", now, now.Add(time.Hour))
	linked := linkedToOneOffMeetings("Follow up on budget", "abc123")
	ws.Files = append(ws.Files,
		&org.File{Path: filepath.Join(ws.Dir, "calendar.org"), Headlines: []*org.Headline{event}},
		&org.File{Path: filepath.Join(ws.Dir, "projects.org"), Headlines: []*org.Headline{linked}},
	)
	m := New(ws)
	m.switchToView(calendarView)
	m.cursor = findRow(t, m, "Follow up on budget")
	if !m.rows[m.cursor].isCalendarLinkedItem {
		t.Fatalf("fixture assumption broken: cursor isn't on the linked item row")
	}

	m = sendKey(m, "enter")

	if m.view != outlineView {
		t.Fatalf("view after enter = %v, want outlineView", m.view)
	}
	if m.currentHeadline() != linked {
		t.Errorf("cursor after enter = %v, want the linked headline %v", m.currentHeadline(), linked)
	}
}

func TestEnterNoopOnCalendarEventRow(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	event := calendarEventHeadline("abc123", now, now.Add(time.Hour))
	ws.Files = append(ws.Files, &org.File{
		Path:      filepath.Join(ws.Dir, "calendar.org"),
		Headlines: []*org.Headline{event},
	})
	m := New(ws)
	m.switchToView(calendarView)
	m.cursor = findRow(t, m, "Meeting abc123")

	m = sendKey(m, "enter")

	if m.view != calendarView {
		t.Fatalf("view after enter on a calendar event row = %v, want calendarView (a no-op — calendar.org isn't in the outline)", m.view)
	}
}

func TestCalendarViewStatusLineShowsEventOwnLink(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	ws.Files = append(ws.Files, &org.File{
		Path: filepath.Join(ws.Dir, "calendar.org"),
		Headlines: []*org.Headline{
			calendarEventHeadline("abc123", now, now.Add(time.Hour)),
		},
	})
	m := New(ws)
	m.switchToView(calendarView)
	m.cursor = findRow(t, m, "Meeting abc123")
	m.width, m.height = 200, len(m.rows)+5

	out := stripANSI(m.View())

	if !strings.Contains(out, "Meeting abc123") {
		t.Errorf("view = %q, want the event's own title", out)
	}
	if !strings.Contains(out, "https://calendar.google.com/event?eid=abc123") {
		t.Errorf("view = %q, want the event's own GCAL_HTML_LINK", out)
	}
}
