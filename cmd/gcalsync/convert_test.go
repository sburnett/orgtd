package main

import (
	"strings"
	"testing"
	"time"

	"github.com/sburnett/orgtd/internal/gcal"
	"github.com/sburnett/orgtd/internal/org"
)

func TestBuildFileSortsByStartTime(t *testing.T) {
	events := []gcal.Event{
		{ID: "later", Summary: "Later", Start: mustParse(t, "2026-09-10T10:00:00-07:00"), End: mustParse(t, "2026-09-10T10:30:00-07:00")},
		{ID: "earlier", Summary: "Earlier", Start: mustParse(t, "2026-09-10T09:00:00-07:00"), End: mustParse(t, "2026-09-10T09:30:00-07:00")},
	}
	f := BuildFile("/tmp/calendar.org", events)
	if len(f.Headlines) != 2 {
		t.Fatalf("len(Headlines) = %d, want 2", len(f.Headlines))
	}
	if got := f.Headlines[0].Title; got != "Earlier" {
		t.Errorf("Headlines[0].Title = %q, want %q", got, "Earlier")
	}
	if got := f.Headlines[1].Title; got != "Later" {
		t.Errorf("Headlines[1].Title = %q, want %q", got, "Later")
	}
}

func TestBuildHeadlineHasNoKeywordOrScheduled(t *testing.T) {
	h := buildHeadline(gcal.Event{
		ID:         "abc123",
		CalendarID: "primary",
		Summary:    "Standup",
		Start:      mustParse(t, "2026-09-10T09:00:00-07:00"),
		End:        mustParse(t, "2026-09-10T09:15:00-07:00"),
	})
	if h.Keyword != "" {
		t.Errorf("Keyword = %q, want empty", h.Keyword)
	}
	if h.Scheduled != nil {
		t.Errorf("Scheduled = %v, want nil (must not feed orgtd's agenda)", h.Scheduled)
	}
	if h.Properties["GCAL_EVENT_ID"] != "abc123" {
		t.Errorf("GCAL_EVENT_ID = %q, want %q", h.Properties["GCAL_EVENT_ID"], "abc123")
	}
	if h.Properties["GCAL_CALENDAR_ID"] != "primary" {
		t.Errorf("GCAL_CALENDAR_ID = %q, want %q", h.Properties["GCAL_CALENDAR_ID"], "primary")
	}
	wantStart := mustParse(t, "2026-09-10T09:00:00-07:00").Format(time.RFC3339)
	if h.Properties["GCAL_START"] != wantStart {
		t.Errorf("GCAL_START = %q, want %q", h.Properties["GCAL_START"], wantStart)
	}
	wantEnd := mustParse(t, "2026-09-10T09:15:00-07:00").Format(time.RFC3339)
	if h.Properties["GCAL_END"] != wantEnd {
		t.Errorf("GCAL_END = %q, want %q", h.Properties["GCAL_END"], wantEnd)
	}
}

func TestEventBoundsAllDayUsesLocalMidnight(t *testing.T) {
	ev := gcal.Event{
		AllDay: true,
		Start:  time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC),
		End:    time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC),
	}
	start, end := eventBounds(ev)
	wantStart := time.Date(2026, 9, 12, 0, 0, 0, 0, time.Local)
	wantEnd := time.Date(2026, 9, 13, 0, 0, 0, 0, time.Local)
	if !start.Equal(wantStart) {
		t.Errorf("start = %v, want %v", start, wantStart)
	}
	if !end.Equal(wantEnd) {
		t.Errorf("end = %v, want %v", end, wantEnd)
	}
}

func TestBuildHeadlineRecordsRecurringEventID(t *testing.T) {
	h := buildHeadline(gcal.Event{
		ID:               "instance-1",
		RecurringEventID: "series-abc",
		Start:            mustParse(t, "2026-09-10T09:00:00-07:00"),
		End:              mustParse(t, "2026-09-10T09:15:00-07:00"),
	})
	if got := h.Properties["GCAL_RECURRING_EVENT_ID"]; got != "series-abc" {
		t.Errorf("GCAL_RECURRING_EVENT_ID = %q, want %q", got, "series-abc")
	}
	if len(h.Tags) != 1 || h.Tags[0] != "recurring" {
		t.Errorf("Tags = %v, want [recurring]", h.Tags)
	}
}

func TestBuildHeadlineOneOffEventHasNoRecurrenceMarkers(t *testing.T) {
	h := buildHeadline(gcal.Event{
		ID:    "one-off",
		Start: mustParse(t, "2026-09-10T09:00:00-07:00"),
		End:   mustParse(t, "2026-09-10T09:15:00-07:00"),
	})
	if _, ok := h.Properties["GCAL_RECURRING_EVENT_ID"]; ok {
		t.Errorf("GCAL_RECURRING_EVENT_ID = %q, want no such property", h.Properties["GCAL_RECURRING_EVENT_ID"])
	}
	if len(h.Tags) != 0 {
		t.Errorf("Tags = %v, want none", h.Tags)
	}
}

func TestBuildHeadlineEmptyTitleFallback(t *testing.T) {
	h := buildHeadline(gcal.Event{ID: "x", Start: mustParse(t, "2026-09-10T09:00:00-07:00"), End: mustParse(t, "2026-09-10T09:15:00-07:00")})
	if h.Title != "(no title)" {
		t.Errorf("Title = %q, want %q", h.Title, "(no title)")
	}
}

func TestBuildHeadlineIncludesLocationDescriptionAndLink(t *testing.T) {
	h := buildHeadline(gcal.Event{
		ID:          "abc123",
		Summary:     "Planning",
		Location:    "Room 5",
		Description: "Agenda:\nline two",
		HTMLLink:    "https://calendar.google.com/event?eid=abc123",
		Start:       mustParse(t, "2026-09-10T09:00:00-07:00"),
		End:         mustParse(t, "2026-09-10T09:15:00-07:00"),
	})
	body := strings.Join(h.Body, "\n")
	for _, want := range []string{"Location: Room 5", "Agenda:", "line two", "[[https://calendar.google.com/event?eid=abc123][Open in Google Calendar]]"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q; body:\n%s", want, body)
		}
	}
	if got := h.Properties["GCAL_HTML_LINK"]; got != "https://calendar.google.com/event?eid=abc123" {
		t.Errorf("GCAL_HTML_LINK = %q, want %q", got, "https://calendar.google.com/event?eid=abc123")
	}
}

func TestBuildHeadlineNoHTMLLinkOmitsProperty(t *testing.T) {
	h := buildHeadline(gcal.Event{
		ID:    "abc123",
		Start: mustParse(t, "2026-09-10T09:00:00-07:00"),
		End:   mustParse(t, "2026-09-10T09:15:00-07:00"),
	})
	if _, ok := h.Properties["GCAL_HTML_LINK"]; ok {
		t.Errorf("GCAL_HTML_LINK = %q, want no such property", h.Properties["GCAL_HTML_LINK"])
	}
}

func TestOrgTimestampTimedSameDay(t *testing.T) {
	ev := gcal.Event{Start: mustParse(t, "2026-09-10T09:00:00-07:00"), End: mustParse(t, "2026-09-10T09:15:00-07:00")}
	got := orgTimestamp(ev)
	if !strings.Contains(got, "2026-09-10") || !strings.HasPrefix(got, "<") || !strings.HasSuffix(got, ">") {
		t.Errorf("orgTimestamp = %q, want a single bracketed timestamp for 2026-09-10", got)
	}
	if strings.Contains(got, "--") {
		t.Errorf("orgTimestamp = %q, want no range for a same-day event", got)
	}
}

func TestOrgTimestampAllDaySingleDay(t *testing.T) {
	ev := gcal.Event{
		AllDay: true,
		Start:  time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC),
		End:    time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC), // exclusive, per Google's convention
	}
	want := "<2026-09-12 Sat>"
	if got := orgTimestamp(ev); got != want {
		t.Errorf("orgTimestamp = %q, want %q", got, want)
	}
}

func TestOrgTimestampAllDayMultiDay(t *testing.T) {
	ev := gcal.Event{
		AllDay: true,
		Start:  time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC),
		End:    time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC), // exclusive: last inclusive day is 9-14
	}
	want := "<2026-09-12 Sat>--<2026-09-14 Mon>"
	if got := orgTimestamp(ev); got != want {
		t.Errorf("orgTimestamp = %q, want %q", got, want)
	}
}

func TestExcludeTooLongDropsTimedEventOverSixHours(t *testing.T) {
	events := []gcal.Event{
		{ID: "short", Summary: "Standup", Start: mustParse(t, "2026-09-10T09:00:00-07:00"), End: mustParse(t, "2026-09-10T09:15:00-07:00")},
		{ID: "long", Summary: "Offsite", Start: mustParse(t, "2026-09-10T09:00:00-07:00"), End: mustParse(t, "2026-09-10T15:00:01-07:00")}, // 6h + 1s
	}
	got := ExcludeTooLong(events)
	if len(got) != 1 || got[0].ID != "short" {
		t.Errorf("ExcludeTooLong = %+v, want just the short event", got)
	}
}

func TestExcludeTooLongKeepsExactlySixHours(t *testing.T) {
	events := []gcal.Event{
		{ID: "exactly-six", Start: mustParse(t, "2026-09-10T09:00:00-07:00"), End: mustParse(t, "2026-09-10T15:00:00-07:00")},
	}
	got := ExcludeTooLong(events)
	if len(got) != 1 {
		t.Errorf("ExcludeTooLong = %+v, want the exactly-6h event kept (only strictly longer is excluded)", got)
	}
}

// TestExcludeTooLongDropsAllDayEvents covers a real gap in an earlier
// version of this filter, which exempted all-day events on the
// assumption they're holidays/OOO rather than "meetings". A reported
// case showed that's wrong for at least some real calendars (a
// full-day workshop/offsite marked all-day in Google Calendar is
// exactly the kind of "meeting longer than 6 hours" the filter is
// meant to catch) — and since an all-day event is always at least 24
// hours (see eventBounds), there's no legitimate "all-day but under 6
// hours" case an exemption would need to preserve anyway. The rule now
// applies uniformly, single-day all-day events included.
func TestExcludeTooLongDropsAllDayEvents(t *testing.T) {
	events := []gcal.Event{
		{
			ID:     "single-day",
			AllDay: true,
			Start:  time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC),
			End:    time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC), // exclusive end: exactly one day
		},
		{
			ID:     "week-long",
			AllDay: true,
			Start:  time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC),
			End:    time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC),
		},
	}
	got := ExcludeTooLong(events)
	if len(got) != 0 {
		t.Errorf("ExcludeTooLong = %+v, want every all-day event dropped (always >= 24h)", got)
	}
}

func TestExcludeTooLongLeavesInputSliceUntouched(t *testing.T) {
	events := []gcal.Event{
		{ID: "short", Start: mustParse(t, "2026-09-10T09:00:00-07:00"), End: mustParse(t, "2026-09-10T09:15:00-07:00")},
		{ID: "long", Start: mustParse(t, "2026-09-10T09:00:00-07:00"), End: mustParse(t, "2026-09-10T20:00:00-07:00")},
	}
	original := append([]gcal.Event(nil), events...)
	ExcludeTooLong(events)
	if len(events) != len(original) {
		t.Fatalf("input slice length changed: %d, want %d", len(events), len(original))
	}
	for i := range events {
		if events[i].ID != original[i].ID {
			t.Errorf("input slice mutated at index %d: %q, want %q", i, events[i].ID, original[i].ID)
		}
	}
}

func TestBuildFileRoundTripsThroughRender(t *testing.T) {
	events := []gcal.Event{
		{ID: "abc123", CalendarID: "primary", Summary: "Standup", Start: mustParse(t, "2026-09-10T09:00:00-07:00"), End: mustParse(t, "2026-09-10T09:15:00-07:00")},
	}
	rendered := org.RenderFile(BuildFile("/tmp/calendar.org", events))
	parsed, err := org.Parse(strings.NewReader(rendered), "/tmp/calendar.org")
	if err != nil {
		t.Fatalf("Parse(rendered): %v", err)
	}
	if len(parsed.Headlines) != 1 {
		t.Fatalf("len(Headlines) = %d, want 1", len(parsed.Headlines))
	}
	if got := parsed.Headlines[0].Title; got != "Standup" {
		t.Errorf("Title = %q, want %q", got, "Standup")
	}
	if got := parsed.Headlines[0].Properties["GCAL_EVENT_ID"]; got != "abc123" {
		t.Errorf("GCAL_EVENT_ID = %q, want %q", got, "abc123")
	}
}

func mustParse(t *testing.T, rfc3339 string) time.Time {
	t.Helper()
	tm, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		t.Fatalf("time.Parse(%q): %v", rfc3339, err)
	}
	return tm
}
