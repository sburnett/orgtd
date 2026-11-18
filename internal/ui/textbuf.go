package ui

import (
	"slices"
	"strings"
	"unicode"
)

// textBuffer is the multi-line text insert mode edits (see insert_mode.go):
// an entry's own text, one string per line, and a caret within it. It's
// a plain value, and every edit builds a fresh lines slice rather than
// writing into the old one, so a Model copied before a keystroke (as
// Bubble Tea's value-typed models routinely are) never sees the edit.
type textBuffer struct {
	lines []string // never empty: an empty buffer is one empty line
	row   int      // caret line
	col   int      // caret position within lines[row], in runes (0..len)

	// goal is the column up/down try to return to, so moving through a
	// short line and on to a long one doesn't lose the original column,
	// as in any editor. -1 when no vertical move is in progress.
	goal int
}

// newTextBuffer returns a buffer holding text, caret at the very start.
func newTextBuffer(text string) textBuffer {
	return textBuffer{lines: strings.Split(normalizeNewlines(text), "\n"), goal: -1}
}

// normalizeNewlines turns CRLF and lone CR line endings — what a paste
// from some terminals or clipboards carries — into plain "\n".
func normalizeNewlines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

// String returns the buffer's text, lines joined by "\n".
func (b textBuffer) String() string { return strings.Join(b.lines, "\n") }

// runes returns line i as runes.
func (b textBuffer) runes(i int) []rune { return []rune(b.lines[i]) }

// replaceLines swaps lines [from, to) for repl, as a new slice.
func (b *textBuffer) replaceLines(from, to int, repl ...string) {
	b.lines = slices.Concat(b.lines[:from:from], repl, b.lines[to:])
}

// clamp keeps the caret within the buffer, e.g. after its text was
// replaced wholesale by an $EDITOR round trip.
func (b *textBuffer) clamp() {
	b.row = max(0, min(b.row, len(b.lines)-1))
	b.col = max(0, min(b.col, len(b.runes(b.row))))
}

// insert types s at the caret, which ends up just after it; s may span
// several lines (Enter, or a paste).
func (b *textBuffer) insert(s string) {
	b.goal = -1
	parts := strings.Split(normalizeNewlines(s), "\n")
	line := b.runes(b.row)
	before, after := string(line[:b.col]), string(line[b.col:])
	last := len(parts) - 1
	newCol := len([]rune(parts[last]))
	if last == 0 {
		newCol += b.col
	}
	parts[0] = before + parts[0]
	parts[last] += after
	b.replaceLines(b.row, b.row+1, parts...)
	b.row += last
	b.col = newCol
}

// deleteRange removes the text between two caret positions (r1, c1) and
// (r2, c2), the first at or before the second, leaving the caret at the
// first.
func (b *textBuffer) deleteRange(r1, c1, r2, c2 int) {
	b.goal = -1
	head := string(b.runes(r1)[:c1])
	tail := string(b.runes(r2)[c2:])
	b.replaceLines(r1, r2+1, head+tail)
	b.row, b.col = r1, c1
}

// backspace deletes the character before the caret, joining onto the
// previous line at the start of one.
func (b *textBuffer) backspace() {
	r, c := b.prevPos()
	if r != b.row || c != b.col {
		b.deleteRange(r, c, b.row, b.col)
	}
}

// deleteForward deletes the character under the caret, joining the next
// line on at the end of one.
func (b *textBuffer) deleteForward() {
	r, c := b.nextPos()
	if r != b.row || c != b.col {
		b.deleteRange(b.row, b.col, r, c)
	}
}

// deleteWordBack deletes back to the start of the word before the caret
// (ctrl-w, alt-backspace).
func (b *textBuffer) deleteWordBack() {
	r, c := b.wordStartBefore()
	b.deleteRange(r, c, b.row, b.col)
}

// deleteWordForward deletes to the end of the word after the caret (alt-d).
func (b *textBuffer) deleteWordForward() {
	r, c := b.wordEndAfter()
	b.deleteRange(b.row, b.col, r, c)
}

// killToLineStart deletes from the start of the line to the caret (ctrl-u).
func (b *textBuffer) killToLineStart() { b.deleteRange(b.row, 0, b.row, b.col) }

// killToLineEnd deletes from the caret to the end of the line, or joins
// the next line on if the caret is already there (ctrl-k, as in emacs).
func (b *textBuffer) killToLineEnd() {
	if n := len(b.runes(b.row)); b.col < n {
		b.deleteRange(b.row, b.col, b.row, n)
	} else {
		b.deleteForward()
	}
}

// prevPos is the caret position one character back, crossing to the end
// of the previous line from the start of one; the caret itself at the
// very start of the buffer.
func (b *textBuffer) prevPos() (int, int) {
	switch {
	case b.col > 0:
		return b.row, b.col - 1
	case b.row > 0:
		return b.row - 1, len(b.runes(b.row - 1))
	}
	return b.row, b.col
}

// nextPos is prevPos's mirror image.
func (b *textBuffer) nextPos() (int, int) {
	switch {
	case b.col < len(b.runes(b.row)):
		return b.row, b.col + 1
	case b.row < len(b.lines)-1:
		return b.row + 1, 0
	}
	return b.row, b.col
}

func (b *textBuffer) left()  { b.goal = -1; b.row, b.col = b.prevPos() }
func (b *textBuffer) right() { b.goal = -1; b.row, b.col = b.nextPos() }

func (b *textBuffer) lineStart() { b.goal = -1; b.col = 0 }
func (b *textBuffer) lineEnd()   { b.goal = -1; b.col = len(b.runes(b.row)) }

// vertical moves the caret dir lines up (-1) or down (+1), keeping to
// the goal column where the line is long enough; at the first/last line
// it goes to that line's start/end instead, like most text inputs.
func (b *textBuffer) vertical(dir int) {
	if b.goal < 0 {
		b.goal = b.col
	}
	r := b.row + dir
	switch {
	case r < 0:
		b.col, b.goal = 0, -1
		return
	case r >= len(b.lines):
		b.col, b.goal = len(b.runes(b.row)), -1
		return
	}
	b.row = r
	b.col = min(b.goal, len(b.runes(r)))
}

func (b *textBuffer) up()   { b.vertical(-1) }
func (b *textBuffer) down() { b.vertical(1) }

// wordStartBefore is where a backward word motion from the caret lands:
// back over any spaces (and line breaks), then over the word before them.
func (b *textBuffer) wordStartBefore() (int, int) {
	r, c := b.row, b.col
	for {
		line := b.runes(r)
		for c > 0 && unicode.IsSpace(line[c-1]) {
			c--
		}
		if c > 0 || r == 0 {
			for c > 0 && !unicode.IsSpace(line[c-1]) {
				c--
			}
			return r, c
		}
		r--
		c = len(b.runes(r))
	}
}

// wordEndAfter is wordStartBefore's mirror image.
func (b *textBuffer) wordEndAfter() (int, int) {
	r, c := b.row, b.col
	for {
		line := b.runes(r)
		for c < len(line) && unicode.IsSpace(line[c]) {
			c++
		}
		if c < len(line) || r == len(b.lines)-1 {
			for c < len(line) && !unicode.IsSpace(line[c]) {
				c++
			}
			return r, c
		}
		r++
		c = 0
	}
}

func (b *textBuffer) wordLeft()  { b.goal = -1; b.row, b.col = b.wordStartBefore() }
func (b *textBuffer) wordRight() { b.goal = -1; b.row, b.col = b.wordEndAfter() }
