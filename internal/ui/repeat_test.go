package ui

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/sburnett/orgtd/internal/org"
	"github.com/sburnett/orgtd/internal/orgdate"
)

// rawOccurrenceDate extracts the literal date a Timestamp's Raw text
// names, ignoring any repeater or warning cookie — unlike
// orgdate.ParseTimestampDate, which rolls a repeating timestamp
// forward/floors it relative to "today" for agenda display. Tests that
// check what completing a repeating item wrote back need the literal
// date, not that display-time floor.
func rawOccurrenceDate(t *testing.T, ts *org.Timestamp) time.Time {
	t.Helper()
	raw := cookieRe.ReplaceAllString(ts.Raw, "")
	raw = strings.Join(strings.Fields(raw), " ")
	for _, layout := range orgdate.Layouts {
		if parsed, err := time.ParseInLocation(layout, raw, time.Local); err == nil {
			return parsed
		}
	}
	t.Fatalf("rawOccurrenceDate: could not parse %q", ts.Raw)
	return time.Time{}
}

var cookieRe = regexp.MustCompile(`(\+\+|\.\+|\+|-)\d+[dwmy]`)

func TestCompletingRecurringScheduledItemAdvancesInsteadOfClosing(t *testing.T) {
	now := orgdate.TruncateToDate(time.Now())
	// 2 weeks overdue, so a single naive "+1w" advance leaves it still 1
	// week overdue — proving this really is a one-interval step, not a
	// catch-up-to-today jump.
	overdue := now.AddDate(0, 0, -14)
	orgText := fmt.Sprintf("* TODO Weekly standup\n  SCHEDULED: <%s +1w>\n", ts(overdue))
	ws := agendaFixture(t, orgText)
	m := New(ws)
	m.cursor = findRow(t, m, "Weekly standup")
	h := m.currentHeadline()

	// TODO -> NEXT -> WAITING -> SOMEDAY -> DONE
	for _, shortcut := range []string{"n", "w", "s", "d"} {
		m = setStatus(m, shortcut)
	}

	if h.Keyword != "SOMEDAY" {
		t.Errorf("keyword after completing a recurring item = %q, want unchanged (SOMEDAY, its state right before DONE) per org-mode semantics", h.Keyword)
	}
	if h.Closed != nil {
		t.Errorf("Closed = %v, want nil (a repeating item never actually closes)", h.Closed)
	}
	if h.Scheduled == nil {
		t.Fatal("Scheduled = nil after completion, want an advanced timestamp")
	}
	if want, got := overdue.AddDate(0, 0, 7), rawOccurrenceDate(t, h.Scheduled); !got.Equal(want) {
		t.Errorf("Scheduled advanced to %v, want %v", got, want)
	}
	lastRepeat, exists := h.Properties["LAST_REPEAT"]
	if !exists || lastRepeat == "" {
		t.Error("LAST_REPEAT property not set after completing a recurring item")
	}
}

func TestCompletingRecurringItemShowsUpcomingInAgenda(t *testing.T) {
	now := orgdate.TruncateToDate(time.Now())
	overdue := now.AddDate(0, 0, -3) // advancing by +1w lands 4 days in the future
	orgText := fmt.Sprintf("* TODO Weekly standup\n  SCHEDULED: <%s +1w>\n", ts(overdue))
	ws := agendaFixture(t, orgText)
	m := New(ws)
	m.cursor = findRow(t, m, "Weekly standup")

	// Jump straight TODO -> DONE via the status picker, rather than
	// stepping through SOMEDAY on the way: SOMEDAY is excluded from the
	// agenda entirely (it means "not committed to a date"), and since a
	// repeating item's keyword reverts to whatever it was right before
	// the done-class transition, going through SOMEDAY first would leave
	// this item invisible in the agenda for an unrelated reason.
	m = setStatus(m, "d") // shortcut for DONE

	m.switchToView(agendaView)
	var section, currentSection string
	for _, r := range m.rows {
		if r.section != "" {
			currentSection = r.section
		}
		if r.headline != nil && r.headline.Title == "Weekly standup" {
			section = currentSection
		}
	}
	if section != "Upcoming" {
		t.Errorf("agenda section for the completed recurring item = %q, want %q", section, "Upcoming")
	}
}

func TestCompletingItemWithOnlyDeadlineRepeatingAdvancesJustThat(t *testing.T) {
	now := orgdate.TruncateToDate(time.Now())
	overdue := now.AddDate(0, 0, -7)
	orgText := fmt.Sprintf("* TODO Pay rent\n  DEADLINE: <%s +1m>\n", ts(overdue))
	ws := agendaFixture(t, orgText)
	m := New(ws)
	m.cursor = findRow(t, m, "Pay rent")
	h := m.currentHeadline()

	for _, shortcut := range []string{"n", "w", "s", "d"} {
		m = setStatus(m, shortcut)
	}

	if h.Keyword != "SOMEDAY" {
		t.Errorf("keyword = %q, want unchanged", h.Keyword)
	}
	if h.Deadline == nil {
		t.Fatal("Deadline = nil, want an advanced timestamp")
	}
	if want, got := overdue.AddDate(0, 1, 0), rawOccurrenceDate(t, h.Deadline); !got.Equal(want) {
		t.Errorf("Deadline advanced to %v, want %v", got, want)
	}
}

func TestCompletingNonRepeatingItemStillClosesNormally(t *testing.T) {
	now := orgdate.TruncateToDate(time.Now())
	orgText := fmt.Sprintf("* TODO One-off task\n  SCHEDULED: <%s>\n", ts(now))
	ws := agendaFixture(t, orgText)
	m := New(ws)
	m.cursor = findRow(t, m, "One-off task")
	h := m.currentHeadline()

	for _, shortcut := range []string{"n", "w", "s", "d"} {
		m = setStatus(m, shortcut)
	}

	if h.Keyword != "DONE" {
		t.Errorf("keyword = %q, want DONE (no repeater, ordinary completion)", h.Keyword)
	}
	if h.Closed == nil {
		t.Error("Closed = nil, want a CLOSED stamp for an ordinary (non-repeating) completion")
	}
	if _, exists := h.Properties["LAST_REPEAT"]; exists {
		t.Error("LAST_REPEAT set on a non-repeating item's completion")
	}
}

func TestUndoRestoresPreCompletionScheduleAndKeyword(t *testing.T) {
	now := orgdate.TruncateToDate(time.Now())
	overdue := now.AddDate(0, 0, -7)
	orgText := fmt.Sprintf("* TODO Weekly standup\n  SCHEDULED: <%s +1w>\n", ts(overdue))
	ws := agendaFixture(t, orgText)
	m := New(ws)
	m.cursor = findRow(t, m, "Weekly standup")
	h := m.currentHeadline()
	origRaw := h.Scheduled.Raw

	for _, shortcut := range []string{"n", "w", "s", "d"} {
		m = setStatus(m, shortcut)
	}
	advancedRaw := h.Scheduled.Raw
	if advancedRaw == origRaw {
		t.Fatal("fixture assumption broken: Scheduled did not change")
	}

	m = sendKey(m, "u")
	if h.Scheduled.Raw != origRaw {
		t.Errorf("Scheduled after undo = %q, want the original %q", h.Scheduled.Raw, origRaw)
	}
	if h.Keyword != "SOMEDAY" {
		t.Errorf("keyword after undo = %q, want SOMEDAY (the pre-completion state)", h.Keyword)
	}
	if _, exists := h.Properties["LAST_REPEAT"]; exists {
		t.Error("LAST_REPEAT still present after undo")
	}

	m = sendKey(m, "ctrl+r")
	if h.Scheduled.Raw != advancedRaw {
		t.Errorf("Scheduled after redo = %q, want the advanced %q", h.Scheduled.Raw, advancedRaw)
	}
	if _, exists := h.Properties["LAST_REPEAT"]; !exists {
		t.Error("LAST_REPEAT missing after redo")
	}
}

func TestUndoRestoresPreexistingLastRepeatValue(t *testing.T) {
	now := orgdate.TruncateToDate(time.Now())
	overdue := now.AddDate(0, 0, -7)
	orgText := fmt.Sprintf(`* TODO Weekly standup
  SCHEDULED: <%s +1w>
  :PROPERTIES:
  :LAST_REPEAT: [2020-01-01 Wed 08:00]
  :END:
`, ts(overdue))
	ws := agendaFixture(t, orgText)
	m := New(ws)
	m.cursor = findRow(t, m, "Weekly standup")
	h := m.currentHeadline()

	for _, shortcut := range []string{"n", "w", "s", "d"} {
		m = setStatus(m, shortcut)
	}
	if h.Properties["LAST_REPEAT"] == "[2020-01-01 Wed 08:00]" {
		t.Fatal("fixture assumption broken: LAST_REPEAT did not change")
	}

	m = sendKey(m, "u")
	if got := h.Properties["LAST_REPEAT"]; got != "[2020-01-01 Wed 08:00]" {
		t.Errorf("LAST_REPEAT after undo = %q, want the original value restored", got)
	}
}

func TestCancellingRecurringItemAlsoAdvances(t *testing.T) {
	// org-mode's repeat-on-completion logic keys off "done-class", not
	// literally the DONE keyword — jumping straight to CANCELLED (orgtd's
	// other done-class state, via the R picker) triggers the same
	// advance-and-revert as DONE would.
	now := orgdate.TruncateToDate(time.Now())
	overdue := now.AddDate(0, 0, -7)
	orgText := fmt.Sprintf("* TODO Weekly standup\n  SCHEDULED: <%s +1w>\n", ts(overdue))
	ws := agendaFixture(t, orgText)
	m := New(ws)
	m.cursor = findRow(t, m, "Weekly standup")
	h := m.currentHeadline()

	m = setStatus(m, "c") // shortcut for CANCELLED

	if h.Keyword != "TODO" {
		t.Errorf("keyword = %q, want unchanged (TODO) — CANCELLED should never actually stick on a repeating item", h.Keyword)
	}
	if h.Closed != nil {
		t.Errorf("Closed = %v, want nil", h.Closed)
	}
	if want, got := overdue.AddDate(0, 0, 7), rawOccurrenceDate(t, h.Scheduled); !got.Equal(want) {
		t.Errorf("Scheduled advanced to %v, want %v", got, want)
	}
}

func TestRepeatedCompletionKeepsReAdvancingSchedule(t *testing.T) {
	// A quirk inherited faithfully from org-mode: since the keyword never
	// actually changes on a repeating item, picking a done-class state
	// again later just re-triggers the repeat-advance again rather than
	// having any further effect — matching org's own well-known behavior
	// here.
	now := orgdate.TruncateToDate(time.Now())
	overdue := now.AddDate(0, 0, -7)
	orgText := fmt.Sprintf("* TODO Weekly standup\n  SCHEDULED: <%s +1w>\n", ts(overdue))
	ws := agendaFixture(t, orgText)
	m := New(ws)
	m.cursor = findRow(t, m, "Weekly standup")
	h := m.currentHeadline()

	m = setStatus(m, "d") // [advance]
	m = setStatus(m, "d") // [advance again]

	if h.Keyword != "TODO" {
		t.Errorf("keyword = %q, want TODO (never actually left it, being a repeating item)", h.Keyword)
	}
	if want, got := overdue.AddDate(0, 0, 14), rawOccurrenceDate(t, h.Scheduled); !got.Equal(want) {
		t.Errorf("Scheduled = %v, want two weekly advances applied (%v)", got, want)
	}
}
