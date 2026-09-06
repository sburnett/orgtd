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
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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
	dirty          map[*org.File]bool     // files with in-memory changes not yet written to disk
	dirtyHeadlines map[*org.Headline]bool // headlines changed since the last write of their file
	rows           []row

	cursor int
	offset int // index of the first visible row (for scrolling)

	width, height int

	pendingG bool

	mode         mode
	commandInput string
	message      string // transient status-line message (e.g. an error), cleared on the next key press

	selectFilter string // typed so far, in selectMode
	selectIndex  int    // highlighted index within the filtered candidates, in selectMode
}

// New builds a viewer model over ws. Every headline starts expanded.
func New(ws *workspace.Workspace) Model {
	m := Model{
		ws:             ws,
		collapsed:      make(map[*org.Headline]bool),
		dirty:          make(map[*org.File]bool),
		dirtyHeadlines: make(map[*org.Headline]bool),
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
		default:
			return m.updateNormalMode(msg)
		}
	}

	return m, nil
}

func (m Model) updateNormalMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	wasPendingG := m.pendingG
	m.pendingG = false
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
		delete(m.dirty, f)
		org.Walk(f.Headlines, func(h *org.Headline) { delete(m.dirtyHeadlines, h) })
		written = append(written, filepath.Base(f.Path))
	}

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
	wasDone := org.IsDoneKeyword(h.Keyword)
	isDone := org.IsDoneKeyword(keyword)
	h.Keyword = keyword
	switch {
	case isDone && !wasDone:
		h.Closed = &org.Timestamp{Raw: time.Now().Format("2006-01-02 Mon 15:04")}
	case !isDone && wasDone:
		h.Closed = nil
	}
	m.markDirty(h)
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

// markDirty flags h itself, and h's file, as having unwritten in-memory
// changes.
func (m *Model) markDirty(h *org.Headline) {
	if f := m.fileForHeadline(h); f != nil {
		m.dirty[f] = true
	}
	m.dirtyHeadlines[h] = true
}

// editFinishedMsg reports that the external editor launched by startEdit
// has exited.
type editFinishedMsg struct {
	path   string
	target *org.Headline
	err    error
}

// startEdit writes the current headline (and its entire subtree) to a
// temp file and opens it in $EDITOR (vim by default), suspending the TUI
// for the duration. Returns nil if there's nothing to edit or the temp
// file couldn't be created, in which case any error is left in m.message.
func (m *Model) startEdit() tea.Cmd {
	h := m.currentHeadline()
	if h == nil {
		return nil
	}

	tmp, err := os.CreateTemp("", "orgtd-edit-*.org")
	if err != nil {
		m.message = fmt.Sprintf("Could not create temp file: %v", err)
		return nil
	}
	path := tmp.Name()

	_, err = tmp.WriteString(org.RenderHeadline(h))
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
		return editFinishedMsg{path: path, target: h, err: err}
	})
}

// finishEdit reads back the edited entry, reparses it, and splices the
// result into the tree in place of the original headline. On any error
// (editor failure, unreadable file, or an edit that leaves nothing
// parseable) the original headline is left untouched and the error is
// shown on the status line.
func (m Model) finishEdit(msg editFinishedMsg) (tea.Model, tea.Cmd) {
	defer os.Remove(msg.path)

	if msg.err != nil {
		m.message = fmt.Sprintf("Editor exited with an error: %v", msg.err)
		return m, nil
	}

	data, err := os.ReadFile(msg.path)
	if err != nil {
		m.message = fmt.Sprintf("Could not read edited entry: %v", err)
		return m, nil
	}

	file, err := org.Parse(strings.NewReader(string(data)), "")
	if err != nil {
		m.message = fmt.Sprintf("Could not parse edited entry: %v", err)
		return m, nil
	}
	if len(file.Headlines) == 0 {
		m.message = "Edited entry had no headline; leaving it unchanged"
		return m, nil
	}

	m.markDirty(msg.target)
	m.replaceHeadline(msg.target, file.Headlines)
	m.rebuildRows()
	return m, nil
}

// replaceHeadline splices replacements into old's parent (or its file's
// top-level list) in place of old, carrying over old's own fold state to
// the first replacement, and marking every headline in the replacement
// subtree(s) as changed (the whole edited tree, not just its root).
func (m *Model) replaceHeadline(old *org.Headline, replacements []*org.Headline) {
	wasCollapsed := m.collapsed[old]
	org.Walk([]*org.Headline{old}, func(h *org.Headline) {
		delete(m.collapsed, h)
		delete(m.dirtyHeadlines, h)
	})

	for _, n := range replacements {
		n.Parent = old.Parent
	}
	if wasCollapsed && len(replacements) > 0 {
		m.collapsed[replacements[0]] = true
	}
	org.Walk(replacements, func(h *org.Headline) { m.dirtyHeadlines[h] = true })

	if old.Parent != nil {
		for i, c := range old.Parent.Children {
			if c == old {
				old.Parent.Children = spliceHeadlines(old.Parent.Children, i, replacements)
				return
			}
		}
		return
	}
	for _, f := range m.ws.Files {
		for i, top := range f.Headlines {
			if top == old {
				f.Headlines = spliceHeadlines(f.Headlines, i, replacements)
				return
			}
		}
	}
}

// spliceHeadlines returns a copy of list with the element at idx replaced
// by replacements (which may contain zero, one, or several headlines).
func spliceHeadlines(list []*org.Headline, idx int, replacements []*org.Headline) []*org.Headline {
	out := make([]*org.Headline, 0, len(list)-1+len(replacements))
	out = append(out, list[:idx]...)
	out = append(out, replacements...)
	out = append(out, list[idx+1:]...)
	return out
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
