package meetings

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sburnett/orgtd/internal/org"
)

var t0 = time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

func parse(t *testing.T, name, text string) *org.File {
	t.Helper()
	f, err := org.Parse(strings.NewReader(text), name)
	if err != nil {
		t.Fatalf("org.Parse: %v", err)
	}
	return f
}

// event renders one synced calendar occurrence headline. recurID may be
// empty for a one-off event.
func event(id, recurID, title string, start, end time.Time, tags ...string) string {
	tagStr := ""
	if len(tags) > 0 {
		tagStr = " :" + strings.Join(tags, ":") + ":"
	}
	s := fmt.Sprintf("* %s%s\n  :PROPERTIES:\n  :GCAL_EVENT_ID: %s\n", title, tagStr, id)
	if recurID != "" {
		s += fmt.Sprintf("  :GCAL_RECURRING_EVENT_ID: %s\n", recurID)
	}
	s += fmt.Sprintf("  :GCAL_START: %s\n  :GCAL_END: %s\n  :GCAL_HTML_LINK: https://cal/%s\n  :END:\n",
		start.Format(time.RFC3339), end.Format(time.RFC3339), id)
	return s
}

func titles(hs []*org.Headline) []string {
	out := make([]string, len(hs))
	for i, h := range hs {
		out[i] = h.Title
	}
	return out
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestIdentityPrefersRecurringID(t *testing.T) {
	f := parse(t, "c.org", event("occ-1", "series-1", "Standup", t0, t0.Add(time.Hour))+event("solo", "", "One-off", t0, t0.Add(time.Hour)))
	if k, ok := Identity(f.Headlines[0]); !ok || k != (Key{Recurring, "series-1"}) {
		t.Errorf("Identity(recurring occurrence) = %v, %v; want {Recurring series-1}", k, ok)
	}
	if k, ok := Identity(f.Headlines[1]); !ok || k != (Key{OneOff, "solo"}) {
		t.Errorf("Identity(one-off) = %v, %v; want {OneOff solo}", k, ok)
	}
	plain := parse(t, "p.org", "* TODO Plain\n")
	if _, ok := Identity(plain.Headlines[0]); ok {
		t.Error("Identity(plain entry) ok = true, want false")
	}
}

func TestKindProperties(t *testing.T) {
	if OneOff.IDsProperty() != "GCAL_EVENT_IDS" || Recurring.IDsProperty() != "GCAL_RECURRING_EVENT_IDS" {
		t.Error("IDsProperty names changed")
	}
	if OneOff.TagsIDsProperty() == OneOff.IDsProperty() {
		t.Error("TagsIDsProperty must differ from IDsProperty, or tag records would link to their own meeting")
	}
}

func TestLessPrefersAcceptedInProgressThenShorterThenClosest(t *testing.T) {
	now := t0
	inLong := Meeting{When: now.Add(-time.Hour), End: now.Add(3 * time.Hour), Accepted: true}
	inShort := Meeting{When: now.Add(-10 * time.Minute), End: now.Add(20 * time.Minute), Accepted: true}
	inMaybe := Meeting{When: now.Add(-time.Minute), End: now.Add(time.Hour), Accepted: false}
	soon := Meeting{When: now.Add(5 * time.Minute), End: now.Add(time.Hour)}
	if !Less(inShort, inLong, now) {
		t.Error("shorter accepted in-progress meeting should rank ahead of a longer one")
	}
	if !Less(inLong, inMaybe, now) {
		t.Error("accepted in-progress should beat tentative in-progress")
	}
	if !Less(inMaybe, soon, now) {
		t.Error("tentative started 1m ago is closer to now than one starting in 5m")
	}
	if got := DefaultIndex([]Meeting{soon, inMaybe, inLong, inShort}, now); got != 3 {
		t.Errorf("DefaultIndex = %d, want 3 (the short accepted in-progress meeting)", got)
	}
	if got := DefaultIndex(nil, now); got != 0 {
		t.Errorf("DefaultIndex(nil) = %d, want 0", got)
	}
}

func TestFilterMatchesTitleOrTagCaseInsensitively(t *testing.T) {
	cs := []Meeting{
		{Title: "Planning sync", Tags: map[string]bool{"@alice": true}},
		{Title: "Retro", Tags: map[string]bool{"@bob": true}},
	}
	if got := Filter(cs, "ALICE"); len(got) != 1 || got[0].Title != "Planning sync" {
		t.Errorf("Filter by tag = %v", got)
	}
	if got := Filter(cs, "retro"); len(got) != 1 || got[0].Title != "Retro" {
		t.Errorf("Filter by title = %v", got)
	}
	if got := Filter(cs, ""); len(got) != 2 {
		t.Errorf("Filter(\"\") = %v, want all", got)
	}
}

func TestCandidatesDedupesSeriesAndPicksRelevantOccurrence(t *testing.T) {
	// A daily standup: yesterday's, today's (in progress now), tomorrow's.
	cal := parse(t, "calendar.org",
		event("s-1", "series", "Standup (yesterday)", t0.Add(-24*time.Hour), t0.Add(-23*time.Hour), "recurring")+
			event("s-2", "series", "Standup (today)", t0.Add(-10*time.Minute), t0.Add(20*time.Minute), "recurring")+
			event("s-3", "series", "Standup (tomorrow)", t0.Add(24*time.Hour), t0.Add(25*time.Hour), "recurring")+
			event("solo", "", "Kickoff", t0.Add(2*time.Hour), t0.Add(3*time.Hour)))
	cal.Headlines[1].Properties["GCAL_SELF_RESPONSE_STATUS"] = "accepted"
	ix := Build([]*org.File{cal}, nil)

	got := ix.Candidates(t0)
	if len(got) != 2 {
		t.Fatalf("Candidates = %d meetings, want 2 (series deduped): %+v", len(got), got)
	}
	// Chronological by representative occurrence: the series' in-progress
	// occurrence (today) starts before the kickoff.
	if got[0].Title != "Standup (today)" || got[0].Key != (Key{Recurring, "series"}) {
		t.Errorf("first candidate = %+v, want today's standup occurrence", got[0])
	}
	if got[1].Title != "Kickoff" {
		t.Errorf("second candidate = %q, want Kickoff", got[1].Title)
	}
	if got[0].Link != "https://cal/s-2" || !got[0].Accepted {
		t.Errorf("representative display fields = %+v, want link and accepted from today's occurrence", got[0])
	}
}

func TestCandidatesSkipsOccurrencesWithoutAStart(t *testing.T) {
	f := parse(t, "c.org", "* Broken\n  :PROPERTIES:\n  :GCAL_EVENT_ID: x\n  :END:\n")
	if got := Build([]*org.File{f}, nil).Candidates(t0); len(got) != 0 {
		t.Errorf("Candidates = %+v, want none", got)
	}
}

func TestLinkedItemsExplicitAndTagLinks(t *testing.T) {
	cal := parse(t, "calendar.org", event("kick", "", "Kickoff", t0, t0.Add(time.Hour), "@alice"))
	work := parse(t, "work.org", `* TODO Attached explicitly
  :PROPERTIES:
  :GCAL_EVENT_IDS: other kick
  :END:
* TODO Tag matched :@alice:
* TODO Both ways :@alice:
  :PROPERTIES:
  :GCAL_EVENT_IDS: kick
  :END:
* TODO Unrelated :@bob:
* DONE Finished :@alice:
`)
	ix := Build([]*org.File{cal, work}, nil)
	got := titles(ix.LinkedItems(OneOff, "kick"))
	want := []string{"Attached explicitly", "Tag matched", "Both ways"}
	if !eq(got, want) {
		t.Errorf("LinkedItems = %v, want %v (tree order, once each, DONE excluded)", got, want)
	}
	if got := ix.LinkedItems(OneOff, "nope"); len(got) != 0 {
		t.Errorf("LinkedItems(unknown) = %v, want none", got)
	}
}

func TestSeriesTagNeverLinks(t *testing.T) {
	cal := parse(t, "calendar.org", event("o1", "series", "Standup", t0, t0.Add(time.Hour), "recurring"))
	work := parse(t, "work.org", "* TODO Tagged recurring :recurring:\n")
	ix := Build([]*org.File{cal, work}, nil)
	if got := ix.LinkedItems(Recurring, "series"); len(got) != 0 {
		t.Errorf("LinkedItems = %v, want none: the recurring system tag never counts", titles(got))
	}
	if ix.Tags(Recurring, "series")[SeriesTag] {
		t.Error("Tags includes the series tag")
	}
}

func TestCalendarEventsAreNotTagLinkedToEachOther(t *testing.T) {
	cal := parse(t, "calendar.org",
		event("a", "", "Meeting A", t0, t0.Add(time.Hour), "@alice")+
			event("b", "", "Meeting B", t0.Add(2*time.Hour), t0.Add(3*time.Hour), "@alice"))
	ix := Build([]*org.File{cal}, nil)
	if got := ix.LinkedItems(OneOff, "a"); len(got) != 0 {
		t.Errorf("LinkedItems = %v, want none: a synced event isn't linkable by tag", titles(got))
	}
	if got := ix.TagLinked(cal.Headlines[0], t0); len(got) != 0 {
		t.Errorf("TagLinked(event) = %+v, want none", got)
	}
}

func TestMeetingTagsRecordTagsLinkButRecordItselfDoesNot(t *testing.T) {
	cal := parse(t, "calendar.org", event("kick", "", "Kickoff", t0, t0.Add(time.Hour)))
	tags := parse(t, "meeting-tags.org", `* Kickoff :@carol:
  :PROPERTIES:
  :MEETING_TAG_EVENT_IDS: kick
  :END:
`)
	work := parse(t, "work.org", "* TODO For carol :@carol:\n")
	ix := Build([]*org.File{cal, tags, work}, tags)

	if got := titles(ix.LinkedItems(OneOff, "kick")); !eq(got, []string{"For carol"}) {
		t.Errorf("LinkedItems = %v, want just the ordinary entry (the tag record itself excluded)", got)
	}
	if !ix.Tags(OneOff, "kick")["@carol"] {
		t.Error("Tags lacks the recorded tag")
	}
	if rec := ix.TagRecord(OneOff, "kick"); rec == nil || rec.Title != "Kickoff" {
		t.Errorf("TagRecord = %v, want the Kickoff record", rec)
	}
	if ix.TagRecord(OneOff, "other") != nil {
		t.Error("TagRecord(unknown) != nil")
	}
	// Without the tags file, the recorded tag is gone.
	if Build([]*org.File{cal, work}, nil).LinkedItems(OneOff, "kick") != nil {
		t.Error("with no tags file, nothing should link")
	}
}

func TestTagLinkedReturnsSharedTagMeetingsChronologically(t *testing.T) {
	cal := parse(t, "calendar.org",
		event("late", "", "Late", t0.Add(5*time.Hour), t0.Add(6*time.Hour), "@alice")+
			event("early", "", "Early", t0.Add(time.Hour), t0.Add(2*time.Hour), "@alice", "@bob")+
			event("none", "", "Other", t0, t0.Add(time.Hour), "@zed"))
	work := parse(t, "work.org", "* TODO Needs alice :@alice:\n* TODO Untagged\n* TODO Series only :recurring:\n")
	ix := Build([]*org.File{cal, work}, nil)

	got := ix.TagLinked(work.Headlines[0], t0)
	if len(got) != 2 || got[0].Title != "Early" || got[1].Title != "Late" {
		t.Errorf("TagLinked = %+v, want Early then Late", got)
	}
	if got := ix.TagLinked(work.Headlines[1], t0); got != nil {
		t.Errorf("TagLinked(untagged) = %+v, want nil", got)
	}
	if got := ix.TagLinked(work.Headlines[2], t0); got != nil {
		t.Errorf("TagLinked(series tag only) = %+v, want nil", got)
	}
	if got := ix.TagLinked(nil, t0); got != nil {
		t.Errorf("TagLinked(nil) = %+v, want nil", got)
	}
}

func TestEventLookups(t *testing.T) {
	cal := parse(t, "calendar.org",
		event("a", "series", "Standup", t0, t0.Add(time.Hour))+
			event("b", "series", "Standup", t0.Add(24*time.Hour), t0.Add(25*time.Hour)))
	ix := Build([]*org.File{cal}, nil)

	title, url, when, hasWhen, ok := ix.EventByProperty("GCAL_RECURRING_EVENT_ID", "series")
	if !ok || title != "Standup" || url != "https://cal/a" || !hasWhen || !when.Equal(t0) {
		t.Errorf("EventByProperty = %q %q %v %v %v, want the first occurrence", title, url, when, hasWhen, ok)
	}
	if _, _, _, _, ok := ix.EventByProperty("GCAL_EVENT_ID", "missing"); ok {
		t.Error("EventByProperty(missing) ok = true")
	}
	if got, ok := ix.EventTimeByLink("https://cal/b"); !ok || !got.Equal(t0.Add(24*time.Hour)) {
		t.Errorf("EventTimeByLink = %v, %v", got, ok)
	}
	if _, ok := ix.EventTimeByLink("https://cal/zzz"); ok {
		t.Error("EventTimeByLink(missing) ok = true")
	}
}

func TestEventsForSortsChronologically(t *testing.T) {
	cal := parse(t, "calendar.org",
		event("b", "series", "Later", t0.Add(24*time.Hour), t0.Add(25*time.Hour))+
			event("a", "series", "Sooner", t0, t0.Add(time.Hour))+
			event("solo", "", "Solo", t0.Add(time.Hour), t0.Add(2*time.Hour))+
			event("x", "", "Unwanted", t0, t0.Add(time.Hour)))
	ix := Build([]*org.File{cal}, nil)
	got := titles(ix.EventsFor([]string{"series"}, []string{"solo"}))
	if want := []string{"Sooner", "Solo", "Later"}; !eq(got, want) {
		t.Errorf("EventsFor = %v, want %v", got, want)
	}
	if got := ix.EventsFor(nil, nil); got != nil {
		t.Errorf("EventsFor(nil, nil) = %v, want nil", got)
	}
}

func TestIsAttachedToAndFromEvent(t *testing.T) {
	f := parse(t, "w.org", "* TODO Task\n  :PROPERTIES:\n  :GCAL_RECURRING_EVENT_IDS: s1 s2\n  :END:\n")
	task := f.Headlines[0]
	if !(Meeting{Key: Key{Recurring, "s2"}}).IsAttachedTo(task) {
		t.Error("IsAttachedTo(s2) = false")
	}
	if (Meeting{Key: Key{OneOff, "s2"}}).IsAttachedTo(task) {
		t.Error("IsAttachedTo matched the wrong kind")
	}
	if (Meeting{Key: Key{Recurring, "s2"}}).IsAttachedTo(nil) {
		t.Error("IsAttachedTo(nil) = true")
	}
	cal := parse(t, "c.org", event("e", "", "Ev", t0, t0.Add(time.Hour)))
	m, ok := FromEvent(cal.Headlines[0])
	if !ok || m.Key != (Key{OneOff, "e"}) || m.Title != "Ev" || m.Link != "https://cal/e" {
		t.Errorf("FromEvent = %+v, %v", m, ok)
	}
	if _, ok := FromEvent(task); ok {
		t.Error("FromEvent(plain task) ok = true")
	}
}

func TestCursorTargetPrefersTheMeetingInProgress(t *testing.T) {
	f := parse(t, "c.org",
		event("past", "", "Earlier", t0.Add(-3*time.Hour), t0.Add(-2*time.Hour))+
			event("now", "", "In progress", t0.Add(-10*time.Minute), t0.Add(20*time.Minute))+
			event("recent", "", "Just ended", t0.Add(-time.Hour), t0.Add(-30*time.Minute))+
			event("later", "", "Upcoming", t0.Add(time.Hour), t0.Add(2*time.Hour)))
	if got := CursorTarget(f.Headlines, t0); got == nil || got.Title != "In progress" {
		t.Errorf("CursorTarget = %v, want the in-progress meeting", got)
	}
}

func TestCursorTargetFallsBackToTheMostRecentlyStarted(t *testing.T) {
	f := parse(t, "c.org",
		event("a", "", "Older", t0.Add(-3*time.Hour), t0.Add(-2*time.Hour))+
			event("b", "", "Newer", t0.Add(-time.Hour), t0.Add(-30*time.Minute))+
			event("c", "", "Upcoming", t0.Add(time.Hour), t0.Add(2*time.Hour)))
	if got := CursorTarget(f.Headlines, t0); got == nil || got.Title != "Newer" {
		t.Errorf("CursorTarget = %v, want the most recently started meeting", got)
	}
}

func TestCursorTargetIsNilWhenEverythingIsUpcoming(t *testing.T) {
	f := parse(t, "c.org", event("a", "", "Upcoming", t0.Add(time.Hour), t0.Add(2*time.Hour)))
	if got := CursorTarget(f.Headlines, t0); got != nil {
		t.Errorf("CursorTarget = %v, want nil", got)
	}
	if got := CursorTarget(nil, t0); got != nil {
		t.Errorf("CursorTarget(nil) = %v, want nil", got)
	}
}
