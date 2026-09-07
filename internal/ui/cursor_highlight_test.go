package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestCursorHighlightCoversWholeLineRegardlessOfKeyword(t *testing.T) {
	// One row with a keyword (an extra styled, separately-reset segment
	// before the title) and one without, so a highlight that only
	// survives the first segment's reset would behave differently on
	// the two — exactly the inconsistency reported.
	ws := agendaFixture(t, "* TODO Has a keyword\n** Has none\n")
	m := New(ws)
	m.width = 60

	withKeyword := m.rows[1] // "* TODO Has a keyword"
	withoutKeyword := m.rows[2]

	lineA := m.renderRowWithBg(withKeyword, cursorBg)
	lineB := m.renderRowWithBg(withoutKeyword, cursorBg)

	// Every ANSI-styled segment on the line resets independently, so the
	// background escape must reappear after every one of them — not
	// just once at the very start. A regression here (wrapping the
	// whole pre-rendered line in one outer style) would leave a single
	// background code covering only the first segment.
	if n := strings.Count(lineA, "\x1b[") - strings.Count(lineA, "\x1b[0m"); n < 3 {
		t.Errorf("row with a keyword has too few independently-backgrounded segments (%d): %q", n, lineA)
	}
	if n := strings.Count(lineB, "\x1b[") - strings.Count(lineB, "\x1b[0m"); n < 2 {
		t.Errorf("row without a keyword has too few independently-backgrounded segments (%d): %q", n, lineB)
	}

	// Both lines, once background-padded to the terminal width (as
	// View() does for the cursor row), must reach exactly that width —
	// confirming the highlight extends the full line, not just up to
	// wherever the visible text ends.
	for _, line := range []string{lineA, lineB} {
		padded := m.padLineToWidth(line, cursorBg)
		if got := lipgloss.Width(padded); got != m.width {
			t.Errorf("padded highlighted line width = %d, want %d: %q", got, m.width, padded)
		}
	}
}

func TestCursorRowInViewIsPaddedToFullWidth(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.width, m.height = 200, len(m.rows)+3
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")

	out := m.View()
	lines := strings.Split(out, "\n")
	cursorLine := lines[m.cursor]

	if got := lipgloss.Width(cursorLine); got != m.width {
		t.Errorf("cursor line width = %d, want %d (full terminal width): %q", got, m.width, cursorLine)
	}
	// A non-cursor row is not padded — only the highlighted line should
	// stretch to the full width.
	otherLine := lines[m.cursor+1]
	if got := lipgloss.Width(otherLine); got >= m.width {
		t.Errorf("non-cursor line unexpectedly padded to full width: %q", otherLine)
	}
}
