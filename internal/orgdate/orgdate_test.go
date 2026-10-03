package orgdate

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sburnett/orgtd/internal/org"
)

// ts formats t (date only) the same way orgtd itself writes a
// SCHEDULED/DEADLINE timestamp, for building fixtures relative to the
// real "now".
func ts(t time.Time) string {
	return t.Format("2006-01-02 Mon")
}

func TestParseTimestampDateAcceptsKnownFormats(t *testing.T) {
	now := TruncateToDate(time.Now())
	cases := []*org.Timestamp{
		{Raw: ts(now)},
		{Raw: now.Format("2006-01-02 Mon 15:04")},
		{Raw: now.Format("2006-01-02")}, // weekday-less fallback
	}
	for _, c := range cases {
		got, missed, ok := ParseTimestampDate(c, now)
		if !ok {
			t.Errorf("ParseTimestampDate(%q) did not match", c.Raw)
			continue
		}
		if !got.Equal(now) {
			t.Errorf("ParseTimestampDate(%q) = %v, want %v", c.Raw, got, now)
		}
		if missed != 0 {
			t.Errorf("ParseTimestampDate(%q) missed = %d, want 0 (non-repeating)", c.Raw, missed)
		}
	}
}

func TestParseTimestampDateRejectsGarbageAndNil(t *testing.T) {
	now := TruncateToDate(time.Now())
	if _, _, ok := ParseTimestampDate(nil, now); ok {
		t.Errorf("ParseTimestampDate(nil) matched")
	}
	if _, _, ok := ParseTimestampDate(&org.Timestamp{Raw: "not a date"}, now); ok {
		t.Errorf("ParseTimestampDate(garbage) matched")
	}
}

// TestParseTimestampDateRepeaterStaysOverdueUntilCaughtUp verifies the
// org-mode-matching behavior: a repeating timestamp's date only ever
// advances when the item is completed, so a stale date (one the user
// skipped without marking done) is reported at its most recent due
// occurrence — still in the past — rather than being rolled forward past
// today and hidden. missed reports how many earlier occurrences already
// elapsed on top of that.
func TestParseTimestampDateRepeaterStaysOverdueUntilCaughtUp(t *testing.T) {
	now := TruncateToDate(time.Now())

	// 17 days ago, weekly: occurrences at -17, -10, -3 (all <= today), and
	// +4 (> today, not reached) — so the current occurrence is 3 days ago,
	// with 2 earlier occurrences (-17, -10) already elapsed on top of it.
	weekly := ts(now.AddDate(0, 0, -17)) + " +1w"
	wantDate := now.AddDate(0, 0, -3)
	if got, missed, ok := ParseTimestampDate(&org.Timestamp{Raw: weekly}, now); !ok || !got.Equal(wantDate) || missed != 2 {
		t.Errorf("ParseTimestampDate(%q) = %v, missed=%d, ok=%v, want %v missed=2", weekly, got, missed, ok, wantDate)
	}

	// Scheduled yesterday, daily: today is itself a valid occurrence (one
	// interval past base), so the current occurrence is today — Due
	// Today, not Overdue — with yesterday's skipped occurrence counted
	// as 1 missed.
	daily := ts(now.AddDate(0, 0, -1)) + " +1d"
	if got, missed, ok := ParseTimestampDate(&org.Timestamp{Raw: daily}, now); !ok || !got.Equal(now) || missed != 1 {
		t.Errorf("ParseTimestampDate(%q) = %v, missed=%d, ok=%v, want %v missed=1", daily, got, missed, ok, now)
	}

	// Monthly/yearly land on an irregular day count (calendar month/year
	// lengths vary), so just check the result stays on-or-before today
	// (never rolled into the future) with a positive missed count.
	for _, c := range []struct{ name, raw string }{
		{"monthly, well overdue", ts(now.AddDate(0, -2, -3)) + " +1m"},
		{"yearly, overdue", ts(now.AddDate(-1, 0, -1)) + " +1y"},
	} {
		got, missed, ok := ParseTimestampDate(&org.Timestamp{Raw: c.raw}, now)
		if !ok {
			t.Errorf("%s: ParseTimestampDate(%q) did not match", c.name, c.raw)
			continue
		}
		if got.After(now) {
			t.Errorf("%s: ParseTimestampDate(%q) = %v, rolled past today %v", c.name, c.raw, got, now)
		}
		if missed < 1 {
			t.Errorf("%s: ParseTimestampDate(%q) missed = %d, want at least 1", c.name, c.raw, missed)
		}
	}
}

func TestParseTimestampDateRepeaterLeavesFutureDateUnchanged(t *testing.T) {
	now := TruncateToDate(time.Now())
	future := now.AddDate(0, 0, 5)
	raw := ts(future) + " +1w"
	got, missed, ok := ParseTimestampDate(&org.Timestamp{Raw: raw}, now)
	if !ok {
		t.Fatalf("ParseTimestampDate(%q) did not match", raw)
	}
	if !got.Equal(future) {
		t.Errorf("ParseTimestampDate(%q) = %v, want unchanged future date %v", raw, got, future)
	}
	if missed != 0 {
		t.Errorf("ParseTimestampDate(%q) missed = %d, want 0 (not due yet)", raw, missed)
	}
}

func TestParseTimestampDateRepeaterExactlyTodayUnchanged(t *testing.T) {
	now := TruncateToDate(time.Now())
	raw := ts(now) + " +1w"
	got, missed, ok := ParseTimestampDate(&org.Timestamp{Raw: raw}, now)
	if !ok {
		t.Fatalf("ParseTimestampDate(%q) did not match", raw)
	}
	if !got.Equal(now) {
		t.Errorf("ParseTimestampDate(%q) = %v, want today %v unchanged", raw, got, now)
	}
	if missed != 0 {
		t.Errorf("ParseTimestampDate(%q) missed = %d, want 0", raw, missed)
	}
}

func TestParseTimestampDateStripsWarningPeriodCookie(t *testing.T) {
	now := TruncateToDate(time.Now())
	raw := ts(now) + " +1w -3d"
	got, _, ok := ParseTimestampDate(&org.Timestamp{Raw: raw}, now)
	if !ok {
		t.Fatalf("ParseTimestampDate(%q) did not match", raw)
	}
	if !got.Equal(now) {
		t.Errorf("ParseTimestampDate(%q) = %v, want %v", raw, got, now)
	}
}

func TestRepeaterCookie(t *testing.T) {
	if got := RepeaterCookie(&org.Timestamp{Raw: "2026-08-10 Mon +1w"}); got != "+1w" {
		t.Errorf("RepeaterCookie = %q, want %q", got, "+1w")
	}
	if got := RepeaterCookie(&org.Timestamp{Raw: "2026-08-10 Mon"}); got != "" {
		t.Errorf("RepeaterCookie = %q, want empty", got)
	}
	if got := RepeaterCookie(nil); got != "" {
		t.Errorf("RepeaterCookie(nil) = %q, want empty", got)
	}
}

// rawOccurrenceDate extracts the literal date a Timestamp's Raw text
// names, ignoring any repeater — unlike ParseTimestampDate, which rolls
// a repeating timestamp forward/floors it relative to "today" for
// agenda display. Tests that want to check AdvanceRepeating's
// output directly (a Timestamp that itself still carries a repeater
// cookie) need this instead, since running it back through
// ParseTimestampDate would apply that unrelated floor logic on top and
// obscure what AdvanceRepeating actually computed.
func rawOccurrenceDate(t *testing.T, ts *org.Timestamp) time.Time {
	t.Helper()
	raw := repeaterRe.ReplaceAllString(ts.Raw, "")
	raw = strings.Join(strings.Fields(warningRe.ReplaceAllString(raw, "")), " ")
	for _, layout := range Layouts {
		if parsed, err := time.ParseInLocation(layout, raw, time.Local); err == nil {
			return parsed
		}
	}
	t.Fatalf("rawOccurrenceDate: could not parse %q", ts.Raw)
	return time.Time{}
}

func TestAdvanceRepeatingTimestampSimpleMarkStepsOnceFromOldDate(t *testing.T) {
	now := TruncateToDate(time.Now())
	// Very overdue (5 weeks late): "+" is the naive mark — it advances
	// exactly one interval from the *old* date, however overdue that
	// still leaves it, matching org-mode's own "+"  semantics.
	old := now.AddDate(0, 0, -35)
	ts := &org.Timestamp{Active: true, Raw: ts(old) + " +1w"}

	got, ok := AdvanceRepeating(ts, time.Now())
	if !ok {
		t.Fatal("AdvanceRepeating: ok = false")
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
	now := TruncateToDate(time.Now())
	old := now.AddDate(0, 0, -35) // 5 weeks late
	ts := &org.Timestamp{Active: true, Raw: fmt.Sprintf("%s ++1w", ts(old))}

	got, ok := AdvanceRepeating(ts, time.Now())
	if !ok {
		t.Fatal("AdvanceRepeating: ok = false")
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
	now := TruncateToDate(time.Now())
	old := now.AddDate(0, 0, -100) // very stale
	ts := &org.Timestamp{Active: true, Raw: fmt.Sprintf("%s .+1w", ts(old))}

	got, ok := AdvanceRepeating(ts, time.Now())
	if !ok {
		t.Fatal("AdvanceRepeating: ok = false")
	}
	want := now.AddDate(0, 0, 7)
	if gotDate := rawOccurrenceDate(t, got); !gotDate.Equal(want) {
		t.Errorf("advanced date = %v, want one week from today %v (old date should be ignored)", gotDate, want)
	}
}

func TestAdvanceRepeatingTimestampPreservesTimeOfDay(t *testing.T) {
	now := TruncateToDate(time.Now())
	old := now.AddDate(0, 0, -7)
	raw := fmt.Sprintf("%s 09:30 +1w", ts(old))
	tsVal := &org.Timestamp{Active: true, Raw: raw}

	got, ok := AdvanceRepeating(tsVal, time.Now())
	if !ok {
		t.Fatal("AdvanceRepeating: ok = false")
	}
	if !strings.Contains(got.Raw, "09:30") {
		t.Errorf("raw = %q, want the original time of day (09:30) preserved", got.Raw)
	}
}

func TestAdvanceRepeatingTimestampPreservesWarningCookie(t *testing.T) {
	now := TruncateToDate(time.Now())
	old := now.AddDate(0, 0, -7)
	raw := ts(old) + " +1w -3d"
	tsVal := &org.Timestamp{Active: true, Raw: raw}

	got, ok := AdvanceRepeating(tsVal, time.Now())
	if !ok {
		t.Fatal("AdvanceRepeating: ok = false")
	}
	if !strings.HasSuffix(got.Raw, "-3d") {
		t.Errorf("raw = %q, want the warning-period cookie (-3d) preserved", got.Raw)
	}
	if !strings.Contains(got.Raw, "+1w") {
		t.Errorf("raw = %q, want the repeater cookie (+1w) preserved", got.Raw)
	}
}

func TestAdvanceRepeatingTimestampNonRepeatingReturnsUnchanged(t *testing.T) {
	now := TruncateToDate(time.Now())
	orig := &org.Timestamp{Active: true, Raw: ts(now)}
	got, ok := AdvanceRepeating(orig, time.Now())
	if ok {
		t.Error("AdvanceRepeating on a non-repeating timestamp: ok = true, want false")
	}
	if got != orig {
		t.Errorf("got = %v, want the original pointer unchanged", got)
	}
}

func TestAdvanceRepeatingTimestampNilReturnsUnchanged(t *testing.T) {
	got, ok := AdvanceRepeating(nil, time.Now())
	if ok || got != nil {
		t.Errorf("AdvanceRepeating(nil) = (%v, %v), want (nil, false)", got, ok)
	}
}

func TestFuzzyDateRollsSameMonthDayForwardOnlyPastToday(t *testing.T) {
	today := time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC) // a Monday

	cases := []struct {
		input string
		want  time.Time
	}{
		// "sep 30" hasn't happened yet this year: stays in 2026, not
		// pushed a year out just because today is also in September
		// (see fuzzyDate's doc comment — that was the actual bug in the
		// date library this replaced).
		{"sep 30", time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)},
		// "sep 1" already passed this year: rolls forward to 2027.
		{"sep 1", time.Date(2027, 9, 1, 0, 0, 0, 0, time.UTC)},
		// An explicit year (here, D/M/Y — the one when.EN date shape
		// that actually carries a year) is trusted as-is, even in the
		// past — never rolled forward like the year-less cases above.
		{"31/3/2014", time.Date(2014, 3, 31, 0, 0, 0, 0, time.UTC)},
		{"thu", time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC)},
	}
	for _, c := range cases {
		got, ok := fuzzyDate(c.input, today)
		if !ok {
			t.Errorf("fuzzyDate(%q) did not match", c.input)
			continue
		}
		if !got.Equal(c.want) {
			t.Errorf("fuzzyDate(%q) = %v, want %v", c.input, got, c.want)
		}
	}
}

func TestFuzzyDateRejectsPartialMatchesAndGarbage(t *testing.T) {
	today := time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC)
	for _, input := range []string{"not-a-date", "last thursday in august, 202", "", "3d"} {
		if _, ok := fuzzyDate(input, today); ok {
			t.Errorf("fuzzyDate(%q) unexpectedly matched", input)
		}
	}
}

func TestParseRelativeOffsetShorthandAndSpelledOut(t *testing.T) {
	base := time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC) // a Sunday

	cases := []struct {
		input string
		want  time.Time
	}{
		{"3d", base.AddDate(0, 0, 3)},
		{"3 days", base.AddDate(0, 0, 3)},
		{"2w", base.AddDate(0, 0, 14)},
		{"2 weeks", base.AddDate(0, 0, 14)},
		{"1m", base.AddDate(0, 1, 0)},
		{"1 month", base.AddDate(0, 1, 0)},
		{"1y", base.AddDate(1, 0, 0)},
		{"1 year", base.AddDate(1, 0, 0)},
		{"-5d", base.AddDate(0, 0, -5)},
		{"+5d", base.AddDate(0, 0, 5)},
		{"3D", base.AddDate(0, 0, 3)}, // case-insensitive
	}
	for _, c := range cases {
		got, ok := parseRelativeOffset(c.input, base)
		if !ok {
			t.Errorf("parseRelativeOffset(%q) did not match", c.input)
			continue
		}
		if !got.Equal(c.want) {
			t.Errorf("parseRelativeOffset(%q) = %v, want %v", c.input, got, c.want)
		}
	}
}

func TestParseRelativeOffsetRejectsNonMatches(t *testing.T) {
	base := time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)
	for _, input := range []string{"next tuesday", "2026-12-25", "", "abc", "3 hours", "d3"} {
		if _, ok := parseRelativeOffset(input, base); ok {
			t.Errorf("parseRelativeOffset(%q) unexpectedly matched", input)
		}
	}
}

func TestCreatedTimeParsesTheCreatedProperty(t *testing.T) {
	h := &org.Headline{}
	h.SetProperty("CREATED", "[2026-09-24 Thu 14:32]")
	got, ok := CreatedTime(h)
	want := time.Date(2026, 9, 24, 14, 32, 0, 0, time.Local)
	if !ok || !got.Equal(want) {
		t.Errorf("CreatedTime = %v, %v; want %v", got, ok, want)
	}
}

func TestCreatedTimeIsNotOKWhenMissingOrUnreadable(t *testing.T) {
	if _, ok := CreatedTime(&org.Headline{}); ok {
		t.Error("CreatedTime ok = true with no CREATED property")
	}
	h := &org.Headline{}
	h.SetProperty("CREATED", "[yesterday-ish]")
	if _, ok := CreatedTime(h); ok {
		t.Error("CreatedTime ok = true for an unparseable CREATED")
	}
}
