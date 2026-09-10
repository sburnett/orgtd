package ui

import (
	"fmt"
	"testing"
	"time"

	"github.com/sburnett/orgtd/internal/org"
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
		if r.headline != nil && !r.isBodyLine && r.headline.Title == title {
			return i
		}
	}
	return -1
}

func TestHideDoneOffByDefaultWithoutOption(t *testing.T) {
	now := time.Now()
	orgText := fmt.Sprintf(`* DONE Old finished task
  CLOSED: [%s]
`, closedTS(now.Add(-48*time.Hour)))
	ws := agendaFixture(t, orgText)
	m := New(ws)

	if rowIndex(m, "Old finished task") < 0 {
		t.Errorf("stale DONE item should stay visible when WithHideDoneAfterHours is never passed")
	}
}

func TestHideDoneAfterHoursHidesStaleClosedItemsAndSubtree(t *testing.T) {
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
	m := New(ws, WithHideDoneAfterHours(24))

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

func TestHideDoneAfterHoursThresholdIsConfigurable(t *testing.T) {
	now := time.Now()
	orgText := fmt.Sprintf(`* DONE Finished two hours ago
  CLOSED: [%s]
`, closedTS(now.Add(-2*time.Hour)))
	ws := agendaFixture(t, orgText)
	m := New(ws, WithHideDoneAfterHours(1))

	if i := rowIndex(m, "Finished two hours ago"); i >= 0 {
		t.Errorf("item past a 1-hour threshold at row %d should be hidden", i)
	}
}

func TestHideDoneAfterHoursZeroOrNegativeUsesBuiltInDefault(t *testing.T) {
	now := time.Now()
	orgText := fmt.Sprintf(`* DONE Finished twelve hours ago
  CLOSED: [%s]
`, closedTS(now.Add(-12*time.Hour)))
	ws := agendaFixture(t, orgText)
	m := New(ws, WithHideDoneAfterHours(0))

	if rowIndex(m, "Finished twelve hours ago") < 0 {
		t.Error("hours <= 0 should fall back to the 24h default, not hide immediately")
	}
	if m.hideDoneAfterHours != 24 {
		t.Errorf("hideDoneAfterHours = %d, want 24 (built-in default)", m.hideDoneAfterHours)
	}
}

func TestHideDoneWithNoClosedTimestampStaysVisible(t *testing.T) {
	orgText := "* DONE Marked done but never stamped\n"
	ws := agendaFixture(t, orgText)
	m := New(ws, WithHideDoneAfterHours(24))

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
	m := New(ws, WithHideDoneAfterHours(24))

	if i := rowIndex(m, "Old finished task"); i >= 0 {
		t.Fatalf("stale DONE item at row %d should start hidden", i)
	}

	m = sendKey(m, ":")
	m = typeKeys(m, "toggledone")
	m, _ = sendKeyCmd(m, "enter")

	if rowIndex(m, "Old finished task") < 0 {
		t.Error(":toggledone should reveal the stale item")
	}

	m = sendKey(m, ":")
	m = typeKeys(m, "toggledone")
	m, _ = sendKeyCmd(m, "enter")

	if i := rowIndex(m, "Old finished task"); i >= 0 {
		t.Errorf(":toggledone again should re-hide the stale item, found at row %d", i)
	}
}

func TestHideDoneIgnoresUnparseableClosedTimestamp(t *testing.T) {
	h := &org.Headline{Keyword: "DONE", Closed: &org.Timestamp{Raw: "not a date"}}
	var m Model
	m.hideDoneEnabled = true
	m.hideDoneAfterHours = 24
	if m.hiddenAsStaleDone(h) {
		t.Error("an unparseable CLOSED timestamp should not be treated as stale")
	}
}
