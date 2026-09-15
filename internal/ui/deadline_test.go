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

func TestFuzzyDateRollsSameMonthDayForwardOnlyPastToday(t *testing.T) {
	today := time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC) // a Monday

	cases := []struct {
		input string
		want  time.Time
	}{
		// "sep 30" hasn't happened yet this year: stays in 2026, not
		// pushed a year out just because today is also in September
		// (see fuzzyDate's doc comment — that was the actual bug in the
		// date library this replaced).
		{"sep 30", time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)},
		// "sep 1" already passed this year: rolls forward to 2027.
		{"sep 1", time.Date(2027, 9, 1, 0, 0, 0, 0, time.UTC)},
		// An explicit year (here, D/M/Y — the one when.EN date shape
		// that actually carries a year) is trusted as-is, even in the
		// past — never rolled forward like the year-less cases above.
		{"31/3/2014", time.Date(2014, 3, 31, 0, 0, 0, 0, time.UTC)},
		{"thu", time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC)},
	}
	for _, c := range cases {
		got, ok := fuzzyDate(c.input, today)
		if !ok {
			t.Errorf("fuzzyDate(%q) did not match", c.input)
			continue
		}
		if !got.Equal(c.want) {
			t.Errorf("fuzzyDate(%q) = %v, want %v", c.input, got, c.want)
		}
	}
}

func TestFuzzyDateRejectsPartialMatchesAndGarbage(t *testing.T) {
	today := time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC)
	for _, input := range []string{"not-a-date", "last thursday in august, 202", "", "3d"} {
		if _, ok := fuzzyDate(input, today); ok {
			t.Errorf("fuzzyDate(%q) unexpectedly matched", input)
		}
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

// TestDeadlineAcceptsAbbreviatedMonthAndDayThroughTheUI covers the
// specific phrase reported as not working: "sep 30" (an abbreviated
// month name plus a bare day, no year) should resolve to this year's
// September 30 if it hasn't passed yet, or next year's otherwise — never
// a full year further out just because today also happens to fall in
// September (see fuzzyDate's doc comment for why that's worth calling
// out: the previous date library got exactly this case wrong).
func TestDeadlineAcceptsAbbreviatedMonthAndDayThroughTheUI(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	h := m.currentHeadline()
	today := truncateToDate(time.Now())

	m = sendKey(m, "g")
	m = sendKey(m, "d")
	m = typeKeys(m, "sep 30")
	m = sendKey(m, "enter")

	if m.mode != normalMode {
		t.Fatalf("mode after enter = %v, want normalMode (\"sep 30\" should have been accepted)", m.mode)
	}
	want := time.Date(today.Year(), time.September, 30, 0, 0, 0, 0, today.Location())
	if want.Before(today) {
		want = want.AddDate(1, 0, 0)
	}
	if h.Deadline == nil || h.Deadline.Raw != want.Format("2006-01-02 Mon") {
		t.Errorf("deadline = %v, want %q", h.Deadline, want.Format("2006-01-02 Mon"))
	}
}

// TestDeadlineAcceptsAbbreviatedWeekdayThroughTheUI covers "thu": an
// abbreviated weekday name should mean the next upcoming occurrence of
// that weekday, skipping today even if today is itself a Thursday.
func TestDeadlineAcceptsAbbreviatedWeekdayThroughTheUI(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	h := m.currentHeadline()
	today := truncateToDate(time.Now())

	m = sendKey(m, "g")
	m = sendKey(m, "d")
	m = typeKeys(m, "thu")
	m = sendKey(m, "enter")

	if m.mode != normalMode {
		t.Fatalf("mode after enter = %v, want normalMode (\"thu\" should have been accepted)", m.mode)
	}
	days := (int(time.Thursday) - int(today.Weekday()) + 7) % 7
	if days == 0 {
		days = 7
	}
	want := today.AddDate(0, 0, days)
	if h.Deadline == nil || h.Deadline.Raw != want.Format("2006-01-02 Mon") {
		t.Errorf("deadline = %v, want %q", h.Deadline, want.Format("2006-01-02 Mon"))
	}
}
