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
	"regexp"
	"strings"
	"time"

	"github.com/sburnett/orgtd/internal/capture"
	"github.com/sburnett/orgtd/internal/capturequeue"
	"github.com/sburnett/orgtd/internal/org"
	"github.com/sburnett/orgtd/internal/orgdate"
)

// DroppedOp is a state change that could no longer be applied when the
// branch was rebuilt — its entry was changed, moved or removed on the
// desktop — kept so the app can say so.
type DroppedOp struct {
	Title     string    `json:"title"`
	From      string    `json:"from"`
	To        string    `json:"to"`
	Added     bool      `json:"added,omitempty"`     // a new entry, whose parent changed, rather than a change to one
	Move      int       `json:"move,omitempty"`      // a demote (1) or promote (-1), rather than a keyword change
	Reordered bool      `json:"reordered,omitempty"` // a move up or down among siblings
	Deleted   bool      `json:"deleted,omitempty"`   // a delete
	Reason    string    `json:"reason"`
	At        time.Time `json:"at"`
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

// SubtreeFingerprint identifies h's text with all its descendants', for
// telling whether any of it has changed since a delete was queued.
func SubtreeFingerprint(h *org.Headline) string {
	sum := sha256.Sum256([]byte(org.RenderHeadline(h)))
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
		if found != nil || h.Title != op.Title() {
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
	if op.Insert != nil {
		return applyInsert(p, data, f, op)
	}
	h, err := findEntry(f, op)
	if err != nil {
		return err
	}
	if h.Keyword != op.From {
		return ErrEntryChanged
	}
	if op.Move != nil && op.Move.ToFile != "" && op.Move.ToFile != op.File {
		return applyFileMove(dir, p, data, h, op)
	}
	if op.Move != nil {
		return applyMove(p, data, f, h, op)
	}
	if op.Delete != nil {
		if SubtreeFingerprint(h) != op.Delete.Subtree {
			return ErrEntryChanged
		}
		lines := splitLines(data)
		end := subtreeEnd(h)
		if end > len(lines) || h.SrcStart >= end {
			return fmt.Errorf("gitsync: %s: entry %q out of range", op.File, op.Title())
		}
		out := append(append([]string{}, lines[:h.SrcStart]...), lines[end:]...)
		text := strings.Join(out, "\n")
		if len(out) > 0 && len(data) > 0 && data[len(data)-1] == '\n' {
			text += "\n"
		}
		return writeFileAtomic(p, []byte(text))
	}
	lines := splitLines(data)
	if h.SrcEnd > len(lines) || h.SrcStart >= h.SrcEnd {
		return fmt.Errorf("gitsync: %s: entry %q out of range", op.File, op.Title())
	}
	own, err := patchEntry(lines[h.SrcStart:h.SrcEnd], h, op)
	if err != nil {
		return err
	}
	out := append(append(append([]string{}, lines[:h.SrcStart]...), own...), lines[h.SrcEnd:]...)
	text := strings.Join(out, "\n")
	if len(data) > 0 && data[len(data)-1] == '\n' {
		text += "\n"
	}
	return writeFileAtomic(p, []byte(text))
}

// findParent finds an insert op's parent (or, for Beside, its sibling):
// the first entry with the op's title path whose fingerprint still
// matches. A top-level insert has no parent (nil).
func findParent(f *org.File, op capturequeue.StateOp) (*org.Headline, error) {
	if len(op.Path) == 0 {
		return nil, nil
	}
	var found *org.Headline
	want := strings.Join(op.Path, "\x00")
	org.Walk(f.Headlines, func(h *org.Headline) {
		if found == nil && strings.Join(TitlePath(h), "\x00") == want && Fingerprint(h) == op.Fingerprint {
			found = h
		}
	})
	if found == nil {
		return nil, ErrEntryChanged
	}
	return found, nil
}

// applyInsert adds an insert op's new entry to the raw file data (parsed
// as f, at path p): first or last among its parent's children, or among
// the file's top-level entries, or right before or after (the subtree
// of) a sibling, without touching any other line. If the
// line just before the new entry is blank, as between entries spaced
// out with blank lines, the entry gets a blank line after it too, so the
// spacing stays even.
func applyInsert(p string, data []byte, f *org.File, op capturequeue.StateOp) error {
	anchor, err := findParent(f, op)
	if err != nil {
		return err
	}
	lines := splitLines(data)
	if op.Insert.Beside && anchor == nil {
		return ErrEntryChanged
	}
	level, siblings := 1, f.Headlines
	if anchor != nil {
		level, siblings = anchor.Level+1, anchor.Children
	}
	parent := anchor
	var at int
	switch {
	case op.Insert.Beside && op.Insert.Last:
		level, at = anchor.Level, subtreeEnd(anchor)
	case op.Insert.Beside:
		level, at = anchor.Level, anchor.SrcStart
	case op.Insert.Last && len(siblings) > 0:
		at = subtreeEnd(siblings[len(siblings)-1])
	case op.Insert.Last || len(siblings) == 0:
		at = len(lines)
		if parent != nil {
			at = parent.SrcEnd
		}
	default:
		at = siblings[0].SrcStart
	}
	if at > len(lines) {
		return fmt.Errorf("gitsync: %s: entry %q out of range", op.File, op.NewTitle())
	}
	text, err := capture.RenderAt(op.Insert.Entry, level)
	if err != nil {
		return err
	}
	entry := splitLines([]byte(text))
	if at > 0 && at < len(lines) && strings.TrimSpace(lines[at-1]) == "" {
		entry = append(entry, "")
	}
	out := append(append(append([]string{}, lines[:at]...), entry...), lines[at:]...)
	return writeFileAtomic(p, []byte(strings.Join(out, "\n")+"\n"))
}

// subtreeEnd is the line just after h's last descendant (or h itself).
func subtreeEnd(h *org.Headline) int {
	for len(h.Children) > 0 {
		h = h.Children[len(h.Children)-1]
	}
	return h.SrcEnd
}

// applyMove moves h (found for op in the raw file data, parsed as f, at
// path p) one level, as desktop ">>"/"<<" do, changing only the stars of
// its subtree's headline lines. A demote makes h the last child of its
// previous sibling, which it already follows in the file, so nothing
// moves. A promote makes h the next sibling of its parent, so h's
// subtree moves to after the parent's, past any later siblings, which
// stay where they are. Indentation of planning, drawer and body lines
// is left as written: org reads it either way.
func applyMove(p string, data []byte, f *org.File, h *org.Headline, op capturequeue.StateOp) error {
	lines := splitLines(data)
	start, end := h.SrcStart, subtreeEnd(h)
	if end > len(lines) || start >= end {
		return fmt.Errorf("gitsync: %s: entry %q out of range", op.File, op.Title())
	}
	var heads []int
	org.Walk([]*org.Headline{h}, func(x *org.Headline) { heads = append(heads, x.SrcStart) })
	var out []string
	switch op.Move.Delta {
	case 1:
		siblings := f.Headlines
		if h.Parent != nil {
			siblings = h.Parent.Children
		}
		i := org.Siblings{File: f, Parent: h.Parent}.IndexOf(h)
		to := op.Move.To
		if i <= 0 || len(to) < 2 || siblings[i-1].Title != to[len(to)-2] {
			return ErrEntryChanged
		}
		out = append([]string{}, lines...)
		for _, n := range heads {
			out[n] = "*" + out[n]
		}
	case -1:
		if h.Parent == nil {
			return ErrEntryChanged
		}
		block := append([]string{}, lines[start:end]...)
		for _, n := range heads {
			block[n-start] = strings.TrimPrefix(block[n-start], "*")
		}
		after := subtreeEnd(h.Parent)
		out = append(append([]string{}, lines[:start]...), lines[end:after]...)
		out = append(append(out, block...), lines[after:]...)
	case 0:
		siblings := f.Headlines
		if h.Parent != nil {
			siblings = h.Parent.Children
		}
		var anchor *org.Headline
		for _, s := range siblings {
			if s != h && s.Title == op.Move.Anchor && Fingerprint(s) == op.Move.AnchorFingerprint {
				anchor = s
				break
			}
		}
		if anchor == nil {
			return ErrEntryChanged
		}
		at := anchor.SrcStart
		if op.Move.After {
			at = subtreeEnd(anchor)
		}
		out = reorder(lines, start, end, at)
	default:
		return fmt.Errorf("gitsync: bad move %d", op.Move.Delta)
	}
	text := strings.Join(out, "\n")
	if len(data) > 0 && data[len(data)-1] == '\n' {
		text += "\n"
	}
	return writeFileAtomic(p, []byte(text))
}

// reorder moves lines[start:end] (a subtree) to before line at (outside
// it), keeping blank-line spacing even (see cutSubtree and pasteSubtree).
func reorder(lines []string, start, end, at int) []string {
	rest, body, gap, cut := cutSubtree(lines, start, end)
	if at >= end {
		at -= end - cut
	} else if at > cut {
		at = cut
	}
	return pasteSubtree(rest, at, body, gap)
}

// cutSubtree takes lines[start:end] (a subtree) out of lines, with the
// blank lines that separate it from its neighbours: those after it, or,
// for one with none after it (the file's last), those before it. It
// returns the remaining lines, the subtree without trailing blank lines,
// how many blank lines separated it, and where the cut began.
func cutSubtree(lines []string, start, end int) (rest, body []string, gap, cut int) {
	blank := func(s string) bool { return strings.TrimSpace(s) == "" }
	body = lines[start:end]
	for gap < len(body) && blank(body[len(body)-1-gap]) {
		gap++
	}
	body = body[:len(body)-gap]
	cut = start
	if gap == 0 {
		for cut > 0 && blank(lines[cut-1]) {
			cut--
		}
		gap = start - cut
	}
	rest = append(append([]string{}, lines[:cut]...), lines[end:]...)
	return rest, body, gap, cut
}

// pasteSubtree puts body, a subtree cut by cutSubtree, into lines before
// line at, followed by gap blank lines — or, at the very end, preceded by
// them (unless lines is empty), so blank-line spacing stays even.
func pasteSubtree(lines []string, at int, body []string, gap int) []string {
	sep := make([]string, gap)
	var moved []string
	switch {
	case at < len(lines):
		moved = append(append([]string{}, body...), sep...)
	case len(lines) == 0:
		moved = body
	default:
		moved = append(sep, body...)
	}
	return append(append(append([]string{}, lines[:at]...), moved...), lines[at:]...)
}

// applyFileMove moves h, a top-level entry (found for op in the raw file
// data at path p, parsed as f), with its subtree to the top level of
// another file, op.Move.ToFile (under dir): right before or after the
// subtree of op.Move.Anchor there, or, with no anchor, at its end. Only
// the subtree's lines move: org levels are the same at the top of any
// file. Both files are written.
func applyFileMove(dir, p string, data []byte, h *org.Headline, op capturequeue.StateOp) error {
	if h.Parent != nil {
		return ErrEntryChanged
	}
	dp := filepath.Join(dir, filepath.FromSlash(op.Move.ToFile))
	ddata, err := os.ReadFile(dp)
	if errors.Is(err, os.ErrNotExist) {
		return ErrEntryChanged
	}
	if err != nil {
		return err
	}
	df, err := org.Parse(bytes.NewReader(ddata), dp)
	if err != nil {
		return err
	}
	dlines := splitLines(ddata)
	at := len(dlines)
	if op.Move.Anchor != "" {
		var anchor *org.Headline
		for _, s := range df.Headlines {
			if s.Title == op.Move.Anchor && Fingerprint(s) == op.Move.AnchorFingerprint {
				anchor = s
				break
			}
		}
		if anchor == nil {
			return ErrEntryChanged
		}
		at = anchor.SrcStart
		if op.Move.After {
			at = subtreeEnd(anchor)
		}
	}
	lines := splitLines(data)
	start, end := h.SrcStart, subtreeEnd(h)
	if end > len(lines) || start >= end || at > len(dlines) {
		return fmt.Errorf("gitsync: %s: entry %q out of range", op.File, op.Title())
	}
	rest, body, gap, _ := cutSubtree(lines, start, end)
	out := pasteSubtree(dlines, at, body, gap)
	text := strings.Join(rest, "\n")
	if len(rest) > 0 && len(data) > 0 && data[len(data)-1] == '\n' {
		text += "\n"
	}
	if err := writeFileAtomic(p, []byte(text)); err != nil {
		return err
	}
	return writeFileAtomic(dp, []byte(strings.Join(out, "\n")+"\n"))
}

// patchEntry returns h's own lines (as in the file) with op applied,
// rewriting only the lines that change: the headline line (keyword,
// title, tags), the planning line (added or removed as needed), the
// LAST_REPEAT property when completing a repeating item, and the body
// when an edit changes it. Every other line — the property drawer above
// all — is kept byte for byte.
func patchEntry(own []string, h *org.Headline, op capturequeue.StateOp) ([]string, error) {
	hadPlanning := h.Scheduled != nil || h.Deadline != nil || h.Closed != nil
	oldPlanning := org.PlanningLine(h)
	oldHead, _, _ := strings.Cut(org.RenderEntry(h), "\n")
	indent := strings.Repeat(" ", h.Level+1)
	bodyIndent := commonIndent(h.TrimmedBody(), indent)

	sc := orgdate.ChangeState(h, op.To, op.CreatedAt)
	sc.Apply(h)

	var newBody []string
	bodyChanged := false
	if e := op.Edit; e != nil {
		if e.Title != nil {
			h.Title = strings.Join(strings.Fields(*e.Title), " ")
		}
		if e.Tags != nil {
			h.Tags = cleanTags(*e.Tags)
		}
		var err error
		if e.Scheduled != nil {
			if h.Scheduled, err = reschedule(h.Scheduled, *e.Scheduled); err != nil {
				return nil, err
			}
		}
		if e.Deadline != nil {
			if h.Deadline, err = reschedule(h.Deadline, *e.Deadline); err != nil {
				return nil, err
			}
		}
		if e.Body != nil {
			bodyChanged = true
			newBody = indentBody(*e.Body, bodyIndent)
		}
	}

	out := []string{own[0]}
	if head, _, _ := strings.Cut(org.RenderEntry(h), "\n"); head != oldHead {
		out[0] = head
	}
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
	if bodyChanged {
		rest = replaceBody(rest, newBody)
	}
	return append(out, rest...), nil
}

// cookieRe matches a timestamp's repeater ("+1w", "++1w", ".+1w") or
// warning ("-3d", "--3d") cookie.
var cookieRe = regexp.MustCompile(`(?:\.\+|\+\+|\+|--|-)\d+[hdwmy]`)

// reschedule is a SCHEDULED/DEADLINE set to value ("YYYY-MM-DD",
// optionally " HH:MM"; "" removes it), keeping old's repeater and warning
// cookies, as org-mode's rescheduling does.
func reschedule(old *org.Timestamp, value string) (*org.Timestamp, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	t, hasTime, err := orgdate.ParseFlexible(value)
	if err != nil {
		return nil, fmt.Errorf("gitsync: %w", err)
	}
	layout := "2006-01-02 Mon"
	if hasTime {
		layout = "2006-01-02 Mon 15:04"
	}
	raw := t.Format(layout)
	if old != nil {
		for _, c := range cookieRe.FindAllString(old.Raw, -1) {
			raw += " " + c
		}
	}
	return &org.Timestamp{Active: true, Raw: raw}, nil
}

// cleanTags makes tags valid org tags, dropping blanks and duplicates.
func cleanTags(tags []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, t := range tags {
		t = org.SanitizeTag(strings.Trim(strings.TrimSpace(t), ":"))
		if t != "" && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// commonIndent is the leading whitespace every non-blank line of lines
// shares, or fallback if there are none.
func commonIndent(lines []string, fallback string) string {
	common := ""
	first := true
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		lead := l[:len(l)-len(strings.TrimLeft(l, " \t"))]
		if first {
			common, first = lead, false
			continue
		}
		for !strings.HasPrefix(lead, common) {
			common = common[:len(common)-1]
		}
	}
	if first {
		return fallback
	}
	return common
}

// indentBody turns edited notes (dedented text) back into body lines.
func indentBody(text, indent string) []string {
	text = strings.TrimRight(strings.ReplaceAll(text, "\r\n", "\n"), " \t\n")
	if strings.TrimSpace(text) == "" {
		return nil
	}
	var out []string
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimRight(l, " \t")
		if l == "" {
			out = append(out, "")
			continue
		}
		out = append(out, indent+l)
	}
	return out
}

// replaceBody swaps the body lines among rest (an entry's own lines after
// its planning) for body: the property drawer, if any, is kept as is,
// followed by the new body, then the blank lines the entry ended with.
func replaceBody(rest, body []string) []string {
	var drawer, other []string
	in := false
	for _, l := range rest {
		t := strings.TrimSpace(l)
		switch {
		case !in && t == ":PROPERTIES:" && drawer == nil:
			in = true
			drawer = append(drawer, l)
		case in:
			drawer = append(drawer, l)
			if t == ":END:" {
				in = false
			}
		default:
			other = append(other, l)
		}
	}
	trailing := 0
	for i := len(other) - 1; i >= 0 && strings.TrimSpace(other[i]) == ""; i-- {
		trailing++
	}
	out := append(append([]string{}, drawer...), body...)
	for range trailing {
		out = append(out, "")
	}
	return out
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

// NewDeleteOp describes deleting h, with its subtree, from file at the
// time at.
func NewDeleteOp(file string, h *org.Headline, at time.Time) capturequeue.StateOp {
	op := NewStateOp(file, h, h.Keyword, at)
	op.Delete = &capturequeue.EntryDelete{Subtree: SubtreeFingerprint(h)}
	return op
}

// NewInsertBesideOp describes adding c to file right before sibling or,
// with after, right after sibling's subtree, at sibling's level.
func NewInsertBesideOp(file string, sibling *org.Headline, after bool, c capture.Capture) capturequeue.StateOp {
	op := NewInsertOp(file, sibling, after, c)
	op.Insert.Beside = true
	return op
}

// NewInsertOp describes adding c to file (relative to the repository
// root, as currently checked out): first or, with last, last among
// parent's children, or among the file's top-level entries when parent
// is nil.
func NewInsertOp(file string, parent *org.Headline, last bool, c capture.Capture) capturequeue.StateOp {
	op := capturequeue.StateOp{
		CreatedAt: c.CreatedAt,
		File:      file,
		Path:      []string{},
		To:        c.Keyword,
		Insert:    &capturequeue.EntryInsert{Last: last, Entry: c},
	}
	if parent != nil {
		op.Path = TitlePath(parent)
		op.Fingerprint = Fingerprint(parent)
	}
	return op
}
