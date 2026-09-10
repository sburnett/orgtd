package ui

import "testing"

func countPrefixFixtureModel(t *testing.T) Model {
	t.Helper()
	ws := agendaFixture(t, `* TODO A
* NEXT B
* WAITING C
* TODO D
`)
	return New(ws)
}

func TestCountPrefixDeleteRemovesNEntriesInOneUndoStep(t *testing.T) {
	m := countPrefixFixtureModel(t)
	m.cursor = findRow(t, m, "A")

	undoDepthBefore := m.undoPos
	m = sendKey(m, "3")
	m = sendKey(m, "d")
	m = sendKey(m, "d")

	for _, title := range []string{"A", "B", "C"} {
		if rowIndex(m, title) >= 0 {
			t.Errorf("%q should have been deleted by 3dd", title)
		}
	}
	if rowIndex(m, "D") < 0 {
		t.Error("D was outside the count of 3 and should remain")
	}
	if got := m.undoPos - undoDepthBefore; got != 1 {
		t.Errorf("undo steps pushed by 3dd = %d, want 1 (a single batched step)", got)
	}

	m.undo()
	for _, title := range []string{"A", "B", "C", "D"} {
		if rowIndex(m, title) < 0 {
			t.Errorf("after undo, %q should be restored", title)
		}
	}
}

func TestCountPrefixDeleteSkipsDescendantsOfSelectedAncestor(t *testing.T) {
	ws := agendaFixture(t, `* TODO A
** TODO Child of A
* TODO B
* TODO C
`)
	m := New(ws)
	m.cursor = findRow(t, m, "A")

	// 2dd covers the row range A, Child of A (2 entries) — Child of A is
	// filtered out as a descendant of the also-selected A, but deleting A
	// still takes the child with it (dd always removes a whole subtree).
	m = sendKey(m, "2")
	m = sendKey(m, "d")
	m = sendKey(m, "d")

	if rowIndex(m, "A") >= 0 || rowIndex(m, "Child of A") >= 0 {
		t.Error("A and its child should have been deleted")
	}
	if rowIndex(m, "B") < 0 || rowIndex(m, "C") < 0 {
		t.Error("B and C were outside the count of 2 and should remain")
	}
}

func TestCountPrefixDeleteOfOneOrZeroUsesPlainDeleteAndFillsRegister(t *testing.T) {
	m := countPrefixFixtureModel(t)
	m.cursor = findRow(t, m, "A")
	m.register = nil

	m = sendKey(m, "1")
	m = sendKey(m, "d")
	m = sendKey(m, "d")

	if rowIndex(m, "A") >= 0 {
		t.Error("A should have been deleted")
	}
	if m.register == nil || m.register.Title != "A" {
		t.Errorf("register = %v, want A (a count of 1 should behave exactly like plain dd)", m.register)
	}
}

func TestPlainDeleteWithoutCountStillFillsRegister(t *testing.T) {
	// Regression guard: refactoring dd to share code with the counted
	// path must not stop plain dd (no digits typed) from populating the
	// paste register the way it always has.
	m := countPrefixFixtureModel(t)
	m.cursor = findRow(t, m, "A")
	m.register = nil

	m = sendKey(m, "d")
	m = sendKey(m, "d")

	if m.register == nil || m.register.Title != "A" {
		t.Errorf("register = %v, want A", m.register)
	}
}

func TestCountPrefixDeleteAboveTwoDoesNotFillRegister(t *testing.T) {
	m := countPrefixFixtureModel(t)
	m.cursor = findRow(t, m, "A")
	m.register = nil

	m = sendKey(m, "3")
	m = sendKey(m, "d")
	m = sendKey(m, "d")

	if m.register != nil {
		t.Errorf("register = %v, want nil (bulk delete doesn't populate a single-entry register)", m.register)
	}
}

func TestCountPrefixDeleteIsNoOpOnAFileRow(t *testing.T) {
	m := countPrefixFixtureModel(t)
	m.cursor = 0 // the file header row

	m = sendKey(m, "3")
	m = sendKey(m, "d")
	m = sendKey(m, "d")

	for _, title := range []string{"A", "B", "C", "D"} {
		if rowIndex(m, title) < 0 {
			t.Errorf("%q should not have been touched by 3dd on a file row", title)
		}
	}
}

func TestCountPrefixIsDiscardedByAnUnrelatedKey(t *testing.T) {
	m := countPrefixFixtureModel(t)
	m.cursor = findRow(t, m, "A")

	m = sendKey(m, "3")
	m = sendKey(m, "j") // unrelated key — should discard the pending count
	m.cursor = findRow(t, m, "A")
	m = sendKey(m, "d")
	m = sendKey(m, "d")

	if rowIndex(m, "A") >= 0 {
		t.Error("A should have been deleted by the plain dd")
	}
	if rowIndex(m, "B") < 0 {
		t.Error("B should still be present — the earlier '3' should not have carried over to this dd")
	}
}

func TestCountPrefixBuildsMultiDigitNumberAndClampsAtEndOfList(t *testing.T) {
	m := countPrefixFixtureModel(t) // only 4 entries
	m.cursor = findRow(t, m, "A")

	m = sendKey(m, "1")
	m = sendKey(m, "0") // pendingCount = 10, far more entries than exist
	m = sendKey(m, "d")
	m = sendKey(m, "d")

	for _, title := range []string{"A", "B", "C", "D"} {
		if rowIndex(m, title) >= 0 {
			t.Errorf("%q should have been deleted (count clamps at the end of the list)", title)
		}
	}
}

func TestCountPrefixSetStatusAppliesToNEntriesInOneUndoStep(t *testing.T) {
	m := countPrefixFixtureModel(t)
	m.cursor = findRow(t, m, "A")

	undoDepthBefore := m.undoPos
	m = sendKey(m, "2")
	m = sendKey(m, "R")
	if m.mode != selectMode || len(m.selectModeTargets) != 2 {
		t.Fatalf("mode after 2R = %v (selectModeTargets=%v), want selectMode with 2 targets", m.mode, m.selectModeTargets)
	}
	m = sendKey(m, "d") // uniquely filters to DONE and auto-applies

	if m.mode != normalMode {
		t.Errorf("mode after choosing a status = %v, want normalMode", m.mode)
	}
	if h := findHeadlineByTitle(t, m, "A"); h.Keyword != "DONE" {
		t.Errorf("A keyword = %q, want DONE", h.Keyword)
	}
	if h := findHeadlineByTitle(t, m, "B"); h.Keyword != "DONE" {
		t.Errorf("B keyword = %q, want DONE", h.Keyword)
	}
	if h := findHeadlineByTitle(t, m, "C"); h.Keyword != "WAITING" {
		t.Errorf("C keyword = %q, want unchanged WAITING (outside the count of 2)", h.Keyword)
	}
	if got := m.undoPos - undoDepthBefore; got != 1 {
		t.Errorf("undo steps pushed by 2R = %d, want 1 (a single batched step)", got)
	}
}

func TestCountPrefixOfOneOrZeroActsLikePlainR(t *testing.T) {
	m := countPrefixFixtureModel(t)
	m.cursor = findRow(t, m, "A")

	m = sendKey(m, "1")
	m = sendKey(m, "R")
	if len(m.selectModeTargets) != 0 {
		t.Errorf("selectModeTargets = %v, want none (a count of 1 should behave like plain R)", m.selectModeTargets)
	}
	m = sendKey(m, "d")

	if h := findHeadlineByTitle(t, m, "A"); h.Keyword != "DONE" {
		t.Errorf("A keyword = %q, want DONE", h.Keyword)
	}
	if h := findHeadlineByTitle(t, m, "B"); h.Keyword != "NEXT" {
		t.Errorf("B keyword = %q, want unchanged NEXT", h.Keyword)
	}
}
