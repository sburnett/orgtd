package ui

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
)

// Insert mode treats each of an entry's tags — the ":home:errand:" at
// the end of its first line — as a single character. Moving onto them
// (→ from the end of the title, End, or "gt") enters the tag area: the
// tags are lifted out of the first line into insertSession.tags, with
// an empty tag added at the end for typing a new one, and the caret is
// on one tag at a time. Typing adds to the tag under the caret and
// Backspace removes from it; ":" moves on to the next tag, and nothing
// else a tag can't hold can be typed. Leaving the area writes the tags back, minus empty
// and repeated ones (see leaveTags).

// titleTagsRe matches the tags at the end of a headline's text, as
// org.Parse finds them: whitespace, then ":tag:tag:".
var titleTagsRe = regexp.MustCompile(`\s+:([[:alnum:]_@%#+:]+):\s*$`)

// splitTitleTags splits line, an entry's first line, into where its
// title text ends (in runes) and its tags.
func splitTitleTags(line string) (titleEnd int, tags []string) {
	loc := titleTagsRe.FindStringSubmatchIndex(line)
	if loc == nil {
		return utf8.RuneCountInString(line), nil
	}
	for _, t := range strings.Split(line[loc[2]:loc[3]], ":") {
		if t != "" {
			tags = append(tags, t)
		}
	}
	return utf8.RuneCountInString(line[:loc[0]]), tags
}

// insertTitleEnd is where the title text on insert mode's first line
// ends, before its tags — the furthest the caret goes on that line
// outside the tag area.
func (m *Model) insertTitleEnd() int {
	end, _ := splitTitleTags(m.ins.buf.lines[0])
	return end
}

// enterTags moves the caret into the tag area: onto the first tag, or
// with atNew onto the empty one at the end, for typing a new tag.
func (m *Model) enterTags(atNew bool) {
	b := &m.ins.buf
	end, tags := splitTitleTags(b.lines[0])
	b.lines = append([]string{string(b.runes(0)[:end])}, b.lines[1:]...)
	m.ins.tags = append(tags, "")
	m.ins.tagIdx = 0
	if atNew {
		m.ins.tagIdx = len(m.ins.tags) - 1
	}
	m.ins.inTags, m.ins.onKeyword = true, false
	b.row, b.col, b.goal = 0, end, -1
}

// leaveTags writes the tags back onto the end of the first line — empty
// ones dropped, and any that repeat an earlier one — leaving the caret
// at the end of the title text. A no-op outside the tag area.
func (m *Model) leaveTags() {
	if !m.ins.inTags {
		return
	}
	var tags []string
	seen := map[string]bool{}
	for _, t := range m.ins.tags {
		if t != "" && !seen[t] {
			seen[t] = true
			tags = append(tags, t)
		}
	}
	b := &m.ins.buf
	line := b.lines[0]
	if len(tags) > 0 {
		line = strings.TrimRight(line, " ") + "   :" + strings.Join(tags, ":") + ":"
	}
	b.lines = append([]string{line}, b.lines[1:]...)
	m.ins.inTags, m.ins.tags, m.ins.tagIdx = false, nil, 0
	b.row, b.col, b.goal = 0, m.insertTitleEnd(), -1
}

// snapOutOfTags keeps the caret off the tags' own characters outside
// the tag area: anywhere past the title text lands at its end.
func (m *Model) snapOutOfTags() {
	if m.ins.inTags || m.ins.buf.row != 0 {
		return
	}
	if end := m.insertTitleEnd(); m.ins.buf.col > end {
		m.ins.buf.col = end
	}
}

// moveTag moves the caret dir tags along the tag area, dropping the tag
// it leaves if that's been emptied (other than the new one at the end),
// and leaving the area altogether going left from the first.
func (m *Model) moveTag(dir int) {
	i := m.ins.tagIdx
	last := len(m.ins.tags) - 1
	switch {
	case dir < 0 && i == 0:
		m.leaveTags()
		return
	case dir > 0 && i == last:
		return
	}
	if m.ins.tags[i] == "" && i != last {
		m.ins.tags = append(m.ins.tags[:i:i], m.ins.tags[i+1:]...)
		if dir > 0 {
			return // the next tag has moved into place
		}
	}
	m.ins.tagIdx = i + dir
}

// setTag replaces the tag under the caret with t, keeping exactly one
// empty tag at the end: adding one if t filled in the last, or, if t
// emptied the one before it, letting that become the new tag instead
// (":a: :" emptied is ": :", not ": ::").
func (m *Model) setTag(t string) {
	tags := append([]string(nil), m.ins.tags...)
	tags[m.ins.tagIdx] = t
	if tags[len(tags)-1] != "" {
		tags = append(tags, "")
	}
	for n := len(tags); n >= 2 && tags[n-1] == "" && tags[n-2] == ""; n = len(tags) {
		tags = tags[:n-1]
	}
	m.ins.tags = tags
	m.ins.tagIdx = min(m.ins.tagIdx, len(tags)-1)
}

// insertTagsKey handles the keys that act differently because of the
// tags: every key in the tag area, and the ones that would reach into
// the tags from the title. handled is false for anything left to
// ordinary editing.
func (m *Model) insertTagsKey(msg tea.KeyMsg) (handled bool) {
	if !m.ins.inTags {
		return m.titleEndKey(msg)
	}
	cur := m.ins.tags[m.ins.tagIdx]
	switch msg.Type {
	case tea.KeyRunes:
		switch {
		case msg.Alt && !msg.Paste && string(msg.Runes) == "b":
			m.moveTag(-1)
		case msg.Alt && !msg.Paste && string(msg.Runes) == "f":
			m.moveTag(1)
		case msg.Alt && !msg.Paste && string(msg.Runes) == "d":
			m.setTag("")
		case !msg.Alt && !msg.Paste && string(msg.Runes) == ":":
			// The separator between tags: on to the next one, if
			// there is one — so "work:home" types two tags.
			m.moveTag(1)
		case !msg.Alt:
			var add strings.Builder
			for _, r := range msg.Runes {
				if isTagRune(r) { // never ":", nor a space
					add.WriteRune(r)
				}
			}
			if add.Len() > 0 {
				m.setTag(cur + add.String())
			}
		}
	case tea.KeyBackspace, tea.KeyCtrlH:
		switch {
		case cur == "":
			// Nothing left here: on to the tag before, to carry on
			// deleting from it.
			m.moveTag(-1)
		case msg.Alt:
			m.setTag("")
		default:
			r := []rune(cur)
			m.setTag(string(r[:len(r)-1]))
		}
	case tea.KeyCtrlW, tea.KeyCtrlU:
		m.setTag("")
	case tea.KeyDelete, tea.KeyCtrlD:
		// The whole tag, the next one taking its place.
		if m.ins.tagIdx != len(m.ins.tags)-1 {
			i := m.ins.tagIdx
			m.ins.tags = append(m.ins.tags[:i:i], m.ins.tags[i+1:]...)
		}
	case tea.KeyEnter:
		// Done with this one: on to a new tag.
		m.ins.tagIdx = len(m.ins.tags) - 1
	case tea.KeyTab:
		m.completeInsertTag()
	case tea.KeyLeft, tea.KeyCtrlB, tea.KeyCtrlLeft:
		m.moveTag(-1)
	case tea.KeyRight, tea.KeyCtrlF, tea.KeyCtrlRight:
		m.moveTag(1)
	case tea.KeyEnd, tea.KeyCtrlE:
		m.ins.tagIdx = len(m.ins.tags) - 1
	case tea.KeyHome, tea.KeyCtrlA:
		m.leaveTags()
		_, m.ins.buf.col = m.insertKeyword()
	case tea.KeyDown, tea.KeyCtrlN:
		m.leaveTags()
		m.ins.buf.down()
	}
	// Anything else (Space, ↑, ...) has nowhere to go.
	return true
}

// titleEndKey handles the keys that reach from the end of the title
// into its tags: → and End move into the tag area, and deletions stop
// at the title's end rather than eating into the tags.
func (m *Model) titleEndKey(msg tea.KeyMsg) bool {
	b := &m.ins.buf
	if b.row != 0 || m.ins.onKeyword {
		return false
	}
	end, tags := splitTitleTags(b.lines[0])
	atEnd := b.col == end
	switch msg.Type {
	case tea.KeyRight, tea.KeyCtrlF, tea.KeyCtrlRight:
		if atEnd {
			m.enterTags(false)
			return true
		}
	case tea.KeyRunes:
		if msg.Alt && !msg.Paste && string(msg.Runes) == "f" && atEnd {
			m.enterTags(false)
			return true
		}
		if msg.Alt && !msg.Paste && string(msg.Runes) == "d" && len(tags) > 0 {
			if r, c := b.wordEndAfter(); r != 0 || c > end {
				b.deleteRange(0, b.col, 0, end)
				return true
			}
		}
	case tea.KeyEnd, tea.KeyCtrlE:
		// The end of the title, then the new tag after the tags.
		if atEnd {
			m.enterTags(true)
		} else {
			b.col, b.goal = end, -1
		}
		return true
	case tea.KeyDelete, tea.KeyCtrlD:
		return atEnd && len(tags) > 0 // nothing of the title's to delete
	case tea.KeyCtrlK:
		if len(tags) > 0 {
			b.deleteRange(0, b.col, 0, end)
			return true
		}
	}
	return false
}

// completeInsertTag completes the tag under the caret against every tag
// in the workspace, as "gt"'s prompt did: to the one tag it's a prefix
// of, or as far as several share (the "Tags:" list below shows which).
func (m *Model) completeInsertTag() {
	cur := m.ins.tags[m.ins.tagIdx]
	matches := m.insertTagMatches()
	switch len(matches) {
	case 0:
		if cur == "" {
			m.message = "No existing tags"
		} else {
			m.message = fmt.Sprintf("No existing tag starting with %q", cur)
		}
	case 1:
		m.setTag(matches[0])
	default:
		if common := commonPrefix(matches); len(common) > len(cur) {
			m.setTag(common)
		}
	}
}

// insertTagMatches returns the workspace's tags the tag under the caret
// is a prefix of, other than ones the entry already has — what Tab can
// complete it to, and what the "Tags:" list below shows.
func (m *Model) insertTagMatches() []string {
	cur := m.ins.tags[m.ins.tagIdx]
	have := map[string]bool{}
	for i, t := range m.ins.tags {
		if i != m.ins.tagIdx {
			have[t] = true
		}
	}
	var out []string
	for _, t := range m.workspaceTags() {
		if strings.HasPrefix(t, cur) && !have[t] && t != cur {
			out = append(out, t)
		}
	}
	return out
}

// tagsDisplay returns insert mode's first line as drawn in the tag
// area — the title, then the tags with the empty one at the end — and
// the [c0, c1) runes of it the caret covers: the tag under it, or a
// blank cell between its colons if it's empty.
func (m *Model) tagsDisplay() (rs []rune, c0, c1 int) {
	rs = []rune(strings.TrimRight(m.ins.buf.lines[0], " "))
	rs = append(rs, []rune("   :")...)
	for i, t := range m.ins.tags {
		if i == m.ins.tagIdx {
			c0 = len(rs)
			if t == "" {
				t = " "
			}
			c1 = c0 + utf8.RuneCountInString(t)
		}
		rs = append(rs, []rune(t)...)
		rs = append(rs, ':')
	}
	return rs, c0, c1
}
