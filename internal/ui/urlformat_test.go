package ui

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFakeFormatter writes a shell script to serve as urlFormatterCmd,
// with body as its script body (given "$1" as the URL argument).
func writeFakeFormatter(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-formatter.sh")
	script := "#!/bin/sh\n" + body + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

// captureLog redirects the standard log package's output to a buffer
// for the duration of the test (restored on cleanup), so tests can
// assert on what runURLFormatter logs — the whole point of that
// logging is to be inspectable outside the running TUI, so this is the
// only way to verify it actually happened.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prevOut := log.Writer()
	prevFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0) // no timestamp prefix, so assertions can match exact text
	t.Cleanup(func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	})
	return &buf
}

func TestFormatURLsNoopWhenDisabled(t *testing.T) {
	m := Model{}
	text := "See https://example.com/page for details."
	if got := m.formatURLs(text); got != text {
		t.Errorf("formatURLs with no formatter configured changed text: %q", got)
	}
}

func TestFormatURLsReplacesBareURL(t *testing.T) {
	m := Model{urlFormatterCmd: writeFakeFormatter(t, `echo "[[$1][Formatted]]"`)}
	text := "See https://example.com/page for details."
	want := "See [[https://example.com/page][Formatted]] for details."
	if got := m.formatURLs(text); got != want {
		t.Errorf("formatURLs = %q, want %q", got, want)
	}
}

func TestRunURLFormatterLogsSuccessfulAttempt(t *testing.T) {
	logBuf := captureLog(t)
	script := writeFakeFormatter(t, `echo "[[$1][Formatted]]"`)
	m := Model{urlFormatterCmd: script}

	m.runURLFormatter("https://example.com")

	logged := logBuf.String()
	if !strings.Contains(logged, "running") || !strings.Contains(logged, script) {
		t.Errorf("log = %q, want it to record the command actually run", logged)
	}
	if !strings.Contains(logged, "[[https://example.com][Formatted]]") {
		t.Errorf("log = %q, want it to record the successful result", logged)
	}
}

func TestRunURLFormatterLogsFailureWithStderr(t *testing.T) {
	logBuf := captureLog(t)
	script := writeFakeFormatter(t, `echo "boom: no such template" >&2; exit 1`)
	m := Model{urlFormatterCmd: script, debug: true}

	got := m.runURLFormatter("https://example.com")

	if got != "https://example.com" {
		t.Errorf("runURLFormatter = %q, want the url unchanged on failure", got)
	}
	logged := logBuf.String()
	if !strings.Contains(logged, "failed") {
		t.Errorf("log = %q, want it to record the failure", logged)
	}
	if !strings.Contains(logged, "boom: no such template") {
		t.Errorf("log = %q, want it to include the subprocess's stderr", logged)
	}
	if m.message == "" || !strings.Contains(m.message, "debug.log") {
		t.Errorf("m.message = %q, want a visible failure notice pointing at the log", m.message)
	}
}

func TestRunURLFormatterFailureHintsAtEnablingDebugWhenOff(t *testing.T) {
	captureLog(t)
	script := writeFakeFormatter(t, `exit 1`)
	m := Model{urlFormatterCmd: script} // debug left false (the default)

	m.runURLFormatter("https://example.com")

	if m.message == "" || strings.Contains(m.message, "debug.log") {
		t.Errorf("m.message = %q, want a hint to enable --debug, not a pointer at a file nothing was written to", m.message)
	}
}

func TestRunURLFormatterLogsEmptyOutput(t *testing.T) {
	logBuf := captureLog(t)
	script := writeFakeFormatter(t, `true`) // exits 0, prints nothing
	m := Model{urlFormatterCmd: script}

	got := m.runURLFormatter("https://example.com")

	if got != "https://example.com" {
		t.Errorf("runURLFormatter = %q, want the url unchanged on empty output", got)
	}
	if logged := logBuf.String(); !strings.Contains(logged, "no output") {
		t.Errorf("log = %q, want it to record the empty-output case", logged)
	}
	if m.message == "" {
		t.Error("expected a visible message for the empty-output case")
	}
}

func TestRunURLFormatterLogsCommandNotFound(t *testing.T) {
	logBuf := captureLog(t)
	m := Model{urlFormatterCmd: "/no/such/program/anywhere"}

	got := m.runURLFormatter("https://example.com")

	if got != "https://example.com" {
		t.Errorf("runURLFormatter = %q, want the url unchanged", got)
	}
	if logged := logBuf.String(); !strings.Contains(logged, "failed") {
		t.Errorf("log = %q, want it to record the failure even when the program doesn't exist", logged)
	}
}

// TestRunURLFormatterSplitsCommandAndArguments guards against a real
// bug: urlFormatterCmd wasn't split on whitespace before being passed to
// exec.Command, so a configured command with extra arguments (e.g.
// "myformatter an-argument", exactly as one would write it in the
// config file or on -url-formatter) would look for a single executable
// literally named "myformatter an-argument" — which never exists — and
// silently fail every time, leaving the URL unformatted with no visible
// error.
func TestRunURLFormatterSplitsCommandAndArguments(t *testing.T) {
	script := writeFakeFormatter(t, `echo "$1-$2"`)
	m := Model{urlFormatterCmd: script + " an-argument"}

	got := m.runURLFormatter("https://example.com")
	want := "an-argument-https://example.com"
	if got != want {
		t.Errorf("runURLFormatter = %q, want %q (extra argument then url)", got, want)
	}
}

func TestRunURLFormatterHandlesMultipleExtraArguments(t *testing.T) {
	script := writeFakeFormatter(t, `echo "$1|$2|$3"`)
	m := Model{urlFormatterCmd: script + " first second"}

	got := m.runURLFormatter("https://example.com")
	want := "first|second|https://example.com"
	if got != want {
		t.Errorf("runURLFormatter = %q, want %q", got, want)
	}
}

// TestRunURLFormatterExpandsHomeInConfiguredPath is the exact bug
// reported: url_formatter = "~/scripts/url_formatter.py" ran fine when
// passed on the command line (the shell expands "~" before orgtd ever
// sees argv) but silently did nothing when set to the identical value
// in the config file (no shell is involved there, so exec.Command
// received a literal "~/..." path, found no such program, and the
// error was swallowed — leaving the URL unformatted with no visible
// failure). Simulates the config-file path by setting $HOME to a temp
// directory and placing the fake formatter under it.
func TestRunURLFormatterExpandsHomeInConfiguredPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	scriptDir := filepath.Join(home, "scripts")
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	scriptPath := filepath.Join(scriptDir, "url_formatter.py")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\necho \"[[$1][Formatted]]\"\n"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	m := Model{urlFormatterCmd: "~/scripts/url_formatter.py"}
	got := m.runURLFormatter("https://example.com")
	want := "[[https://example.com][Formatted]]"
	if got != want {
		t.Errorf("runURLFormatter = %q, want %q (the tilde-prefixed path should have run)", got, want)
	}
}

// TestRunURLFormatterHandlesQuotedArgumentWithSpaces guards against a
// second, distinct bug in the same area: even after urlFormatterCmd
// started being split at all, the split itself (strings.Fields) had no
// concept of quoting, so a quoted multi-word argument — e.g.
// `myformatter "an argument"` — was torn into two mangled fields
// (`"an`, `argument"`, quote characters and all) instead of becoming one
// argument ("an argument").
func TestRunURLFormatterHandlesQuotedArgumentWithSpaces(t *testing.T) {
	script := writeFakeFormatter(t, `echo "1=[$1] 2=[$2]"`)
	m := Model{urlFormatterCmd: script + ` "an argument"`}

	got := m.runURLFormatter("https://example.com")
	want := "1=[an argument] 2=[https://example.com]"
	if got != want {
		t.Errorf("runURLFormatter = %q, want %q", got, want)
	}
}

func TestFormatURLsCommandWithExtraArguments(t *testing.T) {
	script := writeFakeFormatter(t, `echo "[[$2][arg=$1]]"`)
	m := Model{urlFormatterCmd: script + " an-argument"}
	text := "See https://example.com/page for details."

	want := "See [[https://example.com/page][arg=an-argument]] for details."
	if got := m.formatURLs(text); got != want {
		t.Errorf("formatURLs = %q, want %q", got, want)
	}
}

func TestRunURLFormatterEmptyCommandReturnsURLUnchanged(t *testing.T) {
	m := Model{urlFormatterCmd: "   "}
	url := "https://example.com"
	if got := m.runURLFormatter(url); got != url {
		t.Errorf("runURLFormatter with a blank command = %q, want the url unchanged", got)
	}
}

func TestFormatURLsLeavesExistingLinkWithDescriptionAlone(t *testing.T) {
	calls := filepath.Join(t.TempDir(), "calls.log")
	m := Model{urlFormatterCmd: writeFakeFormatter(t, `echo called >> `+calls+"\n"+`echo "[[$1][Formatted]]"`)}
	text := "Already a link: [[https://example.com/page][Existing Title]]"

	got := m.formatURLs(text)

	if got != text {
		t.Errorf("formatURLs modified an already-formatted link:\n%s", got)
	}
	if data, err := os.ReadFile(calls); err == nil && len(data) > 0 {
		t.Errorf("formatter was invoked on an already-formatted link: %s", data)
	}
}

func TestFormatURLsLeavesBareLinkWithoutDescriptionAlone(t *testing.T) {
	m := Model{urlFormatterCmd: writeFakeFormatter(t, `echo "[[$1][Formatted]]"`)}
	text := "See [[https://example.com/page]] for details."

	if got := m.formatURLs(text); got != text {
		t.Errorf("formatURLs modified a bracketed URL with no description:\n%s", got)
	}
}

func TestFormatURLsCachesRepeatedURLs(t *testing.T) {
	calls := filepath.Join(t.TempDir(), "calls.log")
	m := Model{urlFormatterCmd: writeFakeFormatter(t, `echo x >> `+calls+"\n"+`echo "[[$1][Formatted]]"`)}
	text := "https://example.com/page and again https://example.com/page"

	got := m.formatURLs(text)

	want := "[[https://example.com/page][Formatted]] and again [[https://example.com/page][Formatted]]"
	if got != want {
		t.Errorf("formatURLs = %q, want %q", got, want)
	}
	data, _ := os.ReadFile(calls)
	if n := strings.Count(string(data), "x"); n != 1 {
		t.Errorf("formatter invoked %d times, want 1 (repeated URLs should be cached)", n)
	}
}

func TestFormatURLsLeavesURLUnchangedOnFormatterFailure(t *testing.T) {
	m := Model{urlFormatterCmd: writeFakeFormatter(t, `exit 1`)}
	text := "See https://example.com/page for details."

	if got := m.formatURLs(text); got != text {
		t.Errorf("formatURLs on formatter failure = %q, want unchanged %q", got, text)
	}
}

func TestFormatURLsHandlesMultipleDistinctURLs(t *testing.T) {
	m := Model{urlFormatterCmd: writeFakeFormatter(t, `echo "[[$1][Formatted]]"`)}
	text := "https://a.example.com then https://b.example.com"

	want := "[[https://a.example.com][Formatted]] then [[https://b.example.com][Formatted]]"
	if got := m.formatURLs(text); got != want {
		t.Errorf("formatURLs = %q, want %q", got, want)
	}
}

func TestBuildBareURLRegexpMatchesConfiguredPrefixes(t *testing.T) {
	re := buildBareURLRegexp([]string{"bit.ly/", "go/"})
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
			t.Errorf("buildBareURLRegexp match in %q = %q, want %q", c.text, got, c.want)
		}
	}
}

func TestBuildBareURLRegexpIgnoresEmptyPrefix(t *testing.T) {
	re := buildBareURLRegexp([]string{"", "go/"})
	if got := re.FindString("go/x"); got != "go/x" {
		t.Errorf("match = %q, want %q", got, "go/x")
	}
}

func TestFormatURLsFormatsConfiguredPrefix(t *testing.T) {
	m := Model{
		urlFormatterCmd:      writeFakeFormatter(t, `echo "[[$1][Formatted]]"`),
		urlFormatterPrefixes: []string{"bit.ly/", "go/"},
	}
	text := "See bit.ly/xyz and go/my-shortlink for details."
	want := "See [[bit.ly/xyz][Formatted]] and [[go/my-shortlink][Formatted]] for details."
	if got := m.formatURLs(text); got != want {
		t.Errorf("formatURLs = %q, want %q", got, want)
	}
}

func TestFormatURLsWithConfiguredPrefixesStillRequiresWordBoundary(t *testing.T) {
	m := Model{
		urlFormatterCmd:      writeFakeFormatter(t, `echo "[[$1][Formatted]]"`),
		urlFormatterPrefixes: []string{"go/"},
	}
	text := "embargo/foo should not become a link"
	if got := m.formatURLs(text); got != text {
		t.Errorf("formatURLs = %q, want unchanged (mid-word match)", got)
	}
}

func TestFormatURLsWithoutConfiguredPrefixesDoesNotMatchThem(t *testing.T) {
	m := Model{urlFormatterCmd: writeFakeFormatter(t, `echo "[[$1][Formatted]]"`)}
	text := "See go/my-shortlink for details."
	if got := m.formatURLs(text); got != text {
		t.Errorf("formatURLs = %q, want unchanged (no prefixes configured)", got)
	}
}

func TestWithURLFormatterPrefixesAppliedThroughNew(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws,
		WithURLFormatter(writeFakeFormatter(t, `echo "[[$1][Formatted Title]]"`)),
		WithURLFormatterPrefixes([]string{"go/"}),
	)
	idx := findRow(t, m, "Call the vet about Fido's checkup")
	m.cursor = idx
	old := m.currentHeadline()

	path := writeTempOrgFile(t, "* TODO Call the vet about Fido's checkup\n  See go/my-shortlink for details.\n")

	updated, _ := m.Update(editFinishedMsg{path: path, target: old})
	m = updated.(Model)

	body := strings.Join(m.rows[idx].headline.Body, "\n")
	if !strings.Contains(body, "[[go/my-shortlink][Formatted Title]]") {
		t.Errorf("body after edit = %q, want the formatted go/ link", body)
	}
}

func TestFormatURLsAppliedDuringFinishEdit(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws, WithURLFormatter(writeFakeFormatter(t, `echo "[[$1][Formatted Title]]"`)))
	idx := findRow(t, m, "Call the vet about Fido's checkup")
	m.cursor = idx
	old := m.currentHeadline()

	path := writeTempOrgFile(t, "* TODO Call the vet about Fido's checkup\n  See https://example.com/vet for details.\n")

	updated, _ := m.Update(editFinishedMsg{path: path, target: old})
	m = updated.(Model)

	got := m.rows[idx].headline
	body := strings.Join(got.Body, "\n")
	if !strings.Contains(body, "[[https://example.com/vet][Formatted Title]]") {
		t.Errorf("body after edit = %q, want the formatted link", body)
	}
	if strings.Contains(body, "See https://example.com/vet for details.") {
		t.Errorf("body still contains the raw bare URL: %q", body)
	}
}

func TestFormatURLsNotAppliedWhenNoFormatterConfigured(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws) // no WithURLFormatter option
	idx := findRow(t, m, "Call the vet about Fido's checkup")
	m.cursor = idx
	old := m.currentHeadline()

	path := writeTempOrgFile(t, "* TODO Call the vet about Fido's checkup\n  See https://example.com/vet for details.\n")

	updated, _ := m.Update(editFinishedMsg{path: path, target: old})
	m = updated.(Model)

	got := m.rows[idx].headline
	body := strings.Join(got.Body, "\n")
	if !strings.Contains(body, "https://example.com/vet") {
		t.Errorf("body after edit = %q, want the raw URL left untouched", body)
	}
	if strings.Contains(body, "[[") {
		t.Errorf("body after edit = %q, want no link syntax introduced", body)
	}
}
