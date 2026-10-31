package ui

import (
	"sort"
	"time"

	"github.com/sburnett/orgtd/internal/config"
	"github.com/sburnett/orgtd/internal/org"
	"github.com/sburnett/orgtd/internal/orgdate"
)

// snippetEntry is one completed headline destined for snippetsView, with
// when it was completed.
type snippetEntry struct {
	h  *org.Headline
	at time.Time
}

// snippetEntries collects every headline completed on or after start
// (see orgdate.CompletedTime), across the same files the outline shows,
// newest first, ties in file/tree order. A keyword with snippets = "skip"
// is left out however it was completed. Stale-DONE hiding deliberately
// doesn't apply: looking back at old completions is this view's purpose.
func (m *Model) snippetEntries(start time.Time) []snippetEntry {
	var entries []snippetEntry
	for _, f := range m.ws.Files {
		if m.isNonOutlineFile(f) {
			continue
		}
		org.Walk(f.Headlines, func(h *org.Headline) {
			if m.keywordSnippets(h.Keyword) == config.SnippetsSkip {
				return
			}
			if at, ok := orgdate.CompletedTime(h); ok && !at.Before(start) {
				entries = append(entries, snippetEntry{h: h, at: at})
			}
		})
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].at.After(entries[j].at) })
	return entries
}

// appendSnippetsRows appends the rows for snippetsView: one flush-left
// section-header row per day with anything completed on it, most recent
// day first, each followed by that day's entries and, under an entry with
// one, its closing note. The window is today and the SnippetsWindowDays-1
// days before it.
func (m *Model) appendSnippetsRows(dst *[]row) {
	start := orgdate.TruncateToDate(m.now()).AddDate(0, 0, 1-m.cfg.SnippetsWindowDays)
	var day time.Time
	for _, e := range m.snippetEntries(start) {
		if d := orgdate.TruncateToDate(e.at); !d.Equal(day) {
			day = d
			*dst = append(*dst, row{kind: rowSection, text: day.Format("2006-01-02 Mon")})
		}
		*dst = append(*dst, row{headline: e.h, level: 1, kind: rowSnippetsItem})
		m.appendClosingNote(dst, e.h)
	}
}

// appendClosingNote appends h's closing note, if any, as body rows under
// its row in snippetsView — what wrapping and j/k already do for body
// text, and searchable the same way (see row.bodySearch).
func (m *Model) appendClosingNote(dst *[]row, h *org.Headline) {
	note := orgdate.ClosingNote(h)
	if note == "" {
		return
	}
	const level = 2
	(*dst)[len(*dst)-1].bodySearch = note
	segs := wrapBodyLine(note, m.listWidth()-(5+2*level+2))
	var wrap *wrappedLine
	if len(segs) > 1 {
		wrap = &wrappedLine{segs: segs}
	}
	for i, seg := range segs {
		*dst = append(*dst, row{headline: h, level: level, kind: rowBody, text: seg, bodyIdx: i, wrap: wrap, wrapSeg: i})
	}
}

// jumpToSnippetSource is Enter in snippetsView: jumpToSource, except that
// most of what's listed here is old enough for the outline to be hiding
// it (see hiddenAsStaleDone), or sits under something that is, and
// there'd be no row to land on. Turns the hiding off in that case, the
// same switch as :toggleclosed.
func (m *Model) jumpToSnippetSource() {
	h := m.currentHeadline()
	if h == nil {
		return
	}
	hidden := false
	for p := h; p != nil; p = p.Parent {
		hidden = hidden || m.hiddenAsStaleDone(p)
	}
	if hidden {
		m.hideClosedEnabled = false
	}
	m.jumpToSource()
	if hidden {
		m.message = "Showing all DONE/CANCELLED items"
	}
}

// keywordSnippets returns the configured snippets role of keyword ("" if
// none, or if it isn't a configured keyword).
func (m Model) keywordSnippets(keyword string) string {
	k, _ := m.keywordConfig(keyword)
	return k.Snippets
}
