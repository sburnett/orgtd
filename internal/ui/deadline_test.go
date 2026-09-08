package ui

import (
	"strings"
	"testing"
	"time"
)

func TestGdOpensDeadlinePrefilled(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Follow up with finance about the Q3 budget doc")
	h := m.currentHeadline()
	if h.Deadline == nil || h.Deadline.Raw != "2026-09-12 Sat" {
		t.Fatalf("fixture assumption broken: deadline = %v", h.Deadline)
	}

	m = sendKey(m, "g")
	m = sendKey(m, "d")

	if m.mode != deadlineMode {
		t.Fatalf("mode = %v, want deadlineMode", m.mode)
	}
	if m.deadlineInput != "2026-09-12" {
		t.Errorf("deadlineInput = %q, want %q (prefilled, weekday dropped)", m.deadlineInput, "2026-09-12")
	}
}

func TestGdOnEntryWithNoDeadlineStartsBlank(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	if m.currentHeadline().Deadline != nil {
		t.Fatalf("fixture assumption broken: expected no deadline")
	}

	m = sendKey(m, "g")
	m = sendKey(m, "d")

	if m.mode != deadlineMode {
		t.Fatalf("mode = %v, want deadlineMode", m.mode)
	}
	if m.deadlineInput != "" {
		t.Errorf("deadlineInput = %q, want empty", m.deadlineInput)
	}
}

func TestGdIsTwoKeyChordNotSingleG(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")

	// A lone "g" should not open the deadline prompt; it's waiting for a
	// second key (gg, or now gd).
	m = sendKey(m, "g")
	if m.mode == deadlineMode {
		t.Fatalf("single g opened deadline mode")
	}
	if !m.pendingG {
		t.Errorf("expected pendingG after a single g")
	}
}

func TestGgStillJumpsToTop(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m = sendKey(m, "G")
	if m.cursor == 0 {
		t.Fatalf("G did not move the cursor away from the top")
	}

	m = sendKey(m, "g")
	m = sendKey(m, "g")
	if m.cursor != 0 {
		t.Errorf("gg after adding gd did not jump to the top: cursor = %d", m.cursor)
	}
	if m.mode != normalMode {
		t.Errorf("gg left mode = %v, want normalMode", m.mode)
	}
}

func TestGdNoopOnFileRow(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = 0
	if m.rows[0].file == nil {
		t.Fatalf("fixture assumption broken: row 0 is not a file row")
	}

	m = sendKey(m, "g")
	m = sendKey(m, "d")
	if m.mode == deadlineMode {
		t.Errorf("gd opened deadline mode on a file row")
	}
}

func TestDeadlineEnterSetsDeadlineAsUndoStep(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	h := m.currentHeadline()

	m = sendKey(m, "g")
	m = sendKey(m, "d")
	m = typeKeys(m, "2026-12-25")
	m = sendKey(m, "enter")

	if m.mode != normalMode {
		t.Fatalf("mode after enter = %v, want normalMode", m.mode)
	}
	if h.Deadline == nil || h.Deadline.Raw != "2026-12-25 Fri" {
		t.Fatalf("deadline = %v, want 2026-12-25 Fri", h.Deadline)
	}
	if !m.dirtyHeadlines[h] {
		t.Errorf("expected the headline to be marked dirty")
	}

	m = sendKey(m, "u")
	if h.Deadline != nil {
		t.Errorf("deadline after undo = %v, want nil", h.Deadline)
	}

	m = sendKey(m, "ctrl+r")
	if h.Deadline == nil || h.Deadline.Raw != "2026-12-25 Fri" {
		t.Errorf("deadline after redo = %v, want 2026-12-25 Fri", h.Deadline)
	}
}

func TestDeadlineWithTime(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	h := m.currentHeadline()

	m = sendKey(m, "g")
	m = sendKey(m, "d")
	m = typeKeys(m, "2026-12-25 14:30")
	m = sendKey(m, "enter")

	if h.Deadline == nil || h.Deadline.Raw != "2026-12-25 Fri 14:30" {
		t.Errorf("deadline = %v, want 2026-12-25 Fri 14:30", h.Deadline)
	}
}

func TestDeadlineEmptyInputClears(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Follow up with finance about the Q3 budget doc")
	h := m.currentHeadline()
	if h.Deadline == nil {
		t.Fatalf("fixture assumption broken: expected an existing deadline")
	}

	m = sendKey(m, "g")
	m = sendKey(m, "d")
	// Backspace out the prefilled date entirely.
	for range m.deadlineInput {
		m = sendKey(m, "backspace")
	}
	m = sendKey(m, "enter")

	if m.mode != normalMode {
		t.Fatalf("mode after enter = %v, want normalMode", m.mode)
	}
	if h.Deadline != nil {
		t.Errorf("deadline = %v, want nil (cleared)", h.Deadline)
	}
}

func TestDeadlineEscCancelsWithoutChange(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	h := m.currentHeadline()

	m = sendKey(m, "g")
	m = sendKey(m, "d")
	m = typeKeys(m, "2026-12-25")
	m = sendKey(m, "esc")

	if m.mode != normalMode {
		t.Fatalf("mode after esc = %v, want normalMode", m.mode)
	}
	if h.Deadline != nil {
		t.Errorf("deadline = %v, want nil (unchanged)", h.Deadline)
	}
}

func TestDeadlineInvalidInputStaysOpenForCorrection(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	h := m.currentHeadline()

	m = sendKey(m, "g")
	m = sendKey(m, "d")
	m = typeKeys(m, "not-a-date")
	m = sendKey(m, "enter")

	if m.mode != deadlineMode {
		t.Fatalf("mode after invalid input = %v, want deadlineMode (stay open to correct)", m.mode)
	}
	if m.deadlineInput != "not-a-date" {
		t.Errorf("deadlineInput = %q, want preserved for correction", m.deadlineInput)
	}
	if m.message == "" {
		t.Errorf("expected an error message for the invalid date")
	}
	if h.Deadline != nil {
		t.Errorf("deadline = %v, want unchanged", h.Deadline)
	}

	// Fix it and confirm it now applies.
	for range m.deadlineInput {
		m = sendKey(m, "backspace")
	}
	m = typeKeys(m, "2026-12-25")
	m = sendKey(m, "enter")
	if h.Deadline == nil || h.Deadline.Raw != "2026-12-25 Fri" {
		t.Errorf("deadline after correction = %v, want 2026-12-25 Fri", h.Deadline)
	}
}

// TestDeadlineInvalidInputErrorIsActuallyVisible guards against a real
// bug: m.message was set correctly on an invalid date (per
// TestDeadlineInvalidInputStaysOpenForCorrection above), but the status
// bar's rendering switch checked "mode == deadlineMode" before
// "message != empty", so the error was silently never drawn — from the user's
// perspective, pressing Enter on a bad date did nothing at all. Checking
// m.message alone (as the model-state test above does) can't catch this
// class of bug; only inspecting the actual rendered line can.
func TestDeadlineInvalidInputErrorIsActuallyVisible(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.width, m.height = 120, 30
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")

	m = sendKey(m, "g")
	m = sendKey(m, "d")
	m = typeKeys(m, "not-a-date")
	m = sendKey(m, "enter")

	if m.mode != deadlineMode {
		t.Fatalf("fixture assumption broken: mode = %v, want deadlineMode", m.mode)
	}
	if m.message == "" {
		t.Fatalf("fixture assumption broken: expected m.message to be set")
	}

	lines := strings.Split(m.View(), "\n")
	last := lines[len(lines)-1]
	if !strings.Contains(last, m.message) {
		t.Errorf("status line = %q, missing the error message %q", last, m.message)
	}
	if !strings.Contains(last, "not-a-date") {
		t.Errorf("status line = %q, should still show the input being corrected", last)
	}
}

// TestDeadlineReportedRepro is the literal phrase reported as "pressing
// Enter does nothing": it doesn't match any recognized date shape, so it
// must produce a visible error rather than silently leaving the prompt
// unchanged.
func TestDeadlineReportedRepro(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.width, m.height = 120, 30
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")

	m = sendKey(m, "g")
	m = sendKey(m, "d")
	m = typeKeys(m, "last thursday in august, 202")
	m = sendKey(m, "enter")

	if m.mode != deadlineMode {
		t.Fatalf("mode = %v, want deadlineMode (stay open to correct)", m.mode)
	}
	if m.message == "" {
		t.Fatal("expected a visible error, not silent inaction")
	}
	last := strings.Split(m.View(), "\n")
	if line := last[len(last)-1]; !strings.Contains(line, m.message) {
		t.Errorf("status line = %q, missing the error message %q", line, m.message)
	}
}

func TestDeadlineErrorClearsOnNextKeystroke(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")

	m = sendKey(m, "g")
	m = sendKey(m, "d")
	m = typeKeys(m, "not-a-date")
	m = sendKey(m, "enter")
	if m.message == "" {
		t.Fatalf("fixture assumption broken: expected m.message to be set")
	}

	m = sendKey(m, "backspace")
	if m.message != "" {
		t.Errorf("m.message = %q, want cleared after the next keystroke", m.message)
	}
}

func TestDeadlineErrorClearsOnEsc(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")

	m = sendKey(m, "g")
	m = sendKey(m, "d")
	m = typeKeys(m, "not-a-date")
	m = sendKey(m, "enter")
	if m.message == "" {
		t.Fatalf("fixture assumption broken: expected m.message to be set")
	}

	m = sendKey(m, "esc")
	if m.message != "" {
		t.Errorf("m.message = %q, want cleared on Esc (shouldn't linger into the normal status line)", m.message)
	}
}

func TestDeadlineErrorClearsAfterSubsequentSuccess(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")

	m = sendKey(m, "g")
	m = sendKey(m, "d")
	m = typeKeys(m, "not-a-date")
	m = sendKey(m, "enter")
	if m.message == "" {
		t.Fatalf("fixture assumption broken: expected m.message to be set")
	}

	for range m.deadlineInput {
		m = sendKey(m, "backspace")
	}
	m = typeKeys(m, "2026-12-25")
	m = sendKey(m, "enter")

	if m.message != "" {
		t.Errorf("m.message = %q, want cleared after a subsequent successful submit", m.message)
	}
}

func TestParseRelativeOffsetShorthandAndSpelledOut(t *testing.T) {
	base := time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC) // a Sunday

	cases := []struct {
		input string
		want  time.Time
	}{
		{"3d", base.AddDate(0, 0, 3)},
		{"3 days", base.AddDate(0, 0, 3)},
		{"2w", base.AddDate(0, 0, 14)},
		{"2 weeks", base.AddDate(0, 0, 14)},
		{"1m", base.AddDate(0, 1, 0)},
		{"1 month", base.AddDate(0, 1, 0)},
		{"1y", base.AddDate(1, 0, 0)},
		{"1 year", base.AddDate(1, 0, 0)},
		{"-5d", base.AddDate(0, 0, -5)},
		{"+5d", base.AddDate(0, 0, 5)},
		{"3D", base.AddDate(0, 0, 3)}, // case-insensitive
	}
	for _, c := range cases {
		got, ok := parseRelativeOffset(c.input, base)
		if !ok {
			t.Errorf("parseRelativeOffset(%q) did not match", c.input)
			continue
		}
		if !got.Equal(c.want) {
			t.Errorf("parseRelativeOffset(%q) = %v, want %v", c.input, got, c.want)
		}
	}
}

func TestParseRelativeOffsetRejectsNonMatches(t *testing.T) {
	base := time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)
	for _, input := range []string{"next tuesday", "2026-12-25", "", "abc", "3 hours", "d3"} {
		if _, ok := parseRelativeOffset(input, base); ok {
			t.Errorf("parseRelativeOffset(%q) unexpectedly matched", input)
		}
	}
}

func TestDeadlineAcceptsCompactShorthandThroughTheUI(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	h := m.currentHeadline()

	m = sendKey(m, "g")
	m = sendKey(m, "d")
	m = typeKeys(m, "3d")
	m = sendKey(m, "enter")

	if m.mode != normalMode {
		t.Fatalf("mode after enter = %v, want normalMode (input should have been accepted)", m.mode)
	}
	want := truncateToDate(time.Now()).AddDate(0, 0, 3).Format("2006-01-02 Mon")
	if h.Deadline == nil || h.Deadline.Raw != want {
		t.Errorf("deadline = %v, want %q", h.Deadline, want)
	}
}

func TestDeadlineAcceptsFuzzyPhraseThroughTheUI(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	h := m.currentHeadline()

	m = sendKey(m, "g")
	m = sendKey(m, "d")
	m = typeKeys(m, "tomorrow")
	m = sendKey(m, "enter")

	if m.mode != normalMode {
		t.Fatalf("mode after enter = %v, want normalMode (\"tomorrow\" should have been accepted)", m.mode)
	}
	want := truncateToDate(time.Now()).AddDate(0, 0, 1).Format("2006-01-02 Mon")
	if h.Deadline == nil || h.Deadline.Raw != want {
		t.Errorf("deadline = %v, want %q", h.Deadline, want)
	}
}

func TestDeadlineFuzzyPhraseHasNoTimeOfDay(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	h := m.currentHeadline()

	m = sendKey(m, "g")
	m = sendKey(m, "d")
	m = typeKeys(m, "next friday")
	m = sendKey(m, "enter")

	if h.Deadline == nil {
		t.Fatalf("deadline not set")
	}
	if strings.Contains(h.Deadline.Raw, ":") {
		t.Errorf("deadline = %q, want no time-of-day component for a fuzzy phrase", h.Deadline.Raw)
	}
}
