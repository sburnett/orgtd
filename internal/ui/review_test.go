package ui

import (
	"strings"
	"testing"
)

func TestReviewCommandPinsFirstInboxItem(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)

	m = sendKey(m, ":")
	m = typeKeys(m, "review")
	m, _ = sendKeyCmd(m, "enter")

	if m.view != reviewView {
		t.Fatalf("view after :review = %v, want reviewView", m.view)
	}
	if m.reviewTarget == nil || m.reviewTarget.Title != "Call the vet about Fido's checkup" {
		t.Fatalf("reviewTarget = %v, want the inbox's first headline", m.reviewTarget)
	}
}

func TestReviewBackToOutlineViaCommand(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.enterReviewView()

	m = sendKey(m, ":")
	m = typeKeys(m, "outline")
	m, _ = sendKeyCmd(m, "enter")

	if m.view != outlineView {
		t.Fatalf("view after :outline = %v, want outlineView", m.view)
	}
}

func TestReviewHeaderRendersPinnedItem(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.enterReviewView()

	lines := stripANSILines(m.infoBufferLines())

	if strings.TrimRight(lines[0], " ") != "Registers:" {
		t.Fatalf("line 0 = %q, want %q (plus trailing background padding)", lines[0], "Registers:")
	}
	if !strings.HasPrefix(lines[1], "%") || !strings.Contains(lines[1], "Call the vet about Fido's checkup") {
		t.Errorf("line 1 = %q, want the pinned item's title on the %% register's row", lines[1])
	}
	if strings.TrimRight(lines[2], " ") != "" {
		t.Errorf("line 2 = %q, want a blank (background-padded) separator after the pinned block", lines[2])
	}
}

func TestReviewHeaderShowsCreatedProperty(t *testing.T) {
	ws := agendaFixture(t, "* TODO New idea\n  :PROPERTIES:\n  :CREATED: [2026-09-07 Mon 14:32]\n  :END:\n")
	m := New(ws, WithInboxFile("agenda.org"))
	m.enterReviewView()

	lines := stripANSILines(m.infoBufferLines())
	if !strings.Contains(lines[1], "Created: [2026-09-07 Mon 14:32]") {
		t.Errorf("line 1 = %q, want the CREATED property shown", lines[1])
	}
}

func TestReviewHeaderOmitsCreatedLabelWhenPropertyAbsent(t *testing.T) {
	ws := agendaFixture(t, "* TODO No created property\n")
	m := New(ws, WithInboxFile("agenda.org"))
	m.enterReviewView()

	lines := stripANSILines(m.infoBufferLines())
	if strings.Contains(lines[1], "Created:") {
		t.Errorf("line 1 = %q, should not mention Created when the property is absent", lines[1])
	}
}

func TestReviewHeaderShowsDeadline(t *testing.T) {
	ws := agendaFixture(t, "* TODO Follow up\n  DEADLINE: <2026-09-20 Sun>\n")
	m := New(ws, WithInboxFile("agenda.org"))
	m.enterReviewView()

	lines := stripANSILines(m.infoBufferLines())
	if !strings.Contains(lines[1], "DEADLINE: <2026-09-20 Sun>") {
		t.Errorf("line 1 = %q, want the DEADLINE shown", lines[1])
	}
}

func TestReviewHeaderShowsScheduled(t *testing.T) {
	ws := agendaFixture(t, "* TODO Follow up\n  SCHEDULED: <2026-09-10 Thu>\n")
	m := New(ws, WithInboxFile("agenda.org"))
	m.enterReviewView()

	lines := stripANSILines(m.infoBufferLines())
	if !strings.Contains(lines[1], "SCHEDULED: <2026-09-10 Thu>") {
		t.Errorf("line 1 = %q, want the SCHEDULED date shown", lines[1])
	}
}

func TestReviewHeaderShowsCreatedAndDeadlineTogether(t *testing.T) {
	ws := agendaFixture(t, "* TODO Follow up\n  DEADLINE: <2026-09-20 Sun>\n  :PROPERTIES:\n  :CREATED: [2026-09-07 Mon 14:32]\n  :END:\n")
	m := New(ws, WithInboxFile("agenda.org"))
	m.enterReviewView()

	lines := stripANSILines(m.infoBufferLines())
	if !strings.Contains(lines[1], "Created: [2026-09-07 Mon 14:32]") || !strings.Contains(lines[1], "DEADLINE: <2026-09-20 Sun>") {
		t.Errorf("line 1 = %q, want both CREATED and DEADLINE shown", lines[1])
	}
}

func TestReviewHeaderOmitsDateWhenAbsent(t *testing.T) {
	ws := agendaFixture(t, "* TODO No date at all\n")
	m := New(ws, WithInboxFile("agenda.org"))
	m.enterReviewView()

	lines := stripANSILines(m.infoBufferLines())
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

	lines := m.infoBufferLines()
	markLine := stripANSI(lines[1]) // 0: "Active marks:" label, 1: the "a" mark row
	if strings.Contains(markLine, "DEADLINE:") {
		t.Errorf("marks pinned row = %q, should not show DEADLINE", markLine)
	}
}

func TestMarkedRowDoesNotShowCreatedProperty(t *testing.T) {
	// CREATED is shown for the review target (useful triage context),
	// but not for marks, which can point at any headline in the outline
	// and aren't about triage — see renderPinnedRow's showCreated param.
	ws := agendaFixture(t, "* TODO Has created\n  :PROPERTIES:\n  :CREATED: [2026-09-07 Mon 14:32]\n  :END:\n")
	m := New(ws)
	m.cursor = 1 // the headline row, after the file row
	m.width, m.height = 100, len(m.rows)+m.infoBufferHeight()+3

	m = sendKey(m, "m")
	m = sendKey(m, "a")

	out := stripANSI(m.View())
	if strings.Contains(out, "Created:") {
		t.Errorf("marks pinned row should not show CREATED: %q", out)
	}
}

func TestReviewSeparatorLineCarriesOverlayBackground(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.enterReviewView()
	m.width = 100

	lines := m.infoBufferLines()
	separator := lines[len(lines)-1]
	if strings.TrimSpace(stripANSI(separator)) != "" {
		t.Fatalf("separator = %q, want blank text content", separator)
	}
	if !strings.Contains(separator, "\x1b[") {
		t.Errorf("separator = %q, want it styled with the overlay background, not plain text", separator)
	}
}

func TestReviewMarksTheRealRowInTheListing(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.enterReviewView()

	target := m.reviewTarget
	idx := findRow(t, m, target.Title)

	marked := stripANSI(m.renderRow(m.rows[idx]))
	other := stripANSI(m.renderRow(m.rows[idx+1])) // the next inbox item, not the review target

	if !strings.Contains(marked, "●") {
		t.Errorf("review target's row = %q, want the ● marker", marked)
	}
	if strings.Contains(other, "●") {
		t.Errorf("a different row = %q, should not carry the ● marker", other)
	}
}

func TestReviewMarkerAbsentInOutlineView(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	idx := findRow(t, m, "Call the vet about Fido's checkup")

	line := stripANSI(m.renderRow(m.rows[idx]))
	if strings.Contains(line, "●") {
		t.Errorf("outline view row = %q, should never show the review marker", line)
	}
}

func TestReviewEmptyInboxShowsMessage(t *testing.T) {
	ws := agendaFixture(t, "* TODO Not in the inbox\n") // agenda.org, not inbox.org
	m := New(ws)
	m.enterReviewView()
	m.width, m.height = 100, len(m.rows)+m.infoBufferHeight()+3

	if m.reviewTarget != nil {
		t.Fatalf("reviewTarget = %v, want nil (no inbox.org loaded)", m.reviewTarget)
	}
	if got := m.registerContents('%'); got != nil {
		t.Errorf("register %% = %v, want empty with nothing to review", got)
	}
	if out := stripANSI(m.View()); strings.Contains(out, "Registers:") {
		t.Errorf("View() = %q, want no registers section", out)
	}
}

func TestPercentRegisterHoldsReviewTargetOnlyInReviewView(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	if got := m.registerContents('%'); got != nil {
		t.Errorf("register %% in outline view = %v, want empty", got)
	}
	m.enterReviewView()
	if got := m.registerContents('%'); len(got) != 1 || got[0] != m.reviewTarget {
		t.Errorf("register %% in review view = %v, want the review target", got)
	}
}

func TestPercentRegisterPasteAfterAndBefore(t *testing.T) {
	for _, key := range []string{"p", "P"} {
		ws := loadFixture(t)
		m := New(ws)
		m.enterReviewView()
		title := m.reviewTarget.Title
		count := func() (n int) {
			for _, r := range m.rows {
				if r.kind == rowHeadline && r.headline.Title == title {
					n++
				}
			}
			return n
		}
		before := count()
		m.cursor = findRow(t, m, "Read the RFC linked in yesterday's design review")

		m = sendKey(m, "\"")
		m = sendKey(m, "%")
		m = sendKey(m, key)

		if got := count(); got != before+1 {
			t.Errorf("\"%%%s: %q appears %d times, want %d", key, title, got, before+1)
		}
		if m.register != nil {
			t.Errorf("\"%%%s changed the unnamed register: %v", key, m.register)
		}
		if m.pendingRegister != 0 {
			t.Errorf("pendingRegister = %q after paste, want cleared", m.pendingRegister)
		}
	}
}

func TestPercentRegisterIsReadOnly(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.enterReviewView()
	n := len(m.rows)

	m = sendKey(m, "\"")
	m = sendKey(m, "%")
	m = sendKey(m, "d")
	m = sendKey(m, "d")

	if len(m.rows) != n || m.register != nil {
		t.Errorf("\"%%dd deleted or filled the register (rows %d→%d, register %v)", n, len(m.rows), m.register)
	}
	if !strings.Contains(m.message, "read-only") {
		t.Errorf("message = %q, want a read-only refusal", m.message)
	}
}

func TestRegisterPrefixOnlyAppliesToNextCommand(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.enterReviewView()
	m.register = nil

	m = sendKey(m, "\"")
	m = sendKey(m, "%")
	m = sendKey(m, "j")
	m = sendKey(m, "p")

	if m.message != "Nothing to paste" {
		t.Errorf("message = %q, want bare p to use the (empty) unnamed register", m.message)
	}
}

func TestUnknownRegisterIsRejected(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m = sendKey(m, "\"")
	m = sendKey(m, "q")
	if !strings.Contains(m.message, "Unknown register") || m.pendingRegister != 0 {
		t.Errorf("message = %q, pendingRegister = %q, want rejection", m.message, m.pendingRegister)
	}
}

func TestReviewDeletingTargetAdvancesToNextInboxItem(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.enterReviewView()
	first := m.reviewTarget

	m = sendKey(m, "g")
	m = sendKey(m, "c") // jump to the real row, so dd operates on it
	m = sendKey(m, "d")
	m = sendKey(m, "d")

	if m.reviewTarget == first {
		t.Fatalf("reviewTarget unchanged after deleting it")
	}
	if m.reviewTarget == nil || m.reviewTarget.Title != "Read the RFC linked in yesterday's design review" {
		t.Errorf("reviewTarget after delete = %v, want the inbox's new first item", m.reviewTarget)
	}
}

func TestReviewDeletingLastInboxItemLeavesEmptyTarget(t *testing.T) {
	// A synthetic single-item inbox (using WithInboxFile so our
	// in-memory "agenda.org" fixture stands in for the inbox).
	ws := agendaFixture(t, "* TODO Only item\n")
	m := New(ws, WithInboxFile("agenda.org"))
	m.enterReviewView()
	if m.reviewTarget == nil || m.reviewTarget.Title != "Only item" {
		t.Fatalf("fixture assumption broken: reviewTarget = %v", m.reviewTarget)
	}

	m = sendKey(m, "g")
	m = sendKey(m, "c")
	m = sendKey(m, "d")
	m = sendKey(m, "d")

	if m.reviewTarget != nil {
		t.Errorf("reviewTarget = %v, want nil (inbox now empty)", m.reviewTarget)
	}
}

func TestJumpToReviewTargetMovesCursorToRealRow(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.enterReviewView()
	m.cursor = findFileRow(t, m, "projects.org") // navigate away

	m = sendKey(m, "g")
	m = sendKey(m, "c")

	if m.currentHeadline() != m.reviewTarget {
		t.Errorf("cursor after gc = %v, want the review target %v", m.currentHeadline(), m.reviewTarget)
	}
}

func TestJumpToReviewTargetNoopOutsideReviewView(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	before := m.cursor

	m = sendKey(m, "g")
	m = sendKey(m, "c")

	if m.cursor != before {
		t.Errorf("gc outside review view moved the cursor to %d, want unchanged %d", m.cursor, before)
	}
}

func TestReviewEditingWorksNormallyOnTheRealRow(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.enterReviewView()
	h := m.reviewTarget
	origKeyword := h.Keyword

	m = sendKey(m, "g")
	m = sendKey(m, "c")
	m = setStatus(m, "n") // TODO -> NEXT

	if h.Keyword == origKeyword {
		t.Errorf("status change in review view did not change the keyword")
	}
}

func TestReviewTargetSurvivesEditingIt(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.enterReviewView()
	old := m.reviewTarget

	m = sendKey(m, "g")
	m = sendKey(m, "c")
	path := writeTempOrgFile(t, "NEXT Call the vet about Fido's checkup ASAP\n")
	updated, _ := m.Update(editFinishedMsg{path: path, target: old})
	m = updated.(Model)

	if m.reviewTarget == old {
		t.Fatalf("fixture assumption broken: expected the headline pointer to change")
	}
	if m.reviewTarget == nil || m.reviewTarget.Title != "Call the vet about Fido's checkup ASAP" {
		t.Errorf("reviewTarget after editing it = %v, want the edited headline", m.reviewTarget)
	}
}

func TestReviewEnterSkipsLeadingDoneAndCancelledItems(t *testing.T) {
	ws := agendaFixture(t, "* DONE Old resolved\n* CANCELLED Also resolved\n* TODO Real work\n* TODO More work\n")
	m := New(ws, WithInboxFile("agenda.org"))
	m.enterReviewView()

	if m.reviewTarget == nil || m.reviewTarget.Title != "Real work" {
		t.Fatalf("reviewTarget = %v, want the first non-done item", m.reviewTarget)
	}
}

func TestReviewAllItemsDoneLeavesNilTarget(t *testing.T) {
	ws := agendaFixture(t, "* DONE One\n* CANCELLED Two\n")
	m := New(ws, WithInboxFile("agenda.org"))
	m.enterReviewView()

	if m.reviewTarget != nil {
		t.Errorf("reviewTarget = %v, want nil (every inbox item is done)", m.reviewTarget)
	}
}

func TestReviewMarkingTargetDoneAdvancesToNextPendingItem(t *testing.T) {
	ws := agendaFixture(t, "* TODO First\n* TODO Second\n")
	m := New(ws, WithInboxFile("agenda.org"))
	m.enterReviewView()
	first := m.reviewTarget

	m = sendKey(m, "g")
	m = sendKey(m, "c") // jump to the real row, so R operates on it
	m = sendKey(m, "R")
	m = sendKey(m, "d") // "d" uniquely filters to DONE and auto-applies

	if m.reviewTarget == first {
		t.Fatalf("reviewTarget unchanged after marking it done")
	}
	if m.reviewTarget == nil || m.reviewTarget.Title != "Second" {
		t.Errorf("reviewTarget after marking First done = %v, want Second", m.reviewTarget)
	}
}

func TestReviewMarkingTargetDoneWithNoOtherPendingItemsLeavesNilTarget(t *testing.T) {
	ws := agendaFixture(t, "* TODO Only item\n")
	m := New(ws, WithInboxFile("agenda.org"))
	m.enterReviewView()

	m = sendKey(m, "g")
	m = sendKey(m, "c")
	m = sendKey(m, "R")
	m = sendKey(m, "d")

	if m.reviewTarget != nil {
		t.Errorf("reviewTarget = %v, want nil (the only item is now done)", m.reviewTarget)
	}
}

func TestReviewBulkStatusChangeAdvancesPastTarget(t *testing.T) {
	// A count-prefixed R (2R) marks First and Second done in one bulk
	// step — the review target (First) should skip past both, landing
	// on Third, exactly as if they'd been marked done individually.
	ws := agendaFixture(t, "* TODO First\n* TODO Second\n* TODO Third\n")
	m := New(ws, WithInboxFile("agenda.org"))
	m.enterReviewView()

	m = sendKey(m, "g")
	m = sendKey(m, "c")
	m = sendKey(m, "2")
	m = sendKey(m, "R")
	m = sendKey(m, "d")

	if m.reviewTarget == nil || m.reviewTarget.Title != "Third" {
		t.Errorf("reviewTarget after bulk-marking First and Second done = %v, want Third", m.reviewTarget)
	}
}

func TestReviewNextCommandSkipsDoneItems(t *testing.T) {
	ws := agendaFixture(t, "* TODO First\n* DONE Skipped\n* TODO Third\n")
	m := New(ws, WithInboxFile("agenda.org"))
	m.enterReviewView()
	if m.reviewTarget.Title != "First" {
		t.Fatalf("fixture assumption broken: reviewTarget = %v", m.reviewTarget)
	}

	m = sendKey(m, ":")
	m = typeKeys(m, "next")
	m, _ = sendKeyCmd(m, "enter")

	if m.reviewTarget == nil || m.reviewTarget.Title != "Third" {
		t.Errorf("reviewTarget after :next = %v, want Third (Skipped is DONE)", m.reviewTarget)
	}
}

func TestReviewPrevCommandSkipsDoneItems(t *testing.T) {
	ws := agendaFixture(t, "* TODO First\n* CANCELLED Skipped\n* TODO Third\n")
	m := New(ws, WithInboxFile("agenda.org"))
	m.enterReviewView()
	// Manually advance to Third first, then step back with :prev.
	f := m.findInboxFile()
	m.reviewTarget = f.Headlines[2]

	m = sendKey(m, ":")
	m = typeKeys(m, "prev")
	m, _ = sendKeyCmd(m, "enter")

	if m.reviewTarget == nil || m.reviewTarget.Title != "First" {
		t.Errorf("reviewTarget after :prev = %v, want First (Skipped is CANCELLED)", m.reviewTarget)
	}
}

func TestReviewNextCommandAtLastItemShowsMessage(t *testing.T) {
	ws := agendaFixture(t, "* TODO First\n* TODO Second\n")
	m := New(ws, WithInboxFile("agenda.org"))
	m.enterReviewView()
	f := m.findInboxFile()
	m.reviewTarget = f.Headlines[1] // already the last item

	m = sendKey(m, ":")
	m = typeKeys(m, "next")
	m, _ = sendKeyCmd(m, "enter")

	if m.reviewTarget.Title != "Second" {
		t.Errorf("reviewTarget after :next at the last item = %v, want unchanged Second", m.reviewTarget)
	}
	if m.message == "" {
		t.Error("expected a status message explaining there's nowhere further to go")
	}
}

func TestReviewPrevCommandAtFirstItemShowsMessage(t *testing.T) {
	ws := agendaFixture(t, "* TODO First\n* TODO Second\n")
	m := New(ws, WithInboxFile("agenda.org"))
	m.enterReviewView()

	m = sendKey(m, ":")
	m = typeKeys(m, "prev")
	m, _ = sendKeyCmd(m, "enter")

	if m.reviewTarget.Title != "First" {
		t.Errorf("reviewTarget after :prev at the first item = %v, want unchanged First", m.reviewTarget)
	}
	if m.message == "" {
		t.Error("expected a status message explaining there's nowhere further to go")
	}
}

func TestNextAndPrevCommandsNoopOutsideReviewView(t *testing.T) {
	ws := agendaFixture(t, "* TODO First\n* TODO Second\n")
	m := New(ws, WithInboxFile("agenda.org"))
	// Not entering review view — plain outline view.

	m = sendKey(m, ":")
	m = typeKeys(m, "next")
	m, _ = sendKeyCmd(m, "enter")

	if m.view != outlineView {
		t.Errorf("view after :next outside review = %v, want unchanged outlineView", m.view)
	}
	if m.message == "" {
		t.Error("expected a message explaining :next only works in review view")
	}
}

func TestWithInboxFileOption(t *testing.T) {
	ws := agendaFixture(t, "* TODO Custom inbox item\n")
	m := New(ws, WithInboxFile("agenda.org"))
	m.enterReviewView()

	if m.reviewTarget == nil || m.reviewTarget.Title != "Custom inbox item" {
		t.Errorf("reviewTarget = %v, want the item from the custom inbox file", m.reviewTarget)
	}
}

func TestReviewPageHeightAccountsForInfoBuffer(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.enterReviewView()
	m.width, m.height = 100, len(m.rows)+m.infoBufferHeight()+10

	out := m.View()
	lines := strings.Split(out, "\n")
	if len(lines) != m.height {
		t.Fatalf("got %d lines, want %d (height)\n---\n%s", len(lines), m.height, stripANSI(out))
	}
}
