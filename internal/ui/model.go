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
	"strconv"
	"strings"
	"time"
	"unicode"

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
)

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
	file     *org.File // set for a file-header row
	headline *org.Headline
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

	pendingG bool
	pendingD bool

	register *org.Headline // last deleted entry (dd), pasted (as a copy) by p/P

	mode         mode
	commandInput string
	message      string // transient status-line message (e.g. an error), cleared on the next key press

	selectFilter string // typed so far, in selectMode
	selectIndex  int    // highlighted index within the filtered candidates, in selectMode

	deadlineInput string // typed so far, in deadlineMode
}

// New builds a viewer model over ws. Every headline starts expanded.
func New(ws *workspace.Workspace) Model {
	m := Model{
		ws:             ws,
		collapsed:      make(map[*org.Headline]bool),
		dirty:          make(map[*org.File]bool),
		dirtyHeadlines: make(map[*org.Headline]bool),
		savedPos:       make(map[*org.File]int),
	}
	m.rebuildRows()
	return m
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m *Model) rebuildRows() {
	m.rows = m.rows[:0]
	for _, f := range m.ws.Files {
		m.rows = append(m.rows, row{file: f})
		m.appendHeadlines(f.Headlines)
	}
	if m.cursor >= len(m.rows) {
		m.cursor = len(m.rows) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

func (m *Model) appendHeadlines(headlines []*org.Headline) {
	for _, h := range headlines {
		m.rows = append(m.rows, row{headline: h})
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
	m.pendingG = false
	m.pendingD = false
	m.message = ""

	switch key {
	case ":":
		m.mode = commandMode
		m.commandInput = ""
		return m, nil

	case "j", "down":
		m.moveSiblingLevel(1)

	case "k", "up":
		m.moveSiblingLevel(-1)

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
		m.cursor = len(m.rows) - 1

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
		if cmd := m.insertHeadline(false); cmd != nil {
			return m, cmd
		}

	case "O":
		if cmd := m.insertHeadline(true); cmd != nil {
			return m, cmd
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
		if r := []rune(m.commandInput); len(r) > 0 {
			m.commandInput = string(r[:len(r)-1])
		}
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

	default:
		m.message = fmt.Sprintf("Unknown command: %s", cmd)
	}
	return m, nil
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

	fields := strings.Fields(os.Getenv("EDITOR"))
	if len(fields) == 0 {
		fields = []string{"vim"}
	}
	args := append(append([]string{}, fields[1:]...), path)
	editorCmd := exec.Command(fields[0], args...)

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
	prev, next := m.siblingHeadlines(h)

	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n#\n", fileName)
	if h.Parent != nil {
		fmt.Fprintf(&b, "# %s\n", commentedHeadlineLine(h.Parent))
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
// top-level), or nil for either that doesn't exist.
func (m *Model) siblingHeadlines(h *org.Headline) (prev, next *org.Headline) {
	f, parent, idx := m.insertPosition(h)
	if idx < 0 {
		return nil, nil
	}
	list := f.Headlines
	if parent != nil {
		list = parent.Children
	}
	if idx > 0 {
		prev = list[idx-1]
	}
	if idx+1 < len(list) {
		next = list[idx+1]
	}
	return prev, next
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

	file, err := org.Parse(strings.NewReader(stripCommentLines(string(data))), "")
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

func (m *Model) toggleFold() {
	h := m.currentHeadline()
	if h == nil || len(h.Children) == 0 {
		return
	}
	m.collapsed[h] = !m.collapsed[h]
	m.rebuildRows()
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
func (m *Model) rowLevel(i int) int {
	if r := m.rows[i]; r.file == nil {
		return r.headline.Level
	}
	return 0
}

// moveSiblingLevel moves the cursor to the next (dir>0) or previous
// (dir<0) row at the same indentation level as the current row, skipping
// over any deeper rows (i.e. descendants) along the way. If the current
// row is the last (or first) at its level, this lands on the next
// shallower row instead — "hopping up" to the parent level.
func (m *Model) moveSiblingLevel(dir int) {
	if len(m.rows) == 0 {
		return
	}
	cur := m.rowLevel(m.cursor)
	i := m.cursor + dir
	for i >= 0 && i < len(m.rows) && m.rowLevel(i) > cur {
		i += dir
	}
	if i >= 0 && i < len(m.rows) {
		m.cursor = i
	}
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

// moveShallower moves the cursor to the next shallower indentation level
// (i.e. the current row's parent), or if there is none, falls back to
// moveSiblingLevel(-1).
func (m *Model) moveShallower() {
	if len(m.rows) == 0 {
		return
	}
	if prev := m.cursor - 1; prev >= 0 && m.rowLevel(prev) < m.rowLevel(m.cursor) {
		m.cursor = prev
		return
	}
	m.moveSiblingLevel(-1)
}

// pageSize is the number of rows visible at once, reserving one line for
// the status bar.
func (m *Model) pageSize() int {
	n := m.height - 1
	if n < 1 {
		n = 1
	}
	return n
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
		return "No org files found.\n"
	}

	page := m.pageSize()
	start := m.offset
	end := start + page
	if end > len(m.rows) {
		end = len(m.rows)
	}

	var b strings.Builder
	for i := start; i < end; i++ {
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
		status := fmt.Sprintf(" %s  —  item %d/%d", m.ws.Dir, m.cursor+1, len(m.rows))
		b.WriteString(statusStyle.Render(status))
	}

	return b.String()
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

func (m Model) renderRow(r row) string {
	if r.file != nil {
		return gutter(m.dirty[r.file]) + " " + fileStyle.Render(filepath.Base(r.file.Path))
	}

	h := r.headline
	indent := strings.Repeat("  ", h.Level)

	fold := " "
	if len(h.Children) > 0 {
		if m.collapsed[h] {
			fold = "▸"
		} else {
			fold = "▾"
		}
	}

	var parts []string
	if h.Keyword != "" {
		style, ok := keywordStyles[h.Keyword]
		if !ok {
			style = lipgloss.NewStyle()
		}
		parts = append(parts, style.Render(h.Keyword))
	}
	if h.Priority != "" {
		parts = append(parts, fmt.Sprintf("[#%s]", h.Priority))
	}

	title := h.Title
	if org.IsDoneKeyword(h.Keyword) {
		title = doneTitleStyle.Render(title)
	}
	parts = append(parts, title)

	line := gutter(m.dirtyHeadlines[h]) + " " + indent + fold + " " + strings.Join(parts, " ")

	if len(h.Tags) > 0 {
		line += "  " + tagStyle.Render(":"+strings.Join(h.Tags, ":")+":")
	}

	if ts := planningSummary(h); ts != "" {
		line += "  " + timestampStyle.Render(ts)
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
