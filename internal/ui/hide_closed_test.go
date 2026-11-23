package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sburnett/orgtd/internal/config"
	"github.com/sburnett/orgtd/internal/org"
	"github.com/sburnett/orgtd/internal/workspace"
)

// closedTS formats t the way applyStatus itself stamps a CLOSED
// timestamp when marking an item done, for building fixtures whose
// staleness is relative to the real clock rather than a fixed date.
func closedTS(t time.Time) string {
	return t.Format("2006-01-02 Mon 15:04")
}

// rowIndex is findRow without the t.Fatalf on a miss, for asserting a
// title is absent (hidden) rather than present.
func rowIndex(m Model, title string) int {
	for i, r := range m.rows {
		if r.headline != nil && r.kind != rowBody && r.headline.Title == title {
			return i
		}
	}
	return -1
}

func TestHideClosedOffByDefaultWithoutOption(t *testing.T) {
	now := time.Now()
	orgText := fmt.Sprintf(`* DONE Old finished task
  CLOSED: [%s]
`, closedTS(now.Add(-48*time.Hour)))
	ws := agendaFixture(t, orgText)
	m := New(ws)

	if rowIndex(m, "Old finished task") < 0 {
		t.Errorf("stale DONE item should stay visible when WithHideClosedAfter is never passed")
	}
}

func TestHideClosedAfterHidesStaleClosedItemsAndSubtree(t *testing.T) {
	now := time.Now()
	orgText := fmt.Sprintf(`* DONE Old finished task
  CLOSED: [%s]
** Some child note
* CANCELLED Old cancelled task
  CLOSED: [%s]
* DONE Recently finished task
  CLOSED: [%s]
* TODO Still active task
`,
		closedTS(now.Add(-48*time.Hour)),
		closedTS(now.Add(-25*time.Hour)),
		closedTS(now.Add(-1*time.Hour)),
	)
	ws := agendaFixture(t, orgText)
	m := New(ws, WithHideClosedAfter(24*time.Hour))

	if i := rowIndex(m, "Old finished task"); i >= 0 {
		t.Errorf("stale DONE item at row %d should be hidden", i)
	}
	if i := rowIndex(m, "Some child note"); i >= 0 {
		t.Errorf("child of a hidden stale DONE item at row %d should also be hidden", i)
	}
	if i := rowIndex(m, "Old cancelled task"); i >= 0 {
		t.Errorf("stale CANCELLED item at row %d should be hidden", i)
	}
	if rowIndex(m, "Recently finished task") < 0 {
		t.Error("DONE item closed within the threshold should stay visible")
	}
	if rowIndex(m, "Still active task") < 0 {
		t.Error("non-done item should stay visible regardless of age")
	}
}

func TestHideClosedAfterThresholdIsConfigurable(t *testing.T) {
	now := time.Now()
	orgText := fmt.Sprintf(`* DONE Finished two hours ago
  CLOSED: [%s]
`, closedTS(now.Add(-2*time.Hour)))
	ws := agendaFixture(t, orgText)
	m := New(ws, WithHideClosedAfter(1*time.Hour))

	if i := rowIndex(m, "Finished two hours ago"); i >= 0 {
		t.Errorf("item past a 1-hour threshold at row %d should be hidden", i)
	}
}

func TestHideClosedAfterZeroOrNegativeUsesBuiltInDefault(t *testing.T) {
	now := time.Now()
	orgText := fmt.Sprintf(`* DONE Finished just now
  CLOSED: [%s]
* DONE Finished ten minutes ago
  CLOSED: [%s]
`, closedTS(now), closedTS(now.Add(-10*time.Minute)))
	ws := agendaFixture(t, orgText)
	m := New(ws, WithHideClosedAfter(0))

	if rowIndex(m, "Finished just now") < 0 {
		t.Error("after <= 0 should fall back to the 5m default, not hide immediately")
	}
	if i := rowIndex(m, "Finished ten minutes ago"); i >= 0 {
		t.Errorf("item past the 5m default at row %d should be hidden", i)
	}
	if got := time.Duration(m.cfg.HideClosedAfter); got != 5*time.Minute {
		t.Errorf("HideClosedAfter = %v, want 5m (built-in default)", got)
	}
}

func TestHideClosedWithNoClosedTimestampStaysVisible(t *testing.T) {
	orgText := "* DONE Marked done but never stamped\n"
	ws := agendaFixture(t, orgText)
	m := New(ws, WithHideClosedAfter(24*time.Hour))

	if rowIndex(m, "Marked done but never stamped") < 0 {
		t.Error("a DONE item with no CLOSED timestamp should never be hidden (no age to judge it by)")
	}
}

func TestToggleDoneCommandTogglesVisibility(t *testing.T) {
	now := time.Now()
	orgText := fmt.Sprintf(`* DONE Old finished task
  CLOSED: [%s]
`, closedTS(now.Add(-48*time.Hour)))
	ws := agendaFixture(t, orgText)
	m := New(ws, WithHideClosedAfter(24*time.Hour))

	if i := rowIndex(m, "Old finished task"); i >= 0 {
		t.Fatalf("stale DONE item at row %d should start hidden", i)
	}

	m = sendKey(m, ":")
	m = typeKeys(m, "toggleclosed")
	m, _ = sendKeyCmd(m, "enter")

	if rowIndex(m, "Old finished task") < 0 {
		t.Error(":toggleclosed should reveal the stale item")
	}

	m = sendKey(m, ":")
	m = typeKeys(m, "toggleclosed")
	m, _ = sendKeyCmd(m, "enter")

	if i := rowIndex(m, "Old finished task"); i >= 0 {
		t.Errorf(":toggleclosed again should re-hide the stale item, found at row %d", i)
	}
}

// TestWriteIncludesStaleDoneEntriesHiddenFromTheOutline guards a real
// concern: hide-closed filtering only ever affects rebuildRows' row
// list — it never touches the underlying org.File.Headlines tree, which
// is what :w (via org.RenderFile) actually walks. A hidden
// entry should still round-trip to disk exactly as if it were visible.
func TestWriteIncludesStaleDoneEntriesHiddenFromTheOutline(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "inbox.org")
	content := fmt.Sprintf("* DONE Old finished task\n  CLOSED: [%s]\n* TODO Still active\n", closedTS(time.Now().Add(-48*time.Hour)))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	initRepo(t, dir)
	f, err := org.ParseFile(path, org.DefaultKeywords())
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	ws := &workspace.Workspace{Dir: dir, Files: []*org.File{f}}
	m := New(ws, WithHideClosedAfter(24*time.Hour))

	if rowIndex(m, "Old finished task") >= 0 {
		t.Fatalf("fixture assumption broken: stale DONE entry should be hidden from the outline")
	}

	// An unrelated edit, so the file is actually dirty and :w has
	// something to write.
	m.cursor = findRow(t, m, "Still active")
	m = setStatus(m, "n") // TODO -> NEXT

	m = sendKey(m, ":")
	m = typeKeys(m, "w")
	m, _ = sendKeyCmd(m, "enter")
	if !strings.Contains(m.message, "Wrote") {
		t.Fatalf("message = %q, want it to confirm the write", m.message)
	}

	reparsed, err := org.ParseFile(path, org.DefaultKeywords())
	if err != nil {
		t.Fatalf("ParseFile after :w: %v", err)
	}
	var found bool
	org.Walk(reparsed.Headlines, func(h *org.Headline) {
		if h.Title == "Old finished task" {
			found = true
		}
	})
	if !found {
		t.Error("stale DONE entry, hidden from the outline, was dropped from disk on :w")
	}
}

func TestHideClosedIgnoresUnparseableClosedTimestamp(t *testing.T) {
	h := &org.Headline{Keyword: "DONE", Closed: &org.Timestamp{Raw: "not a date"}}
	var m Model
	m.hideClosedEnabled = true
	m.cfg.HideClosedAfter = config.Duration(24 * time.Hour)
	if m.hiddenAsStaleDone(h) {
		t.Error("an unparseable CLOSED timestamp should not be treated as stale")
	}
}

func TestHideAppliesToAnyKeywordWithClosedStamp(t *testing.T) {
	old := closedTS(time.Now().Add(-48 * time.Hour))
	ws := agendaFixture(t, fmt.Sprintf(`* WAITING Hidden waiting
  CLOSED: [%s]
* WAITING Visible waiting
`, old))
	m := New(ws, WithHideClosedAfter(24*time.Hour))
	if rowIndex(m, "Hidden waiting") >= 0 {
		t.Error("a stale entry with a CLOSED stamp should be hidden whatever its keyword")
	}
	if rowIndex(m, "Visible waiting") < 0 {
		t.Error("an entry with no CLOSED stamp should stay visible")
	}
}
