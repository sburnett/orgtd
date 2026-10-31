package ui

import (
	"fmt"
	"strings"
	"testing"
)

// manyPlainEntriesFixture builds a workspace with n top-level entries,
// each a single line with no body text — so every row is a real,
// steppable entry (see moveCursor, which skips body-line rows when
// counting) and a page-sized cursor move lands at exactly the expected
// row index, with no ambiguity from body lines mixed in.
func manyPlainEntriesFixture(t *testing.T, n int) Model {
	t.Helper()
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "* TODO Entry %d\n", i)
	}
	ws := agendaFixture(t, b.String())
	m := New(ws)
	m.width, m.height = 80, 10 // pageSize() = 8 (height minus the 2-line status/command-line area)
	return m
}

func TestPageDownMovesCursorByAFullPage(t *testing.T) {
	m := manyPlainEntriesFixture(t, 60)
	m.cursor = 0
	page := m.pageSize()

	m = sendKey(m, "pgdown")

	if m.cursor != page {
		t.Errorf("cursor after pgdown = %d, want %d (a full page, not half like ctrl-d)", m.cursor, page)
	}
}

func TestPageUpMovesCursorByAFullPage(t *testing.T) {
	m := manyPlainEntriesFixture(t, 60)
	page := m.pageSize()
	start := page * 2
	m.cursor = start

	m = sendKey(m, "pgup")

	if m.cursor != start-page {
		t.Errorf("cursor after pgup = %d, want %d (a full page back)", m.cursor, start-page)
	}
}

func TestPageDownClampsAtTheEnd(t *testing.T) {
	m := manyPlainEntriesFixture(t, 60)
	last := len(m.rows) - 1
	m.cursor = last

	m = sendKey(m, "pgdown")

	if m.cursor != last {
		t.Errorf("cursor after pgdown at the last row = %d, want unchanged %d", m.cursor, last)
	}
}

func TestPageUpClampsAtTheStart(t *testing.T) {
	m := manyPlainEntriesFixture(t, 60)
	m.cursor = 0

	m = sendKey(m, "pgup")

	if m.cursor != 0 {
		t.Errorf("cursor after pgup at the first row = %d, want unchanged 0", m.cursor)
	}
}

func TestPageDownSkipsBodyLines(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.height = 10
	idx := findRow(t, m, "Read the RFC linked in yesterday's design review")
	m.cursor = idx

	m = sendKey(m, "pgdown")

	if m.rows[m.cursor].kind == rowBody {
		t.Errorf("cursor after pgdown landed on a body line: %+v", m.rows[m.cursor])
	}
}

// entriesWithBodyFixture builds a workspace of n entries, each with
// bodyLines lines of body text, so one entry spans several rows.
func entriesWithBodyFixture(t *testing.T, n, bodyLines int) Model {
	t.Helper()
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "* TODO Entry %d\n", i)
		for j := 0; j < bodyLines; j++ {
			fmt.Fprintf(&b, "body line %d\n", j)
		}
	}
	m := New(agendaFixture(t, b.String()))
	m.width, m.height = 80, 24
	return m
}

func TestPageDownCountsBodyLines(t *testing.T) {
	m := entriesWithBodyFixture(t, 60, 4)
	page := m.pageSize()

	m = sendKey(m, "pgdown")

	if m.cursor == 0 || m.cursor > page {
		t.Errorf("cursor after pgdown = %d, want at most one page of rows (%d)", m.cursor, page)
	}
	if m.rows[m.cursor].kind == rowBody {
		t.Errorf("cursor after pgdown landed on a body line: %+v", m.rows[m.cursor])
	}
}

func TestCtrlDAndCtrlUMoveHalfAPageOfRows(t *testing.T) {
	m := entriesWithBodyFixture(t, 60, 4)
	m = sendKey(m, "pgdown")
	m = sendKey(m, "pgdown")
	start := m.cursor
	half := m.pageSize() / 2

	m = sendKey(m, "ctrl+d")
	if got := m.cursor - start; got <= 0 || got > half {
		t.Errorf("ctrl+d moved %d rows, want 1..%d", got, half)
	}

	start = m.cursor
	m = sendKey(m, "ctrl+u")
	if got := start - m.cursor; got <= 0 || got > half {
		t.Errorf("ctrl+u moved %d rows, want 1..%d", got, half)
	}
}

// Every row must appear on screen at some point while paging through the
// list, in both directions, wherever the cursor sits in the window.
func TestPagingNeverSkipsARow(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cursor int // rows below the top of the window at the start
	}{{"cursor at top", 0}, {"cursor at bottom", 21}} {
		t.Run(tc.name, func(t *testing.T) {
			m := entriesWithBodyFixture(t, 60, 4)
			m.cursor = m.entryStart(tc.cursor)
			m = sendKey(m, "k")
			m = sendKey(m, "j")
			for _, key := range []string{"pgdown", "pgup"} {
				for i := 0; i < len(m.rows); i++ {
					top, bottom := m.offset, m.offset+m.pageSize()
					m = sendKey(m, key)
					if m.offset > bottom || m.offset+m.pageSize() < top {
						t.Fatalf("%s moved the window from [%d,%d) to start at %d, skipping rows", key, top, bottom, m.offset)
					}
				}
			}
			if m.cursor != 0 || m.offset != 0 {
				t.Errorf("after paging back up, cursor = %d, offset = %d, want both 0", m.cursor, m.offset)
			}
		})
	}
}

func TestPageDownStopsWithALastFullPageOnScreen(t *testing.T) {
	m := entriesWithBodyFixture(t, 60, 4)
	for i := 0; i < len(m.rows); i++ {
		m = sendKey(m, "pgdown")
	}
	if want := m.lastPageOffset(); m.offset != want {
		t.Errorf("offset at the end = %d, want %d (a full last page, not blank space)", m.offset, want)
	}
	if want := m.entryStart(len(m.rows) - 1); m.cursor != want {
		t.Errorf("cursor at the end = %d, want the last entry at %d", m.cursor, want)
	}
}

func TestPagingStepsPastAnEntryTallerThanAPage(t *testing.T) {
	m := entriesWithBodyFixture(t, 3, 40)
	m = sendKey(m, "j")
	first := m.cursor

	m = sendKey(m, "pgdown")
	if m.cursor <= first {
		t.Fatalf("pgdown stuck at row %d on an entry taller than a page", m.cursor)
	}
	if m.rows[m.cursor].kind == rowBody {
		t.Errorf("cursor after pgdown landed on a body line: %+v", m.rows[m.cursor])
	}

	m = sendKey(m, "pgup")
	if m.cursor != first {
		t.Errorf("cursor after pgup = %d, want back at %d", m.cursor, first)
	}
}

func TestCtrlDMovesOnAOneRowPage(t *testing.T) {
	m := manyPlainEntriesFixture(t, 10)
	m.height = 3 // one row of content, so half a page rounds down to 0

	m = sendKey(m, "ctrl+d")

	if m.cursor != 1 {
		t.Errorf("cursor after ctrl+d = %d, want 1", m.cursor)
	}
}

// A view can have far more sections than fit on a page; only the ones on
// screen take up room, so they mustn't shrink the jump.
func TestPageDownInCalendarMovesAFullPage(t *testing.T) {
	ws := loadFixture(t)
	ws.Files = append(ws.Files, manyDaysCalendarFile(ws.Dir, 30))
	m := New(ws)
	m.switchToView(calendarView)
	m.width, m.height = 100, 10
	m.cursor, m.offset = 0, 0
	page := m.pageSize()
	if page < 2 {
		t.Fatalf("pageSize = %d, want a page of several rows", page)
	}

	m = sendKey(m, "pgdown")

	if m.cursor != page {
		t.Errorf("cursor after pgdown = %d, want %d", m.cursor, page)
	}
	if m.offset != page {
		t.Errorf("offset after pgdown = %d, want %d", m.offset, page)
	}
}

func TestCtrlEScrollsViewDownWithoutMovingCursor(t *testing.T) {
	m := manyPlainEntriesFixture(t, 60)
	m.cursor = 5
	m.offset = 0

	m = sendKey(m, "ctrl+e")

	if m.offset != 1 {
		t.Errorf("offset after ctrl+e = %d, want 1", m.offset)
	}
	if m.cursor != 5 {
		t.Errorf("cursor after ctrl+e = %d, want unchanged 5 (still visible)", m.cursor)
	}
}

func TestCtrlYScrollsViewUpWithoutMovingCursor(t *testing.T) {
	m := manyPlainEntriesFixture(t, 60)
	m.offset = 10
	m.cursor = 15

	m = sendKey(m, "ctrl+y")

	if m.offset != 9 {
		t.Errorf("offset after ctrl+y = %d, want 9", m.offset)
	}
	if m.cursor != 15 {
		t.Errorf("cursor after ctrl+y = %d, want unchanged 15 (still visible)", m.cursor)
	}
}

func TestCtrlEDragsCursorDownWhenItWouldScrollOffTheTop(t *testing.T) {
	m := manyPlainEntriesFixture(t, 60)
	m.offset = 0
	m.cursor = 0

	m = sendKey(m, "ctrl+e")

	if m.offset != 1 {
		t.Errorf("offset after ctrl+e = %d, want 1", m.offset)
	}
	if m.cursor != 1 {
		t.Errorf("cursor after ctrl+e = %d, want 1 (dragged down to stay visible)", m.cursor)
	}
}

func TestCtrlYDragsCursorUpWhenItWouldScrollOffTheBottom(t *testing.T) {
	m := manyPlainEntriesFixture(t, 60)
	page := m.pageSize()
	m.offset = 10
	m.cursor = 10 + page - 1 // last visible row

	m = sendKey(m, "ctrl+y")

	if m.offset != 9 {
		t.Errorf("offset after ctrl+y = %d, want 9", m.offset)
	}
	wantCursor := 9 + page - 1
	if m.cursor != wantCursor {
		t.Errorf("cursor after ctrl+y = %d, want %d (dragged up to stay visible)", m.cursor, wantCursor)
	}
}

func TestCtrlEClampsAtTheEnd(t *testing.T) {
	m := manyPlainEntriesFixture(t, 60)
	last := len(m.rows) - 1
	m.offset = last
	m.cursor = last

	m = sendKey(m, "ctrl+e")

	if m.offset != last {
		t.Errorf("offset after ctrl+e at the end = %d, want unchanged %d", m.offset, last)
	}
}

func TestCtrlYClampsAtTheStart(t *testing.T) {
	m := manyPlainEntriesFixture(t, 60)
	m.offset = 0
	m.cursor = 0

	m = sendKey(m, "ctrl+y")

	if m.offset != 0 {
		t.Errorf("offset after ctrl+y at the start = %d, want unchanged 0", m.offset)
	}
}

func TestCtrlEAndCtrlYWorkInVisualMode(t *testing.T) {
	m := manyPlainEntriesFixture(t, 60)
	m.offset = 0
	m.cursor = 0

	m = sendKey(m, "V")
	m = sendKey(m, "ctrl+e")

	if m.mode != visualMode {
		t.Fatalf("mode after V, ctrl+e = %v, want visualMode", m.mode)
	}
	if m.offset != 1 {
		t.Errorf("offset after ctrl+e in visual mode = %d, want 1", m.offset)
	}
	if m.visualAnchor != 0 {
		t.Errorf("visualAnchor = %d, want unchanged 0", m.visualAnchor)
	}
	if m.cursor != 1 {
		t.Errorf("cursor after ctrl+e in visual mode = %d, want 1", m.cursor)
	}

	m = sendKey(m, "ctrl+y")
	if m.offset != 0 {
		t.Errorf("offset after ctrl+y in visual mode = %d, want 0", m.offset)
	}
}

func TestPageDownAndPageUpExtendVisualSelection(t *testing.T) {
	m := manyPlainEntriesFixture(t, 60)
	m.cursor = 0
	page := m.pageSize()

	m = sendKey(m, "V")
	m = sendKey(m, "pgdown")

	if m.mode != visualMode {
		t.Fatalf("mode after V, pgdown = %v, want visualMode", m.mode)
	}
	if m.visualAnchor != 0 {
		t.Errorf("visualAnchor = %d, want unchanged 0", m.visualAnchor)
	}
	if m.cursor != page {
		t.Errorf("cursor after pgdown in visual mode = %d, want %d", m.cursor, page)
	}

	m = sendKey(m, "pgup")
	if m.cursor != 0 {
		t.Errorf("cursor after pgup back in visual mode = %d, want 0", m.cursor)
	}
}
