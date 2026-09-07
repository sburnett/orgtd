package ui

import (
	"strings"
	"testing"

	"github.com/sburnett/orgtd/internal/org"
)

func TestSetMarkAndJumpBack(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	idx := findRow(t, m, "Follow up with finance about the Q3 budget doc")
	m.cursor = idx

	m = sendKey(m, "m")
	m = sendKey(m, "a")

	if h := m.marks['a']; h == nil || h.Title != "Follow up with finance about the Q3 budget doc" {
		t.Fatalf("marks['a'] = %v, want the marked headline", h)
	}

	m.cursor = findFileRow(t, m, "projects.org")
	m = sendKey(m, "'")
	m = sendKey(m, "a")

	if m.cursor != idx {
		t.Errorf("cursor after 'a = %d, want %d (back at the mark)", m.cursor, idx)
	}
}

func TestSetMarkNoopOnFileRow(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = 0
	if m.rows[0].file == nil {
		t.Fatalf("fixture assumption broken: row 0 is not a file row")
	}

	m = sendKey(m, "m")
	m = sendKey(m, "a")

	if _, ok := m.marks['a']; ok {
		t.Errorf("mark 'a' was set on a file row")
	}
}

func TestReMarkingSameLetterMovesIt(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	m = sendKey(m, "m")
	m = sendKey(m, "a")

	m.cursor = findRow(t, m, "Follow up with finance about the Q3 budget doc")
	m = sendKey(m, "m")
	m = sendKey(m, "a")

	if m.marks['a'].Title != "Follow up with finance about the Q3 budget doc" {
		t.Errorf("marks['a'] = %v, want the re-marked headline", m.marks['a'])
	}
}

func TestMarkingWithSameLetterAgainClearsIt(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	m = sendKey(m, "m")
	m = sendKey(m, "a")
	if _, ok := m.marks['a']; !ok {
		t.Fatalf("fixture assumption broken: mark 'a' not set")
	}

	m = sendKey(m, "m")
	m = sendKey(m, "a")

	if _, ok := m.marks['a']; ok {
		t.Errorf("mark 'a' still set after marking the same entry 'a' again (should toggle off)")
	}
}

func TestMarkingWithDifferentLetterReplacesTheOldOne(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	idx := findRow(t, m, "Call the vet about Fido's checkup")
	m.cursor = idx
	m = sendKey(m, "m")
	m = sendKey(m, "a")

	m = sendKey(m, "m")
	m = sendKey(m, "b")

	if _, ok := m.marks['a']; ok {
		t.Errorf("mark 'a' still set after re-marking the same entry as 'b' (an entry should hold at most one mark)")
	}
	if h, ok := m.marks['b']; !ok || h != m.rows[idx].headline {
		t.Errorf("marks['b'] = %v, want the entry", m.marks['b'])
	}
}

func TestMarkingDoesNotAffectOtherEntriesMarks(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	m = sendKey(m, "m")
	m = sendKey(m, "a")

	m.cursor = findRow(t, m, "Follow up with finance about the Q3 budget doc")
	m = sendKey(m, "m")
	m = sendKey(m, "b")
	// Re-marking the second entry with a new letter shouldn't touch the
	// first entry's unrelated mark.
	m = sendKey(m, "m")
	m = sendKey(m, "c")

	if _, ok := m.marks['a']; !ok {
		t.Errorf("mark 'a' on an unrelated entry was cleared")
	}
	if _, ok := m.marks['b']; ok {
		t.Errorf("mark 'b' still set after replacing it with 'c' on the same entry")
	}
	if _, ok := m.marks['c']; !ok {
		t.Errorf("mark 'c' not set")
	}
}

func TestMarkSurvivesEditingTheMarkedEntry(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	idx := findRow(t, m, "Call the vet about Fido's checkup")
	m.cursor = idx
	m = sendKey(m, "m")
	m = sendKey(m, "a")
	old := m.currentHeadline()

	path := writeTempOrgFile(t, "* NEXT Call the vet about Fido's checkup ASAP\n")
	updated, _ := m.Update(editFinishedMsg{path: path, target: old})
	m = updated.(Model)

	newH := m.rows[idx].headline
	if newH == old {
		t.Fatalf("fixture assumption broken: expected the headline pointer to change")
	}
	if m.marks['a'] != newH {
		t.Errorf("marks['a'] = %v, want the edited headline %v (the mark should follow the edit)", m.marks['a'], newH)
	}

	line := stripANSI(m.renderRow(m.rows[idx]))
	if !strings.HasPrefix(line, "a") {
		t.Errorf("edited row = %q, want the 'a' marker still in the gutter", line)
	}
}

func TestMarkFollowsUndoOfAnEdit(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	idx := findRow(t, m, "Call the vet about Fido's checkup")
	m.cursor = idx
	m = sendKey(m, "m")
	m = sendKey(m, "a")
	old := m.currentHeadline()

	path := writeTempOrgFile(t, "* NEXT Call the vet about Fido's checkup ASAP\n")
	updated, _ := m.Update(editFinishedMsg{path: path, target: old})
	m = updated.(Model)
	edited := m.marks['a']

	m = sendKey(m, "u")

	if m.marks['a'] != old {
		t.Errorf("marks['a'] after undoing the edit = %v, want the original headline %v back", m.marks['a'], old)
	}

	m = sendKey(m, "ctrl+r")
	if m.marks['a'] != edited {
		t.Errorf("marks['a'] after redoing the edit = %v, want the edited headline %v", m.marks['a'], edited)
	}
}

func TestRemapHeadlineRefsClearsMarkOnADescendantNotTheReplacedRoot(t *testing.T) {
	// Exercises remapHeadlineRefs directly for the case a full edit
	// flow can't easily set up: a mark on a *descendant* of the
	// headline being replaced (e.g. editing a whole file at once, which
	// replaces every top-level headline and its subtree). A descendant
	// has no reliable counterpart in the freshly-parsed replacement, so
	// its mark should be cleared, not left dangling on a headline no
	// longer in any tree.
	ws := loadFixture(t)
	m := New(ws)
	root := m.rows[findRow(t, m, "Ship orgtd v0.1")].headline
	child := root.Children[0]

	m.cursor = findRow(t, m, child.Title)
	m = sendKey(m, "m")
	m = sendKey(m, "a")

	newRoot := org.CloneHeadline(root)
	m.remapHeadlineRefs([]*org.Headline{root}, []*org.Headline{newRoot})

	if _, ok := m.marks['a']; ok {
		t.Errorf("mark on a descendant survived a subtree replace it wasn't the root of, want it cleared")
	}
}

func TestJumpToUnsetMarkShowsMessage(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)

	m = sendKey(m, "'")
	m = sendKey(m, "z")

	if m.message == "" {
		t.Errorf("expected a message for jumping to an unset mark")
	}
}

func TestMultipleMarksStackInPinnedHeader(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	m = sendKey(m, "m")
	m = sendKey(m, "a")
	m.cursor = findRow(t, m, "Follow up with finance about the Q3 budget doc")
	m = sendKey(m, "m")
	m = sendKey(m, "b")

	m.width, m.height = 100, len(m.rows)+m.pinnedHeaderHeight()+3
	out := stripANSI(m.View())
	lines := strings.Split(out, "\n")

	if strings.TrimRight(lines[0], " ") != "Active marks:" {
		t.Fatalf("line 0 = %q, want %q (plus trailing background padding)", lines[0], "Active marks:")
	}
	if !strings.Contains(lines[1], "Call the vet about Fido's checkup") {
		t.Errorf("line 1 = %q, want mark a's item first (sorted)", lines[1])
	}
	if !strings.Contains(lines[2], "Follow up with finance about the Q3 budget doc") {
		t.Errorf("line 2 = %q, want mark b's item second", lines[2])
	}
	if strings.TrimRight(lines[3], " ") != "" {
		t.Errorf("line 3 = %q, want a blank (background-padded) separator after the pinned marks", lines[3])
	}
}

func TestMarkedRowShowsLetterInGutter(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	idx := findRow(t, m, "Call the vet about Fido's checkup")
	m.cursor = idx
	m = sendKey(m, "m")
	m = sendKey(m, "a")

	line := m.renderRow(m.rows[idx])
	if !strings.Contains(line, "a") {
		t.Errorf("marked row = %q, want the 'a' marker", line)
	}
	other := m.renderRow(m.rows[idx+1])
	if strings.HasPrefix(stripANSI(other), "a") {
		t.Errorf("unrelated row = %q, should not carry the mark", other)
	}
}

func TestMarkedAndDirtyRowShowsBothIndicators(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	idx := findRow(t, m, "Call the vet about Fido's checkup")
	m.cursor = idx
	m = sendKey(m, "m")
	m = sendKey(m, "a")
	m = sendKey(m, "r") // dirty it via a status rotate

	line := []rune(stripANSI(m.renderRow(m.rows[idx])))
	if len(line) < 2 || line[0] != 'a' {
		t.Fatalf("row = %q, want the mark in column 0", string(line))
	}
	if line[1] != '+' {
		t.Errorf("row = %q, want the dirty marker in column 1 alongside the mark", string(line))
	}
}

func TestMarksVisibleInEveryView(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	m = sendKey(m, "m")
	m = sendKey(m, "a")

	for _, v := range []viewKind{outlineView, agendaView, clarifyView} {
		m.switchToView(v)
		if len(m.pinnedHeaderLines()) == 0 {
			t.Errorf("view %v: pinnedHeaderLines is empty, want the 'a' mark to still show", v)
		}
	}
}

func TestDeletingMarkedHeadlineClearsItsMark(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	m = sendKey(m, "m")
	m = sendKey(m, "a")

	m = sendKey(m, "d")
	m = sendKey(m, "d")

	if _, ok := m.marks['a']; ok {
		t.Errorf("mark 'a' still set after its headline was deleted")
	}
}

func TestDeletingParentClearsMarkOnDescendant(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Write the design document") // child of "Ship orgtd v0.1"
	m = sendKey(m, "m")
	m = sendKey(m, "a")

	m.cursor = findRow(t, m, "Ship orgtd v0.1")
	m = sendKey(m, "d")
	m = sendKey(m, "d")

	if _, ok := m.marks['a']; ok {
		t.Errorf("mark 'a' still set after its parent (and the whole subtree) was deleted")
	}
}

func TestDelmarksRemovesSpecificMark(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	m = sendKey(m, "m")
	m = sendKey(m, "a")
	m.cursor = findRow(t, m, "Follow up with finance about the Q3 budget doc")
	m = sendKey(m, "m")
	m = sendKey(m, "b")

	m = sendKey(m, ":")
	m = typeKeys(m, "delmarks a")
	m, _ = sendKeyCmd(m, "enter")

	if _, ok := m.marks['a']; ok {
		t.Errorf("mark 'a' still set after :delmarks a")
	}
	if _, ok := m.marks['b']; !ok {
		t.Errorf("mark 'b' was removed by :delmarks a, want it untouched")
	}
}

func TestDelmarksBangRemovesAll(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	m = sendKey(m, "m")
	m = sendKey(m, "a")
	m.cursor = findRow(t, m, "Follow up with finance about the Q3 budget doc")
	m = sendKey(m, "m")
	m = sendKey(m, "b")

	m = sendKey(m, ":")
	m = typeKeys(m, "delmarks!")
	m, _ = sendKeyCmd(m, "enter")

	if len(m.marks) != 0 {
		t.Errorf("marks = %v, want none after :delmarks!", m.marks)
	}
}

func TestDelmarksUnsetLetterShowsMessage(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)

	m = sendKey(m, ":")
	m = typeKeys(m, "delmarks z")
	m, _ = sendKeyCmd(m, "enter")

	if m.message == "" {
		t.Errorf("expected a message for :delmarks on an unset letter")
	}
}

func TestPendingMarkCancelledByNonLetterKey(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")

	m = sendKey(m, "m")
	m = sendKey(m, "esc")

	if len(m.marks) != 0 {
		t.Errorf("marks = %v, want none (esc should not set a mark)", m.marks)
	}
	// Normal keys still work afterward.
	m = sendKey(m, "j")
	if m.cursor != findRow(t, m, "Read the RFC linked in yesterday's design review") {
		t.Errorf("normal navigation broken after a cancelled mark chord")
	}
}

func TestJumpToMarkSwitchesToOutlineWhenNotInAgendaRows(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	// "Call the vet about Fido's checkup" has no SCHEDULED/DEADLINE, so
	// it won't appear in agenda view.
	target := findRow(t, m, "Call the vet about Fido's checkup")
	m.cursor = target
	m = sendKey(m, "m")
	m = sendKey(m, "a")

	m.switchToView(agendaView)
	m = sendKey(m, "'")
	m = sendKey(m, "a")

	if m.view != outlineView {
		t.Fatalf("view after 'a = %v, want outlineView (mark not reachable from agenda)", m.view)
	}
	if m.currentHeadline() == nil || m.currentHeadline().Title != "Call the vet about Fido's checkup" {
		t.Errorf("cursor after 'a = %v, want the marked headline", m.currentHeadline())
	}
}

func TestYankThenPasteDoesNotResetPendingMark(t *testing.T) {
	// Regression-style sanity check: setting a mark, then using
	// unrelated commands, doesn't leave pendingM/pendingQuote stuck.
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	m = sendKey(m, "m")
	m = sendKey(m, "a")
	if m.pendingM {
		t.Errorf("pendingM still set after completing the mark chord")
	}

	m = sendKey(m, "'")
	m = sendKey(m, "a")
	if m.pendingQuote {
		t.Errorf("pendingQuote still set after completing the jump chord")
	}
}
