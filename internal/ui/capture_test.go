package ui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sburnett/orgtd/internal/org"
)

// commitCaptureRollback simulates a :capture/gC session ending with an
// empty result (rollback), preserving originFile the way startCapture
// itself would have recorded it — needed to test rollback focus
// specifically, since commitTentative (shared with the o/O tests, which
// never need originFile since their insertion target is always wherever
// the cursor already was) doesn't set it.
func commitCaptureRollback(t *testing.T, m Model, origin *org.Headline, originFile *org.File) Model {
	t.Helper()
	tentative := m.currentHeadline()
	if tentative == nil {
		t.Fatalf("cursor is not on a headline")
	}
	f, parent, idx := m.insertPosition(tentative)
	ctx := insertContext{f: f, parent: parent, index: idx, origin: origin, originFile: originFile}
	path := writeTempOrgFile(t, "")
	updated, _ := m.Update(editFinishedMsg{path: path, target: tentative, insert: &ctx})
	return updated.(Model)
}

func TestCaptureAppendsToEndOfInbox(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Learn Go generics") // a top-level headline in a different file
	lastInboxIdx := findRow(t, m, "Follow up with finance about the Q3 budget doc")

	m = sendKey(m, "g")
	m = sendKey(m, "C")

	tentative := m.currentHeadline()
	if tentative == nil {
		t.Fatalf("expected cursor to move to a new tentative headline")
	}
	if tentative.Level != 1 || tentative.Parent != nil {
		t.Errorf("tentative = level %d parent %v, want a top-level (level 1, nil parent) headline", tentative.Level, tentative.Parent)
	}
	if m.cursor != lastInboxIdx+1 {
		t.Errorf("tentative row = %d, want %d (right after the last existing inbox item)", m.cursor, lastInboxIdx+1)
	}
	f := m.fileForHeadline(tentative)
	if f == nil || !strings.HasSuffix(f.Path, "inbox.org") {
		t.Errorf("captured headline's file = %v, want inbox.org", f)
	}
	if f.Headlines[len(f.Headlines)-1] != tentative {
		t.Errorf("tentative is not the last top-level headline in the inbox file")
	}
}

func TestCapturePrefillsCreatedProperty(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Learn Go generics")

	m = sendKey(m, "g")
	m = sendKey(m, "C")

	tentative := m.currentHeadline()
	if _, ok := tentative.Properties["CREATED"]; !ok {
		t.Errorf("captured headline has no CREATED property: %#v", tentative.Properties)
	}
}

// calendarEventHeadline builds a top-level headline shaped like one
// :sync-calendar would write to calendar.org (see
// internal/calendarsync/convert.go): a GCAL_EVENT_ID plus GCAL_START/GCAL_END
// bracketing the instant a meeting runs from start to end, plus a
// synthetic GCAL_HTML_LINK, the same as a real synced event.
// GCAL_SELF_RESPONSE_STATUS defaults to "accepted" (the common case, and
// what a real self-organized or confirmed event carries) — see
// withResponseStatus to override it for a test that specifically needs
// a "maybe" or not-yet-responded invite.
func calendarEventHeadline(id string, start, end time.Time) *org.Headline {
	h := &org.Headline{Level: 1, Title: "Meeting " + id}
	h.SetProperty("GCAL_EVENT_ID", id)
	h.SetProperty("GCAL_START", start.Format(time.RFC3339))
	h.SetProperty("GCAL_END", end.Format(time.RFC3339))
	h.SetProperty("GCAL_HTML_LINK", "https://calendar.google.com/event?eid="+id)
	h.SetProperty("GCAL_SELF_RESPONSE_STATUS", "accepted")
	return h
}

// withResponseStatus overrides h's GCAL_SELF_RESPONSE_STATUS (set to
// "accepted" by calendarEventHeadline above by default) — for a test
// that needs a meeting the picker sees as merely invited, not
// confirmed, e.g. to verify meetingPickerLess's in-progress boost
// requires acceptance, not just an invite.
func withResponseStatus(h *org.Headline, status string) *org.Headline {
	h.SetProperty("GCAL_SELF_RESPONSE_STATUS", status)
	return h
}

// TestCaptureDoesNotAttachCalendarMeetingInfo is a regression test for
// an earlier version of capture that auto-attached whatever meeting
// happened to be in progress at the moment of capture (GCAL_EVENT_LINKS/
// GCAL_RECURRING_EVENT_IDS/GCAL_RECURRING_EVENT_LINKS, plus a body
// comment). That was removed in favor of "gM" (see meeting_picker_test.go)
// — an interactive, deliberate way to attach a meeting — since capture
// itself isn't interactive: a wrong guess could only be fixed by hand,
// which defeats the point of automating it. This covers both a one-off
// and a recurring meeting in progress at capture time, and (since it's
// the same underlying insertHeadlineAt) o/O alongside gC for good
// measure.
func TestCaptureDoesNotAttachCalendarMeetingInfo(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	ws.Files = append(ws.Files, &org.File{
		Path: filepath.Join(ws.Dir, "calendar.org"),
		Headlines: []*org.Headline{
			calendarEventHeadline("one-off", now.Add(-30*time.Minute), now.Add(30*time.Minute)),
			recurringCalendarEventHeadline("instance-1", "series-abc", "Weekly Standup", now.Add(-30*time.Minute), now.Add(30*time.Minute)),
		},
	})

	for _, key := range []string{"C", "o"} {
		m := New(ws)
		m.cursor = findRow(t, m, "Learn Go generics")
		if key == "C" {
			m = sendKey(m, "g")
		}
		m = sendKey(m, key)

		tentative := m.currentHeadline()
		for _, prop := range []string{"GCAL_EVENT_IDS", "GCAL_EVENT_LINKS", "GCAL_RECURRING_EVENT_IDS", "GCAL_RECURRING_EVENT_LINKS"} {
			if v, ok := tentative.Properties[prop]; ok {
				t.Errorf("key %q: %s = %q, want no such property", key, prop, v)
			}
		}
		if len(tentative.Body) != 0 {
			t.Errorf("key %q: Body = %v, want empty", key, tentative.Body)
		}
	}
}

func TestCaptureCommandMatchesKeybinding(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Learn Go generics")
	lastInboxIdx := findRow(t, m, "Follow up with finance about the Q3 budget doc")

	m = sendKey(m, ":")
	m = typeKeys(m, "capture")
	m, _ = sendKeyCmd(m, "enter")

	tentative := m.currentHeadline()
	if tentative == nil {
		t.Fatalf("expected cursor to move to a new tentative headline")
	}
	if m.cursor != lastInboxIdx+1 {
		t.Errorf(":capture row = %d, want %d (right after the last existing inbox item)", m.cursor, lastInboxIdx+1)
	}
}

func TestCaptureWorksFromAgendaView(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.switchToView(agendaView)
	before := len(findInboxHeadlines(t, m))

	m = sendKey(m, "g")
	m = sendKey(m, "C")

	after := findInboxHeadlines(t, m)
	if len(after) != before+1 {
		t.Fatalf("inbox headline count = %d, want %d", len(after), before+1)
	}
	if _, ok := after[len(after)-1].Properties["CREATED"]; !ok {
		t.Errorf("captured headline (from agenda view) has no CREATED property")
	}
}

func TestCaptureWorksFromClarifyView(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.enterClarifyView()
	before := len(findInboxHeadlines(t, m))

	m = sendKey(m, "g")
	m = sendKey(m, "C")

	after := findInboxHeadlines(t, m)
	if len(after) != before+1 {
		t.Fatalf("inbox headline count = %d, want %d", len(after), before+1)
	}
}

// commitCapture simulates a :capture/gC session's editor finishing with
// body, preserving switchToOutline the way startCaptureImpl itself
// records it (see insertContext.switchToOutline) — mirrors
// commitCaptureAndPickMeeting (meeting_picker_test.go), which
// additionally sets thenPickMeeting for "gX". Unlike commitTentative/
// commitCaptureRollback, it locates the tentative headline via the
// inbox file directly rather than m.currentHeadline(): from a view
// whose rows aren't the outline's (agenda, calendar, ...), the cursor
// never actually lands on the tentative placeholder — see
// usesOutlineRows — the same as a real capture session, where nothing
// depends on it since $EDITOR covers the screen in the meantime.
func commitCapture(t *testing.T, m Model, body string) Model {
	t.Helper()
	inbox := findInboxHeadlines(t, m)
	if len(inbox) == 0 {
		t.Fatalf("no tentative headline in the inbox file")
	}
	tentative := inbox[len(inbox)-1]
	f, parent, idx := m.insertPosition(tentative)
	ctx := insertContext{f: f, parent: parent, index: idx, switchToOutline: true}
	path := writeTempOrgFile(t, body)
	updated, _ := m.Update(editFinishedMsg{path: path, target: tentative, insert: &ctx})
	return updated.(Model)
}

// TestCaptureFromAgendaViewSwitchesToOutline is a regression test for
// capture leaving the cursor stranded on whatever agenda row it
// happened to be on: agendaView's rows don't include the freshly
// captured (dateless, keyword-less) inbox entry at all, so
// commitInsert's own focusHeadline can't find it there — capture must
// switch to outline view itself once the editor session commits (see
// insertContext.switchToOutline/usesOutlineRows) so the new entry is
// immediately in view, ready for further edits (scheduling, tagging,
// promoting, ...).
func TestCaptureFromAgendaViewSwitchesToOutline(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.switchToView(agendaView)

	m = sendKey(m, "g")
	m = sendKey(m, "C")
	m = commitCapture(t, m, "Buy stamps\n")

	if m.view != outlineView {
		t.Fatalf("view after capture = %v, want outlineView", m.view)
	}
	h := m.currentHeadline()
	if h == nil || h.Title != "Buy stamps" {
		t.Errorf("currentHeadline = %+v, want the just-captured entry", h)
	}
}

// TestCaptureFromCalendarViewSwitchesToOutline is the same regression
// as TestCaptureFromAgendaViewSwitchesToOutline above, but for
// calendarView — whose rows are calendar.org's synced events, not the
// outline's headlines at all.
func TestCaptureFromCalendarViewSwitchesToOutline(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.switchToView(calendarView)

	m = sendKey(m, "g")
	m = sendKey(m, "C")
	m = commitCapture(t, m, "Buy stamps\n")

	if m.view != outlineView {
		t.Fatalf("view after capture = %v, want outlineView", m.view)
	}
	h := m.currentHeadline()
	if h == nil || h.Title != "Buy stamps" {
		t.Errorf("currentHeadline = %+v, want the just-captured entry", h)
	}
}

// TestCaptureFromClarifyViewStaysInClarify checks the flip side of the
// two tests above: clarifyView's rows are the very same full-outline
// listing outlineView itself shows (see usesOutlineRows) — plus a
// pinned info-buffer panel — so the newly captured entry is already
// right there under the cursor once the editor session commits, with
// no need (and no reason) to switch views out from under an in-progress
// clarify session.
func TestCaptureFromClarifyViewStaysInClarify(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.enterClarifyView()

	m = sendKey(m, "g")
	m = sendKey(m, "C")
	m = commitCapture(t, m, "Buy stamps\n")

	if m.view != clarifyView {
		t.Fatalf("view after capture = %v, want clarifyView (unchanged)", m.view)
	}
	h := m.currentHeadline()
	if h == nil || h.Title != "Buy stamps" {
		t.Errorf("currentHeadline = %+v, want the just-captured entry", h)
	}
}

func findInboxHeadlines(t *testing.T, m Model) []*org.Headline {
	t.Helper()
	for _, f := range m.ws.Files {
		if strings.HasSuffix(f.Path, "inbox.org") {
			return f.Headlines
		}
	}
	t.Fatalf("no inbox.org file in workspace")
	return nil
}

func TestCaptureNoInboxFileShowsMessage(t *testing.T) {
	ws := agendaFixture(t, "* TODO Some item\n") // file named "agenda.org", not inbox.org
	m := New(ws)                                 // default inboxFile is "inbox.org" -> not present

	before := len(m.rows)
	m = sendKey(m, "g")
	m = sendKey(m, "C")

	if len(m.rows) != before {
		t.Errorf("rows changed = %d, want unchanged %d (nothing to capture into)", len(m.rows), before)
	}
	if m.message == "" {
		t.Error("expected a message explaining there's no inbox file")
	}
}

func TestCaptureRollbackReturnsToOriginHeadline(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Learn Go generics")
	orig := m.currentHeadline()

	m = sendKey(m, "g")
	m = sendKey(m, "C")
	m = commitCaptureRollback(t, m, orig, nil)

	if h := m.currentHeadline(); h != orig {
		t.Errorf("cursor after rollback = %v, want back on %q", h, orig.Title)
	}
}

func TestCaptureRollbackReturnsToOriginFileRowNotInbox(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	fileIdx := findFileRow(t, m, "projects.org")
	m.cursor = fileIdx
	projectsFile := m.rows[fileIdx].file

	m = sendKey(m, "g")
	m = sendKey(m, "C")
	// origin is nil (cursor was on a file row, not a headline); origin
	// file must be the file the cursor was actually on (projects.org),
	// not insertContext.f (the inbox — capture's fixed destination,
	// which differs from wherever the cursor was here). This is exactly
	// what currentRowFile/originFile exist to get right.
	m = commitCaptureRollback(t, m, nil, projectsFile)

	if m.cursor != fileIdx {
		t.Errorf("cursor after rollback = %d, want %d (back on projects.org's file row, not the inbox)", m.cursor, fileIdx)
	}
}
