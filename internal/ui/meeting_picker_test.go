package ui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sburnett/orgtd/internal/org"
)

// commitCaptureAndPickMeeting simulates a "gX" capture session's editor
// finishing with body, preserving thenPickMeeting the way
// startCaptureAndPickMeeting itself records it — mirrors commitTentative
// (insert_test.go)/commitCaptureRollback (capture_test.go), neither of
// which sets thenPickMeeting.
func commitCaptureAndPickMeeting(t *testing.T, m Model, body string) Model {
	t.Helper()
	tentative := m.currentHeadline()
	if tentative == nil {
		t.Fatalf("cursor is not on a headline")
	}
	f, parent, idx := m.insertPosition(tentative)
	ctx := insertContext{f: f, parent: parent, index: idx, thenPickMeeting: true}
	path := writeTempOrgFile(t, body)
	updated, _ := m.Update(editFinishedMsg{path: path, target: tentative, insert: &ctx})
	return updated.(Model)
}

func TestGXOpensMeetingPickerAfterCaptureCommits(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	ws.Files = append(ws.Files, &org.File{
		Path: filepath.Join(ws.Dir, "calendar.org"),
		Headlines: []*org.Headline{
			recurringCalendarEventHeadline("standup-1", "series-standup", "Weekly Standup", now.Add(time.Hour), now.Add(90*time.Minute)),
		},
	})
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")

	m = sendKey(m, "g")
	m = sendKey(m, "X")
	captured := m.currentHeadline()
	if captured == nil {
		t.Fatalf("gX didn't start a capture (no tentative headline)")
	}

	m = commitCaptureAndPickMeeting(t, m, "* Discuss rollout plan\n")

	if m.mode != meetingPickerMode {
		t.Fatalf("mode = %v, want meetingPickerMode after capture commits", m.mode)
	}
	target := m.currentHeadline()
	if target == nil || target.Title != "Discuss rollout plan" {
		t.Fatalf("meeting picker's target = %+v, want the just-captured entry", target)
	}

	m, _ = sendKeyCmd(m, "enter")
	if got := target.Properties["GCAL_RECURRING_EVENT_IDS"]; got != "series-standup" {
		t.Errorf("GCAL_RECURRING_EVENT_IDS = %q, want %q", got, "series-standup")
	}
}

func TestGXWithNoRecurringMeetingsStillCapturesButSkipsPicker(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")

	m = sendKey(m, "g")
	m = sendKey(m, "X")
	m = commitCaptureAndPickMeeting(t, m, "* Discuss rollout plan\n")

	if m.mode == meetingPickerMode {
		t.Fatalf("meeting picker opened with nothing to offer")
	}
	if m.currentHeadline() == nil || m.currentHeadline().Title != "Discuss rollout plan" {
		t.Errorf("capture itself should still have committed; currentHeadline = %+v", m.currentHeadline())
	}
	if m.message == "" {
		t.Errorf("expected a status message explaining there's nothing to attach")
	}
}

func TestGXCancelledCaptureNeverOpensPicker(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	ws.Files = append(ws.Files, &org.File{
		Path: filepath.Join(ws.Dir, "calendar.org"),
		Headlines: []*org.Headline{
			recurringCalendarEventHeadline("standup-1", "series-standup", "Weekly Standup", now.Add(time.Hour), now.Add(90*time.Minute)),
		},
	})
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")

	m = sendKey(m, "g")
	m = sendKey(m, "X")
	m = commitCaptureAndPickMeeting(t, m, "") // empty body: rollback, not commit

	if m.mode == meetingPickerMode {
		t.Fatalf("meeting picker opened even though the capture itself was cancelled")
	}
}

func TestGXIsTwoKeyChordNotSingleG(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")

	m = sendKey(m, "g")
	if m.mode == meetingPickerMode {
		t.Fatalf("single g opened anything")
	}
	if !m.pendingG {
		t.Errorf("expected pendingG after a single g")
	}
}

func TestGMWithNoRecurringMeetingsShowsMessage(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")

	m = sendKey(m, "g")
	m = sendKey(m, "M")

	if m.mode != normalMode {
		t.Fatalf("mode = %v, want normalMode (nothing to pick)", m.mode)
	}
	if m.message == "" {
		t.Errorf("expected a status message explaining there's nothing to attach")
	}
}

func TestGMIsTwoKeyChordNotSingleG(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")

	m = sendKey(m, "g")
	if m.mode == meetingPickerMode {
		t.Fatalf("single g opened the meeting picker")
	}
	if !m.pendingG {
		t.Errorf("expected pendingG after a single g")
	}
}

func TestGMOpensPickerWithDedupedRecurringSeries(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	ws.Files = append(ws.Files, &org.File{
		Path: filepath.Join(ws.Dir, "calendar.org"),
		Headlines: []*org.Headline{
			recurringCalendarEventHeadline("standup-1", "series-standup", "Weekly Standup", now.Add(time.Hour), now.Add(90*time.Minute)),
			recurringCalendarEventHeadline("standup-2", "series-standup", "Weekly Standup", now.Add(7*24*time.Hour), now.Add(7*24*time.Hour+30*time.Minute)),
			recurringCalendarEventHeadline("planning-1", "series-planning", "Sprint Planning", now.Add(2*time.Hour), now.Add(3*time.Hour)),
		},
	})
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")

	m = sendKey(m, "g")
	m = sendKey(m, "M")

	if m.mode != meetingPickerMode {
		t.Fatalf("mode = %v, want meetingPickerMode", m.mode)
	}
	if len(m.meetingPickerCandidates) != 2 {
		t.Fatalf("candidates = %d, want 2 (deduped by series)", len(m.meetingPickerCandidates))
	}
	// Chronological: Standup's soonest instance (in 1h) sorts before
	// Planning's (in 2h).
	if m.meetingPickerCandidates[0].id != "series-standup" {
		t.Errorf("candidates[0] = %+v, want series-standup first (sooner)", m.meetingPickerCandidates[0])
	}
	if m.meetingPickerCandidates[1].id != "series-planning" {
		t.Errorf("candidates[1] = %+v, want series-planning second", m.meetingPickerCandidates[1])
	}
}

// TestGMDefaultPrefersInProgressInstanceOverAFutureInstanceOfSameSeries
// is a regression test for a real bug: a daily recurring series
// typically has more than one instance synced at once (today's plus
// tomorrow's, say), and picking the series' single "representative"
// occurrence by start time alone ("soonest upcoming wins") treated
// today's already-started occurrence as simply "past", losing it to
// tomorrow's not-yet-started one — so the series never registered as
// in progress at all, and "gM" defaulted to the next meeting instead of
// the one actually happening right now.
func TestGMDefaultPrefersInProgressInstanceOverAFutureInstanceOfSameSeries(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	ws.Files = append(ws.Files, &org.File{
		Path: filepath.Join(ws.Dir, "calendar.org"),
		Headlines: []*org.Headline{
			// series-standup has two instances synced at once: today's,
			// already in progress (started 5m ago, ends in 25m), and
			// tomorrow's, not yet started.
			recurringCalendarEventHeadline("standup-today", "series-standup", "Weekly Standup", now.Add(-5*time.Minute), now.Add(25*time.Minute)),
			recurringCalendarEventHeadline("standup-tomorrow", "series-standup", "Weekly Standup", now.Add(24*time.Hour), now.Add(24*time.Hour+30*time.Minute)),
			// A second series, further out and not in progress, to
			// confirm the in-progress one still sorts ahead of it.
			recurringCalendarEventHeadline("planning-1", "series-planning", "Sprint Planning", now.Add(2*time.Hour), now.Add(3*time.Hour)),
		},
	})
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")

	m = sendKey(m, "g")
	m = sendKey(m, "M")

	if len(m.meetingPickerCandidates) != 2 {
		t.Fatalf("candidates = %d, want 2 (deduped by series)", len(m.meetingPickerCandidates))
	}
	if got := m.meetingPickerCandidates[0].id; got != "series-standup" {
		t.Fatalf("candidates[0] = %q, want series-standup (its today instance is in progress)", got)
	}
	// GCAL_START round-trips through RFC3339 (second precision), so
	// compare at that resolution rather than requiring an exact Equal.
	if when := m.meetingPickerCandidates[0].when; when.Unix() != now.Add(-5*time.Minute).Unix() {
		t.Errorf("candidates[0].when = %v, want today's already-started instance (%v), not tomorrow's", when, now.Add(-5*time.Minute))
	}
}

// TestGMDefaultsToShortestInProgressMeeting covers the "gM" default
// selection policy: when more than one candidate's representative
// occurrence is currently in progress, the shortest one (soonest to
// end) is highlighted first — not just whichever started or was synced
// first — so a quick standup you're nominally "in" right now doesn't
// get buried under an hours-long meeting that's also technically
// ongoing.
func TestGMDefaultsToShortestInProgressMeeting(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	ws.Files = append(ws.Files, &org.File{
		Path: filepath.Join(ws.Dir, "calendar.org"),
		Headlines: []*org.Headline{
			// Long meeting: started 2h ago, ends in 1h (3h total) — in progress.
			recurringCalendarEventHeadline("offsite-1", "series-offsite", "Team Offsite", now.Add(-2*time.Hour), now.Add(time.Hour)),
			// Short meeting: started 10m ago, ends in 5m (15m total) — also in progress, and shorter.
			recurringCalendarEventHeadline("standup-1", "series-standup", "Weekly Standup", now.Add(-10*time.Minute), now.Add(5*time.Minute)),
			// Not in progress yet: starts in 20 minutes.
			recurringCalendarEventHeadline("planning-1", "series-planning", "Sprint Planning", now.Add(20*time.Minute), now.Add(50*time.Minute)),
		},
	})
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")

	m = sendKey(m, "g")
	m = sendKey(m, "M")

	if len(m.meetingPickerCandidates) != 3 {
		t.Fatalf("candidates = %d, want 3", len(m.meetingPickerCandidates))
	}
	if got := m.meetingPickerCandidates[0].id; got != "series-standup" {
		t.Errorf("candidates[0] = %q, want series-standup (shortest in-progress meeting)", got)
	}
	if got := m.meetingPickerCandidates[1].id; got != "series-offsite" {
		t.Errorf("candidates[1] = %q, want series-offsite (longer, but still in progress)", got)
	}
	if got := m.meetingPickerCandidates[2].id; got != "series-planning" {
		t.Errorf("candidates[2] = %q, want series-planning last (not in progress)", got)
	}
}

// TestGMDefaultsToNextStartTimeWhenNothingInProgress mirrors
// TestGMOpensPickerWithDedupedRecurringSeries but names the policy
// explicitly: with no candidate currently in progress, the default
// falls back to whichever starts soonest.
func TestGMDefaultsToNextStartTimeWhenNothingInProgress(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	ws.Files = append(ws.Files, &org.File{
		Path: filepath.Join(ws.Dir, "calendar.org"),
		Headlines: []*org.Headline{
			recurringCalendarEventHeadline("planning-1", "series-planning", "Sprint Planning", now.Add(2*time.Hour), now.Add(3*time.Hour)),
			recurringCalendarEventHeadline("standup-1", "series-standup", "Weekly Standup", now.Add(time.Hour), now.Add(90*time.Minute)),
		},
	})
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")

	m = sendKey(m, "g")
	m = sendKey(m, "M")

	if got := m.meetingPickerCandidates[0].id; got != "series-standup" {
		t.Errorf("candidates[0] = %q, want series-standup (starts sooner, nothing in progress)", got)
	}
}

func TestGMFilterNarrowsBySubstring(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	ws.Files = append(ws.Files, &org.File{
		Path: filepath.Join(ws.Dir, "calendar.org"),
		Headlines: []*org.Headline{
			recurringCalendarEventHeadline("standup-1", "series-standup", "Weekly Standup", now.Add(time.Hour), now.Add(90*time.Minute)),
			recurringCalendarEventHeadline("planning-1", "series-planning", "Sprint Planning", now.Add(2*time.Hour), now.Add(3*time.Hour)),
		},
	})
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	m = sendKey(m, "g")
	m = sendKey(m, "M")

	m = typeKeys(m, "plan")

	matches := filteredMeetingCandidates(m.meetingPickerCandidates, m.meetingPickerFilter)
	if len(matches) != 1 || matches[0].id != "series-planning" {
		t.Fatalf("filtered matches = %+v, want just series-planning", matches)
	}
}

func TestGMEnterAttachesHighlightedMeeting(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	ws.Files = append(ws.Files, &org.File{
		Path: filepath.Join(ws.Dir, "calendar.org"),
		Headlines: []*org.Headline{
			recurringCalendarEventHeadline("standup-1", "series-standup", "Weekly Standup", now.Add(time.Hour), now.Add(90*time.Minute)),
		},
	})
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	target := m.currentHeadline()

	m = sendKey(m, "g")
	m = sendKey(m, "M")
	m, _ = sendKeyCmd(m, "enter")

	if m.mode != normalMode {
		t.Fatalf("mode = %v, want normalMode after Enter", m.mode)
	}
	if got := target.Properties["GCAL_RECURRING_EVENT_IDS"]; got != "series-standup" {
		t.Errorf("GCAL_RECURRING_EVENT_IDS = %q, want %q", got, "series-standup")
	}
	wantLink := "[[https://calendar.google.com/event?eid=standup-1][Weekly Standup]]"
	if got := target.Properties["GCAL_RECURRING_EVENT_LINKS"]; got != wantLink {
		t.Errorf("GCAL_RECURRING_EVENT_LINKS = %q, want %q", got, wantLink)
	}
}

// TestGMOffersOneOffEvent covers the reported gap: a one-time (no
// GCAL_RECURRING_EVENT_ID) calendar event should still show up as a
// "gM" candidate, keyed by its own GCAL_EVENT_ID rather than a series
// ID.
func TestGMOffersOneOffEvent(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	ws.Files = append(ws.Files, &org.File{
		Path: filepath.Join(ws.Dir, "calendar.org"),
		Headlines: []*org.Headline{
			oneOffCalendarEventHeadline("kickoff-1", "Client Kickoff", now.Add(time.Hour), now.Add(2*time.Hour)),
		},
	})
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")

	m = sendKey(m, "g")
	m = sendKey(m, "M")

	if m.mode != meetingPickerMode {
		t.Fatalf("mode = %v, want meetingPickerMode", m.mode)
	}
	if len(m.meetingPickerCandidates) != 1 {
		t.Fatalf("candidates = %d, want 1", len(m.meetingPickerCandidates))
	}
	c := m.meetingPickerCandidates[0]
	if c.title != "Client Kickoff" {
		t.Errorf("candidate title = %q, want %q", c.title, "Client Kickoff")
	}
	if c.id != "kickoff-1" {
		t.Errorf("candidate id = %q, want the event's own GCAL_EVENT_ID %q", c.id, "kickoff-1")
	}
	if c.kind != oneOffMeeting {
		t.Errorf("candidate kind = %v, want oneOffMeeting", c.kind)
	}
}

// TestGMAttachOneOffEventUsesEventProperties is the write-side
// counterpart to TestGMOffersOneOffEvent: attaching a one-off event
// must land on GCAL_EVENT_IDS/GCAL_EVENT_LINKS, not the
// GCAL_RECURRING_EVENT_IDS/LINKS pair a recurring series uses — the two
// are independent, so an entry could in principle be attached to both a
// recurring series and an unrelated one-off event at once.
func TestGMAttachOneOffEventUsesEventProperties(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	ws.Files = append(ws.Files, &org.File{
		Path: filepath.Join(ws.Dir, "calendar.org"),
		Headlines: []*org.Headline{
			oneOffCalendarEventHeadline("kickoff-1", "Client Kickoff", now.Add(time.Hour), now.Add(2*time.Hour)),
		},
	})
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	target := m.currentHeadline()

	m = sendKey(m, "g")
	m = sendKey(m, "M")
	m, _ = sendKeyCmd(m, "enter")

	if got := target.Properties["GCAL_EVENT_IDS"]; got != "kickoff-1" {
		t.Errorf("GCAL_EVENT_IDS = %q, want %q", got, "kickoff-1")
	}
	wantLink := "[[https://calendar.google.com/event?eid=kickoff-1][Client Kickoff]]"
	if got := target.Properties["GCAL_EVENT_LINKS"]; got != wantLink {
		t.Errorf("GCAL_EVENT_LINKS = %q, want %q", got, wantLink)
	}
	if _, ok := target.Properties["GCAL_RECURRING_EVENT_IDS"]; ok {
		t.Errorf("GCAL_RECURRING_EVENT_IDS = %q, want untouched (this is a one-off event)", target.Properties["GCAL_RECURRING_EVENT_IDS"])
	}
}

// TestGMEnterOnAttachedOneOffEventDetaches mirrors
// TestGMEnterOnAttachedMeetingDetaches for a one-off event.
func TestGMEnterOnAttachedOneOffEventDetaches(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	ws.Files = append(ws.Files, &org.File{
		Path: filepath.Join(ws.Dir, "calendar.org"),
		Headlines: []*org.Headline{
			oneOffCalendarEventHeadline("kickoff-1", "Client Kickoff", now.Add(time.Hour), now.Add(2*time.Hour)),
		},
	})
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	target := m.currentHeadline()
	target.SetProperty("GCAL_EVENT_IDS", "kickoff-1")
	target.SetProperty("GCAL_EVENT_LINKS", "[[https://calendar.google.com/event?eid=kickoff-1][Client Kickoff]]")

	m = sendKey(m, "g")
	m = sendKey(m, "M")
	m, _ = sendKeyCmd(m, "enter")

	if _, ok := target.Properties["GCAL_EVENT_IDS"]; ok {
		t.Errorf("GCAL_EVENT_IDS = %q, want removed after re-selecting an attached one-off event", target.Properties["GCAL_EVENT_IDS"])
	}
	if _, ok := target.Properties["GCAL_EVENT_LINKS"]; ok {
		t.Errorf("GCAL_EVENT_LINKS = %q, want removed along with the ID", target.Properties["GCAL_EVENT_LINKS"])
	}
}

// TestGMOneOffAndRecurringCandidatesCoexist confirms both kinds appear
// together in the same picker, independently selectable by title.
func TestGMOneOffAndRecurringCandidatesCoexist(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	ws.Files = append(ws.Files, &org.File{
		Path: filepath.Join(ws.Dir, "calendar.org"),
		Headlines: []*org.Headline{
			recurringCalendarEventHeadline("standup-1", "series-standup", "Weekly Standup", now.Add(time.Hour), now.Add(90*time.Minute)),
			oneOffCalendarEventHeadline("kickoff-1", "Client Kickoff", now.Add(2*time.Hour), now.Add(3*time.Hour)),
		},
	})
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	target := m.currentHeadline()

	m = sendKey(m, "g")
	m = sendKey(m, "M")
	if len(m.meetingPickerCandidates) != 2 {
		t.Fatalf("candidates = %d, want 2 (one recurring, one one-off)", len(m.meetingPickerCandidates))
	}

	m = typeKeys(m, "kickoff")
	matches := filteredMeetingCandidates(m.meetingPickerCandidates, m.meetingPickerFilter)
	if len(matches) != 1 || matches[0].title != "Client Kickoff" {
		t.Fatalf("filtered matches = %+v, want just Client Kickoff", matches)
	}
	m, _ = sendKeyCmd(m, "enter")

	if got := target.Properties["GCAL_EVENT_IDS"]; got != "kickoff-1" {
		t.Errorf("GCAL_EVENT_IDS = %q, want %q", got, "kickoff-1")
	}
	if _, ok := target.Properties["GCAL_RECURRING_EVENT_IDS"]; ok {
		t.Errorf("GCAL_RECURRING_EVENT_IDS = %q, want untouched (only the one-off event was picked)", target.Properties["GCAL_RECURRING_EVENT_IDS"])
	}
}

// TestGMAttachOneOffEventIsUndoable mirrors TestGMAttachIsUndoable for
// a one-off event.
func TestGMAttachOneOffEventIsUndoable(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	ws.Files = append(ws.Files, &org.File{
		Path: filepath.Join(ws.Dir, "calendar.org"),
		Headlines: []*org.Headline{
			oneOffCalendarEventHeadline("kickoff-1", "Client Kickoff", now.Add(time.Hour), now.Add(2*time.Hour)),
		},
	})
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	target := m.currentHeadline()

	m = sendKey(m, "g")
	m = sendKey(m, "M")
	m, _ = sendKeyCmd(m, "enter")
	if got := target.Properties["GCAL_EVENT_IDS"]; got != "kickoff-1" {
		t.Fatalf("setup: GCAL_EVENT_IDS = %q, want kickoff-1", got)
	}

	m = sendKey(m, "u")
	if _, ok := target.Properties["GCAL_EVENT_IDS"]; ok {
		t.Errorf("after undo, GCAL_EVENT_IDS = %q, want removed", target.Properties["GCAL_EVENT_IDS"])
	}
	if _, ok := target.Properties["GCAL_EVENT_LINKS"]; ok {
		t.Errorf("after undo, GCAL_EVENT_LINKS = %q, want removed too", target.Properties["GCAL_EVENT_LINKS"])
	}

	m = sendKey(m, "ctrl+r")
	if got := target.Properties["GCAL_EVENT_IDS"]; got != "kickoff-1" {
		t.Errorf("after redo, GCAL_EVENT_IDS = %q, want kickoff-1", got)
	}
	wantLink := "[[https://calendar.google.com/event?eid=kickoff-1][Client Kickoff]]"
	if got := target.Properties["GCAL_EVENT_LINKS"]; got != wantLink {
		t.Errorf("after redo, GCAL_EVENT_LINKS = %q, want %q", got, wantLink)
	}
}

func TestGMEnterOnAttachedMeetingDetaches(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	ws.Files = append(ws.Files, &org.File{
		Path: filepath.Join(ws.Dir, "calendar.org"),
		Headlines: []*org.Headline{
			recurringCalendarEventHeadline("standup-1", "series-standup", "Weekly Standup", now.Add(time.Hour), now.Add(90*time.Minute)),
		},
	})
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	target := m.currentHeadline()
	target.SetProperty("GCAL_RECURRING_EVENT_IDS", "series-standup")
	target.SetProperty("GCAL_RECURRING_EVENT_LINKS", "[[https://calendar.google.com/event?eid=standup-1][Weekly Standup]]")

	m = sendKey(m, "g")
	m = sendKey(m, "M")
	m, _ = sendKeyCmd(m, "enter")

	if _, ok := target.Properties["GCAL_RECURRING_EVENT_IDS"]; ok {
		t.Errorf("GCAL_RECURRING_EVENT_IDS = %q, want removed after re-selecting an attached meeting", target.Properties["GCAL_RECURRING_EVENT_IDS"])
	}
	if _, ok := target.Properties["GCAL_RECURRING_EVENT_LINKS"]; ok {
		t.Errorf("GCAL_RECURRING_EVENT_LINKS = %q, want removed along with the ID", target.Properties["GCAL_RECURRING_EVENT_LINKS"])
	}
}

// TestGMAttachDetachKeepsIDsAndLinksAligned attaches two series, then
// detaches the first one — the important correctness case for the
// index-aligned pairing buildMeetingAttachAction relies on: removing an
// entry from the middle (or start) of both lists must remove the right
// link, not just the right ID, leaving the second series' own link
// intact and still correctly paired with its ID.
func TestGMAttachDetachKeepsIDsAndLinksAligned(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	ws.Files = append(ws.Files, &org.File{
		Path: filepath.Join(ws.Dir, "calendar.org"),
		Headlines: []*org.Headline{
			recurringCalendarEventHeadline("standup-1", "series-standup", "Weekly Standup", now.Add(time.Hour), now.Add(90*time.Minute)),
			recurringCalendarEventHeadline("planning-1", "series-planning", "Sprint Planning", now.Add(2*time.Hour), now.Add(3*time.Hour)),
		},
	})
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	target := m.currentHeadline()

	// Attach standup, then planning (in that order).
	m = sendKey(m, "g")
	m = sendKey(m, "M")
	m = typeKeys(m, "standup")
	m, _ = sendKeyCmd(m, "enter")
	m = sendKey(m, "g")
	m = sendKey(m, "M")
	m = typeKeys(m, "planning")
	m, _ = sendKeyCmd(m, "enter")

	if got := target.Properties["GCAL_RECURRING_EVENT_IDS"]; got != "series-standup series-planning" {
		t.Fatalf("setup: GCAL_RECURRING_EVENT_IDS = %q, want series-standup series-planning", got)
	}

	// Detach standup (the first of the two) by re-selecting it.
	m = sendKey(m, "g")
	m = sendKey(m, "M")
	m = typeKeys(m, "standup")
	m, _ = sendKeyCmd(m, "enter")

	if got := target.Properties["GCAL_RECURRING_EVENT_IDS"]; got != "series-planning" {
		t.Errorf("GCAL_RECURRING_EVENT_IDS = %q, want just series-planning", got)
	}
	wantLink := "[[https://calendar.google.com/event?eid=planning-1][Sprint Planning]]"
	if got := target.Properties["GCAL_RECURRING_EVENT_LINKS"]; got != wantLink {
		t.Errorf("GCAL_RECURRING_EVENT_LINKS = %q, want just Sprint Planning's link (not Standup's, and not misaligned)", got)
	}
}

func TestGMEnterPreservesOtherAttachedMeetings(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	ws.Files = append(ws.Files, &org.File{
		Path: filepath.Join(ws.Dir, "calendar.org"),
		Headlines: []*org.Headline{
			recurringCalendarEventHeadline("standup-1", "series-standup", "Weekly Standup", now.Add(time.Hour), now.Add(90*time.Minute)),
			recurringCalendarEventHeadline("planning-1", "series-planning", "Sprint Planning", now.Add(2*time.Hour), now.Add(3*time.Hour)),
		},
	})
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	target := m.currentHeadline()
	target.SetProperty("GCAL_RECURRING_EVENT_IDS", "series-planning")

	m = sendKey(m, "g")
	m = sendKey(m, "M")
	m = typeKeys(m, "standup")
	m, _ = sendKeyCmd(m, "enter")

	got := target.Properties["GCAL_RECURRING_EVENT_IDS"]
	if got != "series-planning series-standup" && got != "series-standup series-planning" {
		t.Errorf("GCAL_RECURRING_EVENT_IDS = %q, want both series present", got)
	}
}

func TestGMEscCancelsWithoutChangingProperty(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	ws.Files = append(ws.Files, &org.File{
		Path: filepath.Join(ws.Dir, "calendar.org"),
		Headlines: []*org.Headline{
			recurringCalendarEventHeadline("standup-1", "series-standup", "Weekly Standup", now.Add(time.Hour), now.Add(90*time.Minute)),
		},
	})
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	target := m.currentHeadline()

	m = sendKey(m, "g")
	m = sendKey(m, "M")
	m, _ = sendKeyCmd(m, "esc")

	if m.mode != normalMode {
		t.Fatalf("mode = %v, want normalMode after Esc", m.mode)
	}
	if _, ok := target.Properties["GCAL_RECURRING_EVENT_IDS"]; ok {
		t.Errorf("GCAL_RECURRING_EVENT_IDS = %q, want untouched after Esc", target.Properties["GCAL_RECURRING_EVENT_IDS"])
	}
}

func TestGMAttachIsUndoable(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	ws.Files = append(ws.Files, &org.File{
		Path: filepath.Join(ws.Dir, "calendar.org"),
		Headlines: []*org.Headline{
			recurringCalendarEventHeadline("standup-1", "series-standup", "Weekly Standup", now.Add(time.Hour), now.Add(90*time.Minute)),
		},
	})
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	target := m.currentHeadline()

	m = sendKey(m, "g")
	m = sendKey(m, "M")
	m, _ = sendKeyCmd(m, "enter")
	if got := target.Properties["GCAL_RECURRING_EVENT_IDS"]; got != "series-standup" {
		t.Fatalf("setup: GCAL_RECURRING_EVENT_IDS = %q, want series-standup", got)
	}

	m = sendKey(m, "u")
	if _, ok := target.Properties["GCAL_RECURRING_EVENT_IDS"]; ok {
		t.Errorf("after undo, GCAL_RECURRING_EVENT_IDS = %q, want removed", target.Properties["GCAL_RECURRING_EVENT_IDS"])
	}
	if _, ok := target.Properties["GCAL_RECURRING_EVENT_LINKS"]; ok {
		t.Errorf("after undo, GCAL_RECURRING_EVENT_LINKS = %q, want removed too", target.Properties["GCAL_RECURRING_EVENT_LINKS"])
	}

	m = sendKey(m, "ctrl+r")
	if got := target.Properties["GCAL_RECURRING_EVENT_IDS"]; got != "series-standup" {
		t.Errorf("after redo, GCAL_RECURRING_EVENT_IDS = %q, want series-standup", got)
	}
	wantLink := "[[https://calendar.google.com/event?eid=standup-1][Weekly Standup]]"
	if got := target.Properties["GCAL_RECURRING_EVENT_LINKS"]; got != wantLink {
		t.Errorf("after redo, GCAL_RECURRING_EVENT_LINKS = %q, want %q", got, wantLink)
	}
}

func TestGMArrowKeysMoveHighlight(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	ws.Files = append(ws.Files, &org.File{
		Path: filepath.Join(ws.Dir, "calendar.org"),
		Headlines: []*org.Headline{
			recurringCalendarEventHeadline("standup-1", "series-standup", "Weekly Standup", now.Add(time.Hour), now.Add(90*time.Minute)),
			recurringCalendarEventHeadline("planning-1", "series-planning", "Sprint Planning", now.Add(2*time.Hour), now.Add(3*time.Hour)),
		},
	})
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	m = sendKey(m, "g")
	m = sendKey(m, "M")

	if m.meetingPickerIndex != 0 {
		t.Fatalf("initial meetingPickerIndex = %d, want 0", m.meetingPickerIndex)
	}
	m, _ = sendKeyCmd(m, "down")
	if m.meetingPickerIndex != 1 {
		t.Errorf("after down, meetingPickerIndex = %d, want 1", m.meetingPickerIndex)
	}
	m, _ = sendKeyCmd(m, "down")
	if m.meetingPickerIndex != 1 {
		t.Errorf("down past the last candidate = %d, want clamped to 1", m.meetingPickerIndex)
	}
	m, _ = sendKeyCmd(m, "up")
	if m.meetingPickerIndex != 0 {
		t.Errorf("after up, meetingPickerIndex = %d, want 0", m.meetingPickerIndex)
	}
}

func TestGMTypedLettersFilterRatherThanNavigate(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	ws.Files = append(ws.Files, &org.File{
		Path: filepath.Join(ws.Dir, "calendar.org"),
		Headlines: []*org.Headline{
			recurringCalendarEventHeadline("jam-1", "series-jam", "Jam session", now.Add(time.Hour), now.Add(90*time.Minute)),
		},
	})
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	m = sendKey(m, "g")
	m = sendKey(m, "M")

	// "j" and "k" must filter (a meeting title can legitimately contain
	// either), unlike the status picker where they navigate.
	m = typeKeys(m, "jam")
	if m.meetingPickerFilter != "jam" {
		t.Errorf("meetingPickerFilter = %q, want %q (j/k should filter here, not navigate)", m.meetingPickerFilter, "jam")
	}
	matches := filteredMeetingCandidates(m.meetingPickerCandidates, m.meetingPickerFilter)
	if len(matches) != 1 || matches[0].id != "series-jam" {
		t.Errorf("filtered matches = %+v, want just series-jam", matches)
	}
}

func TestRenderMeetingPickerShowsTitleDateAndAction(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	ws.Files = append(ws.Files, &org.File{
		Path: filepath.Join(ws.Dir, "calendar.org"),
		Headlines: []*org.Headline{
			recurringCalendarEventHeadline("standup-1", "series-standup", "Weekly Standup", now.Add(time.Hour), now.Add(90*time.Minute)),
		},
	})
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	m = sendKey(m, "g")
	m = sendKey(m, "M")

	rendered := stripANSI(m.renderMeetingPicker())
	if !strings.Contains(rendered, "Weekly Standup") {
		t.Errorf("rendered = %q, want the meeting title", rendered)
	}
	if !strings.Contains(rendered, "attaches") {
		t.Errorf("rendered = %q, want it to say Enter attaches (not yet attached)", rendered)
	}

	target := m.currentHeadline()
	target.SetProperty("GCAL_RECURRING_EVENT_IDS", "series-standup")
	rendered = stripANSI(m.renderMeetingPicker())
	if !strings.Contains(rendered, "already attached") {
		t.Errorf("rendered = %q, want it to flag the meeting as already attached", rendered)
	}
}

func TestRenderMeetingPickerNoMatches(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	ws.Files = append(ws.Files, &org.File{
		Path: filepath.Join(ws.Dir, "calendar.org"),
		Headlines: []*org.Headline{
			recurringCalendarEventHeadline("standup-1", "series-standup", "Weekly Standup", now.Add(time.Hour), now.Add(90*time.Minute)),
		},
	})
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	m = sendKey(m, "g")
	m = sendKey(m, "M")
	m = typeKeys(m, "zzz-no-match")

	rendered := stripANSI(m.renderMeetingPicker())
	if !strings.Contains(rendered, "no matches") {
		t.Errorf("rendered = %q, want it to say no matches", rendered)
	}
}

// TestMeetingColumnBlankWhenNoMeetingAttached covers the baseline for
// meetingColumn: an entry with neither GCAL_RECURRING_EVENT_IDS nor
// GCAL_EVENT_IDS set shows a blank gutter column, same as no mark, no
// lock, and no dirty marker.
func TestMeetingColumnBlankWhenNoMeetingAttached(t *testing.T) {
	h := &org.Headline{Level: 1, Keyword: "TODO", Title: "Plain entry"}
	m := Model{}
	line := []rune(stripANSI(m.renderRow(row{headline: h})))
	if len(line) < 3 || line[2] != ' ' {
		t.Errorf("row = %q, want the meeting column (index 2) blank", string(line))
	}
}

// TestMeetingColumnShowsForRecurringAttachment and
// TestMeetingColumnShowsForOneOffAttachment cover meetingColumn's core
// job — surfacing "this entry has a meeting attached" on the row
// itself, without opening $EDITOR to check the property drawer — for
// each of the two property pairs "gM" can write.
func TestMeetingColumnShowsForRecurringAttachment(t *testing.T) {
	h := &org.Headline{Level: 1, Keyword: "TODO", Title: "Prep for standup"}
	h.SetProperty("GCAL_RECURRING_EVENT_IDS", "series-standup")
	m := Model{}
	line := []rune(stripANSI(m.renderRow(row{headline: h})))
	if len(line) < 3 || line[2] != '▣' {
		t.Errorf("row = %q, want the meeting column (index 2) to show ▣", string(line))
	}
}

func TestMeetingColumnShowsForOneOffAttachment(t *testing.T) {
	h := &org.Headline{Level: 1, Keyword: "TODO", Title: "Bring the deck"}
	h.SetProperty("GCAL_EVENT_IDS", "kickoff-1")
	m := Model{}
	line := []rune(stripANSI(m.renderRow(row{headline: h})))
	if len(line) < 3 || line[2] != '▣' {
		t.Errorf("row = %q, want the meeting column (index 2) to show ▣", string(line))
	}
}

// TestGMAttachShowsMeetingColumnImmediately is the end-to-end version:
// after attaching a meeting via "gM", the entry's outline row shows the
// meeting column right away — no need to reopen or edit the entry to
// confirm the attachment took.
func TestGMAttachShowsMeetingColumnImmediately(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	ws.Files = append(ws.Files, &org.File{
		Path: filepath.Join(ws.Dir, "calendar.org"),
		Headlines: []*org.Headline{
			recurringCalendarEventHeadline("standup-1", "series-standup", "Weekly Standup", now.Add(time.Hour), now.Add(90*time.Minute)),
		},
	})
	m := New(ws)
	idx := findRow(t, m, "Call the vet about Fido's checkup")
	m.cursor = idx

	before := []rune(stripANSI(m.renderRow(m.rows[idx])))
	if len(before) < 3 || before[2] != ' ' {
		t.Fatalf("setup: row = %q, want the meeting column blank before attaching", string(before))
	}

	m = sendKey(m, "g")
	m = sendKey(m, "M")
	m, _ = sendKeyCmd(m, "enter")

	after := []rune(stripANSI(m.renderRow(m.rows[idx])))
	if len(after) < 3 || after[2] != '▣' {
		t.Errorf("row after gM attach = %q, want the meeting column to show ▣", string(after))
	}
}

func TestGMRefusesOnImmutableEntry(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	ws.Files = append(ws.Files, &org.File{
		Path: filepath.Join(ws.Dir, "calendar.org"),
		Headlines: []*org.Headline{
			recurringCalendarEventHeadline("standup-1", "series-standup", "Weekly Standup", now.Add(time.Hour), now.Add(90*time.Minute)),
		},
	})
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	h := m.currentHeadline()
	m.immutable[h] = true

	m = sendKey(m, "g")
	m = sendKey(m, "M")

	if m.mode == meetingPickerMode {
		t.Errorf("gM opened the picker on a locked entry")
	}
}
