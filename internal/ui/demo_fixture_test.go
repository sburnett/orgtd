package ui

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sburnett/orgtd/internal/org"
	"github.com/sburnett/orgtd/internal/workspace"
)

// demoNow is a Tuesday morning inside the week testdata/orgdir's dates
// were written around.
var demoNow = time.Date(2026, 10, 6, 10, 0, 0, 0, time.Local)

// loadDemo loads testdata/orgdir read-only (nothing here writes or saves)
// as the realistic sample outline someone can point orgtd at by hand
// (orgtd --dir testdata/orgdir). The rest of the suite uses
// testdata/minimal instead.
func loadDemo(t *testing.T) *workspace.Workspace {
	t.Helper()
	ws, err := workspace.Load("../../testdata/orgdir")
	if err != nil {
		t.Fatalf("workspace.Load: %v", err)
	}
	return ws
}

func TestDemoFilesRoundTripExactly(t *testing.T) {
	for _, f := range loadDemo(t).Files {
		want, err := os.ReadFile(f.Path)
		if err != nil {
			t.Fatal(err)
		}
		if got := org.RenderFile(f); got != string(want) {
			t.Errorf("%s does not round-trip through parse/render (canonical formatting differs)", f.Path)
		}
	}
}

func TestDemoMeetingReferencesResolve(t *testing.T) {
	ws := loadDemo(t)
	events := map[string]bool{}
	recurring := map[string]bool{}
	for _, f := range ws.Files {
		if !strings.HasSuffix(f.Path, "calendar.org") {
			continue
		}
		org.Walk(f.Headlines, func(h *org.Headline) {
			events[h.Properties["GCAL_EVENT_ID"]] = true
			if id := h.Properties["GCAL_RECURRING_EVENT_ID"]; id != "" {
				recurring[id] = true
			}
		})
	}
	if len(events) == 0 {
		t.Fatal("no calendar events loaded")
	}
	for _, f := range ws.Files {
		org.Walk(f.Headlines, func(h *org.Headline) {
			for _, id := range strings.Fields(h.Properties["GCAL_EVENT_IDS"]) {
				if !events[id] {
					t.Errorf("%q: GCAL_EVENT_IDS names %s, not in calendar.org", h.Title, id)
				}
			}
			for _, id := range strings.Fields(h.Properties["GCAL_RECURRING_EVENT_IDS"]) {
				if !recurring[id] {
					t.Errorf("%q: GCAL_RECURRING_EVENT_IDS names %s, not in calendar.org", h.Title, id)
				}
			}
			if strings.HasSuffix(f.Path, "meeting-tags.org") && h.Title == "Planning cadence" {
				for _, id := range strings.Fields(h.Properties["MEETING_TAG_EVENT_IDS"]) {
					if !events[id] {
						t.Errorf("Planning cadence names %s, not in calendar.org", id)
					}
				}
			}
		})
	}
}

func TestDemoEveryViewHasContent(t *testing.T) {
	for name, v := range map[string]viewKind{
		"outline": outlineView, "agenda": agendaView, "calendar": calendarView,
		"meeting-tags": meetingTagsView, "tags": tagsView,
	} {
		m := New(loadDemo(t), fixedClock(demoNow))
		m.switchToView(v)
		if len(m.rows) < 5 {
			t.Errorf("%s view has only %d rows", name, len(m.rows))
		}
	}
}

func TestDemoOutlineHasDeepNestingAndHidesCalendar(t *testing.T) {
	m := New(loadDemo(t), fixedClock(demoNow))
	deepest := 0
	for _, r := range m.rows {
		if r.kind == rowHeadline && r.headline.Level > deepest {
			deepest = r.headline.Level
		}
		if r.kind == rowFile && strings.HasSuffix(r.file.Path, "calendar.org") {
			t.Errorf("calendar.org appears in the outline view")
		}
	}
	if deepest < 5 {
		t.Errorf("deepest headline level = %d, want >= 5 to exercise indent guides", deepest)
	}
}

func TestDemoAgendaPopulatesEverySection(t *testing.T) {
	m := New(loadDemo(t), fixedClock(demoNow))
	m.switchToView(agendaView)
	counts := map[string]int{}
	section := ""
	for _, r := range m.rows {
		switch {
		case r.kind == rowSection:
			section = r.text
		case r.headline != nil:
			counts[section]++
		}
	}
	for _, sec := range agendaSections {
		if counts[sec] == 0 {
			t.Errorf("agenda section %q is empty (counts: %v)", sec, counts)
		}
	}
}

func TestDemoCalendarNestsLinkedItemsUnderEvents(t *testing.T) {
	m := New(loadDemo(t), fixedClock(demoNow))
	m.switchToView(calendarView)
	linked := map[string]bool{}
	for _, r := range m.rows {
		if r.kind == rowCalendarLinked {
			linked[r.headline.Title] = true
		}
	}
	for _, title := range []string{
		"Finalize the versioning scheme decision", // attached by event ID
		"Prep for 1:1 with Dana",                  // attached to a recurring series
		"Write Marcus's mid-year review",          // linked by the @marcus tag
		"Draft the Q4 goals doc",                  // attached by event ID
		"Q4 planning",                             // linked via a meeting-tags record's :planning: tag
	} {
		if !linked[title] {
			t.Errorf("%q isn't nested under any calendar event", title)
		}
	}
}

func TestDemoMeetingTagsViewNestsEventsAndFlagsStaleRecord(t *testing.T) {
	m := New(loadDemo(t), fixedClock(demoNow))
	m.switchToView(meetingTagsView)
	var records []string
	nested := map[string]int{}
	cur := ""
	for _, r := range m.rows {
		switch r.kind {
		case rowMeetingTagsRecord:
			cur = r.headline.Title
			records = append(records, cur)
		case rowCalendarEvent:
			nested[cur]++
		}
	}
	if len(records) != 2 {
		t.Fatalf("records = %v, want 2", records)
	}
	if nested["Planning cadence"] < 2 {
		t.Errorf("Planning cadence has %d nested events, want its recurring series plus the kickoff", nested["Planning cadence"])
	}
	if nested["Incident follow-ups"] != 0 {
		t.Errorf("stale record 'Incident follow-ups' unexpectedly has %d nested events", nested["Incident follow-ups"])
	}
}
