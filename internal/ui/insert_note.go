package ui

import (
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
)

// Choosing a done state for one entry — with "r", or on insert mode's
// keyword slot — asks for a closing note right in the entry: the state
// change goes into the text being edited at once (keyword, CLOSED stamp,
// or a repeating item's advanced dates), along with the note's heading
// and an empty line under it, the caret on that line. Enter confirms it
// (a blank note is left out); Esc does too, and leaves insert mode, as
// it always does. Backing out is "u", which undoes the state change and
// note together.

// noteEdit is insert mode typing a closing note (see beginNote).
type noteEdit struct {
	keyword string // the done state chosen

	// before is the buffer as it was before the state change, and
	// beforeOnKeyword whether the caret was on the keyword slot: what
	// confirming re-applies the state change to, with the typed note.
	before          textBuffer
	beforeOnKeyword bool

	// row is the note's line in the buffer, and prefix how many runes
	// of it come before the note's text (its indentation).
	row, prefix int

	// direct is set for "r" (see startStatusEdit): insert mode is only
	// where the note is typed, so confirming it applies the state change
	// to the entry itself, exactly as "r" always has, and ends insert
	// mode.
	direct bool
}

// noteSentinel stands in for the note's text while laying out its lines,
// so they can be found in the buffer afterward.
const noteSentinel = "orgtdclosingnotehere"

// beginNote applies keyword, a done state, to the text being edited and
// puts the caret on an empty closing note for it.
func (m *Model) beginNote(keyword string) {
	before, onKeyword := m.ins.buf, m.ins.onKeyword
	m.setInsertKeyword(keyword, noteSentinel)
	b := &m.ins.buf
	for row, line := range b.lines {
		if i := strings.Index(line, noteSentinel); i >= 0 {
			b.lines = append(append([]string(nil), b.lines[:row]...), b.lines[row:]...)
			b.lines[row] = line[:i] + line[i+len(noteSentinel):]
			prefix := utf8.RuneCountInString(line[:i])
			b.row, b.col, b.goal = row, prefix, -1
			m.ins.onKeyword = false
			m.ins.note = &noteEdit{keyword: keyword, before: before, beforeOnKeyword: onKeyword, row: row, prefix: prefix}
			return
		}
	}
	// The state change asked for no note after all (or the entry
	// couldn't be parsed): it's applied as it stands.
	m.ins.buf, m.ins.onKeyword = before, onKeyword
	m.applyInsertKeyword(keyword, "")
}

// noteText is the closing note typed so far.
func (m *Model) noteText() string {
	n := m.ins.note
	return string([]rune(m.ins.buf.lines[n.row])[n.prefix:])
}

// confirmNote (Enter) keeps the state change with the note typed, or
// with none if it's blank, the caret moving to the title's start.
func (m *Model) confirmNote() {
	n := m.ins.note
	note := strings.TrimSpace(m.noteText())
	m.ins.note = nil
	m.ins.buf, m.ins.onKeyword = n.before, n.beforeOnKeyword
	m.applyInsertKeyword(n.keyword, note)
}

// noteKey handles a key while typing a closing note: Enter confirms it,
// Esc (or ctrl-c) confirms it and leaves insert mode, and everything
// else edits the note's one line — never leaving it.
func (m Model) noteKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter, tea.KeyCtrlJ, tea.KeyEsc, tea.KeyCtrlC:
		if m.ins.note.direct {
			m.endDirectNote()
			return m, nil
		}
		m.confirmNote()
		if msg.Type == tea.KeyEsc || msg.Type == tea.KeyCtrlC {
			return m.finishInsert()
		}
		return m, nil
	}

	n := m.ins.note
	b := &m.ins.buf
	line := textBuffer{lines: []string{m.noteText()}, col: max(0, b.col-n.prefix), goal: -1}
	editOneLine(&line, msg)
	prefix := string([]rune(b.lines[n.row])[:n.prefix])
	b.lines = append([]string(nil), b.lines...)
	b.lines[n.row] = prefix + line.lines[0]
	b.row, b.col = n.row, n.prefix+line.col
	return m, nil
}

// endDirectNote ends an "r" closing note (see noteEdit.direct) and
// insert mode with it, applying the state change to the entry itself
// with the note typed — or none, if it's blank.
func (m *Model) endDirectNote() {
	keyword, note := m.ins.note.keyword, strings.TrimSpace(m.noteText())
	m.mode = normalMode
	m.ins = insertSession{}
	m.applyStatus(keyword, note)
	m.ensureVisible()
}

// editOneLine applies an editing key to line, a single line of text — a
// closing note, or a date being typed (see dateField) — that the caret
// never leaves: the usual line-editing keys, and text typed or pasted,
// a line break or tab in it becoming a space.
func editOneLine(line *textBuffer, msg tea.KeyMsg) {
	switch msg.Type {
	case tea.KeySpace:
		line.insert(" ")
	case tea.KeyRunes:
		switch {
		case msg.Alt && !msg.Paste && string(msg.Runes) == "b":
			line.wordLeft()
		case msg.Alt && !msg.Paste && string(msg.Runes) == "f":
			line.wordRight()
		case msg.Alt && !msg.Paste && string(msg.Runes) == "d":
			line.deleteWordForward()
		default:
			line.insert(strings.Map(func(r rune) rune {
				if r == '\n' || r == '\r' || r == '\t' {
					return ' '
				}
				return r
			}, typedText(msg.Runes)))
		}
	case tea.KeyBackspace, tea.KeyCtrlH:
		if msg.Alt {
			line.deleteWordBack()
		} else {
			line.backspace()
		}
	case tea.KeyDelete, tea.KeyCtrlD:
		line.deleteForward()
	case tea.KeyCtrlW:
		line.deleteWordBack()
	case tea.KeyCtrlU:
		line.killToLineStart()
	case tea.KeyCtrlK:
		line.killToLineEnd()
	case tea.KeyLeft, tea.KeyCtrlB:
		line.left()
	case tea.KeyRight, tea.KeyCtrlF:
		line.right()
	case tea.KeyCtrlLeft:
		line.wordLeft()
	case tea.KeyCtrlRight:
		line.wordRight()
	case tea.KeyHome, tea.KeyCtrlA:
		line.lineStart()
	case tea.KeyEnd, tea.KeyCtrlE:
		line.lineEnd()
	}
}
