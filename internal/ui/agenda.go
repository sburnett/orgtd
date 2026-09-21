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
// internal/calendarsync/convert.go) whose start falls within [start of today,
// now+24h) — i.e. every meeting starting sometime today or within the
// next 24 hours, current ones included — with at least one item
// elsewhere in the workspace linked to it, whether attached via "gM" or
// sharing a tag with it (see entriesForMeeting): something raised
// during, or otherwise linked to, this meeting (a past occurrence, for a
// recurring series; the meeting itself, for a one-off) that might need
// renewed attention or
// discussion this time around. A meeting with nothing linked to it is
// left out entirely, rather than shown with nothing under it. Sorted
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
			kind, id, ok := meetingIdentity(h)
			if !ok {
				return
			}
			start, startOK := parseRFC3339Property(h, "GCAL_START")
			end, endOK := parseRFC3339Property(h, "GCAL_END")
			if !startOK || !endOK || start.Before(windowStart) || !start.Before(windowEnd) {
				return
			}
			items := m.entriesForMeeting(kind, id)
			if len(items) == 0 {
				return
			}
			meetings = append(meetings, meetingAgendaEntry{title: h.Title, start: start, end: end, items: items})
		})
	}
	sort.SliceStable(meetings, func(i, j int) bool { return meetings[i].start.Before(meetings[j].start) })
	return meetings
}

// entriesForMeeting returns every headline, across the workspace, linked
// to the meeting kind/id names — a past occurrence of it for a recurring
// series or the meeting itself for a one-off — two ways: explicitly, via
// kind.idsProperty() (set by "gM" — see buildMeetingAttachAction), or
// automatically, by sharing a tag with it (see meetingTags/hasSharedTag)
// — e.g. an entry tagged "@alice" links to every meeting :sync-calendar
// has alice down as a confirmed attendee of, with no "gM" attach needed.
// Either way excludes DONE/CANCELLED items (already resolved, nothing
// left to revisit) and, for the tag path only, any headline that's
// itself a synced calendar event (a meeting can't be "linked to" its own
// occurrence, or a sibling occurrence of the same series, just by
// carrying the same attendee tags). Returned in file/tree order, each
// headline at most once even if it matches both ways; an entry can still
// be linked to more than one distinct meeting (an explicit property can
// name several, and a tag can match several), so it can legitimately
// show up in more than one meeting's group.
func (m *Model) entriesForMeeting(kind meetingIDKind, id string) []*org.Headline {
	tags := m.meetingTags(kind, id)
	idsProp := kind.idsProperty()
	var items []*org.Headline
	for _, f := range m.ws.Files {
		org.Walk(f.Headlines, func(h *org.Headline) {
			if org.IsDoneKeyword(h.Keyword) {
				return
			}
			for _, candidateID := range strings.Fields(h.Properties[idsProp]) {
				if candidateID == id {
					items = append(items, h)
					return
				}
			}
			if _, _, ok := meetingIdentity(h); ok {
				return
			}
			if hasSharedTag(h.Tags, tags) {
				items = append(items, h)
			}
		})
	}
	return items
}

// meetingTags returns the union of every tag (the "recurring" system
// tag excluded — see meetingSeriesTag) carried by any synced occurrence
// of the meeting kind/id identifies, across every loaded file — the set
// entriesForMeeting/tagLinkedMeetingCandidates match an entry's own tags
// against for automatic (non-"gM") meeting linking.
func (m *Model) meetingTags(kind meetingIDKind, id string) map[string]bool {
	idProp := kind.idProperty()
	tags := make(map[string]bool)
	for _, f := range m.ws.Files {
		org.Walk(f.Headlines, func(h *org.Headline) {
			if h.Properties[idProp] != id {
				return
			}
			for _, t := range h.Tags {
				if t != meetingSeriesTag {
					tags[t] = true
				}
			}
		})
	}
	return tags
}

// hasSharedTag reports whether any of tags is a member of set — used to
// check an entry's tags against a meeting's (see meetingTags), or vice
// versa.
func hasSharedTag(tags []string, set map[string]bool) bool {
	for _, t := range tags {
		if set[t] {
			return true
		}
	}
	return false
}

// appendMeetingsSection appends the "Meetings" section (see
// upcomingMeetingEntries): a header row per current/upcoming meeting
// (recurring series or one-off) with anything linked to it, each
// followed by its linked items,
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
			m.rows = append(m.rows, row{headline: h, level: 2, isAgendaItem: true, meetingItemTitle: mt.title, meetingItemStart: mt.start})
		}
	}
}

// meetingIDKind distinguishes the two shapes a meetingCandidate (or a
// Meetings-section entry) can attach through: a recurring series,
// stable across every occurrence (GCAL_RECURRING_EVENT_ID on the event,
// GCAL_RECURRING_EVENT_IDS/GCAL_RECURRING_EVENT_LINKS on whatever's
// attached to it), or a single one-off event, unique to itself
// (GCAL_EVENT_ID/GCAL_EVENT_IDS/GCAL_EVENT_LINKS) — see
// internal/calendarsync/convert.go, which sets GCAL_EVENT_ID on every synced
// event and GCAL_RECURRING_EVENT_ID additionally on one that's part of
// a series. Kept as a small enum (rather than, say, always comparing
// against "") so every place that needs "which pair of properties"
// asks it the same way instead of re-deriving it from an ID string.
type meetingIDKind int

const (
	oneOffMeeting meetingIDKind = iota
	recurringMeeting
)

// idProperty names the property a synced calendar occurrence of this
// kind carries its own meeting identity under (singular — one value per
// headline, unlike idsProperty below) — GCAL_RECURRING_EVENT_ID or
// GCAL_EVENT_ID, matching whichever meetingIdentity/meetingCandidates
// read. See meetingTags for the one place besides those that needs it
// directly rather than going through meetingIdentity.
func (k meetingIDKind) idProperty() string {
	if k == recurringMeeting {
		return "GCAL_RECURRING_EVENT_ID"
	}
	return "GCAL_EVENT_ID"
}

// idsProperty and linksProperty name the pair of properties an entry
// records its attachment to a meeting of this kind through — see
// buildMeetingAttachAction/meetingIsAttached and entriesForMeeting.
func (k meetingIDKind) idsProperty() string {
	if k == recurringMeeting {
		return "GCAL_RECURRING_EVENT_IDS"
	}
	return "GCAL_EVENT_IDS"
}

func (k meetingIDKind) linksProperty() string {
	if k == recurringMeeting {
		return "GCAL_RECURRING_EVENT_LINKS"
	}
	return "GCAL_EVENT_LINKS"
}

// meetingSeriesTag is the tag :sync-calendar itself stamps onto every
// occurrence of a recurring series (see internal/calendarsync's
// buildHeadline) — excluded from tag-based meeting linking (see
// meetingTags/hasSharedTag) since every recurring event carries it
// regardless of content: matching on it would link any entry tagged
// "recurring" to every recurring meeting synced, which is noise, not a
// meaningful connection the way a shared attendee "@username" tag is.
const meetingSeriesTag = "recurring"

// meetingIdentity reports which meeting h represents, if h is itself a
// synced calendar event (GCAL_EVENT_ID set — see
// internal/calendarsync/convert.go): id/kind identify a recurring series
// by its GCAL_RECURRING_EVENT_ID (stable across every occurrence) or a
// one-off event by its own GCAL_EVENT_ID, same as meetingCandidate.id/
// kind. ok is false for anything that isn't a synced calendar event at
// all (no GCAL_EVENT_ID) — id/kind are meaningless then.
func meetingIdentity(h *org.Headline) (kind meetingIDKind, id string, ok bool) {
	eventID := h.Properties["GCAL_EVENT_ID"]
	if eventID == "" {
		return oneOffMeeting, "", false
	}
	if recurID := h.Properties["GCAL_RECURRING_EVENT_ID"]; recurID != "" {
		return recurringMeeting, recurID, true
	}
	return oneOffMeeting, eventID, true
}

// meetingCandidate is one distinct meeting offered by the "gM" picker
// (see startMeetingPicker in model.go): one per unique
// GCAL_RECURRING_EVENT_ID for a recurring series (not one per
// individual synced occurrence), or one per unique GCAL_EVENT_ID for a
// one-off event — see meetingCandidates.
type meetingCandidate struct {
	id    string // GCAL_RECURRING_EVENT_ID if kind == recurringMeeting, else GCAL_EVENT_ID
	kind  meetingIDKind
	title string
	link  string    // GCAL_HTML_LINK, "" if :sync-calendar didn't have one — see buildMeetingAttachAction
	when  time.Time // the series' representative occurrence's start (or the one-off event's own start) — see meetingCandidates
	end   time.Time // that same occurrence's end, zero if GCAL_END was missing/unparseable
	tags  map[string]bool // meetingTags(kind, id) — attendee tags, "recurring" excluded — matched by filteredMeetingCandidates alongside title
}

// inProgress reports whether c's representative occurrence has started
// but not yet ended, as of now.
func (c meetingCandidate) inProgress(now time.Time) bool {
	return !c.when.After(now) && now.Before(c.end)
}

// meetingKey identifies one meetingCandidate within meetingCandidates'
// dedup map: kind alongside id, rather than id alone, so a recurring
// series' ID and some one-off event's own ID can never collide even in
// principle (in practice Google's IDs are opaque enough that this can't
// really happen, but nothing here depends on that).
type meetingKey struct {
	kind meetingIDKind
	id   string
}

// meetingCandidates returns one meetingCandidate per distinct meeting
// found in any loaded calendar event — a recurring series, deduped by
// GCAL_RECURRING_EVENT_ID (see internal/calendarsync/convert.go), or a one-off
// event, one per GCAL_EVENT_ID (every synced event carries this;
// GCAL_RECURRING_EVENT_ID only if it's part of a series) — each using
// whichever synced occurrence is currently most relevant
// (meetingPickerLess) as its title/display date. The returned slice is
// sorted chronologically by that representative occurrence's start time,
// so the picker's candidate list (see meetingPickerLines) reads
// top-to-bottom in time order, making it easy to compare times across
// entries — the picker's default highlight (see
// meetingPickerDefaultIndex, in model.go) is computed separately, from
// this same slice, rather than by relying on its ordering. Picking the
// representative occurrence with meetingPickerLess rather than a
// start-only comparison matters for a series with more than one instance
// synced at once (a daily standup: today's and tomorrow's both fall
// inside the sync window): a start-only "soonest upcoming wins"
// comparison would treat today's already-started occurrence as simply
// "past" and lose it to tomorrow's, hiding the fact that the series is
// in progress right now. Only meaningful while :sync-calendar has synced
// at least one relevant instance; a recurring series whose every synced
// occurrence has aged out of the sync window (in either direction), or a
// one-off event that's aged out entirely, simply won't appear until
// :sync-calendar runs again.
func (m *Model) meetingCandidates(now time.Time) []meetingCandidate {
	best := make(map[meetingKey]meetingCandidate)
	for _, f := range m.ws.Files {
		org.Walk(f.Headlines, func(h *org.Headline) {
			kind, id, ok := meetingIdentity(h)
			if !ok {
				return // not a synced calendar event at all
			}
			start, ok := parseRFC3339Property(h, "GCAL_START")
			if !ok {
				return
			}
			end, _ := parseRFC3339Property(h, "GCAL_END")
			cand := meetingCandidate{id: id, kind: kind, title: h.Title, link: h.Properties["GCAL_HTML_LINK"], when: start, end: end}
			key := meetingKey{kind, id}
			if cur, exists := best[key]; !exists || meetingPickerLess(cand, cur, now) {
				best[key] = cand
			}
		})
	}

	candidates := make([]meetingCandidate, 0, len(best))
	for key, c := range best {
		c.tags = m.meetingTags(key.kind, key.id)
		candidates = append(candidates, c)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].when.Before(candidates[j].when)
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
// (whichever starts closest to now, upcoming or recently ended alike).
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

// moreRelevantOccurrence reports whether a should rank ahead of b — used
// by meetingPickerLess as its fallback order once "in progress" is
// already decided (neither a nor b is a substitute for that check on
// its own: it has no notion of "in progress" at all, which is why
// meetingCandidates picks a series' representative occurrence via
// meetingPickerLess rather than this directly): whichever start time is
// closer to now, in either direction, ranks first. This is deliberately
// not "soonest upcoming always beats any past occurrence" — a meeting
// that just ended is exactly the kind of thing you're likely reaching
// for "gM" to attach something to (jotting down a note right as a
// meeting wraps up), so it needs to surface near the top rather than
// sink beneath every future meeting, however far off, just for having
// already started.
func moreRelevantOccurrence(a, b, now time.Time) bool {
	return a.Sub(now).Abs() < b.Sub(now).Abs()
}

// meetingPickerDefaultIndex returns the index, within candidates (as
// returned by meetingCandidates — sorted chronologically, not by
// relevance), of the meeting "gM" should highlight when the picker first
// opens: whichever one meetingPickerLess would rank first were the list
// still relevance-ordered. Kept separate from meetingCandidates' own
// ordering so the picker's list can read chronologically (easy to
// compare times across entries) while still landing the cursor on the
// meeting you're most likely attaching to right now, rather than
// whichever happens to start earliest in the sync window. Returns 0 for
// an empty slice (never actually reached — startMeetingPicker no-ops
// first).
func meetingPickerDefaultIndex(candidates []meetingCandidate, now time.Time) int {
	best := 0
	for i := 1; i < len(candidates); i++ {
		if meetingPickerLess(candidates[i], candidates[best], now) {
			best = i
		}
	}
	return best
}

// filteredMeetingCandidates returns every candidate whose title, or one of
// its attendee tags (c.tags — see meetingCandidates/meetingTags), contains
// filter, case-insensitively — plain substring matching, unlike the status
// picker's matchesFilter (prefix/shortcut over a small fixed keyword set):
// a meeting title is arbitrary text, not a keyword, so there's no natural
// prefix or single-letter shortcut to match on. Matching tags too lets
// typing an attendee's name (e.g. "alice", matching the "@alice" tag
// :sync-calendar stamps on — see Calendar sync) find a meeting whose title
// doesn't happen to mention them.
func filteredMeetingCandidates(candidates []meetingCandidate, filter string) []meetingCandidate {
	if filter == "" {
		return candidates
	}
	lower := strings.ToLower(filter)
	var out []meetingCandidate
	for _, c := range candidates {
		if strings.Contains(strings.ToLower(c.title), lower) || tagsContain(c.tags, lower) {
			out = append(out, c)
		}
	}
	return out
}

// tagsContain reports whether any tag in tags contains lower, a
// lowercased substring, case-insensitively.
func tagsContain(tags map[string]bool, lower string) bool {
	for t := range tags {
		if strings.Contains(strings.ToLower(t), lower) {
			return true
		}
	}
	return false
}

// meetingIsAttached reports whether h's c.kind.idsProperty() (either
// GCAL_RECURRING_EVENT_IDS or GCAL_EVENT_IDS, matching whichever c is)
// already names c.id.
func meetingIsAttached(h *org.Headline, c meetingCandidate) bool {
	if h == nil {
		return false
	}
	for _, id := range strings.Fields(h.Properties[c.kind.idsProperty()]) {
		if id == c.id {
			return true
		}
	}
	return false
}

// tagLinkedMeetingCandidates returns every distinct meeting (see
// meetingCandidates) that shares a tag with h (see
// meetingTags/hasSharedTag) — the entry-side counterpart of
// entriesForMeeting's tag-matching, used by the gutter's meeting marker
// and the status line's link list to treat a tag-matched meeting the
// same as one attached via "gM", with no explicit attach needed. If h
// is itself a synced calendar event, it's never treated as tag-linked
// to any meeting (itself, a sibling occurrence of its own series, or a
// wholly different meeting) — mirrors entriesForMeeting's own blanket
// exclusion of calendar events from tag-matched items, since only
// entries elsewhere in the org directory can be linked this way. nil
// if h has no tags (other than meetingSeriesTag, which never counts)
// to match with.
func (m *Model) tagLinkedMeetingCandidates(h *org.Headline, now time.Time) []meetingCandidate {
	if h == nil {
		return nil
	}
	hasTag := false
	for _, t := range h.Tags {
		if t != meetingSeriesTag {
			hasTag = true
			break
		}
	}
	if !hasTag {
		return nil
	}
	if _, _, isEvent := meetingIdentity(h); isEvent {
		return nil
	}

	var out []meetingCandidate
	for _, c := range m.meetingCandidates(now) {
		if hasSharedTag(h.Tags, m.meetingTags(c.kind, c.id)) {
			out = append(out, c)
		}
	}
	return out
}
