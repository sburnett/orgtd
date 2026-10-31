package ui

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/sburnett/orgtd/internal/config"
	"github.com/sburnett/orgtd/internal/org"
)

// snippetsNow is a fixed clock: a Saturday afternoon.
var snippetsNow = time.Date(2026, 10, 31, 15, 0, 0, 0, time.Local)

func snippetsClock() time.Time { return snippetsNow }

// snippetsRowTexts flattens m.rows into section titles and entry titles.
func snippetsRowTexts(m Model) []string {
	var out []string
	for _, r := range m.rows {
		switch {
		case r.kind == rowSection:
			out = append(out, r.text)
		case r.headline != nil:
			out = append(out, "  "+r.headline.Title)
		}
	}
	return out
}

func TestSnippetsGroupsByDayNewestFirst(t *testing.T) {
	ws := agendaFixture(t, `* DONE Yesterday morning
  CLOSED: [2026-10-30 Fri 09:00]
* DONE Today early
  CLOSED: [2026-10-31 Sat 08:00]
* DONE Yesterday evening
  CLOSED: [2026-10-30 Fri 18:00]
* DONE Today late
  CLOSED: [2026-10-31 Sat 14:00]
`)
	m := New(ws, WithClock(snippetsClock))
	m.switchToView(snippetsView)

	want := []string{
		"2026-10-31 Sat", "  Today late", "  Today early",
		"2026-10-30 Fri", "  Yesterday evening", "  Yesterday morning",
	}
	if got := snippetsRowTexts(m); !reflect.DeepEqual(got, want) {
		t.Errorf("rows = %q, want %q", got, want)
	}
}

func TestSnippetsWindowIncludesTodayAndPreviousDays(t *testing.T) {
	ws := agendaFixture(t, `* DONE First day of the window
  CLOSED: [2026-10-25 Sun 00:00]
* DONE Just before the window
  CLOSED: [2026-10-24 Sat 23:59]
`)
	m := New(ws, WithClock(snippetsClock))
	m.switchToView(snippetsView)
	want := []string{"2026-10-25 Sun", "  First day of the window"}
	if got := snippetsRowTexts(m); !reflect.DeepEqual(got, want) {
		t.Errorf("default window: rows = %q, want %q", got, want)
	}

	m = New(ws, WithClock(snippetsClock), WithSnippetsDays(8))
	m.switchToView(snippetsView)
	if rowIndex(m, "Just before the window") < 0 {
		t.Errorf("an 8-day window should reach back to Oct 24: %q", snippetsRowTexts(m))
	}
}

func TestSnippetsWhatCountsAsCompleted(t *testing.T) {
	ws := agendaFixture(t, `* DONE Finished
  CLOSED: [2026-10-30 Fri 09:00]
* CANCELLED Dropped
  CLOSED: [2026-10-30 Fri 09:00]
* TODO Water plants
  SCHEDULED: <2026-11-02 Mon +1w>
  :PROPERTIES:
  :LAST_REPEAT: [2026-10-29 Thu 10:00]
  :END:
* TODO Old repeater
  SCHEDULED: <2026-11-02 Mon +1w>
  :PROPERTIES:
  :LAST_REPEAT: [2026-09-01 Tue 10:00]
  :END:
* TODO Reopened
  CLOSED: [2026-10-30 Fri 09:00]
* DONE Undated
`)
	m := New(ws, WithClock(snippetsClock))
	m.switchToView(snippetsView)
	want := []string{"2026-10-30 Fri", "  Finished", "2026-10-29 Thu", "  Water plants"}
	if got := snippetsRowTexts(m); !reflect.DeepEqual(got, want) {
		t.Errorf("rows = %q, want %q", got, want)
	}
}

func TestSnippetsListsNestedEntriesIndividually(t *testing.T) {
	ws := agendaFixture(t, `* DONE Project
  CLOSED: [2026-10-30 Fri 10:00]
** DONE Subtask
   CLOSED: [2026-10-30 Fri 09:00]
`)
	m := New(ws, WithClock(snippetsClock))
	m.switchToView(snippetsView)
	want := []string{"2026-10-30 Fri", "  Project", "  Subtask"}
	if got := snippetsRowTexts(m); !reflect.DeepEqual(got, want) {
		t.Errorf("rows = %q, want %q", got, want)
	}
}

func TestSnippetsSkipKeyword(t *testing.T) {
	org.SetKeywords([]string{"TODO"}, []string{"DONE", "ARCHIVED"})
	t.Cleanup(func() { org.SetKeywords(org.ActiveKeywords, org.DoneKeywords) })
	ws := agendaFixture(t, `* ARCHIVED Filed away
  CLOSED: [2026-10-30 Fri 09:00]
* DONE Finished
  CLOSED: [2026-10-30 Fri 09:00]
`)
	cfg := config.Default()
	cfg.Keywords = []config.KeywordConfig{
		{Name: "TODO", Shortcut: "t"},
		{Name: "DONE", Shortcut: "d", Done: true},
		{Name: "ARCHIVED", Shortcut: "a", Done: true, Snippets: config.SnippetsSkip},
	}
	m := New(ws, WithConfig(cfg), WithClock(snippetsClock))
	m.switchToView(snippetsView)
	want := []string{"2026-10-30 Fri", "  Finished"}
	if got := snippetsRowTexts(m); !reflect.DeepEqual(got, want) {
		t.Errorf("rows = %q, want %q", got, want)
	}
}

func TestSnippetsEnterRevealsEntryHiddenFromOutline(t *testing.T) {
	stale := snippetsNow.Add(-72 * time.Hour)
	ws := agendaFixture(t, fmt.Sprintf(`* DONE Project
  CLOSED: [%s]
** DONE Subtask
   CLOSED: [%s]
`, closedTS(stale), closedTS(stale)))
	m := New(ws, WithClock(snippetsClock), WithHideClosedAfterHours(24))
	m.switchToView(snippetsView)
	m.cursor = findRow(t, m, "Subtask")

	m.spec().enter(&m)

	if m.view != outlineView {
		t.Fatalf("view = %v, want the outline", m.view)
	}
	if h := m.currentHeadline(); h == nil || h.Title != "Subtask" {
		t.Errorf("cursor on %v, want the Subtask entry", h)
	}
	if m.message == "" {
		t.Error("turning stale-item hiding off should say so")
	}
}

func TestSnippetsEnterLeavesHidingAloneWhenEntryIsVisible(t *testing.T) {
	ws := agendaFixture(t, fmt.Sprintf(`* DONE Recent
  CLOSED: [%s]
`, closedTS(time.Now().Add(-time.Hour))))
	m := New(ws, WithClock(time.Now), WithHideClosedAfterHours(24))
	m.switchToView(snippetsView)
	m.cursor = findRow(t, m, "Recent")

	m.spec().enter(&m)

	if !m.hideClosedEnabled {
		t.Error("an entry the outline already shows shouldn't turn hiding off")
	}
}
