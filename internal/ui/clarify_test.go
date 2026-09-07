package ui

import (
	"strings"
	"testing"
)

func TestClarifyCommandPinsFirstInboxItem(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)

	m = sendKey(m, ":")
	m = typeKeys(m, "clarify")
	m, _ = sendKeyCmd(m, "enter")

	if m.view != clarifyView {
		t.Fatalf("view after :clarify = %v, want clarifyView", m.view)
	}
	if m.clarifyTarget == nil || m.clarifyTarget.Title != "Call the vet about Fido's checkup" {
		t.Fatalf("clarifyTarget = %v, want the inbox's first headline", m.clarifyTarget)
	}
}

func TestClarifyBackToOutlineViaCommand(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.enterClarifyView()

	m = sendKey(m, ":")
	m = typeKeys(m, "outline")
	m, _ = sendKeyCmd(m, "enter")

	if m.view != outlineView {
		t.Fatalf("view after :outline = %v, want outlineView", m.view)
	}
}

func TestClarifyHeaderRendersPinnedItem(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.enterClarifyView()
	m.width, m.height = 100, len(m.rows)+m.pinnedHeaderHeight()+3

	out := stripANSI(m.View())
	lines := strings.Split(out, "\n")

	if lines[0] != "Clarifying:" {
		t.Fatalf("line 0 = %q, want %q", lines[0], "Clarifying:")
	}
	if !strings.Contains(lines[1], "Call the vet about Fido's checkup") {
		t.Errorf("line 1 = %q, want the pinned item's title", lines[1])
	}
	if lines[2] != "" {
		t.Errorf("line 2 = %q, want a blank separator after the pinned block", lines[2])
	}
}

func TestClarifyMarksTheRealRowInTheListing(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.enterClarifyView()

	target := m.clarifyTarget
	idx := findRow(t, m, target.Title)

	marked := stripANSI(m.renderRow(m.rows[idx]))
	other := stripANSI(m.renderRow(m.rows[idx+1])) // the next inbox item, not the clarify target

	if !strings.Contains(marked, "●") {
		t.Errorf("clarify target's row = %q, want the ● marker", marked)
	}
	if strings.Contains(other, "●") {
		t.Errorf("a different row = %q, should not carry the ● marker", other)
	}
}

func TestClarifyMarkerAbsentInOutlineView(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	idx := findRow(t, m, "Call the vet about Fido's checkup")

	line := stripANSI(m.renderRow(m.rows[idx]))
	if strings.Contains(line, "●") {
		t.Errorf("outline view row = %q, should never show the clarify marker", line)
	}
}

func TestClarifyEmptyInboxShowsMessage(t *testing.T) {
	ws := agendaFixture(t, "* TODO Not in the inbox\n") // agenda.org, not inbox.org
	m := New(ws)
	m.enterClarifyView()
	m.width, m.height = 100, len(m.rows)+m.pinnedHeaderHeight()+3

	if m.clarifyTarget != nil {
		t.Fatalf("clarifyTarget = %v, want nil (no inbox.org loaded)", m.clarifyTarget)
	}
	out := stripANSI(m.View())
	if !strings.Contains(out, "Inbox is empty.") {
		t.Errorf("View() = %q, want the empty-inbox message", out)
	}
}

func TestClarifyDeletingTargetAdvancesToNextInboxItem(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.enterClarifyView()
	first := m.clarifyTarget

	m = sendKey(m, "g")
	m = sendKey(m, "c") // jump to the real row, so dd operates on it
	m = sendKey(m, "d")
	m = sendKey(m, "d")

	if m.clarifyTarget == first {
		t.Fatalf("clarifyTarget unchanged after deleting it")
	}
	if m.clarifyTarget == nil || m.clarifyTarget.Title != "Read the RFC linked in yesterday's design review" {
		t.Errorf("clarifyTarget after delete = %v, want the inbox's new first item", m.clarifyTarget)
	}
}

func TestClarifyDeletingLastInboxItemLeavesEmptyTarget(t *testing.T) {
	// A synthetic single-item inbox (using WithInboxFile so our
	// in-memory "agenda.org" fixture stands in for the inbox).
	ws := agendaFixture(t, "* TODO Only item\n")
	m := New(ws, WithInboxFile("agenda.org"))
	m.enterClarifyView()
	if m.clarifyTarget == nil || m.clarifyTarget.Title != "Only item" {
		t.Fatalf("fixture assumption broken: clarifyTarget = %v", m.clarifyTarget)
	}

	m = sendKey(m, "g")
	m = sendKey(m, "c")
	m = sendKey(m, "d")
	m = sendKey(m, "d")

	if m.clarifyTarget != nil {
		t.Errorf("clarifyTarget = %v, want nil (inbox now empty)", m.clarifyTarget)
	}
}

func TestJumpToClarifyTargetMovesCursorToRealRow(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.enterClarifyView()
	m.cursor = findFileRow(t, m, "projects.org") // navigate away

	m = sendKey(m, "g")
	m = sendKey(m, "c")

	if m.currentHeadline() != m.clarifyTarget {
		t.Errorf("cursor after gc = %v, want the clarify target %v", m.currentHeadline(), m.clarifyTarget)
	}
}

func TestJumpToClarifyTargetNoopOutsideClarifyView(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	before := m.cursor

	m = sendKey(m, "g")
	m = sendKey(m, "c")

	if m.cursor != before {
		t.Errorf("gc outside clarify view moved the cursor to %d, want unchanged %d", m.cursor, before)
	}
}

func TestClarifyEditingWorksNormallyOnTheRealRow(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.enterClarifyView()
	h := m.clarifyTarget
	origKeyword := h.Keyword

	m = sendKey(m, "g")
	m = sendKey(m, "c")
	m = sendKey(m, "r")

	if h.Keyword == origKeyword {
		t.Errorf("status rotate in clarify view did not change the keyword")
	}
}

func TestWithInboxFileOption(t *testing.T) {
	ws := agendaFixture(t, "* TODO Custom inbox item\n")
	m := New(ws, WithInboxFile("agenda.org"))
	m.enterClarifyView()

	if m.clarifyTarget == nil || m.clarifyTarget.Title != "Custom inbox item" {
		t.Errorf("clarifyTarget = %v, want the item from the custom inbox file", m.clarifyTarget)
	}
}

func TestClarifyPageHeightAccountsForPinnedHeader(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.enterClarifyView()
	m.width, m.height = 100, len(m.rows)+m.pinnedHeaderHeight()+10

	out := m.View()
	lines := strings.Split(out, "\n")
	if len(lines) != m.height {
		t.Fatalf("got %d lines, want %d (height)\n---\n%s", len(lines), m.height, stripANSI(out))
	}
}
