// Package org implements a minimal parser for the subset of the org-mode
// file format orgtd cares about: headlines, TODO keywords, tags, planning
// lines (SCHEDULED/DEADLINE/CLOSED), property drawers, and body text.
//
// Parse builds an in-memory tree; RenderFile/RenderHeadline/RenderEntry
// serialize it (saving is internal/gitstore's job). RenderFile reproduces
// the preamble, body text and every unedited entry byte-for-byte, and
// regenerates an edited entry's headline, planning and property lines
// from its parsed fields. See DESIGN.md §3.
package org

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

// Keywords is the set of TODO keywords a parse recognizes, each either
// active (unfinished work, like TODO) or done (finished, like DONE). The
// zero value recognizes none; see DefaultKeywords for the built-in set.
// A Keywords is never modified after it's built, so it's safe to share.
type Keywords struct {
	done map[string]bool // every keyword, mapped to whether it's a done state
}

// NewKeywords returns the keyword set made of active and done. A name in
// both counts as done.
func NewKeywords(active, done []string) Keywords {
	k := Keywords{done: make(map[string]bool, len(active)+len(done))}
	for _, name := range active {
		k.done[name] = false
	}
	for _, name := range done {
		k.done[name] = true
	}
	return k
}

// DefaultKeywords returns the built-in keyword set: TODO, NEXT, WAITING
// and SOMEDAY (active), DONE and CANCELLED (done). It matches
// config.DefaultKeywords.
func DefaultKeywords() Keywords {
	return NewKeywords([]string{"TODO", "NEXT", "WAITING", "SOMEDAY"}, []string{"DONE", "CANCELLED"})
}

// IsKeyword reports whether name is one of k's keywords.
func (k Keywords) IsKeyword(name string) bool {
	_, ok := k.done[name]
	return ok
}

// IsDone reports whether name is one of k's done keywords.
func (k Keywords) IsDone(name string) bool {
	return k.done[name]
}

// Timestamp is a parsed org timestamp, e.g. "<2026-09-10 Thu>" or
// "[2026-09-06 Sun 14:32]". Only the raw text inside the brackets is kept;
// callers that need a date parse Raw themselves (see parseTimestampDate
// in internal/ui/agenda.go).
type Timestamp struct {
	Active bool   // true for <...>, false for [...]
	Raw    string // text between the brackets, e.g. "2026-09-10 Thu"
}

func (t *Timestamp) String() string {
	if t == nil {
		return ""
	}
	if t.Active {
		return "<" + t.Raw + ">"
	}
	return "[" + t.Raw + "]"
}

// Headline is one node in an org outline tree.
type Headline struct {
	Level    int
	Keyword  string // "", or one of the Keywords the file was parsed with
	Priority string // e.g. "A", "" if none
	Title    string // headline text, keyword/priority/tags stripped
	Tags     []string

	Scheduled *Timestamp
	Deadline  *Timestamp
	Closed    *Timestamp

	Properties    map[string]string
	PropertyOrder []string // insertion order, for stable display

	Body []string // raw body lines, excluding planning line and drawer

	Parent   *Headline
	Children []*Headline

	// SrcStart and SrcEnd are the 0-based line range [SrcStart, SrcEnd)
	// of h's own text (its headline line, planning, drawer and body —
	// not its children) in the file it was parsed from, so a caller can
	// rewrite just that entry in the raw file (see the iOS core's state
	// changes). They describe the text as parsed and are not maintained
	// by later edits.
	SrcStart, SrcEnd int

	// src is h's own text exactly as parsed (lines SrcStart:SrcEnd), and
	// srcFields what writeHeadlineFields rendered for it at the time.
	// While h still renders to srcFields nothing has edited it, so
	// RenderFile writes src instead, keeping whatever regenerating it
	// would lose: text after a planning timestamp, timestamp ranges,
	// duplicate properties, a drawer after body text, spacing.
	src, srcFields string
}

// SetProperty sets key to value in h's property drawer, appending key to
// PropertyOrder (in first-seen order, matching how Parse itself builds
// it) if it isn't already present.
func (h *Headline) SetProperty(key, value string) {
	if h.Properties == nil {
		h.Properties = make(map[string]string)
	}
	if _, exists := h.Properties[key]; !exists {
		h.PropertyOrder = append(h.PropertyOrder, key)
	}
	h.Properties[key] = value
}

// DeleteProperty removes key from h's property drawer, if present.
func (h *Headline) DeleteProperty(key string) {
	if _, exists := h.Properties[key]; !exists {
		return
	}
	delete(h.Properties, key)
	for i, k := range h.PropertyOrder {
		if k == key {
			h.PropertyOrder = append(h.PropertyOrder[:i], h.PropertyOrder[i+1:]...)
			break
		}
	}
}

// SetOrDeleteProperty sets h's key property to value, or removes it
// entirely if value is empty (an empty property is meaningless clutter —
// e.g. GCAL_RECURRING_EVENT_IDS/LINKS once every attached meeting has been
// detached — so it's deleted rather than left as "").
func (h *Headline) SetOrDeleteProperty(key, value string) {
	if value == "" {
		h.DeleteProperty(key)
	} else {
		h.SetProperty(key, value)
	}
}

// RestoreProperty reverts h's key property to value if had is true (the
// property existed before the change being reverted), or removes it if had
// is false (the change introduced the property from nothing).
func (h *Headline) RestoreProperty(key string, had bool, value string) {
	if had {
		h.SetProperty(key, value)
	} else {
		h.DeleteProperty(key)
	}
}

// TrimmedBody returns h.Body with any trailing blank lines stripped. Org
// files conventionally have a blank line separating a headline from the
// next one, which the parser has no way to distinguish from deliberate
// trailing whitespace in the body — without this, that separator would
// show up as a meaningless empty line under nearly every single entry.
// Deliberate blank lines *within* a multi-paragraph body (not at the very
// end) are left alone.
func (h *Headline) TrimmedBody() []string {
	lines := h.Body
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return lines[:end]
}

// File is a parsed org file.
type File struct {
	Path      string
	Base      []byte   // the file's content as orgtd last loaded or saved it: the base of a save's three-way merge; nil if never (see ParseFile)
	New       bool     // created in memory (":edit" of a missing file) and not yet saved, so it has no Base
	Preamble  []string // lines before the first headline (e.g. #+TITLE:)
	Headlines []*Headline
}

var (
	headlineRe = regexp.MustCompile(`^(\*+)\s+(.*)$`)
	tagsRe     = regexp.MustCompile(`\s+(:[[:alnum:]_@%#+:]+:)\s*$`)
	priorityRe = regexp.MustCompile(`^\[#([A-Z])\]\s*`)
	drawerKV   = regexp.MustCompile(`^\s*:([^\s:]+):\s*(.*)$`)
	tsRe       = regexp.MustCompile(`([<\[])([^>\]]+)([>\]])`)
	// No leading ^ anchor: applyPlanningLine matches this against the
	// whole (already-identified) planning line via FindAllStringSubmatch
	// to pick up every keyword present, e.g. "SCHEDULED: <...> DEADLINE:
	// <...>" on one line, org-mode's own convention for a line with more
	// than one of them.
	planningRe = regexp.MustCompile(`(SCHEDULED|DEADLINE|CLOSED):\s*([<\[][^>\]]+[>\]])`)
)

// ParseFile reads and parses the org file at path, recognizing kw's
// keywords.
func ParseFile(path string, kw Keywords) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	f, err := Parse(bytes.NewReader(data), path, kw)
	if err != nil {
		return nil, err
	}
	f.Base = nonNil(data)
	return f, nil
}

// ParseContent parses data, the content of the org file at path, and
// records it as the file's Base, as ParseFile does for a file read from
// disk.
func ParseContent(data []byte, path string, kw Keywords) (*File, error) {
	f, err := Parse(bytes.NewReader(data), path, kw)
	if err != nil {
		return nil, err
	}
	f.Base = nonNil(data)
	return f, nil
}

func nonNil(b []byte) []byte {
	if b == nil {
		return []byte{}
	}
	return b
}

// Parse reads an org file from r. path is stored on the result and used
// only for display/identification. A headline's first word is its
// Keyword only if it's one of kw's.
func Parse(r io.Reader, path string, kw Keywords) (*File, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	file := &File{Path: path}
	var stack []*Headline // stack[i] is the current open headline at level i+1

	// currentBody accumulates raw body lines for whichever headline is
	// currently open (nil means "file preamble").
	appendBody := func(line string) {
		if len(stack) == 0 {
			file.Preamble = append(file.Preamble, line)
			return
		}
		cur := stack[len(stack)-1]
		cur.Body = append(cur.Body, line)
	}

	// State for the small per-headline sub-parser: right after a headline
	// line, we may see a planning line, then a properties drawer, before
	// body text begins.
	var (
		awaitingPlanning bool
		inDrawer         bool
		lineNo           = -1
		last             *Headline // the most recent headline, whose own text runs until the next one
		lines            []string
	)

	for scanner.Scan() {
		line := scanner.Text()
		lineNo++
		lines = append(lines, line)

		if m := headlineRe.FindStringSubmatch(line); m != nil {
			level := len(m[1])
			h := parseHeadlineLine(m[2], kw)
			h.Level = level
			h.SrcStart = lineNo
			if last != nil {
				last.SrcEnd = lineNo
			}
			last = h

			// Pop the stack back to the parent of this new headline.
			for len(stack) >= level {
				stack = stack[:len(stack)-1]
			}
			if len(stack) == 0 {
				file.Headlines = append(file.Headlines, h)
			} else {
				parent := stack[len(stack)-1]
				h.Parent = parent
				parent.Children = append(parent.Children, h)
			}
			stack = append(stack, h)

			awaitingPlanning = true
			inDrawer = false
			continue
		}

		if len(stack) == 0 {
			// No headline seen yet: this is file preamble.
			appendBody(line)
			continue
		}

		cur := stack[len(stack)-1]

		if awaitingPlanning {
			awaitingPlanning = false
			if applyPlanningLine(cur, line) {
				continue
			}
			// Not a planning line; fall through to normal handling below.
		}

		trimmed := strings.TrimSpace(line)
		if inDrawer {
			if trimmed == ":END:" {
				inDrawer = false
				continue
			}
			if m := drawerKV.FindStringSubmatch(line); m != nil {
				key, val := m[1], m[2]
				if _, exists := cur.Properties[key]; !exists {
					cur.PropertyOrder = append(cur.PropertyOrder, key)
				}
				if cur.Properties == nil {
					cur.Properties = make(map[string]string)
				}
				cur.Properties[key] = val
				continue
			}
			// Malformed drawer content: keep as body rather than dropping it.
			appendBody(line)
			continue
		}
		if trimmed == ":PROPERTIES:" {
			inDrawer = true
			if cur.Properties == nil {
				cur.Properties = make(map[string]string)
			}
			continue
		}

		appendBody(line)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("org: reading %s: %w", path, err)
	}
	if last != nil {
		last.SrcEnd = lineNo + 1
	}
	Walk(file.Headlines, func(h *Headline) {
		var b strings.Builder
		for _, line := range lines[h.SrcStart:h.SrcEnd] {
			b.WriteString(line)
			b.WriteByte('\n')
		}
		h.src = b.String()
		b.Reset()
		writeHeadlineFields(&b, h)
		h.srcFields = b.String()
	})

	return file, nil
}

// parseHeadlineLine parses everything after the leading stars: optional
// TODO keyword, optional priority cookie, title, optional trailing tags.
func parseHeadlineLine(rest string, kw Keywords) *Headline {
	h := &Headline{}

	rest = strings.TrimSpace(rest)

	if m := tagsRe.FindStringSubmatch(rest); m != nil {
		// Empty tags (":a::b:") are skipped, as org-mode does. A group
		// of nothing but colons (":::") stays in the title: there's no
		// tag to write back in its place, and without it a tag-shaped
		// word before it would become a tag.
		for _, tag := range strings.Split(m[1], ":") {
			if tag != "" {
				h.Tags = append(h.Tags, tag)
			}
		}
		if len(h.Tags) > 0 {
			rest = rest[:len(rest)-len(m[0])]
		}
	}

	fields := strings.SplitN(rest, " ", 2)
	if len(fields) > 0 && kw.IsKeyword(fields[0]) {
		h.Keyword = fields[0]
		if len(fields) > 1 {
			rest = strings.TrimSpace(fields[1])
		} else {
			rest = ""
		}
	}

	if m := priorityRe.FindStringSubmatch(rest); m != nil {
		h.Priority = m[1]
		rest = rest[len(m[0]):]
	}

	h.Title = strings.TrimSpace(rest)
	return h
}

// applyPlanningLine tries to interpret line as an org planning line
// (one or more of SCHEDULED/DEADLINE/CLOSED timestamps on one line). It
// returns false if the line doesn't look like a planning line at all —
// i.e. doesn't *start* with one of those keywords — in which case the
// caller should treat it as ordinary content instead. That check is
// separate from planningRe (which is deliberately unanchored, so it can
// find every keyword on a qualifying line) so a body line that merely
// mentions "DEADLINE:" partway through isn't mistaken for planning info.
func applyPlanningLine(h *Headline, line string) bool {
	trimmed := strings.TrimLeft(line, " \t")
	if !strings.HasPrefix(trimmed, "SCHEDULED:") &&
		!strings.HasPrefix(trimmed, "DEADLINE:") &&
		!strings.HasPrefix(trimmed, "CLOSED:") {
		return false
	}
	matches := planningRe.FindAllStringSubmatch(line, -1)
	if len(matches) == 0 {
		return false
	}
	for _, m := range matches {
		ts := parseTimestamp(m[2])
		switch m[1] {
		case "SCHEDULED":
			h.Scheduled = ts
		case "DEADLINE":
			h.Deadline = ts
		case "CLOSED":
			h.Closed = ts
		}
	}
	return true
}

func parseTimestamp(bracketed string) *Timestamp {
	m := tsRe.FindStringSubmatch(bracketed)
	if m == nil {
		return nil
	}
	return &Timestamp{Active: m[1] == "<", Raw: m[2]}
}

// SanitizeTag replaces any character of s that org's own tag syntax
// doesn't allow (see tagsRe — letters, digits, and "_@%#+" only) with
// "_", so the result still parses back out as a real tag, e.g. an email's
// local part with its "."s and "-"s.
func SanitizeTag(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '_' || r == '@' || r == '%' || r == '#' || r == '+':
			return r
		}
		return '_'
	}, s)
}

// Walk calls fn for h and every descendant, depth-first, pre-order.
func Walk(headlines []*Headline, fn func(*Headline)) {
	for _, h := range headlines {
		fn(h)
		Walk(h.Children, fn)
	}
}
