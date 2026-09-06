// Package org implements a minimal parser for the subset of the org-mode
// file format orgtd cares about: headlines, TODO keywords, tags, planning
// lines (SCHEDULED/DEADLINE/CLOSED), property drawers, and body text.
//
// This initial version is read-only: it builds a tree in memory for
// rendering and navigation. Writing files back out (format-preserving,
// per DESIGN.md) is not implemented yet.
package org

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

// ActiveKeywords are TODO-class states that represent unfinished work.
var ActiveKeywords = []string{"TODO", "NEXT", "WAITING", "SOMEDAY"}

// DoneKeywords are TODO-class states that represent finished work.
var DoneKeywords = []string{"DONE", "CANCELLED"}

var keywordSet = func() map[string]bool {
	m := make(map[string]bool)
	for _, k := range ActiveKeywords {
		m[k] = true
	}
	for _, k := range DoneKeywords {
		m[k] = true
	}
	return m
}()

// IsDoneKeyword reports whether keyword is one of the "done" states.
func IsDoneKeyword(keyword string) bool {
	for _, k := range DoneKeywords {
		if k == keyword {
			return true
		}
	}
	return false
}

// Timestamp is a parsed org timestamp, e.g. "<2026-09-10 Thu>" or
// "[2026-09-06 Sun 14:32]". Only the raw text inside the brackets is kept
// at this stage; date/time parsing will be added when the agenda view
// needs it.
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
	Keyword  string // "", or one of ActiveKeywords/DoneKeywords
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
}

// File is a parsed org file.
type File struct {
	Path      string
	Preamble  []string // lines before the first headline (e.g. #+TITLE:)
	Headlines []*Headline
}

var (
	headlineRe = regexp.MustCompile(`^(\*+)\s+(.*)$`)
	tagsRe     = regexp.MustCompile(`\s+(:[[:alnum:]_@%#+:]+:)\s*$`)
	priorityRe = regexp.MustCompile(`^\[#([A-Z])\]\s*`)
	drawerKV   = regexp.MustCompile(`^\s*:([A-Za-z0-9_]+):\s*(.*)$`)
	tsRe       = regexp.MustCompile(`([<\[])([^>\]]+)([>\]])`)
	planningRe = regexp.MustCompile(`^\s*(SCHEDULED|DEADLINE|CLOSED):\s*([<\[][^>\]]+[>\]])`)
)

// ParseFile reads and parses the org file at path.
func ParseFile(path string) (*File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Parse(f, path)
}

// Parse reads an org file from r. path is stored on the result and used
// only for display/identification.
func Parse(r io.Reader, path string) (*File, error) {
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
	)

	for scanner.Scan() {
		line := scanner.Text()

		if m := headlineRe.FindStringSubmatch(line); m != nil {
			level := len(m[1])
			h := parseHeadlineLine(m[2])
			h.Level = level

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

	return file, nil
}

// parseHeadlineLine parses everything after the leading stars: optional
// TODO keyword, optional priority cookie, title, optional trailing tags.
func parseHeadlineLine(rest string) *Headline {
	h := &Headline{}

	rest = strings.TrimSpace(rest)

	if m := tagsRe.FindStringSubmatch(rest); m != nil {
		tagStr := strings.Trim(m[1], ":")
		h.Tags = strings.Split(tagStr, ":")
		rest = rest[:len(rest)-len(m[0])]
	}

	fields := strings.SplitN(rest, " ", 2)
	if len(fields) > 0 && keywordSet[fields[0]] {
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
// returns false if the line doesn't look like a planning line at all, in
// which case the caller should treat it as ordinary content.
func applyPlanningLine(h *Headline, line string) bool {
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

// Walk calls fn for h and every descendant, depth-first, pre-order.
func Walk(headlines []*Headline, fn func(*Headline)) {
	for _, h := range headlines {
		fn(h)
		Walk(h.Children, fn)
	}
}
