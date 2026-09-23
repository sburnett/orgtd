// Package gcal is a thin, read-only client for the Google Calendar API,
// plus the OAuth2 installed-app auth flow needed to talk to it. It knows
// nothing about org files — see internal/calendarsync for that.
package gcal

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"
)

// Event is one calendar event instance in the sync window. Recurring
// events are expanded into individual instances by the Calendar API
// itself (see Client.ListEvents), so every Event here is a single
// occurrence with its own start/end — but see RecurringEventID for how
// to tell it's one of a series.
type Event struct {
	ID          string
	CalendarID  string
	Summary     string
	Description string
	Location    string
	HTMLLink    string

	// RecurringEventID is the ID of the recurring series this instance
	// belongs to (stable across every occurrence, unlike ID above, which
	// is unique per instance) — empty for a one-off event. It's what a
	// task or project would associate with (per DESIGN.md's
	// project<->meeting concept) to mean "this recurring meeting", not
	// just today's occurrence of it.
	RecurringEventID string

	// AllDay is true for a date-only event (Google's "date" field, no
	// time-of-day or timezone); Start/End are then midnight-anchored
	// calendar dates rather than instants.
	AllDay bool
	Start  time.Time
	End    time.Time

	// Attendees lists everyone Google's API returned as invited to this
	// event (the calendar owner included, via a Self entry — see
	// declinedBySelf above for the other place that matters). Used by
	// internal/calendarsync to tag the synced headline with each
	// confirmed attendee's "@username" — see BuildFile.
	Attendees []Attendee

	// SelfResponseStatus is the calendar owner's own RSVP to this event
	// (the Self attendee's ResponseStatus — see declinedBySelf), one of
	// "accepted", "declined" (excluded before this ever gets set — see
	// ListEvents), "tentative", or "needsAction". An event with no Self
	// attendee at all (a personal entry with no invite list) is treated
	// as "accepted": nothing to RSVP to means it's implicitly confirmed.
	// Used by internal/ui's "gM" picker to require acceptance, not just
	// invitation, before a meeting counts as "in progress" for ranking
	// purposes — see meetingPickerLess.
	SelfResponseStatus string
}

// Attendee is one invitee on a calendar event — just enough of what
// Google's API returns to derive the "@username" tags BuildFile builds
// from confirmed attendees (see internal/calendarsync); orgtd has no
// other use for the rest of the attendee record.
type Attendee struct {
	Email string
	// ResponseStatus is Google's own RSVP value: "accepted", "declined",
	// "tentative", or "needsAction".
	ResponseStatus string
}

// Client is a thin, read-only wrapper around the Google Calendar API.
type Client struct {
	svc *calendar.Service
}

// NewClient builds a Client that authenticates every request via
// httpClient (typically one built by HTTPClient, in auth.go).
func NewClient(ctx context.Context, httpClient *http.Client) (*Client, error) {
	svc, err := calendar.NewService(ctx, option.WithHTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("gcal: creating calendar service: %w", err)
	}
	return &Client{svc: svc}, nil
}

// ListEvents returns every event on calendarID starting in
// [timeMin, timeMax), recurring events expanded into individual
// instances and sorted by start time. Cancelled events, and events the
// caller has personally declined, are excluded.
func (c *Client) ListEvents(ctx context.Context, calendarID string, timeMin, timeMax time.Time) ([]Event, error) {
	var events []Event
	pageToken := ""
	for {
		call := c.svc.Events.List(calendarID).
			Context(ctx).
			SingleEvents(true).
			OrderBy("startTime").
			TimeMin(timeMin.Format(time.RFC3339)).
			TimeMax(timeMax.Format(time.RFC3339)).
			MaxResults(2500)
		if pageToken != "" {
			call = call.PageToken(pageToken)
		}

		resp, err := call.Do()
		if err != nil {
			return nil, fmt.Errorf("gcal: listing events on %s: %w", calendarID, err)
		}
		for _, item := range resp.Items {
			if item.Status == "cancelled" || declinedBySelf(item) {
				continue
			}
			if ev, ok := toEvent(calendarID, item); ok {
				events = append(events, ev)
			}
		}

		if resp.NextPageToken == "" {
			break
		}
		pageToken = resp.NextPageToken
	}
	return events, nil
}

func declinedBySelf(item *calendar.Event) bool {
	for _, a := range item.Attendees {
		if a.Self && a.ResponseStatus == "declined" {
			return true
		}
	}
	return false
}

// selfResponseStatus reports the calendar owner's own RSVP to item — see
// Event.SelfResponseStatus.
func selfResponseStatus(item *calendar.Event) string {
	for _, a := range item.Attendees {
		if a.Self {
			return a.ResponseStatus
		}
	}
	return "accepted"
}

func toEvent(calendarID string, item *calendar.Event) (Event, bool) {
	start, allDay, ok := parseEventTime(item.Start)
	if !ok {
		return Event{}, false
	}
	end, _, ok := parseEventTime(item.End)
	if !ok {
		end = start
	}
	var attendees []Attendee
	for _, a := range item.Attendees {
		attendees = append(attendees, Attendee{Email: a.Email, ResponseStatus: a.ResponseStatus})
	}
	return Event{
		ID:                 item.Id,
		CalendarID:         calendarID,
		Summary:            item.Summary,
		Description:        item.Description,
		Location:           item.Location,
		HTMLLink:           item.HtmlLink,
		RecurringEventID:   item.RecurringEventId,
		AllDay:             allDay,
		Start:              start,
		End:                end,
		Attendees:          attendees,
		SelfResponseStatus: selfResponseStatus(item),
	}, true
}

// parseEventTime reads one endpoint of an event's start/end, which the
// API represents as either a full timestamp (DateTime, timed event) or a
// bare calendar date (Date, all-day event) — never both.
func parseEventTime(t *calendar.EventDateTime) (when time.Time, allDay bool, ok bool) {
	if t == nil {
		return time.Time{}, false, false
	}
	if t.DateTime != "" {
		parsed, err := time.Parse(time.RFC3339, t.DateTime)
		if err != nil {
			return time.Time{}, false, false
		}
		return parsed, false, true
	}
	if t.Date != "" {
		parsed, err := time.Parse("2006-01-02", t.Date)
		if err != nil {
			return time.Time{}, false, false
		}
		return parsed, true, true
	}
	return time.Time{}, false, false
}
