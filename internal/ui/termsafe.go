package ui

import (
	"strings"
	"unicode/utf8"
)

// sanitizeTerminal strips everything from s that a terminal would act
// on rather than just display, except SGR (color/style) sequences, which
// is all orgtd's own rendering (lipgloss, glamour) ever emits. It's
// applied to View's whole output, the one place every byte headed for
// the terminal passes through, because org text isn't trusted: a synced
// calendar invite, a git pull, or a share-sheet capture can put raw
// escape sequences in a title or body. Left in, those could write the
// system clipboard (OSC 52), retitle the window, or make the terminal
// answer a query by "typing" its reply into orgtd's own input.
//
// Kept: "\n", "\t", printable text, and well-formed CSI sequences ending
// in 'm' whose parameters are only digits, ';' and ':'. Dropped: every
// other escape sequence in full (OSC/DCS/APC/PM/SOS up to their
// terminator, any other CSI, two-byte ESC sequences), every other C0
// control and DEL, C1 controls (U+0080-U+009F, which some terminals
// treat as 8-bit CSI/OSC/...), and invalid UTF-8 bytes (a raw 0x9b is
// 8-bit CSI to a terminal not in UTF-8 mode).
//
// An SGR sequence that came from content rather than orgtd's own styling
// still passes; it can only change how text looks, not what the
// terminal does.
func sanitizeTerminal(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == 0x1b:
			i = copyOrSkipEscape(&b, s, i)
		case c == '\n' || c == '\t':
			b.WriteByte(c)
			i++
		case c < 0x20 || c == 0x7f:
			i++
		case c < utf8.RuneSelf:
			b.WriteByte(c)
			i++
		default:
			r, size := utf8.DecodeRuneInString(s[i:])
			if !(r == utf8.RuneError && size == 1) && !(r >= 0x80 && r <= 0x9f) {
				b.WriteString(s[i : i+size])
			}
			i += size
		}
	}
	return b.String()
}

// copyOrSkipEscape handles the escape sequence starting at s[i] (an
// ESC): writes it to b if it's a plain SGR sequence, drops it otherwise,
// and returns the index just past it.
func copyOrSkipEscape(b *strings.Builder, s string, i int) int {
	if i+1 >= len(s) {
		return len(s)
	}
	switch s[i+1] {
	case '[':
		// CSI: parameter/intermediate bytes 0x20-0x3f, final 0x40-0x7e.
		j := i + 2
		sgrParams := true
		for j < len(s) && s[j] >= 0x20 && s[j] <= 0x3f {
			if !(s[j] >= '0' && s[j] <= '9') && s[j] != ';' && s[j] != ':' {
				sgrParams = false
			}
			j++
		}
		if j >= len(s) {
			return len(s)
		}
		if s[j] < 0x40 || s[j] > 0x7e {
			// Malformed: drop just ESC [ and carry on from here, so
			// whatever follows is still filtered byte by byte.
			return i + 2
		}
		if s[j] == 'm' && sgrParams {
			b.WriteString(s[i : j+1])
		}
		return j + 1
	case ']', 'P', '_', '^', 'X':
		// String sequences (OSC, DCS, APC, PM, SOS): skip to BEL (OSC's
		// common terminator) or ST (ESC \), or the end of s if
		// unterminated.
		for j := i + 2; j < len(s); j++ {
			if s[j] == 0x07 {
				return j + 1
			}
			if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
				return j + 2
			}
		}
		return len(s)
	default:
		// Any other ESC sequence: drop ESC and the byte after it (if
		// that's printable ASCII; otherwise just the ESC).
		if s[i+1] >= 0x20 && s[i+1] < 0x7f {
			return i + 2
		}
		return i + 1
	}
}
