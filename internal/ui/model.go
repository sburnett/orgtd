// Package ui implements the Bubble Tea viewer: a scrollable, foldable
// outline over every org file in a workspace, with in-memory editing of
// item status. Changes are not yet written back to disk — no agenda, no
// Google integration either.
package ui

import (
	"fmt"
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

	// overlayBg is the subtle background tint for the pinned header
	// (clarify/marks) and the status bar — a light/dark pair so it reads
	// as a faint panel regardless of the terminal's own color scheme,
	// resolved via lipgloss's terminal background detection.
	overlayBg = lipgloss.AdaptiveColor{Light: "#e4e4e4", Dark: "#262626"}
)

// bgSpan renders s with only a background color — no other styling —
// for the plain-text gaps (join separators, padding) inside a
// background-tinted line, so they don't leave un-tinted holes once an
// adjacent styled segment's own reset code fires.
func bgSpan(bg lipgloss.TerminalColor, s string) string {
	return lipgloss.NewStyle().Background(bg).Render(s)
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

	section      string    // set for an agenda section-header row ("Overdue" etc.); outline rows never set this
	isAgendaItem bool      // true for every agenda item row (Next Actions entries have no date/label, so this — not agendaLabel — is the reliable marker)
	agendaLabel  string    // "Scheduled" or "Deadline", set for a date-based agenda item row; empty for a Next Actions entry
	agendaDate   time.Time // the date this agenda item row is shown for, if agendaLabel is set
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

	register *org.Headline // last deleted (dd) or yanked (yy) entry, pasted (as a copy) by p/P

	marks map[rune]*org.Headline // vim-style marks (letter -> headline), set by "m<letter>", jumped to by "'<letter>"; each stays pinned to the top of the screen (see pinnedHeaderLines) until cleared

	mode         mode
	commandInput string
	message      string // transient status-line message (e.g. an error), cleared on the next key press

	selectFilter string // typed so far, in selectMode
	selectIndex  int    // highlighted index within the filtered candidates, in selectMode

	deadlineInput string // typed so far, in deadlineMode

	urlFormatterCmd string // external program that turns a bare URL into an org-mode link; disabled if empty

	view       viewKind
	agendaDays int // how many days ahead the agenda's "Upcoming" section covers

	inboxFile     string        // base name of the file :clarify treats as the inbox
	clarifyTarget *org.Headline // the inbox item currently pinned for clarification, in clarifyView; nil if the inbox is empty
}

// viewKind selects what rebuildRows populates m.rows with.
type viewKind int

const (
	outlineView viewKind = iota
	agendaView
	clarifyView
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

// New builds a viewer model over ws. Every headline starts expanded.
func New(ws *workspace.Workspace, opts ...Option) Model {
	m := Model{
		ws:             ws,
		collapsed:      make(map[*org.Headline]bool),
		dirty:          make(map[*org.File]bool),
		dirtyHeadlines: make(map[*org.Headline]bool),
		savedPos:       make(map[*org.File]int),
		agendaDays:     14,
		inboxFile:      "inbox.org",
	}
	for _, opt := range opts {
		opt(&m)
	}
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

// advanceClarifyTarget sets m.clarifyTarget to the inbox's current first
// top-level headline, or nil if the inbox file is missing or empty.
func (m *Model) advanceClarifyTarget() {
	f := m.findInboxFile()
	if f != nil && len(f.Headlines) > 0 {
		m.clarifyTarget = f.Headlines[0]
	} else {
		m.clarifyTarget = nil
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

func (m *Model) appendHeadlines(headlines []*org.Headline) {
	for _, h := range headlines {
		m.rows = append(m.rows, row{headline: h, level: h.Level})
		if len(h.Children) > 0 && !m.collapsed[h] {
			m.appendHeadlines(h.Children)
		}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case editFinishedMsg:
		return m.finishEdit(msg)

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

	switch key {
	case ":":
		m.mode = commandMode
		m.commandInput = ""
		return m, nil

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
			m.cursor = n - 1
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
		if m.currentHeadline() != nil {
			m.mode = selectMode
			m.selectFilter = ""
			m.selectIndex = m.currentStatusIndex()
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
		}

	case "a":
		if wasPendingZ {
			m.toggleFold()
		}

	case "A":
		if wasPendingZ {
			m.foldToggleAll()
		}

	case "u":
		m.undo()

	case "ctrl+r":
		m.redo()

	case "d":
		if wasPendingG {
			m.startSetDeadline()
		} else if wasPendingD {
			m.deleteHeadline()
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

func (m Model) updateCommandMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
	}

	return m, nil
}

// runCommand executes the typed command line and always returns to
// normal mode.
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

	case "agenda":
		m.switchToView(agendaView)

	case "clarify":
		m.enterClarifyView()

	case "outline":
		m.switchToView(outlineView)

	case "delmarks":
		m.message = "Usage: :delmarks <letters> or :delmarks!"

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
		m.applyStatus(matches[0].keyword)
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
	m.applyStatus(matches[idx].keyword)
	return m, nil
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
	if h == nil {
		return
	}
	m.mode = deadlineMode
	m.deadlineInput = prefillDateInput(h.Deadline)
}

// updateDeadlineMode handles key presses while the deadline prompt is
// open: Enter applies the typed date (or clears the deadline if left
// empty), Esc cancels without changes.
func (m Model) updateDeadlineMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
// open, input intact, so it can be corrected.
func (m Model) applyDeadlineInput() (tea.Model, tea.Cmd) {
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
// entered (or left) a DONE-class state.
func (m *Model) applyStatus(keyword string) {
	h := m.currentHeadline()
	if h == nil {
		return
	}

	newClosed := h.Closed
	switch {
	case org.IsDoneKeyword(keyword) && !org.IsDoneKeyword(h.Keyword):
		newClosed = &org.Timestamp{Raw: time.Now().Format("2006-01-02 Mon 15:04")}
	case !org.IsDoneKeyword(keyword) && org.IsDoneKeyword(h.Keyword):
		newClosed = nil
	}

	m.pushUndo(&statusChangeAction{
		h:          h,
		f:          m.fileForHeadline(h),
		oldKeyword: h.Keyword,
		newKeyword: keyword,
		oldClosed:  h.Closed,
		newClosed:  newClosed,
	})
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
	err    error
}

// startEdit writes the current headline (and its entire subtree) to a
// temp file and opens it in $EDITOR for editing in place. Returns nil if
// there's nothing to edit or the editor couldn't be launched, in which
// case any error is left in m.message.
func (m *Model) startEdit() tea.Cmd {
	h := m.currentHeadline()
	if h == nil {
		return nil
	}
	return m.launchEditor(h, nil)
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

// buildEditorCommand builds the *exec.Cmd for opening path in the editor
// named by editorEnv ($EDITOR's value; "vim" if empty), splitting off
// any extra words as leading arguments (e.g. "code --wait"). For an
// editor in editorsWithLineArg, it also inserts a "+N" argument so the
// editor opens with the cursor on the real content (before is the
// context text written ahead of it in the file; its newline count is
// exactly the 1-based line the real content starts on).
func buildEditorCommand(editorEnv, path, before string) *exec.Cmd {
	fields := strings.Fields(editorEnv)
	if len(fields) == 0 {
		fields = []string{"vim"}
	}
	args := append([]string{}, fields[1:]...)
	if editorsWithLineArg[filepath.Base(fields[0])] {
		startLine := strings.Count(before, "\n") + 1
		args = append(args, fmt.Sprintf("+%d", startLine))
	}
	args = append(args, path)
	return exec.Command(fields[0], args...)
}

// launchEditor writes h to a temp file and opens it in $EDITOR (vim by
// default), suspending the TUI for the duration. ctx tags the resulting
// editFinishedMsg so finishEdit knows whether this is an o/O insert
// session or a plain `i` edit. Returns nil if the temp file couldn't be
// created or the editor couldn't be started, in which case the error is
// left in m.message.
func (m *Model) launchEditor(h *org.Headline, ctx *insertContext) tea.Cmd {
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

	editorCmd := buildEditorCommand(os.Getenv("EDITOR"), path, before)

	return tea.ExecProcess(editorCmd, func(err error) tea.Msg {
		return editFinishedMsg{path: path, target: h, insert: ctx, err: err}
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

	tentative := &org.Headline{Level: level, Parent: parent}
	if parent != nil {
		parent.Children = spliceHeadlines(parent.Children, idx, 0, []*org.Headline{tentative})
	} else {
		f.Headlines = spliceHeadlines(f.Headlines, idx, 0, []*org.Headline{tentative})
	}
	m.rebuildRows()
	m.focusHeadline(tentative)

	ctx := insertContext{f: f, parent: parent, index: idx, origin: origin}
	cmd := m.launchEditor(tentative, &ctx)
	if cmd == nil {
		// Couldn't even launch the editor; don't leave a blank
		// placeholder headline behind with no way to remove it.
		m.rollbackInsert(ctx, tentative)
	}
	return cmd
}

// deleteHeadline removes the current headline and its whole subtree
// ("dd"), storing a copy in the register so it can be pasted back with
// p/P. A no-op on file rows.
func (m *Model) deleteHeadline() {
	h := m.currentHeadline()
	if h == nil {
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
	if h == nil {
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
	if h == nil {
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

// orgLinkRe matches an existing org-mode link, "[[url]]" or
// "[[url][description]]" — group 1 is the url, group 2 the description
// (absent for the no-description form). Used both to make formatURLs
// leave existing links alone, and to render a link's display text (the
// description if present, else the url) in the row list.
var orgLinkRe = regexp.MustCompile(`\[\[([^\]\[]+)\](?:\[([^\]\[]*)\])?\]`)

// bareURLRe matches a URL not already wrapped in link brackets. It stops
// at '[' and ']' so it can never span into or out of an org-mode link.
var bareURLRe = regexp.MustCompile(`https?://[^\s\[\]]+`)

// renderTitleForDisplay renders title for the row list: each org-mode
// link is replaced with just its display text (the description, or the
// url if there's no description) and underlined, instead of showing the
// raw "[[url][description]]" syntax. base is the style otherwise applied
// to the title (e.g. doneTitleStyle for a DONE/CANCELLED item); every
// segment — link or plain text — is rendered with base (underlined,
// for a link) so the two compose without nesting escape codes.
func renderTitleForDisplay(title string, base lipgloss.Style) string {
	matches := orgLinkRe.FindAllStringSubmatchIndex(title, -1)
	if len(matches) == 0 {
		return base.Render(title)
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
			b.WriteString(base.Render(title[last:start]))
		}
		b.WriteString(linkStyle.Render(display))
		last = end
	}
	if last < len(title) {
		b.WriteString(base.Render(title[last:]))
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
	matches := bareURLRe.FindAllStringIndex(text, -1)
	if len(matches) == 0 {
		return text
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

	cache := make(map[string]string)
	var b strings.Builder
	last := 0
	for _, span := range matches {
		start, end := span[0], span[1]
		if withinLink(start) {
			continue
		}
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

// runURLFormatter invokes the configured urlFormatterCmd as
// `urlFormatterCmd <url>` and returns its trimmed stdout. On any failure
// (exec error, empty output), it returns url unchanged.
func (m *Model) runURLFormatter(url string) string {
	out, err := exec.Command(m.urlFormatterCmd, url).Output()
	if err != nil {
		return url
	}
	formatted := strings.TrimSpace(string(out))
	if formatted == "" {
		return url
	}
	return formatted
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
	if h == nil || len(h.Children) == 0 {
		return
	}
	m.collapsed[h] = !m.collapsed[h]
	m.rebuildRows()
}

// foldOpen ("zo") reveals the current headline's own children, one level.
func (m *Model) foldOpen() {
	h := m.currentHeadline()
	if h == nil || len(h.Children) == 0 {
		return
	}
	m.collapsed[h] = false
	m.rebuildRows()
}

// foldClose ("zc") hides the current headline's own children, one level.
func (m *Model) foldClose() {
	h := m.currentHeadline()
	if h == nil || len(h.Children) == 0 {
		return
	}
	m.collapsed[h] = true
	m.rebuildRows()
}

// foldOpenAll ("zO") reveals the current headline's entire subtree,
// recursively.
func (m *Model) foldOpenAll() {
	h := m.currentHeadline()
	if h == nil || len(h.Children) == 0 {
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
	if h == nil || len(h.Children) == 0 {
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
	if h == nil || len(h.Children) == 0 {
		return
	}
	setCollapsedRecursive(m.collapsed, h, !m.collapsed[h])
	m.rebuildRows()
}

// setCollapsedRecursive sets collapsed[x] = value for h and every
// descendant of h that has children (a childless headline has nothing
// to fold, so it's left out of the map).
func setCollapsedRecursive(collapsed map[*org.Headline]bool, h *org.Headline, value bool) {
	if len(h.Children) > 0 {
		collapsed[h] = value
	}
	for _, c := range h.Children {
		setCollapsedRecursive(collapsed, c, value)
	}
}

func (m *Model) moveCursor(delta int) {
	m.cursor += delta
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.rows) {
		m.cursor = len(m.rows) - 1
	}
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
	for i >= 0 && i < len(m.rows) && m.rowLevel(i) > lvl {
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
	if next := m.cursor + 1; next < len(m.rows) && m.rowLevel(next) > m.rowLevel(m.cursor) {
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
	r := m.rows[m.cursor]
	if r.headline == nil {
		m.moveSiblingLevel(-1)
		return
	}
	if r.headline.Parent != nil {
		m.focusHeadline(r.headline.Parent)
		return
	}
	if f := m.fileForHeadline(r.headline); f != nil {
		m.focusFile(f)
	}
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
		if m.rowLevel(i) == cur+1 {
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
			lines = append(lines, m.renderPinnedRow("●", m.clarifyTarget))
		}
	}
	if letters := m.sortedMarkLetters(); len(letters) > 0 {
		lines = append(lines, m.padLineToWidth(fileStyle.Background(overlayBg).Render("Active marks:"), overlayBg))
		for _, letter := range letters {
			lines = append(lines, m.renderPinnedRow(string(letter), m.marks[letter]))
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
// width.
func (m Model) renderPinnedRow(marker string, h *org.Headline) string {
	line := pinMarkerStyle.Background(overlayBg).Render(marker) +
		bgSpan(overlayBg, "  ") +
		joinBg(m.renderKeywordAndTitle(h, overlayBg), overlayBg)
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

func (m *Model) ensureVisible() {
	page := m.pageSize()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+page {
		m.offset = m.cursor - page + 1
	}
	if m.offset < 0 {
		m.offset = 0
	}
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

	var b strings.Builder
	for _, line := range m.pinnedHeaderLines() {
		b.WriteString(line)
		b.WriteString("\n")
	}
	for i := start; i < end; i++ {
		if i > start && m.rows[i].section != "" {
			b.WriteString("\n")
		}
		line := m.renderRow(m.rows[i])
		if i == m.cursor {
			line = cursorStyle.Render(line)
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
		b.WriteString(cursorStyle.Render(" ")) // caret, always at the end (no in-line editing yet)
	case m.mode == selectMode:
		b.WriteString(m.renderStatusSelector())
	case m.mode == deadlineMode:
		b.WriteString(" Deadline (YYYY-MM-DD, \"3d\", \"next tue\"; empty clears): " + m.deadlineInput)
		b.WriteString(cursorStyle.Render(" "))
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
	if m.view == agendaView {
		place = "agenda"
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
// same way vim's line-number column does, regardless of indentation.
func gutter(dirty bool) string {
	if dirty {
		return errorStyle.Render("+")
	}
	return " "
}

// markColumn is a headline row's mark/clarify gutter column, in outline
// or agenda view alike — a column of its own, separate from gutter's
// dirty marker, so a row that's both marked (or the clarify target) and
// dirty shows both indicators at once instead of one hiding the other:
// the clarify target's "●" (clarify view only) takes priority over a
// mark's letter, since a row can't be both; blank if neither applies.
func (m Model) markColumn(h *org.Headline) string {
	if m.view == clarifyView && h == m.clarifyTarget {
		return pinMarkerStyle.Render("●")
	}
	if letter, ok := m.markLetterFor(h); ok {
		return pinMarkerStyle.Render(string(letter))
	}
	return " "
}

func (m Model) renderRow(r row) string {
	switch {
	case r.section != "":
		// Flush left (no gutter/indent), unlike every item row below it,
		// so a section header stands out at a glance in a long agenda.
		return fileStyle.Render(r.section)
	case r.file != nil:
		// Blank mark column: files themselves are never marked, but this
		// keeps every row's dirty marker lined up in the same column.
		return " " + gutter(m.dirty[r.file]) + " " + fileStyle.Render(filepath.Base(r.file.Path))
	case r.isAgendaItem:
		return m.renderAgendaItemRow(r)
	}

	h := r.headline
	indent := strings.Repeat("  ", h.Level)

	fold := " "
	if len(h.Children) > 0 {
		if m.collapsed[h] {
			fold = "▶" // U+25B6 BLACK RIGHT-POINTING TRIANGLE (full-size; ▸ is a dedicated "small" variant)
		} else {
			fold = "▼" // U+25BC BLACK DOWN-POINTING TRIANGLE (full-size; ▾ is a dedicated "small" variant)
		}
	}

	line := m.markColumn(h) + gutter(m.dirtyHeadlines[h]) + " " + indent + fold + " " + strings.Join(m.renderKeywordAndTitle(h, lipgloss.NoColor{}), " ")

	if len(h.Tags) > 0 {
		line += "  " + tagStyle.Render(":"+strings.Join(h.Tags, ":")+":")
	}

	if ts := planningSummary(h); ts != "" {
		line += "  " + timestampStyle.Render(ts)
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
	var parts []string
	if h.Keyword != "" {
		style, ok := keywordStyles[h.Keyword]
		if !ok {
			style = lipgloss.NewStyle()
		}
		parts = append(parts, style.Background(bg).Render(h.Keyword))
	}
	if h.Priority != "" {
		parts = append(parts, bgSpan(bg, fmt.Sprintf("[#%s]", h.Priority)))
	}

	base := lipgloss.NewStyle().Background(bg)
	if org.IsDoneKeyword(h.Keyword) {
		base = doneTitleStyle.Background(bg)
	}
	parts = append(parts, renderTitleForDisplay(h.Title, base))
	return parts
}

// renderAgendaItemRow renders one agenda item row: keyword/priority/
// title (as in outline, but with no indent or fold arrow — agenda is
// flat), tags, then which file it's from and the date/label (Scheduled
// or Deadline) it's shown for.
func (m Model) renderAgendaItemRow(r row) string {
	h := r.headline
	line := m.markColumn(h) + gutter(m.dirtyHeadlines[h]) + " " + strings.Join(m.renderKeywordAndTitle(h, lipgloss.NoColor{}), " ")

	if len(h.Tags) > 0 {
		line += "  " + tagStyle.Render(":"+strings.Join(h.Tags, ":")+":")
	}

	fileName := ""
	if f := m.fileForHeadline(h); f != nil {
		fileName = filepath.Base(f.Path)
	}
	if r.agendaLabel != "" {
		line += "  " + timestampStyle.Render(fmt.Sprintf("[%s]  %s: %s", fileName, r.agendaLabel, r.agendaDate.Format("2006-01-02 Mon")))
	} else {
		// A Next Actions entry: no date to show, just which file it's in.
		line += "  " + timestampStyle.Render(fmt.Sprintf("[%s]", fileName))
	}

	return line
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

	line := " Set status:  " + strings.Join(parts, "   ")
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
