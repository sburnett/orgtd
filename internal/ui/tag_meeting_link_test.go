package ui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sburnett/orgtd/internal/org"
)

// TestEntriesForMeetingIncludesTagMatchedEntry is the core of tag-based
// meeting linking: an entry sharing a tag with a synced calendar event —
// no "gM" attach, no explicit GCAL_EVENT_IDS/GCAL_RECURRING_EVENT_IDS
// property at all — is treated the same as one that was attached.
func TestEntriesForMeetingIncludesTagMatchedEntry(t *testing.T) {
	now := time.Now()
	meeting := oneOffCalendarEventHeadline("kickoff-1", "Client Kickoff", now, now.Add(time.Hour))
	meeting.Tags = []string{"@alice"}
	item := &org.Headline{Level: 1, Title: "Prep slides", Tags: []string{"@alice"}}
	ws := meetingsFixture(
		&org.File{Path: "calendar.org", Headlines: []*org.Headline{meeting}},
		&org.File{Path: "projects.org", Headlines: []*org.Headline{item}},
	)
	m := New(ws)

	got := m.entriesForMeeting(oneOffMeeting, "kickoff-1")
	if len(got) != 1 || got[0] != item {
		t.Errorf("entriesForMeeting = %+v, want just the tag-matched item", got)
	}
}

// TestEntriesForMeetingExcludesRecurringTagAlone guards the
// meetingSeriesTag exclusion: "recurring" is stamped onto every
// occurrence of every recurring series (see internal/calendarsync's
// buildHeadline), so matching on it alone would link any
// "recurring"-tagged entry to every recurring meeting synced — clearly
// not a meaningful connection the way a shared "@username" tag is.
func TestEntriesForMeetingExcludesRecurringTagAlone(t *testing.T) {
	now := time.Now()
	meeting := recurringCalendarEventHeadline("instance-1", "series-abc", "Weekly Standup", now, now.Add(time.Hour))
	item := &org.Headline{Level: 1, Title: "Unrelated", Tags: []string{"recurring"}}
	ws := meetingsFixture(
		&org.File{Path: "calendar.org", Headlines: []*org.Headline{meeting}},
		&org.File{Path: "projects.org", Headlines: []*org.Headline{item}},
	)
	m := New(ws)

	got := m.entriesForMeeting(recurringMeeting, "series-abc")
	if len(got) != 0 {
		t.Errorf("entriesForMeeting = %+v, want none (only the recurring system tag matches)", got)
	}
}

// TestEntriesForMeetingExcludesDoneTagMatchedItem: a tag-matched entry
// that's already DONE/CANCELLED is excluded, same as an explicitly
// attached one — already resolved, nothing left to revisit.
func TestEntriesForMeetingExcludesDoneTagMatchedItem(t *testing.T) {
	now := time.Now()
	meeting := oneOffCalendarEventHeadline("kickoff-1", "Client Kickoff", now, now.Add(time.Hour))
	meeting.Tags = []string{"@alice"}
	item := &org.Headline{Level: 1, Keyword: "DONE", Title: "Already handled", Tags: []string{"@alice"}}
	ws := meetingsFixture(
		&org.File{Path: "calendar.org", Headlines: []*org.Headline{meeting}},
		&org.File{Path: "projects.org", Headlines: []*org.Headline{item}},
	)
	m := New(ws)

	got := m.entriesForMeeting(oneOffMeeting, "kickoff-1")
	if len(got) != 0 {
		t.Errorf("entriesForMeeting = %+v, want none (DONE item excluded)", got)
	}
}

// TestEntriesForMeetingDoesNotDuplicateExplicitAndTagMatch: an entry
// that's both explicitly attached via "gM" and separately tag-matched
// to the same meeting shows up once, not twice.
func TestEntriesForMeetingDoesNotDuplicateExplicitAndTagMatch(t *testing.T) {
	now := time.Now()
	meeting := oneOffCalendarEventHeadline("kickoff-1", "Client Kickoff", now, now.Add(time.Hour))
	meeting.Tags = []string{"@alice"}
	item := linkedToOneOffMeetings("Bring the deck", "kickoff-1")
	item.Tags = []string{"@alice"}
	ws := meetingsFixture(
		&org.File{Path: "calendar.org", Headlines: []*org.Headline{meeting}},
		&org.File{Path: "projects.org", Headlines: []*org.Headline{item}},
	)
	m := New(ws)

	got := m.entriesForMeeting(oneOffMeeting, "kickoff-1")
	if len(got) != 1 {
		t.Errorf("entriesForMeeting = %+v, want exactly one entry, not a duplicate", got)
	}
}

// TestEntriesForMeetingExcludesOtherCalendarEventFromTagMatch: a
// different synced calendar event sharing a tag (e.g. the same
// attendee, invited to two different meetings) is never itself treated
// as a "linked item" — that role is for entries elsewhere in the
// workspace, not other meetings.
func TestEntriesForMeetingExcludesOtherCalendarEventFromTagMatch(t *testing.T) {
	now := time.Now()
	meeting1 := oneOffCalendarEventHeadline("kickoff-1", "Client Kickoff", now, now.Add(time.Hour))
	meeting1.Tags = []string{"@alice"}
	meeting2 := oneOffCalendarEventHeadline("standup-1", "Standup", now, now.Add(time.Hour))
	meeting2.Tags = []string{"@alice"}
	ws := meetingsFixture(
		&org.File{Path: "calendar.org", Headlines: []*org.Headline{meeting1, meeting2}},
	)
	m := New(ws)

	got := m.entriesForMeeting(oneOffMeeting, "kickoff-1")
	if len(got) != 0 {
		t.Errorf("entriesForMeeting = %+v, want none (meeting2 is a calendar event, not a linkable entry)", got)
	}
}

// TestMeetingsSectionGroupsTagMatchedItemUnderMeetingHeader is the
// integration-level counterpart of TestMeetingsSectionGroupsLinkedItemUnderMeetingHeader,
// but for a tag match instead of an explicit "gM" attach.
func TestMeetingsSectionGroupsTagMatchedItemUnderMeetingHeader(t *testing.T) {
	now := time.Now()
	meeting := recurringCalendarEventHeadline("instance-1", "series-abc", "Weekly Standup", now.Add(time.Hour), now.Add(90*time.Minute))
	meeting.Tags = append(meeting.Tags, "@alice")
	item := &org.Headline{Level: 1, Title: "Follow up on last week's blocker", Tags: []string{"@alice"}}
	ws := meetingsFixture(
		&org.File{Path: "calendar.org", Headlines: []*org.Headline{meeting}},
		&org.File{Path: "projects.org", Headlines: []*org.Headline{item}},
	)
	m := New(ws)
	m.switchToView(agendaView)

	var headerIdx, itemIdx = -1, -1
	for i, r := range m.rows {
		switch {
		case r.isMeetingHeader && r.meetingTitle == "Weekly Standup":
			headerIdx = i
		case r.headline == item:
			itemIdx = i
		}
	}
	if headerIdx == -1 {
		t.Fatalf("no meeting header row for Weekly Standup; rows: %+v", m.rows)
	}
	if itemIdx == -1 || itemIdx < headerIdx {
		t.Fatalf("tag-matched item not shown grouped under its meeting; rows: %+v", m.rows)
	}
}

// TestCalendarViewShowsTagMatchedItem mirrors
// TestCalendarViewShowsItemsAttachedToAMeetingByDefault, but for a tag
// match instead of an explicit "gM" attach.
func TestCalendarViewShowsTagMatchedItem(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	event := calendarEventHeadline("abc123", now, now.Add(time.Hour))
	event.Tags = []string{"@alice"}
	linked := &org.Headline{Level: 1, Title: "Follow up on budget", Tags: []string{"@alice"}}
	ws.Files = append(ws.Files,
		&org.File{Path: filepath.Join(ws.Dir, "calendar.org"), Headlines: []*org.Headline{event}},
		&org.File{Path: filepath.Join(ws.Dir, "projects.org"), Headlines: []*org.Headline{linked}},
	)
	m := New(ws)
	m.switchToView(calendarView)

	found := false
	for _, r := range m.rows {
		if r.headline == linked {
			found = true
			if !r.isCalendarLinkedItem {
				t.Errorf("tag-matched item row isn't marked isCalendarLinkedItem")
			}
		}
	}
	if !found {
		t.Errorf("tag-matched item %q not shown under its meeting", linked.Title)
	}
}

// TestCalendarEventLinksIncludesTagMatchedMeeting covers the status
// line's side of tag-based linking: an entry with no explicit
// attachment at all still shows the tag-matched meeting's title and
// link, the same way an explicitly attached entry always has.
func TestCalendarEventLinksIncludesTagMatchedMeeting(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	event := calendarEventHeadline("abc123", now, now.Add(time.Hour))
	event.Tags = []string{"@alice"}
	item := &org.Headline{Level: 1, Title: "Follow up on budget", Tags: []string{"@alice"}}
	ws.Files = append(ws.Files,
		&org.File{Path: filepath.Join(ws.Dir, "calendar.org"), Headlines: []*org.Headline{event}},
		&org.File{Path: filepath.Join(ws.Dir, "projects.org"), Headlines: []*org.Headline{item}},
	)
	m := New(ws)

	links := m.calendarEventLinks(item)
	joined := strings.Join(links, "\n")
	if !strings.Contains(joined, "Meeting abc123") || !strings.Contains(joined, "https://calendar.google.com/event?eid=abc123") {
		t.Errorf("calendarEventLinks = %v, want the tag-matched meeting's title and link", links)
	}
}

// TestCalendarEventLinksNoDuplicateWhenBothExplicitAndTagMatch: an
// entry explicitly attached (via "gM", giving it a cached
// GCAL_EVENT_LINKS snapshot) to a meeting it also tag-matches only shows
// that meeting's link once.
func TestCalendarEventLinksNoDuplicateWhenBothExplicitAndTagMatch(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	event := calendarEventHeadline("abc123", now, now.Add(time.Hour))
	event.Tags = []string{"@alice"}
	item := linkedToOneOffMeetings("Bring the deck", "abc123")
	item.SetProperty("GCAL_EVENT_LINKS", "[[https://calendar.google.com/event?eid=abc123][Meeting abc123]]")
	item.Tags = []string{"@alice"}
	ws.Files = append(ws.Files,
		&org.File{Path: filepath.Join(ws.Dir, "calendar.org"), Headlines: []*org.Headline{event}},
		&org.File{Path: filepath.Join(ws.Dir, "projects.org"), Headlines: []*org.Headline{item}},
	)
	m := New(ws)

	links := m.calendarEventLinks(item)
	count := strings.Count(strings.Join(links, "\n"), "eid=abc123")
	if count != 1 {
		t.Errorf("calendarEventLinks = %v, want the meeting's link listed exactly once, got %d", links, count)
	}
}

// TestMeetingColumnShowsForTagMatchedEntry covers the gutter's meeting
// marker (▣) lighting up for a tag match, exactly as it does for an
// explicit "gM" attachment — see TestMeetingColumnShowsForRecurringAttachment.
func TestMeetingColumnShowsForTagMatchedEntry(t *testing.T) {
	now := time.Now()
	meeting := oneOffCalendarEventHeadline("kickoff-1", "Client Kickoff", now, now.Add(time.Hour))
	meeting.Tags = []string{"@alice"}
	item := &org.Headline{Level: 1, Keyword: "TODO", Title: "Prep slides", Tags: []string{"@alice"}}
	ws := meetingsFixture(
		&org.File{Path: "calendar.org", Headlines: []*org.Headline{meeting}},
		&org.File{Path: "projects.org", Headlines: []*org.Headline{item}},
	)
	m := New(ws)

	line := []rune(stripANSI(m.renderRow(row{headline: item})))
	if len(line) < 3 || line[2] != '▣' {
		t.Errorf("row = %q, want the meeting column (index 2) to show ▣", string(line))
	}
}

// TestMeetingColumnBlankForRecurringTagOnlyMatch: an entry tagged only
// "recurring" never lights up the gutter marker, no matter how many
// recurring meetings are synced — see meetingSeriesTag.
func TestMeetingColumnBlankForRecurringTagOnlyMatch(t *testing.T) {
	now := time.Now()
	meeting := recurringCalendarEventHeadline("instance-1", "series-abc", "Weekly Standup", now, now.Add(time.Hour))
	item := &org.Headline{Level: 1, Keyword: "TODO", Title: "Unrelated", Tags: []string{"recurring"}}
	ws := meetingsFixture(
		&org.File{Path: "calendar.org", Headlines: []*org.Headline{meeting}},
		&org.File{Path: "projects.org", Headlines: []*org.Headline{item}},
	)
	m := New(ws)

	line := []rune(stripANSI(m.renderRow(row{headline: item})))
	if len(line) < 3 || line[2] != ' ' {
		t.Errorf("row = %q, want the meeting column (index 2) blank", string(line))
	}
}

// TestMeetingColumnBlankWhenTagMatchesNothing: an entry with a tag that
// doesn't match any synced calendar event's tags shows a blank meeting
// column, same as no tags at all.
func TestMeetingColumnBlankWhenTagMatchesNothing(t *testing.T) {
	now := time.Now()
	meeting := oneOffCalendarEventHeadline("kickoff-1", "Client Kickoff", now, now.Add(time.Hour))
	meeting.Tags = []string{"@alice"}
	item := &org.Headline{Level: 1, Keyword: "TODO", Title: "Unrelated", Tags: []string{"@bob"}}
	ws := meetingsFixture(
		&org.File{Path: "calendar.org", Headlines: []*org.Headline{meeting}},
		&org.File{Path: "projects.org", Headlines: []*org.Headline{item}},
	)
	m := New(ws)

	line := []rune(stripANSI(m.renderRow(row{headline: item})))
	if len(line) < 3 || line[2] != ' ' {
		t.Errorf("row = %q, want the meeting column (index 2) blank", string(line))
	}
}

// TestTagLinkedMeetingCandidatesExcludesEventsOwnMeeting: a synced
// calendar event sharing tags with itself (trivially true) never counts
// as linked to its own meeting — only to some *other* meeting it might
// separately tag-match.
func TestTagLinkedMeetingCandidatesExcludesEventsOwnMeeting(t *testing.T) {
	now := time.Now()
	event := oneOffCalendarEventHeadline("kickoff-1", "Client Kickoff", now, now.Add(time.Hour))
	event.Tags = []string{"@alice"}
	ws := meetingsFixture(
		&org.File{Path: "calendar.org", Headlines: []*org.Headline{event}},
	)
	m := New(ws)

	got := m.tagLinkedMeetingCandidates(event, now)
	if len(got) != 0 {
		t.Errorf("tagLinkedMeetingCandidates = %+v, want none (an event can't be linked to its own meeting)", got)
	}
}
