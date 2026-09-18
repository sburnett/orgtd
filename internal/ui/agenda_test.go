package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sburnett/orgtd/internal/org"
	"github.com/sburnett/orgtd/internal/workspace"
)

// ts formats t (date only) the same way orgtd itself writes a
// SCHEDULED/DEADLINE timestamp, for building test fixtures whose dates
// are relative to the real "now" (so tests never go stale).
func ts(t time.Time) string {
	return t.Format("2006-01-02 Mon")
}

// agendaFixture builds a workspace over a single in-memory org file, so
// agenda tests can use dates relative to the real clock without
// depending on (or polluting) the shared testdata/orgdir fixture.
func agendaFixture(t *testing.T, orgText string) *workspace.Workspace {
	t.Helper()
	f, err := org.Parse(strings.NewReader(orgText), "agenda.org")
	if err != nil {
		t.Fatalf("org.Parse: %v", err)
	}
	return &workspace.Workspace{Dir: "agenda-fixture", Files: []*org.File{f}}
}

func TestParseTimestampDateAcceptsKnownFormats(t *testing.T) {
	now := truncateToDate(time.Now())
	cases := []*org.Timestamp{
		{Raw: ts(now)},
		{Raw: now.Format("2006-01-02 Mon 15:04")},
		{Raw: now.Format("2006-01-02")}, // weekday-less fallback
	}
	for _, c := range cases {
		got, missed, ok := parseTimestampDate(c, now)
		if !ok {
			t.Errorf("parseTimestampDate(%q) did not match", c.Raw)
			continue
		}
		if !got.Equal(now) {
			t.Errorf("parseTimestampDate(%q) = %v, want %v", c.Raw, got, now)
		}
		if missed != 0 {
			t.Errorf("parseTimestampDate(%q) missed = %d, want 0 (non-repeating)", c.Raw, missed)
		}
	}
}

func TestParseTimestampDateRejectsGarbageAndNil(t *testing.T) {
	now := truncateToDate(time.Now())
	if _, _, ok := parseTimestampDate(nil, now); ok {
		t.Errorf("parseTimestampDate(nil) matched")
	}
	if _, _, ok := parseTimestampDate(&org.Timestamp{Raw: "not a date"}, now); ok {
		t.Errorf("parseTimestampDate(garbage) matched")
	}
}

// TestParseTimestampDateRepeaterStaysOverdueUntilCaughtUp verifies the
// org-mode-matching behavior: a repeating timestamp's date only ever
// advances when the item is completed, so a stale date (one the user
// skipped without marking done) is reported at its most recent due
// occurrence — still in the past — rather than being rolled forward past
// today and hidden. missed reports how many earlier occurrences already
// elapsed on top of that.
func TestParseTimestampDateRepeaterStaysOverdueUntilCaughtUp(t *testing.T) {
	now := truncateToDate(time.Now())

	// 17 days ago, weekly: occurrences at -17, -10, -3 (all <= today), and
	// +4 (> today, not reached) — so the current occurrence is 3 days ago,
	// with 2 earlier occurrences (-17, -10) already elapsed on top of it.
	weekly := ts(now.AddDate(0, 0, -17)) + " +1w"
	wantDate := now.AddDate(0, 0, -3)
	if got, missed, ok := parseTimestampDate(&org.Timestamp{Raw: weekly}, now); !ok || !got.Equal(wantDate) || missed != 2 {
		t.Errorf("parseTimestampDate(%q) = %v, missed=%d, ok=%v, want %v missed=2", weekly, got, missed, ok, wantDate)
	}

	// Scheduled yesterday, daily: today is itself a valid occurrence (one
	// interval past base), so the current occurrence is today — Due
	// Today, not Overdue — with yesterday's skipped occurrence counted
	// as 1 missed.
	daily := ts(now.AddDate(0, 0, -1)) + " +1d"
	if got, missed, ok := parseTimestampDate(&org.Timestamp{Raw: daily}, now); !ok || !got.Equal(now) || missed != 1 {
		t.Errorf("parseTimestampDate(%q) = %v, missed=%d, ok=%v, want %v missed=1", daily, got, missed, ok, now)
	}

	// Monthly/yearly land on an irregular day count (calendar month/year
	// lengths vary), so just check the result stays on-or-before today
	// (never rolled into the future) with a positive missed count.
	for _, c := range []struct{ name, raw string }{
		{"monthly, well overdue", ts(now.AddDate(0, -2, -3)) + " +1m"},
		{"yearly, overdue", ts(now.AddDate(-1, 0, -1)) + " +1y"},
	} {
		got, missed, ok := parseTimestampDate(&org.Timestamp{Raw: c.raw}, now)
		if !ok {
			t.Errorf("%s: parseTimestampDate(%q) did not match", c.name, c.raw)
			continue
		}
		if got.After(now) {
			t.Errorf("%s: parseTimestampDate(%q) = %v, rolled past today %v", c.name, c.raw, got, now)
		}
		if missed < 1 {
			t.Errorf("%s: parseTimestampDate(%q) missed = %d, want at least 1", c.name, c.raw, missed)
		}
	}
}

func TestParseTimestampDateRepeaterLeavesFutureDateUnchanged(t *testing.T) {
	now := truncateToDate(time.Now())
	future := now.AddDate(0, 0, 5)
	raw := ts(future) + " +1w"
	got, missed, ok := parseTimestampDate(&org.Timestamp{Raw: raw}, now)
	if !ok {
		t.Fatalf("parseTimestampDate(%q) did not match", raw)
	}
	if !got.Equal(future) {
		t.Errorf("parseTimestampDate(%q) = %v, want unchanged future date %v", raw, got, future)
	}
	if missed != 0 {
		t.Errorf("parseTimestampDate(%q) missed = %d, want 0 (not due yet)", raw, missed)
	}
}

func TestParseTimestampDateRepeaterExactlyTodayUnchanged(t *testing.T) {
	now := truncateToDate(time.Now())
	raw := ts(now) + " +1w"
	got, missed, ok := parseTimestampDate(&org.Timestamp{Raw: raw}, now)
	if !ok {
		t.Fatalf("parseTimestampDate(%q) did not match", raw)
	}
	if !got.Equal(now) {
		t.Errorf("parseTimestampDate(%q) = %v, want today %v unchanged", raw, got, now)
	}
	if missed != 0 {
		t.Errorf("parseTimestampDate(%q) missed = %d, want 0", raw, missed)
	}
}

func TestParseTimestampDateStripsWarningPeriodCookie(t *testing.T) {
	now := truncateToDate(time.Now())
	raw := ts(now) + " +1w -3d"
	got, _, ok := parseTimestampDate(&org.Timestamp{Raw: raw}, now)
	if !ok {
		t.Fatalf("parseTimestampDate(%q) did not match", raw)
	}
	if !got.Equal(now) {
		t.Errorf("parseTimestampDate(%q) = %v, want %v", raw, got, now)
	}
}

func TestRepeaterCookie(t *testing.T) {
	if got := repeaterCookie(&org.Timestamp{Raw: "2026-08-10 Mon +1w"}); got != "+1w" {
		t.Errorf("repeaterCookie = %q, want %q", got, "+1w")
	}
	if got := repeaterCookie(&org.Timestamp{Raw: "2026-08-10 Mon"}); got != "" {
		t.Errorf("repeaterCookie = %q, want empty", got)
	}
	if got := repeaterCookie(nil); got != "" {
		t.Errorf("repeaterCookie(nil) = %q, want empty", got)
	}
}

func TestAgendaSectionBucketing(t *testing.T) {
	today := truncateToDate(time.Now())
	cases := []struct {
		date time.Time
		want string
	}{
		{today.AddDate(0, 0, -1), "Overdue"},
		{today, "Due Today"},
		{today.AddDate(0, 0, 1), "Upcoming"},
	}
	for _, c := range cases {
		if got := agendaSection(c.date, today); got != c.want {
			t.Errorf("agendaSection(%v) = %q, want %q", c.date, got, c.want)
		}
	}
}

func TestAgendaEntriesExcludesDoneCancelledAndSomeday(t *testing.T) {
	now := truncateToDate(time.Now())
	org := fmt.Sprintf(`* DONE Finished task
  DEADLINE: <%[1]s>
* CANCELLED Abandoned task
  DEADLINE: <%[1]s>
* SOMEDAY Someday task
  DEADLINE: <%[1]s>
* TODO Still relevant
  DEADLINE: <%[1]s>
`, ts(now))
	ws := agendaFixture(t, org)
	m := New(ws)

	entries := m.agendaEntries(now, 14)
	if len(entries) != 1 {
		t.Fatalf("agendaEntries = %d entries, want 1 (only the TODO): %+v", len(entries), entries)
	}
	if entries[0].h.Title != "Still relevant" {
		t.Errorf("entries[0].h.Title = %q, want %q", entries[0].h.Title, "Still relevant")
	}
}

func TestAgendaEntriesSomedayExcludedEvenWithinWindow(t *testing.T) {
	now := truncateToDate(time.Now())
	orgText := fmt.Sprintf("* SOMEDAY Not committed\n  SCHEDULED: <%s>\n", ts(now.AddDate(0, 0, 2)))
	ws := agendaFixture(t, orgText)
	m := New(ws)

	if entries := m.agendaEntries(now, 14); len(entries) != 0 {
		t.Errorf("agendaEntries = %+v, want none (SOMEDAY always excluded)", entries)
	}
}

func TestAgendaEntriesDualDateProducesTwoEntries(t *testing.T) {
	now := truncateToDate(time.Now())
	scheduled := now.AddDate(0, 0, 3)
	deadline := now.AddDate(0, 0, 5)
	orgText := fmt.Sprintf("* NEXT Both dates\n  SCHEDULED: <%s> DEADLINE: <%s>\n", ts(scheduled), ts(deadline))
	ws := agendaFixture(t, orgText)
	m := New(ws)

	entries := m.agendaEntries(now, 14)
	if len(entries) != 2 {
		t.Fatalf("agendaEntries = %d entries, want 2 (one per date): %+v", len(entries), entries)
	}
	labels := map[string]time.Time{entries[0].label: entries[0].date, entries[1].label: entries[1].date}
	if !labels["Scheduled"].Equal(scheduled) {
		t.Errorf("Scheduled entry date = %v, want %v", labels["Scheduled"], scheduled)
	}
	if !labels["Deadline"].Equal(deadline) {
		t.Errorf("Deadline entry date = %v, want %v", labels["Deadline"], deadline)
	}
}

func TestAgendaEntriesExcludesBeyondWindow(t *testing.T) {
	now := truncateToDate(time.Now())
	orgText := fmt.Sprintf("* TODO Just inside\n  DEADLINE: <%s>\n* TODO Just outside\n  DEADLINE: <%s>\n",
		ts(now.AddDate(0, 0, 14)), ts(now.AddDate(0, 0, 15)))
	ws := agendaFixture(t, orgText)
	m := New(ws)

	entries := m.agendaEntries(now, 14)
	if len(entries) != 1 || entries[0].h.Title != "Just inside" {
		t.Fatalf("agendaEntries = %+v, want just 'Just inside'", entries)
	}
}

func TestAgendaEntriesRecurringPastDueShowsAsOverdueWithMissedCount(t *testing.T) {
	now := truncateToDate(time.Now())
	// Scheduled 17 days ago, weekly: current occurrence is 3 days ago
	// (occurrences at -17, -10, -3; +4 hasn't arrived), 2 missed on top.
	orgText := fmt.Sprintf("* TODO Weekly standup\n  SCHEDULED: <%s +1w>\n", ts(now.AddDate(0, 0, -17)))
	ws := agendaFixture(t, orgText)
	m := New(ws)

	entries := m.agendaEntries(now, 14)
	if len(entries) != 1 {
		t.Fatalf("agendaEntries = %d entries, want 1: %+v", len(entries), entries)
	}
	want := now.AddDate(0, 0, -3)
	if !entries[0].date.Equal(want) {
		t.Errorf("entries[0].date = %v, want current occurrence %v", entries[0].date, want)
	}
	if entries[0].repeater != "+1w" {
		t.Errorf("entries[0].repeater = %q, want %q", entries[0].repeater, "+1w")
	}
	if entries[0].missed != 2 {
		t.Errorf("entries[0].missed = %d, want 2", entries[0].missed)
	}
	if got := agendaSection(entries[0].date, now); got != "Overdue" {
		t.Errorf("agendaSection(current occurrence) = %q, want %q (a skipped recurring item stays visible)", got, "Overdue")
	}
}

func TestAgendaItemRowShowsRepeaterCookie(t *testing.T) {
	now := truncateToDate(time.Now())
	orgText := fmt.Sprintf("* NEXT Weekly standup\n  SCHEDULED: <%s +1w>\n", ts(now))
	ws := agendaFixture(t, orgText)
	m := New(ws)
	m.switchToView(agendaView)

	line := stripANSI(m.renderRow(m.rows[1]))
	if !strings.Contains(line, "+1w") {
		t.Errorf("agenda item row = %q, want the repeater cookie shown alongside the date", line)
	}
}

func TestAgendaItemRowShowsMissedCountWhenOverdue(t *testing.T) {
	now := truncateToDate(time.Now())
	orgText := fmt.Sprintf("* TODO Weekly standup\n  SCHEDULED: <%s +1w>\n", ts(now.AddDate(0, 0, -17)))
	ws := agendaFixture(t, orgText)
	m := New(ws)
	m.switchToView(agendaView)

	line := stripANSI(m.renderRow(m.rows[1]))
	if !strings.Contains(line, "(2x)") {
		t.Errorf("overdue recurring item row = %q, want a %q missed-count marker", line, "(2x)")
	}
}

func TestAgendaItemRowOmitsMissedCountWhenNotOverdue(t *testing.T) {
	now := truncateToDate(time.Now())
	// Due today exactly (no slip) and a not-yet-due future occurrence:
	// neither should show a missed-count marker.
	orgText := fmt.Sprintf("* TODO Due today, on schedule\n  SCHEDULED: <%s +1w>\n* TODO Not due yet\n  SCHEDULED: <%s +1w>\n",
		ts(now), ts(now.AddDate(0, 0, 3)))
	ws := agendaFixture(t, orgText)
	m := New(ws)
	m.switchToView(agendaView)

	for _, r := range m.rows {
		if !r.isAgendaItem {
			continue
		}
		line := stripANSI(m.renderRow(r))
		if strings.Contains(line, "x)") {
			t.Errorf("row = %q, should have no missed-count marker (not overdue)", line)
		}
	}
}

func TestAgendaEntriesExcludesUnparseableDate(t *testing.T) {
	ws := agendaFixture(t, "* TODO Weird date\n  DEADLINE: <not-a-real-date>\n")
	m := New(ws)

	if entries := m.agendaEntries(truncateToDate(time.Now()), 14); len(entries) != 0 {
		t.Errorf("agendaEntries = %+v, want none (unparseable date)", entries)
	}
}

func TestAppendAgendaRowsSkipsEmptySectionsAndSortsByDate(t *testing.T) {
	now := truncateToDate(time.Now())
	orgText := fmt.Sprintf(`* TODO Later this week
  DEADLINE: <%s>
* TODO Sooner this week
  DEADLINE: <%s>
`, ts(now.AddDate(0, 0, 5)), ts(now.AddDate(0, 0, 2)))
	ws := agendaFixture(t, orgText)
	m := New(ws)
	m.switchToView(agendaView)

	if len(m.rows) != 3 {
		t.Fatalf("rows = %d, want 3 (1 section header + 2 items, Overdue/Due Today skipped)", len(m.rows))
	}
	if m.rows[0].section != "Upcoming" {
		t.Errorf("rows[0].section = %q, want %q", m.rows[0].section, "Upcoming")
	}
	if m.rows[0].level != 0 {
		t.Errorf("section row level = %d, want 0", m.rows[0].level)
	}
	if got := m.rows[1].headline.Title; got != "Sooner this week" {
		t.Errorf("rows[1] = %q, want the sooner item first (sorted by date)", got)
	}
	if m.rows[1].level != 1 {
		t.Errorf("item row level = %d, want 1", m.rows[1].level)
	}
	if got := m.rows[2].headline.Title; got != "Later this week" {
		t.Errorf("rows[2] = %q, want the later item second", got)
	}
}

func TestNextActionsSectionListsNextItemsWithNoDate(t *testing.T) {
	orgText := "* NEXT Undated next action\n* TODO Not a next action\n"
	ws := agendaFixture(t, orgText)
	m := New(ws)
	m.switchToView(agendaView)

	if len(m.rows) != 2 {
		t.Fatalf("rows = %d, want 2 (1 section header + 1 item)", len(m.rows))
	}
	if m.rows[0].section != "Next Actions" {
		t.Errorf("rows[0].section = %q, want %q", m.rows[0].section, "Next Actions")
	}
	if got := m.rows[1].headline.Title; got != "Undated next action" {
		t.Errorf("rows[1] = %q, want the NEXT item", got)
	}
}

func TestNextActionsSectionOmittedWhenNoNextItems(t *testing.T) {
	ws := agendaFixture(t, "* TODO Just a todo\n* WAITING Blocked\n")
	m := New(ws)
	m.switchToView(agendaView)

	for _, r := range m.rows {
		if r.section == "Next Actions" {
			t.Fatalf("Next Actions section present with no NEXT items: %+v", m.rows)
		}
	}
}

func TestNextActionsSectionComesAfterDateBasedSections(t *testing.T) {
	now := truncateToDate(time.Now())
	orgText := fmt.Sprintf("* TODO Overdue item\n  DEADLINE: <%s>\n* NEXT Undated next action\n", ts(now.AddDate(0, 0, -1)))
	ws := agendaFixture(t, orgText)
	m := New(ws)
	m.switchToView(agendaView)

	var sections []string
	for _, r := range m.rows {
		if r.section != "" {
			sections = append(sections, r.section)
		}
	}
	want := []string{"Overdue", "Next Actions"}
	if len(sections) != len(want) {
		t.Fatalf("sections = %v, want %v", sections, want)
	}
	for i := range want {
		if sections[i] != want[i] {
			t.Errorf("sections[%d] = %q, want %q", i, sections[i], want[i])
		}
	}
}

func TestNextActionWithADateAppearsInBothSections(t *testing.T) {
	now := truncateToDate(time.Now())
	orgText := fmt.Sprintf("* NEXT Due today and next\n  DEADLINE: <%s>\n", ts(now))
	ws := agendaFixture(t, orgText)
	m := New(ws)
	m.switchToView(agendaView)

	count := 0
	for _, r := range m.rows {
		if r.headline != nil && r.headline.Title == "Due today and next" {
			count++
		}
	}
	if count != 2 {
		t.Errorf("'Due today and next' appears %d times, want 2 (once in Due Today, once in Next Actions)", count)
	}
}

func TestNextActionsRowRenderingHasNoDateLabel(t *testing.T) {
	ws := agendaFixture(t, "* NEXT Undated next action\n")
	m := New(ws)
	m.switchToView(agendaView)

	line := stripANSI(m.renderRow(m.rows[1]))
	if !strings.Contains(line, "[agenda.org]") {
		t.Errorf("Next Actions row = %q, missing the file tag", line)
	}
	if strings.Contains(line, "Scheduled:") || strings.Contains(line, "Deadline:") {
		t.Errorf("Next Actions row = %q, should have no date label", line)
	}
}

func TestAgendaItemRowRendering(t *testing.T) {
	now := truncateToDate(time.Now())
	orgText := fmt.Sprintf("* NEXT Draft the doc\n  DEADLINE: <%s>\n", ts(now))
	ws := agendaFixture(t, orgText)
	m := New(ws)
	m.switchToView(agendaView)

	line := stripANSI(m.renderRow(m.rows[1]))
	for _, want := range []string{"NEXT", "Draft the doc", "[agenda.org]", "Deadline:", now.Format("2006-01-02")} {
		if !strings.Contains(line, want) {
			t.Errorf("agenda item row = %q, missing %q", line, want)
		}
	}
	if strings.Contains(line, "▼") || strings.Contains(line, "▶") {
		t.Errorf("agenda item row = %q, should have no fold arrow (agenda is flat)", line)
	}
}

func TestAgendaItemRowShowsImmediateParentTitle(t *testing.T) {
	now := truncateToDate(time.Now())
	orgText := fmt.Sprintf("* Q3 Planning\n** NEXT Draft the doc\n   DEADLINE: <%s>\n", ts(now))
	ws := agendaFixture(t, orgText)
	m := New(ws)
	m.switchToView(agendaView)

	line := stripANSI(m.renderRow(m.rows[1]))
	if !strings.Contains(line, "[agenda.org › Q3 Planning]") {
		t.Errorf("agenda item row = %q, want the [file › parent] tag naming its immediate parent", line)
	}
}

func TestAgendaItemRowOmitsParentTagForATopLevelHeadline(t *testing.T) {
	now := truncateToDate(time.Now())
	orgText := fmt.Sprintf("* NEXT Draft the doc\n  DEADLINE: <%s>\n", ts(now))
	ws := agendaFixture(t, orgText)
	m := New(ws)
	m.switchToView(agendaView)

	line := stripANSI(m.renderRow(m.rows[1]))
	if !strings.Contains(line, "[agenda.org]") {
		t.Errorf("agenda item row = %q, want a plain [agenda.org] tag for a top-level headline with no parent", line)
	}
}

func TestAgendaItemRowShowsMarkLetterInGutter(t *testing.T) {
	now := truncateToDate(time.Now())
	orgText := fmt.Sprintf("* NEXT Draft the doc\n  DEADLINE: <%s>\n", ts(now))
	ws := agendaFixture(t, orgText)
	m := New(ws)
	m.switchToView(agendaView)
	m.cursor = 1 // the item row, after the section header

	m = sendKey(m, "m")
	m = sendKey(m, "a")

	line := m.renderRow(m.rows[1])
	if !strings.HasPrefix(stripANSI(line), "a ") {
		t.Errorf("agenda item row = %q, want it to start with the 'a' mark marker", line)
	}
}

func TestPinnedHeaderVisibleInAgendaView(t *testing.T) {
	now := truncateToDate(time.Now())
	orgText := fmt.Sprintf("* NEXT Draft the doc\n  DEADLINE: <%s>\n", ts(now))
	ws := agendaFixture(t, orgText)
	m := New(ws)
	m.switchToView(agendaView)
	m.cursor = 1
	m = sendKey(m, "m")
	m = sendKey(m, "a")

	m.width, m.height = 100, len(m.rows)+m.pinnedHeaderHeight()+3
	out := stripANSI(m.View())
	if !strings.Contains(out, "Active marks:") || !strings.Contains(out, "Draft the doc") {
		t.Errorf("View() in agenda view missing the pinned marks header:\n%s", out)
	}
}

func TestAgendaSectionHeaderIsFlushLeftUnlikeItems(t *testing.T) {
	now := truncateToDate(time.Now())
	orgText := fmt.Sprintf("* NEXT Draft the doc\n  DEADLINE: <%s>\n", ts(now))
	ws := agendaFixture(t, orgText)
	m := New(ws)
	m.switchToView(agendaView)

	header := m.renderRow(m.rows[0])
	item := m.renderRow(m.rows[1])

	if strings.HasPrefix(header, " ") {
		t.Errorf("section header = %q, want no leading indent/gutter", header)
	}
	if !strings.HasPrefix(item, " ") {
		t.Errorf("item row = %q, want it still indented past the gutter", item)
	}
}

func TestAgendaViewInsertsBlankLineBetweenSectionsButNotBeforeTheFirst(t *testing.T) {
	now := truncateToDate(time.Now())
	orgText := fmt.Sprintf(`* TODO Overdue item
  DEADLINE: <%s>
* TODO Upcoming item
  DEADLINE: <%s>
`, ts(now.AddDate(0, 0, -1)), ts(now.AddDate(0, 0, 1)))
	ws := agendaFixture(t, orgText)
	m := New(ws)
	m.switchToView(agendaView)
	m.width, m.height = 100, len(m.rows)+m.sectionSeparatorBudget()+3

	out := stripANSI(m.View())
	lines := strings.Split(out, "\n")

	if strings.TrimRight(lines[0], " ") != "Overdue" {
		t.Fatalf("line 0 = %q, want the first section header (plus cursor-highlight padding) with no blank line before it", lines[0])
	}
	if lines[1] == "" {
		t.Errorf("unexpected blank line right after the first section header")
	}

	blankBeforeUpcoming := -1
	for i, l := range lines {
		if l == "Upcoming" {
			blankBeforeUpcoming = i - 1
			break
		}
	}
	if blankBeforeUpcoming < 0 {
		t.Fatalf("could not find the 'Upcoming' header in:\n%s", out)
	}
	if lines[blankBeforeUpcoming] != "" {
		t.Errorf("line before 'Upcoming' = %q, want a blank separator line", lines[blankBeforeUpcoming])
	}
}

func TestSectionSeparatorBudgetZeroInOutlineView(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	if got := m.sectionSeparatorBudget(); got != 0 {
		t.Errorf("sectionSeparatorBudget in outline view = %d, want 0", got)
	}
}

func TestSectionSeparatorBudgetMatchesSectionCountMinusOne(t *testing.T) {
	now := truncateToDate(time.Now())
	orgText := fmt.Sprintf(`* TODO Overdue item
  DEADLINE: <%s>
* TODO Upcoming item
  DEADLINE: <%s>
`, ts(now.AddDate(0, 0, -1)), ts(now.AddDate(0, 0, 1)))
	ws := agendaFixture(t, orgText)
	m := New(ws)
	m.switchToView(agendaView)

	if got := m.sectionSeparatorBudget(); got != 1 {
		t.Errorf("sectionSeparatorBudget with 2 sections = %d, want 1", got)
	}
}

func TestSwitchToAgendaAndBackViaCommands(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")

	m = sendKey(m, ":")
	m = typeKeys(m, "agenda")
	m, _ = sendKeyCmd(m, "enter")

	if m.view != agendaView {
		t.Fatalf("view after :agenda = %v, want agendaView", m.view)
	}
	if m.cursor != 0 {
		t.Errorf("cursor after switching to agenda = %d, want 0", m.cursor)
	}

	m = sendKey(m, ":")
	m = typeKeys(m, "outline")
	m, _ = sendKeyCmd(m, "enter")

	if m.view != outlineView {
		t.Fatalf("view after :outline = %v, want outlineView", m.view)
	}
}

func TestAgendaNavigationBraceJumpsBetweenSections(t *testing.T) {
	now := truncateToDate(time.Now())
	orgText := fmt.Sprintf(`* TODO Overdue item
  DEADLINE: <%s>
* TODO Upcoming item
  DEADLINE: <%s>
`, ts(now.AddDate(0, 0, -1)), ts(now.AddDate(0, 0, 1)))
	ws := agendaFixture(t, orgText)
	m := New(ws)
	m.switchToView(agendaView)

	// rows: 0=Overdue header, 1=Overdue item, 2=Upcoming header, 3=Upcoming item
	m.cursor = 1
	m = sendKey(m, "}")
	if m.rows[m.cursor].section != "Upcoming" {
		t.Fatalf("} from an overdue item landed on row %d (%+v), want the 'Upcoming' section header", m.cursor, m.rows[m.cursor])
	}

	m = sendKey(m, "{")
	if m.rows[m.cursor].section != "Overdue" {
		t.Fatalf("{ from the Upcoming header landed on row %d (%+v), want the 'Overdue' section header", m.cursor, m.rows[m.cursor])
	}
}

func TestAgendaCaretAndDollarStayWithinSection(t *testing.T) {
	now := truncateToDate(time.Now())
	orgText := fmt.Sprintf(`* TODO First upcoming
  DEADLINE: <%s>
* TODO Second upcoming
  DEADLINE: <%s>
`, ts(now.AddDate(0, 0, 1)), ts(now.AddDate(0, 0, 2)))
	ws := agendaFixture(t, orgText)
	m := New(ws)
	m.switchToView(agendaView)

	m.cursor = 2 // "Second upcoming"
	m = sendKey(m, "^")
	if m.rows[m.cursor].section != "Upcoming" {
		t.Fatalf("^ landed on row %+v, want the section header", m.rows[m.cursor])
	}

	m = sendKey(m, "$")
	if got := m.currentHeadline(); got == nil || got.Title != "Second upcoming" {
		t.Fatalf("$ landed on %v, want the section's last item", got)
	}
}

func TestEnterJumpsToSourceFromAgenda(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	outlineIdx := findRow(t, m, "Follow up with finance about the Q3 budget doc")
	h := m.rows[outlineIdx].headline
	if h.Deadline == nil {
		t.Fatalf("fixture assumption broken: expected an existing deadline")
	}

	m.switchToView(agendaView)
	agendaIdx := -1
	for i, r := range m.rows {
		if r.headline == h {
			agendaIdx = i
			break
		}
	}
	if agendaIdx < 0 {
		t.Fatalf("expected to find the deadline item in the agenda")
	}
	m.cursor = agendaIdx

	m = sendKey(m, "enter")
	if m.view != outlineView {
		t.Fatalf("view after enter = %v, want outlineView", m.view)
	}
	if m.currentHeadline() != h {
		t.Errorf("cursor after enter = %v, want the same headline %v", m.currentHeadline(), h)
	}
}

func TestEnterNoopOnAgendaSectionHeaderRow(t *testing.T) {
	now := truncateToDate(time.Now())
	ws := agendaFixture(t, fmt.Sprintf("* TODO Item\n  DEADLINE: <%s>\n", ts(now)))
	m := New(ws)
	m.switchToView(agendaView)
	m.cursor = 0 // the section header row

	m = sendKey(m, "enter")
	if m.view != agendaView {
		t.Errorf("enter on a section header switched view to %v, want to stay in agendaView", m.view)
	}
}

func TestAgendaStatusChangeMutatesSameHeadlineAsOutline(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	outlineIdx := findRow(t, m, "Follow up with finance about the Q3 budget doc")
	h := m.rows[outlineIdx].headline
	origKeyword := h.Keyword

	m.switchToView(agendaView)
	agendaIdx := -1
	for i, r := range m.rows {
		if r.headline == h {
			agendaIdx = i
			break
		}
	}
	if agendaIdx < 0 {
		t.Fatalf("expected to find the item in the agenda")
	}
	m.cursor = agendaIdx
	m = setStatus(m, "n") // TODO -> NEXT

	if h.Keyword == origKeyword {
		t.Fatalf("status change in agenda view did not change the keyword")
	}

	m.switchToView(outlineView)
	if m.rows[outlineIdx].headline.Keyword != h.Keyword {
		t.Errorf("outline view doesn't reflect the agenda-made change")
	}
}

func TestAgendaEmptyShowsFriendlyMessage(t *testing.T) {
	ws := agendaFixture(t, "* TODO Nothing due\n")
	m := New(ws)
	m.switchToView(agendaView)

	out := m.View()
	if !strings.Contains(out, "Nothing due") {
		t.Errorf("View() = %q, want a friendly empty-agenda message", out)
	}
}

// TestAgendaEmptyStillShowsStatusAndCommandLines guards a real bug: an
// empty agenda used to return a bare one-line message and skip the
// status/command-line area entirely (see View's len(m.rows) == 0
// branch), so the bottom bar simply vanished whenever nothing was due.
func TestAgendaEmptyStillShowsStatusAndCommandLines(t *testing.T) {
	ws := agendaFixture(t, "* TODO Nothing due\n")
	m := New(ws)
	m.width, m.height = 100, 20
	m.switchToView(agendaView)

	out := m.View()
	lines := strings.Split(out, "\n")
	if len(lines) != m.height {
		t.Fatalf("got %d lines, want %d (height)\n---\n%s", len(lines), m.height, out)
	}

	status := lines[len(lines)-2]
	if !strings.Contains(status, "agenda") {
		t.Errorf("status line = %q, want it to show the agenda place", status)
	}
	command := lines[len(lines)-1]
	if command != "" {
		t.Errorf("command line = %q, want blank (idle)", command)
	}
}

// TestNoOrgFilesStillShowsStatusAndCommandLines is the same bug's other
// branch: a workspace with no org files at all.
func TestNoOrgFilesStillShowsStatusAndCommandLines(t *testing.T) {
	ws := &workspace.Workspace{Dir: "empty-fixture"}
	m := New(ws)
	m.width, m.height = 100, 20

	out := m.View()
	lines := strings.Split(out, "\n")
	if len(lines) != m.height {
		t.Fatalf("got %d lines, want %d (height)\n---\n%s", len(lines), m.height, out)
	}

	status := lines[len(lines)-2]
	if !strings.Contains(status, "item") {
		t.Errorf("status line = %q, want the usual item count", status)
	}
	command := lines[len(lines)-1]
	if command != "" {
		t.Errorf("command line = %q, want blank (idle)", command)
	}
}

func TestWithAgendaDaysOption(t *testing.T) {
	ws := agendaFixture(t, "* TODO x\n")
	m := New(ws, WithAgendaDays(30))
	if m.agendaDays != 30 {
		t.Errorf("agendaDays = %d, want 30", m.agendaDays)
	}
}

func TestWithAgendaDaysZeroOrNegativeKeepsDefault(t *testing.T) {
	ws := agendaFixture(t, "* TODO x\n")
	m := New(ws, WithAgendaDays(0))
	if m.agendaDays != 14 {
		t.Errorf("agendaDays = %d, want the default 14", m.agendaDays)
	}
}

// recurringCalendarEventHeadline builds a calendar.org-shaped headline
// for one occurrence of a recurring meeting — same shape :sync-calendar
// writes for a recurring series instance (internal/calendarsync/convert.go)
// — linking it to recurID via GCAL_RECURRING_EVENT_ID. See calendarEventHeadline (in
// capture_test.go) for the one-off-event equivalent this builds on.
func recurringCalendarEventHeadline(id, recurID, title string, start, end time.Time) *org.Headline {
	h := calendarEventHeadline(id, start, end)
	h.Title = title
	h.Tags = []string{"recurring"}
	h.SetProperty("GCAL_RECURRING_EVENT_ID", recurID)
	return h
}

// oneOffCalendarEventHeadline is calendarEventHeadline with a custom
// title, for readable one-off-event fixtures alongside
// recurringCalendarEventHeadline above (calendarEventHeadline's own
// default "Meeting <id>" title is fine when the title itself doesn't
// matter to the test).
func oneOffCalendarEventHeadline(id, title string, start, end time.Time) *org.Headline {
	h := calendarEventHeadline(id, start, end)
	h.Title = title
	return h
}

// linkedToRecurringMeetings builds a headline carrying a
// GCAL_RECURRING_EVENT_IDS property (set by "gM" — see
// buildMeetingAttachAction) naming every recurID given, as if it had
// been attached to a past occurrence of each.
func linkedToRecurringMeetings(title string, recurIDs ...string) *org.Headline {
	h := &org.Headline{Level: 1, Title: title}
	h.SetProperty("GCAL_RECURRING_EVENT_IDS", strings.Join(recurIDs, " "))
	return h
}

// linkedToOneOffMeetings is linkedToRecurringMeetings' one-off-event
// counterpart: a GCAL_EVENT_IDS property naming every eventID given.
func linkedToOneOffMeetings(title string, eventIDs ...string) *org.Headline {
	h := &org.Headline{Level: 1, Title: title}
	h.SetProperty("GCAL_EVENT_IDS", strings.Join(eventIDs, " "))
	return h
}

// meetingsFixture builds a workspace directly from the given files —
// unlike agendaFixture (single file, org text), the Meetings section's
// inputs (calendar events with RFC3339 properties, entries with
// GCAL_RECURRING_EVENT_IDS) are easiest to build as Go structs (see
// recurringCalendarEventHeadline/linkedToRecurringMeetings above), and
// realistically span at least two files (calendar.org plus wherever the
// linked entries actually live).
func meetingsFixture(files ...*org.File) *workspace.Workspace {
	return &workspace.Workspace{Dir: "meetings-fixture", Files: files}
}

func TestMeetingsSectionGroupsLinkedItemUnderMeetingHeader(t *testing.T) {
	now := time.Now()
	meeting := recurringCalendarEventHeadline("instance-1", "series-abc", "Weekly Standup", now.Add(time.Hour), now.Add(90*time.Minute))
	item := linkedToRecurringMeetings("Follow up on last week's blocker", "series-abc")
	ws := meetingsFixture(
		&org.File{Path: "calendar.org", Headlines: []*org.Headline{meeting}},
		&org.File{Path: "projects.org", Headlines: []*org.Headline{item}},
	)
	m := New(ws)
	m.switchToView(agendaView)

	var sectionIdx, headerIdx, itemIdx = -1, -1, -1
	for i, r := range m.rows {
		switch {
		case r.section == "Meetings":
			sectionIdx = i
		case r.isMeetingHeader && r.meetingTitle == "Weekly Standup":
			headerIdx = i
		case r.headline == item:
			itemIdx = i
		}
	}
	if sectionIdx == -1 {
		t.Fatalf("no Meetings section row; rows: %+v", m.rows)
	}
	if headerIdx == -1 {
		t.Fatalf("no meeting header row for Weekly Standup; rows: %+v", m.rows)
	}
	if itemIdx == -1 {
		t.Fatalf("linked item not shown in agenda; rows: %+v", m.rows)
	}
	if !(sectionIdx < headerIdx && headerIdx < itemIdx) {
		t.Errorf("row order = section=%d header=%d item=%d, want section < header < item", sectionIdx, headerIdx, itemIdx)
	}
	if got, want := m.rows[sectionIdx].level, 0; got != want {
		t.Errorf("section level = %d, want %d", got, want)
	}
	if got, want := m.rows[headerIdx].level, 1; got != want {
		t.Errorf("meeting header level = %d, want %d", got, want)
	}
	if got, want := m.rows[itemIdx].level, 2; got != want {
		t.Errorf("item level = %d, want %d", got, want)
	}
	if !m.rows[itemIdx].isAgendaItem {
		t.Errorf("linked item row isAgendaItem = false, want true")
	}
}

func TestMeetingsSectionOmitsOneOffMeetingWithNoLinkedItems(t *testing.T) {
	now := time.Now()
	oneOff := calendarEventHeadline("one-off", now.Add(time.Hour), now.Add(90*time.Minute))
	ws := meetingsFixture(&org.File{Path: "calendar.org", Headlines: []*org.Headline{oneOff}})
	m := New(ws)
	m.switchToView(agendaView)

	for _, r := range m.rows {
		if r.section == "Meetings" {
			t.Fatalf("Meetings section present for a one-off meeting with nothing linked to it: %+v", m.rows)
		}
	}
}

// TestMeetingsSectionIncludesOneOffMeetingWithLinkedItem covers
// attaching an entry to a one-time (non-recurring) calendar event via
// "gM" (see linkedToOneOffMeetings/buildMeetingAttachAction): unlike a
// recurring series, a one-off event only ever has the one occurrence,
// but it's otherwise treated exactly like a recurring meeting's
// Meetings-section entry — a header row grouping its linked items.
func TestMeetingsSectionIncludesOneOffMeetingWithLinkedItem(t *testing.T) {
	now := time.Now()
	meeting := oneOffCalendarEventHeadline("kickoff-1", "Client Kickoff", now.Add(time.Hour), now.Add(2*time.Hour))
	item := linkedToOneOffMeetings("Bring the proposal deck", "kickoff-1")
	ws := meetingsFixture(
		&org.File{Path: "calendar.org", Headlines: []*org.Headline{meeting}},
		&org.File{Path: "projects.org", Headlines: []*org.Headline{item}},
	)
	m := New(ws)
	m.switchToView(agendaView)

	var headerIdx, itemIdx = -1, -1
	for i, r := range m.rows {
		switch {
		case r.isMeetingHeader && r.meetingTitle == "Client Kickoff":
			headerIdx = i
		case r.headline == item:
			itemIdx = i
		}
	}
	if headerIdx == -1 {
		t.Fatalf("no meeting header row for Client Kickoff; rows: %+v", m.rows)
	}
	if itemIdx == -1 || itemIdx < headerIdx {
		t.Fatalf("linked item not shown grouped under its one-off meeting; rows: %+v", m.rows)
	}
}

func TestMeetingsSectionOmitsRecurringMeetingWithNoLinkedItems(t *testing.T) {
	now := time.Now()
	meeting := recurringCalendarEventHeadline("instance-1", "series-abc", "Weekly Standup", now.Add(time.Hour), now.Add(90*time.Minute))
	ws := meetingsFixture(&org.File{Path: "calendar.org", Headlines: []*org.Headline{meeting}})
	m := New(ws)
	m.switchToView(agendaView)

	for _, r := range m.rows {
		if r.section == "Meetings" {
			t.Fatalf("Meetings section present for a recurring meeting with nothing linked to it: %+v", m.rows)
		}
	}
}

func TestMeetingsSectionExcludesMeetingsOutsideWindow(t *testing.T) {
	now := time.Now()
	tooLate := recurringCalendarEventHeadline("far-future", "series-far", "Far future meeting", now.Add(25*time.Hour), now.Add(26*time.Hour))
	tooEarly := recurringCalendarEventHeadline("yesterday", "series-early", "Yesterday's meeting", truncateToDate(now).Add(-time.Hour), truncateToDate(now).Add(-30*time.Minute))
	linkedFar := linkedToRecurringMeetings("Item for far-future series", "series-far")
	linkedEarly := linkedToRecurringMeetings("Item for early series", "series-early")
	ws := meetingsFixture(
		&org.File{Path: "calendar.org", Headlines: []*org.Headline{tooLate, tooEarly}},
		&org.File{Path: "projects.org", Headlines: []*org.Headline{linkedFar, linkedEarly}},
	)
	m := New(ws)
	m.switchToView(agendaView)

	for _, r := range m.rows {
		if r.section == "Meetings" {
			t.Fatalf("Meetings section present for meetings outside the window: %+v", m.rows)
		}
	}
}

func TestMeetingsSectionIncludesMeetingAlreadyInProgressToday(t *testing.T) {
	now := time.Now()
	startOfToday := truncateToDate(now)
	earlierToday := startOfToday.Add(time.Since(startOfToday) / 2) // safely between midnight and now
	meeting := recurringCalendarEventHeadline("instance-1", "series-abc", "Morning Sync", earlierToday, earlierToday.Add(30*time.Minute))
	item := linkedToRecurringMeetings("Old business", "series-abc")
	ws := meetingsFixture(
		&org.File{Path: "calendar.org", Headlines: []*org.Headline{meeting}},
		&org.File{Path: "projects.org", Headlines: []*org.Headline{item}},
	)
	m := New(ws)
	m.switchToView(agendaView)

	found := false
	for _, r := range m.rows {
		if r.isMeetingHeader && r.meetingTitle == "Morning Sync" {
			found = true
		}
	}
	if !found {
		t.Errorf("a meeting that already started today should still appear; rows: %+v", m.rows)
	}
}

func TestMeetingsSectionChronologicalOrder(t *testing.T) {
	now := time.Now()
	later := recurringCalendarEventHeadline("later", "series-later", "Later Meeting", now.Add(10*time.Hour), now.Add(11*time.Hour))
	sooner := recurringCalendarEventHeadline("sooner", "series-sooner", "Sooner Meeting", now.Add(time.Hour), now.Add(2*time.Hour))
	itemLater := linkedToRecurringMeetings("Item for later meeting", "series-later")
	itemSooner := linkedToRecurringMeetings("Item for sooner meeting", "series-sooner")
	ws := meetingsFixture(
		&org.File{Path: "calendar.org", Headlines: []*org.Headline{later, sooner}},
		&org.File{Path: "projects.org", Headlines: []*org.Headline{itemLater, itemSooner}},
	)
	m := New(ws)
	m.switchToView(agendaView)

	var headers []string
	for _, r := range m.rows {
		if r.isMeetingHeader {
			headers = append(headers, r.meetingTitle)
		}
	}
	want := []string{"Sooner Meeting", "Later Meeting"}
	if len(headers) != len(want) {
		t.Fatalf("meeting headers = %v, want %v", headers, want)
	}
	for i := range want {
		if headers[i] != want[i] {
			t.Errorf("headers[%d] = %q, want %q", i, headers[i], want[i])
		}
	}
}

func TestMeetingsSectionItemLinkedToMultipleMeetingsAppearsInEachGroup(t *testing.T) {
	now := time.Now()
	first := recurringCalendarEventHeadline("first", "series-first", "First Meeting", now.Add(time.Hour), now.Add(2*time.Hour))
	second := recurringCalendarEventHeadline("second", "series-second", "Second Meeting", now.Add(3*time.Hour), now.Add(4*time.Hour))
	shared := linkedToRecurringMeetings("Cross-cutting item", "series-first", "series-second")
	ws := meetingsFixture(
		&org.File{Path: "calendar.org", Headlines: []*org.Headline{first, second}},
		&org.File{Path: "projects.org", Headlines: []*org.Headline{shared}},
	)
	m := New(ws)
	m.switchToView(agendaView)

	count := 0
	for _, r := range m.rows {
		if r.headline == shared {
			count++
		}
	}
	if count != 2 {
		t.Errorf("shared item appears %d times, want 2 (once per linked meeting)", count)
	}
}

func TestMeetingsSectionExcludesDoneAndCancelledLinkedItems(t *testing.T) {
	now := time.Now()
	meeting := recurringCalendarEventHeadline("instance-1", "series-abc", "Weekly Standup", now.Add(time.Hour), now.Add(2*time.Hour))
	done := linkedToRecurringMeetings("Already handled", "series-abc")
	done.Keyword = "DONE"
	cancelled := linkedToRecurringMeetings("Dropped", "series-abc")
	cancelled.Keyword = "CANCELLED"
	pending := linkedToRecurringMeetings("Still open", "series-abc")
	ws := meetingsFixture(
		&org.File{Path: "calendar.org", Headlines: []*org.Headline{meeting}},
		&org.File{Path: "projects.org", Headlines: []*org.Headline{done, cancelled, pending}},
	)
	m := New(ws)
	m.switchToView(agendaView)

	var shown []string
	for _, r := range m.rows {
		if r.isAgendaItem && r.headline != nil {
			shown = append(shown, r.headline.Title)
		}
	}
	if len(shown) != 1 || shown[0] != "Still open" {
		t.Errorf("items shown under meeting = %v, want only [\"Still open\"]", shown)
	}
}

func TestMeetingHeaderRenderShowsTitleAndTime(t *testing.T) {
	now := time.Now()
	meeting := recurringCalendarEventHeadline("instance-1", "series-abc", "Weekly Standup", now.Add(time.Hour), now.Add(90*time.Minute))
	item := linkedToRecurringMeetings("Follow up item", "series-abc")
	ws := meetingsFixture(
		&org.File{Path: "calendar.org", Headlines: []*org.Headline{meeting}},
		&org.File{Path: "projects.org", Headlines: []*org.Headline{item}},
	)
	m := New(ws)
	m.switchToView(agendaView)

	var rendered string
	for _, r := range m.rows {
		if r.isMeetingHeader {
			rendered = stripANSI(m.renderRow(r))
		}
	}
	if rendered == "" {
		t.Fatalf("no meeting header row rendered")
	}
	if !strings.Contains(rendered, "Weekly Standup") {
		t.Errorf("rendered header = %q, want it to contain the meeting title", rendered)
	}
	if !strings.Contains(rendered, now.Add(time.Hour).Local().Format("15:04")) {
		t.Errorf("rendered header = %q, want it to contain the start time", rendered)
	}
}

func TestSearchFindsMeetingHeaderByTitle(t *testing.T) {
	now := time.Now()
	meeting := recurringCalendarEventHeadline("instance-1", "series-abc", "Weekly Standup Zzyzx", now.Add(time.Hour), now.Add(90*time.Minute))
	item := linkedToRecurringMeetings("Follow up item", "series-abc")
	ws := meetingsFixture(
		&org.File{Path: "calendar.org", Headlines: []*org.Headline{meeting}},
		&org.File{Path: "projects.org", Headlines: []*org.Headline{item}},
	)
	m := New(ws)
	m.switchToView(agendaView)

	m = sendKey(m, "/")
	m = typeKeys(m, "Zzyzx")

	if !m.rows[m.cursor].isMeetingHeader || m.rows[m.cursor].meetingTitle != "Weekly Standup Zzyzx" {
		t.Errorf("cursor landed on %+v, want the meeting header row", m.rows[m.cursor])
	}
}

func TestFormatMeetingWhenOmitsTimeForAllDayMeeting(t *testing.T) {
	start := time.Date(2026, 9, 20, 0, 0, 0, 0, time.Local)
	end := time.Date(2026, 9, 21, 0, 0, 0, 0, time.Local)
	got := formatMeetingWhen(start, end)
	if strings.Contains(got, ":") {
		t.Errorf("formatMeetingWhen(all-day) = %q, want no time-of-day", got)
	}
	if !strings.Contains(got, "2026-09-20") {
		t.Errorf("formatMeetingWhen(all-day) = %q, want the date", got)
	}
}

func TestFormatMeetingWhenShowsRangeForTimedMeeting(t *testing.T) {
	start := time.Date(2026, 9, 20, 14, 0, 0, 0, time.Local)
	end := time.Date(2026, 9, 20, 15, 0, 0, 0, time.Local)
	got := formatMeetingWhen(start, end)
	if !strings.Contains(got, "14:00") || !strings.Contains(got, "15:00") {
		t.Errorf("formatMeetingWhen(timed) = %q, want both start and end times", got)
	}
}
