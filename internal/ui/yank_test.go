package ui

import (
	"fmt"
	"testing"
	"time"
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

	if m.register == nil {
		t.Fatalf("register is nil after yy")
	}
	if m.register.Title != "Call the vet about Fido's checkup" {
		t.Errorf("register title = %q, want the yanked headline's title", m.register.Title)
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

	if m.register == orig {
		t.Fatalf("register is the same pointer as the original; want an independent clone")
	}

	// Editing the original after yanking must not retroactively change
	// what's in the register.
	orig.Title = "Changed after yank"
	if m.register.Title == "Changed after yank" {
		t.Errorf("register reflects a post-yank edit to the original: %q", m.register.Title)
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

	if m.register == nil || m.register.Title != "Agenda item" {
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

	if m.register == nil || m.register.Title != m.clarifyTarget.Title {
		t.Errorf("register after yy in clarify view = %v, want a copy of the clarify target %v", m.register, m.clarifyTarget)
	}
}
