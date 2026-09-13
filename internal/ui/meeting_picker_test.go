package ui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sburnett/orgtd/internal/org"
)

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
	if m.meetingPickerCandidates[0].recurringEventID != "series-standup" {
		t.Errorf("candidates[0] = %+v, want series-standup first (sooner)", m.meetingPickerCandidates[0])
	}
	if m.meetingPickerCandidates[1].recurringEventID != "series-planning" {
		t.Errorf("candidates[1] = %+v, want series-planning second", m.meetingPickerCandidates[1])
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
	if len(matches) != 1 || matches[0].recurringEventID != "series-planning" {
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
	if len(matches) != 1 || matches[0].recurringEventID != "series-jam" {
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
