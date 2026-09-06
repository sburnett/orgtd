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

	reparsed, err := Parse(strings.NewReader(rendered), "rendered.org")
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
	reparsed, err := Parse(strings.NewReader(rendered), "rendered.org")
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
	reparsed, err := Parse(strings.NewReader(rendered), "rendered.org")
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
	reparsed, err := Parse(strings.NewReader(rendered), "rendered.org")
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

func TestWriteFileAtomicRoundTrip(t *testing.T) {
	f := mustParse(t)
	dir := t.TempDir()
	f.Path = filepath.Join(dir, "test.org")

	if err := WriteFile(f); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("directory contains %d entries after WriteFile, want 1 (no leftover temp file): %v", len(entries), entries)
	}

	reparsed, err := ParseFile(f.Path)
	if err != nil {
		t.Fatalf("ParseFile after WriteFile: %v", err)
	}
	if len(reparsed.Headlines) != len(f.Headlines) {
		t.Fatalf("top-level headlines = %d, want %d", len(reparsed.Headlines), len(f.Headlines))
	}
}

func TestWriteFileFailsOnUnwritableDirectory(t *testing.T) {
	f := mustParse(t)
	f.Path = filepath.Join(t.TempDir(), "nonexistent-subdir", "test.org")

	if err := WriteFile(f); err == nil {
		t.Fatalf("expected an error writing into a nonexistent directory")
	}
}
