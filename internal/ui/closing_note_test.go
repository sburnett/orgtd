package ui

import (
	"reflect"
	"strings"
	"testing"

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
	if m.mode != closingNoteMode {
		t.Fatalf("mode after choosing DONE = %v, want closingNoteMode", m.mode)
	}
	if h.Keyword != "TODO" {
		t.Fatalf("keyword = %q, want no change until the note is entered", h.Keyword)
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

func TestClosingNoteEscCancelsStateChange(t *testing.T) {
	m, h := closingNoteModel(t, "* TODO Task\n* TODO Other\n")
	m = sendKey(m, "2")
	m = sendKey(m, "r")
	m = sendKey(m, "d")
	m = typeKeys(m, "abandoned")
	m = sendKey(m, "esc")
	if m.mode != normalMode || h.Keyword != "TODO" || h.Closed != nil || len(h.Body) != 0 {
		t.Errorf("mode=%v keyword=%q closed=%v body=%q, want nothing changed", m.mode, h.Keyword, h.Closed, h.Body)
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
