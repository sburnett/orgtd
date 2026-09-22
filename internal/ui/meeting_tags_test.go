package ui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode"

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

// TestMeetingTagsViewNestsMatchingOneOffEvent covers the core of this
// file's nesting: a meeting-tags.org record naming a one-off event's ID
// shows that synced event nested right under it, one level deeper.
func TestMeetingTagsViewNestsMatchingOneOffEvent(t *testing.T) {
	now := time.Now()
	event := oneOffCalendarEventHeadline("kickoff-1", "Client Kickoff", now, now.Add(time.Hour))
	record := &org.Headline{Level: 1, Title: "Client Kickoff", Tags: []string{"bob_project"}}
	record.SetProperty("MEETING_TAG_EVENT_IDS", "kickoff-1")
	ws := meetingsFixture(
		&org.File{Path: "calendar.org", Headlines: []*org.Headline{event}},
		&org.File{Path: "meeting-tags.org", Headlines: []*org.Headline{record}},
	)
	m := New(ws)
	m.switchToView(meetingTagsView)

	recordIdx, eventIdx := -1, -1
	for i, r := range m.rows {
		switch r.headline {
		case record:
			recordIdx = i
		case event:
			eventIdx = i
		}
	}
	if recordIdx == -1 {
		t.Fatalf("meeting-tags.org record row not found; rows: %+v", m.rows)
	}
	if eventIdx == -1 {
		t.Fatalf("matching calendar event not nested under its meeting-tags.org record; rows: %+v", m.rows)
	}
	if eventIdx != recordIdx+1 {
		t.Errorf("event row at index %d, want immediately after the record at %d", eventIdx, recordIdx)
	}
	if got, want := m.rows[eventIdx].level, m.rows[recordIdx].level+1; got != want {
		t.Errorf("event row level = %d, want %d (one deeper than the record)", got, want)
	}
	if !m.rows[eventIdx].isCalendarItem {
		t.Errorf("nested event row isn't marked isCalendarItem")
	}
}

// TestMeetingTagsViewNestsEveryRecurringOccurrence: a record naming a
// recurring series' ID nests every synced occurrence of it (not just
// one), chronologically — showing at a glance how many instances of the
// series are currently in the sync window.
func TestMeetingTagsViewNestsEveryRecurringOccurrence(t *testing.T) {
	now := time.Now()
	later := recurringCalendarEventHeadline("instance-2", "series-abc", "Weekly Standup", now.Add(48*time.Hour), now.Add(49*time.Hour))
	earlier := recurringCalendarEventHeadline("instance-1", "series-abc", "Weekly Standup", now, now.Add(time.Hour))
	record := &org.Headline{Level: 1, Title: "Weekly Standup", Tags: []string{"bob_project"}}
	record.SetProperty("MEETING_TAG_RECURRING_EVENT_IDS", "series-abc")
	ws := meetingsFixture(
		// Deliberately out of chronological order, so a passing test can't
		// be an accident of file order.
		&org.File{Path: "calendar.org", Headlines: []*org.Headline{later, earlier}},
		&org.File{Path: "meeting-tags.org", Headlines: []*org.Headline{record}},
	)
	m := New(ws)
	m.switchToView(meetingTagsView)

	var nested []*org.Headline
	for _, r := range m.rows {
		if r.headline == earlier || r.headline == later {
			nested = append(nested, r.headline)
		}
	}
	if len(nested) != 2 || nested[0] != earlier || nested[1] != later {
		t.Errorf("nested occurrences = %+v, want [earlier later] in chronological order", nested)
	}
}

// TestMeetingTagsViewNestsNothingForStaleID: a record whose ID no longer
// matches any currently-synced event shows nothing nested under it —
// meeting-tags.org's own window into a stale record (see
// meetingTagsMatchingEvents) — rather than erroring or showing a stale
// placeholder.
func TestMeetingTagsViewNestsNothingForStaleID(t *testing.T) {
	record := &org.Headline{Level: 1, Title: "Long-gone meeting", Tags: []string{"bob_project"}}
	record.SetProperty("MEETING_TAG_EVENT_IDS", "no-longer-synced")
	ws := meetingsFixture(
		&org.File{Path: "calendar.org", Headlines: nil},
		&org.File{Path: "meeting-tags.org", Headlines: []*org.Headline{record}},
	)
	m := New(ws)
	m.switchToView(meetingTagsView)

	if len(m.rows) != 1 { // just the record's own row, nothing nested
		t.Errorf("rows = %+v, want just the record, nothing nested", m.rows)
	}
}

// TestMeetingTagsViewNestedEventSupportsGt: "gt" on a nested event row —
// the same headline as :calendar's own row for it — still routes through
// applyMeetingTag and updates the very record it's nested under, since
// it's the same *org.Headline, not a copy.
func TestMeetingTagsViewNestedEventSupportsGt(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	event := calendarEventHeadline("abc123", now, now.Add(time.Hour))
	ws.Files = append(ws.Files, &org.File{Path: filepath.Join(ws.Dir, "calendar.org"), Headlines: []*org.Headline{event}})
	m := New(ws)
	m.switchToView(calendarView)
	m = gtOnCalendarRow(t, m, "Meeting abc123", "bob_project")

	m.switchToView(meetingTagsView)
	m.cursor = findRow(t, m, "Meeting abc123")
	m = sendKey(m, "g")
	m = sendKey(m, "t")
	m = typeKeys(m, "launch")
	m, _ = sendKeyCmd(m, "enter")

	record := m.findMeetingTagsFile().Headlines[0]
	if len(record.Tags) != 2 || record.Tags[0] != "bob_project" || record.Tags[1] != "launch" {
		t.Errorf("record Tags = %v, want [bob_project launch]", record.Tags)
	}
}

// TestMeetingTagsViewHasNoFileHeaderRow: unlike the plain outline,
// meetingTagsView shows no "meeting-tags.org" file-header row — same as
// calendarView, which shows no "calendar.org" header either.
func TestMeetingTagsViewHasNoFileHeaderRow(t *testing.T) {
	record := &org.Headline{Level: 1, Title: "Client Kickoff", Tags: []string{"bob_project"}}
	record.SetProperty("MEETING_TAG_EVENT_IDS", "kickoff-1")
	ws := meetingsFixture(&org.File{Path: "meeting-tags.org", Headlines: []*org.Headline{record}})
	m := New(ws)
	m.switchToView(meetingTagsView)

	for _, r := range m.rows {
		if r.file != nil {
			t.Errorf("rows = %+v, want no file-header row", m.rows)
		}
	}
}

// TestMeetingTagsViewNestedEventVisualIndentIsCappedAtOneLevel: even
// though a nested event's row.level is genuinely one deeper than its
// record's (so "l"/"h" treat it as a child — see
// TestMeetingTagsViewLAndHNavigateToAndFromNestedEvent), its rendered
// indent (renderCalendarItemRowWithBg) is capped at a single level
// rather than scaling with row.level, and the record's own row
// (renderMeetingTagsRecordRowWithBg) omits the indent/fold columns
// entirely — so the gap between them is exactly those omitted columns'
// width (indent + fold + space = 4), not something that keeps growing
// with how deeply either one happens to be nested.
func TestMeetingTagsViewNestedEventVisualIndentIsCappedAtOneLevel(t *testing.T) {
	now := time.Now()
	event := oneOffCalendarEventHeadline("kickoff-1", "Client Kickoff", now, now.Add(time.Hour))
	record := &org.Headline{Level: 1, Title: "Client Kickoff", Tags: []string{"bob_project"}}
	record.SetProperty("MEETING_TAG_EVENT_IDS", "kickoff-1")
	ws := meetingsFixture(
		&org.File{Path: "calendar.org", Headlines: []*org.Headline{event}},
		&org.File{Path: "meeting-tags.org", Headlines: []*org.Headline{record}},
	)
	m := New(ws)
	m.switchToView(meetingTagsView)
	if len(m.rows) != 2 {
		t.Fatalf("rows = %+v, want exactly [record, nested event]", m.rows)
	}
	if m.rows[1].level != m.rows[0].level+1 {
		t.Fatalf("nested event's row.level = %d, want %d (record's %d, one deeper, for l/h)", m.rows[1].level, m.rows[0].level+1, m.rows[0].level)
	}

	recordPrefix := contentStartColumn(stripANSI(m.renderRow(m.rows[0])))
	eventPrefix := contentStartColumn(stripANSI(m.renderRow(m.rows[1])))
	if eventPrefix != recordPrefix+4 {
		t.Errorf("nested event's gutter width = %d, want %d (record's %d, plus the indent/fold/space columns the record's own compact row omits)", eventPrefix, recordPrefix+4, recordPrefix)
	}
}

// TestMeetingTagsViewLAndHNavigateToAndFromNestedEvent: since a nested
// event's row.level is one deeper than its record's (see
// appendMeetingTagsHeadlines), "l"/"h" (moveDeeper/moveShallower) step
// between them exactly as they would between a headline and its real
// child anywhere else in the outline.
func TestMeetingTagsViewLAndHNavigateToAndFromNestedEvent(t *testing.T) {
	now := time.Now()
	event := oneOffCalendarEventHeadline("kickoff-1", "Client Kickoff", now, now.Add(time.Hour))
	record := &org.Headline{Level: 1, Title: "Client Kickoff", Tags: []string{"bob_project"}}
	record.SetProperty("MEETING_TAG_EVENT_IDS", "kickoff-1")
	ws := meetingsFixture(
		&org.File{Path: "calendar.org", Headlines: []*org.Headline{event}},
		&org.File{Path: "meeting-tags.org", Headlines: []*org.Headline{record}},
	)
	m := New(ws)
	m.switchToView(meetingTagsView)
	m.cursor = findRow(t, m, "Client Kickoff") // the record's own row, listed first

	m = sendKey(m, "l")
	if m.currentHeadline() != event {
		t.Fatalf("after l, currentHeadline = %v, want the nested event %v", m.currentHeadline(), event)
	}

	m = sendKey(m, "h")
	if m.currentHeadline() != record {
		t.Errorf("after h, currentHeadline = %v, want back on the record %v", m.currentHeadline(), record)
	}
}

// TestMeetingTagsViewRecordRowOmitsIndentAndFoldColumns: a
// meeting-tags.org record's own row (renderMeetingTagsRecordRowWithBg)
// goes straight from its gutter (mark/lock/meeting/dirty) to its title —
// no indent or fold column reserved, unlike a plain headline row
// elsewhere in the app.
func TestMeetingTagsViewRecordRowOmitsIndentAndFoldColumns(t *testing.T) {
	record := &org.Headline{Level: 1, Title: "Client Kickoff", Tags: []string{"bob_project"}}
	record.SetProperty("MEETING_TAG_EVENT_IDS", "kickoff-1")
	ws := meetingsFixture(&org.File{Path: "meeting-tags.org", Headlines: []*org.Headline{record}})
	m := New(ws)
	m.switchToView(meetingTagsView)
	if len(m.rows) != 1 {
		t.Fatalf("rows = %+v, want just the record", m.rows)
	}
	if !m.rows[0].isMeetingTagsRecordRow {
		t.Fatalf("record row isn't marked isMeetingTagsRecordRow")
	}

	// mark(1) + lock(1) + meeting(1) + dirty(1) + space(1) = 5, then the
	// title — no indent, no fold column.
	if got := contentStartColumn(stripANSI(m.renderRow(m.rows[0]))); got != 5 {
		t.Errorf("record row content starts at column %d, want 5 (gutter only, no indent/fold)", got)
	}
}

// contentStartColumn returns the rune index of the first letter or digit
// in s — the point where a row's gutter/indent columns end and its own
// text (a time, or a title) begins. Unlike counting leading spaces, this
// isn't thrown off by a non-blank gutter glyph (e.g. "▣"), which occupies
// its column's width without being a space or the row's actual content.
func contentStartColumn(s string) int {
	for i, r := range []rune(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return i
		}
	}
	return -1
}
