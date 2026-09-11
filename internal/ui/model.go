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
	"github.com/charmbracelet/lipgloss"
	naturaldate "github.com/tj/go-naturaldate"

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
	pinMarkerStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	lockedStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("208"))
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

// statusCycle is the order r rotates through.
var statusCycle = []string{"", "TODO", "NEXT", "WAITING", "SOMEDAY", "DONE", "CANCELLED"}

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

	text string // set for a plain read-only informational row (:config view); rendered flush left, never interactive
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
	pendingCount int  // numeric prefix built up so far for "dd"/"R" (e.g. "3dd", "2R"); 0 means none typed

	register *org.Headline // last deleted (dd) or yanked (yy) entry, pasted (as a copy) by p/P

	marks map[rune]*org.Headline // vim-style marks (letter -> headline), set by "m<letter>", jumped to by "'<letter>"; each stays pinned to the top of the screen (see pinnedHeaderLines) until cleared

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

	selectFilter string // typed so far, in selectMode
	selectIndex  int    // highlighted index within the filtered candidates, in selectMode

	deadlineInput string // typed so far, in deadlineMode

	searchQuery   string // typed so far, in searchMode
	searchForward bool   // true for "/" (forward), false for "?" (backward)
	searchOrigin  int    // cursor position when the search started, restored on Esc

	lastSearchQuery   string // most recently confirmed search, repeated by n/N
	lastSearchForward bool   // that search's direction ("n" repeats it, "N" reverses it)

	confirmMessage  string    // prompt shown in confirmMode
	pendingFileEdit *org.File // the file to open in $EDITOR if confirmMode's prompt is accepted ("y")

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

	hideDoneAfterHours int  // how many hours after CLOSED a DONE/CANCELLED item disappears from the outline; see WithHideDoneAfterHours
	hideDoneEnabled    bool // whether hideDoneAfterHours filtering is active; off by default (see New), toggled by :toggledone, turned on at startup by WithHideDoneAfterHours

	debug bool // whether main.go turned on debug logging (see WithDebug); the Model itself never logs anything based on this — it's only carried here so :config can report it
}

// viewKind selects what rebuildRows populates m.rows with.
type viewKind int

const (
	outlineView viewKind = iota
	agendaView
	clarifyView
	configView
	logView
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
		hideDoneAfterHours: 24,
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
	default:
		for _, f := range m.ws.Files {
			m.rows = append(m.rows, row{file: f})
			m.appendHeadlines(f.Headlines)
		}
	}
	if m.cursor >= len(m.rows) {
		m.cursor = len(m.rows) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

// jumpToSource ("Enter" on an agenda row) switches to outline view with
// the cursor on that row's real headline. A no-op outside agenda view,
// or on an agenda section-header row.
func (m *Model) jumpToSource() {
	if m.view != agendaView {
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
	if !m.rowsContainHeadline(h) {
		m.switchToView(outlineView)
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
// resetting the cursor to the top — the two views have entirely
// different row sets, so there's no sensible position to preserve.
func (m *Model) switchToView(v viewKind) {
	m.view = v
	m.cursor = 0
	m.offset = 0
	m.rebuildRows()
}

// appendConfigRows populates m.rows for config view: one read-only line
// per configurable setting, showing its effective current value (after
// flags/config-file/built-in-default resolution has already happened in
// main.go — this view has no idea which of those a value came from,
// only what it ended up as).
func (m *Model) appendConfigRows() {
	line := func(format string, args ...any) {
		m.rows = append(m.rows, row{text: fmt.Sprintf(format, args...)})
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
	line("Hide done after: %d hours (currently %s — :toggledone to switch)", m.hideDoneAfterHours, onOff(m.hideDoneEnabled))
	line("Debug logging: %s", onOff(m.debug))
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
		m.rows = append(m.rows, row{text: "No external commands have been run yet."})
		return
	}
	for _, e := range entries {
		m.rows = append(m.rows, row{text: fmt.Sprintf("%s  %-6s  pid %-7s  %s", e.time.Format("15:04:05.000"), e.kind.label(), pidLabel(e.pid), e.text)})
	}
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

func (m *Model) appendHeadlines(headlines []*org.Headline) {
	for _, h := range headlines {
		if m.hiddenAsStaleDone(h) {
			continue
		}
		m.rows = append(m.rows, row{headline: h, level: h.Level})
		if !m.collapsed[h] {
			m.appendBodyLines(h)
			if len(h.Children) > 0 {
				m.appendHeadlines(h.Children)
			}
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
func (m *Model) appendBodyLines(h *org.Headline) {
	for _, line := range visibleBodyLines(h) {
		m.rows = append(m.rows, row{headline: h, level: h.Level + 1, isBodyLine: true, bodyText: line})
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
		return m, nil

	case editFinishedMsg:
		return m.finishEdit(msg)

	case fileEditFinishedMsg:
		return m.finishEditFile(msg)

	case formatLinksMsg:
		return m.finishFormatLinks(msg)

	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}

		switch m.mode {
		case commandMode:
			return m.updateCommandMode(msg)
		case selectMode:
			return m.updateSelectMode(msg)
		case deadlineMode:
			return m.updateDeadlineMode(msg)
		case searchMode:
			return m.updateSearchMode(msg)
		case confirmMode:
			return m.updateConfirmMode(msg)
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

	// A digit builds up a numeric prefix for "dd"/"R" (e.g. "3dd", "2R")
	// instead of being handled by the switch below — intercepted here for
	// the same reason as m/' above. A leading zero (no digits typed yet)
	// is not a valid count on its own — there's no "0" command to
	// distinguish it from — so it falls through as a plain, currently
	// unbound key instead of starting a count. Any other key that isn't
	// "d" or "R" themselves clears a pending count rather than silently
	// applying to some other command later — the prefix is scoped to
	// exactly these two.
	if len(key) == 1 && key[0] >= '0' && key[0] <= '9' {
		if d := int(key[0] - '0'); d > 0 || m.pendingCount > 0 {
			m.pendingCount = m.pendingCount*10 + d
		}
		m.ensureVisible()
		return m, nil
	}
	if key != "d" && key != "R" {
		m.pendingCount = 0
	}

	switch key {
	case ":":
		m.mode = commandMode
		m.commandInput = ""
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
			m.cursor = 0
		} else {
			m.pendingG = true
		}

	case "G":
		if n := len(m.rows); n > 0 {
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

	case "r":
		m.rotateStatus()

	case "R":
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
		if cmd := m.startEdit(); cmd != nil {
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
	}

	m.ensureVisible()
	return m, nil
}

// updateVisualMode handles "V" (visual line selection): plain navigation
// keys extend the selection (from visualAnchor to the cursor, snapped to
// whole entries — see visualRange) exactly as they move the cursor in
// normal mode, while "d" and "R" act on every entry currently selected.
// Only a subset of normal mode's keys apply here — anything that isn't
// navigation or one of the two bulk operations (editing a single entry,
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

	case "d":
		m.deleteVisualSelection()

	case "R":
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
// n — giving "dd"/"R" a numeric prefix (e.g. "3dd", "2R") the same
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
// tracking is inherently per-file. Unlike dd, this doesn't populate the
// paste register — there's no single entry to put there, and p/P only
// ever pastes one. Any headline locked by :format-links is silently
// excluded first (see filterImmutable) rather than aborting the whole
// operation; the summary message notes how many, if any.
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
	m.mode = normalMode
	m.confirmMessage = ""
	m.pendingFileEdit = nil

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
// searchOrigin if the query is empty or matches nothing.
func (m *Model) performIncrementalSearch() {
	m.cursor = m.searchOrigin
	if m.searchQuery != "" {
		if idx, ok := m.findMatch(m.searchOrigin, m.searchQuery, m.searchForward); ok {
			m.cursor = idx
		}
	}
	m.ensureVisible()
}

// repeatSearch ("n"/"N") repeats the last confirmed search from the
// current cursor position, in the given direction.
func (m *Model) repeatSearch(forward bool) {
	if m.lastSearchQuery == "" {
		return
	}
	if idx, ok := m.findMatch(m.cursor, m.lastSearchQuery, forward); ok {
		m.cursor = idx
	}
}

// findMatch searches m.rows for the nearest row — excluding start
// itself — whose searchable text (see rowSearchText) contains query,
// case-insensitively, moving forward or backward from start and
// wrapping around the ends (vim's default 'wrapscan' behavior).
func (m *Model) findMatch(start int, query string, forward bool) (int, bool) {
	n := len(m.rows)
	if n == 0 {
		return 0, false
	}
	q := strings.ToLower(query)
	step := 1
	if !forward {
		step = -1
	}
	for i := 1; i <= n; i++ {
		idx := ((start+step*i)%n + n) % n
		if strings.Contains(strings.ToLower(rowSearchText(m.rows[idx])), q) {
			return idx, true
		}
	}
	return 0, false
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
	}

	return m, nil
}

// runCommand executes the typed command line and always returns to
// normal mode.
// commandNames lists every command-mode word tab completion knows
// about. Both short and long forms of the same command (e.g. "q" and
// "quit") are listed individually, since either is something you might
// type and want completed.
var commandNames = []string{
	"w", "write", "wq", "q", "quit", "q!", "quit!",
	"undo", "redo", "agenda", "clarify", "outline", "config", "capture",
	"delmarks", "delmarks!", "noh", "nohlsearch", "toggledone", "next", "prev", "format-links", "log",
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

	case "capture":
		return m, m.startCapture()

	case "delmarks":
		m.message = "Usage: :delmarks <letters> or :delmarks!"

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

	case "log":
		m.switchToView(logView)

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
// natural-language phrase ("next tuesday", "tomorrow", "friday"), via
// go-naturaldate. Only the first form can produce a time of day — the
// other two always resolve to a plain date, since "in 3 days" or "next
// tuesday" don't imply a specific hour.
func resolveDeadlineDate(input string) (t time.Time, hasTime bool, err error) {
	input = strings.TrimSpace(input)

	if t, hasTime, err := parseFlexibleDate(input); err == nil {
		return t, hasTime, nil
	}

	today := truncateToDate(time.Now())

	if t, ok := parseRelativeOffset(input, today); ok {
		return t, false, nil
	}

	if t, ferr := naturaldate.Parse(input, today, naturaldate.WithDirection(naturaldate.Future)); ferr == nil {
		return truncateToDate(t), false, nil
	}

	return time.Time{}, false, fmt.Errorf(`invalid date %q (try "2026-12-25", "3d", "2 weeks", or "next tuesday")`, input)
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

// rotateStatus advances the current headline to the next state in
// statusCycle, wrapping around.
func (m *Model) rotateStatus() {
	h := m.currentHeadline()
	if h == nil {
		return
	}
	idx := 0
	for i, k := range statusCycle {
		if k == h.Keyword {
			idx = i
			break
		}
	}
	m.applyStatus(statusCycle[(idx+1)%len(statusCycle)])
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
	// cursorAtEntryStart ("i", "o"/"O") puts the cursor right after the
	// bullet — e.g. column 3 for a level-1 headline ("* " is 2
	// characters) — in insert mode, so typing immediately inserts text
	// there exactly as pressing vim's own "i" at that spot would. For
	// o/O the entry is a blank template, so this is also where its
	// title will end up starting.
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

// resolveCursorPlacement returns the placement and (1-based) column
// launchEditor should actually request for h, downgrading
// cursorAtEntryStart to cursorAtLineEnd when nothing follows the bullet
// yet (a blank o/O template): vim's cursor()+startinsert needs the
// target column to be an existing character — cursor() clamps to the
// line's last real character rather than allowing a column one past the
// end — so requesting the bullet's very next column on a line that ends
// exactly there would land one character too early, ahead of the
// bullet's own trailing space instead of after it. cursorAtLineEnd's
// startinsert! (append) sidesteps this entirely: with nothing after the
// bullet, "end of line" and "right after the bullet" are the exact same
// position anyway. col is meaningless for any other placement.
func resolveCursorPlacement(h *org.Headline, placement editorCursorPlacement) (editorCursorPlacement, int) {
	col := h.Level + 2
	if placement == cursorAtEntryStart {
		firstLine, _, _ := strings.Cut(org.RenderHeadline(h), "\n")
		if col > len(firstLine) {
			placement = cursorAtLineEnd
		}
	}
	return placement, col
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
// session or a plain `i`/`A` edit. placement (see editorCursorPlacement)
// controls where a vim-family editor lands the cursor and whether it
// starts in insert mode already. Returns nil if the temp file couldn't
// be created or the editor couldn't be started, in which case the error
// is left in m.message.
func (m *Model) launchEditor(h *org.Headline, ctx *insertContext, placement editorCursorPlacement) tea.Cmd {
	tmp, err := os.CreateTemp("", "orgtd-edit-*.org")
	if err != nil {
		m.message = fmt.Sprintf("Could not create temp file: %v", err)
		return nil
	}
	path := tmp.Name()

	before, after := m.editorContext(ctx != nil, h)
	content := before + org.RenderHeadline(h) + after
	_, err = tmp.WriteString(content)
	tmp.Close()
	if err != nil {
		os.Remove(path)
		m.message = fmt.Sprintf("Could not write temp file: %v", err)
		return nil
	}

	placement, col := resolveCursorPlacement(h, placement)
	editorCmd := buildEditorCommand(m.editorCommand(), path, before, placement, col)

	return tea.ExecProcess(editorCmd, func(err error) tea.Msg {
		return editFinishedMsg{path: path, target: h, insert: ctx, cmd: editorCmd, err: err}
	})
}

// editorContextComment builds a git-commit-style trailer appended after
// the real content in the editor buffer: instructions, plus the entries
// immediately before and after the one being edited, for orientation.
// Every line is an org comment ("# ..."), so it's inert either way —
// finishEdit strips comment lines before parsing the result, so this
// trailer never ends up as part of the saved content whether the user
// deletes it or leaves it in place.
// editorContext builds the git-commit-style trailer split around the
// real content: before is prepended, after is appended, so the buffer
// reads like a small outline with the real entry sitting in place among
// its actual structural neighbors — its parent (if nested) and its
// previous/next sibling — each rendered with real org stars matching its
// own level, commented out. finishEdit strips comment lines before
// parsing the result, so this context is inert whether the user deletes
// it or leaves it in place.
func (m *Model) editorContext(isInsert bool, h *org.Headline) (before, after string) {
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
	before = b.String()

	b.Reset()
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
	after = b.String()
	return before, after
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
	list := f.Headlines
	if parent != nil {
		list = parent.Children
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
	return m.insertHeadlineAt(f, parent, idx, level, origin, nil)
}

// startCapture (:capture, "gC") appends a blank top-level headline to
// the end of the inbox file and opens it in $EDITOR — a dedicated
// quick-add path, distinct from o/O, that always targets the inbox
// regardless of the current cursor position or view (agenda, clarify,
// or scrolled to some other file entirely in outline). A no-op (with a
// status message) if the inbox file isn't loaded.
func (m *Model) startCapture() tea.Cmd {
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
	return m.insertHeadlineAt(f, nil, len(f.Headlines), 1, m.currentHeadline(), m.currentRowFile())
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
// set to now —
// org-mode's standard (if not automatic) convention for recording an
// entry's creation time, e.g. via org-capture's %U escape. It's part of
// the editable template, not stamped after the fact, so it's just as
// overridable or deletable as anything else the user types before
// saving. origin (and originFile, its fallback when origin is nil) is
// refocused if the session is rolled back — see rollbackInsert. The
// insert isn't recorded in undo history until the editor session
// finishes successfully (see commitInsert), so the whole "open a
// headline, type into it" session is one undo step.
func (m *Model) insertHeadlineAt(f *org.File, parent *org.Headline, idx, level int, origin *org.Headline, originFile *org.File) tea.Cmd {
	tentative := &org.Headline{Level: level, Parent: parent}
	tentative.SetProperty("CREATED", "["+time.Now().Format("2006-01-02 Mon 15:04")+"]")
	if parent != nil {
		parent.Children = spliceHeadlines(parent.Children, idx, 0, []*org.Headline{tentative})
	} else {
		f.Headlines = spliceHeadlines(f.Headlines, idx, 0, []*org.Headline{tentative})
	}
	m.rebuildRows()
	m.focusHeadline(tentative)

	ctx := insertContext{f: f, parent: parent, index: idx, origin: origin, originFile: originFile}
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
func (m *Model) deleteHeadline() {
	h := m.currentHeadline()
	if h == nil || m.refuseIfImmutable(h) {
		return
	}
	f, parent, idx := m.insertPosition(h)
	if idx < 0 {
		return
	}
	m.register = h
	m.pushUndo(&deleteAction{spliceAction{f: f, parent: parent, index: idx, headlines: []*org.Headline{h}, inTree: true}})

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
	m.register = org.CloneHeadline(h)
	m.message = "Yanked"
}

// pasteHeadline inserts a copy of the register's contents after
// (before=false, "p") or before (before=true, "P") the current row (see
// resolveInsertPosition), adjusting its level (and its descendants', by
// the same amount) to fit the destination depth. The register itself is
// left untouched, so it can be pasted again.
func (m *Model) pasteHeadline(before bool) {
	if m.register == nil {
		m.message = "Nothing to paste"
		return
	}
	f, parent, idx, level, _, ok := m.resolveInsertPosition(before)
	if !ok {
		return
	}

	clone := org.CloneHeadline(m.register)
	shiftHeadlineLevel(clone, level-clone.Level)
	m.pushUndo(&insertAction{spliceAction{f: f, parent: parent, index: idx, headlines: []*org.Headline{clone}}})
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
	list := f.Headlines
	if parent != nil {
		list = parent.Children
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
// `i` edit, the result replaces the original headline in place (or, if
// the edit left nothing parseable, the original is left untouched). For
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

	text := m.formatURLs(stripCommentLines(string(data)))

	file, err := org.Parse(strings.NewReader(text), "")
	if err != nil {
		m.message = fmt.Sprintf("Could not parse edited entry: %v", err)
		if msg.insert != nil {
			m.rollbackInsert(*msg.insert, msg.target)
		}
		return m, nil
	}

	if len(file.Headlines) == 0 {
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
		return m, nil
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

// pageSize is the number of rows visible at once, reserving one line for
// the status bar.
func (m *Model) pageSize() int {
	n := m.height - m.statusHeight() - m.sectionSeparatorBudget() - m.pinnedHeaderHeight()
	if n < 1 {
		n = 1
	}
	return n
}

// pinnedHeaderHeight is how many lines the pinned header occupies at the
// top of the screen: the clarify block (label + item-or-empty-message,
// kept a fixed 2 lines so the layout doesn't jump around as the inbox
// empties out) if in clarify view, plus one line per active mark, plus
// one trailing blank separator line if there's anything pinned at all —
// 0 if there's nothing pinned.
func (m *Model) pinnedHeaderHeight() int {
	n := 0
	if m.view == clarifyView {
		n += 2 // "Clarifying:" label + the item/empty-message line
	}
	if len(m.marks) > 0 {
		n += 1 + len(m.marks) // "Active marks:" label + one line per mark
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
// then a trailing blank separator — or nil if there's nothing pinned.
func (m Model) pinnedHeaderLines() []string {
	var lines []string
	if m.view == clarifyView {
		lines = append(lines, m.padLineToWidth(fileStyle.Background(overlayBg).Render("Clarifying:"), overlayBg))
		if m.clarifyTarget == nil {
			lines = append(lines, m.padLineToWidth(statusStyle.Background(overlayBg).Render("  Inbox is empty."), overlayBg))
		} else {
			lines = append(lines, m.renderPinnedRow("●", m.clarifyTarget, true))
		}
	}
	if letters := m.sortedMarkLetters(); len(letters) > 0 {
		lines = append(lines, m.padLineToWidth(fileStyle.Background(overlayBg).Render("Active marks:"), overlayBg))
		for _, letter := range letters {
			lines = append(lines, m.renderPinnedRow(string(letter), m.marks[letter], false))
		}
	}
	if len(lines) == 0 {
		return nil
	}
	// The trailing separator carries the overlay background too, so the
	// tinted block reads as one solid panel rather than cutting off
	// right before an untinted blank line.
	return append(lines, m.padLineToWidth("", overlayBg))
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
// clarify target's "●", or a mark's letter) in place of the
// gutter/indent/fold a normal listing row would have, then h's keyword
// and title — the same format regardless of which pinned section it's
// in, and regardless of h's actual level in its file's tree. The whole
// line carries the overlay background, padded to fill the terminal
// width. forClarify appends h's CREATED property (if it has one) and any
// SCHEDULED/DEADLINE/CLOSED planning line (via planningSummary, the same
// rendering the outline view itself uses) — on for the clarify target,
// where knowing how long an item has sat in the inbox and whether it
// already has a date is useful triage context; off for marks, which can
// point at any headline in the outline and aren't about triage.
func (m Model) renderPinnedRow(marker string, h *org.Headline, forClarify bool) string {
	line := pinMarkerStyle.Background(overlayBg).Render(marker) +
		bgSpan(overlayBg, "  ") +
		joinBg(m.renderKeywordAndTitle(h, overlayBg), overlayBg)
	if forClarify {
		if created := h.Properties["CREATED"]; created != "" {
			line += bgSpan(overlayBg, "  ") + timestampStyle.Background(overlayBg).Render("Created: "+created)
		}
		if planning := planningSummary(h); planning != "" {
			line += bgSpan(overlayBg, "  ") + timestampStyle.Background(overlayBg).Render(planning)
		}
	}
	return m.padLineToWidth(line, overlayBg)
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

	page := m.pageSize()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if end := m.entryEnd(m.cursor); end >= m.offset+page {
		offset := end - page + 1
		if offset > m.cursor {
			offset = m.cursor
		}
		m.offset = offset
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

func (m Model) View() string {
	if len(m.rows) == 0 {
		if m.view == agendaView {
			return fmt.Sprintf("Nothing due in the next %d days. :outline to go back.\n", m.agendaDays)
		}
		return "No org files found.\n"
	}

	page := m.pageSize()
	start := m.offset
	end := start + page
	if end > len(m.rows) {
		end = len(m.rows)
	}

	// An entry's body lines highlight along with it — the whole entry is
	// one item, not a separately-steppable row per line — so extend the
	// highlight from the cursor over any of its own body lines that
	// immediately follow. In visual mode, the rest of the selection (from
	// visualAnchor to the cursor) is also highlighted, but with
	// visualSelectionBg rather than cursorBg — otherwise the whole block
	// looks uniform and there'd be no way to tell which end is actually
	// the cursor (e.g. before extending the selection further, or right
	// after Esc leaves the cursor wherever it was).
	cursorStart, cursorEnd := m.cursor, m.entryEnd(m.cursor)
	selStart, selEnd := cursorStart, cursorEnd
	if m.mode == visualMode {
		selStart, selEnd = m.visualRange()
	}

	var b strings.Builder
	for _, line := range m.pinnedHeaderLines() {
		b.WriteString(line)
		b.WriteString("\n")
	}
	for i := start; i < end; i++ {
		if i > start && m.rows[i].section != "" {
			b.WriteString("\n")
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
	// Pad with blank lines so the status bar always sits on the last row
	// of the screen, even when there are fewer than a page of items.
	for i := end - start; i < page; i++ {
		b.WriteString("\n")
	}

	switch {
	case m.mode == commandMode:
		b.WriteString(":" + m.commandInput)
		b.WriteString(cursorStyle.Render(" ")) // caret, right after the input (no in-line editing yet)
		if m.commandCompletions != "" {
			b.WriteString("  " + statusStyle.Render(m.commandCompletions))
		}
		// m.message can be set without leaving commandMode (e.g. Tab
		// completion finding no match) — shown here too, not just in the
		// mode-less case below, or it'd be set but never actually visible.
		if m.message != "" {
			b.WriteString("  " + errorStyle.Render(m.message))
		}
	case m.mode == selectMode:
		b.WriteString(m.renderStatusSelector())
	case m.mode == deadlineMode:
		b.WriteString(" Deadline (YYYY-MM-DD, \"3d\", \"next tue\"; empty clears): " + m.deadlineInput)
		b.WriteString(cursorStyle.Render(" "))
		// As above: an invalid date sets m.message but deliberately leaves
		// the prompt open for correction (see applyDeadlineInput), so it
		// must be shown here rather than only in the mode-less case below.
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
		b.WriteString(statusStyle.Render(fmt.Sprintf("-- VISUAL LINE -- %d selected  (d: delete, R: set status, Esc: cancel)", len(m.visualSelectedHeadlines()))))
		if m.message != "" {
			b.WriteString("  " + errorStyle.Render(m.message))
		}
	case m.message != "":
		b.WriteString(errorStyle.Render(m.message))
	default:
		for i, line := range m.normalStatusLines() {
			if i > 0 {
				b.WriteString("\n")
			}
			b.WriteString(m.padLineToWidth(statusStyle.Background(overlayBg).Render(line), overlayBg))
		}
	}

	return b.String()
}

// normalStatusLines returns the line(s) for the default (mode-less)
// status area: usually just one ("dir — item N/M — url"), but if the
// current entry has one or more links and the combined line would be
// wider than m.width, the link(s) are moved off of it — onto a line of
// their own together if that fits, or (with multiple links) one per line
// if even that doesn't — since a wrapped URL can't be resolved by the
// terminal, this gives each the best chance of fitting unwrapped.
// Unknown width (m.width <= 0) never triggers a split.
func (m *Model) normalStatusLines() []string {
	place := m.ws.Dir
	switch m.view {
	case agendaView:
		place = "agenda"
	case configView:
		place = "config"
	case logView:
		place = "log"
	}
	main := fmt.Sprintf(" %s  —  item %d/%d", place, m.cursor+1, len(m.rows))
	h := m.currentHeadline()
	if h == nil {
		return []string{main}
	}
	urls := linksInTitle(h.Title)
	if len(urls) == 0 {
		return []string{main}
	}
	// Plain, unstyled URLs, printed as-is (not org-mode link syntax) so
	// the terminal's own URL detection can make them clickable.
	joined := strings.Join(urls, "  ")
	if m.width <= 0 || fitsWidth(main+"  —  "+joined, m.width) {
		return []string{main + "  —  " + joined}
	}
	if fitsWidth(" "+joined, m.width) {
		return []string{main, " " + joined}
	}
	lines := make([]string, 0, 1+len(urls))
	lines = append(lines, main)
	for _, u := range urls {
		lines = append(lines, " "+u)
	}
	return lines
}

// fitsWidth reports whether s (measured in runes, not bytes) fits within
// width columns.
func fitsWidth(s string, width int) bool {
	return utf8.RuneCountInString(s) <= width
}

// statusHeight is how many lines the bottom status area occupies for the
// current mode/cursor: every mode but the default one is always one
// line; the default one is whatever normalStatusLines returns (usually
// 1, but 2 when a link is being given its own line — see
// normalStatusLines).
func (m *Model) statusHeight() int {
	if m.mode != normalMode || m.message != "" {
		return 1
	}
	return len(m.normalStatusLines())
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

// gutter renders the leftmost column of a row: a single-character dirty
// marker, always present (blank when clean) so every row lines up the
// same way vim's line-number column does, regardless of indentation. bg
// is the background it's rendered with — lipgloss.NoColor{} normally,
// or the cursor row's highlight (see renderRowWithBg).
func gutter(dirty bool, bg lipgloss.TerminalColor) string {
	if dirty {
		return errorStyle.Background(bg).Render("+")
	}
	return bgSpan(bg, " ")
}

// markColumn is a headline row's mark/clarify gutter column, in outline
// or agenda view alike — a column of its own, separate from gutter's
// dirty marker, so a row that's both marked (or the clarify target) and
// dirty shows both indicators at once instead of one hiding the other:
// the clarify target's "●" (clarify view only) takes priority over a
// mark's letter, since a row can't be both; blank if neither applies.
// bg is the background it's rendered with (see gutter).
func (m Model) markColumn(h *org.Headline, bg lipgloss.TerminalColor) string {
	if m.view == clarifyView && h == m.clarifyTarget {
		return pinMarkerStyle.Background(bg).Render("●")
	}
	if letter, ok := m.markLetterFor(h); ok {
		return pinMarkerStyle.Background(bg).Render(string(letter))
	}
	return bgSpan(bg, " ")
}

// lockColumn is a headline row's :format-links gutter column — a column
// of its own (see gutter, markColumn), so it shows up alongside the
// dirty marker and any mark/clarify pin rather than hiding them. "◆"
// (U+25C6 BLACK DIAMOND — plain single-width Unicode, same block as the
// "●" mark/clarify glyph, so it renders reliably anywhere that already
// does) while h is locked (see m.immutable), blank otherwise. bg is the
// background it's rendered with (see gutter).
func (m Model) lockColumn(h *org.Headline, bg lipgloss.TerminalColor) string {
	if m.immutable[h] {
		return lockedStyle.Background(bg).Render("◆")
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
	case r.text != "":
		// Flush left, unstyled beyond the cursor's own background — a
		// :config row is plain informational text, not a headline.
		return highlightMatches(r.text, query, lipgloss.NewStyle().Background(bg))
	case r.section != "":
		// Flush left (no gutter/indent), unlike every item row below it,
		// so a section header stands out at a glance in a long agenda.
		return highlightMatches(r.section, query, fileStyle.Background(bg))
	case r.file != nil:
		// Blank mark and lock columns: files themselves are never marked
		// or locked by :format-links, but this keeps every row's dirty
		// marker lined up in the same column.
		name := highlightMatches(filepath.Base(r.file.Path), query, fileStyle.Background(bg))
		return bgSpan(bg, " ") + bgSpan(bg, " ") + gutter(m.dirty[r.file], bg) + bgSpan(bg, " ") + name
	case r.isAgendaItem:
		return m.renderAgendaItemRowWithBg(r, bg)
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

	line := m.markColumn(h, bg) + m.lockColumn(h, bg) + gutter(m.dirtyHeadlines[h], bg) + bgSpan(bg, " ") + indent + fold + bgSpan(bg, " ") + joinBg(m.renderKeywordAndTitle(h, bg), bg)

	if len(h.Tags) > 0 {
		line += bgSpan(bg, "  ") + highlightMatches(":"+strings.Join(h.Tags, ":")+":", query, m.fadeIfImmutable(tagStyle, h).Background(bg))
	}

	if ts := planningSummary(h); ts != "" {
		line += bgSpan(bg, "  ") + m.fadeIfImmutable(timestampStyle, h).Background(bg).Render(ts)
	}

	return line
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
// flat), tags, then which file it's from and the date/label (Scheduled
// or Deadline) it's shown for.
func (m Model) renderAgendaItemRowWithBg(r row, bg lipgloss.TerminalColor) string {
	h := r.headline
	line := m.markColumn(h, bg) + m.lockColumn(h, bg) + gutter(m.dirtyHeadlines[h], bg) + bgSpan(bg, " ") + joinBg(m.renderKeywordAndTitle(h, bg), bg)

	if len(h.Tags) > 0 {
		line += bgSpan(bg, "  ") + highlightMatches(":"+strings.Join(h.Tags, ":")+":", m.activeSearchQuery(), m.fadeIfImmutable(tagStyle, h).Background(bg))
	}

	fileName := ""
	if f := m.fileForHeadline(h); f != nil {
		fileName = filepath.Base(f.Path)
	}
	timestamp := m.fadeIfImmutable(timestampStyle, h).Background(bg)
	if r.agendaLabel != "" {
		label := r.agendaLabel
		if r.agendaMissed > 0 {
			label = fmt.Sprintf("%s (%dx)", label, r.agendaMissed)
		}
		date := r.agendaDate.Format("2006-01-02 Mon")
		if r.agendaRepeater != "" {
			date += " " + r.agendaRepeater
		}
		line += bgSpan(bg, "  ") + timestamp.Render(fmt.Sprintf("[%s]  %s: %s", fileName, label, date))
	} else {
		// A Next Actions entry: no date to show, just which file it's in.
		line += bgSpan(bg, "  ") + timestamp.Render(fmt.Sprintf("[%s]", fileName))
	}

	return line
}

// renderBodyLineWithBg renders one line of a headline's free-text body,
// indented to line up where a child's own content would start (blank
// mark/gutter/fold columns, since a body line isn't itself a separately
// addressable item — that state lives on the headline's own row), shown
// in a muted style so it doesn't compete visually with real entries.
func (m Model) renderBodyLineWithBg(r row, bg lipgloss.TerminalColor) string {
	indent := strings.Repeat("  ", r.level)
	blanks := bgSpan(bg, "    "+indent+"  ") // mark + lock + gutter + space, then indent, then fold + space
	style := m.fadeIfImmutable(bodyStyle, r.headline).Background(bg)
	return blanks + highlightMatches(strings.TrimSpace(r.bodyText), m.activeSearchQuery(), style)
}

// renderStatusSelector renders the R status picker's single status-line
// menu: every candidate with its shortcut bracketed, the currently
// highlighted one shown in reverse video.
func (m Model) renderStatusSelector() string {
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

	var parts []string
	for i, c := range statusCandidates {
		label := fmt.Sprintf("[%c] %s", c.shortcut, c.label)
		if i == highlighted {
			label = cursorStyle.Render(label)
		} else {
			label = shortcutStyle.Render(fmt.Sprintf("[%c]", c.shortcut)) + " " + c.label
		}
		parts = append(parts, label)
	}

	prefix := " Set status:  "
	if n := len(m.selectModeTargets); n > 0 {
		prefix = fmt.Sprintf(" Set status for %d selected:  ", n)
	}
	line := prefix + strings.Join(parts, "   ")
	if m.selectFilter != "" {
		line += "   (" + m.selectFilter + ")"
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
