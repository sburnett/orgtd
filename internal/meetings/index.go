package meetings

import (
	"sort"
	"strings"
	"time"

	"github.com/sburnett/orgtd/internal/org"
)

// Occurrence is one synced calendar event headline: a single instance of
// a meeting (every occurrence of a recurring series is its own headline).
type Occurrence struct {
	Headline *org.Headline
	Key
	Start, End       time.Time
	HasStart, HasEnd bool // whether GCAL_START/GCAL_END parsed
}

// Index is an immutable snapshot of the meetings in a set of org files and
// of which entries link to which. Build one with Build and reuse it until
// the files change; see the package comment.
type Index struct {
	occurrences []Occurrence
	byKey       map[Key][]int // indexes into occurrences, in occurrence order

	// tags is the tag set per meeting (see Meeting.Tags), for every
	// meeting known from an ID property or a tag record. tagMeetings is its inverse: tag
	// → meetings carrying it, which is what makes "which meetings does this
	// entry's tag set match" a few lookups instead of a workspace scan.
	tags        map[Key]map[string]bool
	tagMeetings map[string][]Key

	// records maps a meeting to the meeting-tags-file record that names it
	// (the first in tree order, if several do).
	records map[Key]*org.Headline

	// linked is, per meeting, the entries linked to it (see LinkedItems),
	// in file/tree order.
	linked map[Key][]*org.Headline

	// byProp finds the first headline whose property (GCAL_EVENT_ID or
	// GCAL_RECURRING_EVENT_ID) has a given value; eventTimeByLink finds the
	// start time of the first headline with a given GCAL_HTML_LINK whose
	// GCAL_START parses. See EventByProperty and EventTimeByLink.
	byProp          map[propKey]*org.Headline
	eventTimeByLink map[string]time.Time
}

type propKey struct{ prop, value string }

var kinds = [...]Kind{OneOff, Recurring}

// Build indexes files (every loaded org file, in workspace order).
// tagsFile is the meeting-tags file recording durable per-meeting tags, or
// nil if there isn't one yet; it should also be among files if loaded —
// its records are excluded from linking and read for their tags here.
func Build(files []*org.File, tagsFile *org.File) *Index {
	ix := &Index{
		byKey:           make(map[Key][]int),
		tags:            make(map[Key]map[string]bool),
		tagMeetings:     make(map[string][]Key),
		records:         make(map[Key]*org.Headline),
		linked:          make(map[Key][]*org.Headline),
		byProp:          make(map[propKey]*org.Headline),
		eventTimeByLink: make(map[string]time.Time),
	}

	// Pass 1: occurrences, the property lookups, and each meeting's own
	// tags (from every headline carrying its ID property, which in
	// practice is only its synced occurrences).
	base := make(map[Key]map[string]bool)
	for _, f := range files {
		org.Walk(f.Headlines, func(h *org.Headline) {
			for _, k := range kinds {
				id := h.Properties[k.IDProperty()]
				if id == "" {
					continue
				}
				pk := propKey{k.IDProperty(), id}
				if _, seen := ix.byProp[pk]; !seen {
					ix.byProp[pk] = h
				}
				key := Key{k, id}
				set := base[key]
				if set == nil {
					set = make(map[string]bool)
					base[key] = set
				}
				for _, t := range h.Tags {
					if t != SeriesTag {
						set[t] = true
					}
				}
			}
			if link := h.Properties["GCAL_HTML_LINK"]; link != "" {
				if _, seen := ix.eventTimeByLink[link]; !seen {
					if start, ok := TimeProperty(h, "GCAL_START"); ok {
						ix.eventTimeByLink[link] = start
					}
				}
			}
			if key, ok := Identity(h); ok {
				start, hasStart := TimeProperty(h, "GCAL_START")
				end, hasEnd := TimeProperty(h, "GCAL_END")
				ix.byKey[key] = append(ix.byKey[key], len(ix.occurrences))
				ix.occurrences = append(ix.occurrences, Occurrence{Headline: h, Key: key, Start: start, End: end, HasStart: hasStart, HasEnd: hasEnd})
			}
		})
	}

	// Meeting-tags records: durable tags per meeting.
	if tagsFile != nil {
		org.Walk(tagsFile.Headlines, func(h *org.Headline) {
			for _, k := range kinds {
				for _, id := range strings.Fields(h.Properties[k.TagsIDsProperty()]) {
					key := Key{k, id}
					if _, seen := ix.records[key]; !seen {
						ix.records[key] = h
					}
				}
			}
		})
	}

	// Union of a meeting's own tags and its record's, for every meeting we
	// know anything about, plus the tag → meetings inverse (only for
	// meetings that have occurrences — the only ones entries can link to).
	known := make(map[Key]bool, len(base)+len(ix.records))
	for key := range base {
		known[key] = true
	}
	for key := range ix.records {
		known[key] = true
	}
	for key := range known {
		set := make(map[string]bool, len(base[key]))
		for t := range base[key] {
			set[t] = true
		}
		if rec := ix.records[key]; rec != nil {
			for _, t := range rec.Tags {
				if t != SeriesTag {
					set[t] = true
				}
			}
		}
		ix.tags[key] = set
		if ix.byKey[key] != nil {
			for t := range set {
				ix.tagMeetings[t] = append(ix.tagMeetings[t], key)
			}
		}
	}

	// Pass 2: link entries to meetings. An entry (not DONE/CANCELLED) is
	// linked to a meeting if it names the meeting's ID in its explicit
	// attachment property; or, failing that, if it's an ordinary entry
	// (not itself a synced event, not a tag record) sharing a tag with the
	// meeting. At most once per meeting; an entry can link to several.
	for _, f := range files {
		org.Walk(f.Headlines, func(h *org.Headline) {
			if org.IsDoneKeyword(h.Keyword) {
				return
			}
			var hit map[Key]bool
			add := func(key Key) {
				if hit == nil {
					hit = make(map[Key]bool)
				}
				if !hit[key] {
					hit[key] = true
					ix.linked[key] = append(ix.linked[key], h)
				}
			}
			for _, k := range kinds {
				for _, id := range strings.Fields(h.Properties[k.IDsProperty()]) {
					if key := (Key{k, id}); ix.byKey[key] != nil {
						add(key)
					}
				}
			}
			if _, isEvent := Identity(h); isEvent || IsTagRecord(h) {
				return
			}
			for _, t := range h.Tags {
				for _, key := range ix.tagMeetings[t] {
					add(key)
				}
			}
		})
	}
	return ix
}

// Occurrences returns every synced calendar event headline across the
// files, in file/tree order. The slice is shared; don't modify it.
func (ix *Index) Occurrences() []Occurrence { return ix.occurrences }

// LinkedItems returns every entry linked to the meeting — explicitly
// attached, or sharing a tag with it — in file/tree order, each at most
// once. DONE/CANCELLED entries are excluded (already resolved, nothing
// left to revisit), and the tag path excludes synced calendar events
// themselves (a meeting can't be "linked to" its own occurrence, or a
// sibling occurrence of the same series, just by carrying the same
// attendee tags) and meeting-tags records. The slice is shared; don't
// modify it (copy before sorting).
func (ix *Index) LinkedItems(k Kind, id string) []*org.Headline {
	return ix.linked[Key{k, id}]
}

// Tags returns the tag set used to link entries to the meeting by shared
// tag: every tag carried by any synced occurrence (SeriesTag excluded)
// plus any the meeting-tags file records for it. Empty (never nil) if the
// meeting has no occurrences or tags. Read-only and shared.
func (ix *Index) Tags(k Kind, id string) map[string]bool {
	if set, ok := ix.tags[Key{k, id}]; ok {
		return set
	}
	return map[string]bool{}
}

// TagRecord returns the meeting-tags-file record naming the meeting, or
// nil if there's none (or no meeting-tags file).
func (ix *Index) TagRecord(k Kind, id string) *org.Headline {
	return ix.records[Key{k, id}]
}

// representative returns the meeting for key, with the display fields
// taken from whichever of its occurrences is most relevant at now (see
// Less). Picking it with Less rather than a start-only comparison matters
// for a series with more than one instance synced at once (a daily
// standup: today's and tomorrow's both fall inside the sync window): a
// "soonest upcoming wins" comparison would treat today's already-started
// occurrence as simply past and lose it to tomorrow's, hiding the fact
// that the series is in progress right now. Occurrences without a
// parseable start are skipped; ok is false if none remain.
func (ix *Index) representative(key Key, now time.Time) (Meeting, bool) {
	var best Meeting
	found := false
	for _, i := range ix.byKey[key] {
		o := ix.occurrences[i]
		if !o.HasStart {
			continue
		}
		cand := Meeting{
			Key:      key,
			Title:    o.Headline.Title,
			Link:     o.Headline.Properties["GCAL_HTML_LINK"],
			When:     o.Start,
			End:      o.End,
			Accepted: o.Headline.Properties["GCAL_SELF_RESPONSE_STATUS"] == "accepted",
		}
		if !found || Less(cand, best, now) {
			best, found = cand, true
		}
	}
	if found {
		best.Tags = ix.tags[key]
	}
	return best, found
}

// Candidates returns one Meeting per distinct meeting found — a recurring
// series deduped by GCAL_RECURRING_EVENT_ID, a one-off event by
// GCAL_EVENT_ID — each described by its most relevant occurrence as of
// now, sorted chronologically by that occurrence's start so a picker
// reads top-to-bottom in time order. Only meaningful while
// :sync-calendar has synced at least one relevant instance; a meeting
// whose every occurrence has aged out of the sync window simply won't
// appear until it runs again.
func (ix *Index) Candidates(now time.Time) []Meeting {
	out := make([]Meeting, 0, len(ix.byKey))
	for key := range ix.byKey {
		if m, ok := ix.representative(key, now); ok {
			out = append(out, m)
		}
	}
	sortChronologically(out)
	return out
}

// TagLinked returns every meeting that shares a tag with entry h — the
// entry-side counterpart of LinkedItems' tag matching, so a tag-matched
// meeting can be treated the same as one attached explicitly. Empty if h
// is itself a synced calendar event (never linked to any meeting by tag,
// including its own), or has no tags other than SeriesTag. Chronological
// by representative occurrence as of now, like Candidates.
func (ix *Index) TagLinked(h *org.Headline, now time.Time) []Meeting {
	if h == nil {
		return nil
	}
	if _, isEvent := Identity(h); isEvent {
		return nil
	}
	var keys map[Key]bool
	for _, t := range h.Tags {
		for _, key := range ix.tagMeetings[t] {
			if keys == nil {
				keys = make(map[Key]bool)
			}
			keys[key] = true
		}
	}
	if len(keys) == 0 {
		return nil
	}
	var out []Meeting
	for key := range keys {
		if m, ok := ix.representative(key, now); ok {
			out = append(out, m)
		}
	}
	sortChronologically(out)
	return out
}

// EventByProperty finds the first headline whose property prop
// (GCAL_EVENT_ID or GCAL_RECURRING_EVENT_ID) equals id — i.e. one
// :sync-calendar wrote — returning its title, GCAL_HTML_LINK and
// GCAL_START (hasWhen false if that's missing or unparseable). ok is
// false if there's no such headline.
func (ix *Index) EventByProperty(prop, id string) (title, url string, when time.Time, hasWhen, ok bool) {
	h := ix.byProp[propKey{prop, id}]
	if h == nil {
		return "", "", time.Time{}, false, false
	}
	when, hasWhen = TimeProperty(h, "GCAL_START")
	return h.Title, h.Properties["GCAL_HTML_LINK"], when, hasWhen, true
}

// EventTimeByLink returns the start time of a synced calendar event whose
// GCAL_HTML_LINK is url, for recovering a meeting's time for an entry
// whose *_LINKS property only captured its title and link at attach time.
// ok is false if no loaded event matches (the event has aged out of the
// sync window).
func (ix *Index) EventTimeByLink(url string) (time.Time, bool) {
	t, ok := ix.eventTimeByLink[url]
	return t, ok
}

// EventsFor returns every synced calendar event occurrence whose meeting
// ID appears in recurringIDs (for a recurring series) or oneOffIDs (for a
// one-off event), sorted chronologically by start. For showing which
// occurrences a meeting-tags record's IDs currently resolve to.
func (ix *Index) EventsFor(recurringIDs, oneOffIDs []string) []*org.Headline {
	if len(recurringIDs) == 0 && len(oneOffIDs) == 0 {
		return nil
	}
	want := make(map[Key]bool, len(recurringIDs)+len(oneOffIDs))
	for _, id := range recurringIDs {
		want[Key{Recurring, id}] = true
	}
	for _, id := range oneOffIDs {
		want[Key{OneOff, id}] = true
	}
	var matches []Occurrence
	for _, o := range ix.occurrences {
		if want[o.Key] {
			matches = append(matches, o)
		}
	}
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].Start.Before(matches[j].Start) })
	out := make([]*org.Headline, len(matches))
	for i, o := range matches {
		out[i] = o.Headline
	}
	return out
}
