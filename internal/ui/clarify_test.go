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

	if strings.TrimRight(lines[0], " ") != "Clarifying:" {
		t.Fatalf("line 0 = %q, want %q (plus trailing background padding)", lines[0], "Clarifying:")
	}
	if !strings.Contains(lines[1], "Call the vet about Fido's checkup") {
		t.Errorf("line 1 = %q, want the pinned item's title", lines[1])
	}
	if strings.TrimRight(lines[2], " ") != "" {
		t.Errorf("line 2 = %q, want a blank (background-padded) separator after the pinned block", lines[2])
	}
}

func TestClarifyHeaderShowsCreatedProperty(t *testing.T) {
	ws := agendaFixture(t, "* TODO New idea\n  :PROPERTIES:\n  :CREATED: [2026-09-07 Mon 14:32]\n  :END:\n")
	m := New(ws, WithInboxFile("agenda.org"))
	m.enterClarifyView()
	m.width, m.height = 100, len(m.rows)+m.pinnedHeaderHeight()+3

	out := stripANSI(m.View())
	lines := strings.Split(out, "\n")
	if !strings.Contains(lines[1], "Created: [2026-09-07 Mon 14:32]") {
		t.Errorf("line 1 = %q, want the CREATED property shown", lines[1])
	}
}

func TestClarifyHeaderOmitsCreatedLabelWhenPropertyAbsent(t *testing.T) {
	ws := agendaFixture(t, "* TODO No created property\n")
	m := New(ws, WithInboxFile("agenda.org"))
	m.enterClarifyView()
	m.width, m.height = 100, len(m.rows)+m.pinnedHeaderHeight()+3

	out := stripANSI(m.View())
	lines := strings.Split(out, "\n")
	if strings.Contains(lines[1], "Created:") {
		t.Errorf("line 1 = %q, should not mention Created when the property is absent", lines[1])
	}
}

func TestClarifyHeaderShowsDeadline(t *testing.T) {
	ws := agendaFixture(t, "* TODO Follow up\n  DEADLINE: <2026-09-20 Sun>\n")
	m := New(ws, WithInboxFile("agenda.org"))
	m.enterClarifyView()
	m.width, m.height = 100, len(m.rows)+m.pinnedHeaderHeight()+3

	out := stripANSI(m.View())
	lines := strings.Split(out, "\n")
	if !strings.Contains(lines[1], "DEADLINE: <2026-09-20 Sun>") {
		t.Errorf("line 1 = %q, want the DEADLINE shown", lines[1])
	}
}

func TestClarifyHeaderShowsScheduled(t *testing.T) {
	ws := agendaFixture(t, "* TODO Follow up\n  SCHEDULED: <2026-09-10 Thu>\n")
	m := New(ws, WithInboxFile("agenda.org"))
	m.enterClarifyView()
	m.width, m.height = 100, len(m.rows)+m.pinnedHeaderHeight()+3

	out := stripANSI(m.View())
	lines := strings.Split(out, "\n")
	if !strings.Contains(lines[1], "SCHEDULED: <2026-09-10 Thu>") {
		t.Errorf("line 1 = %q, want the SCHEDULED date shown", lines[1])
	}
}

func TestClarifyHeaderShowsCreatedAndDeadlineTogether(t *testing.T) {
	ws := agendaFixture(t, "* TODO Follow up\n  DEADLINE: <2026-09-20 Sun>\n  :PROPERTIES:\n  :CREATED: [2026-09-07 Mon 14:32]\n  :END:\n")
	m := New(ws, WithInboxFile("agenda.org"))
	m.enterClarifyView()
	m.width, m.height = 100, len(m.rows)+m.pinnedHeaderHeight()+3

	out := stripANSI(m.View())
	lines := strings.Split(out, "\n")
	if !strings.Contains(lines[1], "Created: [2026-09-07 Mon 14:32]") || !strings.Contains(lines[1], "DEADLINE: <2026-09-20 Sun>") {
		t.Errorf("line 1 = %q, want both CREATED and DEADLINE shown", lines[1])
	}
}

func TestClarifyHeaderOmitsDateWhenAbsent(t *testing.T) {
	ws := agendaFixture(t, "* TODO No date at all\n")
	m := New(ws, WithInboxFile("agenda.org"))
	m.enterClarifyView()
	m.width, m.height = 100, len(m.rows)+m.pinnedHeaderHeight()+3

	out := stripANSI(m.View())
	lines := strings.Split(out, "\n")
	for _, want := range []string{"SCHEDULED:", "DEADLINE:", "CLOSED:"} {
		if strings.Contains(lines[1], want) {
			t.Errorf("line 1 = %q, should not mention %q when absent", lines[1], want)
		}
	}
}

func TestMarkedRowDoesNotShowDeadline(t *testing.T) {
	// Check the pinned mark line specifically (not the whole View()
	// output): the headline's own real outline row legitimately shows
	// its DEADLINE too (via the same planningSummary the outline always
	// uses), so asserting over the whole screen wouldn't isolate what
	// the pinned *mark* row itself renders.
	ws := agendaFixture(t, "* TODO Follow up\n  DEADLINE: <2026-09-20 Sun>\n")
	m := New(ws)
	m.cursor = 1 // the headline row, after the file row

	m = sendKey(m, "m")
	m = sendKey(m, "a")

	lines := m.pinnedHeaderLines()
	markLine := stripANSI(lines[1]) // 0: "Active marks:" label, 1: the "a" mark row
	if strings.Contains(markLine, "DEADLINE:") {
		t.Errorf("marks pinned row = %q, should not show DEADLINE", markLine)
	}
}

func TestMarkedRowDoesNotShowCreatedProperty(t *testing.T) {
	// CREATED is shown for the clarify target (useful triage context),
	// but not for marks, which can point at any headline in the outline
	// and aren't about triage — see renderPinnedRow's showCreated param.
	ws := agendaFixture(t, "* TODO Has created\n  :PROPERTIES:\n  :CREATED: [2026-09-07 Mon 14:32]\n  :END:\n")
	m := New(ws)
	m.cursor = 1 // the headline row, after the file row
	m.width, m.height = 100, len(m.rows)+m.pinnedHeaderHeight()+3

	m = sendKey(m, "m")
	m = sendKey(m, "a")

	out := stripANSI(m.View())
	if strings.Contains(out, "Created:") {
		t.Errorf("marks pinned row should not show CREATED: %q", out)
	}
}

func TestClarifySeparatorLineCarriesOverlayBackground(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.enterClarifyView()
	m.width, m.height = 100, len(m.rows)+m.pinnedHeaderHeight()+3

	lines := m.pinnedHeaderLines()
	separator := lines[len(lines)-1]
	if strings.TrimSpace(stripANSI(separator)) != "" {
		t.Fatalf("separator = %q, want blank text content", separator)
	}
	if !strings.Contains(separator, "\x1b[") {
		t.Errorf("separator = %q, want it styled with the overlay background, not plain text", separator)
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

func TestClarifyTargetSurvivesEditingIt(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.enterClarifyView()
	old := m.clarifyTarget

	m = sendKey(m, "g")
	m = sendKey(m, "c")
	path := writeTempOrgFile(t, "* NEXT Call the vet about Fido's checkup ASAP\n")
	updated, _ := m.Update(editFinishedMsg{path: path, target: old})
	m = updated.(Model)

	if m.clarifyTarget == old {
		t.Fatalf("fixture assumption broken: expected the headline pointer to change")
	}
	if m.clarifyTarget == nil || m.clarifyTarget.Title != "Call the vet about Fido's checkup ASAP" {
		t.Errorf("clarifyTarget after editing it = %v, want the edited headline", m.clarifyTarget)
	}
}

func TestClarifyEnterSkipsLeadingDoneAndCancelledItems(t *testing.T) {
	ws := agendaFixture(t, "* DONE Old resolved\n* CANCELLED Also resolved\n* TODO Real work\n* TODO More work\n")
	m := New(ws, WithInboxFile("agenda.org"))
	m.enterClarifyView()

	if m.clarifyTarget == nil || m.clarifyTarget.Title != "Real work" {
		t.Fatalf("clarifyTarget = %v, want the first non-done item", m.clarifyTarget)
	}
}

func TestClarifyAllItemsDoneLeavesNilTarget(t *testing.T) {
	ws := agendaFixture(t, "* DONE One\n* CANCELLED Two\n")
	m := New(ws, WithInboxFile("agenda.org"))
	m.enterClarifyView()

	if m.clarifyTarget != nil {
		t.Errorf("clarifyTarget = %v, want nil (every inbox item is done)", m.clarifyTarget)
	}
}

func TestClarifyMarkingTargetDoneAdvancesToNextPendingItem(t *testing.T) {
	ws := agendaFixture(t, "* TODO First\n* TODO Second\n")
	m := New(ws, WithInboxFile("agenda.org"))
	m.enterClarifyView()
	first := m.clarifyTarget

	m = sendKey(m, "g")
	m = sendKey(m, "c") // jump to the real row, so R operates on it
	m = sendKey(m, "R")
	m = sendKey(m, "d") // "d" uniquely filters to DONE and auto-applies

	if m.clarifyTarget == first {
		t.Fatalf("clarifyTarget unchanged after marking it done")
	}
	if m.clarifyTarget == nil || m.clarifyTarget.Title != "Second" {
		t.Errorf("clarifyTarget after marking First done = %v, want Second", m.clarifyTarget)
	}
}

func TestClarifyMarkingTargetDoneWithNoOtherPendingItemsLeavesNilTarget(t *testing.T) {
	ws := agendaFixture(t, "* TODO Only item\n")
	m := New(ws, WithInboxFile("agenda.org"))
	m.enterClarifyView()

	m = sendKey(m, "g")
	m = sendKey(m, "c")
	m = sendKey(m, "R")
	m = sendKey(m, "d")

	if m.clarifyTarget != nil {
		t.Errorf("clarifyTarget = %v, want nil (the only item is now done)", m.clarifyTarget)
	}
}

func TestClarifyBulkStatusChangeAdvancesPastTarget(t *testing.T) {
	// A count-prefixed R (2R) marks First and Second done in one bulk
	// step — the clarify target (First) should skip past both, landing
	// on Third, exactly as if they'd been marked done individually.
	ws := agendaFixture(t, "* TODO First\n* TODO Second\n* TODO Third\n")
	m := New(ws, WithInboxFile("agenda.org"))
	m.enterClarifyView()

	m = sendKey(m, "g")
	m = sendKey(m, "c")
	m = sendKey(m, "2")
	m = sendKey(m, "R")
	m = sendKey(m, "d")

	if m.clarifyTarget == nil || m.clarifyTarget.Title != "Third" {
		t.Errorf("clarifyTarget after bulk-marking First and Second done = %v, want Third", m.clarifyTarget)
	}
}

func TestClarifyNextCommandSkipsDoneItems(t *testing.T) {
	ws := agendaFixture(t, "* TODO First\n* DONE Skipped\n* TODO Third\n")
	m := New(ws, WithInboxFile("agenda.org"))
	m.enterClarifyView()
	if m.clarifyTarget.Title != "First" {
		t.Fatalf("fixture assumption broken: clarifyTarget = %v", m.clarifyTarget)
	}

	m = sendKey(m, ":")
	m = typeKeys(m, "next")
	m, _ = sendKeyCmd(m, "enter")

	if m.clarifyTarget == nil || m.clarifyTarget.Title != "Third" {
		t.Errorf("clarifyTarget after :next = %v, want Third (Skipped is DONE)", m.clarifyTarget)
	}
}

func TestClarifyPrevCommandSkipsDoneItems(t *testing.T) {
	ws := agendaFixture(t, "* TODO First\n* CANCELLED Skipped\n* TODO Third\n")
	m := New(ws, WithInboxFile("agenda.org"))
	m.enterClarifyView()
	// Manually advance to Third first, then step back with :prev.
	f := m.findInboxFile()
	m.clarifyTarget = f.Headlines[2]

	m = sendKey(m, ":")
	m = typeKeys(m, "prev")
	m, _ = sendKeyCmd(m, "enter")

	if m.clarifyTarget == nil || m.clarifyTarget.Title != "First" {
		t.Errorf("clarifyTarget after :prev = %v, want First (Skipped is CANCELLED)", m.clarifyTarget)
	}
}

func TestClarifyNextCommandAtLastItemShowsMessage(t *testing.T) {
	ws := agendaFixture(t, "* TODO First\n* TODO Second\n")
	m := New(ws, WithInboxFile("agenda.org"))
	m.enterClarifyView()
	f := m.findInboxFile()
	m.clarifyTarget = f.Headlines[1] // already the last item

	m = sendKey(m, ":")
	m = typeKeys(m, "next")
	m, _ = sendKeyCmd(m, "enter")

	if m.clarifyTarget.Title != "Second" {
		t.Errorf("clarifyTarget after :next at the last item = %v, want unchanged Second", m.clarifyTarget)
	}
	if m.message == "" {
		t.Error("expected a status message explaining there's nowhere further to go")
	}
}

func TestClarifyPrevCommandAtFirstItemShowsMessage(t *testing.T) {
	ws := agendaFixture(t, "* TODO First\n* TODO Second\n")
	m := New(ws, WithInboxFile("agenda.org"))
	m.enterClarifyView()

	m = sendKey(m, ":")
	m = typeKeys(m, "prev")
	m, _ = sendKeyCmd(m, "enter")

	if m.clarifyTarget.Title != "First" {
		t.Errorf("clarifyTarget after :prev at the first item = %v, want unchanged First", m.clarifyTarget)
	}
	if m.message == "" {
		t.Error("expected a status message explaining there's nowhere further to go")
	}
}

func TestNextAndPrevCommandsNoopOutsideClarifyView(t *testing.T) {
	ws := agendaFixture(t, "* TODO First\n* TODO Second\n")
	m := New(ws, WithInboxFile("agenda.org"))
	// Not entering clarify view — plain outline view.

	m = sendKey(m, ":")
	m = typeKeys(m, "next")
	m, _ = sendKeyCmd(m, "enter")

	if m.view != outlineView {
		t.Errorf("view after :next outside clarify = %v, want unchanged outlineView", m.view)
	}
	if m.message == "" {
		t.Error("expected a message explaining :next only works in clarify view")
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
