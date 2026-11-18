package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// The fixture's vet entry has ":CAPTURED_AT: [2026-09-05 Sat 09:12]" on
// its fourth line, the timestamp starting at column 14.
const (
	capturedRow   = 3
	capturedStart = len(":CAPTURED_AT: ")
	capturedEnd   = capturedStart + len("[2026-09-05 Sat 09:12]")
)

// onCapturedAt is insert mode on the vet entry with the caret at the
// start of its CAPTURED_AT timestamp.
func onCapturedAt(t *testing.T) Model {
	t.Helper()
	m := sendKey(vetEntry(t), "i")
	if got := m.ins.buf.lines[capturedRow]; got != ":CAPTURED_AT: [2026-09-05 Sat 09:12]" {
		t.Fatalf("fixture assumption broken: line %d = %q", capturedRow, got)
	}
	m.ins.buf.row, m.ins.buf.col = capturedRow, capturedStart
	return m
}

func TestTimestampSpansFindsBothKinds(t *testing.T) {
	got := timestampSpans("SCHEDULED: <2026-11-17 Tue +1w> and [2026-11-17 Tue 18:50]--[2026-11-18]")
	want := [][2]int{{11, 31}, {36, 58}, {60, 72}}
	if len(got) != len(want) {
		t.Fatalf("spans = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("span %d = %v, want %v", i, got[i], want[i])
		}
	}
	if s := timestampSpans("no [dates] <here> [2026-1-1]"); len(s) != 0 {
		t.Errorf("spans in text without timestamps = %v", s)
	}
}

func TestArrowsStepOverATimestamp(t *testing.T) {
	m := onCapturedAt(t)
	m = sendCtrl(m, tea.KeyRight)
	if m.ins.buf.col != capturedEnd {
		t.Fatalf("→ on the timestamp: col %d, want %d (past it)", m.ins.buf.col, capturedEnd)
	}
	m = sendCtrl(m, tea.KeyLeft)
	if m.ins.buf.col != capturedStart {
		t.Errorf("← from past it: col %d, want %d (back on it)", m.ins.buf.col, capturedStart)
	}
	m = sendCtrl(m, tea.KeyLeft)
	if m.ins.buf.col != capturedStart-1 {
		t.Errorf("← from on it: col %d, want %d (before it)", m.ins.buf.col, capturedStart-1)
	}
}

func TestWordMotionsAndVerticalMovesNeverLandInsideATimestamp(t *testing.T) {
	m := onCapturedAt(t)
	m = sendCtrl(m, tea.KeyCtrlRight)
	if m.ins.buf.col != capturedEnd {
		t.Errorf("word right on the timestamp: col %d, want %d", m.ins.buf.col, capturedEnd)
	}
	m = sendCtrl(m, tea.KeyCtrlLeft)
	if m.ins.buf.col != capturedStart {
		t.Errorf("word left from past it: col %d, want %d", m.ins.buf.col, capturedStart)
	}
	m.ins.buf.row, m.ins.buf.col, m.ins.buf.goal = capturedRow+1, 0, 20
	m = sendKey(m, "up")
	if m.ins.buf.row != capturedRow || m.ins.buf.col != capturedStart {
		t.Errorf("↑ into the middle of it: at %d:%d, want on its start", m.ins.buf.row, m.ins.buf.col)
	}
}

func TestLettersOnATimestampAreIgnoredButPasteInsertsBefore(t *testing.T) {
	m := onCapturedAt(t)
	m = typeKeys(m, "x ")
	if got := m.ins.buf.lines[capturedRow]; got != ":CAPTURED_AT: [2026-09-05 Sat 09:12]" {
		t.Errorf("typing on a timestamp changed the line to %q", got)
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("~"), Paste: true})
	m = updated.(Model)
	if got := m.ins.buf.lines[capturedRow]; got != ":CAPTURED_AT: ~[2026-09-05 Sat 09:12]" {
		t.Errorf("a paste on a timestamp = %q, want it inserted before", got)
	}
}

func TestDeletingATimestampTakesItWhole(t *testing.T) {
	for _, c := range []struct {
		name string
		col  int
		key  tea.KeyMsg
	}{
		{"Delete on it", capturedStart, tea.KeyMsg{Type: tea.KeyDelete}},
		{"Backspace on it", capturedStart, tea.KeyMsg{Type: tea.KeyBackspace}},
		{"ctrl-d on it", capturedStart, tea.KeyMsg{Type: tea.KeyCtrlD}},
		{"Backspace past it", capturedEnd, tea.KeyMsg{Type: tea.KeyBackspace}},
		{"ctrl-w past it", capturedEnd, tea.KeyMsg{Type: tea.KeyCtrlW}},
		{"alt-Backspace past it", capturedEnd, tea.KeyMsg{Type: tea.KeyBackspace, Alt: true}},
		{"alt-d on it", capturedStart, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d"), Alt: true}},
	} {
		m := onCapturedAt(t)
		m.ins.buf.col = c.col
		updated, _ := m.Update(c.key)
		m = updated.(Model)
		if got := m.ins.buf.lines[capturedRow]; got != ":CAPTURED_AT: " || m.ins.buf.col != capturedStart {
			t.Errorf("%s: line %q, col %d; want the whole timestamp gone, caret where it was", c.name, got, m.ins.buf.col)
		}
	}
}

func TestEnterOnATimestampChangesItsDate(t *testing.T) {
	m := onCapturedAt(t)
	m = sendKey(m, "enter")
	if m.mode != insertMode || m.ins.date == nil {
		t.Fatalf("after Enter on a timestamp: mode %d, field %v; want a date field in insert mode", m.mode, m.ins.date)
	}
	if got := m.ins.date.input.String(); got != "" {
		t.Errorf("field starts with %q, want it empty", got)
	}
	// The field replaces the timestamp in place, the old date previewed.
	if view := stripANSI(m.View()); !strings.Contains(view, ":CAPTURED_AT: [ ]  currently [2026-09-05 Sat 09:12]") {
		t.Errorf("field not drawn in place:\n%s", view)
	}
	m = typeKeys(m, "2026-12-25")
	m = sendKey(m, "enter")
	if m.mode != insertMode {
		t.Fatalf("mode after the date = %d, want back in insert mode", m.mode)
	}
	// Inactive stays inactive; a date without a time drops it, as gd does.
	want := ":CAPTURED_AT: [2026-12-25 Fri]"
	if got := m.ins.buf.lines[capturedRow]; got != want {
		t.Errorf("line = %q, want %q", got, want)
	}
	if m.ins.buf.col != len(want) {
		t.Errorf("caret col %d, want just past the new timestamp", m.ins.buf.col)
	}

	m = sendKey(m, "esc")
	if got := m.currentHeadline().Properties["CAPTURED_AT"]; got != "[2026-12-25 Fri]" {
		t.Errorf("CAPTURED_AT after Esc = %q", got)
	}
}

func TestTimestampPromptTakesFuzzyDatesAndKeepsActive(t *testing.T) {
	m := sendKey(vetEntry(t), "i")
	m.ins.buf = newTextBuffer("TODO Call the vet\nDEADLINE: <2026-09-12 Sat>")
	m.ins.buf.row, m.ins.buf.col = 1, len("DEADLINE: ")
	m.clock = func() time.Time { return time.Date(2026, 11, 17, 10, 0, 0, 0, time.Local) }
	m = sendKey(m, "enter")
	m = typeKeys(m, "tomorrow")
	m = sendKey(m, "enter")
	if got := m.ins.buf.lines[1]; got != "DEADLINE: <2026-11-18 Wed>" {
		t.Errorf("after \"tomorrow\": %q", got)
	}
}

func TestDateFieldRefusesAnInvalidDate(t *testing.T) {
	for _, key := range []string{"enter", "esc"} {
		m := onCapturedAt(t)
		m = sendKey(m, "enter")
		m = typeKeys(m, "not a date")
		if !strings.Contains(stripANSI(m.View()), "✗ not a date") {
			t.Errorf("%s: preview doesn't say the date is invalid", key)
		}
		m = sendKey(m, key)
		if m.mode != insertMode || m.ins.date == nil || m.message == "" {
			t.Errorf("%s on an invalid date: mode %d, field %v, message %q; want the field kept open with an error", key, m.mode, m.ins.date, m.message)
		}
		if !strings.Contains(lastLine(m), "invalid date") {
			t.Errorf("%s: command line %q, want the error", key, lastLine(m))
		}
	}
}

func TestEscInTheDateFieldAppliesItAndEndsInsertMode(t *testing.T) {
	m := onCapturedAt(t)
	m = sendKey(m, "enter")
	m = typeKeys(m, "2026-12-25")
	m = sendKey(m, "esc")
	if m.mode != normalMode {
		t.Fatalf("mode after Esc = %d, want insert mode ended", m.mode)
	}
	if got := m.currentHeadline().Properties["CAPTURED_AT"]; got != "[2026-12-25 Fri]" {
		t.Errorf("CAPTURED_AT = %q, want the typed date", got)
	}
}

func TestCtrlCInTheDateFieldIsEsc(t *testing.T) {
	m := onCapturedAt(t)
	m = sendKey(m, "enter")
	m = typeKeys(m, "2026-12-25")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = updated.(Model)
	if cmd != nil || m.mode != normalMode || m.currentHeadline().Properties["CAPTURED_AT"] != "[2026-12-25 Fri]" {
		t.Errorf("ctrl-c in the field: cmd %v, mode %d; want it applied as Esc does, no quit", cmd, m.mode)
	}
}

func TestAnEmptyDateFieldKeepsTheOldDate(t *testing.T) {
	m := onCapturedAt(t)
	m = sendKey(m, "enter")
	m = sendKey(m, "enter")
	if got := m.ins.buf.lines[capturedRow]; got != ":CAPTURED_AT: [2026-09-05 Sat 09:12]" || m.ins.date != nil {
		t.Errorf("empty Enter: line %q, field %v; want the old date kept, the field closed", got, m.ins.date)
	}
	if m.ins.buf.col != capturedStart {
		t.Errorf("caret col %d, want back on the timestamp", m.ins.buf.col)
	}
	m = sendKey(m, "enter")
	m = sendKey(m, "esc")
	if m.mode != normalMode || len(m.dirty) != 0 {
		t.Errorf("empty Esc: mode %d, dirty %v; want insert mode ended, nothing changed", m.mode, m.dirty)
	}
}

func TestDateFieldPreviewsTheResolvedDate(t *testing.T) {
	m := onCapturedAt(t)
	m.clock = func() time.Time { return time.Date(2026, 11, 17, 10, 0, 0, 0, time.Local) }
	m = sendKey(m, "enter")
	for _, c := range []struct{ typed, want string }{
		{"3d", "→ Fri 2026-11-20, in 3 days"},
		{"tomorrow", "→ Wed 2026-11-18, tomorrow"},
		{"2026-11-17 14:30", "→ Tue 2026-11-17 14:30, today"},
		{"2026-11-10", "→ Tue 2026-11-10, 7 days ago"},
	} {
		m = sendCtrl(m, tea.KeyCtrlU)
		m = typeKeys(m, c.typed)
		if got, _ := m.datePreview(); !strings.Contains(got, c.want) {
			t.Errorf("preview of %q = %q, want %q", c.typed, got, c.want)
		}
	}
}

func TestDateFieldEditsLikeALine(t *testing.T) {
	m := sendKey(onCapturedAt(t), "enter")
	m = typeKeys(m, "nxt fri")
	for range len(" fri") {
		m = sendCtrl(m, tea.KeyLeft)
	}
	m = sendCtrl(m, tea.KeyLeft)
	m = sendCtrl(m, tea.KeyLeft)
	m = typeKeys(m, "e")
	if got := m.ins.date.input.String(); got != "next fri" {
		t.Errorf("field = %q, want the e inserted mid-word", got)
	}
	lines, caret := m.insertEditorLines("", "", lipgloss.NoColor{})
	if want := m.caretStyle().Render("x"); !strings.Contains(lines[caret], want) {
		t.Errorf("caret line %q: caret not on the x after the inserted e", lines[caret])
	}
}

func TestEnterElsewhereStillBreaksTheLine(t *testing.T) {
	m := onCapturedAt(t)
	m.ins.buf.col = capturedStart - 1
	m = sendKey(m, "enter")
	if m.mode != insertMode || m.ins.buf.lines[capturedRow] != ":CAPTURED_AT:" {
		t.Errorf("Enter before a timestamp: mode %d, line %q; want an ordinary line break", m.mode, m.ins.buf.lines[capturedRow])
	}
}

func TestATimestampIsDrawnAsOneCaretBlock(t *testing.T) {
	m := onCapturedAt(t)
	lines, caret := m.insertEditorLines("", "", lipgloss.NoColor{})
	if want := m.caretStyle().Render("[2026-09-05 Sat 09:12]"); !strings.Contains(lines[caret], want) {
		t.Errorf("caret line %q doesn't draw the whole timestamp as the caret", lines[caret])
	}
	if !strings.Contains(stripANSI(m.View()), "date: Enter to change it") {
		t.Errorf("no timestamp hint on the command line")
	}
}

func TestTimestampPromptCtrlUAndCtrlW(t *testing.T) {
	m := sendKey(onCapturedAt(t), "enter")
	m = typeKeys(m, "2026-09-05 09:12")
	m = sendCtrl(m, tea.KeyCtrlW)
	if got := m.ins.date.input.String(); got != "2026-09-05 " {
		t.Errorf("ctrl-w = %q, want the time gone", got)
	}
	m = sendCtrl(m, tea.KeyCtrlU)
	if got := m.ins.date.input.String(); got != "" {
		t.Errorf("ctrl-u = %q, want it cleared", got)
	}
}
