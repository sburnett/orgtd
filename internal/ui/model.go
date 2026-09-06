// Package ui implements the Bubble Tea viewer: a scrollable, foldable
// outline over every org file in a workspace. Read-only navigation only
// for now — no editing, no agenda, no Google integration.
package ui

import (
	"fmt"
	"path/filepath"
	"strings"

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
)

// row is one visible line in the outline: either a file header or a
// headline at some depth.
type row struct {
	file     *org.File // set for a file-header row
	headline *org.Headline
}

// Model is the Bubble Tea model for the viewer.
type Model struct {
	ws *workspace.Workspace

	collapsed map[*org.Headline]bool
	rows      []row

	cursor int
	offset int // index of the first visible row (for scrolling)

	width, height int

	pendingG bool
}

// New builds a viewer model over ws. Every headline starts expanded.
func New(ws *workspace.Workspace) Model {
	m := Model{
		ws:        ws,
		collapsed: make(map[*org.Headline]bool),
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

	case tea.KeyMsg:
		key := msg.String()

		wasPendingG := m.pendingG
		m.pendingG = false

		switch key {
		case "ctrl+c", "q":
			return m, tea.Quit

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

		case "ctrl+d":
			m.moveCursor(m.pageSize() / 2)

		case "ctrl+u":
			m.moveCursor(-m.pageSize() / 2)
		}

		m.ensureVisible()
		return m, nil
	}

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

	status := fmt.Sprintf(" %s  —  item %d/%d", m.ws.Dir, m.cursor+1, len(m.rows))
	b.WriteString(statusStyle.Render(status))

	return b.String()
}

func (m Model) renderRow(r row) string {
	if r.file != nil {
		return fileStyle.Render(filepath.Base(r.file.Path))
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

	line := indent + fold + " " + strings.Join(parts, " ")

	if len(h.Tags) > 0 {
		line += "  " + tagStyle.Render(":"+strings.Join(h.Tags, ":")+":")
	}

	if ts := planningSummary(h); ts != "" {
		line += "  " + timestampStyle.Render(ts)
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
