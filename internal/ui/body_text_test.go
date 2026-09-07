package ui

import (
	"strings"
	"testing"
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
