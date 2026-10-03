package orgdate

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/sburnett/orgtd/internal/org"
)

// repeaterRe matches an org repeater cookie trailing a timestamp's date,
// e.g. "+1w" (simple recur), "++2w" (catch up to the next occurrence
// after today when marked done), or ".+3d" (recur from the completion
// date rather than the original one) — the three marks org-mode
// supports. orgtd treats all three identically for agenda display (see
// repeaterCurrentOccurrence): the distinction only matters when a completed
// occurrence's date is rewritten in the file, which orgtd doesn't do.
// Only day/week/month/year units are recognized, consistent with
// parseRelativeOffset — orgtd's dates are day-grained, not hour-grained.
var repeaterRe = regexp.MustCompile(`(\+\+|\.\+|\+)(\d+)([dwmy])`)

// warningRe matches a DEADLINE warning-period cookie, e.g. "-3d". orgtd
// doesn't act on it (the agenda's window already surfaces near-term
// deadlines), but it must still be stripped before the remaining text is
// parsed as a date.
var warningRe = regexp.MustCompile(`-\d+[dwmy]`)

// RepeaterCookie returns ts's repeater cookie (e.g. "+1w"), if any, for
// display alongside the computed date — so a recurring item's row shows
// its interval instead of looking identical to a one-off date.
func RepeaterCookie(ts *org.Timestamp) string {
	if ts == nil {
		return ""
	}
	return repeaterRe.FindString(ts.Raw)
}

// ParseTimestampDate returns ts's date, with any time-of-day dropped, if
// it can be parsed as one of Layouts — the same layouts
// accepted when typing a date into the deadline prompt, which covers
// every format orgtd itself writes (see the ui package's applyDeadlineInput) plus a
// weekday-less fallback for hand-edited files. A repeater cookie
// (e.g. "+1w") and/or a warning-period cookie (e.g. "-3d") are stripped
// before that match is attempted, and — for a repeater — the parsed date
// is advanced to its current occurrence (see repeaterCurrentOccurrence),
// which mirrors org-mode: a repeating item's date only ever moves when
// the item is completed, so a stale, un-advanced date correctly shows as
// Overdue rather than being hidden by rolling it into the future. missed
// reports how many earlier occurrences have already elapsed since that
// date without the item being completed (0 for a non-repeating
// timestamp, or one whose repeater hasn't reached its first occurrence
// yet). A Raw value in some other shape (a range, or anything else
// emacs/org-mode might produce that orgtd doesn't write itself) is
// simply excluded from the agenda rather than guessed at.
func ParseTimestampDate(ts *org.Timestamp, today time.Time) (date time.Time, missed int, ok bool) {
	if ts == nil {
		return time.Time{}, 0, false
	}
	raw := ts.Raw
	repeat := repeaterRe.FindStringSubmatch(raw)
	if repeat != nil {
		raw = repeaterRe.ReplaceAllString(raw, "")
	}
	raw = warningRe.ReplaceAllString(raw, "")
	raw = strings.Join(strings.Fields(raw), " ")

	for _, layout := range Layouts {
		// ParseInLocation (not Parse, which defaults to UTC) so the
		// result is comparable against TruncateToDate(time.Now()), which
		// is anchored to the local zone — otherwise a date that's
		// "today" locally could parse as a different instant and get
		// bucketed into the wrong section near a timezone's UTC offset.
		t, err := time.ParseInLocation(layout, raw, time.Local)
		if err != nil {
			continue
		}
		base := TruncateToDate(t)
		if repeat == nil {
			return base, 0, true
		}
		n, err := strconv.Atoi(repeat[2])
		if err != nil {
			return base, 0, true
		}
		current, missed := repeaterCurrentOccurrence(base, n, repeat[3][0], today)
		return current, missed, true
	}
	return time.Time{}, 0, false
}

// repeaterCurrentOccurrence advances base by n-unit steps as long as the
// result doesn't pass today, returning the most recent due occurrence —
// the "floor", not the next future occurrence. Stopping at today rather
// than rolling past it is what makes a skipped recurring item visibly
// Overdue (org-mode only advances a repeating SCHEDULED/DEADLINE when the
// item is actually marked done, so a stale date genuinely means it's
// still pending). missed counts how many earlier occurrences have
// already elapsed since base without the item being completed — 0 if
// base itself hasn't arrived yet (nothing to advance past) or this is
// still that first pending occurrence.
func repeaterCurrentOccurrence(base time.Time, n int, unit byte, today time.Time) (current time.Time, missed int) {
	if n <= 0 || !base.Before(today) {
		return base, 0
	}
	for {
		next := repeaterStep(base, n, unit)
		if !next.After(base) || next.After(today) {
			return base, missed
		}
		base = next
		missed++
	}
}

// repeaterStep advances t by one repeater interval (n units), per
// repeaterRe's recognized units. An unrecognized unit (which repeaterRe
// itself never produces) returns t unchanged, letting the caller's
// !next.After(base) check break out rather than loop forever.
func repeaterStep(t time.Time, n int, unit byte) time.Time {
	switch unit {
	case 'd':
		return t.AddDate(0, 0, n)
	case 'w':
		return t.AddDate(0, 0, n*7)
	case 'm':
		return t.AddDate(0, n, 0)
	case 'y':
		return t.AddDate(n, 0, 0)
	}
	return t
}

// AdvanceRepeating computes ts's next occurrence for the
// "complete a repeating item" flow (see the ui package's repeatAdvanceForCompletion),
// replicating org-mode's own three repeater marks:
//
//   - "+"  (simple): exactly one interval past the old date, however
//     overdue that leaves it — org's plain, "naive" advance.
//   - "++" (catch-up): one or more intervals past the old date, however
//     many it takes to land on or after today, so completing a
//     long-overdue item doesn't just make it overdue by one interval
//     less.
//   - ".+" (from-now): one interval past *today*, the completion date,
//     ignoring how stale the old date was.
//
// This mirrors repeaterCurrentOccurrence's date/warning-cookie parsing,
// but that function computes a read-only, display-time "current
// occurrence" for the agenda and never rolls forward past today (so a
// skipped occurrence stays visibly Overdue); this one computes the new
// date to actually write back to the file once, at the moment the item
// is completed — the two serve different purposes and must stay
// separate. ok is false if ts has no repeater cookie, or its date
// portion doesn't parse, in which case ts itself is returned unchanged.
func AdvanceRepeating(ts *org.Timestamp, now time.Time) (next *org.Timestamp, ok bool) {
	if ts == nil {
		return ts, false
	}
	m := repeaterRe.FindStringSubmatch(ts.Raw)
	if m == nil {
		return ts, false
	}
	mark, numStr, unit := m[1], m[2], m[3][0]
	n, err := strconv.Atoi(numStr)
	if err != nil || n <= 0 {
		return ts, false
	}

	withoutRepeat := repeaterRe.ReplaceAllString(ts.Raw, "")
	warning := warningRe.FindString(withoutRepeat)
	dateText := strings.Join(strings.Fields(warningRe.ReplaceAllString(withoutRepeat, "")), " ")

	var base time.Time
	hasTime := false
	parsed := false
	for _, layout := range Layouts {
		if t, err := time.ParseInLocation(layout, dateText, time.Local); err == nil {
			base, hasTime, parsed = t, strings.Contains(layout, "15:04"), true
			break
		}
	}
	if !parsed {
		return ts, false
	}

	today := TruncateToDate(now)
	var result time.Time
	switch mark {
	case ".+":
		result = repeaterStep(today, n, unit)
		if hasTime {
			result = time.Date(result.Year(), result.Month(), result.Day(), base.Hour(), base.Minute(), 0, 0, result.Location())
		}
	case "++":
		result = repeaterStep(base, n, unit)
		for TruncateToDate(result).Before(today) {
			result = repeaterStep(result, n, unit)
		}
	default: // "+"
		result = repeaterStep(base, n, unit)
	}

	format := "2006-01-02 Mon"
	if hasTime {
		format = "2006-01-02 Mon 15:04"
	}
	raw := result.Format(format) + " " + mark + numStr + string(unit)
	if warning != "" {
		raw += " " + warning
	}
	return &org.Timestamp{Active: ts.Active, Raw: raw}, true
}
