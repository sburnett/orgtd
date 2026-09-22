package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sburnett/orgtd/internal/org"
)

func TestYankLeavesOriginalInPlace(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	idx := findRow(t, m, "Ship orgtd v0.1") // has children
	m.cursor = idx
	before := len(m.rows)
	orig := m.currentHeadline()

	m = sendKey(m, "y")
	m = sendKey(m, "y")

	if len(m.rows) != before {
		t.Fatalf("rows after yy = %d, want %d (nothing removed)", len(m.rows), before)
	}
	if m.rows[idx].headline != orig {
		t.Errorf("row %d changed after yy: %#v", idx, m.rows[idx])
	}
}

func TestYankPopulatesRegisterForPaste(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")

	m = sendKey(m, "y")
	m = sendKey(m, "y")

	if len(m.register) != 1 {
		t.Fatalf("register after yy = %v, want exactly 1 entry", m.register)
	}
	if m.register[0].Title != "Call the vet about Fido's checkup" {
		t.Errorf("register title = %q, want the yanked headline's title", m.register[0].Title)
	}

	m.cursor = findRow(t, m, "Read the RFC linked in yesterday's design review")
	m = sendKey(m, "p")

	// The original at its old position, plus the pasted copy: the title
	// should now appear twice.
	count := 0
	for _, r := range m.rows {
		if r.headline != nil && r.headline.Title == "Call the vet about Fido's checkup" {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("title appears %d times after yy+p, want 2 (original + pasted copy)", count)
	}
}

func TestYankRegisterIsIndependentSnapshot(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	orig := m.currentHeadline()

	m = sendKey(m, "y")
	m = sendKey(m, "y")

	if len(m.register) != 1 || m.register[0] == orig {
		t.Fatalf("register is the same pointer as the original; want an independent clone")
	}

	// Editing the original after yanking must not retroactively change
	// what's in the register.
	orig.Title = "Changed after yank"
	if m.register[0].Title == "Changed after yank" {
		t.Errorf("register reflects a post-yank edit to the original: %q", m.register[0].Title)
	}
}

func TestYankPasteIsUndoable(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	m = sendKey(m, "y")
	m = sendKey(m, "y")

	before := len(m.rows)
	m = sendKey(m, "p")
	if len(m.rows) != before+1 {
		t.Fatalf("rows after paste = %d, want %d", len(m.rows), before+1)
	}

	m = sendKey(m, "u")
	if len(m.rows) != before {
		t.Errorf("rows after undoing the paste = %d, want %d", len(m.rows), before)
	}
}

func TestYankIsDoubleYNotSingle(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")

	m = sendKey(m, "y")
	if m.register != nil {
		t.Fatalf("a single y already populated the register")
	}
	if !m.pendingY {
		t.Errorf("expected pendingY after a single y")
	}

	// An unrelated key cancels the pending y.
	m = sendKey(m, "j")
	m = sendKey(m, "y")
	if m.register != nil {
		t.Errorf("y after an intervening key still yanked")
	}
}

func TestYankNoopOnFileRow(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = 0
	if m.rows[0].file == nil {
		t.Fatalf("fixture assumption broken: row 0 is not a file row")
	}

	m = sendKey(m, "y")
	m = sendKey(m, "y")

	if m.register != nil {
		t.Errorf("yy on a file row populated the register: %v", m.register)
	}
}

func TestYankShowsConfirmationMessage(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")

	m = sendKey(m, "y")
	m = sendKey(m, "y")

	if m.message == "" {
		t.Errorf("expected a confirmation message after yy")
	}
}

func TestYankWorksOnAgendaItem(t *testing.T) {
	now := truncateToDate(time.Now())
	orgText := fmt.Sprintf("* TODO Agenda item\n  DEADLINE: <%s>\n", ts(now))
	ws := agendaFixture(t, orgText)
	m := New(ws)
	m.switchToView(agendaView)
	m.cursor = 1 // the item row, after the section header

	m = sendKey(m, "y")
	m = sendKey(m, "y")

	if len(m.register) != 1 || m.register[0].Title != "Agenda item" {
		t.Fatalf("register after yy in agenda view = %v, want the agenda item", m.register)
	}
	// The agenda row itself must still be there (not removed).
	if len(m.rows) != 2 {
		t.Errorf("rows after yy in agenda view = %d, want 2 (section header + item, unchanged)", len(m.rows))
	}
}

func TestYankWorksInClarifyView(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.enterClarifyView()
	m = sendKey(m, "g")
	m = sendKey(m, "c") // jump to the real row of the clarify target

	m = sendKey(m, "y")
	m = sendKey(m, "y")

	if len(m.register) != 1 || m.register[0].Title != m.clarifyTarget.Title {
		t.Errorf("register after yy in clarify view = %v, want a copy of the clarify target %v", m.register, m.clarifyTarget)
	}
}

func TestYankShowsInInfoBuffer(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")

	m = sendKey(m, "y")
	m = sendKey(m, "y")

	m.width, m.height = 100, len(m.rows)+m.infoBufferHeight()+3
	out := stripANSI(m.View())
	if !strings.Contains(out, "Register:") || !strings.Contains(out, "Call the vet about Fido's checkup") {
		t.Fatalf("view after yy = %q, want a pinned \"Register:\" section showing the yanked entry", out)
	}
}

func TestDeleteAlsoShowsInPinnedRegister(t *testing.T) {
	// The register is shared between dd and yy (see the register field's
	// own comment), so a plain dd should pin the deleted entry too, not
	// just yy.
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")

	m = sendKey(m, "d")
	m = sendKey(m, "d")

	m.width, m.height = 100, len(m.rows)+m.infoBufferHeight()+3
	out := stripANSI(m.View())
	if !strings.Contains(out, "Register:") || !strings.Contains(out, "Call the vet about Fido's checkup") {
		t.Fatalf("view after dd = %q, want a pinned \"Register:\" section showing the deleted entry", out)
	}
}

func TestRegisterPinnedSectionIsHeightBounded(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)

	// Simulate a register overflowing with more entries than a visual-mode
	// delete or a big <N>dd could plausibly display in full — the pinned
	// header must cap how many it shows rather than growing without bound.
	const total = maxRegisterPinnedLines + 3
	register := make([]*org.Headline, total)
	for i := range register {
		register[i] = &org.Headline{Level: 1, Title: fmt.Sprintf("Queued entry %d", i)}
	}
	m.register = register

	if got := m.registerPinnedLineCount(); got != maxRegisterPinnedLines+1 {
		t.Fatalf("registerPinnedLineCount() = %d, want %d (capped entries + 1 summary line)", got, maxRegisterPinnedLines+1)
	}

	lines := m.registerPinnedLines()
	if len(lines) != 1+maxRegisterPinnedLines+1 {
		t.Fatalf("registerPinnedLines() has %d lines, want %d (label + capped entries + summary)", len(lines), 1+maxRegisterPinnedLines+1)
	}
	for i := 0; i < maxRegisterPinnedLines; i++ {
		if !strings.Contains(lines[1+i], fmt.Sprintf("Queued entry %d", i)) {
			t.Errorf("line %d = %q, want entry %d", 1+i, lines[1+i], i)
		}
	}
	summary := stripANSI(lines[len(lines)-1])
	wantOverflow := total - maxRegisterPinnedLines
	if !strings.Contains(summary, fmt.Sprintf("...and %d more", wantOverflow)) {
		t.Errorf("summary line = %q, want it to mention %d more entries", summary, wantOverflow)
	}

	m.width, m.height = 100, len(m.rows)+m.infoBufferHeight()+3
	if !strings.Contains(stripANSI(m.View()), fmt.Sprintf("...and %d more", wantOverflow)) {
		t.Errorf("rendered view is missing the overflow summary line")
	}
}

func TestClearRegistersEmptiesRegister(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")

	m = sendKey(m, "y")
	m = sendKey(m, "y")
	if len(m.register) == 0 {
		t.Fatalf("register empty after yy, want an entry to clear")
	}

	m = sendKey(m, ":")
	m = typeKeys(m, "clear-registers")
	m, _ = sendKeyCmd(m, "enter")

	if m.register != nil {
		t.Errorf("register = %v, want nil after :clear-registers", m.register)
	}
	if m.message == "" {
		t.Errorf("expected a status message after :clear-registers")
	}
}
