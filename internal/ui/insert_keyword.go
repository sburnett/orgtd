package ui

import (
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sburnett/orgtd/internal/org"
)

// Insert mode treats an entry's TODO keyword as a single character: a
// slot at the very start of its first line that holds the keyword, or
// nothing. The caret can sit on it (insertSession.onKeyword), and while
// it does, the status picker's candidates are shown and only their
// shortcuts can be typed — each one changing the entry's state exactly
// as "r" would (see chooseInsertKeyword). Motions step over the keyword
// as one character, and deleting backward into it removes it whole.
// :reference, which shows a keyword as plain title text, has no slot
// (insertSession.keywords is false there).

// insertKeyword returns the configured keyword insert mode's first line
// starts with, and the column (in runes) its title starts at — past the
// keyword and the space after it — or "" and 0 if there's none, or no
// keyword slot at all (see insertSession.keywords).
func (m *Model) insertKeyword() (kw string, titleCol int) {
	if !m.ins.keywords {
		return "", 0
	}
	line := m.ins.buf.lines[0]
	for _, k := range m.keywords() {
		switch {
		case line == k.Name:
			return k.Name, utf8.RuneCountInString(k.Name)
		case strings.HasPrefix(line, k.Name+" "):
			return k.Name, utf8.RuneCountInString(k.Name) + 1
		}
	}
	return "", 0
}

// enterKeywordSlot puts the caret on the keyword slot.
func (m *Model) enterKeywordSlot() {
	m.ins.onKeyword = true
	m.ins.buf.row, m.ins.buf.col, m.ins.buf.goal = 0, 0, -1
}

// leaveKeywordSlot moves the caret from the keyword slot to the start of
// the title, just past it.
func (m *Model) leaveKeywordSlot() {
	_, titleCol := m.insertKeyword()
	m.ins.onKeyword = false
	m.ins.buf.row, m.ins.buf.col, m.ins.buf.goal = 0, titleCol, -1
}

// snapOutOfKeyword keeps the caret off the keyword's own characters
// after an ordinary edit or motion: anywhere inside it lands on the
// title's start instead (e.g. moving up from the body).
func (m *Model) snapOutOfKeyword() {
	if m.ins.onKeyword || m.ins.buf.row != 0 {
		return
	}
	if _, titleCol := m.insertKeyword(); m.ins.buf.col < titleCol {
		m.ins.buf.col = titleCol
	}
}

// insertKeywordKey handles the keys that act differently because of the
// keyword slot: every key while the caret is on it, and the motions and
// deletions that would cross into it from the title. handled is false
// for anything left to ordinary editing.
func (m *Model) insertKeywordKey(msg tea.KeyMsg) (handled bool) {
	kw, titleCol := m.insertKeyword()
	b := &m.ins.buf

	if m.ins.onKeyword {
		switch msg.Type {
		case tea.KeyRunes:
			switch {
			case msg.Paste:
				// Pasted text is title text, not a keyword choice.
				m.leaveKeywordSlot()
				return false
			case msg.Alt && string(msg.Runes) == "f":
				m.leaveKeywordSlot()
			case !msg.Alt && len(msg.Runes) == 1:
				for _, c := range m.statusCandidates() {
					if c.shortcut == msg.Runes[0] {
						m.chooseInsertKeyword(c)
						break
					}
				}
			}
		case tea.KeyRight, tea.KeyCtrlF, tea.KeyCtrlRight:
			m.leaveKeywordSlot()
		case tea.KeyEnd, tea.KeyCtrlE:
			m.leaveKeywordSlot()
			b.lineEnd()
		case tea.KeyDown, tea.KeyCtrlN:
			m.ins.onKeyword = false
			b.col = 0
			b.down()
			m.snapOutOfKeyword()
		case tea.KeyDelete, tea.KeyCtrlD:
			if kw != "" {
				m.setInsertKeyword("", "")
				m.enterKeywordSlot()
			}
		}
		// Anything else — other letters, Enter, Backspace, motions
		// further left — has nowhere to go from the slot.
		return true
	}

	atTitleStart := b.row == 0 && b.col == titleCol
	switch msg.Type {
	case tea.KeyLeft, tea.KeyCtrlB, tea.KeyCtrlLeft:
		if atTitleStart {
			m.enterKeywordSlot()
			return true
		}
	case tea.KeyRunes:
		if msg.Alt && !msg.Paste && string(msg.Runes) == "b" && atTitleStart {
			m.enterKeywordSlot()
			return true
		}
	case tea.KeyHome, tea.KeyCtrlA:
		// The line's start is the title's start, then the slot.
		if b.row == 0 {
			if atTitleStart {
				m.enterKeywordSlot()
			} else {
				b.col, b.goal = titleCol, -1
			}
			return true
		}
	case tea.KeyBackspace, tea.KeyCtrlH, tea.KeyCtrlW:
		if atTitleStart {
			// Backspace, ctrl-w and alt-backspace alike: the keyword
			// is the character (and the word) before the title.
			if kw != "" {
				m.removeInsertKeyword()
			}
			return true
		}
		wordWise := msg.Type == tea.KeyCtrlW || msg.Alt
		if r, c := b.wordStartBefore(); wordWise && r == 0 && c < titleCol {
			// Stop at the title's start rather than eating into the
			// keyword along with the word.
			b.deleteRange(0, titleCol, b.row, b.col)
			return true
		}
	case tea.KeyCtrlU:
		// ctrl-u removes everything before the caret on its line,
		// keyword included.
		if b.row == 0 && kw != "" {
			if b.col > titleCol {
				b.deleteRange(0, titleCol, 0, b.col)
			}
			m.removeInsertKeyword()
			return true
		}
	}
	return false
}

// removeInsertKeyword clears the keyword, leaving the caret at the
// title's start — the same state change as choosing "(none)".
func (m *Model) removeInsertKeyword() {
	m.setInsertKeyword("", "")
	m.leaveKeywordSlot()
}

// chooseInsertKeyword applies the status picker candidate c, typed on
// the keyword slot, as "r" does: completing the entry first asks for a
// closing note (see closingNoteForInsert), which then applies it.
func (m *Model) chooseInsertKeyword(c statusCandidate) {
	kw, _ := m.insertKeyword()
	if org.IsDoneKeyword(c.keyword) && !org.IsDoneKeyword(kw) {
		m.mode = closingNoteMode
		m.closingNoteKeyword = c.keyword
		m.closingNoteLabel = c.label
		m.closingNoteForInsert = true
		return
	}
	m.applyInsertKeyword(c.keyword, "")
}

// applyInsertKeyword sets keyword, as chooseInsertKeyword or the closing
// note it asked for resolves it, and moves the caret to the title's
// start, ready to type it.
func (m *Model) applyInsertKeyword(keyword, note string) {
	m.setInsertKeyword(keyword, note)
	m.ins.onKeyword = false
	_, titleCol := m.insertKeyword()
	if h := m.parsedInsertEntry(); h != nil {
		titleCol = m.titleStartCol(h, m.ins.buf.lines[0]) // past a priority cookie, too
	}
	m.ins.buf.row, m.ins.buf.col, m.ins.buf.goal = 0, titleCol, -1
}

// setInsertKeyword changes the keyword of the entry being edited, with
// everything "r" does along with it (see buildStatusChangeAction):
// stamping or clearing CLOSED, advancing a repeating item's dates
// instead of completing it, and logging note — applied right away, to
// the text being edited: the entry as typed so far is parsed, changed,
// and written back to the buffer, so it reaches the outline with the
// rest of the edit on Esc, as one undo step.
func (m *Model) setInsertKeyword(keyword, note string) {
	h := m.parsedInsertEntry()
	if h == nil {
		m.message = "Could not parse the entry to change its state"
		return
	}
	m.buildStatusChangeAction(h, keyword, note).apply(m)
	m.ins.buf.lines = newTextBuffer(entryEditText(h)).lines
	m.ins.buf.clamp()
}

// parsedInsertEntry parses insert mode's buffer into a stand-alone
// headline, as applyEditedEntry would on Esc; nil if it can't.
func (m *Model) parsedInsertEntry() *org.Headline {
	text := org.IndentEntry(m.ins.buf.String(), m.ins.target.Level)
	f, err := org.Parse(strings.NewReader(text), "")
	if err != nil || len(f.Headlines) == 0 {
		return nil
	}
	return f.Headlines[0]
}

// inInsertSession reports whether an insert-mode session is underway:
// in insert mode itself, in the closing-note prompt choosing a done
// keyword on its keyword slot opened, or in the date prompt Enter on a
// timestamp opened (see insert_timestamp.go).
func (m *Model) inInsertSession() bool {
	return m.mode == insertMode || m.mode == timestampMode || m.mode == closingNoteMode && m.closingNoteForInsert
}
