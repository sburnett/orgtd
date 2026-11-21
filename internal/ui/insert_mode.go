package ui

import (
	"math"
	"regexp"
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

	// keywords is whether the first line's TODO keyword is a slot of its
	// own (see insert_keyword.go) — everywhere but :reference — and
	// onKeyword whether the caret is on it, rather than at buf's row/col.
	keywords  bool
	onKeyword bool

	// date is the timestamp being retyped in place, if one is (see
	// insert_timestamp.go).
	date *dateField

	// exitWhenDone ends the session as soon as a date prompt is
	// resolved, applied or cancelled — for "gd", which only uses insert
	// mode to set the deadline.
	exitWhenDone bool

	// note is the closing note being typed, if one is (see insert_note.go).
	note *noteEdit

	// statusOnly marks an "r" session (see startStatusEdit): only there
	// to set the keyword, which is applied to the entry itself as soon as
	// it's chosen, ending insert mode. Moving off the keyword slot makes
	// it an ordinary edit.
	statusOnly bool

	// inTags is whether the caret is in the tag area at the end of the
	// first line, where tags — lifted out of buf meanwhile, with an
	// empty one at the end for a new tag — are edited one at a time,
	// tagIdx the one under the caret (see insert_tags.go).
	inTags bool
	tags   []string
	tagIdx int
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
		buf.lineStart() // and onto the keyword slot, below
	case caretAfterTitleStart:
		// Past the end of the line only if there's nothing there, as
		// vim's "a" on an empty line.
		buf.col = min(m.titleStartCol(h, buf.lines[0])+1, len(buf.runes(0)))
	case caretAtLineEnd:
		buf.col, _ = splitTitleTags(buf.lines[0]) // the title's end, before any tags
	}
	m.ins = insertSession{target: h, insert: insert, buf: buf, keywords: m.view != referenceView}
	if caret == caretAtLineStart && m.ins.keywords {
		m.enterKeywordSlot()
	}
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
	m.leaveTags()
	m.dropDatelessPlanningLabels()
	s := m.ins
	m.mode = normalMode
	m.ins = insertSession{}
	m.applyEditedEntry(s.buf.String(), s.target, s.insert)
	// As "r" does, with :review on: past the pinned item, if this just
	// closed it.
	m.advanceReviewTargetIfDone()
	m.ensureVisible()
	return m, nil
}

// editInsertInEditor (ctrl-g in insert mode) opens what's been typed so
// far in $EDITOR, with the cursor right where the caret is (see
// extprog.CaretAt) but in the editor's normal mode; once it exits, the result comes back into insert mode
// (see resumeInsertFromEditor) to carry on with, or apply with Esc — as
// ctrl-g does in a shell's or Claude Code's prompt. Insert mode stays on
// throughout, with the TUI suspended while the editor runs.
func (m *Model) editInsertInEditor() tea.Cmd {
	b := m.ins.buf
	text := b.String()
	return m.launchEditorText(m.ins.target, text, m.ins.insert, extprog.CaretAt(text, b.row, b.col), true)
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
	m.ins.onKeyword = false
	m.snapOutOfKeyword()
	m.snapOutOfTags()
	m.snapOutOfTimestamp(m.ins.buf.row, m.ins.buf.col)
}

// updateInsertMode handles a key in insert mode: text and the usual
// line-editing keys (arrows, home/end, and the emacs/readline ones a
// terminal prompt has) go to the buffer; Esc or ctrl-c applies it and
// returns to normal mode; ctrl-g opens it in $EDITOR.
func (m Model) updateInsertMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.message = ""
	if m.ins.note != nil {
		return m.noteKey(msg)
	}
	if m.ins.date != nil {
		return m.dateKey(msg)
	}
	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		return m.finishInsert()
	case tea.KeyCtrlG:
		m.leaveTags()
		return m, m.editInsertInEditor()
	}
	if m.insertTagsKey(msg) || m.insertKeywordKey(msg) || m.insertTimestampKey(msg) {
		return m, nil
	}
	fromRow, fromCol := m.ins.buf.row, m.ins.buf.col
	b := m.ins.buf
	switch msg.Type {
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
	m.snapOutOfKeyword()
	m.snapOutOfTags()
	m.snapOutOfTimestamp(fromRow, fromCol)
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
	return m.inInsertSession() && m.currentHeadline() == m.ins.target
}

// keepInsertTargetInView keeps the cursor on the entry insert mode is
// editing, the only place the buffer is drawn, should anything that
// runs meanwhile (a background calendar sync, a resize) rebuild the
// rows out from under it — falling back to the outline, which lists
// every entry, if the current view no longer shows it at all.
func (m *Model) keepInsertTargetInView() {
	if !m.inInsertSession() || m.currentHeadline() == m.ins.target {
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
// rows, at its indentation and on insertBg, with the rows before and
// after it around it as usual but faint (see dimRow) — scrolled, if
// the buffer has grown, so that the caret's line always stays on screen.
func (m Model) listingWithInsert() []string {
	bg := m.insertBg()
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
		pre = append(pre, m.dimRow(m.rows[i]))
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
		lines = append(lines, m.dimRow(m.rows[i]))
	}
	for len(lines) < budget {
		lines = append(lines, "")
	}
	return lines
}

// dimRow draws r, a row other than the one being edited, faint (SGR 2)
// but otherwise in its own colors — a subtle step back, so the entry
// being edited stands out without the rest becoming hard to read. The
// row's own styling resets intensity as it goes (bold off, or a full
// reset, is SGR 22 or 0), so faint is set again after every SGR
// sequence in it.
func (m Model) dimRow(r row) string {
	const faint, normal = "\x1b[2m", "\x1b[22m"
	return faint + sgrRe.ReplaceAllString(m.renderRow(r), "${0}"+faint) + normal
}

// sgrRe matches one SGR (styling) escape sequence.
var sgrRe = regexp.MustCompile(`\x1b\[[0-9;:]*m`)

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
// drawn on the character it sits on, or past the end of its line — or,
// on a timestamp, over all of it; in the tag area, over the tag (see
// tagsDisplay); on the keyword slot, over the whole keyword, or as a blank cell before
// the title if there's none; caret is the index of the screen line
// holding it.
func (m Model) insertEditorLines(lead, blank string, bg lipgloss.TerminalColor) (lines []string, caret int) {
	avail := math.MaxInt
	if m.width > 0 {
		avail = max(1, m.width-lipgloss.Width(lead))
	}
	b := m.ins.buf
	kw, _ := m.insertKeyword()
	text := lipgloss.NewStyle().Background(bg)
	for i, line := range b.lines {
		rs := []rune(line)
		// The caret covers runes [c0, c1) of this line, if it's on it.
		c0, c1 := -1, -1
		if i == b.row {
			c0, c1 = b.col, b.col+1
			if s, ok := m.onTimestamp(); ok {
				c1 = s[1] // the whole timestamp is the caret
			}
		}
		if i == 0 && m.ins.inTags {
			rs, c0, c1 = m.tagsDisplay()
		}
		p0, p1, previewStyle := -1, -1, lipgloss.NewStyle()
		if d := m.ins.date; d != nil && i == d.row {
			rs, c0, c1, p0, p1, previewStyle = m.dateFieldDisplay()
			previewStyle = previewStyle.Background(bg)
		}
		if n := m.ins.note; n != nil && i == n.row && n.prefix < len(rs) {
			// The caret covers the whole note typed so far, as a tag's
			// does; an empty one gets a blank cell past the line's end.
			c0, c1 = n.prefix, len(rs)
		}
		if i == 0 && m.ins.onKeyword {
			if n := utf8.RuneCountInString(kw); n > 0 {
				c0, c1 = 0, n
			} else {
				rs = append([]rune{' '}, rs...)
				c0, c1 = 0, 1
			}
		}
		atEnd := c0 == len(rs)
		segs, lastW := wrapRunes(rs, avail)
		if atEnd && lastW >= avail {
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
			// Each rune is drawn as text, the caret, or the date
			// field's preview, in runs of the same kind.
			const asText, asCaret, asPreview = 0, 1, 2
			kind := func(k int) int {
				switch {
				case c0 >= 0 && !atEnd && k >= c0 && k < c1:
					return asCaret
				case k >= p0 && k < p1:
					return asPreview
				}
				return asText
			}
			styles := [...]lipgloss.Style{asText: text, asCaret: m.caretStyle(), asPreview: previewStyle}
			for k := seg[0]; k < seg[1]; {
				end := k + 1
				for end < seg[1] && kind(end) == kind(k) {
					end++
				}
				s.WriteString(styles[kind(k)].Render(strings.ReplaceAll(string(rs[k:end]), "\t", " ")))
				k = end
			}
			if c0 >= 0 && !atEnd && c0 >= seg[0] && c0 < seg[1] {
				caret = len(lines)
			}
			if atEnd && j == len(segs)-1 {
				s.WriteString(m.caretStyle().Render(" "))
				caret = len(lines)
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
