package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestEditorContextShowsSiblingsAndFile(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Read the RFC linked in yesterday's design review")
	h := m.currentHeadline()
	if h.Parent != nil {
		t.Fatalf("fixture assumption broken: expected a top-level headline")
	}

	before, after := m.editorContext(false, h)

	if !strings.Contains(before, "# inbox.org\n") {
		t.Errorf("before-block missing the file name header:\n%s", before)
	}
	if !strings.Contains(before, "# * TODO Call the vet about Fido's checkup\n") {
		t.Errorf("before-block missing the previous sibling:\n%s", before)
	}
	if strings.Contains(before, "Ship orgtd") || strings.Count(before, "*") != 1 {
		t.Errorf("before-block should have no parent line (top-level headline):\n%s", before)
	}

	if !strings.Contains(after, "# * TODO Follow up with finance about the Q3 budget doc\n") {
		t.Errorf("after-block missing the next sibling:\n%s", after)
	}
	if !strings.Contains(after, "Editing this entry.") {
		t.Errorf("after-block missing the action/instructions:\n%s", after)
	}

	for _, block := range []string{before, after} {
		for _, line := range strings.Split(strings.TrimRight(block, "\n"), "\n") {
			if line != "" && !strings.HasPrefix(line, "#") {
				t.Errorf("non-comment line in context block: %q", line)
			}
		}
	}
}

func TestEditorContextShowsParentWhenNested(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Implement the org file parser") // a child of "Ship orgtd v0.1"
	h := m.currentHeadline()
	if h.Parent == nil || h.Level != 2 {
		t.Fatalf("fixture assumption broken: expected a level-2 headline with a parent")
	}

	before, after := m.editorContext(true, h)

	if !strings.Contains(before, "# * Ship orgtd v0.1\n") {
		t.Errorf("before-block missing the parent (single star, level 1):\n%s", before)
	}
	if !strings.Contains(before, "# ** DONE Write the design document\n") {
		t.Errorf("before-block missing the previous sibling (two stars, level 2):\n%s", before)
	}
	if !strings.Contains(after, "# ** TODO Implement the Bubble Tea viewer\n") {
		t.Errorf("after-block missing the next sibling (two stars, level 2):\n%s", after)
	}
	if !strings.Contains(after, "Inserting a new entry.") {
		t.Errorf("after-block missing the insert-specific action text:\n%s", after)
	}
}

func TestEditorContextAtBoundaries(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup") // first headline in inbox.org
	h := m.currentHeadline()

	before, after := m.editorContext(false, h)

	// No parent, no previous sibling: the before-block is just the file header.
	if strings.Contains(before, "*") {
		t.Errorf("before-block should have no parent/sibling line at the very first entry:\n%s", before)
	}
	if !strings.Contains(after, "# * TODO Read the RFC linked in yesterday's design review\n") {
		t.Errorf("after-block missing the next sibling:\n%s", after)
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

	if !strings.Contains(content, "* TODO Call the vet about Fido's checkup") {
		t.Errorf("temp file missing the real content:\n%s", content)
	}
	if !strings.Contains(content, "# inbox.org\n") {
		t.Errorf("temp file missing the file name header:\n%s", content)
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
