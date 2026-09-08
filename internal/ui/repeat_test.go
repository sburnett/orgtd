package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sburnett/orgtd/internal/org"
)

// rawOccurrenceDate extracts the literal date a Timestamp's Raw text
// names, ignoring any repeater — unlike parseTimestampDate, which rolls
// a repeating timestamp forward/floors it relative to "today" for
// agenda display. Tests that want to check advanceRepeatingTimestamp's
// output directly (a Timestamp that itself still carries a repeater
// cookie) need this instead, since running it back through
// parseTimestampDate would apply that unrelated floor logic on top and
// obscure what advanceRepeatingTimestamp actually computed.
func rawOccurrenceDate(t *testing.T, ts *org.Timestamp) time.Time {
	t.Helper()
	raw := repeaterRe.ReplaceAllString(ts.Raw, "")
	raw = strings.Join(strings.Fields(warningRe.ReplaceAllString(raw, "")), " ")
	for _, layout := range dateInputLayouts {
		if parsed, err := time.ParseInLocation(layout, raw, time.Local); err == nil {
			return parsed
		}
	}
	t.Fatalf("rawOccurrenceDate: could not parse %q", ts.Raw)
	return time.Time{}
}

func TestAdvanceRepeatingTimestampSimpleMarkStepsOnceFromOldDate(t *testing.T) {
	now := truncateToDate(time.Now())
	// Very overdue (5 weeks late): "+" is the naive mark — it advances
	// exactly one interval from the *old* date, however overdue that
	// still leaves it, matching org-mode's own "+"  semantics.
	old := now.AddDate(0, 0, -35)
	ts := &org.Timestamp{Active: true, Raw: ts(old) + " +1w"}

	got, ok := advanceRepeatingTimestamp(ts, time.Now())
	if !ok {
		t.Fatal("advanceRepeatingTimestamp: ok = false")
	}
	want := old.AddDate(0, 0, 7)
	if gotDate := rawOccurrenceDate(t, got); !gotDate.Equal(want) {
		t.Errorf("advanced date = %v, want %v (still overdue — raw=%q)", gotDate, want, got.Raw)
	}
	if !strings.HasSuffix(got.Raw, "+1w") {
		t.Errorf("raw = %q, want the repeater cookie preserved", got.Raw)
	}
	if got.Active != ts.Active {
		t.Errorf("Active = %v, want unchanged (%v)", got.Active, ts.Active)
	}
}

func TestAdvanceRepeatingTimestampCatchUpMarkNeverLandsInThePast(t *testing.T) {
	now := truncateToDate(time.Now())
	old := now.AddDate(0, 0, -35) // 5 weeks late
	ts := &org.Timestamp{Active: true, Raw: fmt.Sprintf("%s ++1w", ts(old))}

	got, ok := advanceRepeatingTimestamp(ts, time.Now())
	if !ok {
		t.Fatal("advanceRepeatingTimestamp: ok = false")
	}
	gotDate := rawOccurrenceDate(t, got)
	if gotDate.Before(now) {
		t.Errorf("++ mark left the date in the past: %v (today=%v)", gotDate, now)
	}
	// old + 5*7 = old+35 = now, which is on-or-after today, so ++ should
	// land exactly on today (not skip an extra week further).
	if !gotDate.Equal(now) {
		t.Errorf("advanced date = %v, want exactly today %v", gotDate, now)
	}
}

func TestAdvanceRepeatingTimestampFromNowMarkIgnoresOldDate(t *testing.T) {
	now := truncateToDate(time.Now())
	old := now.AddDate(0, 0, -100) // very stale
	ts := &org.Timestamp{Active: true, Raw: fmt.Sprintf("%s .+1w", ts(old))}

	got, ok := advanceRepeatingTimestamp(ts, time.Now())
	if !ok {
		t.Fatal("advanceRepeatingTimestamp: ok = false")
	}
	want := now.AddDate(0, 0, 7)
	if gotDate := rawOccurrenceDate(t, got); !gotDate.Equal(want) {
		t.Errorf("advanced date = %v, want one week from today %v (old date should be ignored)", gotDate, want)
	}
}

func TestAdvanceRepeatingTimestampPreservesTimeOfDay(t *testing.T) {
	now := truncateToDate(time.Now())
	old := now.AddDate(0, 0, -7)
	raw := fmt.Sprintf("%s 09:30 +1w", ts(old))
	tsVal := &org.Timestamp{Active: true, Raw: raw}

	got, ok := advanceRepeatingTimestamp(tsVal, time.Now())
	if !ok {
		t.Fatal("advanceRepeatingTimestamp: ok = false")
	}
	if !strings.Contains(got.Raw, "09:30") {
		t.Errorf("raw = %q, want the original time of day (09:30) preserved", got.Raw)
	}
}

func TestAdvanceRepeatingTimestampPreservesWarningCookie(t *testing.T) {
	now := truncateToDate(time.Now())
	old := now.AddDate(0, 0, -7)
	raw := ts(old) + " +1w -3d"
	tsVal := &org.Timestamp{Active: true, Raw: raw}

	got, ok := advanceRepeatingTimestamp(tsVal, time.Now())
	if !ok {
		t.Fatal("advanceRepeatingTimestamp: ok = false")
	}
	if !strings.HasSuffix(got.Raw, "-3d") {
		t.Errorf("raw = %q, want the warning-period cookie (-3d) preserved", got.Raw)
	}
	if !strings.Contains(got.Raw, "+1w") {
		t.Errorf("raw = %q, want the repeater cookie (+1w) preserved", got.Raw)
	}
}

func TestAdvanceRepeatingTimestampNonRepeatingReturnsUnchanged(t *testing.T) {
	now := truncateToDate(time.Now())
	orig := &org.Timestamp{Active: true, Raw: ts(now)}
	got, ok := advanceRepeatingTimestamp(orig, time.Now())
	if ok {
		t.Error("advanceRepeatingTimestamp on a non-repeating timestamp: ok = true, want false")
	}
	if got != orig {
		t.Errorf("got = %v, want the original pointer unchanged", got)
	}
}

func TestAdvanceRepeatingTimestampNilReturnsUnchanged(t *testing.T) {
	got, ok := advanceRepeatingTimestamp(nil, time.Now())
	if ok || got != nil {
		t.Errorf("advanceRepeatingTimestamp(nil) = (%v, %v), want (nil, false)", got, ok)
	}
}

func TestCompletingRecurringScheduledItemAdvancesInsteadOfClosing(t *testing.T) {
	now := truncateToDate(time.Now())
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
	for i := 0; i < 4; i++ {
		m = sendKey(m, "r")
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
	now := truncateToDate(time.Now())
	overdue := now.AddDate(0, 0, -3) // advancing by +1w lands 4 days in the future
	orgText := fmt.Sprintf("* TODO Weekly standup\n  SCHEDULED: <%s +1w>\n", ts(overdue))
	ws := agendaFixture(t, orgText)
	m := New(ws)
	m.cursor = findRow(t, m, "Weekly standup")

	// Jump straight TODO -> DONE via the R picker, rather than r-cycling
	// through SOMEDAY on the way: SOMEDAY is excluded from the agenda
	// entirely (it means "not committed to a date"), and since a
	// repeating item's keyword reverts to whatever it was right before
	// the done-class transition, cycling through SOMEDAY first would
	// leave this item invisible in the agenda for an unrelated reason.
	m = sendKey(m, "R")
	m = sendKey(m, "d") // shortcut for DONE

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
	now := truncateToDate(time.Now())
	overdue := now.AddDate(0, 0, -7)
	orgText := fmt.Sprintf("* TODO Pay rent\n  DEADLINE: <%s +1m>\n", ts(overdue))
	ws := agendaFixture(t, orgText)
	m := New(ws)
	m.cursor = findRow(t, m, "Pay rent")
	h := m.currentHeadline()

	for i := 0; i < 4; i++ {
		m = sendKey(m, "r")
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
	now := truncateToDate(time.Now())
	orgText := fmt.Sprintf("* TODO One-off task\n  SCHEDULED: <%s>\n", ts(now))
	ws := agendaFixture(t, orgText)
	m := New(ws)
	m.cursor = findRow(t, m, "One-off task")
	h := m.currentHeadline()

	for i := 0; i < 4; i++ {
		m = sendKey(m, "r")
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
	now := truncateToDate(time.Now())
	overdue := now.AddDate(0, 0, -7)
	orgText := fmt.Sprintf("* TODO Weekly standup\n  SCHEDULED: <%s +1w>\n", ts(overdue))
	ws := agendaFixture(t, orgText)
	m := New(ws)
	m.cursor = findRow(t, m, "Weekly standup")
	h := m.currentHeadline()
	origRaw := h.Scheduled.Raw

	for i := 0; i < 4; i++ {
		m = sendKey(m, "r")
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
	now := truncateToDate(time.Now())
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

	for i := 0; i < 4; i++ {
		m = sendKey(m, "r")
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
	now := truncateToDate(time.Now())
	overdue := now.AddDate(0, 0, -7)
	orgText := fmt.Sprintf("* TODO Weekly standup\n  SCHEDULED: <%s +1w>\n", ts(overdue))
	ws := agendaFixture(t, orgText)
	m := New(ws)
	m.cursor = findRow(t, m, "Weekly standup")
	h := m.currentHeadline()

	m = sendKey(m, "R")
	m = sendKey(m, "c") // shortcut for CANCELLED

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

func TestRepeatedCycleThroughDoneKeepsReAdvancingSchedule(t *testing.T) {
	// A quirk inherited faithfully from org-mode: since the keyword never
	// actually advances past its pre-completion state, cycling further
	// with r just re-triggers the repeat-advance again rather than
	// reaching CANCELLED — matching org's own well-known behavior here.
	now := truncateToDate(time.Now())
	overdue := now.AddDate(0, 0, -7)
	orgText := fmt.Sprintf("* TODO Weekly standup\n  SCHEDULED: <%s +1w>\n", ts(overdue))
	ws := agendaFixture(t, orgText)
	m := New(ws)
	m.cursor = findRow(t, m, "Weekly standup")
	h := m.currentHeadline()

	// TODO -> NEXT -> WAITING -> SOMEDAY -> [advance] -> [advance again]
	for i := 0; i < 5; i++ {
		m = sendKey(m, "r")
	}

	if h.Keyword != "SOMEDAY" {
		t.Errorf("keyword = %q, want SOMEDAY (still stuck right before the done-class state)", h.Keyword)
	}
	if want, got := overdue.AddDate(0, 0, 14), rawOccurrenceDate(t, h.Scheduled); !got.Equal(want) {
		t.Errorf("Scheduled = %v, want two weekly advances applied (%v)", got, want)
	}
}
