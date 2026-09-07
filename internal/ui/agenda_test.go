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
		got, ok := parseTimestampDate(c)
		if !ok {
			t.Errorf("parseTimestampDate(%q) did not match", c.Raw)
			continue
		}
		if !got.Equal(now) {
			t.Errorf("parseTimestampDate(%q) = %v, want %v", c.Raw, got, now)
		}
	}
}

func TestParseTimestampDateRejectsGarbageAndNil(t *testing.T) {
	if _, ok := parseTimestampDate(nil); ok {
		t.Errorf("parseTimestampDate(nil) matched")
	}
	if _, ok := parseTimestampDate(&org.Timestamp{Raw: "not a date"}); ok {
		t.Errorf("parseTimestampDate(garbage) matched")
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

	if lines[0] != "Overdue" {
		t.Fatalf("line 0 = %q, want the first section header with no blank line before it", lines[0])
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

func TestAgendaStatusRotateMutatesSameHeadlineAsOutline(t *testing.T) {
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
	m = sendKey(m, "r")

	if h.Keyword == origKeyword {
		t.Fatalf("status rotate in agenda view did not change the keyword")
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
