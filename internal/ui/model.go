// Package ui implements orgtd's Bubble Tea application: a scrollable,
// foldable outline over every org file in a workspace, with in-memory
// editing (written to disk only on :w), derived views (agenda, calendar,
// tags, clarify, ...), and background commands (calendar sync, link
// formatting, git commit). Nearly all behavior lives here, centered on
// Model; see DESIGN.md §4-§5 for the runtime model and file layout.
package ui

import (
	"fmt"
	"path/filepath"
	"regexp"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sburnett/orgtd/internal/execlog"
	"github.com/sburnett/orgtd/internal/extprog"
	"github.com/sburnett/orgtd/internal/gitrepo"
	"github.com/sburnett/orgtd/internal/org"
	"github.com/sburnett/orgtd/internal/workspace"
)

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
	meetingPickerMode
	tagMode
)

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

	register []*org.Headline // last deleted (dd/<N>dd/visual d) or yanked (yy) top-level entry/entries, pasted (as copies, in the same order) by p/P; stays pinned in the info buffer at the bottom of the screen (see infoBufferLines) until overwritten by a later delete/yank

	marks map[rune]*org.Headline // vim-style marks (letter -> headline), set by "m<letter>", jumped to by "'<letter>"; each stays pinned in the info buffer at the bottom of the screen (see infoBufferLines) until cleared

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
	// formatters, $EDITOR), for :log — see execlog.Log's own doc comment for
	// why it's a pointer rather than a plain value.
	execLog *execlog.Log

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
	pendingUntrackedThen  func(m *Model) tea.Cmd

	urlFormatterCmd      string         // external program that turns a bare URL into an org-mode link when editing an entry; disabled if empty
	urlFormatterPrefixes []string       // extra bare-URL prefixes beyond http(s)://, e.g. "bit.ly/", "go/" (see WithURLFormatterPrefixes)
	bareURLRe            *regexp.Regexp // compiled from urlFormatterPrefixes at construction time; see extprog.BareURLRegexp
	editorOverride       string         // takes precedence over $EDITOR when set (see WithEditor); empty means "use $EDITOR"

	// formatLinksURLFormatterCmd is the external program :format-links
	// invokes in batch mode (see extprog.RunBatchFormatter) — configured
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

	// meetingTagsFile is the base name of the file holding durable
	// meeting-tag records (e.g. "meeting-tags.org", the default) — "gt"
	// on a calendarView entry writes here instead of editing the entry
	// itself (see applyMeetingTag), so the tag survives calendarFile's
	// wholesale regeneration by :sync-calendar. Excluded from the outline
	// view entirely (see rebuildRows' default case) and shown instead, as
	// an editable outline of its own, in meetingTagsView.
	meetingTagsFile string

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
	// and renderPinnedRow for the same markers pinned in the info buffer
	// at the bottom of the screen) — set from the config file's [icons]
	// section (see WithDirtyIcon and its siblings, in options.go), each
	// defaulting to New's own built-in glyph/color when unset. markColor
	// has no matching markIcon: a mark's glyph is always the letter it
	// was set with ("m<letter>"), not a fixed character.
	dirtyIcon, dirtyColor     string
	markColor                 string
	clarifyIcon, clarifyColor string
	lockIcon, lockColor       string
	meetingIcon, meetingColor string

	// colors overrides the rest of the built-in color scheme (see
	// ColorOverrides and WithColors, and the resolver methods —
	// fileStyle, keywordStyle, tagStyle, and friends, in style.go) — set from the config file's [colors] section, same as
	// the icon fields above, each field defaulting to that resolver's own
	// built-in wildcharm-dark color when unset.
	colors ColorOverrides

	diffOutput string // combined stdout of the last :diff run (see showDiff), split into one row per line by appendDiffRows
	diffErr    string // if the last :diff run failed, why — shown instead of diffOutput; empty means it succeeded (even if there was nothing to show)

	// gitRunning is true while :commit's git commit + git push are in
	// flight on their own goroutine (see applyCommit/finishCommitPush) —
	// unlike :diff (fast, always synchronous), a push can block on the
	// remote for a while, so it runs in the background like
	// :format-links/:sync-calendar. Guards against a second :commit
	// starting concurrently, and against :w running while the working
	// tree git is about to commit could still change underneath it.
	gitRunning bool

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
	meetingTagsView
	tagsView
)

// New builds a viewer model over ws. Every headline starts expanded.
func New(ws *workspace.Workspace, opts ...Option) Model {
	m := Model{
		ws:                 ws,
		collapsed:          make(map[*org.Headline]bool),
		dirty:              make(map[*org.File]bool),
		dirtyHeadlines:     make(map[*org.Headline]bool),
		savedPos:           make(map[*org.File]int),
		immutable:          make(map[*org.Headline]bool),
		execLog:            &execlog.Log{},
		agendaDays:         14,
		inboxFile:          "inbox.org",
		calendarFile:       "calendar.org",
		meetingTagsFile:    "meeting-tags.org",
		hideDoneAfterHours: 24,
		gcalCalendarIDs:    []string{"primary"},
		gcalSyncPastDays:   1,
		gcalSyncFutureDays: 14,
	}
	for _, opt := range opts {
		opt(&m)
	}
	m.bareURLRe = extprog.BareURLRegexp(m.urlFormatterPrefixes)
	m.rebuildRows()
	return m
}

func (m Model) Init() tea.Cmd {
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

// findMeetingTagsFile returns the workspace file meetingTagsView shows
// (see WithMeetingTagsFile), or nil if it isn't loaded yet — nothing has
// ever been tagged via "gt" on a calendar entry, so the file doesn't
// exist on disk (see applyMeetingTag, which materializes it lazily on
// first use, mirroring how finishSyncCalendar lazily creates the
// calendar file's own workspace entry).
func (m *Model) findMeetingTagsFile() *org.File {
	for _, f := range m.ws.Files {
		if filepath.Base(f.Path) == m.meetingTagsFile {
			return f
		}
	}
	return nil
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

	case commitPushMsg:
		return m.finishCommitPush(msg)

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
		case visualMode:
			return m.updateVisualMode(msg)
		default:
			return m.updateNormalMode(msg)
		}
	}

	return m, nil
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
			if _, err := m.repo().Add(untrackedFiles); err != nil {
				m.message = fmt.Sprintf("git add failed: %s", gitrepo.ErrorText(err))
				return m, nil
			}
		}
		return m, untrackedThen(&m)
	}

	if !accepted {
		return m, nil
	}
	if pendingFileEdit != nil {
		return m, m.startEditFile(pendingFileEdit)
	}
	return m, nil
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

func (m *Model) currentHeadline() *org.Headline {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return nil
	}
	return m.rows[m.cursor].headline
}
