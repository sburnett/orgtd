package mobilecore

import (
	"sort"
	"strings"
	"time"

	"github.com/sburnett/orgtd/internal/agenda"
	"github.com/sburnett/orgtd/internal/org"
	"github.com/sburnett/orgtd/internal/orgdate"
)

// Agenda returns the agenda as JSON (agendaResult): Overdue, Due Today,
// Upcoming (agenda_window_days) and Next Actions, by the same rules as
// the desktop's :agenda, minus Meetings.
func Agenda(groupDir string, nowUnix int64) (string, error) {
	mu.Lock()
	defer mu.Unlock()
	l, err := load(groupDir)
	if err != nil {
		return "", err
	}
	today := orgdate.TruncateToDate(now(nowUnix))
	res := agendaResult{Today: today.Format("2006-01-02"), Sections: []agendaSection{}}
	by := agenda.BySection(l.ws, l.cfg, today)
	for _, name := range agenda.DateSections {
		es := by[name]
		if len(es) == 0 {
			continue
		}
		sec := agendaSection{Name: name}
		for _, e := range es {
			sec.Items = append(sec.Items, agendaItem{Entry: l.flatEntry(e.Headline), Label: e.Label, Date: e.Date.Format("2006-01-02"), Repeater: e.Repeater, Missed: e.Missed})
		}
		res.Sections = append(res.Sections, sec)
	}
	if next := agenda.NextActionHeadlines(l.ws, l.cfg); len(next) > 0 {
		sec := agendaSection{Name: agenda.NextActions}
		for _, h := range next {
			sec.Items = append(sec.Items, agendaItem{Entry: l.flatEntry(h)})
		}
		res.Sections = append(res.Sections, sec)
	}
	return toJSON(res)
}

// Outline returns every outline file's headline tree as JSON
// (outlineResult), with reference/ files separately. Unless showClosed,
// a headline closed more than hide_closed_after ago is left out
// with its subtree, as the desktop outline does (reference material is
// never hidden).
func Outline(groupDir string, showClosed bool, nowUnix int64) (string, error) {
	mu.Lock()
	defer mu.Unlock()
	l, err := load(groupDir)
	if err != nil {
		return "", err
	}
	t := now(nowUnix)
	after := time.Duration(l.cfg.HideClosedAfter)
	keep := func(h *org.Headline) bool { return showClosed || !orgdate.StaleClosed(h, t, after) }
	res := outlineResult{Files: []outlineFile{}, Reference: []outlineFile{}}
	for _, f := range l.ws.Files {
		ref := l.ws.IsReference(f)
		if !ref && !l.isOutlineFile(f) {
			continue
		}
		of := outlineFile{File: l.rel(f), Entries: []entry{}}
		for i, h := range f.Headlines {
			if !ref && !keep(h) {
				continue
			}
			k := keep
			if ref {
				k = nil
			}
			of.Entries = append(of.Entries, l.toEntry(f, h, []int{i}, true, k))
		}
		if ref {
			res.Reference = append(res.Reference, of)
		} else {
			res.Files = append(res.Files, of)
		}
	}
	return toJSON(res)
}

// Search returns, as JSON (searchResult), every outline or reference
// headline whose title, tags or body contains query, case-insensitively —
// the desktop "/" match — in file order.
func Search(groupDir, query string) (string, error) {
	mu.Lock()
	defer mu.Unlock()
	l, err := load(groupDir)
	if err != nil {
		return "", err
	}
	res := searchResult{Query: query, Entries: []entry{}}
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return toJSON(res)
	}
	for _, f := range l.ws.Files {
		if !l.ws.IsReference(f) && !l.isOutlineFile(f) {
			continue
		}
		org.Walk(f.Headlines, func(h *org.Headline) {
			text := strings.ToLower(h.Title + "\n" + strings.Join(h.Tags, ":") + "\n" + strings.Join(h.Body, "\n"))
			if strings.Contains(text, q) {
				res.Entries = append(res.Entries, l.flatEntry(h))
			}
		})
	}
	return toJSON(res)
}

// Tags returns every tag used anywhere in the loaded files, as a JSON
// array of strings for the tag field's suggestions: most-used first (the
// ones offered before anything is typed), ties alphabetical.
func Tags(groupDir string) (string, error) {
	mu.Lock()
	defer mu.Unlock()
	l, err := load(groupDir)
	if err != nil {
		return "", err
	}
	uses := map[string]int{}
	for _, f := range l.ws.Files {
		if l.ws.IsNamed(f, l.cfg.CalendarFile) {
			continue // attendee tags; noise for capture
		}
		org.Walk(f.Headlines, func(h *org.Headline) {
			for _, t := range h.Tags {
				uses[t]++
			}
		})
	}
	tags := make([]string, 0, len(uses))
	for t := range uses {
		tags = append(tags, t)
	}
	sort.Slice(tags, func(i, j int) bool {
		if uses[tags[i]] != uses[tags[j]] {
			return uses[tags[i]] > uses[tags[j]]
		}
		return tags[i] < tags[j]
	})
	return toJSON(tags)
}
