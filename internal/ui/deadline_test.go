package ui

import "testing"

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
	if m.cursor != len(m.rows)-1 {
		t.Fatalf("G did not jump to the last row")
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
