package ui

import (
	"math"
	"strings"
	"unicode"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/sburnett/orgtd/internal/extprog"
	"github.com/sburnett/orgtd/internal/org"
)

// insertSession is what insert mode is editing: an entry's own text —
// the same bullet-free, dedented buffer an $EDITOR session gets (see
// entryEditText), minus the comment trailer — typed into right in the
// outline instead of in an external program. Entered with i/I/A on an
// existing entry, or o/O/gC/gX/:capture on a new one — in the outline,
// if the current view doesn't list it (see insertHeadlineAt); Esc
// applies it, ctrl-g hands it to $EDITOR and back.
type insertSession struct {
	target *org.Headline  // the entry being edited, or o/O's tentative placeholder
	insert *insertContext // non-nil for a new entry (o/O, capture); see finishEdit's own insert sessions
	buf    textBuffer
}

// caretStart is where insert mode puts the caret on entering it.
type caretStart int

const (
	caretAtTitle         caretStart = iota // "i": the start of the title (see titleStartCol)
	caretAtLineStart                       // "I": the very start of the first line, before any keyword
	caretAfterTitleStart                   // "a": one character past that, as vim's "a" is past its cursor
	caretAtLineEnd                         // "A": the end of the first line
)

// enterInsertMode starts editing h in place, caret placed per caret.
// insert is non-nil when h is a tentative new entry (see
// insertHeadlineAt), committed or rolled back once insert mode ends.
func (m *Model) enterInsertMode(h *org.Headline, insert *insertContext, caret caretStart) {
	buf := newTextBuffer(entryEditText(h))
	switch caret {
	case caretAtTitle:
		buf.col = m.titleStartCol(h, buf.lines[0])
	case caretAtLineStart:
		buf.lineStart()
	case caretAfterTitleStart:
		// Past the end of the line only if there's nothing there, as
		// vim's "a" on an empty line.
		buf.col = min(m.titleStartCol(h, buf.lines[0])+1, len(buf.runes(0)))
	case caretAtLineEnd:
		buf.lineEnd()
	}
	m.ins = insertSession{target: h, insert: insert, buf: buf}
	m.mode = insertMode
	m.pendingCount = 0
}

// titleStartCol is where "i" puts the caret on line, h's first buffer
// line: past its TODO keyword and priority cookie ("TODO [#A] "), at the
// start of the title text itself, which is what's usually edited. In
// :reference, which shows a keyword as plain title text (see
// renderPlainTitle), that's the very start of the line instead.
func (m *Model) titleStartCol(h *org.Headline, line string) int {
	if m.view == referenceView {
		return 0
	}
	var prefix string
	if h.Keyword != "" {
		prefix += h.Keyword + " "
	}
	if h.Priority != "" {
		prefix += "[#" + h.Priority + "] "
	}
	if !strings.HasPrefix(line, prefix) {
		return 0
	}
	return utf8.RuneCountInString(prefix)
}

// finishInsert leaves insert mode (Esc), applying what was typed exactly
// as if it had come back from $EDITOR (see applyEditedEntry): one undo
// step for the whole session, as vim records "i, type, Esc"; a new entry
// left with no title is dropped again.
func (m Model) finishInsert() (tea.Model, tea.Cmd) {
	s := m.ins
	m.mode = normalMode
	m.ins = insertSession{}
	m.applyEditedEntry(s.buf.String(), s.target, s.insert)
	m.ensureVisible()
	return m, nil
}

// editInsertInEditor (ctrl-g in insert mode) opens what's been typed so
// far in $EDITOR — a vim-family one in insert mode, right where the
// caret is (see extprog.InsertAt); once it exits, the result comes back into insert mode
// (see resumeInsertFromEditor) to carry on with, or apply with Esc — as
// ctrl-g does in a shell's or Claude Code's prompt. Insert mode stays on
// throughout, with the TUI suspended while the editor runs.
func (m *Model) editInsertInEditor() tea.Cmd {
	b := m.ins.buf
	text := b.String()
	return m.launchEditorText(m.ins.target, text, m.ins.insert, extprog.InsertAt(text, b.row, b.col), true)
}

// resumeInsertFromEditor replaces insert mode's buffer with text, what
// ctrl-g's $EDITOR session came back with, keeping the caret where it
// was as far as the new text allows.
func (m *Model) resumeInsertFromEditor(text string) {
	if m.mode != insertMode {
		return
	}
	b := m.ins.buf
	b.lines = newTextBuffer(text).lines
	b.goal = -1
	b.clamp()
	m.ins.buf = b
}

// updateInsertMode handles a key in insert mode: text and the usual
// line-editing keys (arrows, home/end, and the emacs/readline ones a
// terminal prompt has) go to the buffer; Esc or ctrl-c applies it and
// returns to normal mode; ctrl-g opens it in $EDITOR.
func (m Model) updateInsertMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.message = ""
	b := m.ins.buf
	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		return m.finishInsert()
	case tea.KeyCtrlG:
		return m, m.editInsertInEditor()
	case tea.KeyEnter, tea.KeyCtrlJ:
		b.insert("\n")
	case tea.KeySpace:
		b.insert(" ")
	case tea.KeyRunes:
		switch {
		case msg.Alt && !msg.Paste && string(msg.Runes) == "b":
			b.wordLeft()
		case msg.Alt && !msg.Paste && string(msg.Runes) == "f":
			b.wordRight()
		case msg.Alt && !msg.Paste && string(msg.Runes) == "d":
			b.deleteWordForward()
		default:
			b.insert(typedText(msg.Runes))
		}
	case tea.KeyBackspace, tea.KeyCtrlH:
		if msg.Alt {
			b.deleteWordBack()
		} else {
			b.backspace()
		}
	case tea.KeyDelete, tea.KeyCtrlD:
		b.deleteForward()
	case tea.KeyCtrlW:
		b.deleteWordBack()
	case tea.KeyCtrlU:
		b.killToLineStart()
	case tea.KeyCtrlK:
		b.killToLineEnd()
	case tea.KeyLeft, tea.KeyCtrlB:
		if msg.Alt {
			b.wordLeft()
		} else {
			b.left()
		}
	case tea.KeyRight, tea.KeyCtrlF:
		if msg.Alt {
			b.wordRight()
		} else {
			b.right()
		}
	case tea.KeyCtrlLeft:
		b.wordLeft()
	case tea.KeyCtrlRight:
		b.wordRight()
	case tea.KeyUp, tea.KeyCtrlP:
		b.up()
	case tea.KeyDown, tea.KeyCtrlN:
		b.down()
	case tea.KeyHome, tea.KeyCtrlA:
		b.lineStart()
	case tea.KeyEnd, tea.KeyCtrlE:
		b.lineEnd()
	}
	m.ins.buf = b
	return m, nil
}

// typedText is runes as text to insert: control characters — which only
// a paste could deliver — are dropped, except the line breaks and tabs
// that are ordinary text.
func typedText(runes []rune) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
			return -1
		}
		return r
	}, string(runes))
}

// insertInline reports whether the entry insert mode is editing is the
// one under the cursor, so it's drawn in place of the entry's own rows
// (see listingWithInsert). keepInsertTargetInView sees to it that it
// always is.
func (m *Model) insertInline() bool {
	return m.mode == insertMode && m.currentHeadline() == m.ins.target
}

// keepInsertTargetInView keeps the cursor on the entry insert mode is
// editing, the only place the buffer is drawn, should anything that
// runs meanwhile (a background calendar sync, a resize) rebuild the
// rows out from under it — falling back to the outline, which lists
// every entry, if the current view no longer shows it at all.
func (m *Model) keepInsertTargetInView() {
	if m.mode != insertMode || m.currentHeadline() == m.ins.target {
		return
	}
	m.focusHeadline(m.ins.target)
	if m.currentHeadline() != m.ins.target {
		m.switchToViewNoJump(outlineView)
		m.focusHeadline(m.ins.target)
	}
}

// listingWithInsert is listing (see view.go) while insert mode is
// editing the entry under the cursor: the buffer replaces that entry's
// rows, at its indentation, with the rows before and after it around it
// as usual — scrolled, if the buffer has grown, so that the caret's line
// always stays on screen.
func (m Model) listingWithInsert(bg lipgloss.TerminalColor) []string {
	budget := m.contentBudget()
	cs, ce := m.cursor, m.entryEnd(m.cursor)
	start := min(m.offset, cs)

	lead, blank := m.insertLead(m.rows[cs], bg)
	edit, caret := m.insertEditorLines(lead, blank, bg)
	if len(edit) > budget {
		top := max(0, caret-budget+1)
		edit = edit[top : top+budget]
	}

	var pre []string
	for i := start; i < cs; i++ {
		if i > start && m.rows[i].kind == rowSection {
			pre = append(pre, "")
		}
		pre = append(pre, m.renderRow(m.rows[i]))
	}
	if room := budget - len(edit); len(pre) > room {
		pre = pre[len(pre)-room:]
	}

	lines := append(pre, edit...)
	for i := ce + 1; i < len(m.rows) && len(lines) < budget; i++ {
		if m.rows[i].kind == rowSection {
			lines = append(lines, "")
			if len(lines) >= budget {
				break
			}
		}
		lines = append(lines, m.renderRow(m.rows[i]))
	}
	for len(lines) < budget {
		lines = append(lines, "")
	}
	return lines
}

// insertLead returns what the buffer's first line (lead) and every later
// line (blank) start with, matching how r, the entry's own row, lays out
// its gutter and indentation — so editing doesn't shift the entry
// sideways.
func (m Model) insertLead(r row, bg lipgloss.TerminalColor) (lead, blank string) {
	switch r.kind {
	case rowHeadline, rowCalendarEvent:
		c := m.entryContent(r, bg)
		return c.lead, c.blank
	}
	return m.gutterColumns(r.headline, bg), bgSpan(bg, "     ")
}

// insertEditorLines draws insert mode's buffer as screen lines, each
// line of text wrapped (at any character, as vim's own 'wrap' does) to
// the width left after lead — what the first line starts with — or
// blank, what every later one does, and padded out with bg. The caret is
// drawn on the character it sits on, or past the end of its line; caret
// is the index of the screen line holding it.
func (m Model) insertEditorLines(lead, blank string, bg lipgloss.TerminalColor) (lines []string, caret int) {
	avail := math.MaxInt
	if m.width > 0 {
		avail = max(1, m.width-lipgloss.Width(lead))
	}
	b := m.ins.buf
	text := lipgloss.NewStyle().Background(bg)
	for i, line := range b.lines {
		rs := []rune(line)
		segs, lastW := wrapRunes(rs, avail)
		caretLine := i == b.row
		if caretLine && b.col == len(rs) && lastW >= avail {
			// No room left for the caret after the last character.
			segs = append(segs, [2]int{len(rs), len(rs)})
		}
		for j, seg := range segs {
			prefix := blank
			if i == 0 && j == 0 {
				prefix = lead
			}
			var s strings.Builder
			s.WriteString(prefix)
			plain := func(from, to int) {
				if from < to {
					s.WriteString(text.Render(strings.ReplaceAll(string(rs[from:to]), "\t", " ")))
				}
			}
			onCaret := caretLine && (b.col >= seg[0] && b.col < seg[1] || j == len(segs)-1 && b.col == seg[1])
			if onCaret {
				caret = len(lines)
				plain(seg[0], b.col)
				under := " "
				if b.col < seg[1] {
					under = strings.ReplaceAll(string(rs[b.col]), "\t", " ")
				}
				s.WriteString(m.caretStyle().Render(under))
				plain(min(b.col+1, seg[1]), seg[1])
			} else {
				plain(seg[0], seg[1])
			}
			lines = append(lines, m.padLineToWidth(s.String(), bg))
		}
	}
	return lines, caret
}

// wrapRunes splits rs into runs of at most width display columns — at
// least one, an empty one for an empty line — returned as [start, end)
// rune indexes, along with the width of the last.
func wrapRunes(rs []rune, width int) (segs [][2]int, lastW int) {
	start, w := 0, 0
	for k, r := range rs {
		rw := 1
		if r != '\t' {
			rw = ansi.StringWidth(string(r))
		}
		if w+rw > width && k > start {
			segs = append(segs, [2]int{start, k})
			start, w = k, 0
		}
		w += rw
	}
	return append(segs, [2]int{start, len(rs)}), w
}
