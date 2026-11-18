package ui

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/sburnett/orgtd/internal/orgdate"
)

// Insert mode treats every org timestamp in the text — active <...> or
// inactive [...], on the planning line, in a property, or in the title
// or body — as a single character, as it does the TODO keyword (see
// insert_keyword.go). With the caret at a timestamp's start the whole
// timestamp is the caret; motions step over it, and deletions that
// would cut into it take it whole. Letters typed on it go nowhere;
// Enter turns it into a field to type its new date in, in place, which
// takes what "gd" takes (see dateField).

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
	// Backspace, or a word deletion ending inside it. (Backspace on one
	// removes it too: see below.)
	switch {
	case msg.Type == tea.KeyBackspace && !msg.Alt, msg.Type == tea.KeyCtrlH:
		if _, on := m.onTimestamp(); on {
			break
		}
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
		m.openDateField(s)
		return true
	case tea.KeyDelete, tea.KeyCtrlD, tea.KeyBackspace, tea.KeyCtrlH:
		// The highlighted timestamp is the thing to delete — whichever
		// delete key it is (a Mac's "delete" is Backspace).
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

// dateField is a timestamp being retyped in place (see openDateField):
// the field shows in its spot, in its brackets, until Enter or Esc puts
// the date it resolves to there instead.
type dateField struct {
	row, start int        // where the timestamp starts in the buffer (its opening bracket)
	old        string     // the timestamp's text, brackets and all; "" for a new deadline "gd" is adding
	active     bool       // <...>, not [...]
	label      *dateLabel // what "gd" added for a new deadline, taken back out if it gets no date
	input      textBuffer // what's been typed: one line
}

// openDateField (Enter on a timestamp) turns the timestamp at span on
// the caret's row into an empty field to type its new date in.
func (m *Model) openDateField(span [2]int) {
	b := m.ins.buf
	old := string(b.runes(b.row)[span[0]:span[1]])
	m.ins.date = &dateField{row: b.row, start: span[0], old: old, active: old[0] == '<', input: newTextBuffer("")}
}

// dateLabel is text "gd" added for a new deadline — "DEADLINE: ", and
// maybe the planning line around it — from (r1, c1) to (r2, c2) in
// insert mode's buffer, taken back out if no date is given for it.
type dateLabel struct{ r1, c1, r2, c2 int }

// dateKey handles a key while a date is being typed (see dateField):
// Enter puts the date typed in place of the timestamp, Esc (or ctrl-c)
// does too and leaves insert mode, and anything else edits the field.
// With nothing typed, both keep the old date (or, for a new deadline,
// take its label back out); a date that doesn't parse keeps the field
// open, saying so. In a "gd" session, either ends insert mode.
func (m Model) dateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter, tea.KeyCtrlJ, tea.KeyEsc, tea.KeyCtrlC:
		if !m.closeDateField() {
			return m, nil
		}
		if msg.Type == tea.KeyEsc || msg.Type == tea.KeyCtrlC || m.ins.exitWhenDone {
			return m.finishInsert()
		}
		return m, nil
	}
	editOneLine(&m.ins.date.input, msg)
	return m, nil
}

// closeDateField resolves the date field: the typed date replaces the
// timestamp — active or inactive as it was — with the caret just past
// it; with nothing typed, the old one stays (a new deadline's label is
// taken back out). ok is false, the field left open with an error, if
// the typed date doesn't parse.
func (m *Model) closeDateField() (ok bool) {
	f := m.ins.date
	b := &m.ins.buf
	input := strings.TrimSpace(f.input.String())
	if input == "" {
		m.ins.date = nil
		if l := f.label; l != nil {
			b.deleteRange(l.r1, l.c1, l.r2, l.c2)
			b.row, b.col, b.goal = 0, m.titleStartCol(m.ins.target, b.lines[0]), -1
			b.clamp()
		} else {
			b.row, b.col = f.row, f.start // back on the timestamp
		}
		return true
	}
	ts, err := orgdate.ParseDeadlineInput(input, m.now())
	if err != nil {
		m.message = err.Error()
		return false
	}
	replacement := "<" + ts.Raw + ">"
	if !f.active {
		replacement = "[" + ts.Raw + "]"
	}
	m.ins.date = nil
	b.deleteRange(f.row, f.start, f.row, f.start+utf8.RuneCountInString(f.old))
	b.row, b.col = f.row, f.start
	b.insert(replacement)
	return true
}

// dateFieldDisplay returns the line the date field is on as drawn: the
// field in place of the timestamp, in its brackets, then a preview of
// what's been typed (see datePreview). caret is the rune the caret is
// on, and [p0, p1) the preview's runes, drawn in previewStyle.
func (m *Model) dateFieldDisplay() (rs []rune, caret, p0, p1 int, previewStyle lipgloss.Style) {
	f := m.ins.date
	line := m.ins.buf.runes(f.row)
	open, close := "[", "]"
	if f.active {
		open, close = "<", ">"
	}
	input := []rune(f.input.String())
	rs = append(rs, line[:f.start]...)
	rs = append(rs, []rune(open)...)
	caret = len(rs) + f.input.col
	rs = append(rs, input...)
	if f.input.col == len(input) {
		rs = append(rs, ' ') // a cell for the caret, past what's typed
	}
	rs = append(rs, []rune(close)...)
	preview, isErr := m.datePreview()
	p0 = len(rs)
	rs = append(rs, []rune(preview)...)
	p1 = len(rs)
	rs = append(rs, line[f.start+utf8.RuneCountInString(f.old):]...)
	previewStyle = m.statusStyle()
	if isErr {
		previewStyle = m.errorStyle()
	}
	return rs, caret, p0, p1, previewStyle
}

// datePreview is what the date field's preview says: what the typed date
// resolves to, and how far off that is ("→ Fri 2026-11-20, in 3 days");
// why it doesn't resolve (isErr); or, with nothing typed yet, the date
// it's replacing — or some examples, for a new deadline.
func (m *Model) datePreview() (text string, isErr bool) {
	f := m.ins.date
	input := strings.TrimSpace(f.input.String())
	if input == "" {
		if f.old == "" {
			return "  e.g. 2026-12-25, 3d, 2w, next tue", false
		}
		return "  currently " + f.old, false
	}
	ts, err := orgdate.ParseDeadlineInput(input, m.now())
	if err != nil {
		return "  ✗ not a date", true
	}
	t, hasTime, err := orgdate.ParseFlexible(ts.Raw)
	if err != nil {
		return "  → " + ts.Raw, false
	}
	when := t.Format("Mon 2006-01-02")
	if hasTime {
		when += t.Format(" 15:04")
	}
	return "  → " + when + ", " + relativeDays(orgdate.TruncateToDate(t), orgdate.TruncateToDate(m.now())), false
}

// relativeDays says how far day is from today, in words.
func relativeDays(day, today time.Time) string {
	n := int(math.Round(day.Sub(today).Hours() / 24))
	switch {
	case n == 0:
		return "today"
	case n == 1:
		return "tomorrow"
	case n == -1:
		return "yesterday"
	case n > 0:
		return fmt.Sprintf("in %d days", n)
	}
	return fmt.Sprintf("%d days ago", -n)
}
