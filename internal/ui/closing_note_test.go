package ui

import (
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/sburnett/orgtd/internal/org"
)

func closeWithNote(m Model, note string) Model {
	m = sendKey(m, "r")
	m = sendKey(m, "d")
	m = typeKeys(m, note)
	return sendKey(m, "enter")
}

func closingNoteModel(t *testing.T, content string) (Model, *org.Headline) {
	t.Helper()
	ws := agendaFixture(t, content)
	m := New(ws, WithClock(snippetsClock))
	m.height = 30
	m.cursor = findRow(t, m, "Task")
	return m, m.currentHeadline()
}

func TestClosingNoteIsLoggedLikeOrgMode(t *testing.T) {
	m, h := closingNoteModel(t, "* TODO Task\n  :PROPERTIES:\n  :CREATED: [2026-10-01 Thu 09:00]\n  :END:\n  Existing body\n")

	m = sendKey(m, "r")
	m = sendKey(m, "d")
	if m.mode != insertMode || m.ins.note == nil {
		t.Fatalf("mode after choosing DONE = %v (note %v), want insert mode, typing the note", m.mode, m.ins.note)
	}
	if h.Keyword != "TODO" {
		t.Fatalf("keyword = %q, want no change until the note is entered", h.Keyword)
	}
	// The entry, as it'll be, shows inline with the caret on an empty note.
	text := m.ins.buf.String()
	if !strings.HasPrefix(text, "DONE Task\nCLOSED: [2026-10-31 Sat 15:00]") || !strings.Contains(text, "- CLOSING NOTE [2026-10-31 Sat 15:00]") {
		t.Errorf("buffer = %q, want the state change and the note's heading in place", text)
	}
	if got := m.ins.buf.lines[m.ins.buf.row]; got != "  " {
		t.Errorf("caret line = %q, want the empty note line", got)
	}
	m = typeKeys(m, "Sent it")
	m = sendKey(m, "enter")

	want := "* DONE Task\n" +
		"  CLOSED: [2026-10-31 Sat 15:00]\n" +
		"  :PROPERTIES:\n  :CREATED: [2026-10-01 Thu 09:00]\n  :END:\n" +
		"  - CLOSING NOTE [2026-10-31 Sat 15:00] \\\\\n" +
		"    Sent it\n" +
		"  Existing body\n"
	if got := org.RenderEntry(h); got != want {
		t.Errorf("entry =\n%s\nwant\n%s", got, want)
	}
	if m.mode != normalMode {
		t.Errorf("mode = %v, want normalMode", m.mode)
	}
}

func TestClosingNoteSurvivesReload(t *testing.T) {
	m, h := closingNoteModel(t, "* TODO Task\n")
	closeWithNote(m, "Sent it")
	f, err := org.Parse(strings.NewReader(org.RenderEntry(h)), "x.org")
	if err != nil {
		t.Fatal(err)
	}
	got := f.Headlines[0]
	if got.Closed == nil || !reflect.DeepEqual(got.Body, h.Body) {
		t.Errorf("reparsed closed=%v body=%q, want body %q", got.Closed, got.Body, h.Body)
	}
}

func TestClosingNoteEmptyClosesWithoutNote(t *testing.T) {
	m, h := closingNoteModel(t, "* TODO Task\n")
	closeWithNote(m, "   ")
	if h.Keyword != "DONE" || h.Closed == nil || len(h.Body) != 0 {
		t.Errorf("keyword=%q closed=%v body=%q, want closed with no body", h.Keyword, h.Closed, h.Body)
	}
}

func TestClosingNoteEscAppliesItToo(t *testing.T) {
	m, h := closingNoteModel(t, "* TODO Task\n* TODO Other\n")
	m = sendKey(m, "2")
	m = sendKey(m, "r")
	m = sendKey(m, "d")
	m = typeKeys(m, "wrapped up")
	m = sendKey(m, "esc")
	if m.mode != normalMode || h.Keyword != "DONE" || h.Closed == nil || !strings.Contains(strings.Join(h.Body, "\n"), "wrapped up") {
		t.Errorf("mode=%v keyword=%q closed=%v body=%q, want it closed with the note", m.mode, h.Keyword, h.Closed, h.Body)
	}
	if len(m.selectModeTargets) != 0 || m.closingNoteInput != "" {
		t.Errorf("targets=%v input=%q, want the prompt's state cleared", m.selectModeTargets, m.closingNoteInput)
	}
	if m = sendKey(m, "r"); m.selectModeTargets != nil {
		t.Errorf("a later plain r still targets %v", m.selectModeTargets)
	}
}

func TestClosingNoteNotAskedWithoutACompletion(t *testing.T) {
	for _, tc := range []struct{ name, keyword, shortcut string }{
		{"active state", "TODO", "n"},
		{"done to done", "DONE", "c"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := closingNoteModel(t, "* "+tc.keyword+" Task\n")
			m = sendKey(m, "r")
			m = sendKey(m, tc.shortcut)
			if m.mode != normalMode {
				t.Errorf("mode = %v, want normalMode with no prompt", m.mode)
			}
		})
	}
}

func TestClosingNoteUndoIsOneStep(t *testing.T) {
	m, h := closingNoteModel(t, "* TODO Task\n  Existing body\n")
	m = closeWithNote(m, "Sent it")
	m = sendKey(m, "u")
	if h.Keyword != "TODO" || h.Closed != nil || !reflect.DeepEqual(h.Body, []string{"  Existing body"}) {
		t.Errorf("after undo keyword=%q closed=%v body=%q", h.Keyword, h.Closed, h.Body)
	}
	m = sendKey(m, "ctrl+r")
	if h.Keyword != "DONE" || len(h.Body) != 3 {
		t.Errorf("after redo keyword=%q body=%q", h.Keyword, h.Body)
	}
}

func TestClosingNoteOnRepeatingItemLogsStateLine(t *testing.T) {
	m, h := closingNoteModel(t, "* TODO Task\n  SCHEDULED: <2026-10-31 Sat +1w>\n")
	closeWithNote(m, "Done again")
	want := []string{
		`  - State "DONE"       from "TODO"       [2026-10-31 Sat 15:00] \\`,
		"    Done again",
	}
	if !reflect.DeepEqual(h.Body, want) {
		t.Errorf("body = %q, want %q", h.Body, want)
	}
	if h.Keyword != "TODO" || h.Closed != nil {
		t.Errorf("keyword=%q closed=%v, want a repeating item left open", h.Keyword, h.Closed)
	}
}

func TestClosingNoteBulkWritesOneNoteOnEachClosedEntry(t *testing.T) {
	m, h := closingNoteModel(t, "* TODO Task\n* DONE Already\n  CLOSED: [2026-10-01 Thu 09:00]\n* TODO Third\n")
	m = sendKey(m, "3")
	m = closeWithNote(m, "All sent")
	third := m.ws.Files[0].Headlines[2]
	already := m.ws.Files[0].Headlines[1]
	if len(h.Body) != 2 || len(third.Body) != 2 {
		t.Errorf("bodies = %q, %q, want a note on each", h.Body, third.Body)
	}
	if len(already.Body) != 0 {
		t.Errorf("already-closed entry body = %q, want no note", already.Body)
	}
	if got := len(m.undoStack); got != 1 {
		t.Errorf("undo steps = %d, want 1", got)
	}
}

func TestClosingNoteShownInSnippets(t *testing.T) {
	ws := agendaFixture(t, `* DONE With note
  CLOSED: [2026-10-31 Sat 14:00]
  - CLOSING NOTE [2026-10-31 Sat 14:00] \\
    Sent to the team
    and archived
  Plain body
* DONE Stale note
  CLOSED: [2026-10-31 Sat 13:00]
  - CLOSING NOTE [2026-10-20 Tue 13:00] \\
    From an earlier close
* TODO Repeating
  SCHEDULED: <2026-11-07 Sat +1w>
  :PROPERTIES:
  :LAST_REPEAT: [2026-10-31 Sat 12:00]
  :END:
  - State "DONE"       from "TODO"       [2026-10-31 Sat 12:00] \\
    Weekly one
`)
	m := New(ws, WithClock(snippetsClock))
	m.width, m.height = 80, 30
	m.switchToView(snippetsView)

	var got []string
	for _, r := range m.rows {
		if r.kind == rowBody {
			got = append(got, r.text)
		}
	}
	want := []string{"Sent to the team and archived", "Weekly one"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("note rows = %q, want %q", got, want)
	}

	m.cursor = findRow(t, m, "With note")
	m = sendKey(m, "j")
	if h := m.currentHeadline(); h == nil || h.Title != "Stale note" {
		t.Errorf("j landed on %v, want it to step over the note row", h)
	}
	if view := m.View(); !strings.Contains(view, "Sent to the team") {
		t.Errorf("view doesn't show the note:\n%s", view)
	}
}

func TestClosingNoteEscKeepsItAndUndoTakesItBack(t *testing.T) {
	m, h := closingNoteModel(t, "* TODO Task\n  Existing body\n")
	undoBefore := m.undoPos
	m = sendKey(m, "r")
	m = sendKey(m, "d")
	m = typeKeys(m, "all done")
	m = sendKey(m, "esc")
	if m.mode != normalMode || h.Keyword != "DONE" || h.Closed == nil || !strings.Contains(strings.Join(h.Body, "\n"), "all done") {
		t.Errorf("after Esc: mode %v, keyword %q, closed %v, body %q; want normal mode, closed with the note",
			m.mode, h.Keyword, h.Closed, h.Body)
	}
	if m.undoPos != undoBefore+1 {
		t.Errorf("undo steps = %d, want 1", m.undoPos-undoBefore)
	}
	m = sendKey(m, "u")
	if h.Keyword != "TODO" || h.Closed != nil || strings.Contains(strings.Join(h.Body, "\n"), "all done") {
		t.Errorf("after u: keyword %q, closed %v, body %q; want it all undone", h.Keyword, h.Closed, h.Body)
	}
}

func TestClosingNoteStaysOnItsLine(t *testing.T) {
	m, _ := closingNoteModel(t, "* TODO Task\n  Existing body\n")
	m = sendKey(m, "r")
	m = sendKey(m, "d")
	row := m.ins.buf.row
	m = typeKeys(m, "ab")
	for _, k := range []string{"up", "down", "backspace", "backspace", "backspace"} {
		m = sendKey(m, k)
	}
	if m.ins.buf.row != row || m.ins.buf.lines[row] != "  " {
		t.Errorf("caret on row %d, %q; want it kept on the (now empty) note line %d", m.ins.buf.row, m.ins.buf.lines[m.ins.buf.row], row)
	}
	if !strings.Contains(stripANSI(m.View()), "closing note: Enter or Esc when done") {
		t.Errorf("no closing-note hint on the command line")
	}
}

func TestClosingNoteIsEditedAtItsEndLikeATag(t *testing.T) {
	m, _ := closingNoteModel(t, "* TODO Task\n")
	m = sendKey(m, "r")
	m = sendKey(m, "d")
	lines, caret := m.insertEditorLines("", "", lipgloss.NoColor{})
	if want := m.caretStyle().Render(" "); !strings.Contains(lines[caret], want) {
		t.Errorf("empty note line %q, want one blank caret cell", lines[caret])
	}
	m = typeKeys(m, "sent i")
	m = sendCtrl(m, tea.KeyLeft) // no position within the note to move to
	m = typeKeys(m, "t")
	if got := m.noteText(); got != "sent it" {
		t.Errorf("note = %q, want typing to add at the end", got)
	}
	lines, caret = m.insertEditorLines("", "", lipgloss.NoColor{})
	if want := m.caretStyle().Render("sent it"); !strings.Contains(lines[caret], want) {
		t.Errorf("note line %q, want the caret over all of the note", lines[caret])
	}
}

func TestBulkClosingNoteCaretCoversTheNote(t *testing.T) {
	ws := agendaFixture(t, "* TODO Alpha\n* TODO Beta\n")
	m := New(ws, WithClock(snippetsClock))
	m.width, m.height = 80, 30
	m.cursor = findRow(t, m, "Alpha")
	m = sendKey(m, "2")
	m = sendKey(m, "R")
	m = sendKey(m, "d")
	m = typeKeys(m, "batch")
	if n := strings.Count(m.View(), m.caretStyle().Render("batch")); n != 2 {
		t.Errorf("notes drawn with the caret over them: %d, want 2", n)
	}
}
func TestBulkClosingNoteShowsUnderEachEntryBeingClosed(t *testing.T) {
	ws := agendaFixture(t, "* TODO Alpha\n  alpha body\n* TODO Beta\n* DONE Gamma\n")
	m := New(ws, WithClock(snippetsClock))
	m.width, m.height = 80, 30
	m.cursor = findRow(t, m, "Alpha")
	m = sendKey(m, "V")
	m = sendKey(m, "j")
	m = sendKey(m, "j")
	m = sendKey(m, "j")
	m = sendKey(m, "R")
	m = sendKey(m, "d")
	if m.mode != closingNoteMode {
		t.Fatalf("mode after R d on a selection = %v, want the closing note", m.mode)
	}
	m = typeKeys(m, "shipped it")

	view := stripANSI(m.View())
	lines := strings.Split(view, "\n")
	find := func(s string) int {
		for i, l := range lines {
			if strings.Contains(l, s) {
				return i
			}
		}
		return -1
	}
	alpha, beta, gamma := find("Alpha"), find("Beta"), find("Gamma")
	if n := strings.Count(view, "- CLOSING NOTE [2026-10-31 Sat 15:00]"); n != 2 {
		t.Errorf("CLOSING NOTE headings shown = %d, want 2 (Gamma's already done):\n%s", n, view)
	}
	if n := strings.Count(view, "shipped it"); n != 2 {
		t.Errorf("note shown %d times, want under both entries:\n%s", n, view)
	}
	// Under each title, before Alpha's body.
	if !strings.Contains(lines[alpha+1], "CLOSING NOTE") || !strings.Contains(lines[alpha+2], "shipped it") || !strings.Contains(lines[alpha+3], "alpha body") {
		t.Errorf("lines under Alpha = %q, want the note then its body", lines[alpha+1:alpha+4])
	}
	if !strings.Contains(lines[beta+2], "shipped it") || gamma != beta+3 {
		t.Errorf("lines under Beta = %q, want the note, then Gamma", lines[beta+1:gamma+1])
	}
	if !strings.Contains(view, "CLOSING NOTE for 2 entries") {
		t.Errorf("no bulk closing-note banner:\n%s", view)
	}
	if n := len(lines); n != m.height {
		t.Errorf("view is %d lines, want the %d-line screen", n, m.height)
	}

	m = sendKey(m, "enter")
	for _, title := range []string{"Alpha", "Beta"} {
		m.cursor = findRow(t, m, title)
		h := m.currentHeadline()
		if h.Keyword != "DONE" || !strings.Contains(strings.Join(h.Body, "\n"), "shipped it") {
			t.Errorf("%s after Enter: %q, body %q", title, h.Keyword, h.Body)
		}
	}
}

func TestBulkClosingNoteEscAppliesIt(t *testing.T) {
	ws := agendaFixture(t, "* TODO Alpha\n* TODO Beta\n")
	m := New(ws, WithClock(snippetsClock))
	m.cursor = findRow(t, m, "Alpha")
	m = sendKey(m, "2")
	m = sendKey(m, "R")
	m = sendKey(m, "d")
	m = typeKeys(m, "yep")
	m = sendKey(m, "esc")
	for _, title := range []string{"Alpha", "Beta"} {
		m.cursor = findRow(t, m, title)
		if h := m.currentHeadline(); h.Keyword != "DONE" || !strings.Contains(strings.Join(h.Body, "\n"), "yep") {
			t.Errorf("%s after Esc: %q, body %q; want closed with the note", title, h.Keyword, h.Body)
		}
	}
	if strings.Contains(stripANSI(m.View()), "CLOSING NOTE") {
		t.Errorf("note lines still shown after Esc")
	}
}

func TestCtrlCInAClosingNoteIsEsc(t *testing.T) {
	m, h := closingNoteModel(t, "* TODO Task\n")
	m = sendKey(m, "r")
	m = sendKey(m, "d")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = updated.(Model)
	if cmd != nil || m.mode != normalMode || h.Keyword != "DONE" {
		t.Errorf("ctrl-c in the note: cmd %v, mode %v, keyword %q; want it applied as Esc does, no quit", cmd, m.mode, h.Keyword)
	}
}

func TestCtrlCInABulkClosingNoteIsEsc(t *testing.T) {
	ws := agendaFixture(t, "* TODO Alpha\n* TODO Beta\n")
	m := New(ws, WithClock(snippetsClock))
	m.cursor = findRow(t, m, "Alpha")
	m = sendKey(m, "2")
	m = sendKey(m, "R")
	m = sendKey(m, "d")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = updated.(Model)
	if cmd != nil || m.mode != normalMode {
		t.Errorf("ctrl-c in a bulk note: cmd %v, mode %v; want it applied as Esc does, no quit", cmd, m.mode)
	}
}
