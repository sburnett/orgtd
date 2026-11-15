package capture

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sburnett/orgtd/internal/org"
)

var pst = time.FixedZone("PST", -8*3600)

func sample() Capture {
	return Capture{
		ID:        "6F1C",
		CreatedAt: time.Date(2026, 11, 14, 9, 12, 0, 0, pst),
		Title:     "Call the plumber",
		Body:      "re: kitchen sink",
		Tags:      []string{"home"},
		Deadline:  "2026-11-18",
	}
}

func TestRenderMatchesDesignExample(t *testing.T) {
	got, err := Render(sample())
	if err != nil {
		t.Fatal(err)
	}
	want := `* Call the plumber   :home:
  DEADLINE: <2026-11-18 Wed>
  :PROPERTIES:
  :CREATED: [2026-11-14 Sat 09:12]
  :END:
  re: kitchen sink
`
	if got != want {
		t.Errorf("Render =\n%s\nwant\n%s", got, want)
	}
}

func TestRenderRoundTripsThroughParse(t *testing.T) {
	c := sample()
	c.Scheduled = "2026-11-16 14:30"
	c.Body = "line one\n\n* not a headline\n  indented"
	c.Tags = []string{"home", "first.last", "", "home"}
	text, err := Render(c)
	if err != nil {
		t.Fatal(err)
	}
	f, err := org.Parse(strings.NewReader(text), "inbox.org")
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Headlines) != 1 {
		t.Fatalf("parsed %d headlines from\n%s", len(f.Headlines), text)
	}
	h := f.Headlines[0]
	if h.Title != "Call the plumber" || !reflect.DeepEqual(h.Tags, []string{"home", "first_last"}) {
		t.Errorf("title/tags = %q %q", h.Title, h.Tags)
	}
	if h.Scheduled.Raw != "2026-11-16 Mon 14:30" || h.Deadline.Raw != "2026-11-18 Wed" {
		t.Errorf("planning = %v %v", h.Scheduled, h.Deadline)
	}
	if h.Properties["CREATED"] != "[2026-11-14 Sat 09:12]" {
		t.Errorf("CREATED = %q", h.Properties["CREATED"])
	}
	if got := strings.Join(h.TrimmedBody(), "\n"); !strings.Contains(got, "* not a headline") {
		t.Errorf("body = %q", got)
	}
	if org.RenderFile(f) != text {
		t.Errorf("re-rendering the parsed file changed it:\n%s\nvs\n%s", org.RenderFile(f), text)
	}
}

func TestValidate(t *testing.T) {
	for _, c := range []Capture{
		{Title: "  "},
		{Title: "x", Deadline: "next tuesday"},
		{Title: "x", Scheduled: "2026-13-01"},
	} {
		if err := c.Validate(); err == nil {
			t.Errorf("Validate(%+v) = nil, want an error", c)
		}
	}
}

func TestParseDateAcceptsWhatGdAccepts(t *testing.T) {
	now := time.Date(2026, 11, 14, 9, 0, 0, 0, time.Local) // a Saturday
	cases := map[string]string{
		"2026-12-25":       "2026-12-25",
		"2026-12-25 08:00": "2026-12-25 08:00",
		"3d":               "2026-11-17",
		"2w":               "2026-11-28",
		"next tue":         "2026-11-17",
	}
	for in, want := range cases {
		got, err := ParseDate(in, now)
		if err != nil || got != want {
			t.Errorf("ParseDate(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := ParseDate("whenever", now); err == nil {
		t.Error("ParseDate(whenever) succeeded")
	}
}

func TestAppendEntryIsAPureAppend(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "inbox.org")
	original := "#+TITLE: Inbox\n* odd   spacing   kept\n  no trailing newline"
	os.WriteFile(path, []byte(original), 0o644)
	if err := AppendEntry(path, "* New\n"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if want := original + "\n* New\n"; string(got) != want {
		t.Errorf("file = %q, want %q", got, want)
	}

	missing := filepath.Join(dir, "new.org")
	if err := AppendEntry(missing, "* First\n"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(missing); string(got) != "* First\n" {
		t.Errorf("new file = %q", got)
	}
}
