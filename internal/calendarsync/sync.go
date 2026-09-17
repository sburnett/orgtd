package calendarsync

import (
	"context"
	"time"

	"github.com/sburnett/orgtd/internal/gcal"
	"github.com/sburnett/orgtd/internal/org"
)

// Settings is everything one sync run needs: which calendars to pull
// from Google, how wide a window around now to sync, the OAuth2
// installed-app client to authenticate with, and where the resulting org
// file's Path should point (its content is never read from OutputPath —
// see Sync — only written there by the caller; the whole file is
// wholesale-regenerated every run, per BuildFile).
type Settings struct {
	OutputPath                       string
	CalendarIDs                      []string
	SyncPastDays, SyncFutureDays     int
	OAuthClientID, OAuthClientSecret string

	// AttendeeTagDomains restricts the "@username" attendee tags
	// BuildFile derives (see attendeeTags) to attendees whose email ends
	// in one of these domains. Empty means no restriction — every
	// confirmed attendee is tagged, regardless of domain.
	AttendeeTagDomains []string

	// AttendeeIgnorePatterns excludes any attendee whose email matches
	// one of these filepath.Match-style glob patterns from
	// consideration entirely — checked before AttendeeTagDomains, and
	// unrelated to it (see attendeeIgnored) — e.g. ["c_*@*"] to drop the
	// synthetic "c_...@..." attendees Google Calendar attaches to
	// represent a resource/room booking, which would otherwise each get
	// their own meaningless tag. Empty means no exclusions.
	AttendeeIgnorePatterns []string
}

// Result is one sync run's outcome: the regenerated org file (not yet
// written to disk — the caller does that, e.g. via org.WriteFile, once
// it also has a chance to fold it into an already-loaded workspace) plus
// counts for a status message.
type Result struct {
	File          *org.File
	EventCount    int
	CalendarCount int
}

// Sync authenticates to the Google Calendar API (reusing a token cached
// in the OS keychain from a previous run, or running the interactive
// consent flow if there isn't one — see gcal.HTTPClient; onConsentURL is
// passed straight through to it), lists every event across
// s.CalendarIDs within the sync window around now, drops any event
// running longer than ExcludeTooLong allows, and renders what's left
// into a fresh org.File via BuildFile. It never touches disk.
func Sync(ctx context.Context, s Settings, onConsentURL func(url string)) (Result, error) {
	httpClient, err := gcal.HTTPClient(ctx, s.OAuthClientID, s.OAuthClientSecret, onConsentURL)
	if err != nil {
		return Result{}, err
	}
	client, err := gcal.NewClient(ctx, httpClient)
	if err != nil {
		return Result{}, err
	}

	now := time.Now()
	startOfToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	timeMin := startOfToday.AddDate(0, 0, -s.SyncPastDays)
	timeMax := startOfToday.AddDate(0, 0, s.SyncFutureDays+1)

	var events []gcal.Event
	for _, calID := range s.CalendarIDs {
		evs, err := client.ListEvents(ctx, calID, timeMin, timeMax)
		if err != nil {
			return Result{}, err
		}
		events = append(events, evs...)
	}
	events = ExcludeTooLong(events)

	return Result{
		File:          BuildFile(s.OutputPath, events, s.AttendeeTagDomains, s.AttendeeIgnorePatterns),
		EventCount:    len(events),
		CalendarCount: len(s.CalendarIDs),
	}, nil
}

// ForgetToken discards any cached Google OAuth token (see gcal.ForgetToken),
// forcing the next Sync call to run the interactive consent flow again —
// used by ":sync-calendar!" in internal/ui.
func ForgetToken() error {
	return gcal.ForgetToken()
}
