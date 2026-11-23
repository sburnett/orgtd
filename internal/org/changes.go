package org

import (
	"bytes"
	"fmt"
	"strings"
	"unicode/utf8"
)

// ChangeKind is what happened to one entry between two versions of a
// file (see DiffEntries).
type ChangeKind int

const (
	Marked  ChangeKind = iota // its TODO keyword changed
	Added                     // it's new (only the topmost entry of a new subtree is reported)
	Edited                    // its own text changed: title, tags, planning, properties or body
	Moved                     // it has a new parent, or a new place among its siblings
	Removed                   // it's gone (only the topmost entry of a removed subtree is reported)
)

var kindVerbs = [...]string{Marked: "mark", Added: "add", Edited: "edit", Moved: "move", Removed: "remove"}

// EntryChange is one entry-level difference between two versions of an
// org file.
type EntryChange struct {
	Kind       ChangeKind
	Title      string // the entry's title in the new version (the old one, if Removed)
	Keyword    string // Marked: the new keyword, "" if cleared
	OldKeyword string // Marked: the old keyword, "" if there was none
}

// String describes c for a commit message, e.g. `mark DONE "Title"`.
func (c EntryChange) String() string {
	title := fmt.Sprintf("%q", truncateTitle(c.Title))
	if c.Kind == Marked {
		if c.Keyword == "" {
			return "unmark " + c.OldKeyword + " " + title
		}
		return "mark " + c.Keyword + " " + title
	}
	return kindVerbs[c.Kind] + " " + title
}

const maxTitleRunes = 60

func truncateTitle(s string) string {
	if utf8.RuneCountInString(s) <= maxTitleRunes {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:maxTitleRunes-1])) + "…"
}

// DiffEntries lists the entries that changed between old and new, two
// versions of the same org file: added and edited entries, state changes
// and moves in new's order, then removed entries in old's order. Each
// entry is reported once, as the first of Marked, Moved, Edited that
// applies.
//
// Entries are matched across versions by their outline path of titles,
// then by title alone (an entry refiled under a new parent), then by an
// :ID: or :CREATED: property (an entry whose title was edited). An entry
// that matches nothing was added or removed. A version that doesn't
// parse (a line too long to read) gives no changes. kw is the keyword
// set both versions are parsed with.
func DiffEntries(old, new []byte, kw Keywords) []EntryChange {
	oldF, err := Parse(bytes.NewReader(old), "", kw)
	if err != nil {
		return nil
	}
	newF, err := Parse(bytes.NewReader(new), "", kw)
	if err != nil {
		return nil
	}
	olds, news := flatten(oldF.Headlines), flatten(newF.Headlines)
	match := matchEntries(olds, news) // new → old
	matched := map[*Headline]bool{}   // old entries with a match
	for _, o := range match {
		matched[o] = true
	}
	moved := reordered(oldF.Headlines, newF.Headlines, match)
	for _, n := range news {
		if o := match[n]; o != nil && match[n.Parent] != o.Parent {
			moved[n] = true
		}
	}

	var out []EntryChange
	for _, n := range news {
		o := match[n]
		switch {
		case o == nil:
			if n.Parent == nil || match[n.Parent] != nil {
				out = append(out, EntryChange{Kind: Added, Title: n.Title})
			}
		case o.Keyword != n.Keyword:
			out = append(out, EntryChange{Kind: Marked, Title: n.Title, Keyword: n.Keyword, OldKeyword: o.Keyword})
		case moved[n]:
			out = append(out, EntryChange{Kind: Moved, Title: n.Title})
		case ownText(o) != ownText(n):
			out = append(out, EntryChange{Kind: Edited, Title: n.Title})
		}
	}
	for _, o := range olds {
		if !matched[o] && (o.Parent == nil || matched[o.Parent]) {
			out = append(out, EntryChange{Kind: Removed, Title: o.Title})
		}
	}
	return out
}

// flatten lists hs and all their descendants in document order.
func flatten(hs []*Headline) []*Headline {
	var out []*Headline
	var walk func([]*Headline)
	walk = func(hs []*Headline) {
		for _, h := range hs {
			out = append(out, h)
			walk(h.Children)
		}
	}
	walk(hs)
	return out
}

// matchEntries pairs entries of news with entries of olds (see
// DiffEntries), returning each matched new entry's old one.
func matchEntries(olds, news []*Headline) map[*Headline]*Headline {
	match := map[*Headline]*Headline{}
	used := map[*Headline]bool{}
	pass := func(key func(*Headline) string) {
		byKey := map[string][]*Headline{}
		for _, o := range olds {
			if k := key(o); !used[o] && k != "" {
				byKey[k] = append(byKey[k], o)
			}
		}
		for _, n := range news {
			k := key(n)
			if match[n] != nil || k == "" || len(byKey[k]) == 0 {
				continue
			}
			o := byKey[k][0]
			byKey[k] = byKey[k][1:]
			match[n] = o
			used[o] = true
		}
	}
	pass(titlePath)
	pass(func(h *Headline) string { return h.Title })
	pass(func(h *Headline) string { return prop(h, "ID") })
	pass(func(h *Headline) string { return prop(h, "CREATED") })
	return match
}

func titlePath(h *Headline) string {
	var parts []string
	for ; h != nil; h = h.Parent {
		parts = append(parts, h.Title)
	}
	return strings.Join(parts, "\x00")
}

func prop(h *Headline, key string) string {
	if v := h.Properties[key]; v != "" {
		return key + "\x00" + v
	}
	return ""
}

// reordered finds the matched entries whose place among their siblings
// changed: under each parent, the matched children kept in a longest run
// of their old order stay put, and the rest moved. oldTop is the old
// version's top-level entries.
func reordered(oldTop, top []*Headline, match map[*Headline]*Headline) map[*Headline]bool {
	oldIndex := map[*Headline]int{}
	var index func([]*Headline)
	index = func(hs []*Headline) {
		for i, h := range hs {
			oldIndex[h] = i
			index(h.Children)
		}
	}
	index(oldTop)

	moved := map[*Headline]bool{}
	var walk func(siblings []*Headline)
	walk = func(siblings []*Headline) {
		var kids []*Headline
		var order []int
		for _, n := range siblings {
			// An entry from another parent moved anyway; leave it out
			// so it can't make its new siblings look reordered.
			if o := match[n]; o != nil && o.Parent == match[n.Parent] {
				kids = append(kids, n)
				order = append(order, oldIndex[o])
			}
		}
		keep := longestIncreasing(order)
		for i, n := range kids {
			if !keep[i] {
				moved[n] = true
			}
		}
		for _, n := range siblings {
			walk(n.Children)
		}
	}
	walk(top)
	return moved
}

// longestIncreasing marks, for each index of seq, whether it belongs to
// one longest strictly increasing subsequence.
func longestIncreasing(seq []int) []bool {
	n := len(seq)
	length := make([]int, n)
	prev := make([]int, n)
	best := -1
	for i := range seq {
		length[i], prev[i] = 1, -1
		for j := 0; j < i; j++ {
			if seq[j] < seq[i] && length[j]+1 > length[i] {
				length[i], prev[i] = length[j]+1, j
			}
		}
		if best < 0 || length[i] > length[best] {
			best = i
		}
	}
	keep := make([]bool, n)
	for i := best; i >= 0; i = prev[i] {
		keep[i] = true
	}
	return keep
}

// ownText is h's own text (not its children's) as it would render at
// level 1, so a change of level alone doesn't count as an edit.
func ownText(h *Headline) string {
	c := *h
	c.Level = 1
	c.Keyword = ""
	return RenderEntry(&c)
}

// FileDiff is one file's old and new versions, for CommitMessage.
type FileDiff struct {
	Path     string
	Old, New []byte
	Deleted  bool // the file is gone; New is ignored
}

// maxBodyChanges caps how many changes a commit message's body lists.
const maxBodyChanges = 40

// CommitMessage describes files' changes for a commit: a subject line of
// prefix followed by each file's summary — its one change, e.g.
// `todo.org: mark DONE "Title"`, or a count of each kind, e.g.
// `inbox.org: add 2, edit 1` — and, when there's more than one change,
// a body listing them all, file by file. kw is the keyword set the files
// are parsed with.
func CommitMessage(prefix string, files []FileDiff, kw Keywords) string {
	var summaries []string
	var body strings.Builder
	total, listed := 0, 0
	for _, f := range files {
		if f.Deleted {
			summaries = append(summaries, f.Path+": delete file")
			continue
		}
		changes := DiffEntries(f.Old, f.New, kw)
		total += len(changes)
		summaries = append(summaries, f.Path+": "+summarize(changes))
		if len(changes) == 0 {
			continue
		}
		body.WriteString("\n" + f.Path + ":\n")
		for _, c := range changes {
			if listed == maxBodyChanges {
				break
			}
			body.WriteString("  " + c.String() + "\n")
			listed++
		}
	}
	msg := prefix + strings.Join(summaries, "; ")
	if total > 1 {
		msg += "\n" + body.String()
		if listed < total {
			msg += fmt.Sprintf("\n… and %d more\n", total-listed)
		}
	}
	return msg
}

// summarize is changes in a few words: the one change, or how many of
// each kind; "update" when no entry changed (only the text before the
// first entry, say, or blank lines).
func summarize(changes []EntryChange) string {
	switch len(changes) {
	case 0:
		return "update"
	case 1:
		return changes[0].String()
	}
	var counts [len(kindVerbs)]int
	for _, c := range changes {
		counts[c.Kind]++
	}
	var parts []string
	for k, n := range counts {
		if n > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", kindVerbs[k], n))
		}
	}
	return strings.Join(parts, ", ")
}
