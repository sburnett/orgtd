package ui

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sburnett/orgtd/internal/org"
)

// agendaSections lists the agenda's sections, in display order.
var agendaSections = []string{"Overdue", "Due Today", "Upcoming", "Next Actions"}

// repeaterRe matches an org repeater cookie trailing a timestamp's date,
// e.g. "+1w" (simple recur), "++2w" (catch up to the next occurrence
// after today when marked done), or ".+3d" (recur from the completion
// date rather than the original one) — the three marks org-mode
// supports. orgtd treats all three identically for agenda display (see
// repeaterNextDate): the distinction only matters when a completed
// occurrence's date is rewritten in the file, which orgtd doesn't do.
// Only day/week/month/year units are recognized, consistent with
// parseRelativeOffset — orgtd's dates are day-grained, not hour-grained.
var repeaterRe = regexp.MustCompile(`(\+\+|\.\+|\+)(\d+)([dwmy])`)

// warningRe matches a DEADLINE warning-period cookie, e.g. "-3d". orgtd
// doesn't act on it (the agenda's window already surfaces near-term
// deadlines), but it must still be stripped before the remaining text is
// parsed as a date.
var warningRe = regexp.MustCompile(`-\d+[dwmy]`)

// repeaterCookie returns ts's repeater cookie (e.g. "+1w"), if any, for
// display alongside the computed date — so a recurring item's row shows
// its interval instead of looking identical to a one-off date.
func repeaterCookie(ts *org.Timestamp) string {
	if ts == nil {
		return ""
	}
	return repeaterRe.FindString(ts.Raw)
}

// parseTimestampDate returns ts's date, with any time-of-day dropped, if
// it can be parsed as one of dateInputLayouts — the same layouts
// accepted when typing a date into the deadline prompt, which covers
// every format orgtd itself writes (see applyDeadlineInput) plus a
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
func parseTimestampDate(ts *org.Timestamp, today time.Time) (date time.Time, missed int, ok bool) {
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

	for _, layout := range dateInputLayouts {
		// ParseInLocation (not Parse, which defaults to UTC) so the
		// result is comparable against truncateToDate(time.Now()), which
		// is anchored to the local zone — otherwise a date that's
		// "today" locally could parse as a different instant and get
		// bucketed into the wrong section near a timezone's UTC offset.
		t, err := time.ParseInLocation(layout, raw, time.Local)
		if err != nil {
			continue
		}
		base := truncateToDate(t)
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

// agendaEntry is one (headline, relevant date) pair destined for the
// agenda — a headline with both SCHEDULED and DEADLINE produces two.
type agendaEntry struct {
	h        *org.Headline
	label    string // "Scheduled" or "Deadline"
	date     time.Time
	repeater string // e.g. "+1w", if date came from a recurring timestamp; empty otherwise
	missed   int    // occurrences elapsed since date without completion; only meaningful (and only shown) when date is Overdue
}

// agendaEntries collects one entry per (headline, relevant date) pair
// across every file in the workspace, for every headline that's active
// (not DONE/CANCELLED) and not SOMEDAY — SOMEDAY is excluded from the
// agenda entirely, even for a date within the window, since it's
// explicitly the "not committed to a date" state. Only dates on or
// before today+windowDays are collected; anything further out (or a
// SCHEDULED/DEADLINE whose Raw text can't be parsed) is dropped.
func (m *Model) agendaEntries(today time.Time, windowDays int) []agendaEntry {
	var entries []agendaEntry
	horizon := today.AddDate(0, 0, windowDays)
	consider := func(ts *org.Timestamp, label string, h *org.Headline) {
		date, missed, ok := parseTimestampDate(ts, today)
		if !ok || date.After(horizon) {
			return
		}
		entries = append(entries, agendaEntry{h: h, label: label, date: date, repeater: repeaterCookie(ts), missed: missed})
	}
	for _, f := range m.ws.Files {
		org.Walk(f.Headlines, func(h *org.Headline) {
			if org.IsDoneKeyword(h.Keyword) || h.Keyword == "SOMEDAY" {
				return
			}
			consider(h.Deadline, "Deadline", h)
			consider(h.Scheduled, "Scheduled", h)
		})
	}
	return entries
}

// agendaSection buckets date relative to today: "Overdue" (before
// today), "Due Today", or "Upcoming" (after today — callers are
// expected to have already excluded anything beyond the window).
func agendaSection(date, today time.Time) string {
	switch {
	case date.Before(today):
		return "Overdue"
	case date.Equal(today):
		return "Due Today"
	default:
		return "Upcoming"
	}
}

// nextActionHeadlines returns every NEXT headline across the workspace,
// in file/tree order — regardless of whether it has a SCHEDULED or
// DEADLINE date, since NEXT is GTD's "immediately actionable" state on
// its own merits, independent of timing. A NEXT item that also has a
// near-term date still additionally appears in its date-based section
// too — the two sections answer different questions ("what's next to
// work on" vs. "what's due when").
func (m *Model) nextActionHeadlines() []*org.Headline {
	var next []*org.Headline
	for _, f := range m.ws.Files {
		org.Walk(f.Headlines, func(h *org.Headline) {
			if h.Keyword == "NEXT" {
				next = append(next, h)
			}
		})
	}
	return next
}

// appendAgendaRows populates m.rows for agenda view: one section-header
// row per non-empty section (Overdue, Due Today, Upcoming, Next
// Actions, in that order). The three date-based sections' item rows are
// sorted by date, ties broken by original file/tree order; Next
// Actions' are left in plain file/tree order, since most NEXT items
// have no date to sort by.
func (m *Model) appendAgendaRows() {
	today := truncateToDate(time.Now())
	entries := m.agendaEntries(today, m.agendaDays)

	bySection := make(map[string][]agendaEntry, len(agendaSections))
	for _, e := range entries {
		s := agendaSection(e.date, today)
		bySection[s] = append(bySection[s], e)
	}

	for _, section := range agendaSections {
		if section == "Next Actions" {
			m.appendNextActionsSection()
			continue
		}
		es := bySection[section]
		if len(es) == 0 {
			continue
		}
		sort.SliceStable(es, func(i, j int) bool { return es[i].date.Before(es[j].date) })
		m.rows = append(m.rows, row{section: section})
		for _, e := range es {
			missed := 0
			if section == "Overdue" {
				missed = e.missed
			}
			m.rows = append(m.rows, row{headline: e.h, level: 1, isAgendaItem: true, agendaLabel: e.label, agendaDate: e.date, agendaRepeater: e.repeater, agendaMissed: missed})
		}
	}
}

func (m *Model) appendNextActionsSection() {
	next := m.nextActionHeadlines()
	if len(next) == 0 {
		return
	}
	m.rows = append(m.rows, row{section: "Next Actions"})
	for _, h := range next {
		m.rows = append(m.rows, row{headline: h, level: 1, isAgendaItem: true})
	}
}
