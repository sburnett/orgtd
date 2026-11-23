package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/sburnett/orgtd/internal/orgdate"
)

// vetEntry puts m's cursor on the fixture's "TODO Call the vet about
// Fido's checkup" entry.
func vetEntry(t *testing.T) Model {
	t.Helper()
	m := New(loadFixture(t))
	m.width, m.height = 80, 30
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	m.ensureVisible()
	return m
}

func firstLine(m Model) string { return m.ins.buf.lines[0] }

func TestCapitalIStartsOnTheKeywordAndShowsThePicker(t *testing.T) {
	m := sendKey(vetEntry(t), "I")
	if !m.ins.onKeyword {
		t.Fatalf("I didn't put the caret on the keyword")
	}
	view := stripANSI(m.View())
	if !strings.Contains(view, "Status:") || !strings.Contains(view, "[n] NEXT") {
		t.Errorf("status picker not shown on the keyword:\n%s", view)
	}

	m = sendKey(m, "x") // not a shortcut
	if firstLine(m) != "TODO Call the vet about Fido's checkup" || !m.ins.onKeyword {
		t.Errorf("a non-shortcut letter on the keyword changed things: %q, onKeyword %v", firstLine(m), m.ins.onKeyword)
	}
}

func TestTypingAShortcutOnTheKeywordSetsItAndMovesToTheTitle(t *testing.T) {
	m := vetEntry(t)
	undoBefore := m.history.pos
	m = sendKey(m, "I")
	m = sendKey(m, "n")
	if firstLine(m) != "NEXT Call the vet about Fido's checkup" {
		t.Fatalf("first line = %q, want the keyword switched to NEXT", firstLine(m))
	}
	if m.ins.onKeyword || m.ins.buf.col != len("NEXT ") {
		t.Errorf("caret onKeyword %v col %d, want at the title's start", m.ins.onKeyword, m.ins.buf.col)
	}
	m = typeKeys(m, "Really ")
	m = sendKey(m, "esc")
	h := m.currentHeadline()
	if h.Keyword != "NEXT" || h.Title != "Really Call the vet about Fido's checkup" {
		t.Errorf("after Esc: %q %q", h.Keyword, h.Title)
	}
	if m.history.pos != undoBefore+1 {
		t.Errorf("undo steps = %d, want 1", m.history.pos-undoBefore)
	}
}

func TestMotionsStepOverTheKeywordAsOneCharacter(t *testing.T) {
	m := sendKey(vetEntry(t), "i") // at the title's start
	m = sendCtrl(m, tea.KeyLeft)
	if !m.ins.onKeyword {
		t.Fatalf("← from the title's start didn't land on the keyword")
	}
	m = sendCtrl(m, tea.KeyLeft)
	if !m.ins.onKeyword {
		t.Errorf("← on the keyword left it")
	}
	m = sendCtrl(m, tea.KeyRight)
	if m.ins.onKeyword || m.ins.buf.col != len("TODO ") {
		t.Errorf("→ from the keyword: onKeyword %v col %d, want the title's start", m.ins.onKeyword, m.ins.buf.col)
	}
	m = sendCtrl(m, tea.KeyRight)
	if m.ins.buf.col != len("TODO C") {
		t.Errorf("→ in the title: col %d, want one character on", m.ins.buf.col)
	}
}

func TestHomeGoesToTheTitleThenTheKeyword(t *testing.T) {
	m := sendKey(vetEntry(t), "A")
	m = sendCtrl(m, tea.KeyHome)
	if m.ins.onKeyword || m.ins.buf.col != len("TODO ") {
		t.Fatalf("Home from the line's end: onKeyword %v col %d, want the title's start", m.ins.onKeyword, m.ins.buf.col)
	}
	m = sendCtrl(m, tea.KeyHome)
	if !m.ins.onKeyword {
		t.Errorf("Home again didn't reach the keyword")
	}
}

func TestUpFromTheBodySkipsOverTheKeyword(t *testing.T) {
	m := sendKey(vetEntry(t), "i")
	m = sendKey(m, "down")
	m = sendCtrl(m, tea.KeyHome)
	m = sendKey(m, "up")
	if m.ins.onKeyword || m.ins.buf.row != 0 || m.ins.buf.col != len("TODO ") {
		t.Errorf("↑ to column 0 of the first line: onKeyword %v at %d:%d, want the title's start", m.ins.onKeyword, m.ins.buf.row, m.ins.buf.col)
	}
}

func TestBackspaceAtTheTitleStartRemovesTheKeyword(t *testing.T) {
	m := sendKey(vetEntry(t), "i")
	m = sendKey(m, "backspace")
	if firstLine(m) != "Call the vet about Fido's checkup" || m.ins.buf.col != 0 || m.ins.onKeyword {
		t.Errorf("after backspace: %q col %d onKeyword %v; want the keyword gone, caret at the title", firstLine(m), m.ins.buf.col, m.ins.onKeyword)
	}
	m = sendKey(m, "esc")
	if h := m.currentHeadline(); h.Keyword != "" || h.Title != "Call the vet about Fido's checkup" {
		t.Errorf("after Esc: %q %q", h.Keyword, h.Title)
	}
}

func TestCtrlWStopsAtTheTitleThenRemovesTheKeyword(t *testing.T) {
	m := sendKey(vetEntry(t), "i")
	m = sendCtrl(m, tea.KeyRight)
	m = sendCtrl(m, tea.KeyRight)
	m = sendCtrl(m, tea.KeyCtrlW)
	if firstLine(m) != "TODO ll the vet about Fido's checkup" {
		t.Fatalf("ctrl-w inside the first word = %q, want only the title's part deleted", firstLine(m))
	}
	m = sendCtrl(m, tea.KeyCtrlW)
	if firstLine(m) != "ll the vet about Fido's checkup" {
		t.Errorf("ctrl-w at the title's start = %q, want the keyword gone", firstLine(m))
	}
}

func TestCtrlURemovesTheTitleSoFarAndTheKeyword(t *testing.T) {
	m := sendKey(vetEntry(t), "i")
	for range len("Call ") {
		m = sendCtrl(m, tea.KeyRight)
	}
	m = sendCtrl(m, tea.KeyCtrlU)
	if firstLine(m) != "the vet about Fido's checkup" || m.ins.buf.col != 0 {
		t.Errorf("ctrl-u = %q (col %d), want keyword and text before the caret gone", firstLine(m), m.ins.buf.col)
	}
}

func TestDeleteOnTheKeywordLeavesAnEmptySlot(t *testing.T) {
	m := sendKey(vetEntry(t), "I")
	m = sendCtrl(m, tea.KeyDelete)
	if firstLine(m) != "Call the vet about Fido's checkup" || !m.ins.onKeyword {
		t.Errorf("Delete on the keyword: %q, onKeyword %v; want it gone, caret still on the slot", firstLine(m), m.ins.onKeyword)
	}
	m = sendKey(m, "w")
	if firstLine(m) != "WAITING Call the vet about Fido's checkup" {
		t.Errorf("a shortcut on the empty slot = %q, want WAITING added", firstLine(m))
	}
}

func TestNewEntryKeywordSlot(t *testing.T) {
	m := sendKey(vetEntry(t), "o")
	if m.ins.onKeyword {
		t.Fatalf("o started on the keyword slot, want the (empty) title")
	}
	m = sendCtrl(m, tea.KeyLeft)
	if !m.ins.onKeyword {
		t.Fatalf("← from an empty title didn't reach the slot")
	}
	m = sendKey(m, "t")
	m = typeKeys(m, "Buy treats")
	m = sendKey(m, "esc")
	h := m.currentHeadline()
	if h.Keyword != "TODO" || h.Title != "Buy treats" || h.Properties["CREATED"] == "" {
		t.Errorf("new entry = %q %q %v", h.Keyword, h.Title, h.Properties)
	}
}

func TestDoneOnTheKeywordAsksForANoteInline(t *testing.T) {
	m := vetEntry(t)
	undoBefore := m.history.pos
	m = sendKey(m, "I")
	m = typeKeys(m, "d")
	if m.mode != insertMode || m.ins.note == nil {
		t.Fatalf("after d on the keyword: mode %d, note %v; want insert mode typing a note", m.mode, m.ins.note)
	}
	text := m.ins.buf.String()
	if !strings.HasPrefix(text, "DONE Call the vet") || !strings.Contains(text, "CLOSED:") || !strings.Contains(text, "- CLOSING NOTE") {
		t.Errorf("buffer = %q, want the keyword, CLOSED and the note's heading in place", text)
	}
	m = typeKeys(m, "booked for Tuesday")
	m = sendKey(m, "enter")
	if m.mode != insertMode || m.ins.note != nil {
		t.Fatalf("after Enter: mode %d, note %v; want insert mode carrying on", m.mode, m.ins.note)
	}
	if m.ins.buf.row != 0 || m.ins.buf.col != len("DONE ") {
		t.Errorf("caret at %d:%d, want the title's start", m.ins.buf.row, m.ins.buf.col)
	}
	if h := m.currentHeadline(); h.Keyword != "TODO" {
		t.Errorf("outline changed before Esc: %q", h.Keyword)
	}

	m = sendKey(m, "esc")
	h := m.currentHeadline()
	if h.Keyword != "DONE" || h.Closed == nil || !strings.Contains(strings.Join(h.Body, "\n"), "booked for Tuesday") {
		t.Errorf("after Esc: keyword %q, closed %v, body %q", h.Keyword, h.Closed, h.Body)
	}
	if m.history.pos != undoBefore+1 {
		t.Errorf("undo steps = %d, want 1", m.history.pos-undoBefore)
	}
	m = sendKey(m, "u")
	if h := m.currentHeadline(); h.Keyword != "TODO" || h.Closed != nil {
		t.Errorf("after u: %q closed %v, want it all undone", h.Keyword, h.Closed)
	}
}

func TestBlankNoteOnTheKeywordLeavesNoNote(t *testing.T) {
	m := sendKey(vetEntry(t), "I")
	m = typeKeys(m, "d")
	m = sendKey(m, "enter")
	if text := m.ins.buf.String(); !strings.HasPrefix(text, "DONE ") || strings.Contains(text, "CLOSING NOTE") {
		t.Errorf("buffer = %q, want DONE with no note", text)
	}
}

func TestEscFromTheNoteOnTheKeywordKeepsItAndEndsTheEdit(t *testing.T) {
	m := sendKey(vetEntry(t), "I")
	m = typeKeys(m, "d")
	m = typeKeys(m, "done it")
	m = sendKey(m, "esc")
	if m.mode != normalMode {
		t.Fatalf("after Esc in the note: mode %d, want insert mode ended", m.mode)
	}
	h := m.currentHeadline()
	if h.Keyword != "DONE" || h.Closed == nil || !strings.Contains(strings.Join(h.Body, "\n"), "done it") {
		t.Errorf("after Esc: keyword %q, closed %v, body %q; want closed with the note", h.Keyword, h.Closed, h.Body)
	}
}
func TestDoneOnARepeatingKeywordAdvancesItsDate(t *testing.T) {
	today := orgdate.TruncateToDate(time.Now())
	m := New(agendaFixture(t, fmt.Sprintf("* TODO Weekly review\n  SCHEDULED: <%s +1w>\n", ts(today))))
	m.cursor = findRow(t, m, "Weekly review")
	m = sendKey(m, "I")
	m = sendKey(m, "d")
	m = sendKey(m, "enter") // no note
	text := m.ins.buf.String()
	if !strings.HasPrefix(text, "TODO Weekly review") || !strings.Contains(text, ts(today.AddDate(0, 0, 7))) || !strings.Contains(text, "LAST_REPEAT") {
		t.Errorf("buffer after DONE on a repeating item = %q, want it still TODO, a week on, with LAST_REPEAT", text)
	}
}

func TestReferenceViewHasNoKeywordSlot(t *testing.T) {
	m := vetEntry(t)
	m.view = referenceView
	m = sendKey(m, "I")
	if m.ins.onKeyword || m.ins.buf.col != 0 {
		t.Errorf("I in :reference: onKeyword %v col %d, want plain column 0", m.ins.onKeyword, m.ins.buf.col)
	}
	m = sendCtrl(m, tea.KeyRight)
	if m.ins.buf.col != 1 {
		t.Errorf("→ in :reference: col %d, want 1 (the keyword is ordinary text)", m.ins.buf.col)
	}
}

func TestTheKeywordIsDrawnAsOneCaretBlock(t *testing.T) {
	m := sendKey(vetEntry(t), "I")
	plain := lipgloss.NewStyle()
	lines, _ := m.insertEditorLines("", "", lipgloss.NoColor{})
	if want := m.caretStyle().Render("TODO"); !strings.Contains(lines[0], want) {
		t.Errorf("first line %q doesn't draw the whole keyword as the caret %q", lines[0], want)
	}

	m = sendCtrl(m, tea.KeyDelete) // now an empty slot
	lines, _ = m.insertEditorLines("", "", lipgloss.NoColor{})
	if want := m.caretStyle().Render(" ") + plain.Render("Call"); !strings.HasPrefix(stripANSI(lines[0]), " Call") || !strings.Contains(lines[0], m.caretStyle().Render(" ")) {
		t.Errorf("empty slot line %q, want a blank caret cell before the title (%q)", lines[0], want)
	}
}

// sendCtrl sends a key that keyMsgFor has no name for.
func sendCtrl(m Model, k tea.KeyType) Model {
	updated, _ := m.Update(tea.KeyMsg{Type: k})
	return updated.(Model)
}

func TestRPicksAKeywordAndEndsInsertMode(t *testing.T) {
	m := vetEntry(t)
	h := m.currentHeadline()
	undoBefore := m.history.pos
	m = sendKey(m, "r")
	m = sendKey(m, "n")
	if m.mode != normalMode {
		t.Fatalf("mode after r n = %v, want normal mode", m.mode)
	}
	// Applied in place, as r always has: the same headline, one step.
	if h.Keyword != "NEXT" || m.currentHeadline() != h {
		t.Errorf("keyword %q (same headline: %v), want NEXT on the same headline", h.Keyword, m.currentHeadline() == h)
	}
	if m.history.pos != undoBefore+1 {
		t.Errorf("undo steps = %d, want 1", m.history.pos-undoBefore)
	}
}

func TestREscChangesNothing(t *testing.T) {
	m := vetEntry(t)
	undoBefore := m.history.pos
	m = sendKey(m, "r")
	m = sendKey(m, "esc")
	if m.mode != normalMode || m.history.pos != undoBefore || len(m.dirty) != 0 {
		t.Errorf("r then Esc: mode %v, undo steps %d, dirty %v; want nothing changed", m.mode, m.history.pos-undoBefore, m.dirty)
	}
}

func TestRDeleteRemovesTheKeyword(t *testing.T) {
	m := vetEntry(t)
	h := m.currentHeadline()
	m = sendKey(m, "r")
	m = sendCtrl(m, tea.KeyDelete)
	if m.mode != normalMode || h.Keyword != "" {
		t.Errorf("r then Delete: mode %v, keyword %q; want it removed, back in normal mode", m.mode, h.Keyword)
	}
}

func TestRThenMovingIntoTheTextIsAnOrdinaryEdit(t *testing.T) {
	m := vetEntry(t)
	m = sendKey(m, "r")
	m = sendCtrl(m, tea.KeyRight) // into the title
	m = typeKeys(m, "Soon: ")
	if m.mode != insertMode {
		t.Fatalf("mode after typing in the title = %v, want still insert mode", m.mode)
	}
	m = sendCtrl(m, tea.KeyLeft)
	m = sendCtrl(m, tea.KeyHome)
	m = sendCtrl(m, tea.KeyHome) // back on the keyword
	m = sendKey(m, "n")
	if m.mode != insertMode {
		t.Fatalf("choosing a keyword after editing: mode %v, want still insert mode (it's an ordinary edit now)", m.mode)
	}
	m = sendKey(m, "esc")
	if h := m.currentHeadline(); h.Keyword != "NEXT" || h.Title != "Soon: Call the vet about Fido's checkup" {
		t.Errorf("after Esc: %q %q", h.Keyword, h.Title)
	}
}

func TestROnALockedEntryRefuses(t *testing.T) {
	m := vetEntry(t)
	m.immutable[m.currentHeadline()] = true
	m = sendKey(m, "r")
	if m.mode != normalMode {
		t.Errorf("r on a locked entry: mode %v, want normal mode", m.mode)
	}
}

func TestBackspaceOnTheKeywordRemovesIt(t *testing.T) {
	m := sendKey(vetEntry(t), "I")
	m = sendKey(m, "backspace")
	if firstLine(m) != "Call the vet about Fido's checkup" || !m.ins.onKeyword {
		t.Errorf("Backspace on the keyword: %q, onKeyword %v; want it gone, caret still on the slot", firstLine(m), m.ins.onKeyword)
	}

	m = sendKey(vetEntry(t), "r")
	m = sendKey(m, "backspace")
	if h := m.currentHeadline(); m.mode != normalMode || h.Keyword != "" {
		t.Errorf("r then Backspace: mode %v, keyword %q; want it removed, back in normal mode", m.mode, h.Keyword)
	}
}
