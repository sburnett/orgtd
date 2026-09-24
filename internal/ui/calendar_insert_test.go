package ui

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/sburnett/orgtd/internal/org"
)

// commitCalendarCapture simulates insertCalendarCapture's (o/O from
// calendarView) editor session finishing with body, preserving
// attachMeeting the way insertCalendarCapture itself records it — mirrors
// commitCapture (capture_test.go), which never sets attachMeeting, and
// commitCaptureAndPickMeeting (meeting_picker_test.go), whose
// thenPickMeeting is the interactive-picker equivalent of this. Locates
// the tentative headline via the inbox file directly, same reasoning as
// commitCapture: calendarView's own rows never include it.
func commitCalendarCapture(t *testing.T, m Model, body string, cand meetingCandidate) Model {
	t.Helper()
	inbox := findInboxHeadlines(t, m)
	if len(inbox) == 0 {
		t.Fatalf("no tentative headline in the inbox file")
	}
	tentative := inbox[len(inbox)-1]
	f, parent, idx := m.insertPosition(tentative)
	ctx := insertContext{f: f, parent: parent, index: idx, switchToOutline: true, attachMeeting: &cand}
	path := writeTempOrgFile(t, body)
	updated, _ := m.Update(editFinishedMsg{path: path, target: tentative, insert: &ctx})
	return updated.(Model)
}

// TestCalendarOOnEventRowCapturesToInboxAndAttaches is the reported
// behavior change: o/O on a calendar event's own row used to insert a
// sibling in calendar.org itself (lost on the next :sync-calendar) —
// instead it should append to the end of the inbox, same target as
// gC/:capture, and attach the new entry to that event outright, with no
// "gM" picker step needed since the meeting is already unambiguous from
// the cursor's row.
func TestCalendarOOnEventRowCapturesToInboxAndAttaches(t *testing.T) {
	for _, key := range []string{"o", "O"} {
		ws := loadFixture(t)
		now := time.Now()
		event := calendarEventHeadline("abc123", now, now.Add(time.Hour))
		ws.Files = append(ws.Files, &org.File{
			Path:      filepath.Join(ws.Dir, "calendar.org"),
			Headlines: []*org.Headline{event},
		})
		m := New(ws)
		m.switchToView(calendarView)
		m.cursor = findRow(t, m, event.Title)
		before := len(findInboxHeadlines(t, m))

		m = sendKey(m, key)

		inbox := findInboxHeadlines(t, m)
		if len(inbox) != before+1 {
			t.Fatalf("key %q: inbox headline count = %d, want %d", key, len(inbox), before+1)
		}
		tentative := inbox[len(inbox)-1]
		if tentative.Level != 1 || tentative.Parent != nil {
			t.Errorf("key %q: tentative = level %d parent %v, want top-level", key, tentative.Level, tentative.Parent)
		}

		cand, ok := meetingCandidateFromEvent(event)
		if !ok {
			t.Fatalf("meetingCandidateFromEvent: event not recognized as a synced meeting")
		}
		m = commitCalendarCapture(t, m, "Follow up on budget numbers\n", cand)

		final := findInboxHeadlines(t, m)[len(findInboxHeadlines(t, m))-1]
		if final.Title != "Follow up on budget numbers" {
			t.Fatalf("key %q: captured title = %q, want %q", key, final.Title, "Follow up on budget numbers")
		}
		if got := final.Properties["GCAL_EVENT_IDS"]; got != "abc123" {
			t.Errorf("key %q: GCAL_EVENT_IDS = %q, want %q", key, got, "abc123")
		}
		if links := final.Properties["GCAL_EVENT_LINKS"]; links == "" {
			t.Errorf("key %q: GCAL_EVENT_LINKS unset, want the event's link snapshot", key)
		}
	}
}

// TestCalendarOOnLinkedItemAttachesToItsOwnEvent covers o/O pressed on an
// isCalendarLinkedItem row (an entry elsewhere in the workspace, already
// linked to the event and shown nested under it — see linkedMeetingItems)
// rather than the event's own row: the new entry should still be
// attached to that same event (via linkedFromEvent), not wherever the
// existing linked entry happens to live in the outline.
func TestCalendarOOnLinkedItemAttachesToItsOwnEvent(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	event := recurringCalendarEventHeadline("standup-1", "series-standup", "Weekly Standup", now, now.Add(30*time.Minute))
	linked := linkedToRecurringMeetings("Discuss rollout plan", "series-standup")
	ws.Files = append(ws.Files,
		&org.File{Path: filepath.Join(ws.Dir, "calendar.org"), Headlines: []*org.Headline{event}},
		&org.File{Path: filepath.Join(ws.Dir, "projects.org"), Headlines: []*org.Headline{linked}},
	)
	m := New(ws)
	m.switchToView(calendarView)
	m.cursor = findRow(t, m, "Discuss rollout plan")
	if !m.rows[m.cursor].isCalendarLinkedItem {
		t.Fatalf("cursor row isn't marked isCalendarLinkedItem")
	}
	before := len(findInboxHeadlines(t, m))

	m = sendKey(m, "O")

	inbox := findInboxHeadlines(t, m)
	if len(inbox) != before+1 {
		t.Fatalf("inbox headline count = %d, want %d", len(inbox), before+1)
	}

	cand, ok := meetingCandidateFromEvent(event)
	if !ok {
		t.Fatalf("meetingCandidateFromEvent: event not recognized as a synced meeting")
	}
	m = commitCalendarCapture(t, m, "Ping infra about the outage\n", cand)

	all := findInboxHeadlines(t, m)
	final := all[len(all)-1]
	if got := final.Properties["GCAL_RECURRING_EVENT_IDS"]; got != "series-standup" {
		t.Errorf("GCAL_RECURRING_EVENT_IDS = %q, want %q", got, "series-standup")
	}
}

// TestCalendarOOnSectionHeaderRowIsNoop checks that o/O still no-ops on a
// calendarView day-section-header row (no headline, no file — see
// resolveInsertPosition), rather than insertCalendarCapture mistaking it
// for something meeting-associated.
func TestCalendarOOnSectionHeaderRowIsNoop(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	event := calendarEventHeadline("abc123", now, now.Add(time.Hour))
	ws.Files = append(ws.Files, &org.File{
		Path:      filepath.Join(ws.Dir, "calendar.org"),
		Headlines: []*org.Headline{event},
	})
	m := New(ws)
	m.switchToView(calendarView)
	m.cursor = 0
	if m.rows[0].section == "" {
		t.Fatalf("row 0 isn't a section header, test fixture assumption broken")
	}
	before := len(findInboxHeadlines(t, m))

	m = sendKey(m, "o")

	if got := len(findInboxHeadlines(t, m)); got != before {
		t.Errorf("inbox headline count = %d, want unchanged %d", got, before)
	}
}

// TestLinkedMeetingItemsOrderedByCreatedNotFileOrder is the ordering half
// of the o/O-from-calendarView feature: since insertCalendarCapture always
// appends to the inbox (see above) rather than positioning relative to
// the cursor, an item's eventual position in the outline (once filed away
// into some other file) no longer reflects when it was actually
// captured — linkedMeetingItems instead sorts by CREATED, so two entries
// linked to the same event still show up here in the order they were
// captured even when their file/tree order says the opposite.
func TestLinkedMeetingItemsOrderedByCreatedNotFileOrder(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	event := calendarEventHeadline("abc123", now, now.Add(time.Hour))

	newer := linkedToOneOffMeetings("Captured second", "abc123")
	newer.SetProperty("CREATED", "["+now.Add(10*time.Minute).Format("2006-01-02 Mon 15:04")+"]")
	older := linkedToOneOffMeetings("Captured first", "abc123")
	older.SetProperty("CREATED", "["+now.Format("2006-01-02 Mon 15:04")+"]")

	ws.Files = append(ws.Files,
		&org.File{Path: filepath.Join(ws.Dir, "calendar.org"), Headlines: []*org.Headline{event}},
		// "aaa_project.org" sorts before "inbox.org"/"projects.org", so
		// file/tree order alone would put newer ahead of older here —
		// the opposite of CREATED order.
		&org.File{Path: filepath.Join(ws.Dir, "aaa_project.org"), Headlines: []*org.Headline{newer}},
		&org.File{Path: filepath.Join(ws.Dir, "zzz_project.org"), Headlines: []*org.Headline{older}},
	)
	m := New(ws)

	items := m.linkedMeetingItems(event)
	if len(items) != 2 {
		t.Fatalf("linkedMeetingItems returned %d items, want 2", len(items))
	}
	if items[0].Title != "Captured first" || items[1].Title != "Captured second" {
		t.Errorf("linkedMeetingItems order = [%q, %q], want [\"Captured first\", \"Captured second\"]", items[0].Title, items[1].Title)
	}
}
