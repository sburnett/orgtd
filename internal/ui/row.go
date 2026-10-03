package ui

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/sburnett/orgtd/internal/org"
)

// rowKind says what a row is. It is the tag for row's per-kind fields: a
// row only uses the fields documented under its own kind, and kind decides
// how the row is rendered (renderRowWithBg), searched (rowSearchText),
// compared (sameRow) and navigated.
type rowKind int

const (
	// rowHeadline is an ordinary outline headline (the zero value): a
	// foldable entry at its real depth in its own file. Every per-entry
	// command (i, dd, r, gd, marks, ...) works on such a row by resolving
	// it to row.headline.
	rowHeadline rowKind = iota

	// rowFile is a file's own header row in the outline. Uses file;
	// headline is nil.
	rowFile

	// rowBody is one line of a headline's free-text body, shown under its
	// title when expanded (see appendBodyLines). text is that line, which
	// may itself be empty — a blank line in the body — so kind, not
	// text != "", is the reliable marker. headline is still set to the
	// owning headline (not nil), so commands like i/dd/r/gd resolve to it
	// exactly as if the cursor were on the title row itself.
	rowBody

	// rowSection is a flush-left group header: an agenda section
	// ("Overdue"), a calendar day, or a tag name. text is its title;
	// headline is nil.
	rowSection

	// rowText is a plain read-only informational line (:config, :log,
	// :diff, :help), rendered flush left and never interactive. text is
	// the line, which may be empty (a blank line, as :help's embedded
	// README naturally has plenty of).
	rowText

	// rowAgendaItem is an agenda item row: an entry shown under a section
	// or meeting header. A date-based item sets agendaLabel ("Scheduled" or
	// "Deadline"), agendaDate, agendaRepeater and agendaMissed; a Next
	// Actions entry has no date, so kind — not agendaLabel — is the
	// reliable marker. An item in the Meetings section also sets
	// meetingItemTitle/meetingItemStart (see below).
	rowAgendaItem

	// rowMeetingHeader is a meeting-group header row in the agenda's
	// "Meetings" section (see appendMeetingsSection): a label ("<title> —
	// <when>") to group the items below it under, one level deeper than
	// the section header and one shallower than its items (see rowLevel).
	// Uses meetingTitle/meetingStart/meetingEnd. Not itself a headline
	// (headline is nil), so none of the outline's per-headline commands
	// apply to it.
	rowMeetingHeader

	// rowCalendarEvent is a calendar-event row (see
	// appendCalendarHeadlines, appendMeetingTagsHeadlines): rendered with
	// its GCAL_START/GCAL_END time before the title (see
	// renderCalendarItemRowWithBg), rather than the outline's usual
	// keyword-first layout.
	rowCalendarEvent

	// rowMeetingTagsRecord is a meeting-tags.org record's own row in
	// meetingTagsView (see appendMeetingTagsHeadlines): rendered without
	// the indent/fold columns every other headline row reserves (see
	// renderMeetingTagsRecordRowWithBg) — a record is always effectively
	// top-level and never has foldable content in practice, so those
	// columns would just be dead space.
	rowMeetingTagsRecord

	// rowCalendarLinked is a row for an entry elsewhere in the workspace
	// linked to a calendar event — attached via "gM", or sharing a tag
	// with it (see linkedMeetingItems) — shown right after the event
	// itself in calendarView regardless of whether the event is folded
	// (see appendCalendarHeadlines), indented to level (one deeper than
	// the event) rather than the headline's own real level in its own file
	// (see renderCalendarLinkedItemRowWithBg). Uses linkedFromEvent.
	rowCalendarLinked

	// rowTagsItem is a row in tagsView (see appendTagsRows): a flat,
	// single-line row for an entry carrying tagsItemTag, shown under that
	// tag's section header — same rendering shape as rowCalendarLinked (no
	// fold/body/children of its own, a "[file › parent]" place tag
	// instead), since an entry with several tags legitimately gets one row
	// per tag, all sharing the same headline pointer.
	rowTagsItem
)

// row is one visible line of the current view: see rowKind for what each
// kind of row is and which fields it uses. A row is a plain value; rows
// that stand for the same headline in more than one place are told apart
// by sameRow, using the identifying fields noted below.
type row struct {
	kind     rowKind
	file     *org.File // rowFile
	headline *org.Headline
	level    int    // structural level used by level-aware navigation (rowLevel); file/section rows are 0
	text     string // rowSection: the title; rowBody: the body line; rowText: the line

	// rowAgendaItem (date-based items; empty for a Next Actions entry).
	agendaLabel    string    // "Scheduled" or "Deadline"
	agendaDate     time.Time // the date this row is shown for
	agendaRepeater string    // e.g. "+1w", if agendaDate was computed from a recurring timestamp
	agendaMissed   int       // occurrences skipped since agendaDate, shown as "(Nx)"; only ever set on an Overdue row

	// rowMeetingHeader.
	meetingTitle string
	meetingStart time.Time
	meetingEnd   time.Time

	// rowAgendaItem in the Meetings section: echo the meetingTitle/
	// meetingStart of the rowMeetingHeader it's nested under. An item
	// linked to more than one meeting legitimately gets one row per
	// meeting, all sharing the same headline — so sameRow needs these to
	// tell those rows apart; without them, search's "n"/"N" (see
	// findMatch) could never advance past the first such row, since every
	// later one would look identical to it.
	meetingItemTitle string
	meetingItemStart time.Time

	// rowCalendarLinked: the calendar event headline this row is nested
	// under. An entry linked to more than one event gets one row per
	// event, all sharing the same linked headline — so sameRow needs this
	// to tell those rows apart, for the same reason as meetingItemTitle.
	linkedFromEvent *org.Headline

	// rowTagsItem: the tag whose section this row is under (an entry with
	// several tags gets one row per tag).
	tagsItemTag string
}

// sameRow reports whether a and b refer to the same logical row —
// identity, not value equality (two distinct blank body lines under the
// same headline would otherwise be indistinguishable) — used to locate a
// row found via searchRows within the (possibly differently-folded) real
// m.rows, before and after revealRow.
func sameRow(a, b row) bool {
	switch {
	case a.kind == rowFile || b.kind == rowFile:
		return a.kind == b.kind && a.file == b.file
	case a.kind == rowBody || b.kind == rowBody:
		return a.kind == b.kind && a.headline == b.headline && a.text == b.text
	case a.headline != nil || b.headline != nil:
		// Covers plain headline rows and the rowCalendarEvent variant
		// alike — that flag doesn't change what row a headline points
		// at. rowCalendarLinked, rowTagsItem, and the Meetings-section
		// rowAgendaItem case are different: the same headline can
		// legitimately appear in more than one such row (an entry linked
		// to several meetings/events, or carrying several tags), so those
		// need their extra fields compared too, or every row past the
		// first would look identical to it.
		if a.headline != b.headline {
			return false
		}
		if a.kind == rowCalendarLinked || b.kind == rowCalendarLinked {
			return a.kind == b.kind && a.linkedFromEvent == b.linkedFromEvent
		}
		if a.kind == rowTagsItem || b.kind == rowTagsItem {
			return a.kind == b.kind && a.tagsItemTag == b.tagsItemTag
		}
		if a.kind == rowAgendaItem || b.kind == rowAgendaItem {
			return a.kind == b.kind &&
				a.agendaLabel == b.agendaLabel &&
				a.agendaDate.Equal(b.agendaDate) &&
				a.meetingItemTitle == b.meetingItemTitle &&
				a.meetingItemStart.Equal(b.meetingItemStart)
		}
		return true
	case a.kind == rowMeetingHeader || b.kind == rowMeetingHeader:
		// headline is nil on both sides here, so nil == nil would
		// otherwise make every meeting header (and every section/
		// rowText row, below) look like the same row.
		return a.kind == b.kind && a.meetingTitle == b.meetingTitle && a.meetingStart.Equal(b.meetingStart)
	case a.kind == rowText || b.kind == rowText:
		return a.kind == b.kind && a.text == b.text
	default:
		return a.kind == b.kind && a.text == b.text
	}
}

// rowSearchText returns the text of r that "/"/"?" search against.
func rowSearchText(r row) string {
	switch {
	case r.kind == rowSection, r.kind == rowBody, r.kind == rowText:
		return r.text
	case r.kind == rowFile:
		return filepath.Base(r.file.Path)
	case r.kind == rowMeetingHeader:
		return r.meetingTitle
	case r.headline != nil:
		h := r.headline
		text := h.Keyword + " " + h.Title
		if len(h.Tags) > 0 {
			text += " " + strings.Join(h.Tags, " ")
		}
		return text
	}
	return ""
}
