package gcal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"
)

// fakeEventsResponse mirrors just the fields of the Calendar API's
// events.list response this package reads.
type fakeEventsResponse struct {
	Items         []*calendar.Event `json:"items"`
	NextPageToken string            `json:"nextPageToken,omitempty"`
}

func TestListEventsFiltersCancelledAndDeclined(t *testing.T) {
	resp := fakeEventsResponse{
		Items: []*calendar.Event{
			{
				Id:      "kept-timed",
				Summary: "Standup",
				Start:   &calendar.EventDateTime{DateTime: "2026-09-10T09:00:00-07:00"},
				End:     &calendar.EventDateTime{DateTime: "2026-09-10T09:15:00-07:00"},
			},
			{
				Id:      "kept-all-day",
				Summary: "Offsite",
				Start:   &calendar.EventDateTime{Date: "2026-09-12"},
				End:     &calendar.EventDateTime{Date: "2026-09-13"},
			},
			{
				Id:      "cancelled",
				Summary: "Cancelled meeting",
				Status:  "cancelled",
				Start:   &calendar.EventDateTime{DateTime: "2026-09-10T10:00:00-07:00"},
				End:     &calendar.EventDateTime{DateTime: "2026-09-10T10:30:00-07:00"},
			},
			{
				Id:      "declined",
				Summary: "Declined meeting",
				Start:   &calendar.EventDateTime{DateTime: "2026-09-10T11:00:00-07:00"},
				End:     &calendar.EventDateTime{DateTime: "2026-09-10T11:30:00-07:00"},
				Attendees: []*calendar.EventAttendee{
					{Self: true, ResponseStatus: "declined"},
				},
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Fatalf("encoding fake response: %v", err)
		}
	}))
	defer server.Close()

	svc, err := calendar.NewService(context.Background(),
		option.WithoutAuthentication(),
		option.WithEndpoint(server.URL),
		option.WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatalf("calendar.NewService: %v", err)
	}
	c := &Client{svc: svc}

	events, err := c.ListEvents(context.Background(), "primary",
		time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}

	var ids []string
	for _, e := range events {
		ids = append(ids, e.ID)
	}
	want := []string{"kept-timed", "kept-all-day"}
	if len(ids) != len(want) {
		t.Fatalf("ListEvents ids = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Errorf("ListEvents ids = %v, want %v", ids, want)
			break
		}
	}

	for _, e := range events {
		if e.CalendarID != "primary" {
			t.Errorf("event %s CalendarID = %q, want %q", e.ID, e.CalendarID, "primary")
		}
	}

	allDay := events[1]
	if !allDay.AllDay {
		t.Errorf("event %s AllDay = false, want true", allDay.ID)
	}
	if !allDay.Start.Equal(time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("event %s Start = %v, want 2026-09-12", allDay.ID, allDay.Start)
	}
}

func TestListEventsCarriesRecurringEventID(t *testing.T) {
	resp := fakeEventsResponse{
		Items: []*calendar.Event{
			{
				Id:               "instance-1",
				Summary:          "Weekly standup",
				RecurringEventId: "series-abc",
				Start:            &calendar.EventDateTime{DateTime: "2026-09-10T09:00:00-07:00"},
				End:              &calendar.EventDateTime{DateTime: "2026-09-10T09:15:00-07:00"},
			},
			{
				Id:      "one-off",
				Summary: "One-time sync",
				Start:   &calendar.EventDateTime{DateTime: "2026-09-11T09:00:00-07:00"},
				End:     &calendar.EventDateTime{DateTime: "2026-09-11T09:15:00-07:00"},
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Fatalf("encoding fake response: %v", err)
		}
	}))
	defer server.Close()

	svc, err := calendar.NewService(context.Background(),
		option.WithoutAuthentication(),
		option.WithEndpoint(server.URL),
		option.WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatalf("calendar.NewService: %v", err)
	}
	c := &Client{svc: svc}

	events, err := c.ListEvents(context.Background(), "primary",
		time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("len(events) = %d, want 2", len(events))
	}
	if got := events[0].RecurringEventID; got != "series-abc" {
		t.Errorf("instance-1 RecurringEventID = %q, want %q", got, "series-abc")
	}
	if got := events[1].RecurringEventID; got != "" {
		t.Errorf("one-off RecurringEventID = %q, want empty", got)
	}
}

func TestListEventsCarriesAttendees(t *testing.T) {
	resp := fakeEventsResponse{
		Items: []*calendar.Event{
			{
				Id:      "with-attendees",
				Summary: "Planning sync",
				Start:   &calendar.EventDateTime{DateTime: "2026-09-10T09:00:00-07:00"},
				End:     &calendar.EventDateTime{DateTime: "2026-09-10T09:15:00-07:00"},
				Attendees: []*calendar.EventAttendee{
					{Self: true, Email: "me@example.com", ResponseStatus: "accepted"},
					{Email: "jane@example.com", ResponseStatus: "tentative"},
				},
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Fatalf("encoding fake response: %v", err)
		}
	}))
	defer server.Close()

	svc, err := calendar.NewService(context.Background(),
		option.WithoutAuthentication(),
		option.WithEndpoint(server.URL),
		option.WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatalf("calendar.NewService: %v", err)
	}
	c := &Client{svc: svc}

	events, err := c.ListEvents(context.Background(), "primary",
		time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(events))
	}
	want := []Attendee{
		{Email: "me@example.com", ResponseStatus: "accepted"},
		{Email: "jane@example.com", ResponseStatus: "tentative"},
	}
	got := events[0].Attendees
	if len(got) != len(want) {
		t.Fatalf("Attendees = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Attendees[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestListEventsCarriesSelfResponseStatus covers Event.SelfResponseStatus:
// pulled from whichever attendee has Self set, not just the first
// attendee in the list.
func TestListEventsCarriesSelfResponseStatus(t *testing.T) {
	resp := fakeEventsResponse{
		Items: []*calendar.Event{
			{
				Id:      "tentative-self",
				Summary: "Planning sync",
				Start:   &calendar.EventDateTime{DateTime: "2026-09-10T09:00:00-07:00"},
				End:     &calendar.EventDateTime{DateTime: "2026-09-10T09:15:00-07:00"},
				Attendees: []*calendar.EventAttendee{
					{Email: "jane@example.com", ResponseStatus: "accepted"},
					{Self: true, Email: "me@example.com", ResponseStatus: "tentative"},
				},
			},
			{
				Id:      "no-attendees",
				Summary: "Focus block",
				Start:   &calendar.EventDateTime{DateTime: "2026-09-10T10:00:00-07:00"},
				End:     &calendar.EventDateTime{DateTime: "2026-09-10T10:30:00-07:00"},
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Fatalf("encoding fake response: %v", err)
		}
	}))
	defer server.Close()

	svc, err := calendar.NewService(context.Background(),
		option.WithoutAuthentication(),
		option.WithEndpoint(server.URL),
		option.WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatalf("calendar.NewService: %v", err)
	}
	c := &Client{svc: svc}

	events, err := c.ListEvents(context.Background(), "primary",
		time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("len(events) = %d, want 2", len(events))
	}
	if got := events[0].SelfResponseStatus; got != "tentative" {
		t.Errorf("tentative-self SelfResponseStatus = %q, want %q", got, "tentative")
	}
	if got := events[1].SelfResponseStatus; got != "accepted" {
		t.Errorf("no-attendees SelfResponseStatus = %q, want %q (no Self entry implies accepted)", got, "accepted")
	}
}
