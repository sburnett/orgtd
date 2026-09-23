package calendarsync

import (
	"reflect"
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
	f := BuildFile("/tmp/calendar.org", events, nil, nil)
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
	}, nil, nil)
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

// TestBuildHeadlineRecordsSelfResponseStatus covers GCAL_SELF_RESPONSE_STATUS
// round-tripping ev.SelfResponseStatus verbatim — internal/ui's "gM"
// picker (meetingCandidate.accepted) reads it back to require
// acceptance, not just invitation, before treating a meeting as "in
// progress" for ranking purposes.
func TestBuildHeadlineRecordsSelfResponseStatus(t *testing.T) {
	h := buildHeadline(gcal.Event{
		ID:                 "abc123",
		Summary:            "Standup",
		Start:              mustParse(t, "2026-09-10T09:00:00-07:00"),
		End:                mustParse(t, "2026-09-10T09:15:00-07:00"),
		SelfResponseStatus: "tentative",
	}, nil, nil)
	if got := h.Properties["GCAL_SELF_RESPONSE_STATUS"]; got != "tentative" {
		t.Errorf("GCAL_SELF_RESPONSE_STATUS = %q, want %q", got, "tentative")
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
	}, nil, nil)
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
	}, nil, nil)
	if _, ok := h.Properties["GCAL_RECURRING_EVENT_ID"]; ok {
		t.Errorf("GCAL_RECURRING_EVENT_ID = %q, want no such property", h.Properties["GCAL_RECURRING_EVENT_ID"])
	}
	if len(h.Tags) != 0 {
		t.Errorf("Tags = %v, want none", h.Tags)
	}
}

func TestBuildHeadlineEmptyTitleFallback(t *testing.T) {
	h := buildHeadline(gcal.Event{ID: "x", Start: mustParse(t, "2026-09-10T09:00:00-07:00"), End: mustParse(t, "2026-09-10T09:15:00-07:00")}, nil, nil)
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
	}, nil, nil)
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
	}, nil, nil)
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
	rendered := org.RenderFile(BuildFile("/tmp/calendar.org", events, nil, nil))
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

func TestBuildHeadlineTagsEveryoneWhenInviteIsSmall(t *testing.T) {
	h := buildHeadline(gcal.Event{
		ID:    "abc123",
		Start: mustParse(t, "2026-09-10T09:00:00-07:00"),
		End:   mustParse(t, "2026-09-10T09:15:00-07:00"),
		Attendees: []gcal.Attendee{
			{Email: "john@example.com", ResponseStatus: "accepted"},
			{Email: "jane@example.com", ResponseStatus: "declined"},
			{Email: "sam@example.com", ResponseStatus: "tentative"},
			{Email: "alice@example.com", ResponseStatus: "needsAction"},
		},
	}, nil, nil)
	want := []string{"@alice", "@john", "@sam"}
	if got := h.Tags; !reflect.DeepEqual(got, want) {
		t.Errorf("Tags = %v, want %v (small invite tags everyone but the decliner, confirmed or not)", got, want)
	}
}

func TestBuildHeadlineOnlyConfirmedTaggedWhenInviteIsLarge(t *testing.T) {
	var attendees []gcal.Attendee
	for i := 0; i < 10; i++ {
		attendees = append(attendees, gcal.Attendee{Email: strings.Repeat("a", i+1) + "@example.com", ResponseStatus: "needsAction"})
	}
	attendees = append(attendees, gcal.Attendee{Email: "alice@example.com", ResponseStatus: "accepted"})
	h := buildHeadline(gcal.Event{
		ID:        "abc123",
		Start:     mustParse(t, "2026-09-10T09:00:00-07:00"),
		End:       mustParse(t, "2026-09-10T09:15:00-07:00"),
		Attendees: attendees,
	}, nil, nil)
	if got, want := h.Tags, []string{"@alice"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Tags = %v, want %v (invite too large to tag everyone, so only the confirmed attendee)", got, want)
	}
}

func TestBuildHeadlineDeclinedAttendeesDontCountTowardInviteSize(t *testing.T) {
	var attendees []gcal.Attendee
	for i := 0; i < 10; i++ {
		attendees = append(attendees, gcal.Attendee{Email: strings.Repeat("a", i+1) + "@example.com", ResponseStatus: "declined"})
	}
	attendees = append(attendees, gcal.Attendee{Email: "alice@example.com", ResponseStatus: "needsAction"})
	h := buildHeadline(gcal.Event{
		ID:        "abc123",
		Start:     mustParse(t, "2026-09-10T09:00:00-07:00"),
		End:       mustParse(t, "2026-09-10T09:15:00-07:00"),
		Attendees: attendees,
	}, nil, nil)
	if got, want := h.Tags, []string{"@alice"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Tags = %v, want %v (10 decliners plus 1 pending attendee is still a small invite)", got, want)
	}
}

func TestBuildHeadlineOmitsAttendeeTagsOverSeven(t *testing.T) {
	var attendees []gcal.Attendee
	for i := 0; i < 8; i++ {
		attendees = append(attendees, gcal.Attendee{Email: strings.Repeat("a", i+1) + "@example.com", ResponseStatus: "accepted"})
	}
	h := buildHeadline(gcal.Event{
		ID:        "abc123",
		Start:     mustParse(t, "2026-09-10T09:00:00-07:00"),
		End:       mustParse(t, "2026-09-10T09:15:00-07:00"),
		Attendees: attendees,
	}, nil, nil)
	if len(h.Tags) != 0 {
		t.Errorf("Tags = %v, want none (8 attendees exceeds the cap of 7)", h.Tags)
	}
}

func TestBuildHeadlineKeepsAttendeeTagsAtExactlySeven(t *testing.T) {
	var attendees []gcal.Attendee
	for i := 0; i < 7; i++ {
		attendees = append(attendees, gcal.Attendee{Email: strings.Repeat("a", i+1) + "@example.com", ResponseStatus: "accepted"})
	}
	h := buildHeadline(gcal.Event{
		ID:        "abc123",
		Start:     mustParse(t, "2026-09-10T09:00:00-07:00"),
		End:       mustParse(t, "2026-09-10T09:15:00-07:00"),
		Attendees: attendees,
	}, nil, nil)
	if len(h.Tags) != 7 {
		t.Errorf("Tags = %v, want 7 tags (exactly at the cap)", h.Tags)
	}
}

func TestBuildHeadlineCombinesRecurringAndAttendeeTags(t *testing.T) {
	h := buildHeadline(gcal.Event{
		ID:               "instance-1",
		RecurringEventID: "series-abc",
		Start:            mustParse(t, "2026-09-10T09:00:00-07:00"),
		End:              mustParse(t, "2026-09-10T09:15:00-07:00"),
		Attendees: []gcal.Attendee{
			{Email: "john@example.com", ResponseStatus: "accepted"},
		},
	}, nil, nil)
	want := []string{"recurring", "@john"}
	if len(h.Tags) != len(want) || h.Tags[0] != want[0] || h.Tags[1] != want[1] {
		t.Errorf("Tags = %v, want %v (recurring first, then attendee tags)", h.Tags, want)
	}
}

func TestBuildHeadlineSkipsAttendeeWithNoUsableEmail(t *testing.T) {
	h := buildHeadline(gcal.Event{
		ID:    "abc123",
		Start: mustParse(t, "2026-09-10T09:00:00-07:00"),
		End:   mustParse(t, "2026-09-10T09:15:00-07:00"),
		Attendees: []gcal.Attendee{
			{Email: "", ResponseStatus: "accepted"},
			{Email: "@example.com", ResponseStatus: "accepted"},
			{Email: "real@example.com", ResponseStatus: "accepted"},
		},
	}, nil, nil)
	if got, want := h.Tags, []string{"@real"}; len(got) != len(want) || got[0] != want[0] {
		t.Errorf("Tags = %v, want %v", got, want)
	}
}

func TestBuildHeadlineSanitizesInvalidTagCharactersInAttendeeTags(t *testing.T) {
	h := buildHeadline(gcal.Event{
		ID:    "abc123",
		Start: mustParse(t, "2026-09-10T09:00:00-07:00"),
		End:   mustParse(t, "2026-09-10T09:15:00-07:00"),
		Attendees: []gcal.Attendee{
			{Email: "john.smith-jones@example.com", ResponseStatus: "accepted"},
		},
	}, nil, nil)
	if got, want := h.Tags, []string{"@john_smith_jones"}; len(got) != len(want) || got[0] != want[0] {
		t.Errorf("Tags = %v, want %v (invalid org-tag characters replaced with _)", got, want)
	}

	// The whole point of sanitizing: the tag must actually round-trip
	// through render+parse rather than silently failing to be recognized
	// as a tag at all (see tagsRe in internal/org).
	rendered := org.RenderFile(&org.File{Path: "/tmp/calendar.org", Headlines: []*org.Headline{h}})
	parsed, err := org.Parse(strings.NewReader(rendered), "/tmp/calendar.org")
	if err != nil {
		t.Fatalf("Parse(rendered): %v", err)
	}
	if len(parsed.Headlines) != 1 || len(parsed.Headlines[0].Tags) != 1 || parsed.Headlines[0].Tags[0] != "@john_smith_jones" {
		t.Errorf("round-tripped Tags = %v, want [@john_smith_jones]", parsed.Headlines[0].Tags)
	}
}

func TestBuildHeadlineFiltersAttendeeTagsByDomain(t *testing.T) {
	h := buildHeadline(gcal.Event{
		ID:    "abc123",
		Start: mustParse(t, "2026-09-10T09:00:00-07:00"),
		End:   mustParse(t, "2026-09-10T09:15:00-07:00"),
		Attendees: []gcal.Attendee{
			{Email: "john@example.com", ResponseStatus: "accepted"},
			{Email: "vendor@outside.com", ResponseStatus: "accepted"},
		},
	}, []string{"example.com"}, nil)
	if got, want := h.Tags, []string{"@john"}; len(got) != len(want) || got[0] != want[0] {
		t.Errorf("Tags = %v, want %v (vendor@outside.com filtered out)", got, want)
	}
}

func TestBuildHeadlineDomainFilterIsCaseInsensitiveAndIgnoresLeadingAt(t *testing.T) {
	h := buildHeadline(gcal.Event{
		ID:    "abc123",
		Start: mustParse(t, "2026-09-10T09:00:00-07:00"),
		End:   mustParse(t, "2026-09-10T09:15:00-07:00"),
		Attendees: []gcal.Attendee{
			{Email: "john@Example.COM", ResponseStatus: "accepted"},
		},
	}, []string{"@example.com"}, nil)
	if got, want := h.Tags, []string{"@john"}; len(got) != len(want) || got[0] != want[0] {
		t.Errorf("Tags = %v, want %v", got, want)
	}
}

func TestBuildHeadlineDomainFilterAllowsMultipleDomains(t *testing.T) {
	h := buildHeadline(gcal.Event{
		ID:    "abc123",
		Start: mustParse(t, "2026-09-10T09:00:00-07:00"),
		End:   mustParse(t, "2026-09-10T09:15:00-07:00"),
		Attendees: []gcal.Attendee{
			{Email: "john@example.com", ResponseStatus: "accepted"},
			{Email: "jane@partner.org", ResponseStatus: "accepted"},
			{Email: "vendor@outside.com", ResponseStatus: "accepted"},
		},
	}, []string{"example.com", "partner.org"}, nil)
	want := []string{"@jane", "@john"}
	if len(h.Tags) != len(want) || h.Tags[0] != want[0] || h.Tags[1] != want[1] {
		t.Errorf("Tags = %v, want %v", h.Tags, want)
	}
}

func TestBuildHeadlineDomainFilterExcludesEverySuchAttendee(t *testing.T) {
	h := buildHeadline(gcal.Event{
		ID:    "abc123",
		Start: mustParse(t, "2026-09-10T09:00:00-07:00"),
		End:   mustParse(t, "2026-09-10T09:15:00-07:00"),
		Attendees: []gcal.Attendee{
			{Email: "john@outside.com", ResponseStatus: "accepted"},
		},
	}, []string{"example.com"}, nil)
	if len(h.Tags) != 0 {
		t.Errorf("Tags = %v, want none (no attendee matches the configured domain)", h.Tags)
	}
}

func TestBuildHeadlineIgnorePatternExcludesMatchingAttendee(t *testing.T) {
	h := buildHeadline(gcal.Event{
		ID:    "abc123",
		Start: mustParse(t, "2026-09-10T09:00:00-07:00"),
		End:   mustParse(t, "2026-09-10T09:15:00-07:00"),
		Attendees: []gcal.Attendee{
			{Email: "john@example.com", ResponseStatus: "accepted"},
			{Email: "c_abc123@resource.calendar.google.com", ResponseStatus: "accepted"},
		},
	}, nil, []string{"c_*@*"})
	if got, want := h.Tags, []string{"@john"}; len(got) != len(want) || got[0] != want[0] {
		t.Errorf("Tags = %v, want %v (synthetic c_...@... attendee excluded)", got, want)
	}
}

func TestBuildHeadlineIgnorePatternIsCaseInsensitive(t *testing.T) {
	h := buildHeadline(gcal.Event{
		ID:    "abc123",
		Start: mustParse(t, "2026-09-10T09:00:00-07:00"),
		End:   mustParse(t, "2026-09-10T09:15:00-07:00"),
		Attendees: []gcal.Attendee{
			{Email: "C_abc123@Resource.Calendar.Google.Com", ResponseStatus: "accepted"},
		},
	}, nil, []string{"c_*@*"})
	if len(h.Tags) != 0 {
		t.Errorf("Tags = %v, want none (pattern match should be case-insensitive)", h.Tags)
	}
}

func TestBuildHeadlineIgnorePatternCheckedBeforeDomainFilter(t *testing.T) {
	h := buildHeadline(gcal.Event{
		ID:    "abc123",
		Start: mustParse(t, "2026-09-10T09:00:00-07:00"),
		End:   mustParse(t, "2026-09-10T09:15:00-07:00"),
		Attendees: []gcal.Attendee{
			{Email: "c_abc123@example.com", ResponseStatus: "accepted"},
			{Email: "john@example.com", ResponseStatus: "accepted"},
		},
	}, []string{"example.com"}, []string{"c_*@*"})
	if got, want := h.Tags, []string{"@john"}; len(got) != len(want) || got[0] != want[0] {
		t.Errorf("Tags = %v, want %v (ignore pattern excludes c_abc123 even though its domain would pass)", got, want)
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
