// Package merge3 is a line-based three-way merge (diff3): given a common
// base and two edited versions of a text, it combines the edits from
// both sides, and marks the places where they collide with the
// standard <<<<<<< ======= >>>>>>> conflict markers. Pure functions: no
// git, no files, no UI. See MULTI_WRITER.md §5 (or DESIGN.md, once
// that moves there).
package merge3

import (
	"bytes"
	"slices"
)

// Labels name the two sides in conflict markers: "<<<<<<< Ours" and
// ">>>>>>> Theirs". Either may be empty.
type Labels struct {
	Ours, Theirs string
}

// Merge combines the changes base→ours and base→theirs, line by line.
// Changes to separate regions of base merge cleanly; a change both
// sides made identically is taken once; anything else that overlaps is
// a conflict, written out as
//
//	<<<<<<< ours label
//	ours' lines
//	=======
//	theirs' lines
//	>>>>>>> theirs label
//
// conflicts counts such blocks; merged is the whole result either way.
// Lines keep their own endings, so CRLF files and a missing final
// newline survive; a conflict side not ending in a newline gets one
// before the next marker, so the markers always start a line.
func Merge(base, ours, theirs []byte, labels Labels) (merged []byte, conflicts int) {
	switch {
	case bytes.Equal(ours, theirs), bytes.Equal(theirs, base):
		return clone(ours), 0
	case bytes.Equal(ours, base):
		return clone(theirs), 0
	}

	b, o, t := SplitLines(base), SplitLines(ours), SplitLines(theirs)
	hunks := append(diffHunks(b, o, sideOurs), diffHunks(b, t, sideTheirs)...)
	slices.SortStableFunc(hunks, func(x, y hunk) int {
		if x.baseStart != y.baseStart {
			return x.baseStart - y.baseStart
		}
		return x.baseEnd - y.baseEnd
	})

	var out [][]byte
	pos := 0 // next base line not yet copied to out
	for i := 0; i < len(hunks); {
		// A group is a run of hunks that overlap one another, from either
		// side; each group is merged on its own.
		group := []hunk{hunks[i]}
		start, end := hunks[i].baseStart, hunks[i].baseEnd
		for i++; i < len(hunks) && overlaps(start, end, hunks[i]); i++ {
			group = append(group, hunks[i])
			end = max(end, hunks[i].baseEnd)
		}
		out = append(out, b[pos:start]...)
		pos = end

		oursLines := applySide(b, start, end, group, sideOurs, o)
		theirsLines := applySide(b, start, end, group, sideTheirs, t)
		switch {
		case !hasSide(group, sideTheirs):
			out = append(out, oursLines...)
		case !hasSide(group, sideOurs):
			out = append(out, theirsLines...)
		case linesEqual(oursLines, theirsLines):
			out = append(out, oursLines...)
		default:
			conflicts++
			out = append(out, marker("<<<<<<<", labels.Ours))
			out = appendTerminated(out, oursLines)
			out = append(out, []byte("=======\n"))
			out = appendTerminated(out, theirsLines)
			out = append(out, marker(">>>>>>>", labels.Theirs))
		}
	}
	out = append(out, b[pos:]...)
	return bytes.Join(out, nil), conflicts
}

// HasMarkers reports whether text still contains a conflict block: a
// line starting "<<<<<<<", later a "=======" line, and later a line
// starting ">>>>>>>" — how a resolved file is told from one with
// conflicts left in it.
func HasMarkers(text []byte) bool {
	state := 0
	for _, line := range SplitLines(text) {
		line = bytes.TrimRight(line, "\r\n")
		switch {
		case state == 0 && bytes.HasPrefix(line, []byte("<<<<<<<")):
			state = 1
		case state == 1 && bytes.Equal(line, []byte("=======")):
			state = 2
		case state == 2 && bytes.HasPrefix(line, []byte(">>>>>>>")):
			return true
		}
	}
	return false
}

type side int

const (
	sideOurs side = iota
	sideTheirs
)

// hunk replaces base[baseStart:baseEnd] with the side's
// lines[start:end]. An empty base range is an insertion before
// base[baseStart].
type hunk struct {
	side               side
	baseStart, baseEnd int
	start, end         int
}

// overlaps reports whether h collides with a group covering base
// [start, end). Two ranges collide if they share a base line, or if
// they're both insertions at the same place, or if one is an insertion
// strictly inside the other's range. An insertion right before or after
// a changed range doesn't collide with it: its place is unambiguous.
func overlaps(start, end int, h hunk) bool {
	if h.baseStart < end && start < h.baseEnd {
		return true
	}
	if start == end && h.baseStart == h.baseEnd {
		return start == h.baseStart
	}
	if start == end {
		return h.baseStart < start && start < h.baseEnd
	}
	if h.baseStart == h.baseEnd {
		return start < h.baseStart && h.baseStart < end
	}
	return false
}

func hasSide(group []hunk, s side) bool {
	return slices.ContainsFunc(group, func(h hunk) bool { return h.side == s })
}

// applySide returns what base[start:end] becomes on side s: base's lines
// with that side's hunks in the group applied.
func applySide(base [][]byte, start, end int, group []hunk, s side, lines [][]byte) [][]byte {
	var out [][]byte
	pos := start
	for _, h := range group {
		if h.side != s {
			continue
		}
		out = append(out, base[pos:h.baseStart]...)
		out = append(out, lines[h.start:h.end]...)
		pos = h.baseEnd
	}
	return append(out, base[pos:end]...)
}

func linesEqual(a, b [][]byte) bool {
	return slices.EqualFunc(a, b, bytes.Equal)
}

func marker(prefix, label string) []byte {
	if label == "" {
		return []byte(prefix + "\n")
	}
	return []byte(prefix + " " + label + "\n")
}

// appendTerminated appends lines to out, adding a newline to the last
// one if it has none, so whatever follows starts a line of its own.
func appendTerminated(out, lines [][]byte) [][]byte {
	out = append(out, lines...)
	if n := len(out); len(lines) > 0 && !bytes.HasSuffix(out[n-1], []byte("\n")) {
		out[n-1] = append(clone(out[n-1]), '\n')
	}
	return out
}

func clone(b []byte) []byte {
	return append([]byte(nil), b...)
}

// SplitLines splits text into lines, each keeping its own line ending;
// the last one has none if text doesn't end in a newline.
func SplitLines(text []byte) [][]byte {
	var lines [][]byte
	for len(text) > 0 {
		i := bytes.IndexByte(text, '\n')
		if i < 0 {
			lines = append(lines, text)
			break
		}
		lines = append(lines, text[:i+1])
		text = text[i+1:]
	}
	return lines
}
