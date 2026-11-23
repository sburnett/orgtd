package org

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderHeadlineRoundTrips(t *testing.T) {
	f := mustParse(t)
	project := f.Headlines[0] // "Someday goal", with two children

	rendered := RenderHeadline(project)

	reparsed, err := Parse(strings.NewReader(rendered), "rendered.org", DefaultKeywords())
	if err != nil {
		t.Fatalf("re-parsing rendered headline: %v", err)
	}
	if len(reparsed.Headlines) != 1 {
		t.Fatalf("got %d top-level headlines, want 1\n---\n%s", len(reparsed.Headlines), rendered)
	}

	got := reparsed.Headlines[0]
	if got.Title != project.Title || got.Level != project.Level {
		t.Errorf("got title=%q level=%d, want title=%q level=%d", got.Title, got.Level, project.Title, project.Level)
	}
	if len(got.Children) != len(project.Children) {
		t.Fatalf("got %d children, want %d", len(got.Children), len(project.Children))
	}
	for i := range project.Children {
		want := project.Children[i]
		gotChild := got.Children[i]
		if gotChild.Title != want.Title || gotChild.Keyword != want.Keyword {
			t.Errorf("child %d = (keyword=%q title=%q), want (keyword=%q title=%q)",
				i, gotChild.Keyword, gotChild.Title, want.Keyword, want.Title)
		}
	}
}

func TestRenderHeadlinePreservesFields(t *testing.T) {
	f := mustParse(t)
	task := f.Headlines[0].Children[0] // NEXT ... with SCHEDULED, properties, body

	rendered := RenderHeadline(task)
	reparsed, err := Parse(strings.NewReader(rendered), "rendered.org", DefaultKeywords())
	if err != nil {
		t.Fatalf("re-parsing rendered headline: %v", err)
	}
	got := reparsed.Headlines[0]

	if got.Keyword != task.Keyword {
		t.Errorf("keyword = %q, want %q", got.Keyword, task.Keyword)
	}
	if got.Scheduled == nil || got.Scheduled.Raw != task.Scheduled.Raw {
		t.Errorf("scheduled = %#v, want %#v", got.Scheduled, task.Scheduled)
	}
	if got.Properties["ID"] != task.Properties["ID"] {
		t.Errorf("ID property = %q, want %q", got.Properties["ID"], task.Properties["ID"])
	}
	if len(got.Body) != len(task.Body) {
		t.Fatalf("body = %#v, want %#v", got.Body, task.Body)
	}
}

func TestRenderHeadlineWithTags(t *testing.T) {
	f := mustParse(t)
	task := f.Headlines[0].Children[1] // WAITING ... :blocked:review:

	rendered := RenderHeadline(task)
	reparsed, err := Parse(strings.NewReader(rendered), "rendered.org", DefaultKeywords())
	if err != nil {
		t.Fatalf("re-parsing rendered headline: %v", err)
	}
	got := reparsed.Headlines[0]

	if len(got.Tags) != len(task.Tags) {
		t.Fatalf("tags = %#v, want %#v", got.Tags, task.Tags)
	}
	for i := range task.Tags {
		if got.Tags[i] != task.Tags[i] {
			t.Errorf("tags[%d] = %q, want %q", i, got.Tags[i], task.Tags[i])
		}
	}
	if got.Deadline == nil || got.Deadline.Raw != task.Deadline.Raw {
		t.Errorf("deadline = %#v, want %#v", got.Deadline, task.Deadline)
	}
}

func TestRenderFileRoundTrips(t *testing.T) {
	f := mustParse(t)

	rendered := RenderFile(f)
	reparsed, err := Parse(strings.NewReader(rendered), "rendered.org", DefaultKeywords())
	if err != nil {
		t.Fatalf("re-parsing rendered file: %v", err)
	}

	if len(reparsed.Preamble) != len(f.Preamble) {
		t.Fatalf("preamble = %#v, want %#v", reparsed.Preamble, f.Preamble)
	}
	for i := range f.Preamble {
		if reparsed.Preamble[i] != f.Preamble[i] {
			t.Errorf("preamble[%d] = %q, want %q", i, reparsed.Preamble[i], f.Preamble[i])
		}
	}

	if len(reparsed.Headlines) != len(f.Headlines) {
		t.Fatalf("top-level headlines = %d, want %d", len(reparsed.Headlines), len(f.Headlines))
	}
	var titles, wantTitles []string
	Walk(reparsed.Headlines, func(h *Headline) { titles = append(titles, h.Title) })
	Walk(f.Headlines, func(h *Headline) { wantTitles = append(wantTitles, h.Title) })
	if len(titles) != len(wantTitles) {
		t.Fatalf("got %d headlines total, want %d", len(titles), len(wantTitles))
	}
	for i := range wantTitles {
		if titles[i] != wantTitles[i] {
			t.Errorf("headline %d title = %q, want %q", i, titles[i], wantTitles[i])
		}
	}
}

func TestDedentEntryRemovesBulletAndMatchingIndentFromEveryLine(t *testing.T) {
	h := &Headline{
		Level:         2,
		Keyword:       "NEXT",
		Title:         "Implement the org file parser",
		PropertyOrder: []string{"ID"},
		Properties:    map[string]string{"ID": "abc123"},
		Body:          []string{"   Headlines, planning lines, properties, body text."},
	}
	rendered := strings.TrimRight(RenderEntry(h), "\n")

	got := DedentEntry(rendered, h.Level)

	want := "NEXT Implement the org file parser\n" +
		":PROPERTIES:\n" +
		":ID: abc123\n" +
		":END:\n" +
		"Headlines, planning lines, properties, body text."
	if got != want {
		t.Errorf("DedentEntry() =\n%q\nwant\n%q", got, want)
	}
}

func TestIndentEntryReversesDedentEntry(t *testing.T) {
	h := &Headline{
		Level:         2,
		Keyword:       "NEXT",
		Title:         "Implement the org file parser",
		PropertyOrder: []string{"ID"},
		Properties:    map[string]string{"ID": "abc123"},
		Body:          []string{"   Headlines, planning lines, properties, body text."},
	}
	rendered := strings.TrimRight(RenderEntry(h), "\n")

	dedented := DedentEntry(rendered, h.Level)
	got := IndentEntry(dedented, h.Level)

	if got != rendered {
		t.Errorf("IndentEntry(DedentEntry(s)) =\n%q\nwant original\n%q", got, rendered)
	}
}

func TestIndentEntryLeavesBlankLinesAlone(t *testing.T) {
	got := IndentEntry("TODO Buy milk\n\nSecond body line.", 1)
	want := "* TODO Buy milk\n\n  Second body line."
	if got != want {
		t.Errorf("IndentEntry() = %q, want %q (blank line not indented)", got, want)
	}
}

func TestHeadlineLinesFollowsRenderingOnceWritten(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.org")
	if err := os.WriteFile(path, []byte("#+TITLE: x\n* One\nbody\n** Two\n* Three\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := ParseFile(path, DefaultKeywords())
	if err != nil {
		t.Fatal(err)
	}
	one := f.Headlines[0]
	one.Body = append([]string{"new line"}, one.Body...) // pushes Two and Three down a line
	f.Base = []byte(RenderFile(f))                       // as a save records it

	lines := HeadlineLines(f)
	two, three := one.Children[0], f.Headlines[1]
	if lines[one] != 2 || lines[two] != 5 || lines[three] != 6 {
		t.Errorf("lines = one %d, two %d, three %d; want 2, 5, 6", lines[one], lines[two], lines[three])
	}
}

func TestHeadlineLinesUsesSourceLinesForAnUnwrittenFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.org")
	// An unsaved edit makes rendering differ from the disk content;
	// the parsed positions are what's on disk.
	content := "* One          :tag:\n   SCHEDULED: <2026-11-24 Tue>\n\n\nbody\n* Two\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := ParseFile(path, DefaultKeywords())
	if err != nil {
		t.Fatal(err)
	}
	f.Headlines[0].Body = append([]string{"new line"}, f.Headlines[0].Body...)
	if RenderFile(f) == content {
		t.Fatal("fixture renders identically; it doesn't exercise the source-line fallback")
	}

	lines := HeadlineLines(f)
	if lines[f.Headlines[0]] != 1 || lines[f.Headlines[1]] != 6 {
		t.Errorf("lines = %d, %d; want 1, 6", lines[f.Headlines[0]], lines[f.Headlines[1]])
	}
}

// lossyEntries are entries whose regenerated form differs from their
// source: everything here must survive a save untouched when nobody
// edited it.
var lossyEntries = map[string]string{
	"planning trailing text": "* TODO a\n  SCHEDULED: <2026-11-22 Sun> some note\n",
	"timestamp range":        "* TODO a\n  SCHEDULED: <2026-11-22 Sun>--<2026-11-24 Tue>\n",
	"duplicate property":     "* a\n  :PROPERTIES:\n  :X: 1\n  :X: 2\n  :END:\n",
	"dash and plus keys":     "* a\n  :PROPERTIES:\n  :foo-bar: 1\n  :tags+: x\n  :END:\n",
	"late drawer":            "* a\n  body first\n  :PROPERTIES:\n  :ID: 1\n  :END:\n",
	"odd spacing":            "*  TODO   a :t:\n\t:PROPERTIES:\n\t:ID:   1\n\t:END:\n",
}

func TestRenderFileKeepsUnmodifiedEntriesVerbatim(t *testing.T) {
	for name, src := range lossyEntries {
		t.Run(name, func(t *testing.T) {
			// Surround the entry with others, nested under it too, so
			// line ranges between entries are exercised.
			content := "#+TITLE: x\n\n" + src + "** child\n   text\n* other\n"
			f, err := ParseContent([]byte(content), "t.org", DefaultKeywords())
			if err != nil {
				t.Fatal(err)
			}
			if got := RenderFile(f); got != content {
				t.Errorf("RenderFile changed an unmodified file\ngot:\n%s\nwant:\n%s", got, content)
			}
		})
	}
}

func TestRenderFileRerendersOnlyEditedEntries(t *testing.T) {
	content := "* TODO a\n  SCHEDULED: <2026-11-22 Sun> note\n" +
		"* b\n  :PROPERTIES:\n  :foo-bar: 1\n  :END:\n" +
		"** c\n  body first\n  :PROPERTIES:\n  :ID: 1\n  :END:\n"
	f, err := ParseContent([]byte(content), "t.org", DefaultKeywords())
	if err != nil {
		t.Fatal(err)
	}
	f.Headlines[0].Keyword = "DONE"
	got := RenderFile(f)
	want := "* DONE a\n  SCHEDULED: <2026-11-22 Sun>\n" +
		"* b\n  :PROPERTIES:\n  :foo-bar: 1\n  :END:\n" +
		"** c\n  body first\n  :PROPERTIES:\n  :ID: 1\n  :END:\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}

	// A level change counts as an edit, and so does any field change
	// made through a shared pointer (here a Timestamp edited in place).
	f.Headlines[1].Children[0].Level = 3
	f.Headlines[0].Scheduled.Raw = "2026-11-23 Mon"
	got = RenderFile(f)
	if !strings.Contains(got, "*** c\n    :PROPERTIES:\n    :ID: 1\n    :END:\n  body first\n") {
		t.Errorf("demoted entry not re-rendered:\n%s", got)
	}
	if !strings.Contains(got, "SCHEDULED: <2026-11-23 Mon>\n") {
		t.Errorf("in-place timestamp edit lost:\n%s", got)
	}
}

func TestCloneKeepsSourceText(t *testing.T) {
	content := "* a\n  :PROPERTIES:\n  :X: 1\n  :X: 2\n  :END:\n"
	f, err := ParseContent([]byte(content), "t.org", DefaultKeywords())
	if err != nil {
		t.Fatal(err)
	}
	f.Headlines[0] = CloneHeadline(f.Headlines[0])
	if got := RenderFile(f); got != content {
		t.Errorf("got:\n%s\nwant:\n%s", got, content)
	}
}

func TestDrawerKeysWithDashAndPlus(t *testing.T) {
	f, err := ParseContent([]byte("* a\n  :PROPERTIES:\n  :foo-bar: 1\n  :tags+: x\n  :END:\n"), "t.org", DefaultKeywords())
	if err != nil {
		t.Fatal(err)
	}
	h := f.Headlines[0]
	if h.Properties["foo-bar"] != "1" || h.Properties["tags+"] != "x" || len(h.Body) != 0 {
		t.Errorf("properties = %v, body = %q", h.Properties, h.Body)
	}
}

func TestDeletingTheLastPropertyRemovesTheDrawer(t *testing.T) {
	f, err := ParseContent([]byte("* a\n  :PROPERTIES:\n  :X: 1\n  :END:\n  body\n"), "t.org", DefaultKeywords())
	if err != nil {
		t.Fatal(err)
	}
	f.Headlines[0].DeleteProperty("X")
	if got, want := RenderFile(f), "* a\n  body\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	// Unless the body starts with a planning-like line, which an empty
	// drawer keeps in the body.
	f, err = ParseContent([]byte("* a\n  :PROPERTIES:\n  :X: 1\n  :END:\n  SCHEDULED: <2026-11-22 Sun>\n"), "t.org", DefaultKeywords())
	if err != nil {
		t.Fatal(err)
	}
	f.Headlines[0].DeleteProperty("X")
	re, err := Parse(strings.NewReader(RenderFile(f)), "t.org", DefaultKeywords())
	if err != nil {
		t.Fatal(err)
	}
	if h := re.Headlines[0]; h.Scheduled != nil || len(h.Body) != 1 {
		t.Errorf("planning-like body line became planning: scheduled=%v body=%q", h.Scheduled, h.Body)
	}
}
