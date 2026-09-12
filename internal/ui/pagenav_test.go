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

	if m.rows[m.cursor].isBodyLine {
		t.Errorf("cursor after pgdown landed on a body line: %+v", m.rows[m.cursor])
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
