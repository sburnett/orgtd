package ui

import (
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestSanitizeTerminal(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"plain", "hello\tworld\n", "hello\tworld\n"},
		{"sgr kept", "\x1b[1;38;2;255;0;0mred\x1b[0m", "\x1b[1;38;2;255;0;0mred\x1b[0m"},
		{"sgr colon form kept", "\x1b[4:3mx\x1b[m", "\x1b[4:3mx\x1b[m"},
		{"osc52 bel", "a\x1b]52;c;ZXZpbA==\x07b", "ab"},
		{"osc title st", "a\x1b]0;pwned\x1b\\b", "ab"},
		{"osc8 hyperlink", "\x1b]8;;https://evil.example\x07x\x1b]8;;\x07", "x\x1b]8;;\x07"},
		{"osc8 with a guessed id", "\x1b]8;id=orgtd00-1;https://evil.example\x1b\\x", "x"},
		{"osc8 with orgtd's id but a control in the url", "\x1b]8;id=" + hyperlinkNonce + "-1;https://e.example/\x7f\x1b\\x", "x"},
		{"orgtd's own osc8 kept", hyperlink("https://e.example/a b", "x"), "\x1b]8;id=" + hyperlinkNonce + "-" + hyperlinkID("https://e.example/a b") + ";https://e.example/a%20b\x1b\\x\x1b]8;;\x1b\\"},
		{"unterminated osc", "a\x1b]0;rest", "a"},
		{"dcs", "a\x1bP$q m\x1b\\b", "ab"},
		{"device status query", "a\x1b[6nb", "ab"},
		{"private csi", "a\x1b[?1049lb", "ab"},
		{"cursor move", "a\x1b[2Jb\x1b[Hc", "abc"},
		{"two-byte esc", "a\x1bcb", "ab"},
		{"trailing esc", "a\x1b", "a"},
		{"c0 controls", "a\rb\x08c\x07d\x00e\x7f", "abcde"},
		{"c1 csi", "a\u009b6nb", "a6nb"},
		{"c1 osc", "a\u009d0;x\u009cb", "a0;xb"},
		{"invalid utf8", "a\x9b6nb", "a6nb"},
		{"unicode kept", "café ● ▣ 日本", "café ● ▣ 日本"},
	} {
		if got := sanitizeTerminal(tc.in); got != tc.want {
			t.Errorf("%s: sanitizeTerminal(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}

// ownHyperlinks matches the OSC 8 sequences orgtd emits itself (see
// hyperlink): a link opened with hyperlinkNonce, or a bare close.
var ownHyperlinks = regexp.MustCompile("\x1b\\]8;(?:id=" + hyperlinkNonce + "-[0-9a-f]+;[\x21-\x7e]+|;)\x1b\\\\")

// Org text isn't trusted (a calendar invite, a git pull), so an escape
// sequence in a title must never reach the terminal from View.
func TestViewStripsEscapeSequencesFromContent(t *testing.T) {
	ws := loadFixture(t)
	ws.Files[0].Headlines[0].Title = "EVIL\x1b]52;c;ZXZpbA==\x07\x1b]0;pwned\x07\x1b]8;;https://evil.example\x07\x1b[6nEND"
	m := New(ws)
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	v := mm.(Model).View()
	for _, bad := range []string{"\x1b]", "\x1b[6n", "\x07"} {
		if strings.Contains(ownHyperlinks.ReplaceAllString(v, ""), bad) {
			t.Errorf("View() contains %q", bad)
		}
	}
	if !strings.Contains(stripANSI(v), "EVILEND") {
		t.Errorf("View() lost the title's visible text:\n%s", stripANSI(v))
	}
}
