package extprog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sburnett/orgtd/internal/execlog"
)

func TestEditorCommandAddsLineArgForKnownEditors(t *testing.T) {
	before := "# inbox.org\n#\n# * some parent\n" // 3 lines of context

	for _, editorEnv := range []string{"vim", "nvim", "vi", "gvim", "mvim", "emacs", "emacsclient", "nano"} {
		t.Run(editorEnv, func(t *testing.T) {
			cmd := EditorCommand(editorEnv, "/tmp/x.org", before, NoPlacement, 0)
			if len(cmd.Args) < 3 {
				t.Fatalf("Args = %v, want at least [name, +N, path]", cmd.Args)
			}
			last := cmd.Args[len(cmd.Args)-1]
			lineArg := cmd.Args[len(cmd.Args)-2]
			if last != "/tmp/x.org" {
				t.Errorf("last arg = %q, want the file path", last)
			}
			if lineArg != "+4" {
				t.Errorf("line arg = %q, want %q (3 context lines + 1)", lineArg, "+4")
			}
		})
	}
}

func TestEditorCommandSkipsLineArgForUnknownEditors(t *testing.T) {
	for _, editorEnv := range []string{"code --wait", "subl", "nvim-but-not-really", "cat"} {
		t.Run(editorEnv, func(t *testing.T) {
			cmd := EditorCommand(editorEnv, "/tmp/x.org", "# a\n# b\n", NoPlacement, 0)
			for _, a := range cmd.Args {
				if strings.HasPrefix(a, "+") {
					t.Errorf("Args = %v, unexpectedly contains a +N line argument", cmd.Args)
				}
			}
			if got := cmd.Args[len(cmd.Args)-1]; got != "/tmp/x.org" {
				t.Errorf("last arg = %q, want the file path", got)
			}
		})
	}
}

// TestSplitFields guards against a real bug: the naive
// strings.Fields split (used by both EditorCommand and
// runURLFormatter before this) has no concept of quoting, so a
// configured command with a quoted multi-word argument — e.g.
// `myformatter --template "a template" x` — got torn apart into
// separate fields with the literal quote characters still attached
// ("\"a", "template\"") instead of becoming one argument ("a template").
func TestSplitFields(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"myformatter", []string{"myformatter"}},
		{"myformatter an-argument", []string{"myformatter", "an-argument"}},
		{"myformatter first second", []string{"myformatter", "first", "second"}},
		{`myformatter "an argument"`, []string{"myformatter", "an argument"}},
		{`myformatter --template "a template" x`, []string{"myformatter", "--template", "a template", "x"}},
		{`myformatter 'single quoted'`, []string{"myformatter", "single quoted"}},
		{`myformatter foo"bar baz"qux`, []string{"myformatter", "foobar bazqux"}}, // quote embedded mid-word
		{`  myformatter   spaced  `, []string{"myformatter", "spaced"}},           // extra whitespace collapses
		{`myformatter "unterminated`, []string{"myformatter", "unterminated"}},    // unterminated quote: don't drop it
	}
	for _, c := range cases {
		got := SplitFields(c.in)
		if len(got) != len(c.want) {
			t.Errorf("SplitFields(%q) = %#v, want %#v", c.in, got, c.want)
			continue
		}
		for i := range c.want {
			if got[i] != c.want[i] {
				t.Errorf("SplitFields(%q)[%d] = %q, want %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}

// TestExpandHome guards against a real bug: a command typed on the
// command line (--editor, --url-formatter) gets "~" expanded for free by
// the invoking shell before orgtd ever sees it, but the identical value
// read from the config file reaches us raw and unexpanded (no shell is
// involved there), so "~/bin/myformatter" was handed to exec.Command
// completely literally and failed to launch — silently, since a failed
// exec just leaves input unchanged.
func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory available")
	}
	cases := []struct{ in, want string }{
		{"~", home},
		{"~/bin/myformatter", filepath.Join(home, "bin/myformatter")},
		{"~/", filepath.Join(home, "")},
		{"/absolute/path", "/absolute/path"}, // unaffected
		{"relative/path", "relative/path"},   // unaffected
		{"~user/path", "~user/path"},         // unaffected: not "~" or "~/..."
		{"", ""},                             // unaffected
	}
	for _, c := range cases {
		if got := ExpandHome(c.in); got != c.want {
			t.Errorf("ExpandHome(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestEditorCommandExpandsHomeInEditorPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory available")
	}
	cmd := EditorCommand("~/bin/myeditor --wait", "/tmp/x.org", "", NoPlacement, 0)
	want := filepath.Join(home, "bin/myeditor")
	if cmd.Args[0] != want {
		t.Errorf("Args[0] = %q, want %q (expanded)", cmd.Args[0], want)
	}
	if cmd.Args[1] != "--wait" {
		t.Errorf("Args[1] = %q, want %q (unaffected extra arg)", cmd.Args[1], "--wait")
	}
}

func TestEditorCommandHandlesQuotedArgument(t *testing.T) {
	cmd := EditorCommand(`myeditor --template "a template" -x`, "/tmp/x.org", "", NoPlacement, 0)
	want := []string{"myeditor", "--template", "a template", "-x", "/tmp/x.org"}
	if len(cmd.Args) != len(want) {
		t.Fatalf("Args = %#v, want %#v", cmd.Args, want)
	}
	for i := range want {
		if cmd.Args[i] != want[i] {
			t.Errorf("Args[%d] = %q, want %q", i, cmd.Args[i], want[i])
		}
	}
}

func TestEditorCommandPreservesExtraArgsAndOrder(t *testing.T) {
	cmd := EditorCommand("emacs -nw --debug-init", "/tmp/x.org", "# one line\n", NoPlacement, 0)
	want := []string{"emacs", "-nw", "--debug-init", "+2", "/tmp/x.org"}
	if len(cmd.Args) != len(want) {
		t.Fatalf("Args = %v, want %v", cmd.Args, want)
	}
	for i := range want {
		if cmd.Args[i] != want[i] {
			t.Errorf("Args[%d] = %q, want %q", i, cmd.Args[i], want[i])
		}
	}
}

func TestEditorCommandDefaultsToVimWhenEditorUnset(t *testing.T) {
	cmd := EditorCommand("", "/tmp/x.org", "# a\n", NoPlacement, 0)
	if cmd.Args[0] != "vim" {
		t.Errorf("Args[0] = %q, want vim (the default)", cmd.Args[0])
	}
	if got := cmd.Args[len(cmd.Args)-2]; got != "+2" {
		t.Errorf("line arg = %q, want +2", got)
	}
}

func TestCursorArgEntryStartForVimFamily(t *testing.T) {
	for _, base := range []string{"vi", "vim", "nvim", "gvim", "mvim"} {
		t.Run(base, func(t *testing.T) {
			got := CursorArg(base, 4, 5, AtEntryStart)
			want := "+call cursor(4,5)|startinsert"
			if got != want {
				t.Errorf("CursorArg(%q, ...) = %q, want %q", base, got, want)
			}
		})
	}
}

func TestCursorArgLineEndForVimFamily(t *testing.T) {
	for _, base := range []string{"vi", "vim", "nvim", "gvim", "mvim"} {
		t.Run(base, func(t *testing.T) {
			got := CursorArg(base, 4, 5, AtLineEnd)
			want := "+4|startinsert!"
			if got != want {
				t.Errorf("CursorArg(%q, ...) = %q, want %q", base, got, want)
			}
		})
	}
}

func TestCursorArgFallsBackToBareLineForNonVimEditors(t *testing.T) {
	for _, base := range []string{"emacs", "emacsclient", "nano"} {
		for _, placement := range []Placement{AtEntryStart, AtLineEnd} {
			got := CursorArg(base, 4, 5, placement)
			if got != "+4" {
				t.Errorf("CursorArg(%q, placement=%v) = %q, want the bare \"+4\" — %s has no insert-mode concept to start", base, placement, got, base)
			}
		}
	}
}

func TestCursorArgNoPlacementIsAlwaysBareEvenForVim(t *testing.T) {
	if got := CursorArg("vim", 4, 5, NoPlacement); got != "+4" {
		t.Errorf("CursorArg(vim, NoPlacement) = %q, want \"+4\"", got)
	}
}

func TestEditorCommandEntryStartPlacementForVim(t *testing.T) {
	// "before" is 3 lines of context, so the real content starts on line 4.
	before := "# inbox.org\n#\n# * some parent\n"
	cmd := EditorCommand("vim", "/tmp/x.org", before, AtEntryStart, 5)
	lineArg := cmd.Args[len(cmd.Args)-2]
	if want := "+call cursor(4,5)|startinsert"; lineArg != want {
		t.Errorf("line arg = %q, want %q", lineArg, want)
	}
}

func TestEditorCommandLineEndPlacementForVim(t *testing.T) {
	cmd := EditorCommand("nvim", "/tmp/x.org", "# one\n", AtLineEnd, 99)
	lineArg := cmd.Args[len(cmd.Args)-2]
	if want := "+2|startinsert!"; lineArg != want {
		t.Errorf("line arg = %q, want %q (col is irrelevant to AtLineEnd)", lineArg, want)
	}
}

func TestEditorCommandEntryStartPlacementFallsBackForEmacsAndNano(t *testing.T) {
	for _, editorEnv := range []string{"emacs", "nano"} {
		t.Run(editorEnv, func(t *testing.T) {
			cmd := EditorCommand(editorEnv, "/tmp/x.org", "# one\n", AtEntryStart, 5)
			lineArg := cmd.Args[len(cmd.Args)-2]
			if lineArg != "+2" {
				t.Errorf("line arg = %q, want the bare \"+2\"", lineArg)
			}
		})
	}
}

func TestResolveEntryPlacementDowngradesToLineEndForBlankEntry(t *testing.T) {
	// A fresh o/O template: no keyword, no priority, no title — the
	// stripped-bullet first line is completely empty.
	placement, col := ResolveEntryPlacement("", AtEntryStart)
	if placement != AtLineEnd {
		t.Errorf("placement = %v, want AtLineEnd (nothing on the first line)", placement)
	}
	if col != 1 {
		t.Errorf("col = %d, want 1 (unused by AtLineEnd, but still column 1)", col)
	}
}

func TestResolveEntryPlacementKeepsEntryStartWhenTitleExists(t *testing.T) {
	placement, col := ResolveEntryPlacement("TODO Buy milk\n", AtEntryStart)
	if placement != AtEntryStart {
		t.Errorf("placement = %v, want AtEntryStart (there's real text on the line)", placement)
	}
	if col != 1 {
		t.Errorf("col = %d, want 1 (there's no bullet to land after)", col)
	}
}

func TestResolveEntryPlacementKeepsEntryStartWithKeywordButNoTitle(t *testing.T) {
	// writeHeadlineFields always writes a trailing space after the
	// keyword, so "TODO " (note the trailing space) is a non-empty first
	// line even with a blank title — this should stay AtEntryStart.
	placement, col := ResolveEntryPlacement("TODO \n", AtEntryStart)
	if placement != AtEntryStart {
		t.Errorf("placement = %v, want AtEntryStart: \"TODO \" is a non-empty first line", placement)
	}
	if col != 1 {
		t.Errorf("col = %d, want 1", col)
	}
}

func TestResolveEntryPlacementNeverAppliesToOtherPlacements(t *testing.T) {
	if placement, _ := ResolveEntryPlacement("", AtLineEnd); placement != AtLineEnd {
		t.Errorf("AtLineEnd should pass through unchanged, got %v", placement)
	}
	if placement, _ := ResolveEntryPlacement("", NoPlacement); placement != NoPlacement {
		t.Errorf("NoPlacement should pass through unchanged, got %v", placement)
	}
}

func TestEditorCommandLineNumberMatchesContextLength(t *testing.T) {
	cases := []struct {
		before string
		want   string
	}{
		{"", "+1"},
		{"# one\n", "+2"},
		{"# one\n# two\n# three\n# four\n# five\n", "+6"},
	}
	for _, c := range cases {
		cmd := EditorCommand("vim", "/tmp/x.org", c.before, NoPlacement, 0)
		got := cmd.Args[len(cmd.Args)-2]
		if got != c.want {
			t.Errorf("EditorCommand with %d context lines: line arg = %q, want %q",
				strings.Count(c.before, "\n"), got, c.want)
		}
	}
}

func TestRunBatchFormatterMatchesLinesByIndex(t *testing.T) {
	script := writeBatchScript(t, `echo "[[$line][Formatted]]"`)
	got, err := RunBatchFormatter(&execlog.Log{}, script, []string{"https://a.example.com", "https://b.example.com"})
	if err != nil {
		t.Fatalf("RunBatchFormatter: %v", err)
	}
	want := []string{"[[https://a.example.com][Formatted]]", "[[https://b.example.com][Formatted]]"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestRunBatchFormatterErrorsOnLineCountMismatch(t *testing.T) {
	script := writeScript(t, `echo "only one line"`)
	_, err := RunBatchFormatter(&execlog.Log{}, script, []string{"https://a.example.com", "https://b.example.com"})
	if err == nil {
		t.Fatal("expected an error when the formatter's output line count doesn't match the input")
	}
}

func TestRunBatchFormatterErrorsOnFailure(t *testing.T) {
	script := writeScript(t, `echo "boom" >&2; exit 1`)
	_, err := RunBatchFormatter(&execlog.Log{}, script, []string{"https://a.example.com"})
	if err == nil {
		t.Fatal("expected an error when the formatter process fails")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error = %v, want it to include the subprocess's stderr", err)
	}
}

func TestBareURLRegexpMatchesConfiguredPrefixes(t *testing.T) {
	re := BareURLRegexp([]string{"bit.ly/", "go/"})
	cases := []struct {
		text string
		want string // "" means no match expected
	}{
		{"See bit.ly/xyz for details.", "bit.ly/xyz"},
		{"Check go/my-shortlink please", "go/my-shortlink"},
		{"https://example.com/page still works", "https://example.com/page"},
		{"http://example.com/page still works", "http://example.com/page"},
		{"embargo/foo should not match", ""},  // "go/" mid-word
		{"orbit.ly/foo should not match", ""}, // "bit.ly/" mid-word
		{"a bit.ly/foo at word start matches", "bit.ly/foo"},
	}
	for _, c := range cases {
		got := re.FindString(c.text)
		if got != c.want {
			t.Errorf("BareURLRegexp match in %q = %q, want %q", c.text, got, c.want)
		}
	}
}

func TestBareURLRegexpIgnoresEmptyPrefix(t *testing.T) {
	re := BareURLRegexp([]string{"", "go/"})
	if got := re.FindString("go/x"); got != "go/x" {
		t.Errorf("match = %q, want %q", got, "go/x")
	}
}

// writeScript writes an executable shell script with body as its body
// and returns its path, standing in for a real formatter program.
func writeScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-formatter.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

// writeBatchScript writes a script suitable as a batch formatter command:
// it reads lines from stdin and, for each, runs body's shell fragment with
// $line bound to it.
func writeBatchScript(t *testing.T, body string) string {
	t.Helper()
	return writeScript(t, "while IFS= read -r line; do\n"+body+"\ndone")
}
