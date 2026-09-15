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
var agendaSections = []string{"Overdue", "Due Today", "Upcoming", "Next Actions", "Meetings"}

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
		switch section {
		case "Next Actions":
			m.appendNextActionsSection()
			continue
		case "Meetings":
			m.appendMeetingsSection()
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

// meetingAgendaEntry is one calendar meeting occurrence in the
// "Meetings" section's window (see upcomingMeetingEntries), paired with
// the items to show grouped beneath it.
type meetingAgendaEntry struct {
	title      string
	start, end time.Time
	items      []*org.Headline
}

// upcomingMeetingEntries returns one meetingAgendaEntry per calendar
// event (any loaded headline with a GCAL_START — see
// cmd/gcalsync/convert.go) whose start falls within [start of today,
// now+24h) — i.e. every meeting starting sometime today or within the
// next 24 hours, current ones included — and that's part of a recurring
// series (GCAL_RECURRING_EVENT_ID) with at least one item elsewhere in
// the workspace linked to that same series (see
// entriesForRecurringMeeting) — something captured during, or otherwise
// linked to, a past occurrence of this same meeting that might need
// renewed attention or discussion this time around. A one-off meeting
// (no recurring ID) or one with nothing linked to it is left out
// entirely, rather than shown with nothing under it. Sorted
// chronologically by start time; a daily (or more frequent) recurring
// meeting can legitimately produce more than one entry for the same
// series within the window (today's occurrence and tomorrow's, say),
// each with its own date/time but the same linked items.
func (m *Model) upcomingMeetingEntries(now time.Time) []meetingAgendaEntry {
	windowStart := truncateToDate(now)
	windowEnd := now.Add(24 * time.Hour)

	var meetings []meetingAgendaEntry
	for _, f := range m.ws.Files {
		org.Walk(f.Headlines, func(h *org.Headline) {
			recurID := h.Properties["GCAL_RECURRING_EVENT_ID"]
			if recurID == "" {
				return
			}
			start, startOK := parseRFC3339Property(h, "GCAL_START")
			end, endOK := parseRFC3339Property(h, "GCAL_END")
			if !startOK || !endOK || start.Before(windowStart) || !start.Before(windowEnd) {
				return
			}
			items := m.entriesForRecurringMeeting(recurID)
			if len(items) == 0 {
				return
			}
			meetings = append(meetings, meetingAgendaEntry{title: h.Title, start: start, end: end, items: items})
		})
	}
	sort.SliceStable(meetings, func(i, j int) bool { return meetings[i].start.Before(meetings[j].start) })
	return meetings
}

// entriesForRecurringMeeting returns every headline, across the
// workspace, whose GCAL_RECURRING_EVENT_IDS property (set by
// gC/:capture — see insertHeadlineAt) names recurID — i.e. was captured
// during, or otherwise linked to, a past occurrence of this same
// recurring meeting — excluding DONE/CANCELLED items (already resolved,
// nothing left to revisit), in file/tree order. An entry can be linked
// to more than one meeting (its property can name more than one
// recurring series); it's returned once per meeting it names, so it can
// legitimately show up in more than one meeting's group.
func (m *Model) entriesForRecurringMeeting(recurID string) []*org.Headline {
	var items []*org.Headline
	for _, f := range m.ws.Files {
		org.Walk(f.Headlines, func(h *org.Headline) {
			if org.IsDoneKeyword(h.Keyword) {
				return
			}
			for _, id := range strings.Fields(h.Properties["GCAL_RECURRING_EVENT_IDS"]) {
				if id == recurID {
					items = append(items, h)
					return
				}
			}
		})
	}
	return items
}

// appendMeetingsSection appends the "Meetings" section (see
// upcomingMeetingEntries): a header row per current/upcoming recurring
// meeting with anything linked to it, each followed by its linked items,
// one level deeper (see row.level/rowLevel) than the meeting header,
// which is itself one level deeper than the section header — so the
// outline's generic level-aware navigation (moveDeeper, jumpToSubtreeTop/
// Bottom, etc.) already groups them correctly with no special-casing.
func (m *Model) appendMeetingsSection() {
	meetings := m.upcomingMeetingEntries(time.Now())
	if len(meetings) == 0 {
		return
	}
	m.rows = append(m.rows, row{section: "Meetings"})
	for _, mt := range meetings {
		m.rows = append(m.rows, row{level: 1, isMeetingHeader: true, meetingTitle: mt.title, meetingStart: mt.start, meetingEnd: mt.end})
		for _, h := range mt.items {
			m.rows = append(m.rows, row{headline: h, level: 2, isAgendaItem: true})
		}
	}
}

// meetingCandidate is one distinct recurring meeting series offered by
// the "gM" picker (see startMeetingPicker in model.go) — one per unique
// GCAL_RECURRING_EVENT_ID found anywhere in the workspace, not one per
// individual synced occurrence (see meetingCandidates).
type meetingCandidate struct {
	recurringEventID string
	title            string
	link             string    // GCAL_HTML_LINK, "" if gcalsync didn't have one — see buildMeetingAttachAction
	when             time.Time // the series' representative occurrence's start — see meetingCandidates
	end              time.Time // that same occurrence's end, zero if GCAL_END was missing/unparseable
}

// inProgress reports whether c's representative occurrence has started
// but not yet ended, as of now.
func (c meetingCandidate) inProgress(now time.Time) bool {
	return !c.when.After(now) && now.Before(c.end)
}

// meetingCandidates returns one meetingCandidate per distinct recurring
// series found in any loaded calendar event (GCAL_RECURRING_EVENT_ID —
// see cmd/gcalsync/convert.go), each using whichever synced occurrence
// is currently most relevant (moreRelevantOccurrence) as its
// title/display date, sorted for the "gM" picker (meetingPickerLess) so
// index 0 — the picker's default highlight — is the one you're most
// likely attaching an item to right now: a meeting currently in
// progress, the shortest one if more than one is, else the next one to
// start. Only meaningful while gcalsync has at least one instance of a
// series synced; a series whose every synced occurrence has aged out of
// the sync window (in either direction) simply won't appear until
// gcalsync runs again.
func (m *Model) meetingCandidates(now time.Time) []meetingCandidate {
	best := make(map[string]meetingCandidate)
	for _, f := range m.ws.Files {
		org.Walk(f.Headlines, func(h *org.Headline) {
			recurID := h.Properties["GCAL_RECURRING_EVENT_ID"]
			if recurID == "" {
				return
			}
			start, ok := parseRFC3339Property(h, "GCAL_START")
			if !ok {
				return
			}
			if cur, exists := best[recurID]; !exists || moreRelevantOccurrence(start, cur.when, now) {
				end, _ := parseRFC3339Property(h, "GCAL_END")
				best[recurID] = meetingCandidate{recurringEventID: recurID, title: h.Title, link: h.Properties["GCAL_HTML_LINK"], when: start, end: end}
			}
		})
	}

	candidates := make([]meetingCandidate, 0, len(best))
	for _, c := range best {
		candidates = append(candidates, c)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return meetingPickerLess(candidates[i], candidates[j], now)
	})
	return candidates
}

// meetingPickerLess reports whether a should rank ahead of b for the
// "gM" picker's default highlight: a meeting currently in progress
// always ranks ahead of one that isn't; between two in-progress
// meetings, the one ending sooner (the shorter of the two) ranks
// first — so a quick standup you're nominally "in" right now doesn't
// get buried under an hours-long meeting that's also technically
// ongoing. Neither in progress: falls back to moreRelevantOccurrence
// (soonest upcoming first, else most recently ended).
func meetingPickerLess(a, b meetingCandidate, now time.Time) bool {
	aIn, bIn := a.inProgress(now), b.inProgress(now)
	if aIn != bIn {
		return aIn
	}
	if aIn {
		return a.end.Sub(a.when) < b.end.Sub(b.when)
	}
	return moreRelevantOccurrence(a.when, b.when, now)
}

// moreRelevantOccurrence reports whether a should rank ahead of b (used
// both to pick a series' representative occurrence and, via
// meetingPickerLess, as the picker's fallback order once "in progress"
// is decided): an upcoming occurrence (>= now) always ranks ahead of a
// past one; between two upcoming occurrences the soonest ranks first;
// between two past ones the most recent ranks first.
func moreRelevantOccurrence(a, b, now time.Time) bool {
	aUpcoming := !a.Before(now)
	bUpcoming := !b.Before(now)
	if aUpcoming != bUpcoming {
		return aUpcoming
	}
	if aUpcoming {
		return a.Before(b)
	}
	return a.After(b)
}

// filteredMeetingCandidates returns every candidate whose title contains
// filter, case-insensitively — plain substring matching, unlike the
// status picker's matchesFilter (prefix/shortcut over a small fixed
// keyword set): a meeting title is arbitrary text, not a keyword, so
// there's no natural prefix or single-letter shortcut to match on.
func filteredMeetingCandidates(candidates []meetingCandidate, filter string) []meetingCandidate {
	if filter == "" {
		return candidates
	}
	lower := strings.ToLower(filter)
	var out []meetingCandidate
	for _, c := range candidates {
		if strings.Contains(strings.ToLower(c.title), lower) {
			out = append(out, c)
		}
	}
	return out
}

// meetingIsAttached reports whether h's GCAL_RECURRING_EVENT_IDS
// property already names recurID.
func meetingIsAttached(h *org.Headline, recurID string) bool {
	if h == nil {
		return false
	}
	for _, id := range strings.Fields(h.Properties["GCAL_RECURRING_EVENT_IDS"]) {
		if id == recurID {
			return true
		}
	}
	return false
}
