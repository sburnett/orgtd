// Package ui implements the Bubble Tea viewer: a scrollable, foldable
// outline over every org file in a workspace, with in-memory editing of
// item status. Changes are not yet written back to disk — no agenda, no
// Google integration either.
package ui

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/olebedev/when"

	"github.com/sburnett/orgtd/internal/org"
	"github.com/sburnett/orgtd/internal/workspace"
)

var (
	fileStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("14"))

	keywordStyles = map[string]lipgloss.Style{
		"TODO":      lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("9")),
		"NEXT":      lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("11")),
		"WAITING":   lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214")),
		"SOMEDAY":   lipgloss.NewStyle().Foreground(lipgloss.Color("245")),
		"DONE":      lipgloss.NewStyle().Foreground(lipgloss.Color("10")),
		"CANCELLED": lipgloss.NewStyle().Foreground(lipgloss.Color("8")),
	}

	tagStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	doneTitleStyle = lipgloss.NewStyle().Strikethrough(true).Foreground(lipgloss.Color("245"))
	cursorStyle    = lipgloss.NewStyle().Reverse(true)
	statusStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	timestampStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("13"))
	errorStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	shortcutStyle  = lipgloss.NewStyle().Bold(true)
	bodyStyle      = lipgloss.NewStyle().Italic(true).Foreground(lipgloss.Color("245"))

	// overlayBg is the subtle background tint for the pinned header
	// (clarify/marks) and the status bar — a light/dark pair so it reads
	// as a faint panel regardless of the terminal's own color scheme,
	// resolved via lipgloss's terminal background detection.
	overlayBg = lipgloss.AdaptiveColor{Light: "#e4e4e4", Dark: "#262626"}

	// cursorBg highlights the row under the cursor, filling the whole
	// terminal width — a distinct, more prominent shade than overlayBg
	// so the current line and the pinned overlay read as different
	// things. In visual mode, only the cursor's own entry keeps this
	// shade (see visualSelectionBg for the rest of the selection), so
	// which end of a multi-entry selection is the actual cursor is
	// always unambiguous.
	cursorBg = lipgloss.AdaptiveColor{Light: "#cce0ff", Dark: "#2d3f5e"}

	// visualSelectionBg highlights the part of a visual-mode selection
	// that isn't the cursor's own entry — a softer tint of cursorBg's
	// same hue, so the whole selection still reads as one contiguous
	// block while staying visibly less prominent than the cursor itself.
	visualSelectionBg = lipgloss.AdaptiveColor{Light: "#e2ecfb", Dark: "#212d42"}

	// searchHighlightBg marks every occurrence of the active search term
	// (see activeSearchQuery) — vim's 'hlsearch' — layered on top of
	// whatever background (if any) a segment already carries.
	searchHighlightBg = lipgloss.AdaptiveColor{Light: "#fff099", Dark: "#5c4a00"}
)

// bgSpan renders s with only a background color — no other styling —
// for the plain-text gaps (join separators, padding) inside a
// background-tinted line, so they don't leave un-tinted holes once an
// adjacent styled segment's own reset code fires.
func bgSpan(bg lipgloss.TerminalColor, s string) string {
	return lipgloss.NewStyle().Background(bg).Render(s)
}

// activeSearchQuery is the search term row rendering should highlight
// (see highlightMatches): the query typed so far while actively
// searching, or the last confirmed search otherwise — matching vim's
// 'hlsearch', which keeps highlighting the last search until a new one
// starts or it's cleared (:noh).
func (m Model) activeSearchQuery() string {
	if m.mode == searchMode {
		return m.searchQuery
	}
	return m.lastSearchQuery
}

// highlightMatches renders s with every case-insensitive occurrence of
// query given an extra searchHighlightBg background layered on top of
// base, and everything else rendered plainly with base. Each segment is
// rendered independently (not nested) so this composes correctly
// regardless of what background base itself already carries. A blank
// query renders s with base unchanged.
func highlightMatches(s, query string, base lipgloss.Style) string {
	if query == "" {
		return base.Render(s)
	}
	lowerS := strings.ToLower(s)
	lowerQ := strings.ToLower(query)
	highlight := base.Background(searchHighlightBg)

	var b strings.Builder
	last := 0
	for {
		rel := strings.Index(lowerS[last:], lowerQ)
		if rel < 0 {
			break
		}
		start := last + rel
		end := start + len(query)
		if start > last {
			b.WriteString(base.Render(s[last:start]))
		}
		b.WriteString(highlight.Render(s[start:end]))
		last = end
	}
	if last < len(s) {
		b.WriteString(base.Render(s[last:]))
	}
	return b.String()
}

// joinBg joins parts with a bg-tinted single space, the background-aware
// equivalent of strings.Join(parts, " ").
func joinBg(parts []string, bg lipgloss.TerminalColor) string {
	return strings.Join(parts, bgSpan(bg, " "))
}

// padLineToWidth extends line with bg-tinted spaces up to m.width, so a
// background tint fills the whole terminal row rather than stopping
// wherever the visible text ends. A no-op if the width is unknown
// (m.width <= 0) or line already reaches or exceeds it.
func (m Model) padLineToWidth(line string, bg lipgloss.TerminalColor) string {
	if m.width <= 0 {
		return line
	}
	pad := m.width - lipgloss.Width(line)
	if pad <= 0 {
		return line
	}
	return line + bgSpan(bg, strings.Repeat(" ", pad))
}

// fitRowLine joins prefix (a headline's keyword/title, plus whatever
// comes before it — mark/lock/gutter/indent/fold columns) to suffix
// (trailing metadata: tags, a date, a filename, ...), truncating prefix
// with a "…" — bg-styled like every other segment on the row, via
// ansi.Truncate, which is ANSI/wide-rune aware so it never cuts an
// escape code or a multi-byte glyph in half — if the combined line
// would exceed width. Only prefix ever gives way: a long title alone
// shouldn't make trailing metadata disappear off the right edge with no
// indication anything was cut, so suffix is always shown in full (or,
// in the degenerate case where even suffix alone exceeds width, prefix
// simply vanishes rather than also being cut). width <= 0 (m.width
// unset — e.g. before the first WindowSizeMsg, or in a test that never
// sets it) skips truncation entirely, matching padLineToWidth's own
// convention.
func fitRowLine(prefix, suffix string, width int, bg lipgloss.TerminalColor) string {
	if width <= 0 {
		return prefix + suffix
	}
	full := prefix + suffix
	if lipgloss.Width(full) <= width {
		return full
	}
	avail := width - lipgloss.Width(suffix)
	if avail < 0 {
		avail = 0
	}
	return ansi.Truncate(prefix, avail, bgSpan(bg, "…")) + suffix
}

// mode selects how key presses are interpreted.
type mode int

const (
	normalMode mode = iota
	commandMode
	selectMode
	deadlineMode
	searchMode
	confirmMode
	visualMode
	commitMessageMode
	meetingPickerMode
	tagMode
)

// statusCandidate is one entry in the "set status" picker (R).
type statusCandidate struct {
	keyword  string // "" for "no keyword"
	label    string // display label
	shortcut rune   // single-key shortcut
}

// statusCandidates lists every state selectable via R, in display order.
// Every label starts with a distinct letter (matching its shortcut),
// except "(none)", which uses '-' instead.
var statusCandidates = []statusCandidate{
	{"TODO", "TODO", 't'},
	{"NEXT", "NEXT", 'n'},
	{"WAITING", "WAITING", 'w'},
	{"SOMEDAY", "SOMEDAY", 's'},
	{"DONE", "DONE", 'd'},
	{"CANCELLED", "CANCELLED", 'c'},
	{"", "(none)", '-'},
}

// matchesFilter reports whether c is a candidate for the typed filter
// (a lowercase prefix of its label, or its exact shortcut).
func (c statusCandidate) matchesFilter(filter string) bool {
	if filter == "" {
		return true
	}
	if strings.HasPrefix(strings.ToLower(c.label), filter) {
		return true
	}
	return len(filter) == 1 && rune(filter[0]) == c.shortcut
}

// statusCandidateIndex returns keyword's position in statusCandidates.
func statusCandidateIndex(keyword string) int {
	for i, c := range statusCandidates {
		if c.keyword == keyword {
			return i
		}
	}
	return -1
}

func filteredStatusCandidates(filter string) []statusCandidate {
	var out []statusCandidate
	for _, c := range statusCandidates {
		if c.matchesFilter(filter) {
			out = append(out, c)
		}
	}
	return out
}

// row is one visible line in the outline: either a file header or a
// headline at some depth.
type row struct {
	file     *org.File // set for a file-header row (outline view)
	headline *org.Headline
	level    int // structural level used by level-aware navigation (rowLevel); file/section rows are 0

	// isBodyLine marks a row showing one line of headline's free-text
	// body (shown under its title when expanded — see appendBodyLines);
	// bodyText is that line's text (which may itself be empty — a blank
	// line in the body — so isBodyLine, not bodyText != "", is the
	// reliable marker). headline is still set to the owning headline on
	// such a row (not nil), so commands like i/dd/r/gd resolve to it
	// exactly as if the cursor were on the title row itself.
	isBodyLine bool
	bodyText   string

	section        string    // set for an agenda section-header row ("Overdue" etc.); outline rows never set this
	isAgendaItem   bool      // true for every agenda item row (Next Actions entries have no date/label, so this — not agendaLabel — is the reliable marker)
	agendaLabel    string    // "Scheduled" or "Deadline", set for a date-based agenda item row; empty for a Next Actions entry
	agendaDate     time.Time // the date this agenda item row is shown for, if agendaLabel is set
	agendaRepeater string    // e.g. "+1w", if agendaDate was computed from a recurring timestamp; empty otherwise
	agendaMissed   int       // occurrences skipped since agendaDate, shown as "(Nx)"; only ever set on an Overdue row

	// isMeetingHeader marks a meeting-group header row in the agenda's
	// "Meetings" section (see appendMeetingsSection): a label ("<title>
	// — <when>") to group the items below it under, one level deeper
	// than the section header and one level shallower than its items
	// (see rowLevel) — not itself a headline (headline is left nil, like
	// a plain section row), so none of the outline's per-headline
	// commands apply to it.
	isMeetingHeader bool
	meetingTitle    string
	meetingStart    time.Time
	meetingEnd      time.Time

	// isCalendarItem marks a calendar-event row in calendarView (see
	// appendCalendarHeadlines): rendered with its GCAL_START/GCAL_END
	// time shown before the title (see renderCalendarItemRowWithBg),
	// rather than the outline's usual keyword-first layout.
	isCalendarItem bool

	// isCalendarLinkedItem marks a row for an entry elsewhere in the
	// workspace linked to a calendar event — attached via "gM", or
	// sharing a tag with it (see linkedMeetingItems) — shown right after
	// the event itself in calendarView regardless of whether the event is
	// folded (see appendCalendarHeadlines), indented to level (one deeper
	// than the event) rather than the headline's own real level in its
	// own file (see renderCalendarLinkedItemRowWithBg).
	isCalendarLinkedItem bool

	// isTextLine marks a plain read-only informational row (:config/:log/
	// :diff/:help), rendered flush left and never interactive; text is
	// that line's own text (which may itself be empty — a blank line, as
	// :help's embedded README naturally has plenty of — so isTextLine,
	// not text != "", is the reliable marker; same reasoning as
	// isBodyLine/bodyText above).
	isTextLine bool
	text       string
}

// Model is the Bubble Tea model for the viewer.
type Model struct {
	ws *workspace.Workspace

	collapsed      map[*org.Headline]bool
	dirty          map[*org.File]bool     // derived from undoStack/savedPos; see recomputeDirty
	dirtyHeadlines map[*org.Headline]bool // derived from undoStack/savedPos; see recomputeDirty
	rows           []row

	undoStack []undoAction // undoStack[:undoPos] applied, undoStack[undoPos:] available to redo
	undoPos   int
	savedPos  map[*org.File]int // per-file count of applied actions at that file's last successful write

	cursor int
	offset int // index of the first visible row (for scrolling)

	width, height int

	pendingG     bool
	pendingD     bool
	pendingY     bool // pending 'y' of "yy"
	pendingGT    bool // pending '>' of ">>"
	pendingLT    bool // pending '<' of "<<"
	pendingZ     bool // pending 'z' of a fold command (zo/zc/za/zO/zC/zA)
	pendingM     bool // pending 'm' of "m<letter>" (set a mark)
	pendingQuote bool // pending '\'' of "'<letter>" (jump to a mark)
	pendingCount int  // numeric prefix built up so far for "dd"/"r"/"R" (e.g. "3dd", "2R"); 0 means none typed

	// pendingForceQuit is set by a ctrl+c that got refused because there
	// were unsaved changes (see Update) — a second ctrl+c right after it
	// forces the quit anyway, same as ":q!" after ":q" refuses. Cleared
	// by any other key in between, so it's specifically "the very next
	// key", not "ctrl+c was pressed at some earlier point in the session".
	pendingForceQuit bool

	register []*org.Headline // last deleted (dd/<N>dd/visual d) or yanked (yy) top-level entry/entries, pasted (as copies, in the same order) by p/P; stays pinned to the top of the screen (see pinnedHeaderLines) until overwritten by a later delete/yank

	marks map[rune]*org.Headline // vim-style marks (letter -> headline), set by "m<letter>", jumped to by "'<letter>"; each stays pinned to the top of the screen (see pinnedHeaderLines) until cleared

	// jumpList/jumpPos implement vim's own jumplist (ctrl-o/"gi" here —
	// see jumpBack/jumpForward): jumpList holds one entry per recorded
	// "before a large move" position, oldest first; jumpPos is where in
	// that list the cursor currently sits — equal to len(jumpList) means
	// "live", i.e. not currently navigating the list at all. See pushJump
	// for how entries get added.
	jumpList []jumpEntry
	jumpPos  int

	// immutable holds every headline currently locked by an in-flight
	// :format-links batch (see startFormatLinks/finishFormatLinks) —
	// nothing about it (status, deadline, title/body text, position,
	// deletion) can change until the batch resolves and clears it, since
	// the batch's replacement text is computed against a snapshot of its
	// current content. Shown in the outline via a gutter marker (see
	// lockColumn) and enforced by refuseIfImmutable/filterImmutable.
	immutable map[*org.Headline]bool

	// execLog records every external command run since startup (URL
	// formatters, $EDITOR), for :log — see execLog's own doc comment for
	// why it's a pointer rather than a plain value.
	execLog *execLog

	visualAnchor int // row index where "V" was pressed; the selection spans from here to m.cursor (see visualRange), both ends snapped to whole entries

	// selectModeTargets holds the entries a pending R (selectMode) should
	// apply the chosen status to, if more than just the current one — set
	// when R is invoked from visual mode (the whole selection) or with a
	// numeric prefix (the current entry plus the next N-1, see
	// countRowRange). nil for a plain R, meaning "just the current
	// headline" (see applyChosenStatus).
	selectModeTargets []*org.Headline

	mode               mode
	commandInput       string
	commandCompletions string // space-joined tab-completion matches shown after commandInput, cleared on the next keystroke
	message            string // transient status-line message (e.g. an error), cleared on the next key press

	// commandHistory records every command line actually run via Enter
	// (see runCommand), oldest first, for ↑/↓ recall in updateCommandMode
	// — mirrors vim's own cmdline history, including that it's not
	// deduplicated. commandHistoryPos indexes into it for the entry
	// currently shown; len(commandHistory) means "not navigating" (either
	// a fresh command line, or one being freely typed after some ↑/↓
	// browsing) rather than any real history entry. commandHistoryDraft
	// holds what was typed before the first ↑ started navigating, so ↓
	// can restore it once back past the most recent entry — same as a
	// shell's own history search.
	commandHistory      []string
	commandHistoryPos   int
	commandHistoryDraft string

	selectFilter string // typed so far, in selectMode
	selectIndex  int    // highlighted index within the filtered candidates, in selectMode

	// meetingPickerTarget is the entry "gM" was invoked on, whose
	// GCAL_RECURRING_EVENT_IDS or GCAL_EVENT_IDS (matching whichever the
	// highlighted candidate is — see meetingCandidate.kind) is
	// attached/detached from on Enter (see applySelectedMeeting).
	// meetingPickerCandidates is computed once, when the picker opens
	// (startMeetingPicker) — every distinct recurring series or one-off
	// event :sync-calendar currently has synced at least one instance of — and
	// only filtered (never recomputed) for the rest of the session, so
	// the list doesn't shift under the user mid-selection. meetingPickerFilter is
	// typed so far (a plain substring match against each candidate's
	// title, unlike selectFilter's prefix/shortcut matching — titles are
	// arbitrary text, not a small fixed set of keywords); meetingPickerIndex
	// is the highlighted index within the filtered candidates.
	meetingPickerTarget     *org.Headline
	meetingPickerCandidates []meetingCandidate
	meetingPickerFilter     string
	meetingPickerIndex      int

	deadlineInput string // typed so far, in deadlineMode

	// tagTarget is the entry "gt" was invoked on, whose Tags is toggled
	// (added if absent, removed if already present — see applyTagInput)
	// on Enter. tagInput is typed so far; tagCompletions is the
	// space-joined Tab-completion matches against every tag already used
	// somewhere in the workspace (see completeTagInput), shown after
	// tagInput and cleared on the next keystroke, mirroring
	// commandCompletions.
	tagTarget      *org.Headline
	tagInput       string
	tagCompletions string

	commitMessageInput string // typed so far, in commitMessageMode (see startCommit)

	searchQuery   string // typed so far, in searchMode
	searchForward bool   // true for "/" (forward), false for "?" (backward)
	searchOrigin  int    // cursor position when the search started, restored on Esc

	lastSearchQuery   string // most recently confirmed search, repeated by n/N
	lastSearchForward bool   // that search's direction ("n" repeats it, "N" reverses it)

	confirmMessage  string    // prompt shown in confirmMode
	pendingFileEdit *org.File // the file to open in $EDITOR if confirmMode's prompt is accepted ("y")

	// pendingUntrackedFiles/pendingUntrackedThen back a second,
	// independent use of confirmMode: requestAddUntracked, asking
	// whether to `git add` files :diff/:commit found aren't tracked at
	// all yet, before continuing either way (see updateConfirmMode).
	// Never set at the same time as pendingFileEdit — the two questions
	// are asked from unrelated commands.
	pendingUntrackedFiles []string
	pendingUntrackedThen  func(m *Model)

	urlFormatterCmd      string         // external program that turns a bare URL into an org-mode link when editing an entry; disabled if empty
	urlFormatterPrefixes []string       // extra bare-URL prefixes beyond http(s)://, e.g. "bit.ly/", "go/" (see WithURLFormatterPrefixes)
	bareURLRe            *regexp.Regexp // compiled from urlFormatterPrefixes at construction time; see buildBareURLRegexp
	editorOverride       string         // takes precedence over $EDITOR when set (see WithEditor); empty means "use $EDITOR"

	// formatLinksURLFormatterCmd is the external program :format-links
	// invokes in batch mode (see runBatchURLFormatter) — configured
	// separately from urlFormatterCmd since a batch-capable command may
	// differ from (or take different arguments than) whatever handles a
	// single URL while editing. Empty means "use urlFormatterCmd for
	// :format-links too" — see formatLinksFormatterCmd.
	formatLinksURLFormatterCmd string

	view       viewKind
	agendaDays int // how many days ahead the agenda's "Upcoming" section covers

	inboxFile     string        // base name of the file :clarify treats as the inbox
	clarifyTarget *org.Headline // the inbox item currently pinned for clarification, in clarifyView; nil if the inbox is empty

	// calendarFile is the base name of the file :sync-calendar writes (e.g.
	// "calendar.org", the default) — excluded from the outline view
	// entirely (see rebuildRows' default case) and shown instead, grouped
	// by day, in calendarView (see appendCalendarRows).
	calendarFile string

	hideDoneAfterHours int  // how many hours after CLOSED a DONE/CANCELLED item disappears from the outline; see WithHideDoneAfterHours
	hideDoneEnabled    bool // whether hideDoneAfterHours filtering is active; off by default (see New), toggled by :toggledone, turned on at startup by WithHideDoneAfterHours

	debug bool // whether main.go turned on debug logging (see WithDebug); the Model itself never logs anything based on this — it's only carried here so :config can report it

	// gcalOAuthClientID/Secret, gcalCalendarIDs, gcalSyncPastDays/FutureDays
	// configure :sync-calendar (see startSyncCalendar) — the Google OAuth2
	// installed-app client, which calendars to sync, and how wide a
	// window around now to pull events from. An empty
	// gcalOAuthClientID/Secret means :sync-calendar isn't configured at
	// all (see WithGcalOAuthClient). syncingCalendar guards against
	// starting a second sync while one is already in flight — unlike
	// :format-links, a sync never touches any headline the user might be
	// editing, so there's nothing to lock, just this one flag.
	gcalOAuthClientID, gcalOAuthClientSecret string
	gcalCalendarIDs                          []string
	gcalSyncPastDays, gcalSyncFutureDays     int
	syncingCalendar                          bool

	// gcalAttendeeTagDomains restricts the "@username" attendee tags a
	// sync gives a synced event (see internal/calendarsync's BuildFile)
	// to attendees whose email ends in one of these domains — see
	// WithGcalAttendeeTagDomains. Empty (the default) means no
	// restriction: every confirmed attendee is tagged, regardless of
	// domain.
	gcalAttendeeTagDomains []string

	// gcalAttendeeIgnorePatterns excludes any attendee whose email
	// matches one of these glob patterns from consideration entirely,
	// before gcalAttendeeTagDomains is even checked — see
	// WithGcalAttendeeIgnorePatterns. Empty (the default) means no
	// exclusions.
	gcalAttendeeIgnorePatterns []string

	// dirty/mark/clarify/lock/meeting Icon/Color customize the outline's
	// gutter markers (see gutter, markColumn, lockColumn, meetingColumn,
	// and renderPinnedRow for the same markers pinned to the top of the
	// screen) — set from the config file's [icons] section (see
	// WithDirtyIcon and its siblings, below), each defaulting to New's
	// own built-in glyph/color when unset. markColor has no matching
	// markIcon: a mark's glyph is always the letter it was set with
	// ("m<letter>"), not a fixed character.
	dirtyIcon, dirtyColor     string
	markColor                 string
	clarifyIcon, clarifyColor string
	lockIcon, lockColor       string
	meetingIcon, meetingColor string

	diffOutput string // combined stdout of the last :diff run (see showDiff), split into one row per line by appendDiffRows
	diffErr    string // if the last :diff run failed, why — shown instead of diffOutput; empty means it succeeded (even if there was nothing to show)

	readme string // README.md's content, embedded into the binary by the caller (see WithReadme); :help shows it verbatim
}

// viewKind selects what rebuildRows populates m.rows with.
type viewKind int

const (
	outlineView viewKind = iota
	agendaView
	clarifyView
	configView
	logView
	diffView
	helpView
	calendarView
)

// Option customizes a Model at construction time. See New.
type Option func(*Model)

// WithURLFormatter enables passing bare URLs, found in text edited via the
// external editor, through cmd — an external program invoked as
// `cmd <url>`, expected to print an org-mode link (e.g.
// "[[https://foo.com][Foo Site]]") to stdout. URLs already inside an
// org-mode link are left alone. A blank cmd disables the feature (the
// default).
func WithURLFormatter(cmd string) Option {
	return func(m *Model) { m.urlFormatterCmd = cmd }
}

// WithFormatLinksURLFormatter sets the external program :format-links
// invokes in batch mode — called with no trailing URL argument, it's
// expected to read URLs one per line from stdin and print the same
// number of formatted lines to stdout (see runBatchURLFormatter). A
// blank cmd (the default) means :format-links uses urlFormatterCmd
// instead, same as everything else — see formatLinksFormatterCmd.
func WithFormatLinksURLFormatter(cmd string) Option {
	return func(m *Model) { m.formatLinksURLFormatterCmd = cmd }
}

// WithURLFormatterPrefixes adds extra bare-URL prefixes formatURLs
// recognizes beyond the built-in http:// and https:// — e.g. "bit.ly/"
// for a shortlink service, or "go/" for an internal go-link convention.
// Each is matched only at a word boundary (see buildBareURLRegexp), so
// a short prefix like "go/" doesn't also match mid-word. Has no effect
// unless WithURLFormatter is also set, since there'd be nothing to
// format a bare URL into otherwise.
func WithURLFormatterPrefixes(prefixes []string) Option {
	return func(m *Model) { m.urlFormatterPrefixes = prefixes }
}

// WithEditor overrides $EDITOR as the external editor orgtd launches for
// `i` and file edits (see editorCommand). cmd == "" leaves $EDITOR as the
// source (the default).
func WithEditor(cmd string) Option {
	return func(m *Model) { m.editorOverride = cmd }
}

// WithAgendaDays sets how many days ahead of today the agenda view's
// "Upcoming" section covers. days <= 0 is treated as the default (14).
func WithAgendaDays(days int) Option {
	return func(m *Model) {
		if days > 0 {
			m.agendaDays = days
		}
	}
}

// WithInboxFile sets the base file name :clarify treats as the inbox
// (e.g. "inbox.org", the default). name == "" is treated as the
// default.
func WithInboxFile(name string) Option {
	return func(m *Model) {
		if name != "" {
			m.inboxFile = name
		}
	}
}

// WithCalendarFile sets the base file name excluded from the outline
// view and shown instead (grouped by day) in calendarView — the file
// :sync-calendar writes (e.g. "calendar.org", the default). name == "" is
// treated as the default.
func WithCalendarFile(name string) Option {
	return func(m *Model) {
		if name != "" {
			m.calendarFile = name
		}
	}
}

// WithHideDoneAfterHours turns on hiding DONE/CANCELLED headlines (and
// their whole subtrees — see appendHeadlines) whose CLOSED timestamp is
// more than hours in the past from the outline view; hours <= 0 keeps
// the built-in threshold (24) rather than turning filtering on with a
// meaningless one. Not passing this option at all leaves filtering off
// from the start — the zero-value Model default, which is what every
// caller that doesn't care about this feature (chiefly tests) relies
// on — even though hideDoneAfterHours itself still carries a default
// value either way. Once on, filtering can still be toggled off
// entirely at runtime with :toggledone, and back on again the same way.
func WithHideDoneAfterHours(hours int) Option {
	return func(m *Model) {
		if hours > 0 {
			m.hideDoneAfterHours = hours
		}
		m.hideDoneEnabled = true
	}
}

// WithDebug records whether main.go turned on debug logging, purely so
// the :config view can report it accurately — the Model doesn't consult
// this for anything else, since logging itself is set up once, globally,
// before the Model even exists (see main.go).
func WithDebug(enabled bool) Option {
	return func(m *Model) { m.debug = enabled }
}

// WithReadme supplies README.md's content for :help to show — the UI
// package has no file of its own to read it from at runtime (it's
// embedded into the binary elsewhere, at the module root, since
// go:embed can't reach outside that file's own directory; see
// cmd/orgtd/main.go). Empty means :help has nothing to show.
func WithReadme(text string) Option {
	return func(m *Model) { m.readme = text }
}

// WithGcalOAuthClient sets the Google OAuth2 installed-app client
// :sync-calendar authenticates with. Either being empty (the default)
// means :sync-calendar isn't configured — see startSyncCalendar.
func WithGcalOAuthClient(clientID, clientSecret string) Option {
	return func(m *Model) { m.gcalOAuthClientID, m.gcalOAuthClientSecret = clientID, clientSecret }
}

// WithGcalCalendarIDs sets which Google Calendar IDs :sync-calendar
// syncs, e.g. "primary" or an email address for a secondary/shared
// calendar. Default (if this option is never applied): ["primary"] —
// see New.
func WithGcalCalendarIDs(ids []string) Option {
	return func(m *Model) { m.gcalCalendarIDs = ids }
}

// WithGcalSyncWindow sets how many days into the past/future
// :sync-calendar's sync window extends around now. Defaults (if this
// option is never applied): 1/14 — see New.
func WithGcalSyncWindow(pastDays, futureDays int) Option {
	return func(m *Model) { m.gcalSyncPastDays, m.gcalSyncFutureDays = pastDays, futureDays }
}

// WithGcalAttendeeTagDomains restricts the "@username" attendee tags
// :sync-calendar gives a synced event to attendees whose email ends in
// one of domains, e.g. ["example.com"] to tag only coworkers. Default
// (if this option is never applied, or domains is empty): no
// restriction — every confirmed attendee is tagged.
func WithGcalAttendeeTagDomains(domains []string) Option {
	return func(m *Model) { m.gcalAttendeeTagDomains = domains }
}

// WithGcalAttendeeIgnorePatterns excludes any attendee whose email
// matches one of patterns — each a filepath.Match-style glob ("*"
// matches any run of characters, "?" a single one), compared
// case-insensitively against the whole address — from consideration
// entirely, before WithGcalAttendeeTagDomains is even checked, e.g.
// ["c_*@*"] to drop the synthetic "c_...@..." attendees Google Calendar
// attaches to represent a resource/room booking. Default (if this
// option is never applied, or patterns is empty): no exclusions.
func WithGcalAttendeeIgnorePatterns(patterns []string) Option {
	return func(m *Model) { m.gcalAttendeeIgnorePatterns = patterns }
}

// WithDirtyIcon sets the character and color of the gutter marker shown
// on any entry with unsaved changes (default: "+", color "9"). An empty
// icon or color leaves that half at its default, so the config file's
// [icons] section can set just one of the two.
func WithDirtyIcon(icon, color string) Option {
	return func(m *Model) {
		if icon != "" {
			m.dirtyIcon = icon
		}
		if color != "" {
			m.dirtyColor = color
		}
	}
}

// WithMarkColor sets the color of a vim-style mark's letter ("m<letter>"),
// both in the gutter and pinned to the top of the screen (default:
// "212"). There's no matching icon option — a mark's glyph is always the
// letter it was set with, not a fixed character.
func WithMarkColor(color string) Option {
	return func(m *Model) {
		if color != "" {
			m.markColor = color
		}
	}
}

// WithClarifyIcon sets the character and color of the marker on
// :clarify's pinned inbox item, both in the gutter and pinned to the top
// of the screen (default: "●", color "212"). An empty icon or color
// leaves that half at its default.
func WithClarifyIcon(icon, color string) Option {
	return func(m *Model) {
		if icon != "" {
			m.clarifyIcon = icon
		}
		if color != "" {
			m.clarifyColor = color
		}
	}
}

// WithLockIcon sets the character and color of the gutter marker on an
// entry currently locked by an in-flight :format-links batch (default:
// "◆", color "208"). An empty icon or color leaves that half at its
// default.
func WithLockIcon(icon, color string) Option {
	return func(m *Model) {
		if icon != "" {
			m.lockIcon = icon
		}
		if color != "" {
			m.lockColor = color
		}
	}
}

// WithMeetingIcon sets the character and color of the gutter marker on
// an entry attached to a calendar meeting via "gM" (default: "▣", color
// "39"). An empty icon or color leaves that half at its default.
func WithMeetingIcon(icon, color string) Option {
	return func(m *Model) {
		if icon != "" {
			m.meetingIcon = icon
		}
		if color != "" {
			m.meetingColor = color
		}
	}
}

// New builds a viewer model over ws. Every headline starts expanded.
func New(ws *workspace.Workspace, opts ...Option) Model {
	m := Model{
		ws:                 ws,
		collapsed:          make(map[*org.Headline]bool),
		dirty:              make(map[*org.File]bool),
		dirtyHeadlines:     make(map[*org.Headline]bool),
		savedPos:           make(map[*org.File]int),
		immutable:          make(map[*org.Headline]bool),
		execLog:            &execLog{},
		agendaDays:         14,
		inboxFile:          "inbox.org",
		calendarFile:       "calendar.org",
		hideDoneAfterHours: 24,
		gcalCalendarIDs:    []string{"primary"},
		gcalSyncPastDays:   1,
		gcalSyncFutureDays: 14,
	}
	for _, opt := range opts {
		opt(&m)
	}
	m.bareURLRe = buildBareURLRegexp(m.urlFormatterPrefixes)
	m.rebuildRows()
	return m
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m *Model) rebuildRows() {
	m.rows = m.rows[:0]
	switch m.view {
	case agendaView:
		m.appendAgendaRows()
	case configView:
		m.appendConfigRows()
	case logView:
		m.appendLogRows()
	case diffView:
		m.appendDiffRows()
	case helpView:
		m.appendHelpRows()
	case calendarView:
		m.appendCalendarRows(&m.rows, false)
	default:
		for _, f := range m.ws.Files {
			if filepath.Base(f.Path) == m.calendarFile {
				continue
			}
			m.rows = append(m.rows, row{file: f})
			m.appendHeadlines(&m.rows, f.Headlines, false)
		}
	}
	if m.cursor >= len(m.rows) {
		m.cursor = len(m.rows) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

// jumpToSource ("Enter" on an agenda row, or on an item linked to a
// calendar event — see linkedMeetingItems) switches to outline
// view with the cursor on that row's real headline. A no-op outside
// those two cases: agenda view's own section-header rows, and — in
// calendarView — a calendar event's own row, since calendar_file is
// excluded from the outline entirely (see appendCalendarRows), so
// there'd be nowhere to jump to.
func (m *Model) jumpToSource() {
	switch {
	case m.view == agendaView:
	case m.view == calendarView && m.cursor >= 0 && m.cursor < len(m.rows) && m.rows[m.cursor].isCalendarLinkedItem:
	default:
		return
	}
	h := m.currentHeadline()
	if h == nil {
		return
	}
	m.switchToView(outlineView)
	m.focusHeadline(h)
}

// findInboxFile returns the workspace file :clarify treats as the
// inbox (see WithInboxFile), or nil if it isn't loaded.
func (m *Model) findInboxFile() *org.File {
	for _, f := range m.ws.Files {
		if filepath.Base(f.Path) == m.inboxFile {
			return f
		}
	}
	return nil
}

// findCalendarFile returns the workspace file calendarView shows (see
// WithCalendarFile), or nil if it isn't loaded.
func (m *Model) findCalendarFile() *org.File {
	for _, f := range m.ws.Files {
		if filepath.Base(f.Path) == m.calendarFile {
			return f
		}
	}
	return nil
}

// gitFiles returns m.ws.Files minus the calendar file (see
// WithCalendarFile): every git operation (diff/add/commit) that scopes
// itself to "the files currently open in the outline" uses this instead
// of m.ws.Files directly, since the calendar file is :sync-calendar's own
// output — regenerated locally from Google Calendar, not something
// meant to be versioned or committed alongside the rest of the org
// directory.
func (m *Model) gitFiles() []*org.File {
	files := make([]*org.File, 0, len(m.ws.Files))
	for _, f := range m.ws.Files {
		if filepath.Base(f.Path) == m.calendarFile {
			continue
		}
		files = append(files, f)
	}
	return files
}

// advanceClarifyTarget sets m.clarifyTarget to the inbox's first
// top-level headline that isn't DONE/CANCELLED — clarify mode is for
// processing pending items, so one already resolved (marked done but
// not yet filed away or deleted) is skipped rather than pinned for
// clarification — or nil if the inbox file is missing, empty, or every
// item in it is done.
func (m *Model) advanceClarifyTarget() {
	f := m.findInboxFile()
	if f == nil {
		m.clarifyTarget = nil
		return
	}
	for _, h := range f.Headlines {
		if !org.IsDoneKeyword(h.Keyword) {
			m.clarifyTarget = h
			return
		}
	}
	m.clarifyTarget = nil
}

// advanceClarifyTargetIfDone re-pins past the current clarify target if
// a status change (r/R, single or bulk) just left it DONE/CANCELLED —
// there's no reason to keep a resolved item pinned at the top waiting to
// be filed away. A no-op outside clarify view, if nothing's pinned, or
// if the target is still active.
func (m *Model) advanceClarifyTargetIfDone() {
	if m.view == clarifyView && m.clarifyTarget != nil && org.IsDoneKeyword(m.clarifyTarget.Keyword) {
		m.advanceClarifyTarget()
	}
}

// clarifyStep moves the clarify target by delta positions (1 for
// :next, -1 for :prev) among the inbox's top-level headlines, skipping
// any DONE/CANCELLED entries along the way, same as automatic
// advancement — manual navigation should never land on one either. If
// there's no current target (e.g. the inbox was empty when clarify view
// was entered but has since gained an item), this just establishes one
// at the natural starting point instead of stepping from nowhere. A
// no-op (with a status message) if there's nowhere left to go in that
// direction.
func (m *Model) clarifyStep(delta int) {
	f := m.findInboxFile()
	if f == nil || len(f.Headlines) == 0 {
		m.message = "Inbox is empty"
		return
	}
	idx := -1
	for i, h := range f.Headlines {
		if h == m.clarifyTarget {
			idx = i
			break
		}
	}
	if idx < 0 {
		m.advanceClarifyTarget()
		return
	}
	for i := idx + delta; i >= 0 && i < len(f.Headlines); i += delta {
		if !org.IsDoneKeyword(f.Headlines[i].Keyword) {
			m.clarifyTarget = f.Headlines[i]
			return
		}
	}
	if delta > 0 {
		m.message = "Already at the last pending inbox item"
	} else {
		m.message = "Already at the first pending inbox item"
	}
}

// enterClarifyView switches to clarify view, pinning the inbox's first
// top-level headline for clarification (always the first, regardless of
// where the cursor was — :clarify starts a top-to-bottom pass).
func (m *Model) enterClarifyView() {
	m.advanceClarifyTarget()
	m.switchToView(clarifyView)
}

// jumpToClarifyTarget ("gc") moves the cursor to the real row of the
// item currently pinned for clarification, wherever it sits in the
// outline. A no-op outside clarify view, or if the inbox is empty.
func (m *Model) jumpToClarifyTarget() {
	if m.view != clarifyView || m.clarifyTarget == nil {
		return
	}
	m.pushJump()
	m.focusHeadline(m.clarifyTarget)
}

// setMark ("m<letter>") marks the current headline as letter: '<letter>
// jumps back to it later, and it stays pinned to the top of the screen
// (in every view, alongside any other active marks — see
// pinnedHeaderLines) until the mark is deleted or moved elsewhere with
// another "m<letter>". A no-op on a file/section row (nothing to mark).
//
// Each entry holds at most one mark: marking an entry that already has
// a different letter replaces it (the old letter is freed up). Marking
// an entry with the *same* letter it already has toggles the mark off
// instead — a quick way to clear one without dropping into command mode
// for :delmarks.
func (m *Model) setMark(letter rune) {
	h := m.currentHeadline()
	if h == nil {
		return
	}
	if existing, ok := m.markLetterFor(h); ok {
		delete(m.marks, existing)
		if existing == letter {
			m.message = fmt.Sprintf("Mark '%c' cleared", letter)
			return
		}
	}
	if m.marks == nil {
		m.marks = make(map[rune]*org.Headline)
	}
	m.marks[letter] = h
	m.message = fmt.Sprintf("Mark '%c' set", letter)
}

// jumpToMark ("'<letter>") moves the cursor to the headline marked
// letter. If it isn't present among the current view's rows (e.g. it
// has no due date and the current view is agenda), this switches to
// outline view first, since every headline is reachable there.
func (m *Model) jumpToMark(letter rune) {
	h, ok := m.marks[letter]
	if !ok {
		m.message = fmt.Sprintf("Mark '%c' is not set", letter)
		return
	}
	m.pushJump()
	if !m.rowsContainHeadline(h) {
		m.switchToViewNoJump(outlineView)
	}
	m.focusHeadline(h)
}

// rowsContainHeadline reports whether h is one of the headlines
// currently present in m.rows.
func (m *Model) rowsContainHeadline(h *org.Headline) bool {
	for _, r := range m.rows {
		if r.headline == h {
			return true
		}
	}
	return false
}

// markLetterFor returns the letter marking h, if any — an entry holds at
// most one (see setMark) — for showing a marker on its row in the
// listing.
func (m *Model) markLetterFor(h *org.Headline) (rune, bool) {
	best := rune(0)
	found := false
	for letter, target := range m.marks {
		if target == h && (!found || letter < best) {
			best, found = letter, true
		}
	}
	return best, found
}

// clearMarksFor removes every mark pointing at h — called after h is
// deleted, since a mark can't meaningfully point at a removed item.
func (m *Model) clearMarksFor(h *org.Headline) {
	for letter, target := range m.marks {
		if target == h {
			delete(m.marks, letter)
		}
	}
}

// remapHeadlineRefs keeps marks and :clarify's pin correct across an `i`
// edit (subtreeReplaceAction), which always replaces a headline with a
// freshly parsed one — a distinct pointer, even though nothing else
// about the edit changed. oldSet's own root (oldSet[0]) is remapped
// directly to newSet's root (or cleared, if the edit emptied the entry
// out entirely); anything else in oldSet — a descendant, or another
// top-level entry when a whole file is edited at once — has no reliable
// counterpart in the freshly-parsed tree, so its marks/clarify-target
// are cleared rather than left dangling on a headline no longer in any
// tree. Also used, with oldSet/newSet swapped, when the edit is undone.
func (m *Model) remapHeadlineRefs(oldSet, newSet []*org.Headline) {
	var oldRoot, newRoot *org.Headline
	if len(oldSet) > 0 {
		oldRoot = oldSet[0]
	}
	if len(newSet) > 0 {
		newRoot = newSet[0]
	}
	org.Walk(oldSet, func(h *org.Headline) {
		if h != oldRoot {
			if m.clarifyTarget == h {
				m.clarifyTarget = nil
			}
			m.clearMarksFor(h)
			return
		}
		if m.clarifyTarget == h {
			m.clarifyTarget = newRoot
		}
		for letter, target := range m.marks {
			if target != h {
				continue
			}
			if newRoot != nil {
				m.marks[letter] = newRoot
			} else {
				delete(m.marks, letter)
			}
		}
	})
}

// switchToView changes which view rebuildRows populates m.rows with,
// after recording the current position in the jump list (see pushJump)
// so ctrl-o can return to it — resets the cursor to the top, since the
// two views have entirely different row sets and there's no sensible
// position to preserve otherwise. Jump-list restoration (restoreJumpEntry)
// uses switchToViewNoJump instead, to avoid a jump-back pushing a new
// jump of its own.
func (m *Model) switchToView(v viewKind) {
	m.pushJump()
	m.switchToViewNoJump(v)
}

func (m *Model) switchToViewNoJump(v viewKind) {
	m.view = v
	m.cursor = 0
	m.offset = 0
	m.rebuildRows()
}

// jumpEntry is one recorded position in the jump list (see pushJump) —
// the view it was recorded in, plus enough to relocate within that
// view once restored: headline is nil only for a row with no headline
// of its own (a file/section/day-header row), in which case file is
// used as a fallback the same way focusTarget already does elsewhere.
type jumpEntry struct {
	view     viewKind
	headline *org.Headline
	file     *org.File
}

// pushJump records the cursor's current position onto the jump list —
// vim calls this "before a large move", and orgtd applies it in the
// same spirit: gg/G, {/}, a confirmed search, jumping to a mark or to
// the clarify target, and switching views (see switchToView) entirely,
// since that already resets the cursor to row 0 with no way back
// otherwise. Deliberately not called for ordinary j/k or fold/edit
// commands — recording those would make the list useless clutter
// instead of a "where was I before that" trail.
//
// Jumping to the exact same spot the list's current top already holds
// is a no-op that still reports ok (matches vim not recording a
// redundant entry — see jumpBack, which relies on that to tell "nothing
// to bookmark here" apart from "already bookmarked"), and — same as
// typing after an undo discards redo history — pushing a genuinely new
// entry while jumpPos is behind the end of the list discards whatever
// "newer" entries ("gi" could have reached) came after it. Capped at
// maxJumpEntries, oldest dropped first.
//
// ok is false only when idx doesn't name an actual row of a nonempty
// row list (out of range, negative) — a row with neither a headline nor
// a file of its own (a section/day-header row, only possible in agenda
// or calendar view) still records a "just this view" entry (both nil),
// and so does idx==0 when the view has no rows at all (an empty agenda
// or calendar), so restoreJumpEntry can still switch back to that view
// even though it has no specific row, or even any row, to pinpoint
// within it. That matters most for jumpBack's own implicit bookmark of
// the live position (see below): a view switch commonly lands the
// cursor on exactly such a row (row 0 is a section header more often
// than not, and an empty agenda/calendar has no rows at all), and
// without a bookmark there, "gi" would have nothing to return to —
// silently unable to jump back to whichever view ctrl-o had just left,
// even though vim's own jumplist has no such gap switching between
// buffers.
func (m *Model) pushJump() bool {
	return m.pushJumpAt(m.cursor)
}

// pushJumpAt is pushJump, but records the position at row idx rather
// than m.cursor — needed by updateSearchMode's Enter case, where
// incremental search has already moved the cursor by confirm time, so
// what belongs in the jump list is searchOrigin, not the live cursor.
func (m *Model) pushJumpAt(idx int) bool {
	var h *org.Headline
	var f *org.File
	switch {
	case len(m.rows) == 0 && idx == 0:
		// Nothing to point at, but the view itself is still a valid
		// jump target (e.g. an empty agenda) — fall through with h/f
		// left nil.
	case idx < 0 || idx >= len(m.rows):
		return false
	default:
		r := m.rows[idx]
		h = r.headline
		f = r.file
		if f == nil && h != nil {
			f = m.fileForHeadline(h)
		}
	}
	entry := jumpEntry{view: m.view, headline: h, file: f}
	if m.jumpPos > 0 && m.jumpList[m.jumpPos-1] == entry {
		return true
	}
	m.jumpList = append(m.jumpList[:m.jumpPos], entry)
	m.jumpPos = len(m.jumpList)

	const maxJumpEntries = 100
	if len(m.jumpList) > maxJumpEntries {
		m.jumpList = m.jumpList[len(m.jumpList)-maxJumpEntries:]
		m.jumpPos = len(m.jumpList)
	}
	return true
}

// jumpBack ("ctrl-o") moves to the previous position in the jump list,
// same as vim's own ctrl-o. The first press from a "live" position (not
// already mid-navigation) also records that live position itself, so
// "gi" can bring you back to exactly where you started jumping from —
// mirroring vim's own jumplist quirk of the same shape. A no-op at the
// oldest entry, or with an empty list.
func (m *Model) jumpBack() {
	if len(m.jumpList) == 0 {
		return
	}
	if m.jumpPos == len(m.jumpList) {
		if m.pushJump() {
			// pushJump just bookmarked the live position (appended it,
			// or found it already at the top — either way ok) and, for
			// a real append, set jumpPos to the list's new (longer)
			// length, pointing just past what it appended. Step back
			// off that bookmark first, onto the last *real* one
			// recorded before it, which is what this first ctrl-o
			// press should land on. Skipped entirely if there was
			// nothing representable to bookmark at all (e.g. the
			// cursor's on a section/day-header row) — jumpPos is then
			// untouched, so the plain decrement below already lands on
			// the right entry.
			m.jumpPos--
		}
	}
	if m.jumpPos == 0 {
		return
	}
	m.jumpPos--
	m.restoreJumpEntry(m.jumpList[m.jumpPos])
}

// jumpForward ("gi") moves to the next (more recent) position in the
// jump list, same as vim's own ctrl-i — see the README for why orgtd
// binds this to "gi" instead: ctrl-i is the same byte as Tab, already
// bound to fold-toggle, and this app's terminal library can't tell the
// two apart. A no-op once already at the newest recorded entry.
func (m *Model) jumpForward() {
	if m.jumpPos >= len(m.jumpList)-1 {
		return
	}
	m.jumpPos++
	m.restoreJumpEntry(m.jumpList[m.jumpPos])
}

// restoreJumpEntry moves to entry's recorded position: switching view
// first (without disturbing the jump list itself — see
// switchToViewNoJump) if it differs from the current one, then focusing
// its headline (or file, as a fallback — see focusTarget). Silently a
// no-op beyond the view switch if the headline/file is no longer
// present in that view (e.g. deleted since, or no longer meeting
// whatever criteria the view filters on) — same as focusHeadline itself
// already does, rather than erroring on a stale entry.
func (m *Model) restoreJumpEntry(entry jumpEntry) {
	if m.view != entry.view {
		m.switchToViewNoJump(entry.view)
	}
	m.focusTarget(entry.headline, entry.file)
}

// appendHelpRows populates m.rows for :help: README.md's embedded
// content (see WithReadme), rendered through glamour into ANSI-styled
// terminal markdown — headers, emphasis, code blocks, and (the original
// motivation for using glamour at all) properly aligned tables — one
// row per rendered line, same plain-text-row treatment as :log/:diff
// otherwise (no further parsing here). Falls back to the raw markdown
// if rendering itself fails (glamour has no reason to fail on our own
// known-good README, but every other external-ish dependency in this
// codebase is handled defensively too). A binary built without a
// readme wired up (WithReadme never called, e.g. a bare Model{} in a
// test) shows a placeholder instead of an empty view.
func (m *Model) appendHelpRows() {
	if m.readme == "" {
		m.rows = append(m.rows, row{isTextLine: true, text: "No help available."})
		return
	}
	rendered, err := renderMarkdown(m.readme, m.helpWrapWidth())
	if err != nil {
		rendered = m.readme
	}
	for _, line := range strings.Split(strings.TrimRight(rendered, "\n"), "\n") {
		m.rows = append(m.rows, row{isTextLine: true, text: line})
	}
}

// helpWrapWidth is the column width :help's markdown rendering wraps
// to: the terminal's actual width once known (see the WindowSizeMsg
// case in Update, which rebuilds help view's rows on a resize since
// they're wrapped once here rather than at render time), or a
// reasonable default before that first arrives — including in tests,
// which mostly never send one at all.
func (m *Model) helpWrapWidth() int {
	if m.width > 0 {
		return m.width
	}
	return 80
}

// renderMarkdown renders src as terminal-styled markdown via glamour,
// wrapped to width, matching the app's own light/dark and color-profile
// detection (via lipgloss, already resolved and cached from ordinary
// rendering elsewhere) so :help's colors look consistent with
// everything else rather than picking their own independently.
func renderMarkdown(src string, width int) (string, error) {
	style := "light"
	if lipgloss.HasDarkBackground() {
		style = "dark"
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle(style),
		glamour.WithColorProfile(lipgloss.ColorProfile()),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return "", err
	}
	return r.Render(src)
}

// appendConfigRows populates m.rows for config view: one read-only line
// per configurable setting, showing its effective current value (after
// flags/config-file/built-in-default resolution has already happened in
// main.go — this view has no idea which of those a value came from,
// only what it ended up as).
func (m *Model) appendConfigRows() {
	line := func(format string, args ...any) {
		m.rows = append(m.rows, row{isTextLine: true, text: fmt.Sprintf(format, args...)})
	}

	line("Org directory: %s", m.ws.Dir)

	editor := m.editorCommand()
	if editor == "" {
		editor = "vim (default)"
	}
	line("Editor: %s", editor)

	if m.urlFormatterCmd == "" {
		line("URL formatter: (disabled)")
	} else {
		line("URL formatter: %s", m.urlFormatterCmd)
	}
	prefixes := "(none)"
	if len(m.urlFormatterPrefixes) > 0 {
		prefixes = strings.Join(m.urlFormatterPrefixes, ", ")
	}
	line("URL formatter prefixes: %s", prefixes)

	if formatLinksCmd := m.formatLinksFormatterCmd(); formatLinksCmd == "" {
		line("Format-links URL formatter: (disabled)")
	} else if m.formatLinksURLFormatterCmd != "" {
		line("Format-links URL formatter: %s", formatLinksCmd)
	} else {
		line("Format-links URL formatter: %s (same as URL formatter)", formatLinksCmd)
	}

	line("Agenda window: %d days", m.agendaDays)
	line("Inbox file: %s", m.inboxFile)
	line("Calendar file: %s", m.calendarFile)
	line("Hide done after: %d hours (currently %s — :toggledone to switch)", m.hideDoneAfterHours, onOff(m.hideDoneEnabled))
	line("Debug logging: %s", onOff(m.debug))

	if m.gcalOAuthClientID == "" || m.gcalOAuthClientSecret == "" {
		line("Calendar sync: (not configured — see README's Calendar sync section)")
	} else {
		line("Calendar sync: %s, -%dd/+%dd window", strings.Join(m.gcalCalendarIDs, ", "), m.gcalSyncPastDays, m.gcalSyncFutureDays)
		if len(m.gcalAttendeeTagDomains) > 0 {
			line("Attendee tag domains: %s", strings.Join(m.gcalAttendeeTagDomains, ", "))
		} else {
			line("Attendee tag domains: (none — every confirmed attendee is tagged)")
		}
		if len(m.gcalAttendeeIgnorePatterns) > 0 {
			line("Attendee ignore patterns: %s", strings.Join(m.gcalAttendeeIgnorePatterns, ", "))
		}
	}

	line("Gutter icons: dirty %q (%s), mark (%s), clarify %q (%s), lock %q (%s), meeting %q (%s)",
		orDefault(m.dirtyIcon, defaultDirtyIcon), orDefault(m.dirtyColor, defaultDirtyColor),
		orDefault(m.markColor, defaultMarkColor),
		orDefault(m.clarifyIcon, defaultClarifyIcon), orDefault(m.clarifyColor, defaultClarifyColor),
		orDefault(m.lockIcon, defaultLockIcon), orDefault(m.lockColor, defaultLockColor),
		orDefault(m.meetingIcon, defaultMeetingIcon), orDefault(m.meetingColor, defaultMeetingColor))
}

// appendLogRows populates m.rows for :log — every external command
// orgtd has run since startup (see execLog), oldest first, each entry
// (a command starting — with its arguments — one line fed to its stdin,
// one of its output lines, or its exit code) stamped with its own
// timestamp, which stream it came from if applicable, and the process's
// pid ("-" if it never actually started), so entries from two commands
// that happened to run concurrently can still be told apart. A snapshot
// taken right now — if a :format-links batch (or anything else) logs
// more while this view is already open, re-run :log to see it; the view
// itself doesn't live-update.
func (m *Model) appendLogRows() {
	entries := m.execLog.snapshot()
	if len(entries) == 0 {
		m.rows = append(m.rows, row{isTextLine: true, text: "No external commands have been run yet."})
		return
	}
	for _, e := range entries {
		m.rows = append(m.rows, row{isTextLine: true, text: fmt.Sprintf("%s  %-6s  pid %-7s  %s", e.time.Format("15:04:05.000"), e.kind.label(), pidLabel(e.pid), e.text)})
	}
}

// appendDiffRows populates m.rows for :diff: the working-tree diff (see
// showDiff/runGitDiff) for every file currently open in the outline
// (excluding the calendar file — see gitFiles), one row per line, verbatim (like :log's output lines, no further parsing
// or styling) — or a placeholder if there's nothing to show, no files
// are open, or the diff itself failed despite the workspace being a
// proper git repository root (showDiff already refuses before this is
// ever reached otherwise) — e.g. a repository with no commits yet at
// all, so there's no HEAD to diff against.
func (m *Model) appendDiffRows() {
	if m.diffErr != "" {
		m.rows = append(m.rows, row{isTextLine: true, text: fmt.Sprintf("git diff failed: %s", m.diffErr)})
		return
	}
	if len(m.ws.Files) == 0 {
		m.rows = append(m.rows, row{isTextLine: true, text: "No files open in the outline."})
		return
	}
	if strings.TrimSpace(m.diffOutput) == "" {
		m.rows = append(m.rows, row{isTextLine: true, text: "No changes."})
		return
	}
	for _, line := range strings.Split(m.diffOutput, "\n") {
		m.rows = append(m.rows, row{isTextLine: true, text: line})
	}
}

// showDiff (":diff") shows the result of `git diff` for every file
// currently open in the outline — refusing altogether unless the
// workspace is the root of its git repository (see
// gitRepoRootRefusal), same as :commit: a diff run from some
// subdirectory of a larger repo (or outside a repo entirely) would
// never be able to offer adding an untracked file either, so showing it
// at all would be misleading about what :commit could actually do with
// it. Otherwise, first checks whether any open file isn't tracked by
// git yet (see requestAddUntracked); if so, this pauses on that
// question and only actually runs the diff (via runDiffNow) once it's
// answered.
func (m *Model) showDiff() {
	if len(m.gitFiles()) > 0 {
		if reason := m.gitRepoRootRefusal(); reason != "" {
			m.message = fmt.Sprintf("Refusing to diff: %s", reason)
			return
		}
		if untracked, err := m.untrackedFiles(); err == nil && len(untracked) > 0 {
			m.requestAddUntracked(untracked, func(m *Model) { m.runDiffNow() })
			return
		}
	}
	m.runDiffNow()
}

// runDiffNow does showDiff's actual work once there's nothing left to
// ask about: runs `git diff` and switches to diff view to show the
// result. Run synchronously — unlike :format-links' potentially slow,
// arbitrary external formatter, `git diff` on a handful of local org
// files is fast, so there's no need for the async tea.Cmd/Msg dance
// that keeps the app responsive during a longer-running command.
func (m *Model) runDiffNow() {
	m.diffOutput, m.diffErr = "", ""
	if len(m.gitFiles()) > 0 {
		out, err := m.runGitDiff()
		if err != nil {
			m.diffErr = gitErrorText(err)
		} else {
			m.diffOutput = out
		}
	}
	m.switchToView(diffView)
}

// runGitDiff runs `git diff HEAD` scoped to every file currently open in
// the outline except the calendar file (see gitFiles), with git itself pointed at the workspace
// directory (via -C, rather than relying on orgtd's own working
// directory) so a repository rooted there or above is found either way.
// Diffed against HEAD rather than a plain `git diff` (which only shows
// unstaged changes) so a file `git add`ed via requestAddUntracked but
// not yet committed still shows up as an addition here, instead of
// looking like nothing happened. Logged like any other external
// command — see runLoggedCommand.
func (m *Model) runGitDiff() (string, error) {
	args := []string{"-C", m.ws.Dir, "diff", "HEAD", "--"}
	for _, f := range m.gitFiles() {
		args = append(args, f.Path)
	}
	return runLoggedCommand(m.execLog, "git", args, "")
}

// untrackedFiles returns the paths, among m.gitFiles(), that git doesn't
// track at all yet — via `git ls-files --others --exclude-standard`,
// scoped to just those paths so files elsewhere in the repo (or
// gitignored entirely) never show up. Returns (nil, nil) if there are
// no open files or nothing is untracked; the error return is only for a
// genuine failure to even ask (not a git repository, git missing,
// ...) — callers treat that the same as "nothing untracked" and let the
// diff/commit that follows surface the real problem instead.
func (m *Model) untrackedFiles() ([]string, error) {
	gitFiles := m.gitFiles()
	if len(gitFiles) == 0 {
		return nil, nil
	}
	args := []string{"-C", m.ws.Dir, "ls-files", "--others", "--exclude-standard", "--"}
	for _, f := range gitFiles {
		args = append(args, f.Path)
	}
	out, err := runLoggedCommand(m.execLog, "git", args, "")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(out) == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// gitRepoRoot returns the git repository root that contains m.ws.Dir —
// via `git rev-parse --show-toplevel` — or "" if ws.Dir isn't inside a
// git repository at all (or git itself failed). Logged like any other
// external command.
func (m *Model) gitRepoRoot() string {
	out, err := runLoggedCommand(m.execLog, "git", []string{"-C", m.ws.Dir, "rev-parse", "--show-toplevel"}, "")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// gitRepoRootRefusal reports why mutating git commands (add, commit,
// push — see requireGitRepoRoot) must refuse to run against the current
// workspace, or "" if they're fine to run. They're refused unless the
// org directory is itself the *root* of its git repository, not merely
// somewhere inside one: git add/commit are scoped to specific files, so
// on their own a nested workspace wouldn't be too dangerous, but git
// push is not scoped to files at all — it pushes the *whole* current
// branch — and a workspace that's really just a subdirectory of some
// larger, unrelated repository (e.g. orgtd's own testdata/orgdir,
// nested inside this very repo) must never have that run against it.
// Paths are compared after resolving symlinks (see filepath.EvalSymlinks)
// since e.g. macOS routes /tmp and /var through symlinks into /private —
// comparing raw paths would otherwise misreport plenty of genuinely
// rooted workspaces (anything under the system's temp dir included) as
// nested elsewhere.
func (m *Model) gitRepoRootRefusal() string {
	root := m.gitRepoRoot()
	if root == "" {
		return "the org directory isn't inside a git repository"
	}
	wsResolved, wsErr := filepath.EvalSymlinks(m.ws.Dir)
	rootResolved, rootErr := filepath.EvalSymlinks(root)
	if wsErr != nil || rootErr != nil || wsResolved != rootResolved {
		return fmt.Sprintf("the org directory isn't the root of its git repository (root is %s)", root)
	}
	return ""
}

// requireGitRepoRoot is the guard every mutating git operation (gitAdd,
// runGitCommit, runGitPush) checks before doing anything — see
// gitRepoRootRefusal for why. Checked there directly (rather than only
// at the higher-level call sites that ask about it first, for a better
// error message — see showDiff/startCommit) so there's no way to reach
// an actual mutation without passing this, regardless of how it's
// eventually called.
func (m *Model) requireGitRepoRoot() error {
	if reason := m.gitRepoRootRefusal(); reason != "" {
		return fmt.Errorf("%s", reason)
	}
	return nil
}

// requestAddUntracked interrupts :diff/:commit with a y/N confirmation
// (reusing confirmMode, alongside its existing file-edit prompt — see
// pendingUntrackedFiles/pendingUntrackedThen) when untrackedFiles found
// something. then runs either way once answered (see
// updateConfirmMode): `git add`ing untracked first if accepted, or
// completely unchanged if declined — a deliberately untracked file
// shouldn't block diffing/committing everything else.
func (m *Model) requestAddUntracked(untracked []string, then func(m *Model)) {
	m.mode = confirmMode
	m.confirmMessage = fmt.Sprintf("Not tracked by git: %s. Add to git? [y/N]", strings.Join(untracked, ", "))
	m.pendingUntrackedFiles = untracked
	m.pendingUntrackedThen = then
}

// gitAdd runs `git add` for exactly the given paths (as returned by
// untrackedFiles — already suitable as pathspecs from within the
// workspace directory), so accepting requestAddUntracked's prompt never
// stages anything beyond what it named. Refuses outside the workspace's
// own git repository root — see requireGitRepoRoot. Logged like any
// other external command.
func (m *Model) gitAdd(paths []string) (string, error) {
	if err := m.requireGitRepoRoot(); err != nil {
		return "", err
	}
	args := append([]string{"-C", m.ws.Dir, "add", "--"}, paths...)
	return runLoggedCommand(m.execLog, "git", args, "")
}

// gitErrorText extracts the most useful message from a failed git
// invocation (diff, commit, or push): git's own stderr (e.g. "fatal: not
// a git repository...") when there is one, else the raw error (e.g.
// "git" not being installed at all).
func gitErrorText(err error) string {
	if exitErr, ok := err.(*exec.ExitError); ok {
		if msg := strings.TrimSpace(string(exitErr.Stderr)); msg != "" {
			return msg
		}
	}
	return err.Error()
}

// startCommit (":commit") opens a one-line prompt for a commit message
// (see updateCommitMessageMode/applyCommitMessageInput), restricted to
// diff view: :commit only makes sense once you've actually looked at
// what's about to be committed via :diff, and reusing that view's own
// file scope — rather than letting :commit imply some other set of
// files — keeps "what :diff shows" and "what :commit commits" the same
// thing. Since :commit always ends in a mutating git add/commit/push,
// it refuses altogether unless the workspace is safe to run those
// against (see gitRepoRootRefusal) — checked up front, before even
// asking about untracked files, so declining that question is never
// even on the table when the real problem is the repository itself.
// Otherwise, as with :diff, first checks for files git doesn't track at
// all yet (see requestAddUntracked) — `git commit -- <pathspec>`
// silently skips a file that was never even `git add`ed once, so
// without this an untracked org file would just never make it into a
// commit.
func (m *Model) startCommit() {
	if m.view != diffView {
		m.message = ":commit only works in diff view — see :diff"
		return
	}
	if reason := m.gitRepoRootRefusal(); reason != "" {
		m.message = fmt.Sprintf("Refusing to commit: %s", reason)
		return
	}
	if untracked, err := m.untrackedFiles(); err == nil && len(untracked) > 0 {
		m.requestAddUntracked(untracked, func(m *Model) { m.openCommitPrompt() })
		return
	}
	m.openCommitPrompt()
}

// openCommitPrompt opens the one-line commit-message prompt itself,
// split out from startCommit so requestAddUntracked can defer straight
// to it once its own question is answered.
func (m *Model) openCommitPrompt() {
	m.mode = commitMessageMode
	m.commitMessageInput = ""
}

// updateCommitMessageMode handles the one-line commit-message prompt
// opened by startCommit — Enter applies it (see applyCommitMessageInput),
// Esc cancels without committing anything. Mirrors updateDeadlineMode.
func (m Model) updateCommitMessageMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type != tea.KeyEnter {
		m.message = ""
	}

	switch msg.Type {
	case tea.KeyEsc:
		m.mode = normalMode
		m.commitMessageInput = ""
		return m, nil

	case tea.KeyEnter:
		return m.applyCommitMessageInput()

	case tea.KeyBackspace:
		if r := []rune(m.commitMessageInput); len(r) > 0 {
			m.commitMessageInput = string(r[:len(r)-1])
		}
		return m, nil

	case tea.KeySpace:
		m.commitMessageInput += " "
		return m, nil

	case tea.KeyRunes:
		m.commitMessageInput += string(msg.Runes)
		return m, nil
	}
	return m, nil
}

// applyCommitMessageInput commits every file currently open in the
// outline except the calendar file (the same scope :diff shows) with the typed message, then
// pushes — both run synchronously, same tradeoff as showDiff (simple,
// but blocks the UI for as long as git takes to respond, including,
// for the push, however long the remote takes). An empty message leaves
// the prompt open rather than committing with nothing to describe the
// change. A failed commit leaves the working tree untouched and never
// attempts the push; a failed push still leaves the commit in place, so
// the diff view is refreshed either way to show whatever actually
// happened.
func (m Model) applyCommitMessageInput() (tea.Model, tea.Cmd) {
	m.message = ""
	input := strings.TrimSpace(m.commitMessageInput)
	if input == "" {
		m.message = "Commit message can't be empty"
		return m, nil
	}

	m.mode = normalMode
	m.commitMessageInput = ""

	if _, err := m.runGitCommit(input); err != nil {
		m.message = fmt.Sprintf("git commit failed: %s", gitErrorText(err))
		return m, nil
	}
	if _, err := m.runGitPush(); err != nil {
		m.message = fmt.Sprintf("Committed, but git push failed: %s", gitErrorText(err))
		m.showDiff()
		return m, nil
	}

	m.message = "Committed and pushed"
	m.showDiff()
	return m, nil
}

// runGitCommit commits every file currently open in the outline except
// the calendar file (m.gitFiles() — the same scope runGitDiff uses) with message, from
// within the workspace directory. Refuses outside the workspace's own
// git repository root — see requireGitRepoRoot. Logged like any other
// external command — see runLoggedCommand.
func (m *Model) runGitCommit(message string) (string, error) {
	if err := m.requireGitRepoRoot(); err != nil {
		return "", err
	}
	args := []string{"-C", m.ws.Dir, "commit", "-m", message, "--"}
	for _, f := range m.gitFiles() {
		args = append(args, f.Path)
	}
	return runLoggedCommand(m.execLog, "git", args, "")
}

// runGitPush runs a plain `git push` from within the workspace
// directory — unlike diff and commit, a push isn't scoped to particular
// files (there's no such thing as pushing only some files' history), so
// it just pushes the current branch to its configured upstream. This is
// exactly why requireGitRepoRoot matters most here: a push affects the
// whole repository's history, not just the org files orgtd knows about.
// Logged like any other external command — see runLoggedCommand.
func (m *Model) runGitPush() (string, error) {
	if err := m.requireGitRepoRoot(); err != nil {
		return "", err
	}
	return runLoggedCommand(m.execLog, "git", []string{"-C", m.ws.Dir, "push"}, "")
}

// onOff renders b as "on"/"off", for a status line reporting a toggle's
// current state (e.g. hide-done filtering in appendConfigRows).
func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// debugLogHint returns a parenthesized suffix pointing a failure message
// at debug.log when debug logging is actually on (see WithDebug); when
// it's off, nothing was written there, so this instead points at how to
// turn it on rather than sending the user to a file that doesn't exist.
func (m Model) debugLogHint() string {
	if m.debug {
		return " (see debug.log)"
	}
	return " (rerun with --debug for details)"
}

// toggleHideDone flips whether stale DONE/CANCELLED items (older than
// hideDoneAfterHours, per CLOSED) are hidden from the outline — a full
// on/off switch for the filtering, independent of the configured
// threshold, so a stale item is never more than a ":toggledone" away.
// Re-focuses the headline the cursor was on before the toggle, if it's
// still present among the rebuilt rows.
func (m *Model) toggleHideDone() {
	h := m.currentHeadline()
	m.hideDoneEnabled = !m.hideDoneEnabled
	m.rebuildRows()
	if h != nil {
		m.focusHeadline(h)
	}
	if m.hideDoneEnabled {
		m.message = fmt.Sprintf("Hiding DONE/CANCELLED items closed more than %dh ago", m.hideDoneAfterHours)
	} else {
		m.message = "Showing all DONE/CANCELLED items"
	}
}

// ignoreFold, when true, descends into every child/body regardless of
// collapsed[h] — used by searchRows (see below) to build the full
// outline text search scans, so a match inside a folded subtree isn't
// skipped the way it would be for ordinary rendering.
func (m *Model) appendHeadlines(dst *[]row, headlines []*org.Headline, ignoreFold bool) {
	for _, h := range headlines {
		if m.hiddenAsStaleDone(h) {
			continue
		}
		*dst = append(*dst, row{headline: h, level: h.Level})
		if ignoreFold || !m.collapsed[h] {
			m.appendBodyLines(dst, h)
			if len(h.Children) > 0 {
				m.appendHeadlines(dst, h.Children, ignoreFold)
			}
		}
	}
}

// appendCalendarHeadlines is appendHeadlines' calendarView counterpart:
// same recursion (body lines, children), but each row is marked
// isCalendarItem (see renderCalendarItemRowWithBg) instead of rendered
// the outline's usual way, and every event starts folded the first time
// it's ever shown — its Location/Description/link body is meeting
// detail you don't need at a glance, and stays one Tab away rather than
// cluttering every day's listing by default. "The first time" means
// exactly that: once a headline has an entry in m.collapsed at all
// (whether the user folded or unfolded it), that choice sticks across
// rebuilds instead of being reset back to folded on every redraw. Any
// item linked to the event — attached via "gM", or sharing a tag with
// it (see linkedMeetingItems) — is shown right after it regardless of
// fold state — unlike the body, it's not detail about the meeting
// itself but something that needs attention, so it isn't worth hiding
// behind an extra Tab. It's appended after the
// body/children (rather than unconditionally right after the event
// row), so unfolding an event reveals its own detail directly beneath
// it, not pushed down past whatever's attached.
func (m *Model) appendCalendarHeadlines(dst *[]row, headlines []*org.Headline, ignoreFold bool) {
	for _, h := range headlines {
		if m.hiddenAsStaleDone(h) {
			continue
		}
		if _, ok := m.collapsed[h]; !ok {
			m.collapsed[h] = true
		}
		*dst = append(*dst, row{headline: h, level: h.Level, isCalendarItem: true})
		if ignoreFold || !m.collapsed[h] {
			m.appendBodyLines(dst, h)
			if len(h.Children) > 0 {
				m.appendCalendarHeadlines(dst, h.Children, ignoreFold)
			}
		}
		for _, item := range m.linkedMeetingItems(h) {
			*dst = append(*dst, row{headline: item, level: h.Level + 1, isCalendarLinkedItem: true})
		}
	}
}

// hiddenAsStaleDone reports whether h should be omitted from the outline
// (along with its whole subtree, and any body text) because hide-done
// filtering is enabled (see :toggledone) and h is a DONE/CANCELLED
// headline whose CLOSED timestamp is further than hideDoneAfterHours in
// the past. A DONE/CANCELLED headline with no CLOSED timestamp (e.g.
// hand-edited) or an unparseable one is never hidden — there's no age to
// judge it by.
func (m *Model) hiddenAsStaleDone(h *org.Headline) bool {
	if !m.hideDoneEnabled || !org.IsDoneKeyword(h.Keyword) || h.Closed == nil {
		return false
	}
	closed, _, err := parseFlexibleDate(h.Closed.Raw)
	if err != nil {
		return false
	}
	return time.Since(closed) > time.Duration(m.hideDoneAfterHours)*time.Hour
}

// appendBodyLines appends one row per line of h's free-text body,
// indented one level deeper than h's own row (matching where a child
// would sit) — shown right under h's title, before its children, the
// same order the raw org file itself keeps them in. Subject to the same
// collapsed[h] flag as h's children (see appendHeadlines): one fold
// toggle shows or hides both together.
func (m *Model) appendBodyLines(dst *[]row, h *org.Headline) {
	for _, line := range visibleBodyLines(h) {
		*dst = append(*dst, row{headline: h, level: h.Level + 1, isBodyLine: true, bodyText: line})
	}
}

// hasFoldableContent reports whether h has anything a fold command
// could show or hide: children, a body, or both.
func hasFoldableContent(h *org.Headline) bool {
	return len(h.Children) > 0 || len(visibleBodyLines(h)) > 0
}

// visibleBodyLines returns h.Body with any trailing blank lines
// stripped. Org files conventionally have a blank line separating a
// headline from the next one, which the parser has no way to
// distinguish from deliberate trailing whitespace in the body — without
// this, that separator would show up as a meaningless empty line under
// nearly every single entry. Deliberate blank lines *within* a
// multi-paragraph body (not at the very end) are left alone.
func visibleBodyLines(h *org.Headline) []string {
	lines := h.Body
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return lines[:end]
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		if m.view == helpView {
			// Unlike every other view, help view's rows are pre-wrapped
			// to a specific width by glamour (see appendHelpRows) rather
			// than wrapped at render time — so a resize while it's open
			// needs a full rebuild, not just a new pageSize().
			m.rebuildRows()
		}
		return m, nil

	case editFinishedMsg:
		return m.finishEdit(msg)

	case fileEditFinishedMsg:
		return m.finishEditFile(msg)

	case formatLinksMsg:
		return m.finishFormatLinks(msg)

	case syncCalendarMsg:
		return m.finishSyncCalendar(msg)

	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			if len(m.dirty) == 0 || m.pendingForceQuit {
				return m, tea.Quit
			}
			// Mirrors ":q" refusing with unsaved changes (see
			// updateCommandMode) — but ctrl+c has always been an
			// unconditional, no-questions-asked quit, so rather than
			// require switching to ":q!" it accepts a second ctrl+c
			// right after this one as "yes, I meant it".
			m.pendingForceQuit = true
			m.message = "Unsaved changes — :w to save, or ctrl-c again to discard them"
			return m, nil
		}
		m.pendingForceQuit = false

		switch m.mode {
		case commandMode:
			return m.updateCommandMode(msg)
		case selectMode:
			return m.updateSelectMode(msg)
		case meetingPickerMode:
			return m.updateMeetingPickerMode(msg)
		case tagMode:
			return m.updateTagMode(msg)
		case deadlineMode:
			return m.updateDeadlineMode(msg)
		case searchMode:
			return m.updateSearchMode(msg)
		case confirmMode:
			return m.updateConfirmMode(msg)
		case commitMessageMode:
			return m.updateCommitMessageMode(msg)
		case visualMode:
			return m.updateVisualMode(msg)
		default:
			return m.updateNormalMode(msg)
		}
	}

	return m, nil
}

func (m Model) updateNormalMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	wasPendingG := m.pendingG
	wasPendingD := m.pendingD
	wasPendingY := m.pendingY
	wasPendingGT := m.pendingGT
	wasPendingLT := m.pendingLT
	wasPendingZ := m.pendingZ
	wasPendingM := m.pendingM
	wasPendingQuote := m.pendingQuote
	m.pendingG = false
	m.pendingD = false
	m.pendingY = false
	m.pendingGT = false
	m.pendingLT = false
	m.pendingZ = false
	m.pendingM = false
	m.pendingQuote = false
	m.message = ""

	// "m<letter>" and "'<letter>" take an arbitrary a-z argument, unlike
	// every other chord here (which pairs two fixed keys) — so these are
	// intercepted before the switch below, rather than adding a
	// wasPendingM/wasPendingQuote check to every single-letter case that
	// already means something else on its own (r, d, p, ...).
	if wasPendingM {
		if len(key) == 1 && key[0] >= 'a' && key[0] <= 'z' {
			m.setMark(rune(key[0]))
		}
		m.ensureVisible()
		return m, nil
	}
	if wasPendingQuote {
		if len(key) == 1 && key[0] >= 'a' && key[0] <= 'z' {
			m.jumpToMark(rune(key[0]))
		}
		m.ensureVisible()
		return m, nil
	}

	// A digit builds up a numeric prefix for "dd"/"r"/"R" (e.g. "3dd",
	// "2r", "2R") instead of being handled by the switch below —
	// intercepted here for the same reason as m/' above. A leading zero
	// (no digits typed yet) is not a valid count on its own — there's no
	// "0" command to distinguish it from — so it falls through as a
	// plain, currently unbound key instead of starting a count. Any other
	// key that isn't "d", "r", or "R" themselves clears a pending count
	// rather than silently applying to some other command later — the
	// prefix is scoped to exactly these three.
	if len(key) == 1 && key[0] >= '0' && key[0] <= '9' {
		if d := int(key[0] - '0'); d > 0 || m.pendingCount > 0 {
			m.pendingCount = m.pendingCount*10 + d
		}
		m.ensureVisible()
		return m, nil
	}
	if key != "d" && key != "r" && key != "R" {
		m.pendingCount = 0
	}

	switch key {
	case ":":
		m.mode = commandMode
		m.commandInput = ""
		m.commandHistoryPos = len(m.commandHistory)
		m.commandHistoryDraft = ""
		return m, nil

	case "/":
		m.mode = searchMode
		m.searchForward = true
		m.searchOrigin = m.cursor
		m.searchQuery = ""
		return m, nil

	case "?":
		m.mode = searchMode
		m.searchForward = false
		m.searchOrigin = m.cursor
		m.searchQuery = ""
		return m, nil

	case "n":
		m.repeatSearch(m.lastSearchForward)

	case "N":
		m.repeatSearch(!m.lastSearchForward)

	case "j", "down":
		m.moveCursor(1)

	case "k", "up":
		m.moveCursor(-1)

	case "}":
		m.jumpParagraph(1)

	case "{":
		m.jumpParagraph(-1)

	case "l":
		m.moveDeeper()

	case "h":
		m.moveShallower()

	case "g":
		if wasPendingG {
			m.pushJump()
			m.cursor = 0
		} else {
			m.pendingG = true
		}

	case "G":
		if n := len(m.rows); n > 0 {
			m.pushJump()
			// Snap up to the entry's own title row if the very last row
			// happens to be one of its body lines, so the highlight (and
			// gc/editing commands) cover the whole entry, not just its
			// last line.
			m.cursor = m.entryStart(n - 1)
		}

	case "^":
		m.jumpToSubtreeTop()

	case "$":
		m.jumpToSubtreeBottom()

	case "enter":
		m.jumpToSource()

	case "tab":
		m.toggleFold()

	case "ctrl+o":
		m.jumpBack()

	case "r", "R":
		count := m.pendingCount
		m.pendingCount = 0
		if m.currentHeadline() != nil {
			m.mode = selectMode
			if count > 1 {
				m.selectModeTargets = m.headlinesInRowRange(m.countRowRange(count))
			} else {
				m.selectModeTargets = nil
			}
			m.selectFilter = ""
			m.selectIndex = m.currentStatusIndex()
		}

	case "V":
		if len(m.rows) > 0 {
			m.mode = visualMode
			m.visualAnchor = m.cursor
		}

	case "i":
		if wasPendingG {
			m.jumpForward()
		} else if cmd := m.startEdit(); cmd != nil {
			return m, cmd
		}

	case "o":
		if wasPendingZ {
			m.foldOpen()
		} else if cmd := m.insertHeadline(false); cmd != nil {
			return m, cmd
		}

	case "O":
		if wasPendingZ {
			m.foldOpenAll()
		} else if cmd := m.insertHeadline(true); cmd != nil {
			return m, cmd
		}

	case "z":
		m.pendingZ = true

	case "m":
		m.pendingM = true

	case "'":
		m.pendingQuote = true

	case "c":
		switch {
		case wasPendingZ:
			m.foldClose()
		case wasPendingG:
			m.jumpToClarifyTarget()
		}

	case "C":
		if wasPendingZ {
			m.foldCloseAll()
		} else if wasPendingG {
			if cmd := m.startCapture(); cmd != nil {
				return m, cmd
			}
		}

	case "M":
		if wasPendingG {
			m.startMeetingPicker()
		}

	case "X":
		if wasPendingG {
			if cmd := m.startCaptureAndPickMeeting(); cmd != nil {
				return m, cmd
			}
		}

	case "t":
		if wasPendingG {
			m.startTagPrompt()
		}

	case "a":
		if wasPendingZ {
			m.toggleFold()
		}

	case "A":
		if wasPendingZ {
			m.foldToggleAll()
		} else if cmd := m.startEditAppend(); cmd != nil {
			return m, cmd
		}

	case "u":
		m.undo()

	case "ctrl+r":
		m.redo()

	case "d":
		if wasPendingG {
			m.startSetDeadline()
			m.pendingCount = 0
		} else if wasPendingD {
			if m.pendingCount > 1 {
				m.deleteHeadlineCount(m.pendingCount)
			} else {
				m.deleteHeadline()
			}
			m.pendingCount = 0
		} else {
			m.pendingD = true
		}

	case ">":
		if wasPendingGT {
			m.demoteHeadline()
		} else {
			m.pendingGT = true
		}

	case "<":
		if wasPendingLT {
			m.promoteHeadline()
		} else {
			m.pendingLT = true
		}

	case "y":
		if wasPendingY {
			m.yankHeadline()
		} else {
			m.pendingY = true
		}

	case "p":
		m.pasteHeadline(false)

	case "P":
		m.pasteHeadline(true)

	case "ctrl+d":
		m.moveCursor(m.pageSize() / 2)

	case "ctrl+u":
		m.moveCursor(-m.pageSize() / 2)

	case "pgdown":
		m.moveCursor(m.pageSize())

	case "pgup":
		m.moveCursor(-m.pageSize())

	case "ctrl+e":
		m.scrollView(1)
		return m, nil

	case "ctrl+y":
		m.scrollView(-1)
		return m, nil
	}

	m.ensureVisible()
	return m, nil
}

// updateVisualMode handles "V" (visual line selection): plain navigation
// keys extend the selection (from visualAnchor to the cursor, snapped to
// whole entries — see visualRange) exactly as they move the cursor in
// normal mode, while "d", "y", and "r"/"R" act on every entry currently
// selected. Only a subset of normal mode's keys apply here — anything that
// isn't navigation or one of the bulk operations (editing a single entry,
// folding, marks, paste, ...) has no obvious bulk meaning and is left
// unbound rather than guessed at.
func (m Model) updateVisualMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	wasPendingG := m.pendingG
	m.pendingG = false
	m.message = ""

	switch key {
	case "esc", "V":
		m.exitVisualMode()

	case "j", "down":
		m.moveCursor(1)

	case "k", "up":
		m.moveCursor(-1)

	case "}":
		m.jumpParagraph(1)

	case "{":
		m.jumpParagraph(-1)

	case "l":
		m.moveDeeper()

	case "h":
		m.moveShallower()

	case "g":
		if wasPendingG {
			m.cursor = 0
		} else {
			m.pendingG = true
		}

	case "G":
		if n := len(m.rows); n > 0 {
			m.cursor = m.entryStart(n - 1)
		}

	case "^":
		m.jumpToSubtreeTop()

	case "$":
		m.jumpToSubtreeBottom()

	case "ctrl+d":
		m.moveCursor(m.pageSize() / 2)

	case "ctrl+u":
		m.moveCursor(-m.pageSize() / 2)

	case "pgdown":
		m.moveCursor(m.pageSize())

	case "pgup":
		m.moveCursor(-m.pageSize())

	case "ctrl+e":
		m.scrollView(1)
		return m, nil

	case "ctrl+y":
		m.scrollView(-1)
		return m, nil

	case "d":
		m.deleteVisualSelection()

	case "y":
		m.yankVisualSelection()

	case "r", "R":
		if headlines := m.visualSelectedHeadlines(); len(headlines) > 0 {
			m.mode = selectMode
			m.selectModeTargets = headlines
			m.selectFilter = ""
			m.selectIndex = m.currentStatusIndex()
		}
	}

	m.ensureVisible()
	return m, nil
}

// exitVisualMode leaves visual selection and returns to normal mode —
// used by Esc/V (cancel) and once a bulk operation (d, or R after a
// status is chosen) completes.
func (m *Model) exitVisualMode() {
	m.mode = normalMode
	m.selectModeTargets = nil
}

// visualRange returns the current visual selection's row range,
// inclusive, snapped (via entryStart/entryEnd, the same snapping the
// single-cursor highlight uses) so it always covers whole entries —
// never starting or ending mid-body.
func (m *Model) visualRange() (start, end int) {
	a, b := m.visualAnchor, m.cursor
	if a > b {
		a, b = b, a
	}
	return m.entryStart(a), m.entryEnd(b)
}

// countRowRange returns the row range covering n consecutive entries
// starting at the cursor's own entry, which counts as the first of the
// n — giving "dd"/"r"/"R" a numeric prefix (e.g. "3dd", "2R") the same
// row-range shape as a visual selection, so both can share
// headlinesInRowRange and the bulk operations built on it. n < 1 is
// treated as 1 (just the current entry, i.e. no prefix). Running out of
// rows before reaching n just stops at the last entry there is, the same
// way vim's own counted commands clamp at the end of the buffer.
func (m *Model) countRowRange(n int) (start, end int) {
	if n < 1 {
		n = 1
	}
	start = m.entryStart(m.cursor)
	end = start
	counted := 1
	for i := start + 1; i < len(m.rows) && counted < n; i++ {
		if m.rows[i].isBodyLine {
			continue
		}
		end = i
		counted++
	}
	return start, m.entryEnd(end)
}

// visualSelectedHeadlines returns every distinct headline with a row
// inside the current visual selection — see headlinesInRowRange.
func (m *Model) visualSelectedHeadlines() []*org.Headline {
	return m.headlinesInRowRange(m.visualRange())
}

// headlinesInRowRange returns every distinct headline with a row inside
// [start, end], in top-to-bottom order — shared by visualSelectedHeadlines
// (a visual-mode selection) and the numeric-prefix commands (via
// countRowRange). A selected headline may contribute several rows (its
// body, its children), but appears once here regardless; rows with no
// headline (file/section/:config rows) are skipped.
func (m *Model) headlinesInRowRange(start, end int) []*org.Headline {
	seen := make(map[*org.Headline]bool)
	var out []*org.Headline
	for i := start; i <= end && i >= 0 && i < len(m.rows); i++ {
		h := m.rows[i].headline
		if h == nil || seen[h] {
			continue
		}
		seen[h] = true
		out = append(out, h)
	}
	return out
}

// visualTopmostHeadlines filters headlines down to those not descended
// from another headline also in headlines — used by bulk delete, since
// deleting an ancestor already removes its whole subtree (see
// deleteHeadline), so a selected descendant needs no delete of its own
// (and, by the time headlines' own deletes actually run, attempting one
// would either be redundant or operate on a detached, no-longer-visible
// copy).
func visualTopmostHeadlines(headlines []*org.Headline) []*org.Headline {
	selected := make(map[*org.Headline]bool, len(headlines))
	for _, h := range headlines {
		selected[h] = true
	}
	var out []*org.Headline
	for _, h := range headlines {
		underSelectedAncestor := false
		for p := h.Parent; p != nil; p = p.Parent {
			if selected[p] {
				underSelectedAncestor = true
				break
			}
		}
		if !underSelectedAncestor {
			out = append(out, h)
		}
	}
	return out
}

// deleteVisualSelection removes every top-level selected entry (and its
// subtree) — the visual-mode equivalent of dd. See deleteHeadlineSet for
// the shared mechanics.
func (m *Model) deleteVisualSelection() {
	headlines := visualTopmostHeadlines(m.visualSelectedHeadlines())
	m.exitVisualMode()
	m.deleteHeadlineSet(headlines)
}

// yankVisualSelection copies every top-level selected entry (and its
// subtree) into the register — the visual-mode equivalent of yy — leaving
// the originals untouched. Like deleteVisualSelection, a selected entry
// whose ancestor is also selected contributes nothing separately, since
// the ancestor's own clone already carries its whole subtree along.
func (m *Model) yankVisualSelection() {
	headlines := visualTopmostHeadlines(m.visualSelectedHeadlines())
	m.exitVisualMode()
	if len(headlines) == 0 {
		return
	}
	clones := make([]*org.Headline, len(headlines))
	for i, h := range headlines {
		clones[i] = org.CloneHeadline(h)
	}
	m.register = clones
	m.message = "Yanked"
}

// deleteHeadlineCount implements a numeric-prefixed "dd" (e.g. "3dd"):
// deletes the current entry and the next n-1 entries. A no-op on a file
// row, matching plain dd. See deleteHeadlineSet for the shared mechanics.
func (m *Model) deleteHeadlineCount(n int) {
	if m.currentHeadline() == nil {
		return
	}
	headlines := visualTopmostHeadlines(m.headlinesInRowRange(m.countRowRange(n)))
	m.deleteHeadlineSet(headlines)
}

// deleteHeadlineSet removes every headline in headlines (each with its
// own subtree) — the shared implementation behind bulk delete, whether
// the selection came from visual mode (deleteVisualSelection) or a
// numeric prefix (deleteHeadlineCount). headlines is assumed already
// topmost-filtered (see visualTopmostHeadlines) — deleting an ancestor
// already removes its whole subtree, so a selected descendant needs no
// delete of its own. Deletions are grouped into one undo step per file
// touched (a batchAction — see undo.go), so a selection confined to a
// single file, overwhelmingly the common case, undoes in one step; a
// selection spanning files takes one step per file, since undo/dirty
// tracking is inherently per-file. Like plain dd, the whole set fills the
// paste register (top-to-bottom order preserved), so p/P pastes every
// deleted entry back as a group, in one call, at the destination. Any
// headline locked by :format-links is silently excluded first (see
// filterImmutable) rather than aborting the whole operation; the summary
// message notes how many, if any.
func (m *Model) deleteHeadlineSet(headlines []*org.Headline) {
	headlines, skipped := m.filterImmutable(headlines)
	if len(headlines) == 0 {
		if skipped > 0 {
			m.message = "All selected entries are locked by :format-links; nothing deleted"
		}
		return
	}

	type target struct {
		h      *org.Headline
		f      *org.File
		parent *org.Headline
		idx    int
	}
	var order []*org.File
	byFile := make(map[*org.File][]target)
	for _, h := range headlines {
		f, parent, idx := m.insertPosition(h)
		if idx < 0 {
			continue
		}
		if _, ok := byFile[f]; !ok {
			order = append(order, f)
		}
		byFile[f] = append(byFile[f], target{h, f, parent, idx})
	}

	for _, f := range order {
		targets := byFile[f]
		// Descending index so removing one entry doesn't shift another
		// still-to-be-removed entry's already-captured index — safe
		// regardless of parent, since a splice only ever affects its own
		// parent's list.
		sort.SliceStable(targets, func(i, j int) bool { return targets[i].idx > targets[j].idx })
		actions := make([]undoAction, len(targets))
		for i, t := range targets {
			actions[i] = &deleteAction{spliceAction{f: t.f, parent: t.parent, index: t.idx, headlines: []*org.Headline{t.h}, inTree: true}}
		}
		m.pushUndo(&batchAction{actions: actions})
		// clarifyTarget/marks bookkeeping, same as dd's deleteHeadline —
		// done after the delete is actually applied (advanceClarifyTarget
		// must see the removal to skip past the deleted entry, not just
		// re-read the same one that's about to go).
		for _, t := range targets {
			if m.view == clarifyView && t.h == m.clarifyTarget {
				m.advanceClarifyTarget()
			}
			org.Walk([]*org.Headline{t.h}, m.clearMarksFor)
		}
	}
	m.register = headlines
	m.message = fmt.Sprintf("Deleted %d entries", len(headlines))
	if skipped > 0 {
		m.message += fmt.Sprintf(" (%d skipped: locked by :format-links)", skipped)
	}
}

// buildStatusChangeAction returns the undoAction that setting h's
// keyword to keyword would produce — a repeatAdvanceAction if h is
// completing a repeating item (see repeatAdvanceForCompletion), or a
// plain statusChangeAction otherwise — without applying or pushing it,
// so bulk operations (visual-mode R) can batch several of these into one
// undo step the same way applyStatus handles a single one.
func (m *Model) buildStatusChangeAction(h *org.Headline, keyword string) undoAction {
	if org.IsDoneKeyword(keyword) && !org.IsDoneKeyword(h.Keyword) {
		if a := m.repeatAdvanceForCompletion(h); a != nil {
			return a
		}
	}

	newClosed := h.Closed
	switch {
	case org.IsDoneKeyword(keyword) && !org.IsDoneKeyword(h.Keyword):
		newClosed = &org.Timestamp{Raw: time.Now().Format("2006-01-02 Mon 15:04")}
	case !org.IsDoneKeyword(keyword) && org.IsDoneKeyword(h.Keyword):
		newClosed = nil
	}

	return &statusChangeAction{
		h:          h,
		f:          m.fileForHeadline(h),
		oldKeyword: h.Keyword,
		newKeyword: keyword,
		oldClosed:  h.Closed,
		newClosed:  newClosed,
	}
}

// applyStatusToHeadlineSet sets keyword on every headline in headlines —
// the shared implementation behind bulk status change, whether the
// selection came from visual mode or a numeric-prefixed R (see
// applyChosenStatus). Unlike bulk delete, a status change never cascades
// to descendants on its own, so every headline given is changed
// independently, not just the topmost ones (callers don't
// topmost-filter). Grouped into one undo step per file touched, same as
// deleteHeadlineSet. In clarify view, also advances past the pinned
// target if it just became DONE/CANCELLED (see
// advanceClarifyTargetIfDone).
func (m *Model) applyStatusToHeadlineSet(headlines []*org.Headline, keyword, label string) {
	headlines, skipped := m.filterImmutable(headlines)
	if len(headlines) == 0 {
		if skipped > 0 {
			m.message = "All selected entries are locked by :format-links; nothing changed"
		}
		return
	}

	var order []*org.File
	byFile := make(map[*org.File][]undoAction)
	for _, h := range headlines {
		f := m.fileForHeadline(h)
		if _, ok := byFile[f]; !ok {
			order = append(order, f)
		}
		byFile[f] = append(byFile[f], m.buildStatusChangeAction(h, keyword))
	}
	for _, f := range order {
		m.pushUndo(&batchAction{actions: byFile[f]})
	}
	m.message = fmt.Sprintf("Set %d entries to %s", len(headlines), label)
	if skipped > 0 {
		m.message += fmt.Sprintf(" (%d skipped: locked by :format-links)", skipped)
	}
	m.advanceClarifyTargetIfDone()
}

// updateSearchMode handles "/"/"?" incremental search: every keystroke
// re-searches from searchOrigin (not from wherever the previous partial
// query happened to land), so backspacing genuinely retypes the query
// rather than searching onward from the last match — matching vim's own
// incsearch behavior.
// updateConfirmMode handles a pending yes/no confirmation prompt (see
// confirmMessage). "y"/"Y" accepts; anything else — "n", Esc, or any
// other key — cancels, matching a typical CLI y/N prompt rather than
// requiring a specific "no" keystroke.
func (m Model) updateConfirmMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	accepted := msg.Type == tea.KeyRunes && (string(msg.Runes) == "y" || string(msg.Runes) == "Y")

	pendingFileEdit := m.pendingFileEdit
	untrackedFiles := m.pendingUntrackedFiles
	untrackedThen := m.pendingUntrackedThen
	m.mode = normalMode
	m.confirmMessage = ""
	m.pendingFileEdit = nil
	m.pendingUntrackedFiles = nil
	m.pendingUntrackedThen = nil

	// requestAddUntracked's question (unlike the file-edit one below)
	// continues either way once answered — declining just means
	// skipping the `git add`, not abandoning the :diff/:commit that
	// asked in the first place.
	if untrackedThen != nil {
		if accepted {
			if _, err := m.gitAdd(untrackedFiles); err != nil {
				m.message = fmt.Sprintf("git add failed: %s", gitErrorText(err))
				return m, nil
			}
		}
		untrackedThen(&m)
		return m, nil
	}

	if !accepted {
		return m, nil
	}
	if pendingFileEdit != nil {
		return m, m.startEditFile(pendingFileEdit)
	}
	return m, nil
}

func (m Model) updateSearchMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.mode = normalMode
		m.cursor = m.searchOrigin
		m.searchQuery = ""
		m.ensureVisible()
		return m, nil

	case tea.KeyEnter:
		m.mode = normalMode
		if m.searchQuery != "" {
			m.lastSearchQuery = m.searchQuery
			m.lastSearchForward = m.searchForward
			if m.cursor != m.searchOrigin {
				// searchOrigin, not m.cursor: incremental search has
				// already moved the cursor to the match by now — what
				// belongs in the jump list is where the search started
				// from, not where it landed.
				m.pushJumpAt(m.searchOrigin)
			}
		}
		m.searchQuery = ""
		return m, nil

	case tea.KeyBackspace:
		// Vim exits command-line-like modes when backspace is pressed
		// with nothing left to delete (see updateCommandMode); mirrored
		// here, reverting the cursor like Esc does.
		r := []rune(m.searchQuery)
		if len(r) == 0 {
			m.mode = normalMode
			m.cursor = m.searchOrigin
			m.ensureVisible()
			return m, nil
		}
		m.searchQuery = string(r[:len(r)-1])
		m.performIncrementalSearch()
		return m, nil

	case tea.KeySpace:
		m.searchQuery += " "
		m.performIncrementalSearch()
		return m, nil

	case tea.KeyRunes:
		m.searchQuery += string(msg.Runes)
		m.performIncrementalSearch()
		return m, nil
	}
	return m, nil
}

// performIncrementalSearch re-jumps the cursor from searchOrigin to the
// nearest match of the query typed so far, in searchForward's direction
// — called after every keystroke in searchMode. Leaves the cursor at
// searchOrigin if the query is empty or matches nothing. A match inside
// a folded subtree is unfolded to reveal it (see revealRow), which
// rebuilds m.rows and so shifts row indices — searchOrigin is
// re-resolved by identity afterward so it keeps naming the same row
// (rather than whatever now sits at its old index) for the rest of the
// incremental search, and for Esc/backspace-to-empty reverting to it.
func (m *Model) performIncrementalSearch() {
	m.cursor = m.searchOrigin
	if m.searchQuery != "" && m.searchOrigin >= 0 && m.searchOrigin < len(m.rows) {
		origin := m.rows[m.searchOrigin]
		if target, ok := m.findMatch(origin, m.searchQuery, m.searchForward); ok {
			m.revealRow(target)
			if idx := indexOfRow(m.rows, origin); idx >= 0 {
				m.searchOrigin = idx
			}
			if idx := indexOfRow(m.rows, target); idx >= 0 {
				m.cursor = idx
			}
		}
	}
	m.ensureVisible()
}

// repeatSearch ("n"/"N") repeats the last confirmed search from the
// current cursor position, in the given direction. Same reveal-then-
// relocate handling as performIncrementalSearch, for a match inside a
// folded subtree.
func (m *Model) repeatSearch(forward bool) {
	if m.lastSearchQuery == "" || m.cursor < 0 || m.cursor >= len(m.rows) {
		return
	}
	from := m.rows[m.cursor]
	if target, ok := m.findMatch(from, m.lastSearchQuery, forward); ok {
		m.revealRow(target)
		if idx := indexOfRow(m.rows, target); idx >= 0 {
			m.cursor = idx
		}
	}
}

// searchRows returns the rows "/"/"?"/"n"/"N" scan for a match: the same
// rows currently on screen for a view with no folding (agenda, config,
// log, diff, help — appendAgendaRows/appendConfigRows/etc. never gate on
// collapsed), but a fully expanded copy of the outline — every fold
// treated as open, via appendHeadlines/appendCalendarRows's ignoreFold —
// for the outline/clarify and calendar views. Otherwise a match inside a
// folded subtree, or a folded calendar event's Location/description
// body, would be invisible to search simply because its row was never
// built, rather than because it didn't match — unlike vim, where folding
// is a display-only concept and "/" always searches the whole buffer.
// revealRow (below) then unfolds just enough of the real outline to
// bring whatever's found here onto the actual screen.
func (m *Model) searchRows() []row {
	switch m.view {
	case outlineView, clarifyView:
		var rows []row
		for _, f := range m.ws.Files {
			if filepath.Base(f.Path) == m.calendarFile {
				continue
			}
			rows = append(rows, row{file: f})
			m.appendHeadlines(&rows, f.Headlines, true)
		}
		return rows
	case calendarView:
		var rows []row
		m.appendCalendarRows(&rows, true)
		return rows
	default:
		return m.rows
	}
}

// sameRow reports whether a and b refer to the same logical row —
// identity, not value equality (two distinct blank body lines under the
// same headline would otherwise be indistinguishable) — used to locate a
// row found via searchRows within the (possibly differently-folded) real
// m.rows, before and after revealRow.
func sameRow(a, b row) bool {
	switch {
	case a.file != nil || b.file != nil:
		return a.file == b.file
	case a.isBodyLine || b.isBodyLine:
		return a.isBodyLine == b.isBodyLine && a.headline == b.headline && a.bodyText == b.bodyText
	case a.headline != nil || b.headline != nil:
		// Covers plain headline rows and the isCalendarItem/
		// isCalendarLinkedItem variants alike — none of those flags
		// change what row a headline points at.
		return a.headline == b.headline
	case a.isMeetingHeader || b.isMeetingHeader:
		// headline is nil on both sides here, so nil == nil would
		// otherwise make every meeting header (and every section/
		// isTextLine row, below) look like the same row.
		return a.isMeetingHeader == b.isMeetingHeader && a.meetingTitle == b.meetingTitle && a.meetingStart.Equal(b.meetingStart)
	case a.isTextLine || b.isTextLine:
		return a.isTextLine == b.isTextLine && a.text == b.text
	default:
		return a.section == b.section
	}
}

// indexOfRow returns the index of the first row in rows identical to
// target (see sameRow), or -1 if it isn't there at all.
func indexOfRow(rows []row, target row) int {
	for i, r := range rows {
		if sameRow(r, target) {
			return i
		}
	}
	return -1
}

// revealRow unfolds whatever's necessary so target's row — found via
// searchRows, which searches the full outline regardless of fold state —
// actually appears in m.rows: every ancestor of its headline (so the
// headline's own row is reachable at all), and the headline itself too
// when target is one of its own body lines (gated on collapsed[h] the
// same as its children — see appendBodyLines/appendHeadlines). A no-op
// for a view with no folding to begin with (agenda, config, log, diff,
// help), and for the common case where target was already visible (no
// folds in the way). Deliberately doesn't restore folds it opens if the
// search is later cancelled (Esc) — same as vim, which leaves a fold
// opened by search open rather than closing it back up.
func (m *Model) revealRow(target row) {
	switch m.view {
	case outlineView, clarifyView, calendarView:
	default:
		return
	}
	h := target.headline
	if h == nil {
		return
	}
	changed := false
	if target.isBodyLine && m.collapsed[h] {
		m.collapsed[h] = false
		changed = true
	}
	for p := h.Parent; p != nil; p = p.Parent {
		if m.collapsed[p] {
			m.collapsed[p] = false
			changed = true
		}
	}
	if changed {
		m.rebuildRows()
	}
}

// findMatch searches the full outline (see searchRows) for the nearest
// row — excluding from itself — whose searchable text (see
// rowSearchText) contains query, case-insensitively, moving forward or
// backward from from and wrapping around the ends (vim's default
// 'wrapscan' behavior).
func (m *Model) findMatch(from row, query string, forward bool) (row, bool) {
	rows := m.searchRows()
	n := len(rows)
	if n == 0 {
		return row{}, false
	}
	start := indexOfRow(rows, from)
	if start < 0 {
		start = 0
	}
	q := strings.ToLower(query)
	step := 1
	if !forward {
		step = -1
	}
	for i := 1; i <= n; i++ {
		idx := ((start+step*i)%n + n) % n
		if strings.Contains(strings.ToLower(rowSearchText(rows[idx])), q) {
			return rows[idx], true
		}
	}
	return row{}, false
}

// rowSearchText returns the text of r that "/"/"?" search against.
func rowSearchText(r row) string {
	switch {
	case r.section != "":
		return r.section
	case r.file != nil:
		return filepath.Base(r.file.Path)
	case r.isBodyLine:
		return r.bodyText
	case r.isTextLine:
		return r.text
	case r.isMeetingHeader:
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

func (m Model) updateCommandMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type != tea.KeyTab {
		// Any key other than Tab dismisses a shown completion list, and
		// any error it left (e.g. "No command starting with ...") —
		// they're one-shot hints for the keystroke right after Tab, not
		// a persistent part of the command line.
		m.commandCompletions = ""
		m.message = ""
	}

	switch msg.Type {
	case tea.KeyEsc:
		m.mode = normalMode
		m.commandInput = ""
		return m, nil

	case tea.KeyEnter:
		return m.runCommand()

	case tea.KeyBackspace:
		// Vim exits command-line mode when backspace is pressed with
		// nothing left to delete.
		r := []rune(m.commandInput)
		if len(r) == 0 {
			m.mode = normalMode
			return m, nil
		}
		m.commandInput = string(r[:len(r)-1])
		return m, nil

	case tea.KeySpace:
		m.commandInput += " "
		return m, nil

	case tea.KeyRunes:
		m.commandInput += string(msg.Runes)
		return m, nil

	case tea.KeyTab:
		m.completeCommand()
		return m, nil

	case tea.KeyUp:
		m.recallCommandHistory(-1)
		return m, nil

	case tea.KeyDown:
		m.recallCommandHistory(1)
		return m, nil
	}

	return m, nil
}

// recallCommandHistory moves the command line to an older (dir < 0) or
// newer (dir > 0) entry in commandHistory, matching a shell's own
// history recall: the first ↑ saves whatever was already typed
// (commandHistoryDraft) so a later ↓ back past the most recent entry
// restores it instead of leaving the line blank. Clamped at both ends —
// ↑ stops at the oldest entry, ↓ stops back at the draft — rather than
// wrapping around.
func (m *Model) recallCommandHistory(dir int) {
	if len(m.commandHistory) == 0 {
		return
	}
	if m.commandHistoryPos == len(m.commandHistory) {
		if dir > 0 {
			return
		}
		m.commandHistoryDraft = m.commandInput
	}
	pos := m.commandHistoryPos + dir
	if pos < 0 {
		pos = 0
	}
	if pos > len(m.commandHistory) {
		pos = len(m.commandHistory)
	}
	m.commandHistoryPos = pos
	if pos == len(m.commandHistory) {
		m.commandInput = m.commandHistoryDraft
	} else {
		m.commandInput = m.commandHistory[pos]
	}
}

// runCommand executes the typed command line and always returns to
// normal mode.
// commandNames lists every command-mode word tab completion knows
// about. Both short and long forms of the same command (e.g. "q" and
// "quit") are listed individually, since either is something you might
// type and want completed.
var commandNames = []string{
	"w", "write", "wq", "q", "quit", "q!", "quit!",
	"undo", "redo", "agenda", "clarify", "outline", "config", "capture", "calendar",
	"delmarks", "delmarks!", "clear-registers", "noh", "nohlsearch", "toggledone", "next", "prev", "format-links", "log", "diff", "commit", "help",
	"sync-calendar", "sync-calendar!",
}

// completeCommand implements ":<prefix><Tab>": if the command word
// typed so far (no completion once an argument is being typed, i.e.
// past the first space) is a prefix of exactly one command name, the
// input is completed to it in full; if it's a prefix of several, the
// input is extended to their longest common prefix and the matches are
// listed after it so it's clear what to type next; if it matches none,
// a message says so.
func (m *Model) completeCommand() {
	if strings.Contains(m.commandInput, " ") {
		return
	}
	word := m.commandInput

	var matches []string
	for _, name := range commandNames {
		if strings.HasPrefix(name, word) {
			matches = append(matches, name)
		}
	}

	switch len(matches) {
	case 0:
		m.message = fmt.Sprintf("No command starting with %q", word)
	case 1:
		m.commandInput = matches[0]
	default:
		sort.Strings(matches)
		if common := commonPrefix(matches); len(common) > len(word) {
			m.commandInput = common
		}
		m.commandCompletions = strings.Join(matches, "  ")
	}
}

// commonPrefix returns the longest string that's a prefix of every
// element of strs. strs must be non-empty.
func commonPrefix(strs []string) string {
	prefix := strs[0]
	for _, s := range strs[1:] {
		for !strings.HasPrefix(s, prefix) {
			prefix = prefix[:len(prefix)-1]
		}
	}
	return prefix
}

func (m Model) runCommand() (tea.Model, tea.Cmd) {
	cmd := strings.TrimSpace(m.commandInput)
	m.mode = normalMode
	m.commandInput = ""

	if cmd != "" {
		// Recorded regardless of whether cmd turns out valid below —
		// same as vim's own cmdline history, which is exactly what
		// makes it useful for recalling and fixing a typo. Not
		// deduplicated, again matching vim, so repeating the same
		// command several times in a row leaves several entries.
		m.commandHistory = append(m.commandHistory, cmd)
	}
	m.commandHistoryPos = len(m.commandHistory)
	m.commandHistoryDraft = ""

	if cmd == "delmarks!" {
		m.marks = nil
		m.message = "All marks deleted"
		return m, nil
	}
	if letters, ok := strings.CutPrefix(cmd, "delmarks "); ok {
		m.deleteMarks(letters)
		return m, nil
	}

	switch cmd {
	case "":
		// Nothing typed; just dismiss the command line.

	case "w", "write":
		m.message = m.writeAll()

	case "wq":
		msg, ok := m.writeAllResult()
		m.message = msg
		if ok {
			return m, tea.Quit
		}

	case "q", "quit":
		if len(m.dirty) > 0 {
			m.message = "Unsaved changes — :w to save, or :q! to discard them"
			return m, nil
		}
		return m, tea.Quit

	case "q!", "quit!":
		return m, tea.Quit

	case "undo":
		m.undo()

	case "redo":
		m.redo()

	case "noh", "nohlsearch":
		m.lastSearchQuery = ""

	case "agenda":
		m.switchToView(agendaView)

	case "clarify":
		m.enterClarifyView()

	case "outline":
		m.switchToView(outlineView)

	case "config":
		m.switchToView(configView)

	case "calendar":
		m.switchToView(calendarView)

	case "capture":
		return m, m.startCapture()

	case "delmarks":
		m.message = "Usage: :delmarks <letters> or :delmarks!"

	case "clear-registers":
		m.register = nil
		m.message = "Register cleared"

	case "toggledone":
		m.toggleHideDone()

	case "next":
		if m.view != clarifyView {
			m.message = ":next only works in clarify view"
		} else {
			m.clarifyStep(1)
		}

	case "prev":
		if m.view != clarifyView {
			m.message = ":prev only works in clarify view"
		} else {
			m.clarifyStep(-1)
		}

	case "format-links":
		return m, m.startFormatLinks()

	case "sync-calendar":
		return m, m.startSyncCalendar(false)

	case "sync-calendar!":
		return m, m.startSyncCalendar(true)

	case "log":
		m.switchToView(logView)

	case "diff":
		m.showDiff()

	case "commit":
		m.startCommit()

	case "help":
		m.switchToView(helpView)

	default:
		m.message = fmt.Sprintf("Unknown command: %s", cmd)
	}
	return m, nil
}

// deleteMarks handles ":delmarks <letters>" (space-separated or run
// together, e.g. "a b" or "ab"), removing each and reporting any that
// weren't set.
func (m *Model) deleteMarks(arg string) {
	var removed, missing []rune
	for _, r := range arg {
		if r == ' ' {
			continue
		}
		if _, ok := m.marks[r]; ok {
			delete(m.marks, r)
			removed = append(removed, r)
		} else {
			missing = append(missing, r)
		}
	}
	switch {
	case len(removed) == 0 && len(missing) == 0:
		m.message = "Usage: :delmarks <letters> or :delmarks!"
	case len(missing) == 0:
		m.message = fmt.Sprintf("Deleted mark(s): %s", string(removed))
	case len(removed) == 0:
		m.message = fmt.Sprintf("No such mark(s): %s", string(missing))
	default:
		m.message = fmt.Sprintf("Deleted %s; no such mark(s): %s", string(removed), string(missing))
	}
}

// writeAll writes every file with unwritten in-memory changes to disk,
// returning a status-line summary.
func (m *Model) writeAll() string {
	msg, _ := m.writeAllResult()
	return msg
}

// writeAllResult is writeAll but also reports whether every write
// succeeded (false if any file failed), so callers like :wq can decide
// whether it's safe to proceed.
func (m *Model) writeAllResult() (string, bool) {
	var written, failed []string
	for _, f := range m.ws.Files {
		if !m.dirty[f] {
			continue
		}
		if err := org.WriteFile(f); err != nil {
			failed = append(failed, fmt.Sprintf("%s (%v)", filepath.Base(f.Path), err))
			continue
		}
		m.savedPos[f] = m.appliedCountForFile(f)
		written = append(written, filepath.Base(f.Path))
	}
	m.recomputeDirty()

	switch {
	case len(failed) > 0:
		return "Failed to write " + strings.Join(failed, ", "), false
	case len(written) == 0:
		return "No changes to write", true
	default:
		return "Wrote " + strings.Join(written, ", "), true
	}
}

// updateSelectMode handles key presses while the status picker (R) is
// open: j/k or arrows browse the (possibly filtered) candidate list,
// typing a letter narrows it — auto-applying as soon as exactly one
// candidate matches, which is immediate for any of the single-letter
// shortcuts — Enter applies the highlighted candidate, and Esc cancels.
func (m Model) updateSelectMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.mode = normalMode
		m.selectFilter = ""
		m.selectModeTargets = nil
		return m, nil

	case tea.KeyEnter:
		return m.applySelectedStatus()

	case tea.KeyBackspace:
		if r := []rune(m.selectFilter); len(r) > 0 {
			m.selectFilter = string(r[:len(r)-1])
		}
		m.selectIndex = 0
		return m, nil

	case tea.KeyUp:
		m.moveSelectHighlight(-1)
		return m, nil

	case tea.KeyDown:
		m.moveSelectHighlight(1)
		return m, nil

	case tea.KeyRunes:
		for _, r := range msg.Runes {
			m.typeSelectChar(r)
		}
		return m, nil
	}

	return m, nil
}

// typeSelectChar handles one typed rune in the status picker. j/k always
// move the highlight, matching the rest of the app's keybindings.
// Otherwise the rune is tried as a filter character: if it would leave
// zero matching candidates it's rejected outright (there's nothing
// useful to backspace back from), and if it leaves exactly one, that
// candidate is applied immediately.
func (m *Model) typeSelectChar(r rune) {
	switch r {
	case 'j':
		m.moveSelectHighlight(1)
		return
	case 'k':
		m.moveSelectHighlight(-1)
		return
	}

	candidateFilter := strings.ToLower(m.selectFilter + string(r))
	matches := filteredStatusCandidates(candidateFilter)
	if len(matches) == 0 {
		return
	}
	m.selectFilter = candidateFilter
	m.selectIndex = 0
	if len(matches) == 1 {
		m.applyChosenStatus(matches[0].keyword, matches[0].label)
		m.mode = normalMode
		m.selectFilter = ""
	}
}

func (m *Model) moveSelectHighlight(delta int) {
	n := len(filteredStatusCandidates(m.selectFilter))
	m.selectIndex += delta
	if m.selectIndex < 0 {
		m.selectIndex = 0
	}
	if m.selectIndex >= n {
		m.selectIndex = n - 1
	}
}

// applySelectedStatus applies the currently highlighted candidate and
// always returns to normal mode.
func (m Model) applySelectedStatus() (tea.Model, tea.Cmd) {
	matches := filteredStatusCandidates(m.selectFilter)
	m.mode = normalMode
	m.selectFilter = ""
	if len(matches) == 0 {
		return m, nil
	}
	idx := m.selectIndex
	if idx < 0 {
		idx = 0
	}
	if idx >= len(matches) {
		idx = len(matches) - 1
	}
	m.applyChosenStatus(matches[idx].keyword, matches[idx].label)
	return m, nil
}

// applyChosenStatus applies keyword (labeled label, for the bulk status
// message) to either selectModeTargets (set when the R picker now
// resolving was entered from visual mode or with a numeric prefix) or
// just the current headline otherwise — shared by applySelectedStatus
// (Enter) and typeSelectChar's single-match auto-apply.
func (m *Model) applyChosenStatus(keyword, label string) {
	if targets := m.selectModeTargets; len(targets) > 0 {
		m.selectModeTargets = nil
		m.applyStatusToHeadlineSet(targets, keyword, label)
		return
	}
	m.applyStatus(keyword)
}

// currentStatusIndex returns the statusCandidates index matching the
// current headline's keyword, used to pre-highlight it when R opens.
func (m *Model) currentStatusIndex() int {
	h := m.currentHeadline()
	if h == nil {
		return 0
	}
	for i, c := range statusCandidates {
		if c.keyword == h.Keyword {
			return i
		}
	}
	return 0
}

// startMeetingPicker ("gM") opens a fuzzy-filterable picker (see
// updateMeetingPickerMode) over every distinct recurring meeting series
// or one-off event :sync-calendar currently has synced at least one instance
// of (see meetingCandidates, in agenda.go), letting the user toggle the
// chosen meeting's ID on or off the current entry's
// GCAL_RECURRING_EVENT_IDS or GCAL_EVENT_IDS property (matching
// whichever kind the meeting is — see meetingCandidate.kind) — so it
// shows up under that meeting in the agenda's Meetings section (see
// appendMeetingsSection) next time it's due (a recurring series) or
// until it happens (a one-off). A no-op (with a status message) if the
// cursor isn't on a headline, the entry is locked by an in-flight
// :format-links batch, or :sync-calendar hasn't synced anything at all — in
// which case there's nothing to offer, and no point opening an empty
// picker.
func (m *Model) startMeetingPicker() {
	h := m.currentHeadline()
	if h == nil || m.refuseIfImmutable(h) {
		return
	}
	now := time.Now()
	candidates := m.meetingCandidates(now)
	if len(candidates) == 0 {
		m.message = "No calendar meetings synced yet (see :sync-calendar)"
		return
	}
	m.mode = meetingPickerMode
	m.meetingPickerTarget = h
	m.meetingPickerCandidates = candidates
	m.meetingPickerFilter = ""
	m.meetingPickerIndex = meetingPickerDefaultIndex(candidates, now)
}

// updateMeetingPickerMode handles key presses while the "gM" picker is
// open: typing narrows meetingPickerCandidates to those whose title
// contains what's been typed so far (see filteredMeetingCandidates), ↑/↓
// browse the (possibly filtered) result, Enter toggles the highlighted
// candidate on the target entry (see applySelectedMeeting), and Esc
// cancels. Unlike the status picker's typeSelectChar, "j"/"k" are not
// special-cased as navigation here — a meeting title is free text that
// can legitimately contain either letter, so only the arrow keys move
// the highlight while typing.
func (m Model) updateMeetingPickerMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.mode = normalMode
		m.meetingPickerTarget = nil
		m.meetingPickerCandidates = nil
		m.meetingPickerFilter = ""
		return m, nil

	case tea.KeyEnter:
		return m.applySelectedMeeting()

	case tea.KeyBackspace:
		if r := []rune(m.meetingPickerFilter); len(r) > 0 {
			m.meetingPickerFilter = string(r[:len(r)-1])
		}
		m.meetingPickerIndex = 0
		return m, nil

	case tea.KeyUp:
		m.moveMeetingHighlight(-1)
		return m, nil

	case tea.KeyDown:
		m.moveMeetingHighlight(1)
		return m, nil

	case tea.KeyRunes:
		m.meetingPickerFilter += string(msg.Runes)
		m.meetingPickerIndex = 0
		return m, nil
	}
	return m, nil
}

func (m *Model) moveMeetingHighlight(delta int) {
	n := len(filteredMeetingCandidates(m.meetingPickerCandidates, m.meetingPickerFilter))
	m.meetingPickerIndex += delta
	if m.meetingPickerIndex < 0 {
		m.meetingPickerIndex = 0
	}
	if n > 0 && m.meetingPickerIndex >= n {
		m.meetingPickerIndex = n - 1
	}
}

// applySelectedMeeting toggles the currently highlighted candidate (see
// filteredMeetingCandidates/meetingPickerIndex) on meetingPickerTarget
// and always returns to normal mode. A no-op, other than closing the
// picker, if nothing matches the typed filter.
func (m Model) applySelectedMeeting() (tea.Model, tea.Cmd) {
	matches := filteredMeetingCandidates(m.meetingPickerCandidates, m.meetingPickerFilter)
	target := m.meetingPickerTarget
	m.mode = normalMode
	m.meetingPickerTarget = nil
	m.meetingPickerCandidates = nil
	m.meetingPickerFilter = ""
	if len(matches) == 0 || target == nil {
		return m, nil
	}
	idx := m.meetingPickerIndex
	if idx < 0 {
		idx = 0
	}
	if idx >= len(matches) {
		idx = len(matches) - 1
	}
	m.pushUndo(m.buildMeetingAttachAction(target, matches[idx]))
	return m, nil
}

// buildMeetingAttachAction builds the undoAction toggling c on or off
// h's ids/links property pair — GCAL_RECURRING_EVENT_IDS/
// GCAL_RECURRING_EVENT_LINKS for a recurring series, GCAL_EVENT_IDS/
// GCAL_EVENT_LINKS for a one-off event, per c.kind (adding both if c
// isn't yet attached, removing both if it is — see meetingIsAttached),
// without applying or pushing it yet (see pushUndo). The two properties
// are kept index-aligned: attaching appends c's ID and (if c has a
// link) its "[[url][title]]" link to the end of each; detaching removes
// whichever ID matched, and the link at that same index, if one exists
// there (gracefully doing nothing to the links list if the two have
// drifted out of alignment — e.g. a link-less candidate was attached,
// or either property was hand-edited — rather than risk removing the
// wrong entry).
func (m *Model) buildMeetingAttachAction(h *org.Headline, c meetingCandidate) undoAction {
	idsProp, linksProp := c.kind.idsProperty(), c.kind.linksProperty()
	oldIDsRaw, hadIDs := h.Properties[idsProp]
	oldLinksRaw, hadLinks := h.Properties[linksProp]

	ids := strings.Fields(oldIDsRaw)
	links := parseOrgLinks(oldLinksRaw)

	if idx := indexOfString(ids, c.id); idx >= 0 {
		ids = append(ids[:idx], ids[idx+1:]...)
		if idx < len(links) {
			links = append(links[:idx], links[idx+1:]...)
		}
	} else {
		ids = append(ids, c.id)
		if c.link != "" {
			links = append(links, orgLink{url: c.link, description: c.title})
		}
	}

	return &meetingAttachAction{
		h:                h,
		f:                m.fileForHeadline(h),
		idsProp:          idsProp,
		linksProp:        linksProp,
		hadIDsProperty:   hadIDs,
		oldIDs:           oldIDsRaw,
		newIDs:           strings.Join(ids, " "),
		hadLinksProperty: hadLinks,
		oldLinks:         oldLinksRaw,
		newLinks:         joinOrgLinks(links),
	}
}

// joinOrgLinks is the inverse of parseOrgLinks: renders links back into
// a GCAL_RECURRING_EVENT_LINKS- or GCAL_EVENT_LINKS-shaped property
// value.
func joinOrgLinks(links []orgLink) string {
	parts := make([]string, len(links))
	for i, l := range links {
		parts[i] = "[[" + l.url + "][" + l.description + "]]"
	}
	return strings.Join(parts, " ")
}

func indexOfString(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return -1
}

// dateInputLayouts are the formats accepted when typing a date, tried in
// order. A weekday name may or may not be present (it's not required,
// and is regenerated from the actual date on output regardless of what
// was typed, so a stale one left over from editing an existing date
// doesn't matter).
var dateInputLayouts = []string{"2006-01-02 Mon 15:04", "2006-01-02 Mon", "2006-01-02 15:04", "2006-01-02"}

// parseFlexibleDate tries each of dateInputLayouts against input,
// reporting whether the matched layout included a time of day.
func parseFlexibleDate(input string) (t time.Time, hasTime bool, err error) {
	for _, layout := range dateInputLayouts {
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

// truncateToDate drops t's time-of-day component.
func truncateToDate(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// resolveDeadlineDate parses input as, in order: an exact date (with
// optional time of day, per parseFlexibleDate); a compact or
// spelled-out relative offset ("3d", "2 weeks", "-1y"); or a fuzzy
// natural-language phrase ("next tuesday", "tomorrow", "sep 30", "thu"),
// via fuzzyDate/when.EN. Only the first form can produce a time of day —
// the other two always resolve to a plain date, since "in 3 days" or
// "sep 30" don't imply a specific hour.
func resolveDeadlineDate(input string) (t time.Time, hasTime bool, err error) {
	input = strings.TrimSpace(input)

	if t, hasTime, err := parseFlexibleDate(input); err == nil {
		return t, hasTime, nil
	}

	today := truncateToDate(time.Now())

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
	t := truncateToDate(r.Time)
	if t.Before(today) && !explicitYearRe.MatchString(input) {
		t = t.AddDate(1, 0, 0)
	}
	return t, true
}

// parseDeadlineInput parses a typed date into an active org timestamp
// suitable for DEADLINE.
func parseDeadlineInput(input string) (*org.Timestamp, error) {
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

// prefillDateInput renders ts without its weekday, as a starting point
// for editing (empty if ts is nil).
func prefillDateInput(ts *org.Timestamp) string {
	if ts == nil {
		return ""
	}
	t, hasTime, err := parseFlexibleDate(ts.Raw)
	if err != nil {
		return ts.Raw
	}
	if hasTime {
		return t.Format("2006-01-02 15:04")
	}
	return t.Format("2006-01-02")
}

// startSetDeadline opens the deadline-entry prompt ("gd") for the
// current headline, pre-filled with its existing deadline if any. A
// no-op on file rows.
func (m *Model) startSetDeadline() {
	h := m.currentHeadline()
	if h == nil || m.refuseIfImmutable(h) {
		return
	}
	m.mode = deadlineMode
	m.deadlineInput = prefillDateInput(h.Deadline)
}

// updateDeadlineMode handles key presses while the deadline prompt is
// open: Enter applies the typed date (or clears the deadline if left
// empty), Esc cancels without changes. Every key but Enter also clears
// any error message left over from a previous failed attempt (Enter
// itself goes through applyDeadlineInput, which clears it before
// deciding whether to set a new one) — otherwise a stale error would
// keep showing next to input the user has already started correcting.
func (m Model) updateDeadlineMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type != tea.KeyEnter {
		m.message = ""
	}

	switch msg.Type {
	case tea.KeyEsc:
		m.mode = normalMode
		m.deadlineInput = ""
		return m, nil

	case tea.KeyEnter:
		return m.applyDeadlineInput()

	case tea.KeyBackspace:
		if r := []rune(m.deadlineInput); len(r) > 0 {
			m.deadlineInput = string(r[:len(r)-1])
		}
		return m, nil

	case tea.KeySpace:
		m.deadlineInput += " "
		return m, nil

	case tea.KeyRunes:
		m.deadlineInput += string(msg.Runes)
		return m, nil
	}
	return m, nil
}

// applyDeadlineInput parses the typed date and, if valid, records a
// deadlineChangeAction. An empty input clears the deadline. An invalid
// (non-empty) date is reported on the status line and leaves the prompt
// open, input intact, so it can be corrected. m.message is cleared
// unconditionally up front — every path below either leaves it cleared
// (success, or clearing the deadline) or sets a fresh one (failure), so
// a previous attempt's error never lingers once this one is resolved.
func (m Model) applyDeadlineInput() (tea.Model, tea.Cmd) {
	m.message = ""
	input := strings.TrimSpace(m.deadlineInput)
	h := m.currentHeadline()
	if h == nil {
		m.mode = normalMode
		m.deadlineInput = ""
		return m, nil
	}

	if input == "" {
		m.mode = normalMode
		m.deadlineInput = ""
		if h.Deadline != nil {
			m.pushUndo(&deadlineChangeAction{h: h, f: m.fileForHeadline(h), oldDeadline: h.Deadline, newDeadline: nil})
		}
		return m, nil
	}

	ts, err := parseDeadlineInput(input)
	if err != nil {
		m.message = err.Error()
		return m, nil
	}

	m.mode = normalMode
	m.deadlineInput = ""
	m.pushUndo(&deadlineChangeAction{h: h, f: m.fileForHeadline(h), oldDeadline: h.Deadline, newDeadline: ts})
	return m, nil
}

// startTagPrompt opens the tag-entry prompt ("gt") for the current
// headline. A no-op on file rows or a locked (:format-links) entry.
func (m *Model) startTagPrompt() {
	h := m.currentHeadline()
	if h == nil || m.refuseIfImmutable(h) {
		return
	}
	m.mode = tagMode
	m.tagTarget = h
	m.tagInput = ""
	m.tagCompletions = ""
}

// isTagRune reports whether r is a character org-mode allows inside a
// tag (see tagsRe in internal/org) — letters, digits, and "_@%#+".
// Anything else typed at the tag prompt (spaces in particular — a tag
// can never contain one) is silently dropped rather than accepted and
// later rejected, since there's no valid tag it could ever become part
// of.
func isTagRune(r rune) bool {
	if unicode.IsLetter(r) || unicode.IsDigit(r) {
		return true
	}
	switch r {
	case '_', '@', '%', '#', '+':
		return true
	}
	return false
}

// workspaceTags returns every distinct tag used anywhere in the
// workspace, sorted, for the "gt" prompt's Tab-completion (see
// completeTagInput).
func (m *Model) workspaceTags() []string {
	seen := make(map[string]bool)
	for _, f := range m.ws.Files {
		org.Walk(f.Headlines, func(h *org.Headline) {
			for _, t := range h.Tags {
				seen[t] = true
			}
		})
	}
	tags := make([]string, 0, len(seen))
	for t := range seen {
		tags = append(tags, t)
	}
	sort.Strings(tags)
	return tags
}

// updateTagMode handles key presses while the "gt" prompt is open: Tab
// completes against every existing tag in the workspace (same
// prefix/longest-common-prefix behavior as command-mode's
// completeCommand), Enter toggles the typed tag on the target entry
// (see applyTagInput), and Esc cancels.
func (m Model) updateTagMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type != tea.KeyTab {
		// Same reasoning as updateCommandMode: a shown completion list (or
		// a "no match" message it left) is a one-shot hint for the
		// keystroke right after Tab, not something that should linger.
		m.tagCompletions = ""
	}
	if msg.Type != tea.KeyEnter {
		m.message = ""
	}

	switch msg.Type {
	case tea.KeyEsc:
		m.mode = normalMode
		m.tagTarget = nil
		m.tagInput = ""
		m.tagCompletions = ""
		return m, nil

	case tea.KeyEnter:
		return m.applyTagInput()

	case tea.KeyBackspace:
		if r := []rune(m.tagInput); len(r) > 0 {
			m.tagInput = string(r[:len(r)-1])
		}
		return m, nil

	case tea.KeyRunes:
		for _, r := range msg.Runes {
			if isTagRune(r) {
				m.tagInput += string(r)
			}
		}
		return m, nil

	case tea.KeyTab:
		m.completeTagInput()
		return m, nil
	}
	return m, nil
}

// completeTagInput implements the "gt" prompt's Tab-completion: if the
// text typed so far is a prefix of exactly one existing tag, the input
// is completed to it in full; if it's a prefix of several, the input is
// extended to their longest common prefix and the matches are listed
// after it (see tagCompletions); if it matches none, a message says so.
// An empty prompt lists every existing tag, so what's available to
// complete toward is visible even before typing a character of it.
func (m *Model) completeTagInput() {
	word := m.tagInput
	all := m.workspaceTags()

	if word == "" {
		if len(all) == 0 {
			m.message = "No existing tags"
			return
		}
		m.tagCompletions = strings.Join(all, "  ")
		return
	}

	var matches []string
	for _, tag := range all {
		if strings.HasPrefix(tag, word) {
			matches = append(matches, tag)
		}
	}

	switch len(matches) {
	case 0:
		m.message = fmt.Sprintf("No existing tag starting with %q", word)
	case 1:
		m.tagInput = matches[0]
	default:
		if common := commonPrefix(matches); len(common) > len(word) {
			m.tagInput = common
		}
		m.tagCompletions = strings.Join(matches, "  ")
	}
}

// applyTagInput toggles the typed tag (trimmed) on tagTarget: appended
// if not already present, removed if it is — so pressing "gt" and
// retyping the same tag is how a tag comes back off, matching the
// keybinding table's own "adding a tag again removes it". A blank
// input (Enter with nothing typed) cancels without changes, same as
// Esc, rather than clearing every tag — there's no way to distinguish
// "clear all tags" from "typed nothing" otherwise, and gd's own
// empty-clears convention doesn't apply here since a headline can carry
// more than one tag. Always returns to normal mode.
func (m Model) applyTagInput() (tea.Model, tea.Cmd) {
	m.message = ""
	tag := strings.TrimSpace(m.tagInput)
	target := m.tagTarget
	m.mode = normalMode
	m.tagTarget = nil
	m.tagInput = ""
	m.tagCompletions = ""
	if tag == "" || target == nil {
		return m, nil
	}

	old := append([]string(nil), target.Tags...)
	var newTags []string
	removed := false
	for _, t := range target.Tags {
		if t == tag {
			removed = true
			continue
		}
		newTags = append(newTags, t)
	}
	if !removed {
		newTags = append(newTags, tag)
	}
	m.pushUndo(&tagChangeAction{h: target, f: m.fileForHeadline(target), oldTags: old, newTags: newTags})
	return m, nil
}

// applyStatus sets the current headline's keyword, stamping or clearing
// CLOSED to match org-mode's convention of recording when an item
// entered (or left) a DONE-class state — unless h is completing
// (transitioning from a non-done to a done-class keyword) and has a
// repeating SCHEDULED/DEADLINE, in which case repeatAdvanceForCompletion
// takes over instead: per org-mode, the keyword never actually changes
// and the repeating timestamp(s) advance rather than the item closing.
// See buildStatusChangeAction, which does the actual work (shared with
// visual-mode R's bulk apply). In clarify view, this also advances past
// the pinned target if it just became DONE/CANCELLED (see
// advanceClarifyTargetIfDone).
func (m *Model) applyStatus(keyword string) {
	h := m.currentHeadline()
	if h == nil || m.refuseIfImmutable(h) {
		return
	}
	m.pushUndo(m.buildStatusChangeAction(h, keyword))
	m.advanceClarifyTargetIfDone()
}

// fileForHeadline returns the file h (or one of its ancestors) belongs
// to, or nil if it can't be found (shouldn't happen for any headline
// reachable from the workspace).
func (m *Model) fileForHeadline(h *org.Headline) *org.File {
	root := h
	for root.Parent != nil {
		root = root.Parent
	}
	for _, f := range m.ws.Files {
		for _, top := range f.Headlines {
			if top == root {
				return f
			}
		}
	}
	return nil
}

// editFinishedMsg reports that the external editor launched by
// launchEditor has exited. insert is non-nil when this edit session
// originated from o/O (target is then a tentative placeholder headline,
// not yet part of undo history) rather than an `i` edit of existing
// content.
type editFinishedMsg struct {
	path   string
	target *org.Headline
	insert *insertContext
	cmd    *exec.Cmd // the editor process, for logCompletedProcess (its Process field is only populated once tea.ExecProcess has actually started it)
	err    error
}

// startEdit ("i") writes the current headline (and its entire subtree)
// to a temp file and opens it in $EDITOR for editing in place — for a
// vim-family editor, with the cursor already placed right after the
// bullet ("* ") and insert mode already started, so typing begins
// immediately without a manual "i" or cursor motion in the editor
// itself. See startEditAppend for "A", and startEditWithPlacement for
// the shared mechanics.
func (m *Model) startEdit() tea.Cmd {
	return m.startEditWithPlacement(cursorAtEntryStart)
}

// startEditAppend ("A") is startEdit, but positions the cursor at the
// end of the entry's first line instead of right after the bullet —
// vim's own "A" (append at end of line), once the editor's open.
func (m *Model) startEditAppend() tea.Cmd {
	return m.startEditWithPlacement(cursorAtLineEnd)
}

// startEditWithPlacement is the shared implementation behind startEdit
// ("i") and startEditAppend ("A") — writes the current headline (and its
// entire subtree) to a temp file and opens it in $EDITOR, positioning
// the cursor per placement. Returns nil if there's nothing to edit or
// the editor couldn't be launched, in which case any error is left in
// m.message.
func (m *Model) startEditWithPlacement(placement editorCursorPlacement) tea.Cmd {
	if m.cursor >= 0 && m.cursor < len(m.rows) {
		if f := m.rows[m.cursor].file; f != nil {
			// A whole-file edit hands the raw file to $EDITOR directly,
			// bypassing every other guard here — if any headline in it is
			// locked by :format-links, refuse outright rather than risk
			// the user rewriting (or deleting) it in a way finishEditFile
			// has no way to detect or prevent.
			if m.fileHasImmutableHeadline(f) {
				m.message = "This file has entries being formatted by :format-links; can't edit the whole file yet"
				return nil
			}
			// Editing a whole file discards undo history for it (and
			// clears any mark/clarify-target on its headlines) even if
			// the user ends up changing nothing — confirm first rather
			// than doing that as a side effect of a single keystroke.
			// Both i and A land here identically: there's no single
			// entry to position a cursor within.
			m.mode = confirmMode
			m.pendingFileEdit = f
			m.confirmMessage = fmt.Sprintf("Edit %s in $EDITOR? This clears undo history and marks for this file. [y/N]", filepath.Base(f.Path))
			return nil
		}
	}
	h := m.currentHeadline()
	if h == nil || m.refuseIfImmutable(h) {
		return nil
	}
	return m.launchEditor(h, nil, placement)
}

// fileHasImmutableHeadline reports whether any headline in f is
// currently locked by an in-flight :format-links batch (see
// m.immutable) — used to refuse a whole-file edit, which would
// otherwise let the user rewrite an entry's raw text out from under the
// batch with no way for finishFormatLinks to detect it.
func (m *Model) fileHasImmutableHeadline(f *org.File) bool {
	found := false
	org.Walk(f.Headlines, func(h *org.Headline) {
		if m.immutable[h] {
			found = true
		}
	})
	return found
}

// fileEditFinishedMsg reports that the external editor launched by
// startEditFile has exited, for a whole-file edit ("i" on a file row).
type fileEditFinishedMsg struct {
	target *org.File // the file being edited, identified by its old pointer
	cmd    *exec.Cmd // the editor process, for logCompletedProcess (its Process field is only populated once tea.ExecProcess has actually started it)
	err    error
}

// startEditFile ("i" on a file row) opens that file directly in
// $EDITOR — the real file on disk, not a temp copy, since there's no
// synthetic context wrapper needed for editing a whole file the way
// there is for a single entry (see launchEditor). On exit, the file is
// simply reloaded from disk (see finishEditFile). There's no undo for
// this — the editor already wrote the change directly to disk, so
// there's no in-memory action to record or revert.
func (m *Model) startEditFile(f *org.File) tea.Cmd {
	editorCmd := buildEditorCommand(m.editorCommand(), f.Path, "", noCursorPlacement, 0)
	return tea.ExecProcess(editorCmd, func(err error) tea.Msg {
		return fileEditFinishedMsg{target: f, cmd: editorCmd, err: err}
	})
}

// finishEditFile reloads the just-edited file from disk, replacing its
// old *org.File wholesale — every headline pointer it held is gone, so
// any mark, clarify-target, or dirty-marker referencing one of them is
// cleared (see clearRefsForFile) rather than left dangling. The
// reloaded file itself is never marked dirty: the editor already wrote
// it, so there's nothing more to save.
func (m Model) finishEditFile(msg fileEditFinishedMsg) (tea.Model, tea.Cmd) {
	logCompletedProcess(m.execLog, msg.cmd, msg.err)
	if msg.err != nil {
		m.message = fmt.Sprintf("Editor exited with an error: %v", msg.err)
		return m, nil
	}

	newFile, err := org.ParseFile(msg.target.Path)
	if err != nil {
		m.message = fmt.Sprintf("Could not reload %s: %v", filepath.Base(msg.target.Path), err)
		return m, nil
	}

	for i, f := range m.ws.Files {
		if f == msg.target {
			m.ws.Files[i] = newFile
			break
		}
	}
	m.clearRefsForFile(msg.target)
	m.rebuildRows()
	m.focusFile(newFile)
	return m, nil
}

// clearRefsForFile removes every mark, clarify-target reference, and
// dirty marker pointing at a headline in f, plus f's own file-level
// dirty/saved-position bookkeeping — called after f's entire headline
// tree has been discarded and replaced (a whole-file reload), since
// none of those old headline pointers exist anywhere anymore.
func (m *Model) clearRefsForFile(f *org.File) {
	org.Walk(f.Headlines, func(h *org.Headline) {
		if m.clarifyTarget == h {
			m.clarifyTarget = nil
		}
		m.clearMarksFor(h)
		delete(m.dirtyHeadlines, h)
	})
	delete(m.dirty, f)
	delete(m.savedPos, f)
}

// editorsWithLineArg lists $EDITOR basenames known to support a leading
// "+N" argument that opens the file with the cursor on line N — a
// convention shared by vi/vim, emacs, and nano. launchEditor uses this
// to land the cursor on the real content rather than line 1, which is
// now the context trailer's file-name comment. Applied only to these
// editors, since an arbitrary editor could easily misread "+N" as a
// literal filename instead of a line number.
var editorsWithLineArg = map[string]bool{
	"vi": true, "vim": true, "nvim": true, "gvim": true, "mvim": true,
	"emacs": true, "emacsclient": true,
	"nano": true,
}

// vimFamily is the subset of editorsWithLineArg that additionally
// understands vim's ex-command syntax — a "+{command}" argument
// executing an arbitrary command, not just a bare line number — used to
// start "i"/"A" directly in insert mode at a specific spot (see
// cursorPlacementArg). emacs/emacsclient and nano share the "+N" line
// convention but have no equivalent notion of "insert mode" to start
// (nano isn't modal; plain emacs isn't either), so they always just get
// a bare "+N" regardless of placement.
var vimFamily = map[string]bool{
	"vi": true, "vim": true, "nvim": true, "gvim": true, "mvim": true,
}

// editorCursorPlacement selects where launchEditor positions the cursor,
// and whether it starts the editor directly in insert mode, when the
// configured editor is vim-family (see vimFamily) — a plain "+N" line
// jump for anything else, or for noCursorPlacement.
type editorCursorPlacement int

const (
	// noCursorPlacement just lands on the entry's first line, same as
	// always — used only for a whole-file edit (startEditFile), where
	// there's no single entry to position a cursor within.
	noCursorPlacement editorCursorPlacement = iota
	// cursorAtEntryStart ("i", "o"/"O") puts the cursor at the very start
	// of the entry's own text — column 1, since the buffer never shows a
	// bullet to land after (see dedentEntry, launchEditor, and
	// resolveEntryCursorPlacement) — in insert mode, so typing
	// immediately inserts text there exactly as pressing vim's own "i" at
	// that spot would. For o/O the entry is a blank template, so this is
	// also where its title will end up starting.
	cursorAtEntryStart
	// cursorAtLineEnd ("A") puts the cursor at the end of the entry's
	// first line, in insert mode — vim's own "A" (append at end of
	// line), landing on whichever text (keyword, title, tags) the line
	// actually ends with.
	cursorAtLineEnd
)

// cursorPlacementArg returns the "+..." argument buildEditorCommand
// should pass for the given editor basename, startLine (1-based), col
// (1-based, meaningful only for cursorAtEntryStart), and placement.
// Non-vim-family editors (or noCursorPlacement) always get a bare
// "+startLine" — see editorCursorPlacement and vimFamily.
func cursorPlacementArg(editorBase string, startLine, col int, placement editorCursorPlacement) string {
	if vimFamily[editorBase] {
		switch placement {
		case cursorAtEntryStart:
			return fmt.Sprintf("+call cursor(%d,%d)|startinsert", startLine, col)
		case cursorAtLineEnd:
			// startinsert! is vim's own "A": moves to the end of the
			// current line before entering insert mode, so there's no
			// need to compute or pass a column at all.
			return fmt.Sprintf("+%d|startinsert!", startLine)
		}
	}
	return fmt.Sprintf("+%d", startLine)
}

// resolveEntryCursorPlacement returns the placement and (1-based) column
// launchEditor should actually request for a dedented entry buffer (see
// dedentEntry): cursorAtEntryStart always targets column 1, since
// there's no bullet to land after — for either an existing
// entry ("i"/"A") or a blank o/O template alike. It downgrades to
// cursorAtLineEnd when the first line is completely empty (a
// just-started o/O insert, or an existing entry with a blank title):
// vim's cursor()+startinsert needs the target column to be an existing
// character — cursor() clamps to the line's last real character rather
// than allowing a column one past the end — so requesting column 1 on a
// zero-length line would fail. cursorAtLineEnd's startinsert! (append)
// sidesteps this entirely: on an empty line, "end of line" and "column
// 1" are the exact same position anyway.
func resolveEntryCursorPlacement(entry string, placement editorCursorPlacement) (editorCursorPlacement, int) {
	if placement == cursorAtEntryStart {
		firstLine, _, _ := strings.Cut(entry, "\n")
		if len(firstLine) == 0 {
			placement = cursorAtLineEnd
		}
	}
	return placement, 1
}

// editorCommand returns the external editor to launch: m.editorOverride
// (see WithEditor) if set, else $EDITOR — matching DESIGN.md's config
// file semantics ("editor... falls back to $EDITOR if unset").
func (m Model) editorCommand() string {
	if m.editorOverride != "" {
		return m.editorOverride
	}
	return os.Getenv("EDITOR")
}

// splitCommandFields splits s into command-line-style fields the way a
// shell would for simple cases: runs of non-whitespace are one field
// each, except that a single- or double-quoted span is treated as part
// of the enclosing field (quotes stripped) and may itself contain
// whitespace — e.g. `myformatter --template "a template" x` splits into
// ["myformatter", "--template", "a template", "x"]. This is a minimal
// word-splitter, not a shell parser: no escape sequences, no variable
// expansion, no nesting — just enough for a configured command
// (urlFormatterCmd, $EDITOR) to carry an argument containing spaces,
// which plain strings.Fields cannot do (it would tear "a template"
// apart into two fields, quote characters and all). An unterminated
// quote isn't an error — whatever was captured is still emitted as a
// field, rather than the whole config value being discarded.
func splitCommandFields(s string) []string {
	var fields []string
	var cur strings.Builder
	inField := false
	var quote rune

	flush := func() {
		if inField {
			fields = append(fields, cur.String())
			cur.Reset()
			inField = false
		}
	}

	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
			inField = true
		case unicode.IsSpace(r):
			flush()
		default:
			cur.WriteRune(r)
			inField = true
		}
	}
	flush()
	return fields
}

// expandHomeField expands a leading "~" or "~/..." in field to the
// user's home directory, the way an interactive shell expands a tilde
// word during its own word-splitting. This matters specifically for
// editor/urlFormatterCmd: typed on the command line, a value like
// "~/bin/myformatter" is already expanded by the shell before orgtd
// ever sees argv, but the identical value read from the config file
// reaches us as a raw, unexpanded string — no shell is involved there —
// so it would otherwise be handed to exec.Command completely literally
// and fail to launch (silently, from the caller's perspective, since a
// failed exec just leaves the input unchanged). Applied per-field
// (after splitCommandFields), not to the whole command string, so a
// tilde in a later argument is expanded too, not just a leading one.
func expandHomeField(field string) string {
	if field != "~" && !strings.HasPrefix(field, "~/") {
		return field
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return field
	}
	if field == "~" {
		return home
	}
	return filepath.Join(home, field[2:])
}

// buildEditorCommand builds the *exec.Cmd for opening path in the editor
// named by editorEnv ($EDITOR's value; "vim" if empty), splitting off
// any extra words as leading arguments (e.g. "code --wait"). For an
// editor in editorsWithLineArg, it also inserts a "+..." argument (see
// cursorPlacementArg) so the editor opens with the cursor on the real
// content — or, for a vim-family editor with placement other than
// noCursorPlacement, at a specific column within it and already in
// insert mode (before is the context text written ahead of it in the
// file; its newline count is exactly the 1-based line the real content
// starts on). col is the 1-based column cursorAtEntryStart should land
// on; unused otherwise.
func buildEditorCommand(editorEnv, path, before string, placement editorCursorPlacement, col int) *exec.Cmd {
	fields := splitCommandFields(editorEnv)
	if len(fields) == 0 {
		fields = []string{"vim"}
	}
	for i, f := range fields {
		fields[i] = expandHomeField(f)
	}
	args := append([]string{}, fields[1:]...)
	base := filepath.Base(fields[0])
	if editorsWithLineArg[base] {
		startLine := strings.Count(before, "\n") + 1
		args = append(args, cursorPlacementArg(base, startLine, col, placement))
	}
	args = append(args, path)
	return exec.Command(fields[0], args...)
}

// launchEditor writes h to a temp file and opens it in $EDITOR (vim by
// default), suspending the TUI for the duration. ctx tags the resulting
// editFinishedMsg so finishEdit knows whether this is an o/O insert
// session or a plain `i`/`A` edit, and also which buffer format to
// expect back: both an o/O (or "gC"/"gX" capture) insert session and a
// plain `i`/`A` edit of an existing entry now share the same shape — the
// entry's own text (title, minus its bullet, plus its planning line,
// properties, and body, the whole thing dedented by one level — see
// dedentEntry and org.RenderEntry) comes first in the buffer, cursor
// already there, followed by a blank line and a git-commit-style comment
// trailer sketching the entry's place in the outline below it (see
// editEntryContext). ctx tags the resulting editFinishedMsg so finishEdit
// knows whether this is an insert session or a plain edit, and is also
// what selects the trailer's wording ("Inserting a new entry." vs
// "Editing this entry.").
// placement (see editorCursorPlacement) controls where a vim-family
// editor lands the cursor and whether it starts in insert mode already.
// Returns nil if the temp file couldn't be created or the editor
// couldn't be started, in which case the error is left in m.message.
func (m *Model) launchEditor(h *org.Headline, ctx *insertContext, placement editorCursorPlacement) tea.Cmd {
	tmp, err := os.CreateTemp("", "orgtd-edit-*.org")
	if err != nil {
		m.message = fmt.Sprintf("Could not create temp file: %v", err)
		return nil
	}
	path := tmp.Name()

	entry := dedentEntry(org.RenderEntry(h), h.Level)
	if !strings.HasSuffix(entry, "\n") {
		entry += "\n"
	}
	content := entry + "\n" + m.editEntryContext(h, ctx != nil)
	_, err = tmp.WriteString(content)
	tmp.Close()
	if err != nil {
		os.Remove(path)
		m.message = fmt.Sprintf("Could not write temp file: %v", err)
		return nil
	}

	placement, col := resolveEntryCursorPlacement(entry, placement)
	editorCmd := buildEditorCommand(m.editorCommand(), path, "", placement, col)

	return tea.ExecProcess(editorCmd, func(err error) tea.Msg {
		return editFinishedMsg{path: path, target: h, insert: ctx, cmd: editorCmd, err: err}
	})
}

// dedentEntry removes one level's worth of indentation — level+1
// characters, the fixed width writeHeadlineFields always uses, both for
// the bullet ("<stars> ") on the first line and for the span of spaces
// indenting every other line (planning, properties, body) so they align
// under it — from every line of s (see org.RenderEntry). Used to build
// the buffer an entry is edited or inserted in ("i"/"A", o/O, capture):
// with the bullet gone, leaving the rest of the entry indented under
// where it used to be would look disjointed, so the whole entry is
// dedented together. Blank lines are left alone. indentEntry reverses
// this once the edit comes back.
func dedentEntry(s string, level int) string {
	width := level + 1
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if i == 0 {
			// The bullet is "<stars> ", not spaces, but is always exactly
			// width bytes wide regardless of what follows.
			if len(line) < width {
				continue
			}
			lines[i] = line[width:]
			continue
		}
		n := 0
		for n < width && n < len(line) && line[n] == ' ' {
			n++
		}
		lines[i] = line[n:]
	}
	return strings.Join(lines, "\n")
}

// indentEntry reverses dedentEntry once the edit comes back: restores
// the bullet onto s's first line, and the matching span of indentation
// onto every other non-blank line, so the whole entry re-parses as a
// real headline at its original level with its planning/properties/body
// lines indented the way writeHeadlineFields expects. Blank lines are
// left alone, matching how dedentEntry treats them.
func indentEntry(s string, level int) string {
	indent := strings.Repeat(" ", level+1)
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if i == 0 {
			lines[i] = strings.Repeat("*", level) + " " + line
			continue
		}
		if line == "" {
			continue
		}
		lines[i] = indent + line
	}
	return strings.Join(lines, "\n")
}

// editEntryContext builds the git-commit-style comment trailer for both
// editing an existing entry ("i"/"A") and inserting a new one (o/O,
// "gC"/"gX" capture): the entry's own editable text (see launchEditor
// and org.RenderEntry) comes first in the buffer, followed by a blank
// line and this whole trailer below it, sketching the whole outline the
// entry sits in — its file, parent, siblings, and (for an existing
// entry) its own children, each rendered with real org stars matching
// its own level, as a little sub-tree — with a "[THIS ENTRY HERE]"
// marker standing in for the entry itself, since its actual text is
// already sitting above, editable. A brand-new o/O/capture entry never
// has children yet, so that part of the sub-tree is simply empty; a
// dedicated org.Walk special case for it isn't needed. The children
// (when there are any) are shown for orientation only — they aren't
// part of what this buffer edits; see finishEdit. isInsert selects the
// trailer's instructional wording. Every line is an org comment
// ("# ..."), so it's inert whether the user deletes it or leaves it in
// place.
func (m *Model) editEntryContext(h *org.Headline, isInsert bool) string {
	fileName := ""
	if f := m.fileForHeadline(h); f != nil {
		fileName = filepath.Base(f.Path)
	}
	prev, next, earlierCount := m.siblingHeadlines(h)

	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n#\n", fileName)
	if h.Parent != nil {
		fmt.Fprintf(&b, "# %s\n", commentedHeadlineLine(h.Parent))
	}
	if earlierCount > 0 {
		noun := "sibling"
		if earlierCount != 1 {
			noun = "siblings"
		}
		fmt.Fprintf(&b, "#%s... %d earlier %s ...\n", strings.Repeat(" ", h.Level), earlierCount, noun)
	}
	if prev != nil {
		fmt.Fprintf(&b, "# %s\n", commentedHeadlineLine(prev))
	}
	fmt.Fprintf(&b, "# %s [THIS ENTRY HERE]\n", strings.Repeat("*", h.Level))
	org.Walk(h.Children, func(c *org.Headline) {
		fmt.Fprintf(&b, "# %s\n", commentedHeadlineLine(c))
	})
	if next != nil {
		fmt.Fprintf(&b, "# %s\n", commentedHeadlineLine(next))
	}
	action := "Editing this entry."
	if isInsert {
		action = "Inserting a new entry."
	}
	fmt.Fprintf(&b, "#\n# %s Lines starting with '#' are ignored. Save and\n", action)
	fmt.Fprintln(&b, "# exit to apply your changes, or delete the entry's content (leaving")
	fmt.Fprintln(&b, "# only these comments, or nothing) to cancel.")
	if m.urlFormatterCmd != "" {
		fmt.Fprintln(&b, "#")
		fmt.Fprintln(&b, "# Bare URLs will be formatted into org-mode links automatically. To")
		fmt.Fprintln(&b, "# format one yourself instead, write it as [[http://...]] directly.")
	}
	return b.String()
}

// commentedHeadlineLine renders h as a single line of real org syntax —
// its actual stars and keyword — for display as context (always inside
// a "# " comment, never parsed as a real headline).
func commentedHeadlineLine(h *org.Headline) string {
	stars := strings.Repeat("*", h.Level)
	if h.Keyword != "" {
		return stars + " " + h.Keyword + " " + h.Title
	}
	return stars + " " + h.Title
}

// siblingHeadlines returns h's immediate previous and next siblings
// (within its parent's children, or its file's top-level list if h is
// top-level), or nil for either that doesn't exist. earlierCount is the
// number of further siblings before prev (i.e. not shown by prev alone).
func (m *Model) siblingHeadlines(h *org.Headline) (prev, next *org.Headline, earlierCount int) {
	f, parent, idx := m.insertPosition(h)
	if idx < 0 {
		return nil, nil, 0
	}
	var list []*org.Headline
	if parent != nil {
		list = parent.Children
	} else if f != nil {
		list = f.Headlines
	}
	if idx > 0 {
		prev = list[idx-1]
		earlierCount = idx - 1
	}
	if idx+1 < len(list) {
		next = list[idx+1]
	}
	return prev, next, earlierCount
}

// resolveInsertPosition computes where a new entry belongs relative to
// the row under the cursor, for o/O and p/P alike: on a headline row, a
// sibling placed immediately after (before=false) or before (before=true)
// the current headline (after/before its whole subtree, if it has
// children); on a file row, the end (before=false) or beginning
// (before=true) of that file. ok is false if there's nothing sensible to
// place relative to.
func (m *Model) resolveInsertPosition(before bool) (f *org.File, parent *org.Headline, idx, level int, origin *org.Headline, ok bool) {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return nil, nil, 0, 0, nil, false
	}
	row := m.rows[m.cursor]

	switch {
	case row.headline != nil:
		h := row.headline
		origin, level = h, h.Level
		f, parent, idx = m.insertPosition(h)
		if idx < 0 {
			return nil, nil, 0, 0, nil, false
		}
		if !before {
			idx++
		}
		return f, parent, idx, level, origin, true

	case row.file != nil:
		f, level = row.file, 1
		if !before {
			idx = len(f.Headlines)
		}
		return f, nil, idx, level, nil, true
	}
	return nil, nil, 0, 0, nil, false
}

// insertHeadline inserts a blank headline (see resolveInsertPosition for
// where) and opens it in $EDITOR. The insert isn't recorded in undo
// history until the editor session finishes successfully (see
// commitInsert), so the whole "open a headline, type into it" session is
// one undo step, matching vim's o/O.
func (m *Model) insertHeadline(before bool) tea.Cmd {
	f, parent, idx, level, origin, ok := m.resolveInsertPosition(before)
	if !ok {
		return nil
	}
	return m.insertHeadlineAt(f, parent, idx, level, origin, nil, false)
}

// startCapture (:capture, "gC") appends a blank top-level headline to
// the end of the inbox file and opens it in $EDITOR — a dedicated
// quick-add path, distinct from o/O, that always targets the inbox
// regardless of the current cursor position or view (agenda, clarify,
// or scrolled to some other file entirely in outline). A no-op (with a
// status message) if the inbox file isn't loaded.
func (m *Model) startCapture() tea.Cmd {
	return m.startCaptureImpl(false)
}

// startCaptureAndPickMeeting ("gX") is startCapture immediately followed
// by "gM" (see startMeetingPicker) once the capture's editor session
// finishes successfully (see insertContext.thenPickMeeting/finishEdit) —
// a shortcut for the common case of capturing something during a
// meeting and wanting to attach that meeting to it right away, without
// two separate keystrokes bracketing the (possibly slow) editor
// round-trip. Cancelling the capture (empty/blank result, or the editor
// failing to run) never opens the picker — there's nothing to attach it
// to.
func (m *Model) startCaptureAndPickMeeting() tea.Cmd {
	return m.startCaptureImpl(true)
}

func (m *Model) startCaptureImpl(thenPickMeeting bool) tea.Cmd {
	f := m.findInboxFile()
	if f == nil {
		m.message = fmt.Sprintf("No %s file in this org directory", m.inboxFile)
		return nil
	}
	// origin is the headline the cursor is currently on, if any, so
	// cancelling the capture returns focus there rather than to the
	// inbox — capture is meant to not disturb whatever you were doing.
	// originFile covers the file-row case (origin nil): without it,
	// rollback would fall back to insertContext.f, which for capture is
	// always the inbox, not necessarily wherever the cursor actually was.
	return m.insertHeadlineAt(f, nil, len(f.Headlines), 1, m.currentHeadline(), m.currentRowFile(), thenPickMeeting)
}

func parseRFC3339Property(h *org.Headline, key string) (time.Time, bool) {
	raw, ok := h.Properties[key]
	if !ok {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// calendarEventEntry is one calendar meeting a headline is linked to
// (see calendarEventEntries): its name and link — the same pair
// calendarEventLinks resolves into a "<meeting name>: <url>" string —
// plus its start time, when one can still be resolved (hasWhen is false
// once the meeting has aged out of calendar.org's synced window, the
// same case in which the *_LINKS property snapshot keeps the name/link
// working but there's no live time left to show).
type calendarEventEntry struct {
	title, url string
	when       time.Time
	hasWhen    bool
}

// calendarEventEntries resolves h's calendar-meeting properties into
// one entry per linked meeting, used by infoBufferLines to surface the
// meeting(s) an entry references in its own "Meeting:" section: h's own
// link, if h is itself a synced calendar event (GCAL_HTML_LINK — see
// internal/calendarsync/convert.go); one-off events it's attached to
// via "gM" (GCAL_EVENT_LINKS/GCAL_EVENT_IDS); recurring series it's
// attached to, likewise via "gM" (GCAL_RECURRING_EVENT_LINKS/
// GCAL_RECURRING_EVENT_IDS) — see resolveMeetingEntries for how each of
// the latter two pairs is resolved; and any meeting h is linked to
// purely by sharing a tag with it (see tagLinkedMeetingCandidates),
// skipping one already covered by an explicit attachment above so a
// meeting that's both "gM"-attached and tag-matched isn't listed twice.
// This is what lets calendarView show an event's meeting details (link,
// description, location) only on demand (folded by default — see
// appendCalendarHeadlines) rather than inline: the link is still always
// one glance away, in the info buffer.
func (m *Model) calendarEventEntries(h *org.Headline) []calendarEventEntry {
	var entries []calendarEventEntry
	if url := h.Properties["GCAL_HTML_LINK"]; url != "" {
		e := calendarEventEntry{title: h.Title, url: url}
		e.when, e.hasWhen = parseRFC3339Property(h, "GCAL_START")
		entries = append(entries, e)
	}
	entries = append(entries, m.resolveMeetingEntries(h, "GCAL_EVENT_LINKS", "GCAL_EVENT_ID", "GCAL_EVENT_IDS")...)
	entries = append(entries, m.resolveMeetingEntries(h, "GCAL_RECURRING_EVENT_LINKS", "GCAL_RECURRING_EVENT_ID", "GCAL_RECURRING_EVENT_IDS")...)

	attached := make(map[meetingKey]bool)
	for _, id := range strings.Fields(h.Properties["GCAL_EVENT_IDS"]) {
		attached[meetingKey{oneOffMeeting, id}] = true
	}
	for _, id := range strings.Fields(h.Properties["GCAL_RECURRING_EVENT_IDS"]) {
		attached[meetingKey{recurringMeeting, id}] = true
	}
	for _, c := range m.tagLinkedMeetingCandidates(h, time.Now()) {
		if attached[meetingKey{c.kind, c.id}] || c.link == "" {
			continue
		}
		entries = append(entries, calendarEventEntry{title: c.title, url: c.link, when: c.when, hasWhen: true})
	}
	return entries
}

// calendarEventLinks resolves h's calendar-meeting properties into
// "<meeting name>: <url>" strings — calendarEventEntries (see above)
// with the time dropped, kept only for the existing tests that check
// link resolution without caring about meeting times.
func (m *Model) calendarEventLinks(h *org.Headline) []string {
	entries := m.calendarEventEntries(h)
	if len(entries) == 0 {
		return nil
	}
	links := make([]string, len(entries))
	for i, e := range entries {
		links[i] = e.title + ": " + e.url
	}
	return links
}

// resolveMeetingEntries resolves one (linksProp, idProp, idsProp) triple
// on h into calendarEventEntry values.
//
// linksProp (set by "gM" — one "[[url][title]]" per matched/attached
// meeting) is tried first: it was captured once, at the time h was
// linked to the meeting, so its name/link keep working indefinitely,
// even long after :sync-calendar's sync window has moved past the
// meeting (or the meeting stopped recurring entirely) and calendar.org
// no longer has it cached — its start time, looked up live by
// findEventTimeByLink, is the one part of the entry that can still come
// up empty in that case. Only if linksProp is missing entirely — e.g.
// an idsProp hand-attached to a task directly (per DESIGN.md's
// project↔meeting association) rather than via "gM" — does this fall
// back to a live lookup by idProp (the per-headline property
// identifying a single calendar.org event, GCAL_EVENT_ID or
// GCAL_RECURRING_EVENT_ID) against whatever calendar.org currently has
// cached, which (with no captured link to fall back on) can come up
// empty entirely once the event ages out; an ID that resolves neither
// way is silently skipped rather than shown broken.
func (m *Model) resolveMeetingEntries(h *org.Headline, linksProp, idProp, idsProp string) []calendarEventEntry {
	if raw := h.Properties[linksProp]; raw != "" {
		var entries []calendarEventEntry
		for _, l := range parseOrgLinks(raw) {
			title := l.description
			if title == "" {
				title = l.url
			}
			e := calendarEventEntry{title: title, url: l.url}
			e.when, e.hasWhen = m.findEventTimeByLink(l.url)
			entries = append(entries, e)
		}
		return entries
	}

	raw := h.Properties[idsProp]
	if raw == "" {
		return nil
	}
	var entries []calendarEventEntry
	for _, id := range strings.Fields(raw) {
		title, url, when, hasWhen, ok := m.findHeadlineByProperty(idProp, id)
		if !ok || url == "" {
			continue
		}
		entries = append(entries, calendarEventEntry{title: title, url: url, when: when, hasWhen: hasWhen})
	}
	return entries
}

// orgLink is one org-mode "[[url][description]]" (or bare "[[url]]")
// link, as parsed out of a property value like GCAL_EVENT_LINKS.
type orgLink struct {
	url, description string
}

// parseOrgLinks extracts every org-mode link in text, in order.
func parseOrgLinks(text string) []orgLink {
	matches := orgLinkRe.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil
	}
	links := make([]orgLink, len(matches))
	for i, mm := range matches {
		links[i] = orgLink{url: mm[1], description: mm[2]}
	}
	return links
}

// findHeadlineByProperty searches every loaded org file for a headline
// whose idProp property (GCAL_EVENT_ID or GCAL_RECURRING_EVENT_ID —
// i.e. one :sync-calendar wrote to calendar.org) equals id, returning
// its title, GCAL_HTML_LINK, and GCAL_START (when, hasWhen — false if
// missing/unparseable, same as parseRFC3339Property).
func (m *Model) findHeadlineByProperty(idProp, id string) (title, url string, when time.Time, hasWhen bool, ok bool) {
	for _, f := range m.ws.Files {
		org.Walk(f.Headlines, func(candidate *org.Headline) {
			if ok || candidate.Properties[idProp] != id {
				return
			}
			title, url, ok = candidate.Title, candidate.Properties["GCAL_HTML_LINK"], true
			when, hasWhen = parseRFC3339Property(candidate, "GCAL_START")
		})
		if ok {
			return title, url, when, hasWhen, true
		}
	}
	return "", "", time.Time{}, false, false
}

// findEventTimeByLink searches every loaded org file for a synced
// calendar event (GCAL_HTML_LINK) matching url, returning its
// GCAL_START. Used by resolveMeetingEntries to recover a meeting's
// start time for a "gM"-attached entry whose *_LINKS property only
// captured the title/link, not the time, at attach time — comes up
// empty once the event has aged out of calendar.org's synced window,
// same as any other live lookup by ID.
func (m *Model) findEventTimeByLink(url string) (when time.Time, ok bool) {
	for _, f := range m.ws.Files {
		org.Walk(f.Headlines, func(candidate *org.Headline) {
			if ok || candidate.Properties["GCAL_HTML_LINK"] != url {
				return
			}
			when, ok = parseRFC3339Property(candidate, "GCAL_START")
		})
		if ok {
			return when, true
		}
	}
	return time.Time{}, false
}

// currentRowFile returns the file the cursor's current row belongs to:
// the row's own file if it's a file-header row, else the file that owns
// its headline. Used by startCapture as rollbackInsert's fallback focus
// target when the cursor isn't on a headline (see insertContext.originFile).
func (m *Model) currentRowFile() *org.File {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return nil
	}
	r := m.rows[m.cursor]
	if r.file != nil {
		return r.file
	}
	if r.headline != nil {
		return m.fileForHeadline(r.headline)
	}
	return nil
}

// insertHeadlineAt is the shared machinery behind insertHeadline (o/O)
// and startCapture: splices a blank headline into f (at index within
// parent's children, or f's top-level list if parent is nil) and opens
// it in $EDITOR — for a vim-family editor, cursor already right after
// the bullet and in insert mode (see cursorAtEntryStart), so typing the
// new title can start immediately — pre-filled with a CREATED property
// set to now — org-mode's standard (if not automatic) convention for
// recording an entry's creation time, e.g. via org-capture's %U escape.
// It's part of the editable template, not stamped after the fact, so
// it's just as overridable or deletable as anything else the user types
// before saving. origin (and originFile, its fallback when origin is
// nil) is refocused if the session is rolled back — see rollbackInsert.
// The insert isn't recorded in undo history until the editor session
// finishes successfully (see commitInsert), so the whole "open a
// headline, type into it" session is one undo step.
//
// Deliberately does not attach any calendar-meeting info even from
// startCapture, unlike an earlier version of this feature: capture isn't
// interactive, so a wrong guess (any meeting merely in progress at the
// moment of capture, whether or not it's actually relevant) could only
// be undone by hand-editing properties afterward. "gM" (see
// startMeetingPicker) is the deliberate, interactive way to attach a
// meeting instead — nothing here does it for you, though
// thenPickMeeting (set only by startCaptureAndPickMeeting, "gX") queues
// it up to run automatically right after the editor session commits —
// see insertContext.thenPickMeeting and finishEdit.
func (m *Model) insertHeadlineAt(f *org.File, parent *org.Headline, idx, level int, origin *org.Headline, originFile *org.File, thenPickMeeting bool) tea.Cmd {
	tentative := &org.Headline{Level: level, Parent: parent}
	tentative.SetProperty("CREATED", "["+time.Now().Format("2006-01-02 Mon 15:04")+"]")
	if parent != nil {
		parent.Children = spliceHeadlines(parent.Children, idx, 0, []*org.Headline{tentative})
	} else {
		f.Headlines = spliceHeadlines(f.Headlines, idx, 0, []*org.Headline{tentative})
	}
	m.rebuildRows()
	m.focusHeadline(tentative)

	ctx := insertContext{f: f, parent: parent, index: idx, origin: origin, originFile: originFile, thenPickMeeting: thenPickMeeting}
	cmd := m.launchEditor(tentative, &ctx, cursorAtEntryStart)
	if cmd == nil {
		// Couldn't even launch the editor; don't leave a blank
		// placeholder headline behind with no way to remove it.
		m.rollbackInsert(ctx, tentative)
	}
	return cmd
}

// refuseIfImmutable reports whether h is currently locked by an
// in-flight :format-links batch (see m.immutable), setting an
// explanatory status message if so. Every single-entry command that
// would mutate h in some way (delete, status change, deadline, edit,
// promote/demote) checks this before doing anything else.
func (m *Model) refuseIfImmutable(h *org.Headline) bool {
	if h == nil || !m.immutable[h] {
		return false
	}
	m.message = "This entry is being formatted by :format-links and can't be changed yet"
	return true
}

// filterImmutable removes any headline currently locked by an in-flight
// :format-links batch from headlines, for a bulk command (visual-mode or
// numeric-prefixed delete/status-change) to apply to the rest rather
// than refusing the whole operation outright. skipped is how many were
// removed, for the caller's summary message.
func (m *Model) filterImmutable(headlines []*org.Headline) (kept []*org.Headline, skipped int) {
	for _, h := range headlines {
		if m.immutable[h] {
			skipped++
			continue
		}
		kept = append(kept, h)
	}
	return kept, skipped
}

// deleteHeadline removes the current headline and its whole subtree
// ("dd"), storing a copy in the register so it can be pasted back with
// p/P. A no-op on file rows, or on an entry locked by :format-links.
// Matches vim's own dd: the cursor stays at the same screen position
// (see pushUndoKeepingCursor) rather than jumping to a tree-sibling.
func (m *Model) deleteHeadline() {
	h := m.currentHeadline()
	if h == nil || m.refuseIfImmutable(h) {
		return
	}
	f, parent, idx := m.insertPosition(h)
	if idx < 0 {
		return
	}
	m.register = []*org.Headline{h}
	m.pushUndoKeepingCursor(&deleteAction{spliceAction{f: f, parent: parent, index: idx, headlines: []*org.Headline{h}, inTree: true}})

	if m.view == clarifyView && h == m.clarifyTarget {
		m.advanceClarifyTarget()
	}
	// dd removes h's whole subtree, so a mark on any descendant (not
	// just h itself) needs clearing too.
	org.Walk([]*org.Headline{h}, m.clearMarksFor)
}

// yankHeadline ("yy") copies the current headline (and its whole
// subtree) into the register for pasting elsewhere with p/P — unlike
// dd, it leaves the original untouched (in the outline, the agenda, or
// clarify view — wherever the cursor happens to be). The register holds
// an independent snapshot taken now, so later edits to the original
// before pasting aren't reflected in what gets pasted.
func (m *Model) yankHeadline() {
	h := m.currentHeadline()
	if h == nil {
		return
	}
	m.register = []*org.Headline{org.CloneHeadline(h)}
	m.message = "Yanked"
}

// pasteHeadline inserts a copy of the register's contents — one entry
// (dd/yy) or several, in the same order they were deleted/yanked in
// (visual-mode d, <N>dd) — after (before=false, "p") or before
// (before=true, "P") the current row (see resolveInsertPosition),
// adjusting each one's level (and its descendants', by the same amount)
// to fit the destination depth. The register itself is left untouched,
// so it can be pasted again.
func (m *Model) pasteHeadline(before bool) {
	if len(m.register) == 0 {
		m.message = "Nothing to paste"
		return
	}
	f, parent, idx, level, _, ok := m.resolveInsertPosition(before)
	if !ok {
		return
	}

	clones := make([]*org.Headline, len(m.register))
	for i, h := range m.register {
		clone := org.CloneHeadline(h)
		shiftHeadlineLevel(clone, level-clone.Level)
		clones[i] = clone
	}
	m.pushUndo(&insertAction{spliceAction{f: f, parent: parent, index: idx, headlines: clones}})
}

// demoteHeadline (">>") nests the current headline (and its whole
// subtree) one level deeper, making it the last child of its previous
// sibling — matching org-mode's own demote-subtree behavior, which is
// also the only way to increase Level while keeping Parent consistent
// with it. A no-op (with a status-line message) if there's no previous
// sibling to nest under, since there's nothing sensible to reparent
// onto.
func (m *Model) demoteHeadline() {
	h := m.currentHeadline()
	if h == nil || m.refuseIfImmutable(h) {
		return
	}
	f, parent, idx := m.insertPosition(h)
	if idx <= 0 {
		m.message = "Cannot demote: no previous sibling to nest under"
		return
	}
	var list []*org.Headline
	if parent != nil {
		list = parent.Children
	} else if f != nil {
		list = f.Headlines
	}
	prevSibling := list[idx-1]

	m.pushUndo(&reparentAction{
		h: h, f: f,
		oldParent: parent, oldIndex: idx,
		newParent: prevSibling, newIndex: len(prevSibling.Children),
		delta: 1,
	})
}

// promoteHeadline ("<<") un-nests the current headline (and its
// whole subtree) one level shallower, making it the next sibling of its
// former parent — the exact inverse of demoteHeadline, and org-mode's
// own promote-subtree behavior. A no-op (with a message) if the
// headline is already top-level.
func (m *Model) promoteHeadline() {
	h := m.currentHeadline()
	if h == nil || m.refuseIfImmutable(h) {
		return
	}
	if h.Parent == nil {
		m.message = "Cannot promote: already at the top level"
		return
	}
	f, parent, idx := m.insertPosition(h)
	grandparent := parent.Parent
	_, _, parentIdx := m.insertPosition(parent)

	m.pushUndo(&reparentAction{
		h: h, f: f,
		oldParent: parent, oldIndex: idx,
		newParent: grandparent, newIndex: parentIdx + 1,
		delta: -1,
	})
}

// shiftHeadlineLevel adds delta to h.Level and every descendant's Level,
// preserving relative nesting while adapting to a new absolute depth.
func shiftHeadlineLevel(h *org.Headline, delta int) {
	if delta == 0 {
		return
	}
	h.Level += delta
	for _, c := range h.Children {
		shiftHeadlineLevel(c, delta)
	}
}

// commentLineRe matches an org comment line: '#' followed by whitespace
// or end of line. This deliberately excludes org directives like
// "#+TITLE:" (hash immediately followed by '+'), which aren't comments.
var commentLineRe = regexp.MustCompile(`^\s*#(\s|$)`)

// stripCommentLines removes every comment line from text, the same way
// git strips '#'-prefixed lines from a commit message template before
// using it. This is what makes editorContextComment's trailer inert
// regardless of whether the user deletes it.
func stripCommentLines(text string) string {
	lines := strings.Split(text, "\n")
	out := lines[:0]
	for _, l := range lines {
		if !commentLineRe.MatchString(l) {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// headlineBulletChars are plain-text/markdown list-bullet markers that
// don't count as real content on their own — the kind of thing a
// markdown habit or a paste might leave behind with nothing actually
// typed after it.
const headlineBulletChars = "-*+•"

// isBlankHeadlineTitle reports whether title amounts to no text at all:
// nothing but whitespace and/or a run of leading bullet characters (see
// headlineBulletChars). Stripping is prefix-only — never touching the
// end of the string — so real content that merely starts with one of
// these characters (e.g. "-1 lap penalty") is correctly seen as
// non-blank once whatever follows the bullet run is reached.
func isBlankHeadlineTitle(title string) bool {
	s := strings.TrimSpace(title)
	for s != "" {
		r, size := utf8.DecodeRuneInString(s)
		if !strings.ContainsRune(headlineBulletChars, r) {
			break
		}
		s = strings.TrimSpace(s[size:])
	}
	return s == ""
}

// orgLinkRe matches an existing org-mode link, "[[url]]" or
// "[[url][description]]" — group 1 is the url, group 2 the description
// (absent for the no-description form). Used both to make formatURLs
// (and :format-links) leave existing links alone, and to render a
// link's display text (the description if present, else the url) in
// the row list.
//
// The description is matched non-greedily against *any* character, up
// to the nearest following "]]" — deliberately not excluding "[" and
// "]" the way the url group does, matching real org-mode's own lenient
// link grammar (it finds the closest "]]", rather than forbidding
// brackets in a description outright). Without this, a formatter output
// like "[[https://example.com][Some [bracketed] title]]" — a
// perfectly valid org-mode link — would fail to match here at all: the
// url inside it would then still look "bare" on the next :format-links
// or in-editor pass, sending it through the formatter again and
// double-wrapping it.
var orgLinkRe = regexp.MustCompile(`\[\[([^\]\[]+)\](?:\[(.*?)\])?\]`)

// defaultURLSchemes are always recognized as bare-URL prefixes,
// independent of whatever extra prefixes the user configures (see
// WithURLFormatterPrefixes) for things like a shortlink service
// ("bit.ly/...") or an internal go-link convention ("go/...") that don't
// carry a scheme.
var defaultURLSchemes = []string{"https://", "http://"}

// buildBareURLRegexp compiles the regexp formatURLs uses to find a bare
// URL not already wrapped in link brackets: one of defaultURLSchemes, or
// one of extraPrefixes, followed by a run of non-whitespace,
// non-bracket characters (stopping at '[' or ']' so a match can never
// span into or out of an org-mode link). Each extra prefix is guarded
// with \b so it only matches at a word boundary — without that, a short
// prefix like "go/" would also match mid-word inside something like
// "embargo/foo". The built-in schemes don't need this guard: nothing
// realistic precedes "https://" mid-word.
func buildBareURLRegexp(extraPrefixes []string) *regexp.Regexp {
	alts := make([]string, 0, len(defaultURLSchemes)+len(extraPrefixes))
	for _, p := range defaultURLSchemes {
		alts = append(alts, regexp.QuoteMeta(p))
	}
	for _, p := range extraPrefixes {
		if p == "" {
			continue
		}
		alts = append(alts, `\b`+regexp.QuoteMeta(p))
	}
	return regexp.MustCompile(`(?:` + strings.Join(alts, "|") + `)[^\s\[\]]+`)
}

// renderTitleForDisplay renders title for the row list: each org-mode
// link is replaced with just its display text (the description, or the
// url if there's no description) and underlined, instead of showing the
// raw "[[url][description]]" syntax. base is the style otherwise applied
// to the title (e.g. doneTitleStyle for a DONE/CANCELLED item); every
// segment — link or plain text — is rendered with base (underlined,
// for a link) so the two compose without nesting escape codes.
func renderTitleForDisplay(title string, base lipgloss.Style, query string) string {
	matches := orgLinkRe.FindAllStringSubmatchIndex(title, -1)
	if len(matches) == 0 {
		return highlightMatches(title, query, base)
	}
	linkStyle := base.Underline(true)

	var b strings.Builder
	last := 0
	for _, span := range matches {
		start, end := span[0], span[1]
		url := title[span[2]:span[3]]
		desc := ""
		if span[4] >= 0 {
			desc = title[span[4]:span[5]]
		}
		display := desc
		if display == "" {
			display = url
		}
		if start > last {
			b.WriteString(highlightMatches(title[last:start], query, base))
		}
		b.WriteString(highlightMatches(display, query, linkStyle))
		last = end
	}
	if last < len(title) {
		b.WriteString(highlightMatches(title[last:], query, base))
	}
	return b.String()
}

// formatURLs runs every bare URL in text (i.e. not already part of an
// org-mode link) through the configured urlFormatterCmd, replacing it
// with that program's output. It's a no-op if no formatter is configured.
func (m *Model) formatURLs(text string) string {
	if m.urlFormatterCmd == "" {
		return text
	}
	if m.bareURLRe == nil {
		// New() normally builds this from urlFormatterPrefixes; fall
		// back to building it here too, so a Model constructed as a
		// literal (as several tests do) never panics on a nil regexp.
		m.bareURLRe = buildBareURLRegexp(m.urlFormatterPrefixes)
	}
	spans := bareURLSpansOutsideLinks(m.bareURLRe, text)
	if len(spans) == 0 {
		return text
	}

	cache := make(map[string]string)
	var b strings.Builder
	last := 0
	for _, span := range spans {
		start, end := span[0], span[1]
		url := text[start:end]
		formatted, ok := cache[url]
		if !ok {
			formatted = m.runURLFormatter(url)
			cache[url] = formatted
		}
		b.WriteString(text[last:start])
		b.WriteString(formatted)
		last = end
	}
	b.WriteString(text[last:])
	return b.String()
}

// bareURLSpansOutsideLinks returns the start/end byte offsets (as
// FindAllStringIndex would) of every match of re in text that doesn't
// fall inside an existing org-mode link, in order — the shared
// "which bare URLs actually need formatting" logic behind formatURLs
// (one text at a time, during editing) and collectFormatLinksTargets
// (every entry at once, for :format-links).
func bareURLSpansOutsideLinks(re *regexp.Regexp, text string) [][2]int {
	matches := re.FindAllStringIndex(text, -1)
	if len(matches) == 0 {
		return nil
	}
	linkSpans := orgLinkRe.FindAllStringIndex(text, -1)
	withinLink := func(pos int) bool {
		for _, s := range linkSpans {
			if pos >= s[0] && pos < s[1] {
				return true
			}
		}
		return false
	}
	var out [][2]int
	for _, span := range matches {
		if !withinLink(span[0]) {
			out = append(out, [2]int{span[0], span[1]})
		}
	}
	return out
}

// runURLFormatter invokes the configured urlFormatterCmd as
// `<program> <extra args...> <url>` and returns its trimmed stdout.
// urlFormatterCmd is split via splitCommandFields, the same quote-aware
// splitter buildEditorCommand uses for $EDITOR (e.g. `myformatter
// --template "a template"` runs "myformatter" with "--template" and "a
// template" as leading arguments before url), and each field has a
// leading "~" expanded via expandHomeField — without either of these, a
// formatter configured with any extra arguments, or with a path under
// the home directory, would fail every time: exec.Command treats its
// first argument as a literal executable name, so an unsplit
// "myformatter -x" means "look for a program literally named
// 'myformatter -x'", and an unexpanded "~/bin/myformatter" (correct on
// the command line, where the shell expands it, but not from the config
// file, where nothing does) means "look for a program literally named
// '~/bin/myformatter'" — neither ever exists. Every attempt is logged
// via the standard log package, including the subprocess's stderr on
// failure — cmd/orgtd redirects it to a file at startup, since the TUI
// itself owns the terminal and plain log output can't share it. Without
// this, silently leaving the URL unchanged on any error gives no clue
// why; a failure also sets m.message so it's visible without leaving
// the app or checking the log. The actual run goes through
// runLoggedCommand, which also records it (start, every output line, and
// its exit code) in m.execLog for :log.
func (m *Model) runURLFormatter(url string) string {
	fields := splitCommandFields(m.urlFormatterCmd)
	if len(fields) == 0 {
		log.Printf("url formatter: urlFormatterCmd %q has no fields after splitting; skipping %q", m.urlFormatterCmd, url)
		return url
	}
	for i, f := range fields {
		fields[i] = expandHomeField(f)
	}
	args := append(append([]string{}, fields[1:]...), url)
	log.Printf("url formatter: running %v", append([]string{fields[0]}, args...))

	out, err := runLoggedCommand(m.execLog, fields[0], args, "")
	if err != nil {
		detail := err.Error()
		if exitErr, ok := err.(*exec.ExitError); ok && len(exitErr.Stderr) > 0 {
			detail = fmt.Sprintf("%v (stderr: %s)", err, strings.TrimSpace(string(exitErr.Stderr)))
		}
		log.Printf("url formatter: %s failed: %s", fields[0], detail)
		m.message = fmt.Sprintf("URL formatter failed%s: %s", m.debugLogHint(), detail)
		return url
	}

	formatted := strings.TrimSpace(out)
	if formatted == "" {
		log.Printf("url formatter: %s produced no output for %q", fields[0], url)
		m.message = "URL formatter produced no output" + m.debugLogHint()
		return url
	}
	log.Printf("url formatter: %s -> %q", fields[0], formatted)
	return formatted
}

// runBatchURLFormatter invokes urlFormatterCmd once, feeding it every
// url (one per line) on its stdin instead of one at a time via a
// trailing argument (see runURLFormatter) — used by :format-links to
// format many URLs with a single external process instead of one
// process per URL, which could be prohibitively slow for a large batch.
// Returns exactly len(urls) formatted strings, in the same order;
// anything else (a run failure, or a line-count mismatch) is an error,
// since there'd be no reliable way to match output back to input. The
// run itself goes through runLoggedCommand, which records it in elog
// (start, every output line as it's produced, and its exit code) for
// :log — this runs on its own goroutine (see startFormatLinks), so elog
// must be safe for concurrent use, which is exactly what it's for.
func runBatchURLFormatter(elog *execLog, urlFormatterCmd string, urls []string) ([]string, error) {
	fields := splitCommandFields(urlFormatterCmd)
	if len(fields) == 0 {
		return nil, fmt.Errorf("url formatter command is empty")
	}
	for i, f := range fields {
		fields[i] = expandHomeField(f)
	}

	stdin := strings.Join(urls, "\n") + "\n"
	log.Printf("url formatter (batch): running %v with %d url(s) on stdin", append([]string{fields[0]}, fields[1:]...), len(urls))

	out, err := runLoggedCommand(elog, fields[0], fields[1:], stdin)
	if err != nil {
		detail := err.Error()
		if exitErr, ok := err.(*exec.ExitError); ok && len(exitErr.Stderr) > 0 {
			detail = fmt.Sprintf("%v (stderr: %s)", err, strings.TrimSpace(string(exitErr.Stderr)))
		}
		log.Printf("url formatter (batch): %s failed: %s", fields[0], detail)
		return nil, fmt.Errorf("%s", detail)
	}

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(urls) == 0 {
		return nil, nil
	}
	if len(lines) != len(urls) {
		log.Printf("url formatter (batch): %s produced %d line(s), want %d", fields[0], len(lines), len(urls))
		return nil, fmt.Errorf("%s produced %d line(s) of output, want %d (one per url)", fields[0], len(lines), len(urls))
	}
	log.Printf("url formatter (batch): %s -> %d formatted url(s)", fields[0], len(lines))
	return lines, nil
}

// formatLinksSpan locates one bare URL within a headline's Title
// (field == formatLinksTitleField) or one of its Body lines
// (field == that line's index) — see collectFormatLinksTargets.
type formatLinksSpan struct {
	field      int
	start, end int
}

// formatLinksTitleField is formatLinksSpan.field's sentinel for "this
// span is in the headline's Title", distinguishing it from any
// (non-negative) Body line index.
const formatLinksTitleField = -1

// formatLinksTarget is one headline :format-links found bare URLs in,
// plus exactly where each one is — the pending edits, once formatted
// text comes back for each (see finishFormatLinks).
type formatLinksTarget struct {
	h     *org.Headline
	spans []formatLinksSpan
}

// collectFormatLinksTargets walks every headline in every loaded file
// and returns each one that contains a bare URL not already wrapped in
// an org-mode link, together with the flat, ordered list of those URLs
// (target by target, Title then each Body line in order within a
// target) — the same order finishFormatLinks later consumes the
// external formatter's output in. A headline already locked by an
// earlier, still-in-flight :format-links batch (see m.immutable) is
// skipped: it's already queued, and rescanning it here against text
// that batch hasn't rewritten yet would just queue the same URLs a
// second time.
func (m *Model) collectFormatLinksTargets() ([]*formatLinksTarget, []string) {
	if m.bareURLRe == nil {
		m.bareURLRe = buildBareURLRegexp(m.urlFormatterPrefixes)
	}
	var targets []*formatLinksTarget
	var urls []string
	for _, f := range m.ws.Files {
		org.Walk(f.Headlines, func(h *org.Headline) {
			if m.immutable[h] {
				return
			}
			var spans []formatLinksSpan
			for _, sp := range bareURLSpansOutsideLinks(m.bareURLRe, h.Title) {
				spans = append(spans, formatLinksSpan{field: formatLinksTitleField, start: sp[0], end: sp[1]})
				urls = append(urls, h.Title[sp[0]:sp[1]])
			}
			for i, line := range h.Body {
				for _, sp := range bareURLSpansOutsideLinks(m.bareURLRe, line) {
					spans = append(spans, formatLinksSpan{field: i, start: sp[0], end: sp[1]})
					urls = append(urls, line[sp[0]:sp[1]])
				}
			}
			if len(spans) > 0 {
				targets = append(targets, &formatLinksTarget{h: h, spans: spans})
			}
		})
	}
	return targets, urls
}

// formatLinksMsg reports that a :format-links batch (see
// startFormatLinks) has finished — targets and urls are exactly what
// startFormatLinks sent, echoed back so finishFormatLinks doesn't need
// any other state to line formatted back up with the entries it came
// from. formatted is nil if err is set.
type formatLinksMsg struct {
	targets   []*formatLinksTarget
	formatted []string
	err       error
}

// formatLinksFormatterCmd returns the external program :format-links
// should invoke in batch mode: formatLinksURLFormatterCmd if set (see
// WithFormatLinksURLFormatter), else urlFormatterCmd — the same command
// used for live in-editor formatting, so configuring only url_formatter
// (as before this option existed) still works for :format-links too.
func (m *Model) formatLinksFormatterCmd() string {
	if m.formatLinksURLFormatterCmd != "" {
		return m.formatLinksURLFormatterCmd
	}
	return m.urlFormatterCmd
}

// startFormatLinks (":format-links") locks every entry with an
// unformatted bare URL (see collectFormatLinksTargets) — immediately,
// on the main goroutine, before this Cmd even runs — then hands all of
// their URLs to formatLinksFormatterCmd in one external process, running
// in the background so the rest of the app stays fully usable while
// it's in flight (unlike the synchronous, foreground formatting a
// single `i` edit does). Locked entries show a gutter marker (see
// lockColumn) and refuse any command that would change them (see
// refuseIfImmutable/filterImmutable) until finishFormatLinks unlocks
// them.
func (m *Model) startFormatLinks() tea.Cmd {
	formatterCmd := m.formatLinksFormatterCmd()
	if formatterCmd == "" {
		m.message = "No URL formatter configured (see :config)"
		return nil
	}
	targets, urls := m.collectFormatLinksTargets()
	if len(targets) == 0 {
		m.message = "No unformatted URLs found"
		return nil
	}
	for _, t := range targets {
		m.immutable[t.h] = true
	}
	m.message = fmt.Sprintf("Formatting links for %d entries in the background...", len(targets))

	elog := m.execLog
	return func() tea.Msg {
		formatted, err := runBatchURLFormatter(elog, formatterCmd, urls)
		return formatLinksMsg{targets: targets, formatted: formatted, err: err}
	}
}

// formatLinksFieldEdit pairs one formatLinksSpan with the formatted text
// that should replace it, for applyFormatLinksEdits.
type formatLinksFieldEdit struct {
	span      formatLinksSpan
	formatted string
}

// applyFormatLinksEdits rewrites text, replacing each edit's span with
// its formatted text. edits must be in ascending span order (as
// collectFormatLinksTargets produces them) and all refer to spans within
// text; they're applied back to front so replacing a later span never
// invalidates an earlier span's still-pending offsets.
func applyFormatLinksEdits(text string, edits []formatLinksFieldEdit) string {
	for i := len(edits) - 1; i >= 0; i-- {
		e := edits[i]
		text = text[:e.span.start] + e.formatted + text[e.span.end:]
	}
	return text
}

// finishFormatLinks applies a completed :format-links batch: every
// targeted headline is unlocked (whether or not formatting actually
// succeeded — a failed batch shouldn't leave entries stuck immutable
// forever with no way to retry other than restarting orgtd), and on
// success, each one's Title/Body is rewritten with its bare URLs
// replaced by the formatter's output, grouped into one undo step per
// file touched (a batchAction — see undo.go) the same way other bulk
// operations are.
func (m Model) finishFormatLinks(msg formatLinksMsg) (tea.Model, tea.Cmd) {
	for _, t := range msg.targets {
		delete(m.immutable, t.h)
	}

	if msg.err != nil {
		m.message = fmt.Sprintf("Link formatting failed%s: %v", m.debugLogHint(), msg.err)
		m.rebuildRows()
		return m, nil
	}

	idx := 0
	var order []*org.File
	byFile := make(map[*org.File][]undoAction)
	for _, t := range msg.targets {
		editsByField := make(map[int][]formatLinksFieldEdit)
		for _, sp := range t.spans {
			editsByField[sp.field] = append(editsByField[sp.field], formatLinksFieldEdit{span: sp, formatted: msg.formatted[idx]})
			idx++
		}

		newTitle := t.h.Title
		if edits, ok := editsByField[formatLinksTitleField]; ok {
			newTitle = applyFormatLinksEdits(newTitle, edits)
		}
		newBody := append([]string(nil), t.h.Body...)
		for i := range newBody {
			if edits, ok := editsByField[i]; ok {
				newBody[i] = applyFormatLinksEdits(newBody[i], edits)
			}
		}

		f := m.fileForHeadline(t.h)
		action := &linkFormatAction{h: t.h, f: f, oldTitle: t.h.Title, newTitle: newTitle, oldBody: t.h.Body, newBody: newBody}
		if _, ok := byFile[f]; !ok {
			order = append(order, f)
		}
		byFile[f] = append(byFile[f], action)
	}
	for _, f := range order {
		m.pushUndo(&batchAction{actions: byFile[f]})
	}
	m.message = fmt.Sprintf("Formatted links for %d entries", len(msg.targets))
	return m, nil
}

// finishEdit reads back the edited entry and reparses it. For a plain
// `i`/`A` edit, the result replaces the original headline's own text in
// place, its children carried over unchanged since the edit buffer never
// showed them (see indentEntry below and launchEditor) — or, if
// the edit left nothing parseable, the original is left untouched. For
// an o/O insert session, a successful result is committed as a single
// undo step; any failure (editor error, unreadable file, unparseable or
// emptied-out result) rolls back the tentative placeholder entirely,
// leaving no trace.
func (m Model) finishEdit(msg editFinishedMsg) (tea.Model, tea.Cmd) {
	defer os.Remove(msg.path)
	logCompletedProcess(m.execLog, msg.cmd, msg.err)

	if msg.err != nil {
		m.message = fmt.Sprintf("Editor exited with an error: %v", msg.err)
		if msg.insert != nil {
			m.rollbackInsert(*msg.insert, msg.target)
		}
		return m, nil
	}

	data, err := os.ReadFile(msg.path)
	if err != nil {
		m.message = fmt.Sprintf("Could not read edited entry: %v", err)
		if msg.insert != nil {
			m.rollbackInsert(*msg.insert, msg.target)
		}
		return m, nil
	}

	// stripCommentLines can leave a trailing blank line behind — the one
	// separating the real content from editEntryContext's trailer (see
	// launchEditor) — so trim it the same way git's own cleanup mode
	// does, rather than let it accumulate as a stray blank body line on
	// every repeated edit.
	stripped := strings.TrimRight(stripCommentLines(string(data)), "\n")

	// The buffer never showed a bullet (see dedentEntry), so an
	// all-blank result unambiguously means "cancel" — indentEntry
	// below would otherwise turn it into a real, blank-titled headline
	// instead of nothing at all.
	if strings.TrimSpace(stripped) == "" {
		if msg.insert != nil {
			m.rollbackInsert(*msg.insert, msg.target)
			m.message = "Insert cancelled (empty)"
		} else {
			m.message = "Edited entry had no headline; leaving it unchanged"
		}
		return m, nil
	}
	stripped = indentEntry(stripped, msg.target.Level)

	text := m.formatURLs(stripped)

	file, err := org.Parse(strings.NewReader(text), "")
	if err != nil {
		m.message = fmt.Sprintf("Could not parse edited entry: %v", err)
		if msg.insert != nil {
			m.rollbackInsert(*msg.insert, msg.target)
		}
		return m, nil
	}

	if len(file.Headlines) == 0 {
		// Unreachable in practice — indentEntry above guarantees
		// the text starts with a real headline line — but kept as a
		// defensive fallback rather than assuming it.
		if msg.insert != nil {
			m.rollbackInsert(*msg.insert, msg.target)
			m.message = "Insert cancelled (empty)"
		} else {
			m.message = "Edited entry had no headline; leaving it unchanged"
		}
		return m, nil
	}

	if msg.insert != nil {
		if isBlankHeadlineTitle(file.Headlines[0].Title) {
			// A headline with stars and maybe a keyword but no actual
			// text (or just a stray bullet character) is just as
			// unusable as a genuinely empty result — don't create it.
			m.rollbackInsert(*msg.insert, msg.target)
			m.message = "Insert cancelled (no text)"
			return m, nil
		}
		m.commitInsert(*msg.insert, msg.target, file.Headlines)
		if msg.insert.thenPickMeeting {
			// commitInsert already focused file.Headlines[0], so the
			// picker (see startMeetingPicker) targets the just-captured
			// entry — see startCaptureAndPickMeeting ("gX").
			m.startMeetingPicker()
		}
		return m, nil
	}

	// The edit buffer never showed msg.target's children (see
	// launchEditor), so they never went through the editor at all —
	// carry them over as-is rather than leaving the edited entry
	// childless, or discarding them in favor of whatever (if anything)
	// the user happened to type at a deeper level in the buffer.
	file.Headlines[0].Children = msg.target.Children
	for _, c := range file.Headlines[0].Children {
		c.Parent = file.Headlines[0]
	}

	m.pushUndo(&subtreeReplaceAction{
		f:      m.fileForHeadline(msg.target),
		oldSet: []*org.Headline{msg.target},
		newSet: file.Headlines,
	})
	return m, nil
}

func (m *Model) currentHeadline() *org.Headline {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return nil
	}
	return m.rows[m.cursor].headline
}

// toggleFold ("za"/Tab) toggles whether the current headline's own
// children are hidden, one level.
func (m *Model) toggleFold() {
	h := m.currentHeadline()
	if h == nil || !hasFoldableContent(h) {
		return
	}
	m.collapsed[h] = !m.collapsed[h]
	m.rebuildRows()
}

// foldOpen ("zo") reveals the current headline's own children, one level.
func (m *Model) foldOpen() {
	h := m.currentHeadline()
	if h == nil || !hasFoldableContent(h) {
		return
	}
	m.collapsed[h] = false
	m.rebuildRows()
}

// foldClose ("zc") hides the current headline's own children, one level.
func (m *Model) foldClose() {
	h := m.currentHeadline()
	if h == nil || !hasFoldableContent(h) {
		return
	}
	m.collapsed[h] = true
	m.rebuildRows()
}

// foldOpenAll ("zO") reveals the current headline's entire subtree,
// recursively.
func (m *Model) foldOpenAll() {
	h := m.currentHeadline()
	if h == nil || !hasFoldableContent(h) {
		return
	}
	setCollapsedRecursive(m.collapsed, h, false)
	m.rebuildRows()
}

// foldCloseAll ("zC") hides the current headline's entire subtree,
// recursively — every descendant with children is marked collapsed too,
// so a later single-level zo doesn't reveal an inconsistent half-open
// state.
func (m *Model) foldCloseAll() {
	h := m.currentHeadline()
	if h == nil || !hasFoldableContent(h) {
		return
	}
	setCollapsedRecursive(m.collapsed, h, true)
	m.rebuildRows()
}

// foldToggleAll ("zA") opens the current headline's entire subtree
// recursively if it's currently folded, or closes it entirely
// recursively otherwise.
func (m *Model) foldToggleAll() {
	h := m.currentHeadline()
	if h == nil || !hasFoldableContent(h) {
		return
	}
	setCollapsedRecursive(m.collapsed, h, !m.collapsed[h])
	m.rebuildRows()
}

// setCollapsedRecursive sets collapsed[x] = value for h and every
// descendant of h that has foldable content (a headline with neither
// children nor a body has nothing to fold, so it's left out of the map).
func setCollapsedRecursive(collapsed map[*org.Headline]bool, h *org.Headline, value bool) {
	if hasFoldableContent(h) {
		collapsed[h] = value
	}
	for _, c := range h.Children {
		setCollapsedRecursive(collapsed, c, value)
	}
}

// moveCursor moves the cursor by delta entries — not delta rows — for
// j/k and the half-page scroll (ctrl+d/ctrl+u) alike: an entry's body
// lines are part of the entry, not separately steppable rows of their
// own, so they're always skipped over and never landed on.
func (m *Model) moveCursor(delta int) {
	if len(m.rows) == 0 {
		return
	}
	step := 1
	n := delta
	if delta < 0 {
		step = -1
		n = -delta
	}
	cur := m.cursor
	for n > 0 {
		next := cur + step
		if next < 0 || next >= len(m.rows) {
			break
		}
		cur = next
		if !m.rows[cur].isBodyLine {
			n--
		}
	}
	// Only reachable by hitting the very end of the list mid-body (the
	// last entry's trailing body line can be the last row overall) —
	// snap to its owning headline rather than resting on it.
	m.cursor = m.entryStart(cur)
}

// entryStart returns the row where the entry owning row i actually
// begins: i itself, or — if i is one of that entry's own body lines —
// the entry's title row.
func (m *Model) entryStart(i int) int {
	for i > 0 && m.rows[i].isBodyLine {
		i--
	}
	return i
}

// rowLevel returns the indentation level of the row at index i: 0 for a
// file header, or the headline's level otherwise.
// rowLevel returns the indentation/structural level of the row at index
// i — h.Level for an outline headline row, 0 for a file/section-header
// row, 1 for an agenda item row (see row.level).
func (m *Model) rowLevel(i int) int {
	return m.rows[i].level
}

// moveToLevel moves the cursor to the next (dir>0) or previous (dir<0)
// row at level lvl, skipping over any deeper rows along the way. If none
// remain at lvl, this lands on the next shallower row instead — "hopping
// up" progressively until it finds one (or runs off the end/start of the
// list, in which case it's a no-op).
func (m *Model) moveToLevel(dir, lvl int) {
	if len(m.rows) == 0 {
		return
	}
	i := m.cursor + dir
	for i >= 0 && i < len(m.rows) && (m.rowLevel(i) > lvl || m.rows[i].isBodyLine) {
		// A body line is never a valid stopping point here — it's not a
		// sibling or a hop-up target, just supplementary text — even on
		// the rare occasion its level happens to coincide with lvl (an
		// unrelated, shallower headline's body).
		i += dir
	}
	if i >= 0 && i < len(m.rows) {
		m.cursor = i
	}
}

// moveSiblingLevel moves the cursor to the next (dir>0) or previous
// (dir<0) row at the same indentation level as the current row (see
// moveToLevel). Used as the fallback for l/h when there's no
// deeper/shallower row to move into.
func (m *Model) moveSiblingLevel(dir int) {
	if len(m.rows) == 0 {
		return
	}
	m.moveToLevel(dir, m.rowLevel(m.cursor))
}

// jumpParagraph ("}"/"{", mirroring vim's paragraph motions) is like
// moveSiblingLevel, except a leaf (no children) is always treated as one
// level shallower than it actually is, so it hops up immediately rather
// than stepping through remaining leaf siblings one at a time — j/k
// already move between those just as well, one row at a time.
func (m *Model) jumpParagraph(dir int) {
	if len(m.rows) == 0 {
		return
	}
	m.pushJump()
	lvl := m.rowLevel(m.cursor)
	if r := m.rows[m.cursor]; r.headline != nil && len(r.headline.Children) == 0 {
		lvl--
	}
	m.moveToLevel(dir, lvl)
}

// moveDeeper moves the cursor into the next deeper indentation level
// (i.e. the current row's first visible child), or if there is none,
// falls back to moveSiblingLevel(1).
func (m *Model) moveDeeper() {
	if len(m.rows) == 0 {
		return
	}
	// Skip over any body lines right after the cursor — they're part of
	// the current entry, not something to move "into" — to find the
	// first real child, if any.
	next := m.cursor + 1
	for next < len(m.rows) && m.rows[next].isBodyLine {
		next++
	}
	if next < len(m.rows) && m.rowLevel(next) > m.rowLevel(m.cursor) {
		m.cursor = next
		return
	}
	m.moveSiblingLevel(1)
}

// moveShallower moves the cursor one structural level up — regardless of
// where that lands relative to the current row, mirroring moveDeeper's
// directness: to the current headline's parent if it's nested, or to its
// file's header row if it's top-level. On a file row already (nothing
// shallower than a file), it moves to the previous file's header row
// instead, so h never just leaves the cursor stuck in place.
func (m *Model) moveShallower() {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return
	}
	if m.rows[m.cursor].file != nil {
		// Nothing shallower than a file row: hop to the previous file
		// instead of leaving the cursor stuck in place.
		m.moveSiblingLevel(-1)
		return
	}
	// A headline row's parent (or file, if top-level) is always its
	// nearest shallower row (jumpToSubtreeTop) — and for a body-line
	// row (level h.Level+1), that's h's own row, which is exactly what
	// "one level up from inside h's body" should mean.
	m.jumpToSubtreeTop()
}

// jumpToSubtreeTop ("^") moves the cursor to the nearest preceding row
// with a shallower level than the current row — the enclosing parent
// headline row in outline view (or the file/section header), and the
// enclosing section header in agenda view — a no-op if already at the
// shallowest level present (a file or section-header row).
//
// This is a row scan rather than a tree-pointer lookup (parent.Level,
// etc.) so it works uniformly across outline and agenda rows: a visible
// row's nearest shallower predecessor is always its logical container,
// since a row is only visible when every ancestor row before it is too.
func (m *Model) jumpToSubtreeTop() {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return
	}
	cur := m.rowLevel(m.cursor)
	for i := m.cursor - 1; i >= 0; i-- {
		if m.rowLevel(i) < cur {
			m.cursor = i
			return
		}
	}
}

// jumpToSubtreeBottom ("$") moves the cursor to the last row exactly one
// level deeper than the current row, within the current row's own span
// (its last direct child in outline view, or its section's last item in
// agenda view) — a no-op if there's no such row. This is the exact
// inverse of jumpToSubtreeTop ("^"): each press moves exactly one level,
// so pressing "$" repeatedly drills progressively deeper, bottoming out
// once it reaches a leaf. See jumpToSubtreeTop for why this is a row
// scan rather than a tree-pointer lookup.
func (m *Model) jumpToSubtreeBottom() {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return
	}
	cur := m.rowLevel(m.cursor)
	last := -1
	for i := m.cursor + 1; i < len(m.rows) && m.rowLevel(i) > cur; i++ {
		// Body lines don't count as a "last child" to land on — an
		// entry with a body but no real children has nothing deeper to
		// drill into, same as a plain leaf.
		if m.rowLevel(i) == cur+1 && !m.rows[i].isBodyLine {
			last = i
		}
	}
	if last >= 0 {
		m.cursor = last
	}
}

// pageSize is a rough, static estimate of how many rows a page holds,
// reserving a full-list worst-case amount of room for section-separator
// blank lines (see sectionSeparatorBudget) alongside the bottom status/
// command-line area (see statusHeight). Used only as a scroll-jump
// size (ctrl-d/ctrl-u/PageUp/PageDown) — close enough for "move roughly
// one screen's worth of rows". The actual visible window (used by
// View(), ensureVisible, and scrollView) is computed precisely instead,
// by contentBudget/visibleRowCount: pageSize's static reservation is
// only ever a lower bound on what really fits (safe for a jump size,
// since jumping a little short of a full screen is harmless), but it
// can be far too conservative when a view has many more section
// boundaries than fit on one page (e.g. :calendar with more days than
// rows available) — most of them live on other pages, so reserving
// room here for every boundary in the whole list would under-fill the
// actual screen.
func (m *Model) pageSize() int {
	n := m.height - m.statusHeight() - m.sectionSeparatorBudget() - m.pinnedHeaderHeight() - m.infoBufferHeight()
	if n < 1 {
		n = 1
	}
	return n
}

// contentBudget is exactly how many terminal lines the scrollable
// content area may occupy: the screen height minus the pinned header,
// the info buffer, and the bottom status/command-line area — with no
// separate reservation for section-separator blank lines, unlike
// pageSize. Used by visibleRowCount to work out precisely how many rows
// fit from a given starting row, and directly as the padding target in
// View().
func (m *Model) contentBudget() int {
	n := m.height - m.statusHeight() - m.pinnedHeaderHeight() - m.infoBufferHeight()
	if n < 1 {
		n = 1
	}
	return n
}

// visibleRowCount returns how many rows, starting at start, actually
// fit within contentBudget — counting the blank separator line View()
// prints before every section-header row after the first one shown (2
// lines total for such a row, 1 for every other row) — so a page always
// shows as many rows as truly fit, rather than reserving room for every
// section boundary in the whole list up front (see pageSize). 0 if
// start is out of range.
func (m *Model) visibleRowCount(start int) int {
	if start < 0 || start >= len(m.rows) {
		return 0
	}
	budget := m.contentBudget()
	used, count := 0, 0
	for i := start; i < len(m.rows); i++ {
		cost := 1
		if i > start && m.rows[i].section != "" {
			cost = 2 // its own line, plus the blank separator before it
		}
		if used+cost > budget {
			break
		}
		used += cost
		count++
	}
	return count
}

// maxRegisterPinnedLines caps how many of the register's entries the
// pinned header (see pinnedHeaderLines) shows at once. A single yank
// only ever queues one entry, but <N>dd and a visual-mode "d" can queue
// dozens of top-level entries at a stroke — showing all of them would
// push the actual outline listing off-screen entirely, so anything past
// this count collapses into one "...and N more" summary line instead.
const maxRegisterPinnedLines = 5

// registerPinnedLineCount is how many lines the register section of the
// pinned header occupies below its own label: one per entry, or
// maxRegisterPinnedLines plus one summary line once there are more than
// that.
func (m *Model) registerPinnedLineCount() int {
	if len(m.register) > maxRegisterPinnedLines {
		return maxRegisterPinnedLines + 1
	}
	return len(m.register)
}

// pinnedHeaderHeight is how many lines the pinned header occupies at the
// top of the screen: the clarify block (label + item-or-empty-message,
// kept a fixed 2 lines so the layout doesn't jump around as the inbox
// empties out) if in clarify view, plus one line per active mark, plus
// the register section (see registerPinnedLineCount) if anything's
// queued for paste, plus one trailing blank separator line if there's
// anything pinned at all — 0 if there's nothing pinned.
func (m *Model) pinnedHeaderHeight() int {
	n := 0
	if m.view == clarifyView {
		n += 2 // "Clarifying:" label + the item/empty-message line
	}
	if len(m.marks) > 0 {
		n += 1 + len(m.marks) // "Active marks:" label + one line per mark
	}
	if len(m.register) > 0 {
		n += 1 + m.registerPinnedLineCount() // "Register:" label + its (bounded) lines
	}
	if n == 0 {
		return 0
	}
	return n + 1
}

// pinnedHeaderLines renders the pinned header fixed to the top of the
// screen: the current clarify target (if in clarify view, rendered
// exactly as it appears in the listing below, or an empty-inbox
// message), then every active mark (sorted by letter, one line each),
// then whatever's queued in the paste register (see registerPinnedLines),
// then a trailing blank separator — or nil if there's nothing pinned.
func (m Model) pinnedHeaderLines() []string {
	var lines []string
	if m.view == clarifyView {
		lines = append(lines, m.padLineToWidth(fileStyle.Background(overlayBg).Render("Clarifying:"), overlayBg))
		if m.clarifyTarget == nil {
			lines = append(lines, m.padLineToWidth(statusStyle.Background(overlayBg).Render("  Inbox is empty."), overlayBg))
		} else {
			lines = append(lines, m.renderPinnedRow(orDefault(m.clarifyIcon, defaultClarifyIcon), m.clarifyTarget, true))
		}
	}
	if letters := m.sortedMarkLetters(); len(letters) > 0 {
		lines = append(lines, m.padLineToWidth(fileStyle.Background(overlayBg).Render("Active marks:"), overlayBg))
		for _, letter := range letters {
			lines = append(lines, m.renderPinnedRow(string(letter), m.marks[letter], false))
		}
	}
	lines = append(lines, m.registerPinnedLines()...)
	if len(lines) == 0 {
		return nil
	}
	// The trailing separator carries the overlay background too, so the
	// tinted block reads as one solid panel rather than cutting off
	// right before an untinted blank line.
	return append(lines, m.padLineToWidth("", overlayBg))
}

// registerPinnedLines renders the register section of the pinned header:
// a "Register:" label, then one row per queued entry (up to
// maxRegisterPinnedLines, so a big <N>dd or visual-mode delete can't push
// the actual outline listing off-screen), then a summary line for
// whatever didn't fit — or nil if the register is empty. Every entry
// shown, whether it came from a delete or a yank, is exactly what p/P
// would paste next.
func (m Model) registerPinnedLines() []string {
	if len(m.register) == 0 {
		return nil
	}
	lines := []string{m.padLineToWidth(fileStyle.Background(overlayBg).Render("Register:"), overlayBg)}
	shown := m.register
	overflow := 0
	if len(shown) > maxRegisterPinnedLines {
		overflow = len(shown) - maxRegisterPinnedLines
		shown = shown[:maxRegisterPinnedLines]
	}
	for _, h := range shown {
		lines = append(lines, m.renderPinnedRow("\"", h, false))
	}
	if overflow > 0 {
		summary := statusStyle.Background(overlayBg).Render(fmt.Sprintf("  ...and %d more", overflow))
		lines = append(lines, m.padLineToWidth(summary, overlayBg))
	}
	return lines
}

// sortedMarkLetters returns the letters of every active mark, sorted —
// for a deterministic display order in pinnedHeaderLines.
func (m Model) sortedMarkLetters() []rune {
	letters := make([]rune, 0, len(m.marks))
	for letter := range m.marks {
		letters = append(letters, letter)
	}
	sort.Slice(letters, func(i, j int) bool { return letters[i] < letters[j] })
	return letters
}

// renderPinnedRow renders one line of the pinned header: marker (the
// clarify target's m.clarifyIcon, or a mark's letter — the register's
// own callers pass a literal quote mark instead) in place of the
// gutter/indent/fold a normal listing row would have, then h's keyword
// and title — the same format regardless of which pinned section it's
// in, and regardless of h's actual level in its file's tree. The marker
// is colored with m.clarifyColor when forClarify, m.markColor otherwise
// (see WithClarifyIcon/WithMarkColor), so it matches whichever gutter
// column (markColumn) it echoes. The whole line carries the overlay
// background, padded to fill the terminal width. forClarify appends h's
// CREATED property (if it has one) and any SCHEDULED/DEADLINE/CLOSED
// planning line (via planningSummary, the same rendering the outline
// view itself uses) — on for the clarify target, where knowing how long
// an item has sat in the inbox and whether it already has a date is
// useful triage context; off for marks, which can point at any headline
// in the outline and aren't about triage.
func (m Model) renderPinnedRow(marker string, h *org.Headline, forClarify bool) string {
	markerColor := orDefault(m.markColor, defaultMarkColor)
	if forClarify {
		markerColor = orDefault(m.clarifyColor, defaultClarifyColor)
	}
	prefix := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(markerColor)).Background(overlayBg).Render(marker) +
		bgSpan(overlayBg, "  ") +
		joinBg(m.renderKeywordAndTitle(h, overlayBg), overlayBg)
	var suffix string
	if forClarify {
		if created := h.Properties["CREATED"]; created != "" {
			suffix += bgSpan(overlayBg, "  ") + timestampStyle.Background(overlayBg).Render("Created: "+created)
		}
		if planning := planningSummary(h); planning != "" {
			suffix += bgSpan(overlayBg, "  ") + timestampStyle.Background(overlayBg).Render(planning)
		}
	}
	return m.padLineToWidth(fitRowLine(prefix, suffix, m.width, overlayBg), overlayBg)
}

// sectionSeparatorBudget is how many blank separator lines a full render
// could need — one before every section-header row after the first (see
// View). Reserving this many rows of the page for them, even though any
// single page may show fewer section boundaries than the full list has,
// is always safe: unused reservation just becomes ordinary bottom
// padding, exactly like when there are fewer than a page of items.
func (m *Model) sectionSeparatorBudget() int {
	n := 0
	for _, r := range m.rows {
		if r.section != "" {
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return n - 1
}

// ensureVisible scrolls so the cursor's whole entry — its own row plus
// any of its own body lines (see entryEnd), the same span View()
// highlights as one unit — fits on screen when possible, not just the
// cursor's own row. If the entry itself is taller than a page, showing
// all of it is impossible either way, so this falls back to keeping at
// least the cursor's own row visible, rather than scrolling past it to
// chase an unreachable tail.
func (m *Model) ensureVisible() {
	// Enforced here, the one chokepoint every key handler in
	// updateNormalMode passes through before returning: the cursor never
	// rests on a body line, regardless of which command moved it — an
	// entry's body is part of the entry, not a separately-landable row,
	// for every command alike (not just j/k).
	m.cursor = m.entryStart(m.cursor)

	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	// Nudge offset forward one row at a time until the cursor's entry
	// end is within the window visibleRowCount(offset) actually shows
	// from there — precise, since (unlike a static pageSize-based jump)
	// it accounts for however many section-separator blank lines really
	// fall within that specific window. Capped at m.cursor: if the
	// entry itself is taller than a page, showing all of it is
	// impossible either way, so this stops advancing once the cursor's
	// own row would be pushed out, rather than scrolling past it to
	// chase an unreachable tail.
	end := m.entryEnd(m.cursor)
	for m.offset < m.cursor && m.offset+m.visibleRowCount(m.offset) <= end {
		m.offset++
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

// entryEnd returns the last row belonging to the same entry as row i —
// i itself, plus any of its own body lines immediately following it.
func (m *Model) entryEnd(i int) int {
	if i < 0 || i >= len(m.rows) {
		return i
	}
	ch := m.rows[i].headline
	end := i
	for end+1 < len(m.rows) && m.rows[end+1].isBodyLine && m.rows[end+1].headline == ch {
		end++
	}
	return end
}

// scrollView shifts the viewport by delta lines (positive scrolls the view
// down, negative scrolls it up) independently of the cursor — Ctrl-E and
// Ctrl-Y, like vim, move the window a single line at a time and leave the
// cursor right where it was, only dragging it along when the scroll would
// otherwise push it off the newly visible window (off the top when
// scrolling down, off the bottom when scrolling up). Callers must skip the
// usual cursor-driven m.ensureVisible() afterward, since that would just
// recompute the offset from the cursor and undo the scroll.
func (m *Model) scrollView(delta int) {
	if len(m.rows) == 0 {
		return
	}
	offset := m.offset + delta
	if offset < 0 {
		offset = 0
	}
	if max := len(m.rows) - 1; offset > max {
		offset = max
	}
	m.offset = offset

	bottom := m.offset + m.visibleRowCount(m.offset) - 1
	if m.cursor < m.offset {
		m.cursor = m.entryStart(m.offset)
	} else if m.entryEnd(m.cursor) > bottom {
		m.cursor = m.entryStart(bottom)
	}
}

func (m Model) View() string {
	var b strings.Builder
	for _, line := range m.pinnedHeaderLines() {
		b.WriteString(line)
		b.WriteString("\n")
	}

	if len(m.rows) == 0 {
		// No items to list (an empty agenda, or a workspace with no org
		// files at all) — still falls through to the status/command-line
		// area below, same as every other case, rather than returning a
		// bare message and skipping it entirely.
		msg := "No org files found."
		if m.view == agendaView {
			msg = fmt.Sprintf("Nothing due in the next %d days. :outline to go back.", m.agendaDays)
		} else if m.view == calendarView {
			msg = "No calendar events found. :outline to go back."
		}
		b.WriteString(msg)
		b.WriteString("\n")
		for i := 1; i < m.contentBudget(); i++ {
			b.WriteString("\n")
		}
	} else {
		start := m.offset
		end := start + m.visibleRowCount(start)

		// An entry's body lines highlight along with it — the whole entry
		// is one item, not a separately-steppable row per line — so
		// extend the highlight from the cursor over any of its own body
		// lines that immediately follow. In visual mode, the rest of the
		// selection (from visualAnchor to the cursor) is also
		// highlighted, but with visualSelectionBg rather than cursorBg —
		// otherwise the whole block looks uniform and there'd be no way
		// to tell which end is actually the cursor (e.g. before extending
		// the selection further, or right after Esc leaves the cursor
		// wherever it was).
		cursorStart, cursorEnd := m.cursor, m.entryEnd(m.cursor)
		selStart, selEnd := cursorStart, cursorEnd
		if m.mode == visualMode {
			selStart, selEnd = m.visualRange()
		}

		sepShown := 0
		for i := start; i < end; i++ {
			if i > start && m.rows[i].section != "" {
				b.WriteString("\n")
				sepShown++
			}
			var line string
			switch {
			case i >= cursorStart && i <= cursorEnd:
				line = m.padLineToWidth(m.renderRowWithBg(m.rows[i], cursorBg), cursorBg)
			case i >= selStart && i <= selEnd:
				line = m.padLineToWidth(m.renderRowWithBg(m.rows[i], visualSelectionBg), visualSelectionBg)
			default:
				line = m.renderRow(m.rows[i])
			}
			b.WriteString(line)
			b.WriteString("\n")
		}
		// Pad with blank lines so the status bar always sits on the last
		// row of the screen — end (via visibleRowCount) already fills as
		// much of contentBudget as the remaining rows allow, so this is
		// only needed once the list itself runs out before the budget
		// does (e.g. the last page of a paginated view).
		for i := (end - start) + sepShown; i < m.contentBudget(); i++ {
			b.WriteString("\n")
		}
	}

	// Info buffer — holds whatever might need more than one line: links
	// and calendar-meeting detail for the current entry (always, in every
	// mode), plus tag/command Tab-completion matches (only while that
	// mode is active). See infoBufferLines. Renders nothing at all (zero
	// height — see infoBufferHeight) when none of that applies, same
	// convention as pinnedHeaderLines above.
	for _, line := range m.infoBufferLines() {
		b.WriteString(line)
		b.WriteString("\n")
	}

	// Status line — vim's own statusline equivalent: always visible
	// regardless of mode, showing where we are (dir/view — item N/M).
	// The command line below it (see the switch that follows) is a
	// separate row for whatever's active right now, mirroring vim's own
	// split between the two rather than the command line ever replacing
	// the status line.
	b.WriteString(m.padLineToWidth(statusStyle.Background(overlayBg).Render(m.normalStatusLine()), overlayBg))
	b.WriteString("\n")

	// Command line — vim's own command-line/message area equivalent:
	// whatever's active right now (a typed command, a prompt, a mode
	// banner, or the last message), blank if there's nothing to show.
	switch {
	case m.mode == commandMode:
		b.WriteString(":" + m.commandInput)
		b.WriteString(cursorStyle.Render(" ")) // caret, right after the input (no in-line editing yet)
		// m.message can be set without leaving commandMode (e.g. Tab
		// completion finding no match) — shown here too, not just in the
		// mode-less case below, or it'd be set but never actually visible.
		// Completion matches themselves are in the info buffer above
		// (see infoBufferLines), not appended here.
		if m.message != "" {
			b.WriteString("  " + errorStyle.Render(m.message))
		}
	case m.mode == selectMode:
		// The candidate list itself is in the info buffer above (see
		// infoBufferLines' "Status:" section), not appended here.
		prefix := " Set status:  "
		if n := len(m.selectModeTargets); n > 0 {
			prefix = fmt.Sprintf(" Set status for %d selected:  ", n)
		}
		b.WriteString(prefix)
		if m.selectFilter != "" {
			b.WriteString("(" + m.selectFilter + ")")
		}
	case m.mode == meetingPickerMode:
		b.WriteString(m.renderMeetingPicker())
	case m.mode == tagMode:
		b.WriteString(" Tag (Tab completes, empty cancels; retyping an existing tag removes it): " + m.tagInput)
		b.WriteString(cursorStyle.Render(" "))
		// Completion matches are in the info buffer above (see
		// infoBufferLines), not appended here.
		if m.message != "" {
			b.WriteString("  " + errorStyle.Render(m.message))
		}
	case m.mode == deadlineMode:
		b.WriteString(" Deadline (YYYY-MM-DD, \"3d\", \"next tue\"; empty clears): " + m.deadlineInput)
		b.WriteString(cursorStyle.Render(" "))
		// As above: an invalid date sets m.message but deliberately leaves
		// the prompt open for correction (see applyDeadlineInput), so it
		// must be shown here rather than only in the mode-less case below.
		if m.message != "" {
			b.WriteString("  " + errorStyle.Render(m.message))
		}
	case m.mode == commitMessageMode:
		b.WriteString(" Commit message: " + m.commitMessageInput)
		b.WriteString(cursorStyle.Render(" "))
		// As above (deadlineMode): an empty message sets m.message but
		// leaves the prompt open for correction (see
		// applyCommitMessageInput), so it must be shown here too.
		if m.message != "" {
			b.WriteString("  " + errorStyle.Render(m.message))
		}
	case m.mode == searchMode:
		prefix := "/"
		if !m.searchForward {
			prefix = "?"
		}
		b.WriteString(prefix + m.searchQuery)
		b.WriteString(cursorStyle.Render(" "))
	case m.mode == confirmMode:
		b.WriteString(errorStyle.Render(m.confirmMessage))
	case m.mode == visualMode:
		b.WriteString(statusStyle.Render(fmt.Sprintf("-- VISUAL LINE -- %d selected  (d: delete, y: yank, R: set status, Esc: cancel)", len(m.visualSelectedHeadlines()))))
		if m.message != "" {
			b.WriteString("  " + errorStyle.Render(m.message))
		}
	case m.message != "":
		b.WriteString(errorStyle.Render(m.message))
	}

	return b.String()
}

// normalStatusLine returns the single line for the default (mode-less)
// status area: "dir — item N/M". Links and calendar-meeting detail for
// the current entry no longer live here — they're always in the info
// buffer above (see infoBufferLines), one section per kind rather than
// squeezed onto this line only when there's room for them.
func (m *Model) normalStatusLine() string {
	place := m.ws.Dir
	switch m.view {
	case agendaView:
		place = "agenda"
	case configView:
		place = "config"
	case logView:
		place = "log"
	case diffView:
		place = "diff"
	case helpView:
		place = "help"
	case calendarView:
		place = "calendar"
	}
	return fmt.Sprintf(" %s  —  item %d/%d", place, m.cursor+1, len(m.rows))
}

// statusHeight is how many lines the bottom area occupies in total: the
// one status line (normalStatusLine) plus exactly one command-line row
// below it for whatever's active right now (a typed command, a search/
// deadline/commit-message prompt, a mode banner, a message, or nothing
// at all). Mirrors vim's own split between its statusline (always
// visible, showing where you are) and the command-line/message area
// below it (always a separate row, regardless of mode) — see View. The
// info buffer (see infoBufferHeight) is accounted for separately, since
// unlike this it can be zero height.
func (m *Model) statusHeight() int {
	return 2
}

// linksInTitle returns the URL of every org-mode link in title, in order.
func linksInTitle(title string) []string {
	matches := orgLinkRe.FindAllStringSubmatch(title, -1)
	if len(matches) == 0 {
		return nil
	}
	urls := make([]string, len(matches))
	for i, mm := range matches {
		urls[i] = mm[1]
	}
	return urls
}

// maxInfoBufferLines caps how many lines any one section of the info
// buffer (see infoBufferLines) shows before collapsing the rest into an
// "...and N more" summary line — same convention as the register's own
// maxRegisterPinnedLines above, just a higher cap: unlike the register
// (routinely a handful of whole entries), a section here is more often
// a handful of one-line items (links, tags, meetings), so it can afford
// to show more before truncating.
const maxInfoBufferLines = 20

// infoBufferHeight is how many lines the info buffer occupies: 0 if
// none of its sections apply, so it doesn't cost a permanent row on
// every screen.
func (m *Model) infoBufferHeight() int {
	return len(m.infoBufferLines())
}

// infoBufferLines renders the info buffer that sits directly above the
// status line — the multi-line counterpart of the single-line status
// area, for whatever might need more than one line to show. Each
// applicable kind gets its own labeled section (already backgrounded/
// padded, ready to write straight to the screen):
//
//   - "Links:" — every org-mode link literally in the current entry's
//     title (linksInTitle). Shown in every mode, not just when it
//     wouldn't otherwise fit on the status line — unlike the old
//     normalStatusLines, this doesn't depend on terminal width at all.
//   - "Meeting:" — one line per calendar meeting the current entry is
//     linked to (see calendarEventEntries), each "<title>  <time>  <url>"
//     (or just "<title>  <url>" if no time could be resolved — see
//     calendarEventEntry.hasWhen).
//   - "Tags:" — while "gt" is prompting for a tag (tagMode) and there's
//     more than one completion match (see completeTagInput), the
//     matches themselves, one per line, instead of the old single
//     space-joined line appended to the prompt.
//   - "Matches:" — the same idea for command-mode ":<Tab>" completions
//     (see completeCommand).
//   - "Status:" — while the "R"/"r" status picker (selectMode) is open,
//     every status candidate (see statusCandidates), one per line, the
//     currently highlighted one in reverse video — the structured
//     counterpart of the old single-line renderStatusSelector.
//   - "Attach meeting:" — while the "gM" picker (meetingPickerMode) is
//     open, every meeting candidate matching the typed filter (see
//     filteredMeetingCandidates), one per line — title, resolved date,
//     and whether it's already attached to the target entry — the
//     currently highlighted one in reverse video (meetingPickerLines).
//     The structured counterpart of the old single-candidate
//     renderMeetingPicker, which only showed the highlighted one.
//
// A section that doesn't apply is simply omitted; nil (zero height) if
// none of them do at all — same "collapses to nothing" convention as
// pinnedHeaderLines above.
func (m *Model) infoBufferLines() []string {
	var lines []string

	if h := m.currentHeadline(); h != nil {
		lines = m.appendInfoSection(lines, "Links:", linksInTitle(h.Title))
		lines = m.appendInfoSection(lines, "Meeting:", m.calendarEventDisplayLines(h))
	}
	if m.mode == tagMode && m.tagCompletions != "" {
		lines = m.appendInfoSection(lines, "Tags:", strings.Fields(m.tagCompletions))
	}
	if m.mode == commandMode && m.commandCompletions != "" {
		lines = m.appendInfoSection(lines, "Matches:", strings.Fields(m.commandCompletions))
	}
	if m.mode == selectMode {
		lines = m.appendInfoSectionRendered(lines, "Status:", m.statusSelectorLines())
	}
	if m.mode == meetingPickerMode {
		lines = m.appendInfoSectionRendered(lines, "Attach meeting:", m.meetingPickerLines())
	}

	if len(lines) == 0 {
		return nil
	}
	// The trailing separator carries the overlay background too, same
	// reason as pinnedHeaderLines' own: the tinted block reads as one
	// solid panel rather than cutting off right before an untinted
	// blank line.
	return append(lines, m.padLineToWidth("", overlayBg))
}

// appendInfoSection appends one info-buffer section to lines: a label
// row, then up to maxInfoBufferLines of items — plain, unstyled text,
// in particular so a URL among them can still be detected/clicked by
// the terminal, same reasoning the old normalStatusLines called out —
// then a "...and N more" summary for anything past that cap. Returns
// lines unchanged if items is empty, so a section with nothing to show
// never contributes a bare label.
func (m Model) appendInfoSection(lines []string, label string, items []string) []string {
	rendered := make([]string, len(items))
	for i, item := range items {
		rendered[i] = statusStyle.Background(overlayBg).Render(" " + item)
	}
	return m.appendInfoSectionRendered(lines, label, rendered)
}

// appendInfoSectionRendered is appendInfoSection's lower-level
// counterpart, for a section whose items need their own styling per
// line (e.g. the "Status:" section's highlighted candidate — see
// statusSelectorLines) rather than the uniform plain-text style
// appendInfoSection applies. items are already fully rendered; this
// only handles the shared label/cap/overflow/padding scaffolding.
func (m Model) appendInfoSectionRendered(lines []string, label string, items []string) []string {
	if len(items) == 0 {
		return lines
	}
	lines = append(lines, m.padLineToWidth(fileStyle.Background(overlayBg).Render(label), overlayBg))
	shown := items
	overflow := 0
	if len(shown) > maxInfoBufferLines {
		overflow = len(shown) - maxInfoBufferLines
		shown = shown[:maxInfoBufferLines]
	}
	for _, item := range shown {
		lines = append(lines, m.padLineToWidth(item, overlayBg))
	}
	if overflow > 0 {
		lines = append(lines, m.padLineToWidth(statusStyle.Background(overlayBg).Render(fmt.Sprintf("  ...and %d more", overflow)), overlayBg))
	}
	return lines
}

// statusSelectorLines renders one line per statusCandidate (the "R"
// status picker's candidates) for the info buffer's "Status:" section —
// the structured, one-per-line counterpart of the old
// renderStatusSelector, which crammed every candidate onto a single
// command-line row. Every candidate is always shown, same as before
// (matches only narrows which one is highlighted, not which are
// listed — see filteredStatusCandidates): each line is "[shortcut]
// label", with the currently highlighted candidate in reverse video.
func (m Model) statusSelectorLines() []string {
	matches := filteredStatusCandidates(m.selectFilter)
	highlighted := -1
	if len(matches) > 0 {
		idx := m.selectIndex
		if idx < 0 {
			idx = 0
		}
		if idx >= len(matches) {
			idx = len(matches) - 1
		}
		highlighted = statusCandidateIndex(matches[idx].keyword)
	}

	lines := make([]string, len(statusCandidates))
	for i, c := range statusCandidates {
		text := fmt.Sprintf("[%c] %s", c.shortcut, c.label)
		if i == highlighted {
			lines[i] = cursorStyle.Render(" " + text)
		} else {
			lines[i] = bgSpan(overlayBg, " ") + shortcutStyle.Background(overlayBg).Render(fmt.Sprintf("[%c]", c.shortcut)) + bgSpan(overlayBg, " "+c.label)
		}
	}
	return lines
}

// meetingPickerLines renders one line per meeting candidate matching the
// "gM" picker's typed filter (see filteredMeetingCandidates) for the
// info buffer's "Attach meeting:" section — the structured, one-per-line
// counterpart of the old renderMeetingPicker, which only ever showed the
// single highlighted candidate on the command line (there was nowhere
// else to put the rest before the info buffer existed). matches is
// already in chronological order (meetingCandidates sorts it that way),
// and each line leads with its date/time — "<date>  <title>" — rather
// than the title, so the times line up in a column and are easy to
// compare down the list; " (attached)" is appended for a candidate
// already on the target entry (see meetingIsAttached — picking it again
// detaches rather than adding a duplicate), and the currently highlighted
// candidate (see meetingPickerDefaultIndex for how that's chosen when the
// picker first opens) renders in reverse video, same convention as
// statusSelectorLines above. nil if the filter matches nothing.
func (m Model) meetingPickerLines() []string {
	matches := filteredMeetingCandidates(m.meetingPickerCandidates, m.meetingPickerFilter)
	if len(matches) == 0 {
		return nil
	}
	idx := m.meetingPickerIndex
	if idx < 0 {
		idx = 0
	}
	if idx >= len(matches) {
		idx = len(matches) - 1
	}

	lines := make([]string, len(matches))
	for i, c := range matches {
		text := c.when.Local().Format("2006-01-02 Mon 15:04") + "  " + c.title
		if meetingIsAttached(m.meetingPickerTarget, c) {
			text += "  (attached)"
		}
		if i == idx {
			lines[i] = cursorStyle.Render(" " + text)
		} else {
			lines[i] = bgSpan(overlayBg, " "+text)
		}
	}
	return lines
}

// formatCalendarEventEntry formats one calendarEventEntry for the info
// buffer's "Meeting:" section: "<title>  <time>  <url>", using the
// meeting picker's own time format (renderMeetingPicker, below) for
// consistency, or just "<title>  <url>" when hasWhen is false.
func formatCalendarEventEntry(e calendarEventEntry) string {
	if e.hasWhen {
		return e.title + "  " + e.when.Local().Format("2006-01-02 Mon 15:04") + "  " + e.url
	}
	return e.title + "  " + e.url
}

// calendarEventDisplayLines formats every one of h's calendarEventEntries
// (see above) for the info buffer's "Meeting:" section.
func (m *Model) calendarEventDisplayLines(h *org.Headline) []string {
	entries := m.calendarEventEntries(h)
	if len(entries) == 0 {
		return nil
	}
	lines := make([]string, len(entries))
	for i, e := range entries {
		lines[i] = formatCalendarEventEntry(e)
	}
	return lines
}

// Default gutter icon characters/colors — used whenever the
// corresponding Model field is unset, which includes both a Model built
// by New() with no matching WithXxxIcon option applied, and a Model
// built as a bare zero value (as plenty of tests do) without going
// through New() at all. See dirtyIcon/dirtyColor and friends, above, and
// orDefault, below.
const (
	defaultDirtyIcon    = "+"
	defaultDirtyColor   = "9"
	defaultMarkColor    = "212"
	defaultClarifyIcon  = "●"
	defaultClarifyColor = "212"
	defaultLockIcon     = "◆"
	defaultLockColor    = "208"
	defaultMeetingIcon  = "▣"
	defaultMeetingColor = "39"
)

// orDefault returns s, or def if s is empty — used to fall back to a
// gutter icon's built-in character/color when the Model field backing it
// was never set (see the defaultXxx constants, above).
func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// gutter renders the leftmost column of a row: a single-character dirty
// marker, always present (blank when clean) so every row lines up the
// same way vim's line-number column does, regardless of indentation. Its
// character and color come from m.dirtyIcon/dirtyColor (default "+",
// "9"; see WithDirtyIcon and the config file's [icons] section). bg is
// the background it's rendered with — lipgloss.NoColor{} normally, or
// the cursor row's highlight (see renderRowWithBg).
func (m Model) gutter(dirty bool, bg lipgloss.TerminalColor) string {
	if dirty {
		icon := orDefault(m.dirtyIcon, defaultDirtyIcon)
		color := orDefault(m.dirtyColor, defaultDirtyColor)
		return lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Background(bg).Render(icon)
	}
	return bgSpan(bg, " ")
}

// markColumn is a headline row's mark/clarify gutter column, in outline
// or agenda view alike — a column of its own, separate from gutter's
// dirty marker, so a row that's both marked (or the clarify target) and
// dirty shows both indicators at once instead of one hiding the other:
// the clarify target's marker (clarify view only, m.clarifyIcon/
// clarifyColor — default "●", "212") takes priority over a mark's
// letter (m.markColor — default "212"; there's no configurable icon for
// a mark, since its glyph is always the letter it was set with), since a
// row can't be both; blank if neither applies. bg is the background it's
// rendered with (see gutter).
func (m Model) markColumn(h *org.Headline, bg lipgloss.TerminalColor) string {
	if m.view == clarifyView && h == m.clarifyTarget {
		icon := orDefault(m.clarifyIcon, defaultClarifyIcon)
		color := orDefault(m.clarifyColor, defaultClarifyColor)
		return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(color)).Background(bg).Render(icon)
	}
	if letter, ok := m.markLetterFor(h); ok {
		color := orDefault(m.markColor, defaultMarkColor)
		return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(color)).Background(bg).Render(string(letter))
	}
	return bgSpan(bg, " ")
}

// lockColumn is a headline row's :format-links gutter column — a column
// of its own (see gutter, markColumn), so it shows up alongside the
// dirty marker and any mark/clarify pin rather than hiding them. Its
// character and color come from m.lockIcon/lockColor (default "◆", the
// U+25C6 BLACK DIAMOND, "208"; see WithLockIcon and the config file's
// [icons] section) while h is locked (see m.immutable), blank otherwise.
// bg is the background it's rendered with (see gutter).
func (m Model) lockColumn(h *org.Headline, bg lipgloss.TerminalColor) string {
	if m.immutable[h] {
		icon := orDefault(m.lockIcon, defaultLockIcon)
		color := orDefault(m.lockColor, defaultLockColor)
		return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(color)).Background(bg).Render(icon)
	}
	return bgSpan(bg, " ")
}

// meetingColumn is a headline row's "linked to a meeting" gutter
// column — a column of its own (see gutter, markColumn, lockColumn),
// so it shows up alongside any of those rather than hiding them. Its
// character and color come from m.meetingIcon/meetingColor (default
// "▣", the U+25A3 WHITE SQUARE CONTAINING BLACK SMALL SQUARE, "39"; see
// WithMeetingIcon and the config file's [icons] section) while h is
// linked to a recurring series or a one-off event, either explicitly —
// attached via "gM" (GCAL_RECURRING_EVENT_IDS or GCAL_EVENT_IDS — see
// meetingCandidate.kind) — or automatically, by sharing a tag with one
// (see tagLinkedMeetingCandidates, e.g. a confirmed attendee's
// "@username" tag) — blank otherwise. This is what lets "is this entry
// linked to some meeting" be answered by looking at the row, rather
// than opening it in $EDITOR to check its property drawer (and, for a
// tag-based link, there's no property to check there anyway — it's
// computed live from the tags, not stored). bg is the background it's
// rendered with (see gutter).
func (m Model) meetingColumn(h *org.Headline, bg lipgloss.TerminalColor) string {
	linked := h.Properties["GCAL_RECURRING_EVENT_IDS"] != "" || h.Properties["GCAL_EVENT_IDS"] != ""
	if !linked {
		linked = len(m.tagLinkedMeetingCandidates(h, time.Now())) > 0
	}
	if linked {
		icon := orDefault(m.meetingIcon, defaultMeetingIcon)
		color := orDefault(m.meetingColor, defaultMeetingColor)
		return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(color)).Background(bg).Render(icon)
	}
	return bgSpan(bg, " ")
}

// fadeIfImmutable applies a faint (dim) rendering attribute to style
// when h is locked by :format-links, on top of whatever foreground
// color style already carries — so a locked entry's keyword, title,
// tags, and timestamp all wash out together, distinguishing it at a
// glance from an ordinary row, without needing a special case for every
// possible keyword color. Unchanged otherwise.
func (m Model) fadeIfImmutable(style lipgloss.Style, h *org.Headline) lipgloss.Style {
	if m.immutable[h] {
		return style.Faint(true)
	}
	return style
}

// renderRow renders r with no highlight — the ordinary case, used for
// every row except the one under the cursor. See renderRowWithBg.
func (m Model) renderRow(r row) string {
	return m.renderRowWithBg(r, lipgloss.NoColor{})
}

// renderRowWithBg renders r with bg as the background behind every
// segment — not just wrapped around the finished string, which doesn't
// work: each segment (keyword, tags, timestamp, ...) already carries its
// own style with its own reset code, and that reset would cancel an
// outer background the moment it fires, leaving the highlight covering
// only the first styled segment instead of the whole line. This is the
// same technique renderPinnedRow uses for the overlay background.
func (m Model) renderRowWithBg(r row, bg lipgloss.TerminalColor) string {
	query := m.activeSearchQuery()
	switch {
	case r.isTextLine:
		// Flush left, unstyled beyond the cursor's own background — a
		// :config row is plain informational text, not a headline. A
		// :help row, though, is glamour-rendered markdown and already
		// carries its own ANSI styling with inner reset codes, which
		// would cut off an outer background partway through the line
		// (the same problem bgSpan exists to solve elsewhere) — so
		// strip it first whenever there's an actual highlight to apply.
		text := r.text
		if _, plain := bg.(lipgloss.NoColor); !plain {
			text = ansi.Strip(text)
		}
		return highlightMatches(text, query, lipgloss.NewStyle().Background(bg))
	case r.section != "":
		// Flush left (no gutter/indent), unlike every item row below it,
		// so a section header stands out at a glance in a long agenda.
		return highlightMatches(r.section, query, fileStyle.Background(bg))
	case r.isMeetingHeader:
		return m.renderMeetingHeaderRowWithBg(r, bg)
	case r.file != nil:
		// Blank mark, lock, and meeting columns: files themselves are
		// never marked, locked by :format-links, or attached to a
		// meeting, but this keeps every row's dirty marker lined up in
		// the same column.
		name := highlightMatches(filepath.Base(r.file.Path), query, fileStyle.Background(bg))
		return bgSpan(bg, " ") + bgSpan(bg, " ") + bgSpan(bg, " ") + m.gutter(m.dirty[r.file], bg) + bgSpan(bg, " ") + name
	case r.isAgendaItem:
		return m.renderAgendaItemRowWithBg(r, bg)
	case r.isCalendarItem:
		return m.renderCalendarItemRowWithBg(r, bg)
	case r.isCalendarLinkedItem:
		return m.renderCalendarLinkedItemRowWithBg(r, bg)
	case r.isBodyLine:
		return m.renderBodyLineWithBg(r, bg)
	}

	h := r.headline
	indent := bgSpan(bg, strings.Repeat("  ", h.Level))

	fold := bgSpan(bg, " ")
	if hasFoldableContent(h) {
		glyph := "▼" // U+25BC BLACK DOWN-POINTING TRIANGLE (full-size; ▾ is a dedicated "small" variant)
		if m.collapsed[h] {
			glyph = "▶" // U+25B6 BLACK RIGHT-POINTING TRIANGLE (full-size; ▸ is a dedicated "small" variant)
		}
		fold = bgSpan(bg, glyph)
	}

	prefix := m.markColumn(h, bg) + m.lockColumn(h, bg) + m.meetingColumn(h, bg) + m.gutter(m.dirtyHeadlines[h], bg) + bgSpan(bg, " ") + indent + fold + bgSpan(bg, " ") + joinBg(m.renderKeywordAndTitle(h, bg), bg)

	var suffix string
	if len(h.Tags) > 0 {
		suffix += bgSpan(bg, "  ") + highlightMatches(":"+strings.Join(h.Tags, ":")+":", query, m.fadeIfImmutable(tagStyle, h).Background(bg))
	}

	if ts := planningSummary(h); ts != "" {
		suffix += bgSpan(bg, "  ") + m.fadeIfImmutable(timestampStyle, h).Background(bg).Render(ts)
	}

	return fitRowLine(prefix, suffix, m.width, bg)
}

// renderKeywordAndTitle renders h's keyword, priority, and title (with
// its links shown as display text, see renderTitleForDisplay) as
// space-joinable parts — shared between the outline, agenda, and pinned
// row renderers. bg is the background every part is rendered with —
// lipgloss.NoColor{} outside the pinned header, where nothing is
// tinted.
func (m Model) renderKeywordAndTitle(h *org.Headline, bg lipgloss.TerminalColor) []string {
	query := m.activeSearchQuery()
	var parts []string
	if h.Keyword != "" {
		style, ok := keywordStyles[h.Keyword]
		if !ok {
			style = lipgloss.NewStyle()
		}
		style = m.fadeIfImmutable(style, h)
		parts = append(parts, highlightMatches(h.Keyword, query, style.Background(bg)))
	}
	if h.Priority != "" {
		style := m.fadeIfImmutable(lipgloss.NewStyle(), h)
		parts = append(parts, highlightMatches(fmt.Sprintf("[#%s]", h.Priority), query, style.Background(bg)))
	}

	base := lipgloss.NewStyle()
	if org.IsDoneKeyword(h.Keyword) {
		base = doneTitleStyle
	}
	base = m.fadeIfImmutable(base, h).Background(bg)
	parts = append(parts, renderTitleForDisplay(h.Title, base, query))
	return parts
}

// renderAgendaItemRow renders one agenda item row: keyword/priority/
// title (as in outline, but with no indent or fold arrow — agenda is
// flat), tags, then which file (and, for a sub-headline, its immediate
// parent — see agendaPlace) it's from and the date/label (Scheduled or
// Deadline) it's shown for.
//
// The parent-title portion of that "[...]" tag is left full-width
// unless the row doesn't already fit. When it doesn't, the row's own
// title first gets its full natural width reserved (so a short title
// is never truncated just because the parent tag is long), and
// whatever's left over goes to the parent tag — maximizing how much of
// each shows given the other. Only once the parent tag has been shrunk
// to nothing and the row still doesn't fit does the row's own title
// give way too (see fitRowLine), exactly like any other row.
func (m Model) renderAgendaItemRowWithBg(r row, bg lipgloss.TerminalColor) string {
	h := r.headline
	prefix := m.markColumn(h, bg) + m.lockColumn(h, bg) + m.meetingColumn(h, bg) + m.gutter(m.dirtyHeadlines[h], bg) + bgSpan(bg, " ") + joinBg(m.renderKeywordAndTitle(h, bg), bg)

	var tags string
	if len(h.Tags) > 0 {
		tags = bgSpan(bg, "  ") + highlightMatches(":"+strings.Join(h.Tags, ":")+":", m.activeSearchQuery(), m.fadeIfImmutable(tagStyle, h).Background(bg))
	}

	timestamp := m.fadeIfImmutable(timestampStyle, h).Background(bg)
	dateSuffix := func(place string) string {
		if r.agendaLabel == "" {
			// A Next Actions entry: no date to show, just where it's from.
			return bgSpan(bg, "  ") + timestamp.Render(fmt.Sprintf("[%s]", place))
		}
		label := r.agendaLabel
		if r.agendaMissed > 0 {
			label = fmt.Sprintf("%s (%dx)", label, r.agendaMissed)
		}
		date := r.agendaDate.Format("2006-01-02 Mon")
		if r.agendaRepeater != "" {
			date += " " + r.agendaRepeater
		}
		return bgSpan(bg, "  ") + timestamp.Render(fmt.Sprintf("[%s]  %s: %s", place, label, date))
	}

	suffix := tags + dateSuffix(m.agendaPlace(h, -1))
	if m.width > 0 && lipgloss.Width(prefix)+lipgloss.Width(suffix) > m.width {
		overhead := lipgloss.Width(tags) + lipgloss.Width(dateSuffix(m.agendaPlace(h, 0)))
		parentBudget := m.width - lipgloss.Width(prefix) - overhead
		if parentBudget < 0 {
			parentBudget = 0
		}
		suffix = tags + dateSuffix(m.agendaPlace(h, parentBudget))
	}

	return fitRowLine(prefix, suffix, m.width, bg)
}

// agendaPlace returns the "[...]" tag content shown on an agenda row for
// h: the file it's from, plus — since an agenda item shown out of its
// outline context often isn't identifiable from its own title alone —
// its immediate parent headline's title, if it has one (a top-level
// headline has no parent to add). Only the direct parent is shown, not
// the full ancestor chain up to the root: that's usually enough to place
// the item, and stays short even for an item buried deep in the tree —
// the full chain remains one Enter (jumpToSource) away.
//
// parentMaxWidth caps how much of the parent title is included
// (ellipsized with ansi.Truncate past that); a negative value leaves it
// full-width. See renderAgendaItemRowWithBg, the only caller, for how
// that cap is chosen.
func (m Model) agendaPlace(h *org.Headline, parentMaxWidth int) string {
	fileName := ""
	if f := m.fileForHeadline(h); f != nil {
		fileName = filepath.Base(f.Path)
	}
	if h.Parent == nil {
		return fileName
	}
	parentTitle := h.Parent.Title
	if parentMaxWidth >= 0 {
		parentTitle = ansi.Truncate(parentTitle, parentMaxWidth, "…")
	}
	return fmt.Sprintf("%s › %s", fileName, parentTitle)
}

// renderMeetingHeaderRowWithBg renders a meeting-group header row in the
// agenda's "Meetings" section: the meeting's title, bold like a section
// header (see fileStyle) so it still reads as a header despite being
// indented one level under the section row, followed by its date/time.
func (m Model) renderMeetingHeaderRowWithBg(r row, bg lipgloss.TerminalColor) string {
	query := m.activeSearchQuery()
	title := highlightMatches(r.meetingTitle, query, fileStyle.Background(bg))
	when := highlightMatches(formatMeetingWhen(r.meetingStart, r.meetingEnd), query, timestampStyle.Background(bg))
	return fitRowLine(bgSpan(bg, "  ")+title, bgSpan(bg, "  ")+when, m.width, bg)
}

// formatMeetingWhen renders a meeting's start/end for
// renderMeetingHeaderRowWithBg, in local time — omitting the time of day
// entirely for an all-day event, recognized here by both endpoints
// sitting at local midnight (see eventBounds in internal/calendarsync/convert.go,
// which anchors an all-day event's GCAL_START/GCAL_END there).
func formatMeetingWhen(start, end time.Time) string {
	start, end = start.Local(), end.Local()
	if isMidnight(start) && isMidnight(end) && !start.Equal(end) {
		return start.Format("2006-01-02 Mon")
	}
	if sameLocalDay(start, end) {
		return fmt.Sprintf("%s %s-%s", start.Format("2006-01-02 Mon"), start.Format("15:04"), end.Format("15:04"))
	}
	return fmt.Sprintf("%s %s — %s %s", start.Format("2006-01-02 Mon"), start.Format("15:04"), end.Format("2006-01-02 Mon"), end.Format("15:04"))
}

func isMidnight(t time.Time) bool {
	return t.Hour() == 0 && t.Minute() == 0
}

// renderCalendarItemRowWithBg renders one calendarView event row: mark/
// lock/gutter/fold columns exactly as the outline's own default
// headline-row case (see the bottom of renderRowWithBg), but with its
// GCAL_START/GCAL_END time (see calendarItemTime) shown before the
// title in place of a TODO keyword — the time is what's worth seeing at
// a glance here, and a calendar event never has a keyword anyway.
func (m Model) renderCalendarItemRowWithBg(r row, bg lipgloss.TerminalColor) string {
	h := r.headline
	query := m.activeSearchQuery()
	indent := bgSpan(bg, strings.Repeat("  ", h.Level))

	fold := bgSpan(bg, " ")
	if hasFoldableContent(h) {
		glyph := "▼"
		if m.collapsed[h] {
			glyph = "▶"
		}
		fold = bgSpan(bg, glyph)
	}

	prefix := m.markColumn(h, bg) + m.lockColumn(h, bg) + m.meetingColumn(h, bg) + m.gutter(m.dirtyHeadlines[h], bg) + bgSpan(bg, " ") + indent + fold + bgSpan(bg, " ")
	if when := calendarItemTime(h); when != "" {
		prefix += m.fadeIfImmutable(timestampStyle, h).Background(bg).Render(when) + bgSpan(bg, "  ")
	}
	prefix += joinBg(m.renderKeywordAndTitle(h, bg), bg)

	var suffix string
	if len(h.Tags) > 0 {
		suffix = bgSpan(bg, "  ") + highlightMatches(":"+strings.Join(h.Tags, ":")+":", query, m.fadeIfImmutable(tagStyle, h).Background(bg))
	}
	return fitRowLine(prefix, suffix, m.width, bg)
}

// renderCalendarLinkedItemRowWithBg renders one item linked to a
// calendar event — attached via "gM", or sharing a tag with it (see
// linkedMeetingItems/appendCalendarHeadlines):
// mark/lock/meeting/dirty gutter and keyword/title exactly as an
// ordinary headline row, but indented to r.level (one level deeper than
// the event's own indent — see renderCalendarItemRowWithBg) rather than
// the headline's own real level in whatever file it actually lives in,
// so it visibly nests under the event regardless of how deep it sits in
// its own outline. A blank fold column, like a body line's (see
// renderBodyLineWithBg) — this row doesn't support folding its own
// children/body. Tagged with "[file › parent]" (see agendaPlace), same
// as an agenda item, since the entry lives elsewhere in the workspace
// and wouldn't otherwise be placeable from its title alone.
func (m Model) renderCalendarLinkedItemRowWithBg(r row, bg lipgloss.TerminalColor) string {
	h := r.headline
	query := m.activeSearchQuery()
	indent := bgSpan(bg, strings.Repeat("  ", r.level))

	prefix := m.markColumn(h, bg) + m.lockColumn(h, bg) + m.meetingColumn(h, bg) + m.gutter(m.dirtyHeadlines[h], bg) + bgSpan(bg, " ") + indent + bgSpan(bg, " ") + bgSpan(bg, " ") + joinBg(m.renderKeywordAndTitle(h, bg), bg)

	var suffix string
	if len(h.Tags) > 0 {
		suffix += bgSpan(bg, "  ") + highlightMatches(":"+strings.Join(h.Tags, ":")+":", query, m.fadeIfImmutable(tagStyle, h).Background(bg))
	}
	suffix += bgSpan(bg, "  ") + m.fadeIfImmutable(timestampStyle, h).Background(bg).Render(fmt.Sprintf("[%s]", m.agendaPlace(h, -1)))

	return fitRowLine(prefix, suffix, m.width, bg)
}

// calendarItemTime renders h's GCAL_START/GCAL_END as just the time of
// day, with no date — calendarView already groups h under its own
// day's header row (see appendCalendarRows), so the date would be
// redundant here. "All day" for an all-day event (both endpoints at
// local midnight — see eventBounds in internal/calendarsync/convert.go); empty
// if GCAL_START/GCAL_END don't parse (e.g. h isn't actually a synced
// calendar event).
func calendarItemTime(h *org.Headline) string {
	start, startOK := parseRFC3339Property(h, "GCAL_START")
	end, endOK := parseRFC3339Property(h, "GCAL_END")
	if !startOK || !endOK {
		return ""
	}
	start, end = start.Local(), end.Local()
	if isMidnight(start) && isMidnight(end) && !start.Equal(end) {
		return "All day"
	}
	return start.Format("15:04") + "-" + end.Format("15:04")
}

func sameLocalDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

// renderBodyLineWithBg renders one line of a headline's free-text body,
// indented to line up where a child's own content would start (blank
// mark/gutter/fold columns, since a body line isn't itself a separately
// addressable item — that state lives on the headline's own row), shown
// in a muted style so it doesn't compete visually with real entries.
func (m Model) renderBodyLineWithBg(r row, bg lipgloss.TerminalColor) string {
	indent := strings.Repeat("  ", r.level)
	blanks := bgSpan(bg, "     "+indent+"  ") // mark + lock + meeting + dirty gutter + space, then indent, then fold + space
	style := m.fadeIfImmutable(bodyStyle, r.headline).Background(bg)
	return blanks + highlightMatches(strings.TrimSpace(r.bodyText), m.activeSearchQuery(), style)
}

// renderMeetingPicker renders the "gM" picker's command-line prompt: how
// many candidates match the typed filter (or that none do), and the
// filter text itself. The candidate list itself — title, resolved date,
// and whether each is already attached to the target entry — lives in
// the info buffer's "Attach meeting:" section (see meetingPickerLines,
// above), the same split selectMode's own "R" status picker uses for its
// candidate list (statusSelectorLines) rather than crowding it onto this
// single command-line row.
func (m Model) renderMeetingPicker() string {
	matches := filteredMeetingCandidates(m.meetingPickerCandidates, m.meetingPickerFilter)
	line := " Attach meeting: no matches"
	if len(matches) > 0 {
		idx := m.meetingPickerIndex
		if idx < 0 {
			idx = 0
		}
		if idx >= len(matches) {
			idx = len(matches) - 1
		}
		line = fmt.Sprintf(" Attach meeting (%d/%d, Enter toggles attach)", idx+1, len(matches))
	}
	if m.meetingPickerFilter != "" {
		line += "   (" + m.meetingPickerFilter + ")"
	}
	return line
}

func planningSummary(h *org.Headline) string {
	var parts []string
	if h.Scheduled != nil {
		parts = append(parts, "SCHEDULED: "+h.Scheduled.String())
	}
	if h.Deadline != nil {
		parts = append(parts, "DEADLINE: "+h.Deadline.String())
	}
	if h.Closed != nil {
		parts = append(parts, "CLOSED: "+h.Closed.String())
	}
	return strings.Join(parts, "  ")
}
