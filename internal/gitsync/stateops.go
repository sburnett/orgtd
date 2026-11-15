package gitsync

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/sburnett/orgtd/internal/capturequeue"
	"github.com/sburnett/orgtd/internal/org"
	"github.com/sburnett/orgtd/internal/orgdate"
)

// DroppedOp is a state change that could no longer be applied when the
// branch was rebuilt — its entry was changed, moved or removed on the
// desktop — kept so the app can say so.
type DroppedOp struct {
	Title  string    `json:"title"`
	From   string    `json:"from"`
	To     string    `json:"to"`
	Reason string    `json:"reason"`
	At     time.Time `json:"at"`
}

// ErrEntryChanged means a state change's entry isn't there any more as
// it was when the change was made.
var ErrEntryChanged = errors.New("the entry was changed, moved or removed since")

// Fingerprint identifies an entry's own text (not its children's), for
// telling whether it has changed since a state change was queued.
func Fingerprint(h *org.Headline) string {
	sum := sha256.Sum256([]byte(org.RenderEntry(h)))
	return hex.EncodeToString(sum[:8])
}

// TitlePath is the titles from h's top-level ancestor down to h.
func TitlePath(h *org.Headline) []string {
	var p []string
	for cur := h; cur != nil; cur = cur.Parent {
		p = append([]string{cur.Title}, p...)
	}
	return p
}

// findEntry finds the entry an op was made on: the first with the op's
// title path whose fingerprint still matches.
func findEntry(f *org.File, op capturequeue.StateOp) (*org.Headline, error) {
	var found *org.Headline
	org.Walk(f.Headlines, func(h *org.Headline) {
		if found != nil || h.Title != op.Title() || h.Level != len(op.Path) {
			return
		}
		if strings.Join(TitlePath(h), "\x00") == strings.Join(op.Path, "\x00") && Fingerprint(h) == op.Fingerprint {
			found = h
		}
	})
	if found == nil {
		return nil, ErrEntryChanged
	}
	return found, nil
}

// applyOp makes op's change in the working tree under dir, rewriting
// only the entry's own lines of the raw file so the diff is just that
// entry: its headline line, plus CLOSED, or the repeater dates and
// LAST_REPEAT for a repeating item (orgdate.ChangeState, as desktop "r").
// Times come from op.CreatedAt, so replays are identical.
func applyOp(dir string, op capturequeue.StateOp) error {
	p := filepath.Join(dir, filepath.FromSlash(op.File))
	data, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return ErrEntryChanged
	}
	if err != nil {
		return err
	}
	f, err := org.Parse(bytes.NewReader(data), p)
	if err != nil {
		return err
	}
	h, err := findEntry(f, op)
	if err != nil {
		return err
	}
	if h.Keyword != op.From {
		return ErrEntryChanged
	}
	lines := splitLines(data)
	if h.SrcEnd > len(lines) || h.SrcStart >= h.SrcEnd {
		return fmt.Errorf("gitsync: %s: entry %q out of range", op.File, op.Title())
	}
	own := patchEntry(lines[h.SrcStart:h.SrcEnd], h, op.To, op.CreatedAt)
	out := append(append(append([]string{}, lines[:h.SrcStart]...), own...), lines[h.SrcEnd:]...)
	text := strings.Join(out, "\n")
	if len(data) > 0 && data[len(data)-1] == '\n' {
		text += "\n"
	}
	return writeFileAtomic(p, []byte(text))
}

// patchEntry returns h's own lines (as in the file) with h changed to
// keyword at the time at, rewriting only the lines that change: the
// headline line, the planning line if it changes (added or removed as
// needed), and, for a repeating item, the LAST_REPEAT property (in the
// existing drawer, or a new one). Every other line is kept byte for byte.
func patchEntry(own []string, h *org.Headline, keyword string, at time.Time) []string {
	hadPlanning := h.Scheduled != nil || h.Deadline != nil || h.Closed != nil
	oldPlanning := org.PlanningLine(h)
	sc := orgdate.ChangeState(h, keyword, at)
	sc.Apply(h)
	indent := strings.Repeat(" ", h.Level+1)

	headline, _, _ := strings.Cut(org.RenderEntry(h), "\n")
	out := []string{headline}
	rest := own[1:]
	newPlanning := org.PlanningLine(h)
	switch {
	case newPlanning == oldPlanning && hadPlanning:
		out = append(out, rest[0]) // unchanged: keep it as written
		rest = rest[1:]
	case hadPlanning:
		rest = rest[1:]
		if newPlanning != "" {
			out = append(out, indent+newPlanning)
		}
	case newPlanning != "":
		out = append(out, indent+newPlanning)
	}
	if sc.Repeat {
		rest = setProperty(rest, indent, "LAST_REPEAT", sc.LastRepeat)
	}
	return append(out, rest...)
}

// setProperty sets key in the property drawer among lines (an entry's
// own lines after its planning), replacing the key's line or adding one
// before :END:, or adds a drawer at the top if there is none.
func setProperty(lines []string, indent, key, value string) []string {
	start, end := -1, -1
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if start < 0 && t == ":PROPERTIES:" {
			start = i
		} else if start >= 0 && t == ":END:" {
			end = i
			break
		}
	}
	line := indent + ":" + key + ": " + value
	if start < 0 || end < 0 {
		return append([]string{indent + ":PROPERTIES:", line, indent + ":END:"}, lines...)
	}
	out := append([]string{}, lines...)
	for i := start + 1; i < end; i++ {
		if strings.HasPrefix(strings.TrimSpace(out[i]), ":"+key+":") {
			out[i] = line
			return out
		}
	}
	return append(out[:end], append([]string{line}, out[end:]...)...)
}

// splitLines splits data into lines numbered the way org.Parse numbers
// them (a final newline doesn't start another line), each kept byte for
// byte, so the lines a state change doesn't touch are written back
// exactly as they were (a "\r" of a CRLF file included).
func splitLines(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	lines := strings.Split(string(data), "\n")
	if data[len(data)-1] == '\n' {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func writeFileAtomic(p string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(p), ".orgtd-state-*.tmp")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), p)
}

// UnionMerged reports whether the repository at dir's .gitattributes
// gives file (relative, slash-separated) merge=union. The phone refuses
// to change states in such a file: a union merge keeps both the old and
// the new version of an edited line, duplicating the entry.
func UnionMerged(dir, file string) bool {
	data, err := os.ReadFile(filepath.Join(dir, ".gitattributes"))
	if err != nil {
		return false
	}
	union := false
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		pattern := fields[0]
		matched := false
		if strings.Contains(strings.TrimPrefix(pattern, "/"), "/") {
			matched, _ = path.Match(strings.TrimPrefix(pattern, "/"), file)
		} else {
			matched, _ = path.Match(pattern, path.Base(file))
		}
		if !matched {
			continue
		}
		for _, attr := range fields[1:] {
			switch attr {
			case "merge=union":
				union = true
			case "-merge", "!merge", "merge", "merge=text", "merge=binary":
				union = false
			}
		}
	}
	return union
}

// NewStateOp describes changing h (in file, relative to the repository
// root, as currently checked out) to keyword at the time at.
func NewStateOp(file string, h *org.Headline, keyword string, at time.Time) capturequeue.StateOp {
	return capturequeue.StateOp{
		CreatedAt:   at,
		File:        file,
		Path:        TitlePath(h),
		Fingerprint: Fingerprint(h),
		From:        h.Keyword,
		To:          keyword,
	}
}
