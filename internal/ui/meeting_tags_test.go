package ui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sburnett/orgtd/internal/calendarsync"
	"github.com/sburnett/orgtd/internal/org"
)

// gtOnCalendarRow positions the cursor on the calendar event row titled
// title (calendarView must already be current), then runs "gt", types
// tag, and presses Enter — the same keystrokes a person would use to tag
// a synced meeting.
func gtOnCalendarRow(t *testing.T, m Model, title, tag string) Model {
	t.Helper()
	m.cursor = findRow(t, m, title)
	m = sendKey(m, "g")
	m = sendKey(m, "t")
	m = typeKeys(m, tag)
	m, _ = sendKeyCmd(m, "enter")
	return m
}

func TestGtOnCalendarEntryCreatesMeetingTagsEntry(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	event := calendarEventHeadline("abc123", now, now.Add(time.Hour))
	ws.Files = append(ws.Files, &org.File{Path: filepath.Join(ws.Dir, "calendar.org"), Headlines: []*org.Headline{event}})
	m := New(ws)
	m.switchToView(calendarView)

	m = gtOnCalendarRow(t, m, "Meeting abc123", "bob_project")

	tagsFile := m.findMeetingTagsFile()
	if tagsFile == nil {
		t.Fatalf("meeting-tags.org wasn't created")
	}
	if len(tagsFile.Headlines) != 1 {
		t.Fatalf("meeting-tags.org headlines = %d, want 1", len(tagsFile.Headlines))
	}
	h := tagsFile.Headlines[0]
	if h.Title != "Meeting abc123" {
		t.Errorf("Title = %q, want the meeting's own title, for context", h.Title)
	}
	if got := h.Properties["MEETING_TAG_EVENT_IDS"]; got != "abc123" {
		t.Errorf("MEETING_TAG_EVENT_IDS = %q, want %q", got, "abc123")
	}
	if len(h.Tags) != 1 || h.Tags[0] != "bob_project" {
		t.Errorf("Tags = %v, want [bob_project]", h.Tags)
	}

	// calendar.org's own headline is never touched — see applyMeetingTag.
	if len(event.Tags) != 0 {
		t.Errorf("calendar event's own Tags = %v, want untouched (empty)", event.Tags)
	}
}

func TestGtOnCalendarEntryRecurringSeriesUsesRecurringID(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	event := recurringCalendarEventHeadline("instance-1", "series-abc", "Weekly Standup", now, now.Add(time.Hour))
	ws.Files = append(ws.Files, &org.File{Path: filepath.Join(ws.Dir, "calendar.org"), Headlines: []*org.Headline{event}})
	m := New(ws)
	m.switchToView(calendarView)

	m = gtOnCalendarRow(t, m, "Weekly Standup", "bob_project")

	h := meetingTagsHeadlineFor(m.findMeetingTagsFile(), recurringMeeting, "series-abc")
	if h == nil {
		t.Fatalf("no meeting-tags.org entry for the recurring series ID")
	}
	if got := h.Properties["MEETING_TAG_RECURRING_EVENT_IDS"]; got != "series-abc" {
		t.Errorf("MEETING_TAG_RECURRING_EVENT_IDS = %q, want %q", got, "series-abc")
	}
}

func TestGtOnCalendarEntryAddingSecondTagKeepsFirst(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	event := calendarEventHeadline("abc123", now, now.Add(time.Hour))
	ws.Files = append(ws.Files, &org.File{Path: filepath.Join(ws.Dir, "calendar.org"), Headlines: []*org.Headline{event}})
	m := New(ws)
	m.switchToView(calendarView)

	m = gtOnCalendarRow(t, m, "Meeting abc123", "bob_project")
	m = gtOnCalendarRow(t, m, "Meeting abc123", "launch")

	tagsFile := m.findMeetingTagsFile()
	if len(tagsFile.Headlines) != 1 {
		t.Fatalf("meeting-tags.org headlines = %d, want 1 (same meeting, not a duplicate entry)", len(tagsFile.Headlines))
	}
	got := tagsFile.Headlines[0].Tags
	if len(got) != 2 || got[0] != "bob_project" || got[1] != "launch" {
		t.Errorf("Tags = %v, want [bob_project launch]", got)
	}
}

func TestGtOnCalendarEntryRetypingTagRemovesIt(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	event := calendarEventHeadline("abc123", now, now.Add(time.Hour))
	ws.Files = append(ws.Files, &org.File{Path: filepath.Join(ws.Dir, "calendar.org"), Headlines: []*org.Headline{event}})
	m := New(ws)
	m.switchToView(calendarView)

	m = gtOnCalendarRow(t, m, "Meeting abc123", "bob_project")
	m = gtOnCalendarRow(t, m, "Meeting abc123", "launch")
	m = gtOnCalendarRow(t, m, "Meeting abc123", "launch")

	got := m.findMeetingTagsFile().Headlines[0].Tags
	if len(got) != 1 || got[0] != "bob_project" {
		t.Errorf("Tags = %v, want [bob_project] (launch removed by retyping it)", got)
	}
}

// TestGtOnCalendarEntryRemovingLastTagPrunesEntry: once a meeting-tags.org
// entry's last tag is removed, the whole (now-meaningless) headline is
// deleted rather than left behind as a tagless stub.
func TestGtOnCalendarEntryRemovingLastTagPrunesEntry(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	event := calendarEventHeadline("abc123", now, now.Add(time.Hour))
	ws.Files = append(ws.Files, &org.File{Path: filepath.Join(ws.Dir, "calendar.org"), Headlines: []*org.Headline{event}})
	m := New(ws)
	m.switchToView(calendarView)

	m = gtOnCalendarRow(t, m, "Meeting abc123", "bob_project")
	m = gtOnCalendarRow(t, m, "Meeting abc123", "bob_project")

	tagsFile := m.findMeetingTagsFile()
	if tagsFile == nil || len(tagsFile.Headlines) != 0 {
		t.Errorf("meeting-tags.org headlines = %#v, want none (last tag removed)", tagsFile)
	}
}

func TestGtOnCalendarEntryRejectsRecurringTag(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	event := calendarEventHeadline("abc123", now, now.Add(time.Hour))
	ws.Files = append(ws.Files, &org.File{Path: filepath.Join(ws.Dir, "calendar.org"), Headlines: []*org.Headline{event}})
	m := New(ws)
	m.switchToView(calendarView)

	m = gtOnCalendarRow(t, m, "Meeting abc123", "recurring")

	if m.findMeetingTagsFile() != nil {
		t.Errorf("meeting-tags.org was created for a rejected tag")
	}
	if m.message == "" {
		t.Errorf("expected a status message explaining \"recurring\" is reserved")
	}
}

func TestGtOnCalendarEntryUndoRedo(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	event := calendarEventHeadline("abc123", now, now.Add(time.Hour))
	ws.Files = append(ws.Files, &org.File{Path: filepath.Join(ws.Dir, "calendar.org"), Headlines: []*org.Headline{event}})
	m := New(ws)
	m.switchToView(calendarView)

	m = gtOnCalendarRow(t, m, "Meeting abc123", "bob_project")
	if m.findMeetingTagsFile() == nil || len(m.findMeetingTagsFile().Headlines) != 1 {
		t.Fatalf("meeting-tags.org entry not created")
	}

	m = sendKey(m, "u")
	if got := m.findMeetingTagsFile().Headlines; len(got) != 0 {
		t.Errorf("after undo, meeting-tags.org headlines = %v, want none", got)
	}

	m = sendKey(m, "ctrl+r")
	if got := m.findMeetingTagsFile().Headlines; len(got) != 1 {
		t.Errorf("after redo, meeting-tags.org headlines = %v, want 1", got)
	}
}

func TestGtTagCompletionIncludesExistingMeetingTag(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	event := calendarEventHeadline("abc123", now, now.Add(time.Hour))
	ws.Files = append(ws.Files, &org.File{Path: filepath.Join(ws.Dir, "calendar.org"), Headlines: []*org.Headline{event}})
	m := New(ws)
	m.switchToView(calendarView)
	m = gtOnCalendarRow(t, m, "Meeting abc123", "bob_project")

	m.switchToView(outlineView)
	m.cursor = findRow(t, m, "Implement the Bubble Tea viewer")
	m = sendKey(m, "g")
	m = sendKey(m, "t")
	m = typeKeys(m, "bob")
	m = sendKey(m, "tab")

	if m.tagInput != "bob_project" {
		t.Errorf("tagInput = %q, want %q (completed from meeting-tags.org)", m.tagInput, "bob_project")
	}
}

// TestMeetingTagLinksTaskInAgendaAndCalendar is the end-to-end case: a
// tag recorded via "gt" on a calendar entry links a same-tagged task the
// same way a literal tag on the calendar headline would (see meetingTags
// in agenda.go), without any change to entriesForMeeting/
// appendMeetingsSection/appendCalendarHeadlines themselves.
func TestMeetingTagLinksTaskInAgendaAndCalendar(t *testing.T) {
	now := time.Now()
	meeting := oneOffCalendarEventHeadline("kickoff-1", "Client Kickoff", now.Add(time.Hour), now.Add(2*time.Hour))
	tagsEntry := &org.Headline{Level: 1, Title: "Client Kickoff", Tags: []string{"bob_project"}}
	tagsEntry.SetProperty("MEETING_TAG_EVENT_IDS", "kickoff-1")
	item := &org.Headline{Level: 1, Title: "Prep slides", Tags: []string{"bob_project"}}
	ws := meetingsFixture(
		&org.File{Path: "calendar.org", Headlines: []*org.Headline{meeting}},
		&org.File{Path: "meeting-tags.org", Headlines: []*org.Headline{tagsEntry}},
		&org.File{Path: "projects.org", Headlines: []*org.Headline{item}},
	)
	m := New(ws)

	got := m.entriesForMeeting(oneOffMeeting, "kickoff-1")
	if len(got) != 1 || got[0] != item {
		t.Fatalf("entriesForMeeting = %+v, want just the meeting-tags.org-linked item", got)
	}

	m.switchToView(calendarView)
	found := false
	for _, r := range m.rows {
		if r.headline == item {
			found = true
			if !r.isCalendarLinkedItem {
				t.Errorf("linked item row isn't marked isCalendarLinkedItem")
			}
		}
	}
	if !found {
		t.Errorf(":calendar view didn't show %q nested under its meeting", item.Title)
	}
}

// TestMeetingTagSurvivesSyncCalendarResync is the whole point of this
// feature: a tag recorded via "gt" keeps applying after :sync-calendar
// wholesale-regenerates calendar.org, since the tag lives in
// meeting-tags.org, a file :sync-calendar never touches.
func TestMeetingTagSurvivesSyncCalendarResync(t *testing.T) {
	ws := syncFixture(t)
	m := New(ws)
	m.switchToView(calendarView)

	now := time.Now()
	event := calendarEventHeadline("abc123", now, now.Add(time.Hour))
	ws.Files = append(ws.Files, &org.File{Path: filepath.Join(ws.Dir, "calendar.org"), Headlines: []*org.Headline{event}})
	m.rebuildRows()
	m = gtOnCalendarRow(t, m, "Meeting abc123", "bob_project")

	tags := m.meetingTags(oneOffMeeting, "abc123")
	if !tags["bob_project"] {
		t.Fatalf("meetingTags before resync = %v, want bob_project", tags)
	}

	// Simulate :sync-calendar wholesale-regenerating calendar.org with a
	// fresh occurrence of the same meeting (same GCAL_EVENT_ID, a new
	// headline object — exactly what finishSyncCalendar does).
	fresh := calendarEventHeadline("abc123", now.Add(24*time.Hour), now.Add(25*time.Hour))
	result := calendarsync.Result{File: &org.File{Path: filepath.Join(ws.Dir, "calendar.org"), Headlines: []*org.Headline{fresh}}}
	updated, _ := m.Update(syncCalendarMsg{result: result})
	m = updated.(Model)

	tags = m.meetingTags(oneOffMeeting, "abc123")
	if !tags["bob_project"] {
		t.Errorf("meetingTags after resync = %v, want bob_project to survive", tags)
	}
}

func TestCalendarRowShowsMeetingTagOverlay(t *testing.T) {
	now := time.Now()
	meeting := oneOffCalendarEventHeadline("kickoff-1", "Client Kickoff", now, now.Add(time.Hour))
	tagsEntry := &org.Headline{Level: 1, Title: "Client Kickoff", Tags: []string{"bob_project"}}
	tagsEntry.SetProperty("MEETING_TAG_EVENT_IDS", "kickoff-1")
	ws := meetingsFixture(
		&org.File{Path: "calendar.org", Headlines: []*org.Headline{meeting}},
		&org.File{Path: "meeting-tags.org", Headlines: []*org.Headline{tagsEntry}},
	)
	m := New(ws)

	line := stripANSI(m.renderRow(row{headline: meeting, isCalendarItem: true}))
	if !strings.Contains(line, "bob_project") {
		t.Errorf("row = %q, want the overlaid meeting-tags.org tag shown", line)
	}
}

func TestMeetingTagsCommandSwitchesViewAndBack(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")

	m = sendKey(m, ":")
	m = typeKeys(m, "meeting-tags")
	m, _ = sendKeyCmd(m, "enter")

	if m.view != meetingTagsView {
		t.Fatalf("view after :meeting-tags = %v, want meetingTagsView", m.view)
	}

	m = sendKey(m, ":")
	m = typeKeys(m, "outline")
	m, _ = sendKeyCmd(m, "enter")

	if m.view != outlineView {
		t.Fatalf("view after :outline = %v, want outlineView", m.view)
	}
}

func TestMeetingTagsFileExcludedFromOutlineView(t *testing.T) {
	ws := loadFixture(t)
	tagsEntry := &org.Headline{Level: 1, Title: "Client Kickoff", Tags: []string{"bob_project"}}
	tagsEntry.SetProperty("MEETING_TAG_EVENT_IDS", "kickoff-1")
	ws.Files = append(ws.Files, &org.File{Path: filepath.Join(ws.Dir, "meeting-tags.org"), Headlines: []*org.Headline{tagsEntry}})
	m := New(ws)

	for _, r := range m.rows {
		if r.file != nil && filepath.Base(r.file.Path) == "meeting-tags.org" {
			t.Fatalf("meeting-tags.org's file row appears in the outline view")
		}
		if r.headline == tagsEntry {
			t.Fatalf("meeting-tags.org's headline appears in the outline view")
		}
	}

	m.switchToView(meetingTagsView)
	found := false
	for _, r := range m.rows {
		if r.headline == tagsEntry {
			found = true
		}
	}
	if !found {
		t.Errorf("meeting-tags.org's headline didn't appear in :meeting-tags view")
	}
}

func TestConfigViewShowsMeetingTagsFile(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.switchToView(configView)

	found := false
	for _, r := range m.rows {
		if strings.Contains(stripANSI(r.text), "Meeting tags file: meeting-tags.org") {
			found = true
		}
	}
	if !found {
		t.Errorf(":config doesn't show the meeting tags file setting; rows: %#v", m.rows)
	}
}

// TestDiffIncludesMeetingTagsFile is the positive counterpart of
// TestDiffExcludesCalendarFile: unlike calendar.org, meeting-tags.org is
// durable user data (recorded via "gt"), not a regenerated cache, so it
// participates in :diff/:commit like any other org file.
func TestDiffIncludesMeetingTagsFile(t *testing.T) {
	ws := gitRepoFixture(t, "meeting-tags.org", "* Old title\n", "* New title\n")
	m := New(ws)

	m.showDiff()

	var sawOld, sawNew bool
	for _, r := range m.rows {
		if strings.Contains(r.text, "-* Old title") {
			sawOld = true
		}
		if strings.Contains(r.text, "+* New title") {
			sawNew = true
		}
	}
	if !sawOld || !sawNew {
		t.Errorf("rows = %#v, want lines showing meeting-tags.org's old and new titles", m.rows)
	}
}
