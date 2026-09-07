package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/sburnett/orgtd/internal/org"
)

func TestBodyTextVisibleByDefault(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	idx := findRow(t, m, "Read the RFC linked in yesterday's design review")

	bodyIdx := idx + 1
	r := m.rows[bodyIdx]
	if !r.isBodyLine {
		t.Fatalf("row after the headline = %+v, want its body line", r)
	}
	if !strings.Contains(r.bodyText, "Came up in the API versioning discussion.") {
		t.Errorf("bodyText = %q, want the fixture's body content", r.bodyText)
	}
}

func TestBodyTextTrailingBlankLineNotShown(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	idx := findRow(t, m, "Call the vet about Fido's checkup")

	// This entry has no real body content in the fixture — just the
	// conventional blank line before the next headline, which the
	// parser can't distinguish from a deliberate trailing blank in the
	// body. It must not show up as a meaningless empty row.
	if m.rows[idx+1].isBodyLine {
		t.Errorf("row after a body-less headline = %+v, want the next headline, not a phantom blank body line", m.rows[idx+1])
	}
}

func TestHeadlineWithOnlyBodyGetsAFoldIndicator(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	idx := findRow(t, m, "Read the RFC linked in yesterday's design review")
	h := m.rows[idx].headline
	if len(h.Children) != 0 {
		t.Fatalf("fixture assumption broken: expected a childless headline")
	}

	line := stripANSI(m.renderRow(m.rows[idx]))
	if !strings.Contains(line, "▼") {
		t.Errorf("row = %q, want a fold arrow even though it has no children (it has a body)", line)
	}
}

func TestFoldingAHeadlineHidesItsBodyToo(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	idx := findRow(t, m, "Read the RFC linked in yesterday's design review")
	m.cursor = idx
	before := len(m.rows)

	m = sendKey(m, "z")
	m = sendKey(m, "c")

	if len(m.rows) != before-1 {
		t.Fatalf("rows after folding a body-only headline = %d, want %d (the body line hidden)", len(m.rows), before-1)
	}
	if m.rows[idx].isBodyLine {
		t.Errorf("body line still present after folding")
	}

	m = sendKey(m, "z")
	m = sendKey(m, "o")
	if len(m.rows) != before {
		t.Errorf("rows after unfolding = %d, want %d (body line back)", len(m.rows), before)
	}
}

func TestBodyLineIsInertButActionsApplyToOwningHeadline(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	idx := findRow(t, m, "Read the RFC linked in yesterday's design review")
	h := m.rows[idx].headline
	bodyIdx := idx + 1
	if !m.rows[bodyIdx].isBodyLine {
		t.Fatalf("fixture assumption broken: expected a body line right after the headline")
	}

	m.cursor = bodyIdx
	origKeyword := h.Keyword
	m = sendKey(m, "r")

	if h.Keyword == origKeyword {
		t.Errorf("rotating status while the cursor is on a body line did not affect the owning headline")
	}
}

func TestCaretFromBodyLineGoesToOwningHeadline(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	idx := findRow(t, m, "Read the RFC linked in yesterday's design review")
	h := m.rows[idx].headline
	bodyIdx := idx + 1
	if !m.rows[bodyIdx].isBodyLine {
		t.Fatalf("fixture assumption broken: expected a body line right after the headline")
	}

	m.cursor = bodyIdx
	m = sendKey(m, "^")

	if m.currentHeadline() != h || m.rows[m.cursor].isBodyLine {
		t.Errorf("^ from a body line landed on %+v, want the owning headline's own row", m.rows[m.cursor])
	}
}

func TestHKeyFromBodyLineGoesToOwningHeadline(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	idx := findRow(t, m, "Read the RFC linked in yesterday's design review")
	h := m.rows[idx].headline
	bodyIdx := idx + 1
	if !m.rows[bodyIdx].isBodyLine {
		t.Fatalf("fixture assumption broken: expected a body line right after the headline")
	}

	m.cursor = bodyIdx
	m = sendKey(m, "h")

	if m.currentHeadline() != h || m.rows[m.cursor].isBodyLine {
		t.Errorf("h from a body line landed on %+v, want the owning headline's own row", m.rows[m.cursor])
	}
}

func TestBodyLineRenderingIsDimmedAndIndented(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	idx := findRow(t, m, "Read the RFC linked in yesterday's design review")
	bodyIdx := idx + 1

	line := m.renderRow(m.rows[bodyIdx])
	if !strings.Contains(line, "\x1b[") {
		t.Errorf("body line = %q, want some styling applied (dimmed/italic)", line)
	}
	plain := stripANSI(line)
	if strings.TrimSpace(plain) != "Came up in the API versioning discussion." {
		t.Errorf("body line text = %q, want just the trimmed body content", strings.TrimSpace(plain))
	}
	if !strings.HasPrefix(plain, "  ") {
		t.Errorf("body line = %q, want leading indentation", plain)
	}
}

func TestJSkipsOverBodyLines(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	idx := findRow(t, m, "Read the RFC linked in yesterday's design review")
	m.cursor = idx
	if !m.rows[idx+1].isBodyLine {
		t.Fatalf("fixture assumption broken: expected a body line right after the headline")
	}

	m = sendKey(m, "j")

	if m.rows[m.cursor].isBodyLine {
		t.Fatalf("cursor landed on a body line: %+v", m.rows[m.cursor])
	}
	if got := m.currentHeadline(); got == nil || got.Title != "Follow up with finance about the Q3 budget doc" {
		t.Errorf("j from an entry with a body = %v, want the next real entry (body line skipped)", got)
	}
}

func TestKSkipsOverBodyLinesGoingUp(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	idx := findRow(t, m, "Follow up with finance about the Q3 budget doc")
	m.cursor = idx

	m = sendKey(m, "k")

	if m.rows[m.cursor].isBodyLine {
		t.Fatalf("cursor landed on a body line: %+v", m.rows[m.cursor])
	}
	if got := m.currentHeadline(); got == nil || got.Title != "Read the RFC linked in yesterday's design review" {
		t.Errorf("k past an entry with a body = %v, want that entry itself (body line skipped)", got)
	}
}

func TestCursorNeverLandsOnTrailingBodyLineAtEndOfList(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	last := len(m.rows) - 1
	if !m.rows[last].isBodyLine {
		t.Skip("fixture's last row isn't a body line in this configuration; nothing to exercise")
	}

	m.cursor = last - 1
	m = sendKey(m, "j")
	m = sendKey(m, "j") // try to overshoot past the end

	if m.rows[m.cursor].isBodyLine {
		t.Errorf("cursor ended on the trailing body line at the end of the list: %+v", m.rows[m.cursor])
	}
}

func TestHalfPageScrollAlsoSkipsBodyLines(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.height = 10
	idx := findRow(t, m, "Read the RFC linked in yesterday's design review")
	m.cursor = idx

	m = sendKey(m, "ctrl+d")

	if m.rows[m.cursor].isBodyLine {
		t.Errorf("cursor after ctrl+d landed on a body line: %+v", m.rows[m.cursor])
	}
}

func TestCursorHighlightExtendsOverEntrysBodyLines(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.width, m.height = 100, len(m.rows)+3
	idx := findRow(t, m, "Read the RFC linked in yesterday's design review")
	m.cursor = idx

	out := m.View()
	lines := strings.Split(out, "\n")
	titleLine := lines[idx]
	bodyLine := lines[idx+1]

	if !strings.Contains(titleLine, "\x1b[") {
		t.Fatalf("title line not highlighted at all: %q", titleLine)
	}
	// Both the title and its body line should be padded to the full
	// terminal width by the shared cursor background, not just the
	// title row.
	if got := lipgloss.Width(titleLine); got != m.width {
		t.Errorf("title line width = %d, want %d (highlighted)", got, m.width)
	}
	if got := lipgloss.Width(bodyLine); got != m.width {
		t.Errorf("body line width = %d, want %d (highlighted along with its entry)", got, m.width)
	}

	// The row after the body (a sibling entry) must NOT be highlighted.
	nextLine := lines[idx+2]
	if got := lipgloss.Width(nextLine); got >= m.width {
		t.Errorf("unrelated row unexpectedly highlighted to full width: %q", nextLine)
	}
}

func TestScrollingToAnEntryRevealsAllOfItsBodyLines(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.width = 80
	m.height = 6 // small: page holds only a handful of rows

	idx := findRow(t, m, "Read the RFC linked in yesterday's design review")
	h := m.rows[idx].headline
	// Set outright (not appended to the existing body) to avoid any
	// stray trailing-blank artifact from the original fixture ending up
	// in the middle of the new body instead of at the end.
	h.Body = []string{"First body line.", "Second body line.", "Third body line."}
	m.rebuildRows()
	idx = findRow(t, m, "Read the RFC linked in yesterday's design review")
	end := m.entryEnd(idx)
	if end != idx+3 {
		t.Fatalf("fixture setup broken: entryEnd = %d, want %d (title + 3 body lines)", end, idx+3)
	}

	// Walk the cursor down to this entry one row at a time, as a user
	// scrolling down normally would.
	m.cursor, m.offset = 0, 0
	for m.cursor < idx {
		m = sendKey(m, "j")
	}

	page := m.pageSize()
	if m.offset > idx {
		t.Fatalf("offset = %d, scrolled past the entry's own title row (%d)", m.offset, idx)
	}
	if end >= m.offset+page {
		t.Errorf("entry's last body line (row %d) not within the visible page [%d, %d)", end, m.offset, m.offset+page)
	}

	out := m.View()
	lines := strings.Split(out, "\n")
	for i := idx; i <= end; i++ {
		visibleIdx := i - m.offset
		if visibleIdx < 0 || visibleIdx >= len(lines) || strings.TrimSpace(stripANSI(lines[visibleIdx])) == "" {
			t.Errorf("row %d of the entry not rendered on screen (screen line %d): %q", i, visibleIdx, lines)
		}
	}
}

func TestCursorNeverRestsOnABodyLine(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)

	hasBody := false
	for _, r := range m.rows {
		if r.isBodyLine {
			hasBody = true
			break
		}
	}
	if !hasBody {
		t.Skip("fixture has no body lines to exercise")
	}

	// Every key that can move the cursor should be safe to mash without
	// ever landing on a body line — the invariant is enforced centrally
	// in ensureVisible, not by each individual command.
	for _, key := range []string{"j", "j", "j", "k", "G", "gg", "l", "h", "$", "^", "j"} {
		if key == "gg" {
			m = sendKey(m, "g")
			m = sendKey(m, "g")
		} else {
			m = sendKey(m, key)
		}
		if m.rows[m.cursor].isBodyLine {
			t.Fatalf("cursor rests on a body line after %q: %+v", key, m.rows[m.cursor])
		}
	}
}

func TestGSnapsToLastEntryNotItsBodyLine(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	// Give the very last headline in the workspace a body, so the very
	// last row in m.rows is one of its body lines.
	var last *org.Headline
	for _, f := range m.ws.Files {
		org.Walk(f.Headlines, func(h *org.Headline) { last = h })
	}
	last.Body = []string{"Trailing note."}
	m.rebuildRows()
	m.width, m.height = 100, len(m.rows)+3
	if !m.rows[len(m.rows)-1].isBodyLine {
		t.Fatalf("fixture setup broken: last row still isn't a body line")
	}

	m = sendKey(m, "G")

	if m.rows[m.cursor].isBodyLine {
		t.Fatalf("cursor after G rests on a body line: %+v", m.rows[m.cursor])
	}
	if m.currentHeadline() != last {
		t.Errorf("cursor after G = %v, want the last entry itself", m.currentHeadline())
	}
	// The whole entry (title + body) must be highlighted, not just the
	// last line.
	lines := strings.Split(m.View(), "\n")
	titleLine := lines[m.cursor]
	bodyLine := lines[m.cursor+1]
	if lipgloss.Width(titleLine) != m.width {
		t.Errorf("title line not highlighted to full width: %q", titleLine)
	}
	if lipgloss.Width(bodyLine) != m.width {
		t.Errorf("body line not highlighted to full width: %q", bodyLine)
	}
}

func TestLSkipsOverBodyEntirelyToRealChild(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	idx := findRow(t, m, "Ship orgtd v0.1") // has both a body and children
	h := m.rows[idx].headline
	if len(visibleBodyLines(h)) == 0 || len(h.Children) == 0 {
		t.Fatalf("fixture assumption broken: expected 'Ship orgtd v0.1' to have both a body and children")
	}
	m.cursor = idx

	m = sendKey(m, "l")

	if m.rows[m.cursor].isBodyLine {
		t.Fatalf("l landed on a body line: %+v", m.rows[m.cursor])
	}
	if got := m.currentHeadline(); got != h.Children[0] {
		t.Errorf("l = %v, want the first real child %v (body skipped over)", got, h.Children[0])
	}
}
