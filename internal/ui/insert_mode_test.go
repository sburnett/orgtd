package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/sburnett/orgtd/internal/config"
)

func TestInsertModeTypesIntoTheEntryAndEscAppliesIt(t *testing.T) {
	m := New(loadFixture(t))
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	h := m.currentHeadline()
	undoBefore := m.undoPos

	m = sendKey(m, "A")
	// Keys that are commands in normal mode are just text here.
	m = typeKeys(m, " and dd j")
	m = sendKey(m, "enter")
	m = typeKeys(m, "Bring the records.")
	if m.mode != insertMode {
		t.Fatalf("mode while typing = %d, want insertMode", m.mode)
	}
	if h.Title != "Call the vet about Fido's checkup" {
		t.Fatalf("title changed before Esc: %q", h.Title)
	}

	m = sendKey(m, "esc")
	if m.mode != normalMode {
		t.Fatalf("mode after Esc = %d, want normalMode", m.mode)
	}
	got := m.currentHeadline()
	if got.Title != "Call the vet about Fido's checkup and dd j" || got.Keyword != "TODO" {
		t.Errorf("entry after Esc = %q %q", got.Keyword, got.Title)
	}
	if len(got.Body) == 0 || strings.TrimSpace(got.Body[len(got.Body)-1]) != "Bring the records." {
		t.Errorf("body after Esc = %q, want the typed line last", got.Body)
	}
	if m.undoPos != undoBefore+1 {
		t.Errorf("undo steps = %d, want 1 for the whole session", m.undoPos-undoBefore)
	}

	m = sendKey(m, "u")
	if got := m.currentHeadline(); got.Title != "Call the vet about Fido's checkup" {
		t.Errorf("after u = %q, want the original title back", got.Title)
	}
}

func TestInsertModeWithNoChangeRecordsNothing(t *testing.T) {
	m := New(loadFixture(t))
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	h := m.currentHeadline()
	undoBefore := m.undoPos

	m = sendKey(m, "i")
	m = typeKeys(m, "x")
	m = sendKey(m, "backspace")
	m = sendKey(m, "esc")

	if m.undoPos != undoBefore || len(m.dirty) != 0 {
		t.Errorf("undo steps %d, dirty %v; want an unchanged entry to record nothing", m.undoPos-undoBefore, m.dirty)
	}
	if m.currentHeadline() != h {
		t.Errorf("entry was replaced despite no change")
	}
}

func TestInsertModeOTypesANewEntry(t *testing.T) {
	m := New(loadFixture(t))
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	undoBefore := m.undoPos

	m = sendKey(m, "o")
	if m.mode != insertMode || m.ins.insert == nil {
		t.Fatalf("after o: mode %d, session %+v; want an insert session for a new entry", m.mode, m.ins)
	}
	if !strings.Contains(m.ins.buf.String(), ":CREATED:") {
		t.Errorf("new entry's buffer = %q, want the CREATED template", m.ins.buf.String())
	}
	m = typeKeys(m, "TODO Buy dog treats")
	m = sendKey(m, "esc")

	h := m.currentHeadline()
	if h == nil || h.Title != "Buy dog treats" || h.Keyword != "TODO" {
		t.Fatalf("entry after Esc = %+v, want the typed TODO", h)
	}
	if h.Properties["CREATED"] == "" {
		t.Errorf("new entry lost its CREATED property")
	}
	if m.undoPos != undoBefore+1 {
		t.Errorf("undo steps = %d, want 1", m.undoPos-undoBefore)
	}
}

func TestInsertModeOWithNothingTypedIsCancelled(t *testing.T) {
	m := New(loadFixture(t))
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	orig := m.currentHeadline()
	rows := len(m.rows)

	m = sendKey(m, "o")
	m = sendKey(m, "esc")

	if len(m.rows) != rows || m.currentHeadline() != orig {
		t.Errorf("rows %d (want %d), cursor on %v; want the placeholder gone and the cursor back", len(m.rows), rows, m.currentHeadline())
	}
	if !strings.Contains(m.message, "cancelled") {
		t.Errorf("message = %q, want a cancellation notice", m.message)
	}
}

func TestCtrlCInInsertModeLeavesItInsteadOfQuitting(t *testing.T) {
	m := New(loadFixture(t))
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	m = sendKey(m, "A")
	m = typeKeys(m, "!")

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = updated.(Model)
	if cmd != nil {
		t.Fatalf("ctrl-c in insert mode returned a command (quit?)")
	}
	if m.mode != normalMode || m.currentHeadline().Title != "Call the vet about Fido's checkup!" {
		t.Errorf("mode %d, title %q; want insert mode left with the edit applied", m.mode, m.currentHeadline().Title)
	}
}

func TestCtrlGInInsertModeRoundTripsThroughTheEditor(t *testing.T) {
	m := New(loadFixture(t))
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	h := m.currentHeadline()
	m = sendKey(m, "A")
	m = typeKeys(m, " soon")

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	m = updated.(Model)
	if cmd == nil {
		t.Fatalf("ctrl-g in insert mode returned no editor command")
	}
	if m.mode != insertMode {
		t.Fatalf("mode while the editor runs = %d, want still insertMode", m.mode)
	}

	// The scratch buffer holds what was typed so far, with the trailer
	// saying exiting returns to insert mode.
	paths, _ := filepathGlobScratch(m)
	if len(paths) != 1 {
		t.Fatalf("scratch buffers = %v, want one", paths)
	}
	data, _ := os.ReadFile(paths[0])
	if !strings.HasPrefix(string(data), "TODO Call the vet about Fido's checkup soon\n") {
		t.Errorf("editor buffer starts %q, want the typed text", string(data))
	}
	if !strings.Contains(string(data), "go back to editing it in orgtd") {
		t.Errorf("editor buffer's trailer doesn't say exiting returns to insert mode:\n%s", data)
	}

	path := writeTempOrgFile(t, "TODO Call the vet tomorrow\n\n# a trailer line\n")
	updated, _ = m.Update(editFinishedMsg{path: path, target: h, resume: true})
	m = updated.(Model)
	if m.mode != insertMode {
		t.Fatalf("mode after the editor exits = %d, want back in insertMode", m.mode)
	}
	if got := m.ins.buf.String(); got != "TODO Call the vet tomorrow" {
		t.Errorf("buffer after the editor = %q, want its text, comments dropped", got)
	}
	if h.Title != "Call the vet about Fido's checkup" {
		t.Errorf("entry changed before Esc: %q", h.Title)
	}

	m = sendKey(m, "esc")
	if got := m.currentHeadline().Title; got != "Call the vet tomorrow" {
		t.Errorf("title after Esc = %q", got)
	}
}

func filepathGlobScratch(m Model) ([]string, error) {
	return filepath.Glob(filepath.Join(m.scratchDir(), "*", "*", "*", "*.org"))
}

func TestInsertModeDrawsTheBufferInPlace(t *testing.T) {
	m := New(loadFixture(t))
	m.width, m.height = 80, 30
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	m.ensureVisible()
	m = sendKey(m, "A")
	m = typeKeys(m, " XYZZY")
	m = sendKey(m, "enter")
	m = typeKeys(m, "second line")

	view := stripANSI(m.View())
	lines := strings.Split(view, "\n")
	titleAt, bodyAt := -1, -1
	for i, l := range lines {
		if strings.Contains(l, "TODO Call the vet about Fido's checkup XYZZY") {
			titleAt = i
		}
		if strings.Contains(l, "second line") {
			bodyAt = i
		}
	}
	if titleAt < 0 || bodyAt <= titleAt {
		t.Fatalf("buffer not drawn in the listing (title line %d, body line %d):\n%s", titleAt, bodyAt, view)
	}
	if !strings.Contains(view, "-- INSERT --") {
		t.Errorf("no insert-mode banner:\n%s", view)
	}
	if strings.Contains(view, "Editing:") {
		t.Errorf("drawn in the info buffer too, though the entry is on screen:\n%s", view)
	}
	if n := len(lines); n != m.height {
		t.Errorf("view is %d lines, want exactly the %d-line screen", n, m.height)
	}
}

func TestInsertModeWrapsLongLinesKeepingTheCaretOnScreen(t *testing.T) {
	m := New(loadFixture(t))
	m.width, m.height = 40, 20
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	m.ensureVisible()
	m = sendKey(m, "A")
	m = typeKeys(m, strings.Repeat(" word", 30))

	lead, blank := m.insertLead(m.rows[m.cursor], lipgloss.NoColor{})
	lines, caret := m.insertEditorLines(lead, blank, lipgloss.NoColor{})
	if len(lines) < 4 {
		t.Fatalf("a ~190-column line at width 40 drew as %d lines, want it wrapped", len(lines))
	}
	for i, l := range lines {
		if w := lipgloss.Width(l); w > m.width {
			t.Errorf("line %d is %d columns wide, past the %d-column screen", i, w, m.width)
		}
	}
	// The caret ends the title, the buffer's first line: on the last of
	// the screen lines it wrapped onto, before the entry's body lines.
	if caret < 3 || caret+1 >= len(lines) || !strings.Contains(stripANSI(lines[caret+1]), ":PROPERTIES:") {
		t.Errorf("caret on screen line %d of %q, want the title's last, just before the drawer", caret, stripANSILines(lines))
	}
}

func TestCaptureFromAgendaIsTypedInPlaceInTheOutline(t *testing.T) {
	m := New(loadFixture(t))
	m.width, m.height = 80, 30
	m.switchToView(agendaView)
	m = sendKey(m, "g")
	m = sendKey(m, "C")
	if m.mode != insertMode {
		t.Fatalf("mode after gC = %d, want insertMode", m.mode)
	}
	if m.view != outlineView || m.currentHeadline() != m.ins.target {
		t.Fatalf("after gC from the agenda: view %v, cursor on %v; want the outline, on the new entry", m.view, m.currentHeadline())
	}
	m = typeKeys(m, "Captured from agenda")

	view := stripANSI(m.View())
	inboxAt, entryAt := -1, -1
	for i, l := range strings.Split(view, "\n") {
		if strings.Contains(l, "inbox.org") && inboxAt < 0 {
			inboxAt = i
		}
		if strings.Contains(l, "Captured from agenda") {
			entryAt = i
		}
	}
	if inboxAt < 0 || entryAt <= inboxAt {
		t.Errorf("new entry not typed in place under inbox.org (inbox line %d, entry line %d):\n%s", inboxAt, entryAt, view)
	}

	m = sendKey(m, "esc")
	if m.view != outlineView || m.currentHeadline() == nil || m.currentHeadline().Title != "Captured from agenda" {
		t.Errorf("after Esc: view %v, cursor on %v; want the outline, on the new entry", m.view, m.currentHeadline())
	}
	m = sendKey(m, "ctrl+o")
	if m.view != agendaView {
		t.Errorf("ctrl-o after the capture: view %v, want back in the agenda", m.view)
	}
}

func TestCancelledCaptureFromAgendaReturnsToTheAgenda(t *testing.T) {
	m := New(loadFixture(t))
	m.switchToView(agendaView)
	m.cursor = len(m.rows) - 1
	m.ensureVisible()
	origin := m.currentHeadline()

	m = sendKey(m, "g")
	m = sendKey(m, "C")
	m = sendKey(m, "esc")

	if m.view != agendaView || m.currentHeadline() != origin {
		t.Errorf("after cancelling: view %v, cursor on %v; want the agenda, back on %v", m.view, m.currentHeadline(), origin)
	}
}

func TestITitleStartSkipsKeywordAndPriority(t *testing.T) {
	m := New(loadFixture(t))
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	m.currentHeadline().Priority = "A"
	m = sendKey(m, "i")
	if got := string(m.ins.buf.runes(0)[m.ins.buf.col:]); !strings.HasPrefix(got, "Call the vet") {
		t.Errorf("caret before %q, want at the title, past \"TODO [#A] \"", got)
	}
	m = typeKeys(m, "Really ")
	m = sendKey(m, "esc")
	if h := m.currentHeadline(); h.Keyword != "TODO" || h.Priority != "A" || h.Title != "Really Call the vet about Fido's checkup" {
		t.Errorf("after typing at the caret: %q [#%s] %q", h.Keyword, h.Priority, h.Title)
	}
}

func TestITitleStartWithoutKeywordIsLineStart(t *testing.T) {
	m := New(loadFixture(t))
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	m.currentHeadline().Keyword = ""
	m = sendKey(m, "i")
	if m.ins.buf.col != 0 {
		t.Errorf("caret col = %d, want 0 with no keyword", m.ins.buf.col)
	}
}

func TestAOnAnEmptyTitleStaysAtTheLineEnd(t *testing.T) {
	m := New(loadFixture(t))
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	h := m.currentHeadline()
	h.Title = ""
	m = sendKey(m, "a")
	if want := len([]rune(m.ins.buf.lines[0])); m.ins.buf.col != want {
		t.Errorf("caret col = %d on %q, want %d (the line end, not past it)", m.ins.buf.col, m.ins.buf.lines[0], want)
	}
}

func TestInsertModeUsesInsertBgAndDimsOtherRows(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(termenv.ANSI) })
	m := New(loadFixture(t), WithColors(config.ColorsConfig{InsertBg: "#123456"}))
	m.width, m.height = 80, 30
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	m.ensureVisible()
	other := m.rows[m.cursor-1] // the inbox.org file row, just above
	normal := m.renderRow(other)
	m = sendKey(m, "i")

	lines := m.listingWithInsert()
	var dimmed string
	for _, l := range lines {
		if stripANSI(l) == stripANSI(normal) {
			dimmed = l
		}
	}
	if dimmed == "" {
		t.Fatalf("row %q not drawn in %q", stripANSI(normal), lines)
	}
	if !strings.HasPrefix(dimmed, "\x1b[2m") {
		t.Errorf("other row %q isn't faint", dimmed)
	}
	// Faint, not recolored: its own colors are all still there, each
	// followed by faint again, since the row's styling may reset it.
	for _, seq := range sgrRe.FindAllString(normal, -1) {
		if !strings.Contains(dimmed, seq+"\x1b[2m") {
			t.Errorf("dimmed row %q lost %q, or faint after it", dimmed, seq)
		}
	}

	lead, blank := m.insertLead(m.rows[m.cursor], lipgloss.Color("#123456"))
	edit, _ := m.insertEditorLines(lead, blank, lipgloss.Color("#123456"))
	if !strings.Contains(edit[0], "48;2;18;52;86") {
		t.Fatalf("editor line %q isn't on insert_bg", edit[0])
	}
	if !strings.Contains(strings.Join(lines, "\n"), edit[0]) {
		t.Errorf("entry's first line isn't drawn on insert_bg")
	}
}
