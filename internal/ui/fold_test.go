package ui

import (
	"testing"

	"github.com/sburnett/orgtd/internal/org"
)

// rowVisible reports whether a headline with the given title currently
// appears in m.rows (i.e. isn't hidden behind a fold), without failing
// the test if it's absent.
func rowVisible(m Model, title string) bool {
	for _, r := range m.rows {
		if r.headline != nil && r.headline.Title == title {
			return true
		}
	}
	return false
}

func TestFoldCloseHidesChildren(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Ship orgtd v0.1")
	h := m.currentHeadline()
	before := len(m.rows)
	hidden := subtreeRowCount(h) - 1 // everything but h's own row

	m = sendKey(m, "z")
	m = sendKey(m, "c")

	if !m.collapsed[h] {
		t.Errorf("expected collapsed[h] = true after zc")
	}
	if len(m.rows) != before-hidden {
		t.Errorf("rows after zc = %d, want %d", len(m.rows), before-hidden)
	}
}

func TestFoldOpenRevealsChildren(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Ship orgtd v0.1")
	h := m.currentHeadline()
	before := len(m.rows)

	m = sendKey(m, "z")
	m = sendKey(m, "c")
	m = sendKey(m, "z")
	m = sendKey(m, "o")

	if m.collapsed[h] {
		t.Errorf("expected collapsed[h] = false after zc then zo")
	}
	if len(m.rows) != before {
		t.Errorf("rows after zc+zo = %d, want %d (back to original)", len(m.rows), before)
	}
}

func TestFoldOpenNoopWithoutChildren(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup") // a leaf
	before := len(m.rows)

	m = sendKey(m, "z")
	m = sendKey(m, "o")

	if len(m.rows) != before {
		t.Errorf("zo on a leaf changed row count: %d, want %d", len(m.rows), before)
	}
}

func TestFoldCloseOnLeafClosesParent(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Implement the Bubble Tea viewer") // a nested leaf
	parent := m.currentHeadline().Parent

	m = sendKey(m, "z")
	m = sendKey(m, "c")

	if parent == nil || !m.collapsed[parent] {
		t.Errorf("zc on a leaf did not close its parent")
	}
	if m.currentHeadline() != parent {
		t.Errorf("cursor did not move to the closed parent")
	}
}

func TestFoldCloseOnClosedClosesParent(t *testing.T) {
	ws := loadFixture(t)
	m, top, mid, _ := buildThreeLevelNesting(t, New(ws))
	m.cursor = findRow(t, m, mid.Title)

	m = sendKey(m, "z")
	m = sendKey(m, "c")
	if !m.collapsed[mid] || m.collapsed[top] {
		t.Fatalf("first zc: mid=%v top=%v, want only mid closed", m.collapsed[mid], m.collapsed[top])
	}
	m = sendKey(m, "z")
	m = sendKey(m, "c")
	if !m.collapsed[top] || m.currentHeadline() != top {
		t.Errorf("second zc on a closed fold did not close the parent")
	}
}

func TestFoldCloseCountWalksUp(t *testing.T) {
	ws := loadFixture(t)
	m, top, mid, _ := buildThreeLevelNesting(t, New(ws))
	m.cursor = findRow(t, m, mid.Title)

	m = sendKey(m, "2")
	m = sendKey(m, "z")
	m = sendKey(m, "c")

	if !m.collapsed[mid] || !m.collapsed[top] {
		t.Errorf("2zc: mid=%v top=%v, want both closed", m.collapsed[mid], m.collapsed[top])
	}
	if m.pendingCount != 0 {
		t.Errorf("count not consumed")
	}
}

func TestFoldOpenCountOpensNestedLevels(t *testing.T) {
	ws := loadFixture(t)
	m, top, mid, leaf := buildThreeLevelNesting(t, New(ws))
	m = sendKey(m, "z")
	m = sendKey(m, "C")

	m = sendKey(m, "z")
	m = sendKey(m, "o")
	if m.collapsed[top] || !m.collapsed[mid] || rowVisible(m, leaf.Title) {
		t.Fatalf("plain zo should open exactly one level")
	}

	m = sendKey(m, "z")
	m = sendKey(m, "C")
	m = sendKey(m, "2")
	m = sendKey(m, "z")
	m = sendKey(m, "o")
	if m.collapsed[top] || m.collapsed[mid] || !rowVisible(m, leaf.Title) {
		t.Errorf("2zo should open two levels")
	}
}

func TestFoldCountDroppedByOtherKey(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m = sendKey(m, "3")
	m = sendKey(m, "z")
	m = sendKey(m, "j")
	if m.pendingCount != 0 {
		t.Errorf("count leaked past 3zj")
	}
}

func TestFoldToggleZaMatchesTab(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Ship orgtd v0.1")
	h := m.currentHeadline()

	m = sendKey(m, "z")
	m = sendKey(m, "a")
	if !m.collapsed[h] {
		t.Fatalf("za did not collapse")
	}

	m = sendKey(m, "z")
	m = sendKey(m, "a")
	if m.collapsed[h] {
		t.Errorf("za did not re-expand")
	}
}

func TestFoldLoneZIsPendingAndCancellable(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Ship orgtd v0.1")
	h := m.currentHeadline()

	m = sendKey(m, "z")
	if m.collapsed[h] {
		t.Errorf("a lone z already folded")
	}
	if m.chord != "z" {
		t.Errorf("expected chord after a lone z")
	}

	// An unrelated key cancels the pending z, so a subsequent "o"
	// behaves as a normal insert, not fold-open.
	m = sendKey(m, "z")
	m = sendKey(m, "j") // cancels
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	_, cmd := sendKeyCmd(m, "o")
	if cmd == nil {
		t.Errorf("expected a cancelled z to leave o as a normal insert command")
	}
}

func TestFoldNoopOnFileRow(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = 0
	if m.rows[0].file == nil {
		t.Fatalf("fixture assumption broken: row 0 is not a file row")
	}
	before := len(m.rows)

	for _, key := range []string{"o", "c", "a", "O", "C", "A"} {
		m = sendKey(m, "z")
		m = sendKey(m, key)
	}
	if len(m.rows) != before {
		t.Errorf("fold commands on a file row changed row count: %d, want %d", len(m.rows), before)
	}
}

// buildThreeLevelNesting demotes "Implement the org file parser" so it
// becomes a genuine third-level child of "Write the design document",
// giving fold tests real nested structure to work with (the fixture
// otherwise only goes two levels deep).
func buildThreeLevelNesting(t *testing.T, m Model) (m2 Model, top, mid, leaf *org.Headline) {
	t.Helper()
	m.cursor = findRow(t, m, "Implement the org file parser")
	leaf = m.currentHeadline()
	m = sendKey(m, ">")
	m = sendKey(m, ">")
	if leaf.Level != 3 {
		t.Fatalf("setup failed: level = %d, want 3", leaf.Level)
	}
	mid = leaf.Parent
	m.cursor = findRow(t, m, "Ship orgtd v0.1")
	top = m.currentHeadline()
	return m, top, mid, leaf
}

func TestFoldCloseAllRecursivelyCollapsesEveryLevel(t *testing.T) {
	ws := loadFixture(t)
	m, top, mid, leaf := buildThreeLevelNesting(t, New(ws))

	m = sendKey(m, "z")
	m = sendKey(m, "C")

	if !m.collapsed[top] {
		t.Errorf("zC did not collapse the top level")
	}
	if !m.collapsed[mid] {
		t.Errorf("zC did not collapse the middle level")
	}
	if rowVisible(m, mid.Title) {
		t.Errorf("middle entry still visible after zC")
	}
	if rowVisible(m, leaf.Title) {
		t.Errorf("leaf entry still visible after zC")
	}
}

func TestFoldOpenAllRecursivelyOpensEveryLevel(t *testing.T) {
	ws := loadFixture(t)
	m, top, mid, leaf := buildThreeLevelNesting(t, New(ws))

	m = sendKey(m, "z")
	m = sendKey(m, "C")
	m = sendKey(m, "z")
	m = sendKey(m, "O")

	if m.collapsed[top] || m.collapsed[mid] {
		t.Errorf("zO left something collapsed: top=%v mid=%v", m.collapsed[top], m.collapsed[mid])
	}
	if !rowVisible(m, mid.Title) {
		t.Errorf("middle entry not visible after zO")
	}
	if !rowVisible(m, leaf.Title) {
		t.Errorf("leaf entry not visible after zO")
	}
}

func TestFoldCloseAllThenSingleOpenStaysNested(t *testing.T) {
	ws := loadFixture(t)
	m, top, mid, leaf := buildThreeLevelNesting(t, New(ws))

	m = sendKey(m, "z")
	m = sendKey(m, "C")

	// A single-level zo on the top should reveal the middle entry, but
	// the middle entry's own fold (also closed by zC) should still hide
	// the leaf — no inconsistent half-open state.
	m.cursor = findRow(t, m, top.Title)
	m = sendKey(m, "z")
	m = sendKey(m, "o")

	if !rowVisible(m, mid.Title) {
		t.Fatalf("single-level zo did not reveal the middle entry")
	}
	if rowVisible(m, leaf.Title) {
		t.Errorf("leaf entry visible after only a single-level zo on the ancestor")
	}
	if !m.collapsed[mid] {
		t.Errorf("expected the middle entry to still be marked collapsed")
	}
}

func TestFoldToggleAllRecursive(t *testing.T) {
	ws := loadFixture(t)
	m, top, mid, leaf := buildThreeLevelNesting(t, New(ws))

	m = sendKey(m, "z")
	m = sendKey(m, "A") // currently all open -> should close everything
	if !m.collapsed[top] || !m.collapsed[mid] {
		t.Fatalf("zA did not close everything from an open state")
	}
	if rowVisible(m, leaf.Title) {
		t.Errorf("leaf visible after zA closed everything")
	}

	m = sendKey(m, "z")
	m = sendKey(m, "A") // currently closed -> should open everything
	if m.collapsed[top] || m.collapsed[mid] {
		t.Fatalf("zA did not open everything from a closed state")
	}
	if !rowVisible(m, leaf.Title) {
		t.Errorf("leaf not visible after zA opened everything")
	}
}
