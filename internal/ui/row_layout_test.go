package ui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/sburnett/orgtd/internal/org"
)

const (
	longTitle  = "Rewrite the sync engine so it handles renamed calendars without dropping any events"
	plannedRow = "* TODO " + longTitle + " :work:home:\n  SCHEDULED: <2026-10-31 Sat> DEADLINE: <2026-11-02 Mon>\n"
)

// layoutModel loads text as an outline width columns wide, with a fixed
// row list to look through.
func layoutModel(t *testing.T, text string, width int) *Model {
	t.Helper()
	m := New(agendaFixture(t, text))
	m.width = width
	m.height = 30
	m.rebuildRows()
	return &m
}

// lines renders every row m has, plain.
func lines(m *Model) []string {
	var out []string
	for _, r := range m.rows {
		out = append(out, stripANSI(m.renderRow(r)))
	}
	return out
}

func TestAllocateRow(t *testing.T) {
	tests := []struct {
		name         string
		avail, title int
		metas        []int
		wantTitle    int
		wantMetas    []int
	}{
		{"title keeps its share, metadata splits the rest", 100, 200, []int{50, 50}, 60, []int{18, 18}},
		{"a short part's spare room goes to the other", 100, 200, []int{6, 50}, 60, []int{6, 30}},
		{"spare metadata room goes back to the title", 100, 200, []int{6, 7}, 83, []int{6, 7}},
		{"a short title leaves the metadata the rest", 100, 20, []int{50, 60}, 20, []int{38, 38}},
		{"a part too narrow to read is dropped", 20, 200, []int{40, 40}, 12, []int{0, 6}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotTitle, gotMetas := allocateRow(tt.avail, tt.title, tt.metas, make([]int, len(tt.metas)))
			if gotTitle != tt.wantTitle || !reflect.DeepEqual(gotMetas, tt.wantMetas) {
				t.Errorf("allocateRow = %d, %v, want %d, %v", gotTitle, gotMetas, tt.wantTitle, tt.wantMetas)
			}
		})
	}
}

// TestRowFullOfMetadataKeepsItsTitle is the reported bug: tags and two
// planning dates used to crowd the title off the row entirely.
func TestRowFullOfMetadataKeepsItsTitle(t *testing.T) {
	m := layoutModel(t, "* TODO "+longTitle+" :alpha:beta:gamma:delta:epsilon:\n  SCHEDULED: <2026-10-31 Sat> DEADLINE: <2026-11-02 Mon>\n* Short\n", 80)
	m.collapsed[m.ws.Files[0].Headlines[0]] = true
	m.rebuildRows()
	row := stripANSI(m.renderRow(m.rows[1]))

	if !strings.Contains(row, "TODO Rewrite the sync engine") {
		t.Errorf("row = %q, want most of the title visible", row)
	}
	if !strings.Contains(row, ":alpha") {
		t.Errorf("row = %q, want tags still visible", row)
	}
	if !strings.Contains(row, "S:10-31") || !strings.Contains(row, "D:11-02") {
		t.Errorf("row = %q, want compact dates", row)
	}
	if w := lipgloss.Width(row); w > 80 {
		t.Errorf("row width = %d, want <= 80", w)
	}
}

func TestRowThatFitsKeepsFullDates(t *testing.T) {
	m := layoutModel(t, "* TODO Short :work:\n  DEADLINE: <2026-11-02 Mon>\n", 80)
	row := stripANSI(m.renderRow(m.rows[1]))
	if !strings.HasSuffix(row, "TODO Short  :work:  DEADLINE: <2026-11-02 Mon>") {
		t.Errorf("row = %q, want the unchanged full layout", row)
	}
}

func TestCompactDateShowsTheYearOnlyWhenItDiffers(t *testing.T) {
	m := layoutModel(t, "* Plain\n", 80)
	thisYear := m.now().Format("2006") + "-11-02 Mon"
	if got := m.compactTimestamp(&org.Timestamp{Raw: thisYear}); got != "11-02" {
		t.Errorf("compactTimestamp(%q) = %q", thisYear, got)
	}
	if got := m.compactTimestamp(&org.Timestamp{Raw: "1999-11-02 Tue +1w"}); got != "1999-11-02" {
		t.Errorf("compactTimestamp in another year = %q", got)
	}
}

// TestUnfoldedRowWrapsEachPartInItsOwnColumn covers the request's own
// example: a long headline must not bump the tags down a line.
func TestUnfoldedRowWrapsEachPartInItsOwnColumn(t *testing.T) {
	m := layoutModel(t, strings.TrimSuffix(plannedRow, "\n")+"\n  Body text\n", 80)
	got := lines(m)

	first := got[1]
	for _, want := range []string{"TODO Rewrite", ":work:", "SCHEDULED: <2026-10-31 Sat>"} {
		if !strings.Contains(first, want) {
			t.Errorf("first line = %q, want %q on it", first, want)
		}
	}
	if !strings.Contains(got[2], ":home:") {
		t.Errorf("second line = %q, want the tag that didn't fit", got[2])
	}
	if !strings.Contains(got[2], "DEADLINE: <2026-11-02 Mon>") {
		t.Errorf("second line = %q, want the second date stacked under the first", got[2])
	}
	column := func(s, word string) int { return lipgloss.Width(s[:strings.Index(s, word)]) }
	if column(got[2], "DEADLINE") != column(first, "SCHEDULED") {
		t.Errorf("dates aren't in one column:\n%s\n%s", first, got[2])
	}
	all := strings.Join(got, "\n")
	for _, word := range strings.Fields(longTitle) {
		if !strings.Contains(all, word) {
			t.Errorf("rows lost %q:\n%s", word, all)
		}
	}
	if strings.Contains(all, "…") {
		t.Errorf("unfolded row was still cut:\n%s", all)
	}
	for _, l := range got {
		if w := lipgloss.Width(l); w > 80 {
			t.Errorf("line %q is %d wide, want <= 80", l, w)
		}
	}
	if !strings.Contains(got[len(got)-1], "Body text") {
		t.Errorf("last line = %q, want the body after the wrapped title", got[len(got)-1])
	}
}

func TestKeepsTakeFromTheTitleDownToItsFloor(t *testing.T) {
	gotTitle, gotMetas := allocateRow(50, 200, []int{10, 40}, []int{10, 0})
	if gotTitle != 30 || !reflect.DeepEqual(gotMetas, []int{10, 6}) {
		t.Errorf("allocateRow = %d, %v", gotTitle, gotMetas)
	}
	gotTitle, gotMetas = allocateRow(50, 200, []int{45}, []int{45})
	if gotTitle != 16 || !reflect.DeepEqual(gotMetas, []int{32}) {
		t.Errorf("allocateRow = %d, %v, want the title held at its floor", gotTitle, gotMetas)
	}
}

// TestNarrowRowBreaksDatesBetweenWordsNotInsideThem: with no room for a
// date on one line, its timestamp still stays whole.
func TestNarrowRowBreaksDatesBetweenWordsNotInsideThem(t *testing.T) {
	m := layoutModel(t, plannedRow+"  Body text\n", 60)
	all := strings.Join(lines(m), "\n")
	for _, want := range []string{"SCHEDULED:", "<2026-10-31 Sat>", "DEADLINE:", "<2026-11-02 Mon>"} {
		if !strings.Contains(all, want) {
			t.Errorf("%q isn't whole:\n%s", want, all)
		}
	}
}

func TestWrappedTagsBreakBetweenTags(t *testing.T) {
	tags := strings.Fields("alpha_tag beta_tag gamma_tag delta_tag epsilon_tag zeta_tag eta_tag theta_tag")
	m := layoutModel(t, "* TODO Title :"+strings.Join(tags, ":")+":\n  Body\n", 40)
	all := strings.Join(lines(m), "\n")
	for _, tag := range tags {
		if !strings.Contains(all, ":"+tag+":") {
			t.Errorf("tag %q isn't whole:\n%s", tag, all)
		}
	}
}

func TestFoldedRowStaysOnOneLine(t *testing.T) {
	m := layoutModel(t, plannedRow+"  Body text\n", 80)
	m.collapsed[m.ws.Files[0].Headlines[0]] = true
	m.rebuildRows()
	if len(m.rows) != 2 {
		t.Fatalf("got %d rows, want a file row and a single entry row", len(m.rows))
	}
}

// TestTruncatedLeafCanBeUnfolded: a leaf too wide for one line starts
// folded, and Tab shows it whole.
func TestTruncatedLeafCanBeUnfolded(t *testing.T) {
	m := layoutModel(t, "* TODO "+longTitle+"\n* Short\n", 50)
	if len(m.rows) != 3 {
		t.Fatalf("got %d rows, want the leaf folded onto one", len(m.rows))
	}
	row := stripANSI(m.renderRow(m.rows[1]))
	if !strings.Contains(row, "▶") || !strings.Contains(row, "…") {
		t.Errorf("row = %q, want a closed fold and a cut title", row)
	}
	if strings.Contains(stripANSI(m.renderRow(m.rows[2])), "▶") {
		t.Errorf("a short leaf has nothing to fold")
	}

	m.cursor = 1
	m.toggleFold()
	if len(m.rows) <= 3 {
		t.Fatalf("got %d rows, want the leaf wrapped", len(m.rows))
	}
	if row := stripANSI(m.renderRow(m.rows[1])); !strings.Contains(row, "▼") {
		t.Errorf("row = %q, want an open fold", row)
	}
	if all := strings.Join(lines(m), "\n"); strings.Contains(all, "…") || !strings.Contains(all, "events") {
		t.Errorf("unfolded leaf not shown whole:\n%s", all)
	}

	m.toggleFold()
	if len(m.rows) != 3 {
		t.Errorf("got %d rows after folding again, want 3", len(m.rows))
	}
}

// TestContinuationLinesAreNotSteppedOn: j moves entry by entry, and the
// cursor highlight covers every line of a wrapped entry.
func TestContinuationLinesAreNotSteppedOn(t *testing.T) {
	m := layoutModel(t, plannedRow+"  Body\n* Next entry\n", 60)
	m.cursor = 1
	m.moveCursor(1)
	if h := m.rows[m.cursor].headline; h == nil || h.Title != "Next entry" {
		t.Errorf("cursor on %+v, want the next entry", m.rows[m.cursor])
	}
	if end := m.entryEnd(1); strings.TrimSpace(m.rows[end].text) != "Body" {
		t.Errorf("entryEnd = row %d (%+v), want it to reach the body", end, m.rows[end])
	}
}

func TestOverflowsAgreesWithRendering(t *testing.T) {
	text := "* TODO Title that just about fits :tag:\n  DEADLINE: <2026-11-02 Mon>\n** NEXT [#A] Nested [[https://example.com][link]] one\n"
	natural := 0
	m := layoutModel(t, text, 0)
	org.Walk(m.ws.Files[0].Headlines, func(h *org.Headline) {
		natural = lipgloss.Width(stripANSI(m.renderRow(row{headline: h, level: h.Level})))
		m.width = natural
		if m.overflows(h) {
			t.Errorf("%q overflows at its own width %d", h.Title, natural)
		}
		m.width = natural - 1
		if !m.overflows(h) {
			t.Errorf("%q doesn't overflow at %d", h.Title, natural-1)
		}
		m.width = 0
	})
}

func TestWrapSpans(t *testing.T) {
	tests := []struct {
		s     string
		width int
		want  []string
	}{
		{"one two three", 7, []string{"one two", "three"}},
		{"one two three", 100, []string{"one two three"}},
		{"abcdefgh ij", 3, []string{"abc", "def", "gh", "ij"}},
		{"日本語日本語", 5, []string{"日本", "語日", "本語"}},
		{"", 5, []string{""}},
	}
	for _, tt := range tests {
		if got := wrapPlain(tt.s, tt.width); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("wrapPlain(%q, %d) = %q, want %q", tt.s, tt.width, got, tt.want)
		}
	}
}

// TestWrapKeepsStylingOnEveryLine: a style spanning a wrap point must
// reach both lines and be closed on each.
func TestWrapKeepsStylingOnEveryLine(t *testing.T) {
	styled := "\x1b[31mone two three\x1b[0m"
	for _, span := range wrapSpans(ansi.Strip(styled), 7) {
		line := ansi.Cut(styled, span[0], span[1])
		if !strings.HasPrefix(line, "\x1b[31m") || !strings.HasSuffix(line, "\x1b[0m") {
			t.Errorf("line %q isn't styled and closed", line)
		}
	}
}
