package ui

import "testing"

func TestTextBufferInsertSplitsAndJoinsLines(t *testing.T) {
	b := newTextBuffer("hello world")
	b.col = 5
	b.insert("\nthere")
	if got := b.String(); got != "hello\nthere world" {
		t.Fatalf("after inserting a line break = %q", got)
	}
	if b.row != 1 || b.col != 5 {
		t.Errorf("caret = %d:%d, want 1:5 (just after the insert)", b.row, b.col)
	}
	b.col = 0
	b.backspace()
	if got := b.String(); got != "hellothere world" || b.row != 0 || b.col != 5 {
		t.Errorf("backspace at a line start = %q, caret %d:%d; want lines joined, caret at the join", got, b.row, b.col)
	}
}

func TestTextBufferPasteNormalizesLineEndings(t *testing.T) {
	b := newTextBuffer("")
	b.insert("a\r\nb\rc")
	if got := b.String(); got != "a\nb\nc" {
		t.Errorf("after pasting CRLF/CR text = %q, want plain newlines", got)
	}
}

func TestTextBufferEditsDoNotShareLinesWithCopies(t *testing.T) {
	a := newTextBuffer("one\ntwo")
	b := a
	b.col = 3
	b.insert("!")
	b.deleteForward()
	if got := a.String(); got != "one\ntwo" {
		t.Errorf("original after editing a copy = %q, want it untouched", got)
	}
}

func TestTextBufferDeleteForwardAtLineEndJoins(t *testing.T) {
	b := newTextBuffer("ab\ncd")
	b.lineEnd()
	b.deleteForward()
	if got := b.String(); got != "abcd" {
		t.Errorf("= %q, want abcd", got)
	}
}

func TestTextBufferVerticalKeepsGoalColumn(t *testing.T) {
	b := newTextBuffer("a long line\nab\nanother long one")
	b.col = 8
	b.down()
	if b.row != 1 || b.col != 2 {
		t.Fatalf("down onto a short line: caret %d:%d, want 1:2", b.row, b.col)
	}
	b.down()
	if b.row != 2 || b.col != 8 {
		t.Errorf("down past it: caret %d:%d, want 2:8 (back to the goal column)", b.row, b.col)
	}
	b.down()
	if b.row != 2 || b.col != len("another long one") {
		t.Errorf("down from the last line: caret %d:%d, want its end", b.row, b.col)
	}
	b.up()
	b.up()
	b.up()
	if b.row != 0 || b.col != 0 {
		t.Errorf("up from the first line: caret %d:%d, want its start", b.row, b.col)
	}
}

func TestTextBufferWordMotionsAndDeletes(t *testing.T) {
	b := newTextBuffer("foo bar  baz")
	b.lineEnd()
	b.wordLeft()
	if b.col != 9 {
		t.Errorf("word left from the end: col %d, want 9", b.col)
	}
	b.wordLeft()
	if b.col != 4 {
		t.Errorf("word left again: col %d, want 4 (over the spaces too)", b.col)
	}
	b.wordRight()
	if b.col != 7 {
		t.Errorf("word right: col %d, want 7", b.col)
	}
	b.deleteWordBack()
	if got := b.String(); got != "foo   baz" {
		t.Errorf("ctrl-w = %q, want %q", got, "foo   baz")
	}
	b.deleteWordForward()
	if got := b.String(); got != "foo " {
		t.Errorf("alt-d = %q, want %q", got, "foo ")
	}
}

func TestTextBufferWordBackCrossesLines(t *testing.T) {
	b := newTextBuffer("first\n  ")
	b.row, b.col = 1, 2
	b.deleteWordBack()
	if got := b.String(); got != "" {
		t.Errorf("ctrl-w over a blank line into the word before = %q, want it all gone", got)
	}
}

func TestTextBufferKills(t *testing.T) {
	b := newTextBuffer("hello world\nnext")
	b.col = 6
	b.killToLineEnd()
	if got := b.String(); got != "hello \nnext" {
		t.Fatalf("ctrl-k = %q", got)
	}
	b.killToLineEnd()
	if got := b.String(); got != "hello next" {
		t.Fatalf("ctrl-k at the line end = %q, want the next line joined", got)
	}
	b.killToLineStart()
	if got := b.String(); got != "next" || b.col != 0 {
		t.Errorf("ctrl-u = %q (col %d), want %q at col 0", got, b.col, "next")
	}
}

func TestWrapRunesRespectsWideCharacters(t *testing.T) {
	segs, lastW := wrapRunes([]rune("ab日本c"), 4)
	want := [][2]int{{0, 3}, {3, 5}}
	if len(segs) != len(want) || segs[0] != want[0] || segs[1] != want[1] {
		t.Errorf("segs = %v, want %v", segs, want)
	}
	if lastW != 3 {
		t.Errorf("last width = %d, want 3", lastW)
	}
	if segs, _ := wrapRunes(nil, 4); len(segs) != 1 || segs[0] != [2]int{0, 0} {
		t.Errorf("empty line segs = %v, want one empty segment", segs)
	}
}
