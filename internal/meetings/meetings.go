// Package meetings is orgtd's model of calendar meetings and how entries
// link to them. It has no UI dependency: given the loaded org files, it
// answers which meetings exist (Index.Candidates), which entries are
// linked to a meeting (Index.LinkedItems) or which meetings an entry is
// linked to (Index.TagLinked), and the durable tags recorded for a
// meeting in the meeting-tags file.
//
// A *meeting* is either a recurring series, identified by the
// GCAL_RECURRING_EVENT_ID that :sync-calendar stamps on every occurrence,
// or a one-off event, identified by its own GCAL_EVENT_ID (see Kind). An
// entry is *linked* to a meeting two ways: explicitly, by naming its ID in
// a GCAL_EVENT_IDS/GCAL_RECURRING_EVENT_IDS property (what "gM" writes);
// or implicitly, by sharing a tag with the meeting — the meeting's own
// tags (e.g. attendee "@alice" tags from the sync) plus any recorded for
// it in the meeting-tags file. See README's "Tag-based meeting links".
//
// An Index is an immutable snapshot built from the loaded files. Building
// one walks every headline once; every query after that is a map lookup,
// so callers should build one and reuse it until the files change.
package meetings

import (
	"sort"
	"strings"
	"time"

	"github.com/sburnett/orgtd/internal/org"
)

// Kind distinguishes the two shapes a meeting can take: a recurring
// series, stable across every occurrence (GCAL_RECURRING_EVENT_ID on the
// event, GCAL_RECURRING_EVENT_IDS/GCAL_RECURRING_EVENT_LINKS on whatever's
// attached to it), or a single one-off event, unique to itself
// (GCAL_EVENT_ID/GCAL_EVENT_IDS/GCAL_EVENT_LINKS). Every synced event
// carries GCAL_EVENT_ID; GCAL_RECURRING_EVENT_ID additionally if it's part
// of a series (see internal/calendarsync/convert.go). A small enum rather
// than comparing against "" so every place that needs "which pair of
// properties" asks it the same way.
type Kind int

const (
	OneOff Kind = iota
	Recurring
)

// IDProperty names the property a synced calendar occurrence of this kind
// carries its own meeting identity under (singular — one value per
// headline, unlike IDsProperty).
func (k Kind) IDProperty() string {
	if k == Recurring {
		return "GCAL_RECURRING_EVENT_ID"
	}
	return "GCAL_EVENT_ID"
}

// IDsProperty names the property an entry records its explicit
// attachments to meetings of this kind in (space-separated IDs).
func (k Kind) IDsProperty() string {
	if k == Recurring {
		return "GCAL_RECURRING_EVENT_IDS"
	}
	return "GCAL_EVENT_IDS"
}

// LinksProperty names the property holding a title/link snapshot
// ("[[url][title]]", one per attached meeting, parallel to IDsProperty) so
// an attachment still displays after its meeting ages out of the sync
// window.
func (k Kind) LinksProperty() string {
	if k == Recurring {
		return "GCAL_RECURRING_EVENT_LINKS"
	}
	return "GCAL_EVENT_LINKS"
}

// TagsIDsProperty names the property a meeting-tags-file record lists the
// meeting IDs it applies to in. Deliberately distinct from IDsProperty: a
// tag record isn't attached to a meeting the way a task is, it's a tag set
// keyed by meeting ID, and reusing IDsProperty's name would make every
// record wrongly show up as a linked item nested under its own meeting.
func (k Kind) TagsIDsProperty() string {
	if k == Recurring {
		return "MEETING_TAG_RECURRING_EVENT_IDS"
	}
	return "MEETING_TAG_EVENT_IDS"
}

// SeriesTag is the tag :sync-calendar stamps onto every occurrence of a
// recurring series. It never counts for tag-based linking: every
// recurring event carries it regardless of content, so matching on it
// would link any entry tagged "recurring" to every recurring meeting —
// noise, not a meaningful connection like a shared attendee tag.
const SeriesTag = "recurring"

// Key identifies one meeting: Kind alongside ID, rather than ID alone, so
// a recurring series' ID and some one-off event's own ID can never
// collide even in principle.
type Key struct {
	Kind Kind
	ID   string
}

// Identity reports which meeting h represents, if h is itself a synced
// calendar event (GCAL_EVENT_ID set): a recurring series by its
// GCAL_RECURRING_EVENT_ID (stable across every occurrence), or a one-off
// event by its own GCAL_EVENT_ID. ok is false for anything that isn't a
// synced calendar event — the Key is meaningless then.
func Identity(h *org.Headline) (key Key, ok bool) {
	eventID := h.Properties["GCAL_EVENT_ID"]
	if eventID == "" {
		return Key{}, false
	}
	if recurID := h.Properties["GCAL_RECURRING_EVENT_ID"]; recurID != "" {
		return Key{Recurring, recurID}, true
	}
	return Key{OneOff, eventID}, true
}

// IsTagRecord reports whether h is a meeting-tags-file record (carries
// either kind's TagsIDsProperty) rather than an ordinary outline entry.
// Such a record necessarily carries whatever tag it records, so without
// this check it would always tag-match its own meeting and show up as a
// linked item of the very meeting it's bookkeeping for.
func IsTagRecord(h *org.Headline) bool {
	return h.Properties[OneOff.TagsIDsProperty()] != "" || h.Properties[Recurring.TagsIDsProperty()] != ""
}

// TimeProperty parses h's property key as the RFC 3339 timestamp
// :sync-calendar writes (GCAL_START/GCAL_END). ok is false if it's
// missing or unparseable.
func TimeProperty(h *org.Headline, key string) (time.Time, bool) {
	raw := h.Properties[key]
	if raw == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// Meeting is one distinct meeting — a recurring series (deduped across its
// occurrences) or a one-off event — as offered by the "gM" picker and used
// to describe an entry's links. The display fields (Title, Link, When,
// End, Accepted) come from the meeting's *representative occurrence*, the
// one most relevant at the time asked (see Less).
type Meeting struct {
	Key
	Title string
	Link  string    // GCAL_HTML_LINK, "" if :sync-calendar didn't have one
	When  time.Time // the representative occurrence's start
	End   time.Time // that occurrence's end; zero if GCAL_END was missing/unparseable

	// Tags are the meeting's tags for linking purposes: every tag carried
	// by any synced occurrence (SeriesTag excluded) plus any recorded for
	// it in the meeting-tags file. Shared and read-only; callers must not
	// modify it.
	Tags map[string]bool

	// Accepted is GCAL_SELF_RESPONSE_STATUS == "accepted": the calendar
	// owner's own RSVP, not merely having been invited. A "maybe" or
	// not-yet-responded invite does not count — see Prioritized.
	Accepted bool
}

// FromEvent builds the Meeting identifying event h itself (key, title and
// link only — all an attach action needs), for attaching something to h
// directly without the interactive picker. ok is false if h isn't a
// synced calendar event.
func FromEvent(h *org.Headline) (Meeting, bool) {
	key, ok := Identity(h)
	if !ok {
		return Meeting{}, false
	}
	return Meeting{Key: key, Title: h.Title, Link: h.Properties["GCAL_HTML_LINK"]}, true
}

// InProgress reports whether the meeting's representative occurrence has
// started but not yet ended, as of now.
func (c Meeting) InProgress(now time.Time) bool {
	return !c.When.After(now) && now.Before(c.End)
}

// Prioritized reports whether c gets Less's top-tier boost: in progress
// *and* accepted, not merely invited — a meeting you're only tentative on
// (or never responded to) shouldn't jump ahead of everything else just
// because it happens to overlap now.
func (c Meeting) Prioritized(now time.Time) bool {
	return c.InProgress(now) && c.Accepted
}

// IsAttachedTo reports whether h's explicit attachment property for this
// meeting's kind (GCAL_EVENT_IDS or GCAL_RECURRING_EVENT_IDS) already
// names it.
func (c Meeting) IsAttachedTo(h *org.Headline) bool {
	if h == nil {
		return false
	}
	for _, id := range strings.Fields(h.Properties[c.Kind.IDsProperty()]) {
		if id == c.ID {
			return true
		}
	}
	return false
}

// Less reports whether a should rank ahead of b for the "gM" picker's
// default highlight, and for choosing a series' representative
// occurrence: a meeting currently in progress *and accepted* always ranks
// ahead of one that isn't; between two such meetings, the one that's
// shorter ranks first — so a quick standup you're nominally "in" right
// now doesn't get buried under an hours-long meeting that's also
// technically ongoing. Otherwise (including an in-progress meeting you
// only RSVP'd "maybe" to, or haven't responded to): whichever starts
// closest to now, upcoming or recently ended alike. Not "soonest upcoming
// always beats any past occurrence": a meeting that just ended is exactly
// the kind of thing you're likely reaching for "gM" to attach something
// to, so it needs to surface near the top rather than sink beneath every
// future meeting just for having already started.
func Less(a, b Meeting, now time.Time) bool {
	aPri, bPri := a.Prioritized(now), b.Prioritized(now)
	if aPri != bPri {
		return aPri
	}
	if aPri {
		return a.End.Sub(a.When) < b.End.Sub(b.When)
	}
	return a.When.Sub(now).Abs() < b.When.Sub(now).Abs()
}

// DefaultIndex returns the index, within candidates (as returned by
// Index.Candidates — sorted chronologically, not by relevance), of the
// meeting the picker should highlight when it first opens: whichever one
// Less would rank first. Kept separate from Candidates' own ordering so
// the list can read chronologically while still landing the cursor on the
// meeting you're most likely attaching to right now. 0 for an empty
// slice.
func DefaultIndex(candidates []Meeting, now time.Time) int {
	best := 0
	for i := 1; i < len(candidates); i++ {
		if Less(candidates[i], candidates[best], now) {
			best = i
		}
	}
	return best
}

// Filter returns every candidate whose title, or one of its tags,
// contains filter, case-insensitively — plain substring matching, since a
// meeting title is arbitrary text with no natural prefix or shortcut to
// match on. Matching tags too lets typing an attendee's name (e.g.
// "alice", matching the "@alice" tag :sync-calendar stamps on) find a
// meeting whose title doesn't happen to mention them.
func Filter(candidates []Meeting, filter string) []Meeting {
	if filter == "" {
		return candidates
	}
	lower := strings.ToLower(filter)
	var out []Meeting
	for _, c := range candidates {
		if strings.Contains(strings.ToLower(c.Title), lower) || tagsContain(c.Tags, lower) {
			out = append(out, c)
		}
	}
	return out
}

func tagsContain(tags map[string]bool, lower string) bool {
	for t := range tags {
		if strings.Contains(strings.ToLower(t), lower) {
			return true
		}
	}
	return false
}

// sortChronologically orders ms by When, ties broken by title then ID so
// the order is deterministic.
func sortChronologically(ms []Meeting) {
	sort.SliceStable(ms, func(i, j int) bool {
		if !ms[i].When.Equal(ms[j].When) {
			return ms[i].When.Before(ms[j].When)
		}
		if ms[i].Title != ms[j].Title {
			return ms[i].Title < ms[j].Title
		}
		return ms[i].ID < ms[j].ID
	})
}

// CursorTarget picks the event a calendar view should land on: among
// headlines with a GCAL_START at or before now, the one with the latest
// start that's still in progress (now before its GCAL_END) wins outright;
// failing that, the one with the latest start overall (in progress or not
// — an unparseable/missing GCAL_END, or one already elapsed, both fall
// here) — i.e. "the current meeting, or the prior one if none is current".
// nil if nothing has a GCAL_START at or before now at all (every synced
// event is still upcoming, or there are none).
func CursorTarget(headlines []*org.Headline, now time.Time) *org.Headline {
	var current, prior *org.Headline
	var currentStart, priorStart time.Time
	for _, h := range headlines {
		start, ok := TimeProperty(h, "GCAL_START")
		if !ok || start.After(now) {
			continue
		}
		if end, ok := TimeProperty(h, "GCAL_END"); ok && now.Before(end) {
			if current == nil || start.After(currentStart) {
				current, currentStart = h, start
			}
			continue
		}
		if prior == nil || start.After(priorStart) {
			prior, priorStart = h, start
		}
	}
	if current != nil {
		return current
	}
	return prior
}
