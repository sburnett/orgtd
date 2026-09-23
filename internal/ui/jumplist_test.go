package ui

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/sburnett/orgtd/internal/org"
	"github.com/sburnett/orgtd/internal/workspace"
)

func TestJumpBackAfterG(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	origin := findRow(t, m, "Call the vet about Fido's checkup")
	m.cursor = origin

	m = sendKey(m, "G")
	if m.cursor == origin {
		t.Fatalf("G didn't move the cursor; can't test jumping back from it")
	}

	m = sendKey(m, "ctrl+o")
	if m.cursor != origin {
		t.Errorf("cursor after ctrl+o = %d, want %d (back to where G was pressed from)", m.cursor, origin)
	}
}

func TestJumpBackAfterGG(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	origin := findRow(t, m, "Follow up with finance about the Q3 budget doc")
	m.cursor = origin

	m = sendKey(m, "g")
	m = sendKey(m, "g")
	if m.cursor != 0 {
		t.Fatalf("gg didn't move the cursor to row 0")
	}

	m = sendKey(m, "ctrl+o")
	if m.cursor != origin {
		t.Errorf("cursor after ctrl+o = %d, want %d (back to where gg was pressed from)", m.cursor, origin)
	}
}

func TestJumpForwardAfterJumpBack(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	origin := findRow(t, m, "Call the vet about Fido's checkup")
	m.cursor = origin

	m = sendKey(m, "G")
	afterG := m.cursor

	m = sendKey(m, "ctrl+o")
	if m.cursor != origin {
		t.Fatalf("ctrl+o didn't return to origin; got %d, want %d", m.cursor, origin)
	}

	m = sendKey(m, "g")
	m = sendKey(m, "i")
	if m.cursor != afterG {
		t.Errorf("cursor after gi = %d, want %d (forward to where ctrl+o was pressed from)", m.cursor, afterG)
	}
}

func TestJumpBackNoopWhenListEmpty(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	origin := findRow(t, m, "Call the vet about Fido's checkup")
	m.cursor = origin

	m = sendKey(m, "ctrl+o")
	if m.cursor != origin {
		t.Errorf("ctrl+o with an empty jump list moved the cursor: %d, want %d", m.cursor, origin)
	}
}

func TestJumpForwardNoopAtNewestEntry(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	origin := findRow(t, m, "Call the vet about Fido's checkup")
	m.cursor = origin

	m = sendKey(m, "G")
	afterG := m.cursor
	m = sendKey(m, "ctrl+o")
	m = sendKey(m, "g")
	m = sendKey(m, "i")
	if m.cursor != afterG {
		t.Fatalf("setup: gi didn't return to %d, got %d", afterG, m.cursor)
	}

	// Already at the newest entry; another gi should be a no-op.
	m = sendKey(m, "g")
	m = sendKey(m, "i")
	if m.cursor != afterG {
		t.Errorf("extra gi at the newest entry moved the cursor: %d, want %d", m.cursor, afterG)
	}
}

func TestPushJumpDedupesRepeatedGG(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = 0 // already at the top, where gg lands

	// First gg: cursor is already at row 0, so this records row 0's own
	// position (the jump list was empty, so it's recorded regardless of
	// being a "no-op" move).
	m = sendKey(m, "g")
	m = sendKey(m, "g")
	afterFirst := len(m.jumpList)
	if afterFirst == 0 {
		t.Fatalf("gg didn't push a jump entry")
	}

	// Second gg: still at row 0, so this would record the exact same
	// position as the one just pushed — deduped instead of growing the
	// list.
	m = sendKey(m, "g")
	m = sendKey(m, "g")
	if len(m.jumpList) != afterFirst {
		t.Errorf("len(jumpList) after a no-op repeat gg = %d, want %d (deduped)", len(m.jumpList), afterFirst)
	}
}

func TestNewJumpTruncatesForwardHistory(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	origin := findRow(t, m, "Call the vet about Fido's checkup")
	m.cursor = origin

	m = sendKey(m, "G") // push origin
	m = sendKey(m, "ctrl+o")
	if m.cursor != origin {
		t.Fatalf("setup: ctrl+o didn't return to origin")
	}
	if m.jumpPos == len(m.jumpList) {
		t.Fatalf("setup: expected to be mid-history (jumpPos < len(jumpList))")
	}

	// A fresh jump from here (not gi) should discard the "forward"
	// entry ctrl+o's implicit push created — matches undo/redo being
	// discarded by a fresh edit.
	m.cursor = findRow(t, m, "Follow up with finance about the Q3 budget doc")
	m = sendKey(m, "G")
	if m.jumpPos != len(m.jumpList) {
		t.Errorf("jumpPos = %d, len(jumpList) = %d, want equal (live again after a fresh jump)", m.jumpPos, len(m.jumpList))
	}

	m = sendKey(m, "g")
	m = sendKey(m, "i")
	if m.cursor == origin {
		t.Errorf("gi followed old forward history that should have been discarded")
	}
}

func TestJumpToMarkPushesJump(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	markTarget := findRow(t, m, "Follow up with finance about the Q3 budget doc")
	m.cursor = markTarget
	m = sendKey(m, "m")
	m = sendKey(m, "a")

	origin := findRow(t, m, "Call the vet about Fido's checkup")
	m.cursor = origin

	m = sendKey(m, "'")
	m = sendKey(m, "a")
	if m.cursor != markTarget {
		t.Fatalf("'a didn't jump to the mark")
	}

	m = sendKey(m, "ctrl+o")
	if m.cursor != origin {
		t.Errorf("cursor after ctrl+o = %d, want %d (back to before 'a)", m.cursor, origin)
	}
}

func TestJumpParagraphPushesJump(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	origin := findRow(t, m, "Ship orgtd v0.1")
	m.cursor = origin

	m = sendKey(m, "}")
	if m.cursor == origin {
		t.Fatalf("} didn't move the cursor; can't test jumping back from it")
	}

	m = sendKey(m, "ctrl+o")
	if m.cursor != origin {
		t.Errorf("cursor after ctrl+o = %d, want %d (back to before })", m.cursor, origin)
	}
}

func TestSearchConfirmPushesJumpFromOrigin(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	origin := findRow(t, m, "Call the vet about Fido's checkup")
	m.cursor = origin

	m = sendKey(m, "/")
	m = typeKeys(m, "finance")
	afterIncremental := m.cursor
	if afterIncremental == origin {
		t.Fatalf("incremental search didn't move the cursor")
	}
	m, _ = sendKeyCmd(m, "enter")
	if m.cursor != afterIncremental {
		t.Fatalf("confirming the search moved the cursor away from the match")
	}

	m = sendKey(m, "ctrl+o")
	if m.cursor != origin {
		t.Errorf("cursor after ctrl+o = %d, want %d (search origin, not some intermediate incremental-search position)", m.cursor, origin)
	}
}

func TestSwitchViewPushesJumpAndCtrlOReturnsAcrossViews(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	origin := findRow(t, m, "Call the vet about Fido's checkup")
	m.cursor = origin
	originHeadline := m.currentHeadline()

	m = sendKey(m, ":")
	m = typeKeys(m, "agenda")
	m, _ = sendKeyCmd(m, "enter")
	if m.view != agendaView {
		t.Fatalf("setup: :agenda didn't switch views")
	}

	m = sendKey(m, "ctrl+o")
	if m.view != outlineView {
		t.Fatalf("view after ctrl+o = %v, want outlineView", m.view)
	}
	if m.currentHeadline() != originHeadline {
		t.Errorf("cursor after ctrl+o = %v, want back on %q", m.currentHeadline(), originHeadline.Title)
	}
}

// TestJumpForwardReturnsToViewOnlyPosition covers the cross-view case the
// first fix's tests missed: switching to a view whose row 0 has no
// headline or file of its own (calendar view's row 0 is a day-header
// section row), pressing ctrl+o to go back, then gi should still return
// there. Before this fix, pushJumpAt refused to bookmark a headline-less
// row at all, so jumpBack's implicit "bookmark the live position" step
// had nothing to record and gi silently did nothing.
//
// The fixture event is deliberately in the future — :calendar now
// positions the cursor on the in-progress (or most recent past) meeting
// when one exists (see enterCalendarView), which would otherwise land
// row 0 on the event itself rather than the day-header row this test
// means to exercise.
func TestJumpForwardReturnsToViewOnlyPosition(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	ws.Files = append(ws.Files, &org.File{
		Path: filepath.Join(ws.Dir, "calendar.org"),
		Headlines: []*org.Headline{
			calendarEventHeadline("abc123", now.Add(time.Hour), now.Add(2*time.Hour)),
		},
	})
	m := New(ws)
	origin := findRow(t, m, "Call the vet about Fido's checkup")
	m.cursor = origin

	m = sendKey(m, ":")
	m = typeKeys(m, "calendar")
	m, _ = sendKeyCmd(m, "enter")
	if m.view != calendarView {
		t.Fatalf("setup: :calendar didn't switch views")
	}
	if m.currentHeadline() != nil {
		t.Fatalf("setup: expected row 0 of calendar view to have no headline")
	}

	m = sendKey(m, "ctrl+o")
	if m.view != outlineView {
		t.Fatalf("view after ctrl+o = %v, want outlineView", m.view)
	}
	if m.currentHeadline() == nil || m.currentHeadline().Title != "Call the vet about Fido's checkup" {
		t.Fatalf("ctrl+o didn't return to origin")
	}

	m = sendKey(m, "g")
	m = sendKey(m, "i")
	if m.view != calendarView {
		t.Errorf("view after gi = %v, want calendarView (forward to the view left by ctrl+o)", m.view)
	}
}

// TestJumpForwardReturnsToEmptyView covers the other gap the same bug
// had: a view with no rows at all (an empty agenda, "Nothing due"), not
// just a headline-less row 0. Before this fix, pushJumpAt's bounds check
// (idx >= len(m.rows)) rejected idx==0 outright when m.rows was empty,
// so jumpBack's implicit bookmark of the live position failed there too.
func TestJumpForwardReturnsToEmptyView(t *testing.T) {
	// A plain TODO with no NEXT keyword and no scheduled/deadline date
	// shows up in the outline but nowhere in the agenda, so the agenda
	// ends up with zero rows while the outline still has one to jump
	// from.
	ws := &workspace.Workspace{
		Dir: t.TempDir(),
		Files: []*org.File{{
			Path:      filepath.Join(t.TempDir(), "todo.org"),
			Headlines: []*org.Headline{{Level: 1, Keyword: "TODO", Title: "Someday maybe"}},
		}},
	}
	m := New(ws)
	if len(m.rows) == 0 {
		t.Fatalf("setup: outline view unexpectedly empty; can't record an origin")
	}
	origin := m.cursor

	m = sendKey(m, ":")
	m = typeKeys(m, "agenda")
	m, _ = sendKeyCmd(m, "enter")
	if m.view != agendaView {
		t.Fatalf("setup: :agenda didn't switch views")
	}
	if len(m.rows) != 0 {
		t.Fatalf("setup: expected an empty agenda, got %d rows", len(m.rows))
	}

	m = sendKey(m, "ctrl+o")
	if m.view != outlineView || m.cursor != origin {
		t.Fatalf("ctrl+o didn't return to origin: view=%v cursor=%d", m.view, m.cursor)
	}

	m = sendKey(m, "g")
	m = sendKey(m, "i")
	if m.view != agendaView {
		t.Errorf("view after gi = %v, want agendaView (forward to the empty view left by ctrl+o)", m.view)
	}
}

func TestPlainIStillEditsAndDoesNotJump(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")

	m, cmd := sendKeyCmd(m, "i")
	if cmd == nil {
		t.Errorf("plain i should still start an edit (launch $EDITOR)")
	}
	if m.mode == meetingPickerMode {
		t.Errorf("plain i should never be treated as the gi jump-forward chord")
	}
}

func TestGThenIIsTwoKeyChordNotSingleG(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")

	m = sendKey(m, "g")
	if !m.pendingG {
		t.Fatalf("expected pendingG after a single g")
	}
	// The second key of "gi" should not also start an edit.
	_, cmd := sendKeyCmd(m, "i")
	if cmd != nil {
		t.Errorf("gi launched an editor instead of jumping forward")
	}
}
