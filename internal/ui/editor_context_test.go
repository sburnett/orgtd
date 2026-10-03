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

	scratchGlob := filepath.Join(ws.Dir, "scratch", "*", "*", "*", "*.org")
	before, _ := filepath.Glob(scratchGlob)

	_, cmd := sendKeyCmd(m, "i")
	if cmd == nil {
		t.Fatalf("expected a non-nil edit command")
	}

	after, _ := filepath.Glob(scratchGlob)
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
		t.Fatalf("could not find the newly created scratch file among %v", after)
	}

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
