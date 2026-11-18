package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/sburnett/orgtd/internal/org"
	"github.com/sburnett/orgtd/internal/orgdate"
)

// gdOn presses "gd" on the entry titled title.
func gdOn(t *testing.T, m Model, title string) Model {
	t.Helper()
	m.cursor = findRow(t, m, title)
	m = sendKey(m, "g")
	return sendKey(m, "d")
}

// setDeadlineVia types date into the deadline prompt "gd" opened and
// presses Enter, which applies it and leaves insert mode, returning the
// edited entry (a new headline, once the edit applies).
func setDeadlineVia(t *testing.T, m Model, date string) (Model, *org.Headline) {
	t.Helper()
	if m.mode != insertMode || m.ins.date == nil {
		t.Fatalf("mode = %v (field %v), want a date field open", m.mode, m.ins.date)
	}
	m = typeKeys(m, date)
	m = sendKey(m, "enter")
	if m.mode != normalMode {
		t.Fatalf("mode after %q = %v (message %q), want gd done, back in normal mode", date, m.mode, m.message)
	}
	return m, m.currentHeadline()
}

func TestGdPutsTheCaretOnAnExistingDeadline(t *testing.T) {
	m := gdOn(t, New(loadFixture(t)), "Follow up with finance about the Q3 budget doc")
	if m.mode != insertMode {
		t.Fatalf("mode = %v, want insert mode", m.mode)
	}
	s, ok := m.onTimestamp()
	if !ok {
		t.Fatalf("caret at %d:%d isn't on a timestamp", m.ins.buf.row, m.ins.buf.col)
	}
	if got := string(m.ins.buf.runes(m.ins.buf.row)[s[0]:s[1]]); got != "<2026-09-12 Sat>" {
		t.Errorf("caret on %q, want the deadline", got)
	}
	if !strings.HasPrefix(m.ins.buf.lines[m.ins.buf.row], "DEADLINE: ") {
		t.Errorf("caret line %q isn't the deadline's", m.ins.buf.lines[m.ins.buf.row])
	}
}

func TestGdChangesAnExistingDeadline(t *testing.T) {
	m := gdOn(t, New(loadFixture(t)), "Follow up with finance about the Q3 budget doc")
	m = sendKey(m, "enter")
	m, h := setDeadlineVia(t, m, "2026-12-25")
	if h.Deadline == nil || h.Deadline.Raw != "2026-12-25 Fri" {
		t.Errorf("deadline = %v, want 2026-12-25 Fri", h.Deadline)
	}
}

func TestGdWithNoDeadlineAddsOneAndOpensThePrompt(t *testing.T) {
	m := New(loadFixture(t))
	undoBefore := m.undoPos
	m = gdOn(t, m, "Call the vet about Fido's checkup")
	if m.mode != insertMode || m.ins.date == nil {
		t.Fatalf("mode = %v (field %v), want a date field open right away", m.mode, m.ins.date)
	}
	if got := m.ins.buf.lines[1]; got != "DEADLINE: " {
		t.Errorf("line below the title = %q, want a new DEADLINE label", got)
	}
	if view := stripANSI(m.View()); !strings.Contains(view, "DEADLINE: < >  e.g.") {
		t.Errorf("no empty deadline field drawn after the label:\n%s", view)
	}

	m, h := setDeadlineVia(t, m, "2026-12-25")
	if h.Deadline == nil || h.Deadline.Raw != "2026-12-25 Fri" {
		t.Fatalf("deadline = %v, want 2026-12-25 Fri", h.Deadline)
	}
	if !m.dirtyHeadlines[h] {
		t.Errorf("expected the headline to be marked dirty")
	}
	if m.undoPos != undoBefore+1 {
		t.Errorf("undo steps = %d, want 1", m.undoPos-undoBefore)
	}
	m = sendKey(m, "u")
	if h := m.currentHeadline(); h.Deadline != nil {
		t.Errorf("deadline after undo = %v, want nil", h.Deadline)
	}
}

func TestGdNewDeadlineCancelledTakesTheLabelBackOut(t *testing.T) {
	for _, key := range []string{"esc", "enter"} { // Esc, or Enter with nothing typed
		m := New(loadFixture(t))
		m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
		want := entryEditText(m.currentHeadline())
		undoBefore := m.undoPos
		m = gdOn(t, m, "Call the vet about Fido's checkup")
		m = sendKey(m, key)
		if m.mode != normalMode {
			t.Fatalf("%s: mode = %v, want gd done, back in normal mode", key, m.mode)
		}
		if got := entryEditText(m.currentHeadline()); got != want {
			t.Errorf("%s: entry = %q, want it as it was", key, got)
		}
		if m.undoPos != undoBefore || len(m.dirty) != 0 {
			t.Errorf("%s: undo steps %d, dirty %v; want nothing recorded", key, m.undoPos-undoBefore, m.dirty)
		}
	}
}

func TestGdAddsTheDeadlineToAnExistingPlanningLine(t *testing.T) {
	for _, c := range []struct {
		name, entry, wantBefore, wantAfter string
	}{
		{"after SCHEDULED", "* TODO Thing\n  SCHEDULED: <2026-11-20 Fri>\n",
			"SCHEDULED: <2026-11-20 Fri> DEADLINE: ", "SCHEDULED: <2026-11-20 Fri> DEADLINE: <2026-12-25 Fri>"},
		{"before CLOSED", "* DONE Thing\n  CLOSED: [2026-11-01 Sun 10:00]\n",
			"DEADLINE:  CLOSED: [2026-11-01 Sun 10:00]", "DEADLINE: <2026-12-25 Fri> CLOSED: [2026-11-01 Sun 10:00]"},
	} {
		m := gdOn(t, New(agendaFixture(t, c.entry)), "Thing")
		if got := m.ins.buf.lines[1]; got != c.wantBefore {
			t.Errorf("%s: planning line = %q, want %q", c.name, got, c.wantBefore)
		}
		m, h := setDeadlineVia(t, m, "2026-12-25")
		if got := org.PlanningLine(h); got != c.wantAfter {
			t.Errorf("%s: planning line after the date = %q, want %q", c.name, got, c.wantAfter)
		}
	}
}

func TestGdIsTwoKeyChordNotSingleG(t *testing.T) {
	m := New(loadFixture(t))
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")

	// A lone "g" waits for a second key (gg, gd, ...).
	m = sendKey(m, "g")
	if m.mode != normalMode {
		t.Fatalf("single g left normal mode: %v", m.mode)
	}
	if m.chord != "g" {
		t.Errorf("expected chord after a single g")
	}
}

func TestGgStillJumpsToTop(t *testing.T) {
	m := New(loadFixture(t))
	m = sendKey(m, "G")
	if m.cursor == 0 {
		t.Fatalf("G did not move the cursor away from the top")
	}
	m = sendKey(m, "g")
	m = sendKey(m, "g")
	if m.cursor != 0 || m.mode != normalMode {
		t.Errorf("gg: cursor %d, mode %v; want the top, normal mode", m.cursor, m.mode)
	}
}

func TestGdNoopOnFileRow(t *testing.T) {
	m := New(loadFixture(t))
	m.cursor = 0
	if m.rows[0].file == nil {
		t.Fatalf("fixture assumption broken: row 0 is not a file row")
	}
	m = sendKey(m, "g")
	m = sendKey(m, "d")
	if m.mode != normalMode {
		t.Errorf("gd on a file row: mode %v, want normal mode", m.mode)
	}
}

func TestDeadlineWithTime(t *testing.T) {
	m := gdOn(t, New(loadFixture(t)), "Call the vet about Fido's checkup")
	_, h := setDeadlineVia(t, m, "2026-12-25 14:30")
	if h.Deadline == nil || h.Deadline.Raw != "2026-12-25 Fri 14:30" {
		t.Errorf("deadline = %v, want 2026-12-25 Fri 14:30", h.Deadline)
	}
}

func TestDeadlineInvalidInputStaysOpenForCorrection(t *testing.T) {
	m := gdOn(t, New(loadFixture(t)), "Call the vet about Fido's checkup")
	m = typeKeys(m, "not-a-date")
	m = sendKey(m, "enter")
	if m.ins.date == nil {
		t.Fatalf("field closed after invalid input, want it kept open")
	}
	if got := m.ins.date.input.String(); got != "not-a-date" || m.message == "" {
		t.Errorf("input %q, message %q; want the input kept and an error", got, m.message)
	}

	for range "not-a-date" {
		m = sendKey(m, "backspace")
	}
	_, h := setDeadlineVia(t, m, "2026-12-25")
	if h.Deadline == nil || h.Deadline.Raw != "2026-12-25 Fri" {
		t.Errorf("deadline after correction = %v, want 2026-12-25 Fri", h.Deadline)
	}
}

// TestDeadlineInvalidInputErrorIsActuallyVisible guards against a real
// bug: the error was set but never drawn, so from the user's side Enter
// on a bad date did nothing at all. Only the rendered line can show it.
func TestDeadlineInvalidInputErrorIsActuallyVisible(t *testing.T) {
	m := New(loadFixture(t))
	m.width, m.height = 120, 30
	m = gdOn(t, m, "Call the vet about Fido's checkup")
	m = typeKeys(m, "not-a-date")
	m = sendKey(m, "enter")
	if m.message == "" {
		t.Fatalf("fixture assumption broken: expected m.message to be set")
	}
	last := lastLine(m)
	if !strings.Contains(last, m.message) || !strings.Contains(last, "not-a-date") {
		t.Errorf("command line = %q, want the error and the input being corrected", last)
	}
}

// TestDeadlineReportedRepro is the literal phrase reported as "pressing
// Enter does nothing": it doesn't match any recognized date shape, so it
// must produce a visible error rather than silently doing nothing.
func TestDeadlineReportedRepro(t *testing.T) {
	m := New(loadFixture(t))
	m.width, m.height = 120, 30
	m = gdOn(t, m, "Call the vet about Fido's checkup")
	m = typeKeys(m, "last thursday in august, 202")
	m = sendKey(m, "enter")
	if m.ins.date == nil || m.message == "" {
		t.Fatalf("field %v, message %q; want the field open with a visible error", m.ins.date, m.message)
	}
	if !strings.Contains(lastLine(m), m.message) {
		t.Errorf("command line = %q, missing the error %q", lastLine(m), m.message)
	}
}

func TestDeadlineErrorClearsOnNextKeystroke(t *testing.T) {
	m := gdOn(t, New(loadFixture(t)), "Call the vet about Fido's checkup")
	m = typeKeys(m, "not-a-date")
	m = sendKey(m, "enter")
	m = sendKey(m, "backspace")
	if m.message != "" {
		t.Errorf("m.message = %q, want cleared after the next keystroke", m.message)
	}
}

func TestGdOnAnExistingDeadlineEndsWhenItsPromptDoes(t *testing.T) {
	m := gdOn(t, New(loadFixture(t)), "Follow up with finance about the Q3 budget doc")
	m = sendKey(m, "enter")
	m = sendKey(m, "esc")
	if m.mode != normalMode {
		t.Errorf("mode after cancelling gd's date prompt = %v, want normal mode", m.mode)
	}
	if h := m.currentHeadline(); h.Deadline == nil || h.Deadline.Raw != "2026-09-12 Sat" {
		t.Errorf("deadline = %v, want it unchanged", h.Deadline)
	}
}

func TestOtherTimestampPromptsStayInInsertMode(t *testing.T) {
	m := onCapturedAt(t) // insert mode from i, not gd
	m = sendKey(m, "enter")
	m = typeKeys(m, "2026-12-25")
	m = sendKey(m, "enter")
	if m.mode != insertMode {
		t.Errorf("mode after a date prompt outside gd = %v, want still insert mode", m.mode)
	}
}

func TestDeadlineEscOnAnInvalidDateKeepsTheField(t *testing.T) {
	m := gdOn(t, New(loadFixture(t)), "Call the vet about Fido's checkup")
	m = typeKeys(m, "not-a-date")
	m = sendKey(m, "esc")
	if m.mode != insertMode || m.ins.date == nil || m.message == "" {
		t.Errorf("Esc on an invalid date: mode %v, field %v, message %q; want the field kept open, saying why", m.mode, m.ins.date, m.message)
	}
}

func TestDeadlineAcceptsShorthandAndPhrases(t *testing.T) {
	today := orgdate.TruncateToDate(time.Now())
	sep30 := time.Date(today.Year(), time.September, 30, 0, 0, 0, 0, today.Location())
	if sep30.Before(today) {
		sep30 = sep30.AddDate(1, 0, 0)
	}
	days := (int(time.Thursday) - int(today.Weekday()) + 7) % 7
	if days == 0 {
		days = 7
	}
	for _, c := range []struct {
		input string
		want  time.Time
	}{
		{"3d", today.AddDate(0, 0, 3)},
		{"tomorrow", today.AddDate(0, 0, 1)},
		// "sep 30" means this year's if it hasn't passed, never a year
		// further out just because today is in September too.
		{"sep 30", sep30},
		// "thu" is the next Thursday, skipping today if it is one.
		{"thu", today.AddDate(0, 0, days)},
	} {
		m := gdOn(t, New(loadFixture(t)), "Call the vet about Fido's checkup")
		_, h := setDeadlineVia(t, m, c.input)
		if want := c.want.Format("2006-01-02 Mon"); h.Deadline == nil || h.Deadline.Raw != want {
			t.Errorf("%q: deadline = %v, want %q (and no time of day)", c.input, h.Deadline, want)
		}
	}
}

func TestDeletingADateDropsItsLabelOnLeavingInsertMode(t *testing.T) {
	for _, c := range []struct {
		name, entry, wantPlanning string
	}{
		{"alone", "* TODO Thing\n  DEADLINE: <2026-11-20 Fri>\n  Some body\n", ""},
		{"with SCHEDULED", "* TODO Thing\n  SCHEDULED: <2026-11-18 Wed> DEADLINE: <2026-11-20 Fri>\n", "SCHEDULED: <2026-11-18 Wed>"},
	} {
		m := gdOn(t, New(agendaFixture(t, c.entry)), "Thing")
		m = sendKey(m, "backspace") // the caret's on the deadline's date
		if !strings.Contains(m.ins.buf.lines[1], "DEADLINE:") {
			t.Fatalf("%s: label gone while still editing: %q", c.name, m.ins.buf.lines[1])
		}
		m = sendKey(m, "esc")
		h := m.currentHeadline()
		if h.Deadline != nil {
			t.Errorf("%s: deadline = %v, want none", c.name, h.Deadline)
		}
		if got := org.PlanningLine(h); got != c.wantPlanning {
			t.Errorf("%s: planning line = %q, want %q", c.name, got, c.wantPlanning)
		}
		if body := strings.Join(h.Body, "\n"); strings.Contains(body, "DEADLINE") {
			t.Errorf("%s: body = %q, want no stray DEADLINE label", c.name, body)
		}
	}
}

func TestADatelessLabelInTheBodyIsLeftAlone(t *testing.T) {
	m := gdOn(t, New(agendaFixture(t, "* TODO Thing\n  DEADLINE: <2026-11-20 Fri>\n  Mind the DEADLINE: it moved\n")), "Thing")
	m = sendKey(m, "esc")
	if body := strings.Join(m.currentHeadline().Body, "\n"); !strings.Contains(body, "Mind the DEADLINE: it moved") {
		t.Errorf("body = %q, want the body's mention kept", body)
	}
}
