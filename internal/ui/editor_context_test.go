package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sburnett/orgtd/internal/org"
)

// TestEditEntryContextOnDetachedHeadlineDoesNotPanic guards a real
// crash: insertPosition (undo.go) used to dereference f.Headlines
// unconditionally even when the caller was about to use parent.Children
// instead, so any headline whose file couldn't be found (fileForHeadline
// returns nil — e.g. a stale row left over from an external-edit
// reload, per internal/workspace's file watcher) crashed the whole
// program the moment editEntryContext tried to compute its siblings,
// which every "i"/"A"/o/O/capture session does. A top-level headline
// detached from every loaded file exercises the parent==nil branch;
// TestEditEntryContextOnDetachedNestedHeadlineDoesNotPanic below covers
// the parent!=nil branch, since insertPosition's old bug crashed
// regardless of which one applied.
func TestEditEntryContextOnDetachedHeadlineDoesNotPanic(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)

	orphan := &org.Headline{Level: 1, Title: "Not part of any loaded file"}

	f, parent, idx := m.insertPosition(orphan)
	if f != nil || parent != nil || idx != -1 {
		t.Errorf("insertPosition(orphan) = (%v, %v, %d), want (nil, nil, -1)", f, parent, idx)
	}

	prev, next, earlierCount := m.siblingHeadlines(orphan)
	if prev != nil || next != nil || earlierCount != 0 {
		t.Errorf("siblingHeadlines(orphan) = (%v, %v, %d), want (nil, nil, 0)", prev, next, earlierCount)
	}

	// The real crash: building the comment trailer for an entry whose
	// file can't be found.
	trailer := m.editEntryContext(orphan, false)
	if !strings.Contains(trailer, "[THIS ENTRY HERE]") {
		t.Errorf("trailer missing the marker even without file/sibling context:\n%s", trailer)
	}
}

// TestEditEntryContextOnDetachedNestedHeadlineDoesNotPanic is the
// parent!=nil counterpart of the test above: insertPosition's old bug
// dereferenced f.Headlines before ever checking parent, so a detached
// *nested* headline crashed identically even though the lookup would
// have used parent.Children, never f, once it got there.
func TestEditEntryContextOnDetachedNestedHeadlineDoesNotPanic(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)

	orphanParent := &org.Headline{Level: 1, Title: "Detached parent"}
	orphanChild := &org.Headline{Level: 2, Title: "Detached child", Parent: orphanParent}
	orphanParent.Children = []*org.Headline{orphanChild}

	f, parent, idx := m.insertPosition(orphanChild)
	if f != nil || parent != orphanParent || idx != 0 {
		t.Errorf("insertPosition(orphanChild) = (%v, %v, %d), want (nil, orphanParent, 0)", f, parent, idx)
	}

	trailer := m.editEntryContext(orphanChild, false)
	if !strings.Contains(trailer, "[THIS ENTRY HERE]") {
		t.Errorf("trailer missing the marker even without file context:\n%s", trailer)
	}
}

func TestDedentEntryRemovesBulletAndMatchingIndentFromEveryLine(t *testing.T) {
	h := &org.Headline{
		Level:         2,
		Keyword:       "NEXT",
		Title:         "Implement the org file parser",
		PropertyOrder: []string{"ID"},
		Properties:    map[string]string{"ID": "abc123"},
		Body:          []string{"   Headlines, planning lines, properties, body text."},
	}
	rendered := strings.TrimRight(org.RenderEntry(h), "\n")

	got := dedentEntry(rendered, h.Level)

	want := "NEXT Implement the org file parser\n" +
		":PROPERTIES:\n" +
		":ID: abc123\n" +
		":END:\n" +
		"Headlines, planning lines, properties, body text."
	if got != want {
		t.Errorf("dedentEntry() =\n%q\nwant\n%q", got, want)
	}
}

func TestIndentEntryReversesDedentEntry(t *testing.T) {
	h := &org.Headline{
		Level:         2,
		Keyword:       "NEXT",
		Title:         "Implement the org file parser",
		PropertyOrder: []string{"ID"},
		Properties:    map[string]string{"ID": "abc123"},
		Body:          []string{"   Headlines, planning lines, properties, body text."},
	}
	rendered := strings.TrimRight(org.RenderEntry(h), "\n")

	dedented := dedentEntry(rendered, h.Level)
	got := indentEntry(dedented, h.Level)

	if got != rendered {
		t.Errorf("indentEntry(dedentEntry(s)) =\n%q\nwant original\n%q", got, rendered)
	}
}

func TestIndentEntryLeavesBlankLinesAlone(t *testing.T) {
	got := indentEntry("TODO Buy milk\n\nSecond body line.", 1)
	want := "* TODO Buy milk\n\n  Second body line."
	if got != want {
		t.Errorf("indentEntry() = %q, want %q (blank line not indented)", got, want)
	}
}

func TestEditorCommandPrefersOverrideOverEditorEnv(t *testing.T) {
	t.Setenv("EDITOR", "nano")
	m := New(agendaFixture(t, "* TODO x\n"), WithEditor("emacsclient -t"))
	if got := m.editorCommand(); got != "emacsclient -t" {
		t.Errorf("editorCommand() = %q, want the WithEditor override", got)
	}
}

func TestEditorCommandFallsBackToEditorEnvWhenUnset(t *testing.T) {
	t.Setenv("EDITOR", "nano")
	m := New(agendaFixture(t, "* TODO x\n"))
	if got := m.editorCommand(); got != "nano" {
		t.Errorf("editorCommand() = %q, want $EDITOR (%q)", got, "nano")
	}
}

func TestStripCommentLinesPreservesDirectivesAndRealContent(t *testing.T) {
	input := strings.Join([]string{
		"* TODO Real headline",
		"#+TITLE: not a comment, a directive",
		"  body line, not a comment",
		"# a real comment",
		"#",
		"   # indented comment",
		"#no space, still a comment (end of line rule doesn't apply, but bare # does)",
	}, "\n")

	got := stripCommentLines(input)

	for _, want := range []string{"* TODO Real headline", "#+TITLE: not a comment, a directive", "  body line, not a comment"} {
		if !strings.Contains(got, want) {
			t.Errorf("stripped output missing %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"# a real comment", "# indented comment"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("stripped output still contains comment %q:\n%s", unwanted, got)
		}
	}
}

func TestEditEntryContextNotesURLFormattingWhenConfigured(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws, WithURLFormatter(writeFakeFormatter(t, `echo "[[$1]]"`)))
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	h := m.currentHeadline()

	trailer := m.editEntryContext(h, false)

	if !strings.Contains(trailer, "formatted into org-mode links automatically") {
		t.Errorf("trailer missing the URL-formatter note:\n%s", trailer)
	}
	if !strings.Contains(trailer, "[[http://...]]") {
		t.Errorf("trailer missing guidance on doing it yourself:\n%s", trailer)
	}
	for _, line := range strings.Split(strings.TrimRight(trailer, "\n"), "\n") {
		if line != "" && !strings.HasPrefix(line, "#") {
			t.Errorf("non-comment line in context block: %q", line)
		}
	}
}

func TestEditEntryContextOmitsURLFormattingNoteWhenNotConfigured(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws) // no WithURLFormatter option
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	h := m.currentHeadline()

	trailer := m.editEntryContext(h, false)

	if strings.Contains(trailer, "formatted into org-mode links") {
		t.Errorf("trailer should not mention URL formatting when it's disabled:\n%s", trailer)
	}
}

func TestEditEntryContextShowsSiblingsFileAndMarker(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Read the RFC linked in yesterday's design review")
	h := m.currentHeadline()
	if h.Parent != nil {
		t.Fatalf("fixture assumption broken: expected a top-level headline")
	}

	trailer := m.editEntryContext(h, false)

	if !strings.Contains(trailer, "# inbox.org\n") {
		t.Errorf("trailer missing the file name header:\n%s", trailer)
	}
	if !strings.Contains(trailer, "# * TODO Call the vet about Fido's checkup\n") {
		t.Errorf("trailer missing the previous sibling:\n%s", trailer)
	}
	if !strings.Contains(trailer, "# * [THIS ENTRY HERE]\n") {
		t.Errorf("trailer missing the marker for the entry itself (level 1, one star):\n%s", trailer)
	}
	if !strings.Contains(trailer, "# * TODO Follow up with finance about the Q3 budget doc\n") {
		t.Errorf("trailer missing the next sibling:\n%s", trailer)
	}
	if !strings.Contains(trailer, "Editing this entry.") {
		t.Errorf("trailer missing the action/instructions:\n%s", trailer)
	}
	if strings.Contains(trailer, "Ship orgtd") {
		t.Errorf("trailer should have no parent line (top-level headline):\n%s", trailer)
	}

	for _, line := range strings.Split(strings.TrimRight(trailer, "\n"), "\n") {
		if line != "" && !strings.HasPrefix(line, "#") {
			t.Errorf("non-comment line in context block: %q", line)
		}
	}
}

func TestEditEntryContextShowsOwnChildrenBelowMarker(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Ship orgtd v0.1")
	h := m.currentHeadline()
	if len(h.Children) == 0 {
		t.Fatalf("fixture assumption broken: expected children")
	}

	trailer := m.editEntryContext(h, false)

	marker := strings.Index(trailer, "[THIS ENTRY HERE]")
	child := strings.Index(trailer, "# ** DONE Write the design document\n")
	if marker < 0 || child < 0 || child < marker {
		t.Errorf("expected the marker followed by this entry's own children as a sub-tree:\n%s", trailer)
	}
	if !strings.Contains(trailer, "# ** NEXT Implement the org file parser\n") {
		t.Errorf("trailer missing a second child:\n%s", trailer)
	}
}

func TestEditEntryContextShowsParentWhenNestedAndInsertWording(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Implement the org file parser") // a child of "Ship orgtd v0.1"
	h := m.currentHeadline()
	if h.Parent == nil || h.Level != 2 {
		t.Fatalf("fixture assumption broken: expected a level-2 headline with a parent")
	}

	trailer := m.editEntryContext(h, true)

	if !strings.Contains(trailer, "# * Ship orgtd v0.1\n") {
		t.Errorf("trailer missing the parent (single star, level 1):\n%s", trailer)
	}
	if !strings.Contains(trailer, "# ** DONE Write the design document\n") {
		t.Errorf("trailer missing the previous sibling (two stars, level 2):\n%s", trailer)
	}
	if !strings.Contains(trailer, "# ** [THIS ENTRY HERE]\n") {
		t.Errorf("trailer missing the marker (two stars, level 2):\n%s", trailer)
	}
	if !strings.Contains(trailer, "# ** TODO Implement the Bubble Tea viewer\n") {
		t.Errorf("trailer missing the next sibling (two stars, level 2):\n%s", trailer)
	}
	if !strings.Contains(trailer, "Inserting a new entry.") {
		t.Errorf("trailer missing the insert-specific action text:\n%s", trailer)
	}
}

func TestEditEntryContextShowsEarlierSiblingsSummaryWhenMultiple(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	// 4th child of "Ship orgtd v0.1": one immediate previous sibling
	// ("Implement the Bubble Tea viewer"), plus two earlier ones.
	m.cursor = findRow(t, m, "Get feedback on the keybinding scheme")
	h := m.currentHeadline()

	trailer := m.editEntryContext(h, false)

	if !strings.Contains(trailer, "... 2 earlier siblings ...\n") {
		t.Errorf("trailer missing the earlier-siblings summary:\n%s", trailer)
	}
	if !strings.Contains(trailer, "# ** TODO Implement the Bubble Tea viewer\n") {
		t.Errorf("trailer missing the immediate previous sibling:\n%s", trailer)
	}
	if strings.Contains(trailer, "Write the design document") {
		t.Errorf("trailer should not spell out siblings covered by the summary:\n%s", trailer)
	}
}

func TestEditEntryContextOmitsSummaryWithOnlyOneEarlierSibling(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Implement the org file parser") // 2nd child: one previous, zero earlier
	h := m.currentHeadline()

	trailer := m.editEntryContext(h, false)

	if strings.Contains(trailer, "earlier sibling") {
		t.Errorf("trailer should not show a summary when there's nothing more to summarize:\n%s", trailer)
	}
}

func TestEditEntryContextAtBoundaries(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup") // first headline in inbox.org
	h := m.currentHeadline()

	trailer := m.editEntryContext(h, false)

	// No parent, no previous sibling: nothing before the marker itself.
	if strings.Contains(trailer, "Ship orgtd") {
		t.Errorf("trailer should have no parent/sibling line at the very first entry:\n%s", trailer)
	}
	if !strings.Contains(trailer, "# * [THIS ENTRY HERE]\n") {
		t.Errorf("trailer missing the marker:\n%s", trailer)
	}
	if !strings.Contains(trailer, "# * TODO Read the RFC linked in yesterday's design review\n") {
		t.Errorf("trailer missing the next sibling:\n%s", trailer)
	}
}

func TestLaunchEditorTempFileHasContextTrailerThatDoesNotLeak(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")

	before, _ := filepath.Glob(filepath.Join(os.TempDir(), "orgtd-edit-*.org"))

	_, cmd := sendKeyCmd(m, "i")
	if cmd == nil {
		t.Fatalf("expected a non-nil edit command")
	}

	after, _ := filepath.Glob(filepath.Join(os.TempDir(), "orgtd-edit-*.org"))
	newPath := ""
	for _, p := range after {
		found := false
		for _, old := range before {
			if p == old {
				found = true
				break
			}
		}
		if !found {
			newPath = p
		}
	}
	if newPath == "" {
		t.Fatalf("could not find the newly created temp file among %v", after)
	}
	defer os.Remove(newPath)

	data, err := os.ReadFile(newPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(data)

	if !strings.HasPrefix(content, "TODO Call the vet about Fido's checkup") {
		t.Errorf("temp file should start with the entry's own text, bullet-free:\n%s", content)
	}
	if strings.Contains(strings.SplitN(content, "\n", 2)[0], "*") {
		t.Errorf("temp file's first line should have no bullet:\n%s", content)
	}
	if !strings.Contains(content, "# inbox.org\n") {
		t.Errorf("temp file missing the file name header:\n%s", content)
	}
	if !strings.Contains(content, "# * [THIS ENTRY HERE]\n") {
		t.Errorf("temp file missing the entry marker:\n%s", content)
	}
	if !strings.Contains(content, "# * TODO Read the RFC linked in yesterday's design review\n") {
		t.Errorf("temp file missing next-entry context:\n%s", content)
	}
	if !strings.Contains(content, "Editing this entry.") {
		t.Errorf("temp file missing the instructional text:\n%s", content)
	}

	// The trailer must not survive finishEdit, whether or not the user
	// deletes it themselves.
	updated, _ := m.Update(editFinishedMsg{path: newPath, target: m.currentHeadline()})
	m2 := updated.(Model)
	h := m2.currentHeadline()
	if strings.Contains(strings.Join(h.Body, "\n"), "#") {
		t.Errorf("comment trailer leaked into the saved body: %#v", h.Body)
	}
	if h.Title != "Call the vet about Fido's checkup" {
		t.Errorf("title = %q, unaffected fields should round-trip", h.Title)
	}
}

func TestBuildEditorCommandAddsLineArgForKnownEditors(t *testing.T) {
	before := "# inbox.org\n#\n# * some parent\n" // 3 lines of context

	for _, editorEnv := range []string{"vim", "nvim", "vi", "gvim", "mvim", "emacs", "emacsclient", "nano"} {
		t.Run(editorEnv, func(t *testing.T) {
			cmd := buildEditorCommand(editorEnv, "/tmp/x.org", before, noCursorPlacement, 0)
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

func TestBuildEditorCommandSkipsLineArgForUnknownEditors(t *testing.T) {
	for _, editorEnv := range []string{"code --wait", "subl", "nvim-but-not-really", "cat"} {
		t.Run(editorEnv, func(t *testing.T) {
			cmd := buildEditorCommand(editorEnv, "/tmp/x.org", "# a\n# b\n", noCursorPlacement, 0)
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

// TestSplitCommandFields guards against a real bug: the naive
// strings.Fields split (used by both buildEditorCommand and
// runURLFormatter before this) has no concept of quoting, so a
// configured command with a quoted multi-word argument — e.g.
// `myformatter --template "a template" x` — got torn apart into
// separate fields with the literal quote characters still attached
// ("\"a", "template\"") instead of becoming one argument ("a template").
func TestSplitCommandFields(t *testing.T) {
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
		got := splitCommandFields(c.in)
		if len(got) != len(c.want) {
			t.Errorf("splitCommandFields(%q) = %#v, want %#v", c.in, got, c.want)
			continue
		}
		for i := range c.want {
			if got[i] != c.want[i] {
				t.Errorf("splitCommandFields(%q)[%d] = %q, want %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}

// TestExpandHomeField guards against a real bug: a command typed on the
// command line (--editor, --url-formatter) gets "~" expanded for free by
// the invoking shell before orgtd ever sees it, but the identical value
// read from the config file reaches us raw and unexpanded (no shell is
// involved there), so "~/bin/myformatter" was handed to exec.Command
// completely literally and failed to launch — silently, since a failed
// exec just leaves input unchanged.
func TestExpandHomeField(t *testing.T) {
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
		if got := expandHomeField(c.in); got != c.want {
			t.Errorf("expandHomeField(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestBuildEditorCommandExpandsHomeInEditorPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory available")
	}
	cmd := buildEditorCommand("~/bin/myeditor --wait", "/tmp/x.org", "", noCursorPlacement, 0)
	want := filepath.Join(home, "bin/myeditor")
	if cmd.Args[0] != want {
		t.Errorf("Args[0] = %q, want %q (expanded)", cmd.Args[0], want)
	}
	if cmd.Args[1] != "--wait" {
		t.Errorf("Args[1] = %q, want %q (unaffected extra arg)", cmd.Args[1], "--wait")
	}
}

func TestBuildEditorCommandHandlesQuotedArgument(t *testing.T) {
	cmd := buildEditorCommand(`myeditor --template "a template" -x`, "/tmp/x.org", "", noCursorPlacement, 0)
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

func TestBuildEditorCommandPreservesExtraArgsAndOrder(t *testing.T) {
	cmd := buildEditorCommand("emacs -nw --debug-init", "/tmp/x.org", "# one line\n", noCursorPlacement, 0)
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

func TestBuildEditorCommandDefaultsToVimWhenEditorUnset(t *testing.T) {
	cmd := buildEditorCommand("", "/tmp/x.org", "# a\n", noCursorPlacement, 0)
	if cmd.Args[0] != "vim" {
		t.Errorf("Args[0] = %q, want vim (the default)", cmd.Args[0])
	}
	if got := cmd.Args[len(cmd.Args)-2]; got != "+2" {
		t.Errorf("line arg = %q, want +2", got)
	}
}

func TestCursorPlacementArgEntryStartForVimFamily(t *testing.T) {
	for _, base := range []string{"vi", "vim", "nvim", "gvim", "mvim"} {
		t.Run(base, func(t *testing.T) {
			got := cursorPlacementArg(base, 4, 5, cursorAtEntryStart)
			want := "+call cursor(4,5)|startinsert"
			if got != want {
				t.Errorf("cursorPlacementArg(%q, ...) = %q, want %q", base, got, want)
			}
		})
	}
}

func TestCursorPlacementArgLineEndForVimFamily(t *testing.T) {
	for _, base := range []string{"vi", "vim", "nvim", "gvim", "mvim"} {
		t.Run(base, func(t *testing.T) {
			got := cursorPlacementArg(base, 4, 5, cursorAtLineEnd)
			want := "+4|startinsert!"
			if got != want {
				t.Errorf("cursorPlacementArg(%q, ...) = %q, want %q", base, got, want)
			}
		})
	}
}

func TestCursorPlacementArgFallsBackToBareLineForNonVimEditors(t *testing.T) {
	for _, base := range []string{"emacs", "emacsclient", "nano"} {
		for _, placement := range []editorCursorPlacement{cursorAtEntryStart, cursorAtLineEnd} {
			got := cursorPlacementArg(base, 4, 5, placement)
			if got != "+4" {
				t.Errorf("cursorPlacementArg(%q, placement=%v) = %q, want the bare \"+4\" — %s has no insert-mode concept to start", base, placement, got, base)
			}
		}
	}
}

func TestCursorPlacementArgNoPlacementIsAlwaysBareEvenForVim(t *testing.T) {
	if got := cursorPlacementArg("vim", 4, 5, noCursorPlacement); got != "+4" {
		t.Errorf("cursorPlacementArg(vim, noCursorPlacement) = %q, want \"+4\"", got)
	}
}

func TestBuildEditorCommandEntryStartPlacementForVim(t *testing.T) {
	// "before" is 3 lines of context, so the real content starts on line 4.
	before := "# inbox.org\n#\n# * some parent\n"
	cmd := buildEditorCommand("vim", "/tmp/x.org", before, cursorAtEntryStart, 5)
	lineArg := cmd.Args[len(cmd.Args)-2]
	if want := "+call cursor(4,5)|startinsert"; lineArg != want {
		t.Errorf("line arg = %q, want %q", lineArg, want)
	}
}

func TestBuildEditorCommandLineEndPlacementForVim(t *testing.T) {
	cmd := buildEditorCommand("nvim", "/tmp/x.org", "# one\n", cursorAtLineEnd, 99)
	lineArg := cmd.Args[len(cmd.Args)-2]
	if want := "+2|startinsert!"; lineArg != want {
		t.Errorf("line arg = %q, want %q (col is irrelevant to cursorAtLineEnd)", lineArg, want)
	}
}

func TestBuildEditorCommandEntryStartPlacementFallsBackForEmacsAndNano(t *testing.T) {
	for _, editorEnv := range []string{"emacs", "nano"} {
		t.Run(editorEnv, func(t *testing.T) {
			cmd := buildEditorCommand(editorEnv, "/tmp/x.org", "# one\n", cursorAtEntryStart, 5)
			lineArg := cmd.Args[len(cmd.Args)-2]
			if lineArg != "+2" {
				t.Errorf("line arg = %q, want the bare \"+2\"", lineArg)
			}
		})
	}
}

func TestResolveEntryCursorPlacementDowngradesToLineEndForBlankEntry(t *testing.T) {
	// A fresh o/O template: no keyword, no priority, no title — the
	// stripped-bullet first line is completely empty.
	placement, col := resolveEntryCursorPlacement("", cursorAtEntryStart)
	if placement != cursorAtLineEnd {
		t.Errorf("placement = %v, want cursorAtLineEnd (nothing on the first line)", placement)
	}
	if col != 1 {
		t.Errorf("col = %d, want 1 (unused by cursorAtLineEnd, but still column 1)", col)
	}
}

func TestResolveEntryCursorPlacementKeepsEntryStartWhenTitleExists(t *testing.T) {
	placement, col := resolveEntryCursorPlacement("TODO Buy milk\n", cursorAtEntryStart)
	if placement != cursorAtEntryStart {
		t.Errorf("placement = %v, want cursorAtEntryStart (there's real text on the line)", placement)
	}
	if col != 1 {
		t.Errorf("col = %d, want 1 (there's no bullet to land after)", col)
	}
}

func TestResolveEntryCursorPlacementKeepsEntryStartWithKeywordButNoTitle(t *testing.T) {
	// writeHeadlineFields always writes a trailing space after the
	// keyword, so "TODO " (note the trailing space) is a non-empty first
	// line even with a blank title — this should stay cursorAtEntryStart.
	placement, col := resolveEntryCursorPlacement("TODO \n", cursorAtEntryStart)
	if placement != cursorAtEntryStart {
		t.Errorf("placement = %v, want cursorAtEntryStart: \"TODO \" is a non-empty first line", placement)
	}
	if col != 1 {
		t.Errorf("col = %d, want 1", col)
	}
}

func TestResolveEntryCursorPlacementNeverAppliesToOtherPlacements(t *testing.T) {
	if placement, _ := resolveEntryCursorPlacement("", cursorAtLineEnd); placement != cursorAtLineEnd {
		t.Errorf("cursorAtLineEnd should pass through unchanged, got %v", placement)
	}
	if placement, _ := resolveEntryCursorPlacement("", noCursorPlacement); placement != noCursorPlacement {
		t.Errorf("noCursorPlacement should pass through unchanged, got %v", placement)
	}
}

func TestBuildEditorCommandLineNumberMatchesContextLength(t *testing.T) {
	cases := []struct {
		before string
		want   string
	}{
		{"", "+1"},
		{"# one\n", "+2"},
		{"# one\n# two\n# three\n# four\n# five\n", "+6"},
	}
	for _, c := range cases {
		cmd := buildEditorCommand("vim", "/tmp/x.org", c.before, noCursorPlacement, 0)
		got := cmd.Args[len(cmd.Args)-2]
		if got != c.want {
			t.Errorf("buildEditorCommand with %d context lines: line arg = %q, want %q",
				strings.Count(c.before, "\n"), got, c.want)
		}
	}
}
