package ui

import (
	"crypto/rand"
	"encoding/hex"
	"hash/fnv"
	"strconv"
	"strings"
)

// hyperlinkNonce marks the OSC 8 hyperlinks orgtd itself emits (see
// hyperlink), so sanitizeTerminal can tell them apart from one an org
// file smuggled in: it keeps an OSC 8 only when its id parameter
// starts with this. It's random per process and never written anywhere
// content could come from, so org text can't forge it.
var hyperlinkNonce = func() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("orgtd: reading random bytes: " + err.Error())
	}
	return "orgtd" + hex.EncodeToString(b[:])
}()

// hyperlinkSchemes are the URL schemes hyperlink makes clickable: the
// ones a terminal can hand straight to the OS. Anything else an org link
// can point at (an id:, a file: path relative to the org directory, a
// "*Heading" search) means nothing outside org-mode, so it stays plain
// underlined text.
var hyperlinkSchemes = []string{"http://", "https://", "mailto:"}

// hyperlink wraps text, already rendered, in an OSC 8 hyperlink to url,
// which terminals that support it (iTerm2, kitty, WezTerm, Ghostty,
// recent VTE ones) open on a click (cmd-click, in iTerm2), while others
// ignore it and show text as is. text is returned unchanged if url isn't
// one of hyperlinkSchemes.
//
// The id parameter is hyperlinkNonce plus a hash of url, so every piece
// of one link — a title that wraps onto two rows, say — highlights
// together on hover, and sanitizeTerminal recognizes it as orgtd's.
// Bytes the escape sequence can't carry (spaces, controls, non-ASCII)
// are percent-encoded.
func hyperlink(url, text string) string {
	if !hyperlinkable(url) {
		return text
	}
	return "\x1b]8;id=" + hyperlinkNonce + "-" + hyperlinkID(url) + ";" +
		encodeHyperlinkURL(url) + "\x1b\\" + text + "\x1b]8;;\x1b\\"
}

// hyperlinkID is the per-URL part of a hyperlink's id parameter.
func hyperlinkID(url string) string {
	h := fnv.New32a()
	h.Write([]byte(url))
	return strconv.FormatUint(uint64(h.Sum32()), 16)
}

func hyperlinkable(url string) bool {
	lower := strings.ToLower(url)
	for _, scheme := range hyperlinkSchemes {
		if strings.HasPrefix(lower, scheme) && len(url) > len(scheme) {
			return true
		}
	}
	return false
}

// encodeHyperlinkURL percent-encodes every byte of url outside printable
// ASCII (0x21-0x7e), the only bytes an OSC 8 URI may contain.
func encodeHyperlinkURL(url string) string {
	const hexDigits = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(url); i++ {
		c := url[i]
		if c > 0x20 && c < 0x7f {
			b.WriteByte(c)
		} else {
			b.WriteByte('%')
			b.WriteByte(hexDigits[c>>4])
			b.WriteByte(hexDigits[c&0xf])
		}
	}
	return b.String()
}

// keepHyperlink reports whether seq, a complete OSC sequence (from ESC ]
// through its terminator), is one sanitizeTerminal should pass through:
// an OSC 8 that ends a hyperlink ("ESC ]8;;" with nothing after), which
// at worst cuts one of orgtd's links short, or one hyperlink opened,
// carrying hyperlinkNonce.
func keepHyperlink(seq string) bool {
	body, ok := strings.CutPrefix(seq, "\x1b]8;")
	if !ok {
		return false
	}
	if t, ok := strings.CutSuffix(body, "\x1b\\"); ok {
		body = t
	} else if t, ok := strings.CutSuffix(body, "\x07"); ok {
		body = t
	} else {
		return false
	}
	params, uri, ok := strings.Cut(body, ";")
	if !ok {
		return false
	}
	if params == "" && uri == "" {
		return true
	}
	if !strings.HasPrefix(params, "id="+hyperlinkNonce+"-") || uri == "" {
		return false
	}
	for i := 0; i < len(body); i++ {
		if body[i] <= 0x20 || body[i] >= 0x7f {
			return false
		}
	}
	return true
}
