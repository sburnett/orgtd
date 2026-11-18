package ui

import (
	"regexp"
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sburnett/orgtd/internal/orgdate"
)

// Insert mode treats every org timestamp in the text — active <...> or
// inactive [...], on the planning line, in a property, or in the title
// or body — as a single character, as it does the TODO keyword (see
// insert_keyword.go). With the caret at a timestamp's start the whole
// timestamp is the caret; motions step over it, and deletions that
// would cut into it take it whole. Letters typed on it go nowhere;
// Enter opens a prompt to change its date (timestampMode), which takes
// what "gd" takes.

// timestampRe matches one org timestamp: a date, optionally followed by
// a weekday, time, repeater and so on, in <> or [] — each half of a
// range ("<...>--<...>") on its own.
var timestampRe = regexp.MustCompile(`<\d{4}-\d{2}-\d{2}(?: [^<>\n]*)?>|\[\d{4}-\d{2}-\d{2}(?: [^\[\]\n]*)?\]`)

// timestampSpans returns where line's timestamps are, as [start, end)
// rune offsets.
func timestampSpans(line string) [][2]int {
	var out [][2]int
	for _, loc := range timestampRe.FindAllStringIndex(line, -1) {
		start := utf8.RuneCountInString(line[:loc[0]])
		out = append(out, [2]int{start, start + utf8.RuneCountInString(line[loc[0]:loc[1]])})
	}
	return out
}

// insertTimestampAt returns the timestamp on row of insert mode's buffer
// that starts (start) or ends (!start) exactly at col.
func (m *Model) insertTimestampAt(row, col int, start bool) (span [2]int, ok bool) {
	for _, s := range timestampSpans(m.ins.buf.lines[row]) {
		if start && s[0] == col || !start && s[1] == col {
			return s, true
		}
	}
	return span, false
}

// insertTimestampAround returns the timestamp on row strictly containing
// col — somewhere the caret mustn't be.
func (m *Model) insertTimestampAround(row, col int) (span [2]int, ok bool) {
	for _, s := range timestampSpans(m.ins.buf.lines[row]) {
		if s[0] < col && col < s[1] {
			return s, true
		}
	}
	return span, false
}

// onTimestamp returns the timestamp the caret is on, if any.
func (m *Model) onTimestamp() (span [2]int, ok bool) {
	if m.ins.onKeyword {
		return span, false
	}
	return m.insertTimestampAt(m.ins.buf.row, m.ins.buf.col, true)
}

// insertTimestampKey handles the keys that act differently because of a
// timestamp: those typed on one, and deletions that would cut into one.
// handled is false for anything left to ordinary editing.
func (m *Model) insertTimestampKey(msg tea.KeyMsg) (handled bool) {
	b := &m.ins.buf

	// Deleting backward from just past a timestamp takes it whole —
	// Backspace, or a word deletion ending inside it.
	switch {
	case msg.Type == tea.KeyBackspace && !msg.Alt, msg.Type == tea.KeyCtrlH:
		if s, ok := m.insertTimestampAt(b.row, b.col, false); ok {
			b.deleteRange(b.row, s[0], b.row, b.col)
			return true
		}
	case msg.Type == tea.KeyCtrlW, msg.Type == tea.KeyBackspace && msg.Alt:
		r, c := b.wordStartBefore()
		if s, ok := m.insertTimestampAround(r, c); ok {
			c = s[0]
		}
		b.deleteRange(r, c, b.row, b.col)
		return true
	case msg.Type == tea.KeyRunes && msg.Alt && !msg.Paste && string(msg.Runes) == "d":
		r, c := b.wordEndAfter()
		if s, ok := m.insertTimestampAround(r, c); ok {
			c = s[1]
		}
		b.deleteRange(b.row, b.col, r, c)
		return true
	}

	s, on := m.onTimestamp()
	if !on {
		return false
	}
	switch msg.Type {
	case tea.KeyEnter:
		m.startTimestampPrompt(s)
		return true
	case tea.KeyDelete, tea.KeyCtrlD:
		b.deleteRange(b.row, s[0], b.row, s[1])
		return true
	case tea.KeySpace:
		return true
	case tea.KeyRunes:
		// Letters go nowhere (Enter changes the date); a paste is text
		// typed before the timestamp, and alt-b/alt-f are motions.
		return !msg.Paste && !msg.Alt
	}
	return false
}

// snapOutOfTimestamp keeps the caret off a timestamp's insides after an
// ordinary edit or motion from (row, col): onto its start, moving back
// or to another line, or just past its end, moving forward — so it's
// passed over as one character.
func (m *Model) snapOutOfTimestamp(row, col int) {
	b := &m.ins.buf
	s, ok := m.insertTimestampAround(b.row, b.col)
	if !ok {
		return
	}
	if b.row == row && b.col > col {
		b.col = s[1]
	} else {
		b.col = s[0]
	}
}

// startTimestampPrompt opens the date prompt for the timestamp at span
// on the caret's row, blank — the timestamp itself is still in view
// above it — ready for the new date.
func (m *Model) startTimestampPrompt(span [2]int) {
	m.ins.date = span
	m.ins.dateActive = m.ins.buf.runes(m.ins.buf.row)[span[0]] == '<'
	m.ins.dateLabel = nil
	m.ins.dateInput = ""
	m.mode = timestampMode
}

// dateLabel is text "gd" added for a new deadline — "DEADLINE: ", and
// maybe the planning line around it — from (r1, c1) to (r2, c2) in
// insert mode's buffer, taken back out if no date is given for it.
type dateLabel struct{ r1, c1, r2, c2 int }

// cancelNewDate takes back out the label "gd" added for a new deadline
// that's getting no date after all, returning the caret to the title.
func (m *Model) cancelNewDate() {
	if l := m.ins.dateLabel; l != nil {
		m.ins.buf.deleteRange(l.r1, l.c1, l.r2, l.c2)
		m.ins.dateLabel = nil
		m.ins.buf.row, m.ins.buf.col = 0, m.titleStartCol(m.ins.target, m.ins.buf.lines[0])
		m.ins.buf.clamp()
	}
}

// updateTimestampMode handles the date prompt opened on a timestamp in
// insert mode: Enter applies the typed date, as "gd" reads it — an
// empty one removes the timestamp — and Esc (or ctrl-c) returns to
// insert mode leaving it as it was. ctrl-u clears what's been typed and
// ctrl-w its last word. In a "gd" session, either way ends insert mode
// too, applying the edit.
func (m Model) updateTimestampMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type != tea.KeyEnter {
		m.message = ""
	}
	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		m.mode = insertMode
		m.ins.dateInput = ""
		m.cancelNewDate()
	case tea.KeyEnter:
		m.applyTimestampInput()
	case tea.KeyBackspace:
		if r := []rune(m.ins.dateInput); len(r) > 0 {
			m.ins.dateInput = string(r[:len(r)-1])
		}
	case tea.KeyCtrlU:
		m.ins.dateInput = ""
	case tea.KeyCtrlW:
		m.ins.dateInput = strings.TrimRight(m.ins.dateInput, " ")
		m.ins.dateInput = m.ins.dateInput[:strings.LastIndex(m.ins.dateInput, " ")+1]
	case tea.KeySpace:
		m.ins.dateInput += " "
	case tea.KeyRunes:
		m.ins.dateInput += typedText(msg.Runes)
	}
	if m.mode == insertMode && m.ins.exitAfterDate {
		// Resolved, one way or the other: "gd" is done.
		return m.finishInsert()
	}
	return m, nil
}

// applyTimestampInput replaces the timestamp the date prompt was opened
// on with the typed date, parsed as "gd" parses it, keeping only whether
// it was active <...> or inactive [...]; the caret ends up just past it.
// An empty input removes it; an invalid one is reported, leaving the
// prompt open to correct it.
func (m *Model) applyTimestampInput() {
	m.message = ""
	b := &m.ins.buf
	span := m.ins.date
	input := strings.TrimSpace(m.ins.dateInput)
	if input == "" && m.ins.dateLabel != nil {
		// No date for a new deadline: there's nothing to remove.
		m.mode = insertMode
		m.cancelNewDate()
		return
	}
	replacement := ""
	if input != "" {
		ts, err := orgdate.ParseDeadlineInput(input, m.now())
		if err != nil {
			m.message = err.Error()
			return
		}
		replacement = "<" + ts.Raw + ">"
		if !m.ins.dateActive {
			replacement = "[" + ts.Raw + "]"
		}
	}
	b.deleteRange(b.row, span[0], b.row, span[1])
	b.insert(replacement)
	m.mode = insertMode
	m.ins.dateInput = ""
	m.ins.dateLabel = nil
}
