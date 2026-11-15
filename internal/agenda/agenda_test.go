package agenda

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sburnett/orgtd/internal/config"
	"github.com/sburnett/orgtd/internal/org"
	"github.com/sburnett/orgtd/internal/orgdate"
	"github.com/sburnett/orgtd/internal/workspace"
)

func ts(t time.Time) string { return t.Format("2006-01-02 Mon") }

func cfg14() config.Config { return config.Default() }

func agendaFixture(t *testing.T, orgText string) *workspace.Workspace {
	t.Helper()
	f, err := org.Parse(strings.NewReader(orgText), "agenda.org")
	if err != nil {
		t.Fatalf("org.Parse: %v", err)
	}
	return &workspace.Workspace{Dir: "agenda-fixture", Files: []*org.File{f}}
}

func TestAgendaSectionBucketing(t *testing.T) {
	today := orgdate.TruncateToDate(time.Now())
	cases := []struct {
		date time.Time
		want string
	}{
		{today.AddDate(0, 0, -1), "Overdue"},
		{today, "Due Today"},
		{today.AddDate(0, 0, 1), "Upcoming"},
	}
	for _, c := range cases {
		if got := Section(c.date, today); got != c.want {
			t.Errorf("Section(%v) = %q, want %q", c.date, got, c.want)
		}
	}
}

func TestAgendaEntriesExcludesDoneCancelledAndSomeday(t *testing.T) {
	now := orgdate.TruncateToDate(time.Now())
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

	entries := Entries(ws, cfg14(), now)
	if len(entries) != 1 {
		t.Fatalf("agendaEntries = %d entries, want 1 (only the TODO): %+v", len(entries), entries)
	}
	if entries[0].Headline.Title != "Still relevant" {
		t.Errorf("entries[0].Headline.Title = %q, want %q", entries[0].Headline.Title, "Still relevant")
	}
}

func TestAgendaEntriesSomedayExcludedEvenWithinWindow(t *testing.T) {
	now := orgdate.TruncateToDate(time.Now())
	orgText := fmt.Sprintf("* SOMEDAY Not committed\n  SCHEDULED: <%s>\n", ts(now.AddDate(0, 0, 2)))
	ws := agendaFixture(t, orgText)

	if entries := Entries(ws, cfg14(), now); len(entries) != 0 {
		t.Errorf("agendaEntries = %+v, want none (SOMEDAY always excluded)", entries)
	}
}

func TestAgendaEntriesDualDateProducesTwoEntries(t *testing.T) {
	now := orgdate.TruncateToDate(time.Now())
	scheduled := now.AddDate(0, 0, 3)
	deadline := now.AddDate(0, 0, 5)
	orgText := fmt.Sprintf("* NEXT Both dates\n  SCHEDULED: <%s> DEADLINE: <%s>\n", ts(scheduled), ts(deadline))
	ws := agendaFixture(t, orgText)

	entries := Entries(ws, cfg14(), now)
	if len(entries) != 2 {
		t.Fatalf("agendaEntries = %d entries, want 2 (one per date): %+v", len(entries), entries)
	}
	labels := map[string]time.Time{entries[0].Label: entries[0].Date, entries[1].Label: entries[1].Date}
	if !labels["Scheduled"].Equal(scheduled) {
		t.Errorf("Scheduled entry date = %v, want %v", labels["Scheduled"], scheduled)
	}
	if !labels["Deadline"].Equal(deadline) {
		t.Errorf("Deadline entry date = %v, want %v", labels["Deadline"], deadline)
	}
}

func TestAgendaEntriesExcludesBeyondWindow(t *testing.T) {
	now := orgdate.TruncateToDate(time.Now())
	orgText := fmt.Sprintf("* TODO Just inside\n  DEADLINE: <%s>\n* TODO Just outside\n  DEADLINE: <%s>\n",
		ts(now.AddDate(0, 0, 14)), ts(now.AddDate(0, 0, 15)))
	ws := agendaFixture(t, orgText)

	entries := Entries(ws, cfg14(), now)
	if len(entries) != 1 || entries[0].Headline.Title != "Just inside" {
		t.Fatalf("agendaEntries = %+v, want just 'Just inside'", entries)
	}
}

func TestAgendaEntriesRecurringPastDueShowsAsOverdueWithMissedCount(t *testing.T) {
	now := orgdate.TruncateToDate(time.Now())
	// Scheduled 17 days ago, weekly: current occurrence is 3 days ago
	// (occurrences at -17, -10, -3; +4 hasn't arrived), 2 missed on top.
	orgText := fmt.Sprintf("* TODO Weekly standup\n  SCHEDULED: <%s +1w>\n", ts(now.AddDate(0, 0, -17)))
	ws := agendaFixture(t, orgText)

	entries := Entries(ws, cfg14(), now)
	if len(entries) != 1 {
		t.Fatalf("agendaEntries = %d entries, want 1: %+v", len(entries), entries)
	}
	want := now.AddDate(0, 0, -3)
	if !entries[0].Date.Equal(want) {
		t.Errorf("entries[0].Date = %v, want current occurrence %v", entries[0].Date, want)
	}
	if entries[0].Repeater != "+1w" {
		t.Errorf("entries[0].Repeater = %q, want %q", entries[0].Repeater, "+1w")
	}
	if entries[0].Missed != 2 {
		t.Errorf("entries[0].Missed = %d, want 2", entries[0].Missed)
	}
	if got := Section(entries[0].Date, now); got != "Overdue" {
		t.Errorf("Section(current occurrence) = %q, want %q (a skipped recurring item stays visible)", got, "Overdue")
	}
}

func TestAgendaEntriesExcludesUnparseableDate(t *testing.T) {
	ws := agendaFixture(t, "* TODO Weird date\n  DEADLINE: <not-a-real-date>\n")

	if entries := Entries(ws, cfg14(), orgdate.TruncateToDate(time.Now())); len(entries) != 0 {
		t.Errorf("agendaEntries = %+v, want none (unparseable date)", entries)
	}
}

func TestCustomKeywordsDriveAgenda(t *testing.T) {
	org.SetKeywords([]string{"TRIAGE", "LATER", "GO"}, []string{"SHIPPED"})
	t.Cleanup(func() { org.SetKeywords(org.ActiveKeywords, org.DoneKeywords) })

	now := orgdate.TruncateToDate(time.Now())
	ws := agendaFixture(t, fmt.Sprintf(`* TRIAGE Sort it
  DEADLINE: <%[1]s>
* LATER Park it
  DEADLINE: <%[1]s>
* SHIPPED Done it
  DEADLINE: <%[1]s>
* GO Do it
`, ts(now)))
	cfg := config.Default()
	cfg.Keywords = []config.KeywordConfig{
		{Name: "TRIAGE", Shortcut: "t"},
		{Name: "LATER", Shortcut: "l", Agenda: config.AgendaSkip},
		{Name: "GO", Shortcut: "g", Agenda: config.AgendaNext},
		{Name: "SHIPPED", Shortcut: "x", Done: true},
	}
	if entries := Entries(ws, cfg, now); len(entries) != 1 || entries[0].Headline.Title != "Sort it" {
		t.Errorf("Entries = %+v, want only TRIAGE", entries)
	}
	if next := NextActionHeadlines(ws, cfg); len(next) != 1 || next[0].Title != "Do it" {
		t.Errorf("NextActionHeadlines = %+v, want only GO", next)
	}
}

func TestBySectionSortsByDateAndZeroesMissedOutsideOverdue(t *testing.T) {
	now := orgdate.TruncateToDate(time.Now())
	ws := agendaFixture(t, fmt.Sprintf("* TODO Later\n  DEADLINE: <%s>\n* TODO Sooner\n  DEADLINE: <%s>\n* TODO Late\n  DEADLINE: <%s>\n",
		ts(now.AddDate(0, 0, 5)), ts(now.AddDate(0, 0, 2)), ts(now.AddDate(0, 0, -1))))
	got := BySection(ws, config.Default(), now)
	up := got[Upcoming]
	if len(up) != 2 || up[0].Headline.Title != "Sooner" || up[1].Headline.Title != "Later" {
		t.Errorf("Upcoming = %+v, want Sooner then Later", up)
	}
	if len(got[Overdue]) != 1 || len(got[DueToday]) != 0 {
		t.Errorf("BySection = %+v", got)
	}
}
