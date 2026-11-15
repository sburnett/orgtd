// Package capture renders a captured inbox item into org text and
// appends it to a file. It is shared by the terminal UI ("gC", whose
// CREATED stamp format comes from here) and the iOS core (mobilecore),
// so an entry captured on the phone is byte-for-byte what the desktop
// would have written for the same fields. See ios/DESIGN.md §4.4.
package capture

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sburnett/orgtd/internal/org"
	"github.com/sburnett/orgtd/internal/orgdate"
)

// Capture is one captured item. Its JSON form is the capture queue's
// on-disk format (see internal/capturequeue) and is also written by the
// iOS share extension, so field names are a stable contract.
type Capture struct {
	// ID identifies the capture within the queue only; it's never
	// written to an org file.
	ID string `json:"id"`
	// CreatedAt is when the item was captured; it becomes the CREATED
	// property, in CreatedAt's own time zone.
	CreatedAt time.Time `json:"createdAt"`
	// Keyword is the TODO keyword to start the headline with ("" for
	// none). Whether it's a configured keyword is the caller's to check
	// (see mobilecore); here it only has to be a single word.
	Keyword string   `json:"keyword,omitempty"`
	Title   string   `json:"title"`
	Body    string   `json:"body,omitempty"`
	Tags    []string `json:"tags,omitempty"`
	// Scheduled and Deadline are "YYYY-MM-DD" or "YYYY-MM-DD HH:MM"
	// (see ParseDate), or empty for none.
	Scheduled string `json:"scheduled,omitempty"`
	Deadline  string `json:"deadline,omitempty"`
}

// CreatedStamp formats t as a CREATED property value, the inactive
// timestamp every new entry gets: "[2026-11-14 Sat 09:12]".
func CreatedStamp(t time.Time) string {
	return "[" + t.Format("2006-01-02 Mon 15:04") + "]"
}

// ParseDate resolves a typed date the way the desktop's "gd" does — an
// exact date, "3d"/"2w" shorthand, or a phrase like "next tue" — and
// returns it in the form Capture.Scheduled/Deadline hold.
func ParseDate(input string, now time.Time) (string, error) {
	ts, err := orgdate.ParseDeadlineInput(input, now)
	if err != nil {
		return "", err
	}
	return orgdate.PrefillInput(ts), nil
}

// timestamp turns a Capture date field into an active org timestamp, nil
// for "".
func timestamp(field, value string) (*org.Timestamp, error) {
	if value == "" {
		return nil, nil
	}
	t, hasTime, err := orgdate.ParseFlexible(strings.TrimSpace(value))
	if err != nil {
		return nil, fmt.Errorf("capture: %s: %w", field, err)
	}
	layout := "2006-01-02 Mon"
	if hasTime {
		layout = "2006-01-02 Mon 15:04"
	}
	return &org.Timestamp{Active: true, Raw: t.Format(layout)}, nil
}

// Validate reports the first problem that would stop c from rendering.
func (c Capture) Validate() error {
	if strings.TrimSpace(c.Title) == "" {
		return fmt.Errorf("capture: title is required")
	}
	if k := strings.TrimSpace(c.Keyword); k != "" && strings.ContainsAny(k, " \t:") {
		return fmt.Errorf("capture: keyword %q must be a single word", c.Keyword)
	}
	if _, err := timestamp("scheduled", c.Scheduled); err != nil {
		return err
	}
	if _, err := timestamp("deadline", c.Deadline); err != nil {
		return err
	}
	return nil
}

// Headline builds c as a top-level org headline: the keyword, if any, the
// title (on one line),
// tags (sanitized to org's tag syntax, duplicates and blanks dropped), a
// planning line, a CREATED property, and the body.
func (c Capture) Headline() (*org.Headline, error) { return c.HeadlineAt(1) }

// HeadlineAt is Headline at outline level (1 for top-level), with the
// body indented to match — for an entry added under a parent.
func (c Capture) HeadlineAt(level int) (*org.Headline, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if level < 1 {
		level = 1
	}
	h := &org.Headline{Level: level, Keyword: strings.TrimSpace(c.Keyword), Title: strings.Join(strings.Fields(c.Title), " ")}
	seen := map[string]bool{}
	for _, tag := range c.Tags {
		tag = org.SanitizeTag(strings.TrimSpace(tag))
		if tag != "" && !seen[tag] {
			seen[tag] = true
			h.Tags = append(h.Tags, tag)
		}
	}
	h.Scheduled, _ = timestamp("scheduled", c.Scheduled)
	h.Deadline, _ = timestamp("deadline", c.Deadline)
	h.SetProperty("CREATED", CreatedStamp(c.CreatedAt))
	indent := strings.Repeat(" ", h.Level+1)
	body := strings.TrimRight(strings.ReplaceAll(c.Body, "\r\n", "\n"), " \t\n")
	if strings.TrimSpace(body) != "" {
		for _, line := range strings.Split(body, "\n") {
			line = strings.TrimRight(line, " \t")
			if line == "" {
				h.Body = append(h.Body, "")
				continue
			}
			// A body line that looks like a headline would become one
			// when the file is next parsed; indenting it, as every body
			// line is, prevents that.
			h.Body = append(h.Body, indent+line)
		}
	}
	return h, nil
}

// Render is c's org text, exactly as org.RenderHeadline writes it (so a
// later desktop save of the same file leaves it byte-identical).
func Render(c Capture) (string, error) { return RenderAt(c, 1) }

// RenderAt is Render at outline level (see HeadlineAt).
func RenderAt(c Capture, level int) (string, error) {
	h, err := c.HeadlineAt(level)
	if err != nil {
		return "", err
	}
	return org.RenderHeadline(h), nil
}

// AppendEntry appends rendered to the file at path, byte for byte,
// without re-rendering anything already there, so the change is a pure
// append (see ios/DESIGN.md §4.4). A missing final newline is added
// first; a missing file is created. The write is atomic (temp file and
// rename), like org.WriteFile.
func AppendEntry(path, rendered string) error {
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("capture: reading %s: %w", path, err)
	}
	var b strings.Builder
	b.Write(existing)
	if len(existing) > 0 && existing[len(existing)-1] != '\n' {
		b.WriteByte('\n')
	}
	b.WriteString(rendered)
	return writeAtomic(path, []byte(b.String()))
}

// writeAtomic writes data to path via a temp file in the same directory
// and a rename, so a crash never leaves a truncated file.
func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".orgtd-append-*.tmp")
	if err != nil {
		return fmt.Errorf("capture: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return fmt.Errorf("capture: writing %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("capture: writing %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("capture: replacing %s: %w", path, err)
	}
	return nil
}
