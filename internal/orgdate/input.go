// Package orgdate parses and computes with the dates orgtd reads and
// writes: the date strings typed into the deadline prompt (exact dates,
// "3d"-style offsets, and fuzzy phrases like "next tuesday"), the raw
// text of org timestamps including their repeater ("+1w", "++1w", ".+1w")
// and warning ("-3d") cookies, and the arithmetic for completing a
// repeating item. Everything here is pure and has no UI dependency;
// functions that depend on the date take "now" or "today" explicitly,
// except typed-input parsing (ParseDeadlineInput), which anchors relative
// phrases to the local current date.
package orgdate

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/olebedev/when"

	"github.com/sburnett/orgtd/internal/org"
)

// Layouts are the formats accepted when typing a date, tried in
// order. A weekday name may or may not be present (it's not required,
// and is regenerated from the actual date on output regardless of what
// was typed, so a stale one left over from editing an existing date
// doesn't matter).
var Layouts = []string{"2006-01-02 Mon 15:04", "2006-01-02 Mon", "2006-01-02 15:04", "2006-01-02"}

// ParseFlexible tries each of Layouts against input,
// reporting whether the matched layout included a time of day.
func ParseFlexible(input string) (t time.Time, hasTime bool, err error) {
	for _, layout := range Layouts {
		if t, err = time.ParseInLocation(layout, input, time.Local); err == nil {
			return t, strings.Contains(layout, "15:04"), nil
		}
	}
	return time.Time{}, false, fmt.Errorf("invalid date %q (want YYYY-MM-DD, optionally with HH:MM)", input)
}

// relativeOffsetRe matches a compact or spelled-out relative offset like
// "3d", "-2 weeks", "1 month", "2y". Deliberately excludes "min"/"hour":
// deadlines here are date-grained, not time-grained.
var relativeOffsetRe = regexp.MustCompile(`(?i)^([+-]?\d+)\s*(d|days?|w|weeks?|m|months?|y|years?)$`)

// parseRelativeOffset resolves a compact/spelled-out relative offset
// against base, always at day granularity (no time of day). ok is false
// if input doesn't match this shape at all.
func parseRelativeOffset(input string, base time.Time) (t time.Time, ok bool) {
	match := relativeOffsetRe.FindStringSubmatch(strings.TrimSpace(input))
	if match == nil {
		return time.Time{}, false
	}
	n, err := strconv.Atoi(match[1])
	if err != nil {
		return time.Time{}, false
	}
	switch unicode.ToLower(rune(match[2][0])) {
	case 'd':
		return base.AddDate(0, 0, n), true
	case 'w':
		return base.AddDate(0, 0, n*7), true
	case 'm':
		return base.AddDate(0, n, 0), true
	case 'y':
		return base.AddDate(n, 0, 0), true
	}
	return time.Time{}, false
}

// TruncateToDate drops t's time-of-day component.
func TruncateToDate(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// resolveDeadlineDate parses input as, in order: an exact date (with
// optional time of day, per ParseFlexible); a compact or
// spelled-out relative offset ("3d", "2 weeks", "-1y"); or a fuzzy
// natural-language phrase ("next tuesday", "tomorrow", "sep 30", "thu"),
// via fuzzyDate/when.EN. Only the first form can produce a time of day —
// the other two always resolve to a plain date, since "in 3 days" or
// "sep 30" don't imply a specific hour.
func resolveDeadlineDate(input string) (t time.Time, hasTime bool, err error) {
	input = strings.TrimSpace(input)

	if t, hasTime, err := ParseFlexible(input); err == nil {
		return t, hasTime, nil
	}

	today := TruncateToDate(time.Now())

	if t, ok := parseRelativeOffset(input, today); ok {
		return t, false, nil
	}

	if t, ok := fuzzyDate(input, today); ok {
		return t, false, nil
	}

	return time.Time{}, false, fmt.Errorf(`invalid date %q (try "2026-12-25", "3d", "2 weeks", "sep 30", or "next tuesday")`, input)
}

// explicitYearRe matches a bare 4-digit year (e.g. "2027") anywhere in a
// date input — used by fuzzyDate to tell "sep 30" (no year stated, so
// biased toward the nearest upcoming occurrence) apart from an input
// shape that does carry one, like "31/3/2014" (the one when.EN date
// rule that captures a year at all): that result is trusted as-is even
// when it lands in the past, rather than rolled forward a year.
var explicitYearRe = regexp.MustCompile(`\b(?:19|20)\d\d\b`)

// fuzzyDate resolves input (already trimmed) via when.EN — the English
// rule set from github.com/olebedev/when, covering weekday names (full
// or abbreviated — "thursday"/"thu"), month/day names ("sep 30",
// "december 20"), and relative phrases ("tomorrow", "next week"). ok is
// false if nothing in the rule set matches at all, or if a match only
// covers part of input (e.g. "last thursday in august, 202" matches
// "last thursday in august" and leaves ", 202" dangling) — a partial
// match is treated the same as no match at all, rather than silently
// resolving to whatever fragment did parse.
//
// when.EN has no "assume the future" option (unlike the date library
// this replaced, whose equivalent option was actually buggy for a
// same-month date like "sep 30": it compared only the month number
// against today's, so a day later in the current month was wrongly
// pushed a full year out). Emulating that intent correctly instead:
// once a bare month/day phrase resolves to a date before today, and
// input never named an explicit year (see explicitYearRe), roll it
// forward exactly one year — "sep 30" typed on Sep 14 means this year's
// Sep 30, but typed on Oct 1 (after it's passed) means next year's.
// This never fires for a weekday phrase ("thu"), since when.EN already
// resolves those to the next upcoming occurrence on its own.
func fuzzyDate(input string, today time.Time) (time.Time, bool) {
	r, err := when.EN.Parse(input, today)
	if err != nil || r == nil || strings.TrimSpace(r.Text) != input {
		return time.Time{}, false
	}
	t := TruncateToDate(r.Time)
	if t.Before(today) && !explicitYearRe.MatchString(input) {
		t = t.AddDate(1, 0, 0)
	}
	return t, true
}

// ParseDeadlineInput parses a typed date into an active org timestamp
// suitable for DEADLINE.
func ParseDeadlineInput(input string) (*org.Timestamp, error) {
	t, hasTime, err := resolveDeadlineDate(input)
	if err != nil {
		return nil, err
	}
	format := "2006-01-02 Mon"
	if hasTime {
		format = "2006-01-02 Mon 15:04"
	}
	return &org.Timestamp{Active: true, Raw: t.Format(format)}, nil
}

// PrefillInput renders ts without its weekday, as a starting point
// for editing (empty if ts is nil).
func PrefillInput(ts *org.Timestamp) string {
	if ts == nil {
		return ""
	}
	t, hasTime, err := ParseFlexible(ts.Raw)
	if err != nil {
		return ts.Raw
	}
	if hasTime {
		return t.Format("2006-01-02 15:04")
	}
	return t.Format("2006-01-02")
}
