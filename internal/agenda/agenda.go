// Package agenda computes the agenda's date-driven sections (Overdue,
// Due Today, Upcoming) and its Next Actions list from a loaded
// workspace. It is pure — no UI, no clock of its own (callers pass
// "today") — so the terminal UI (internal/ui) and the iOS core
// (mobilecore) bucket entries identically. The UI's Meetings section is
// not here: it depends on the meetings index, which only the UI keeps.
// See DESIGN.md §3.
package agenda

import (
	"sort"
	"time"

	"github.com/sburnett/orgtd/internal/config"
	"github.com/sburnett/orgtd/internal/org"
	"github.com/sburnett/orgtd/internal/orgdate"
	"github.com/sburnett/orgtd/internal/workspace"
)

// Section names, as shown in the agenda.
const (
	Overdue     = "Overdue"
	DueToday    = "Due Today"
	Upcoming    = "Upcoming"
	NextActions = "Next Actions"
)

// DateSections lists the date-driven sections in display order.
var DateSections = []string{Overdue, DueToday, Upcoming}

// Entry is one (headline, relevant date) pair destined for the agenda —
// a headline with both SCHEDULED and DEADLINE produces two.
type Entry struct {
	Headline *org.Headline
	Label    string    // "Scheduled" or "Deadline"
	Date     time.Time // the date (current occurrence, for a repeater), at midnight
	Repeater string    // e.g. "+1w", if Date came from a recurring timestamp; empty otherwise
	Missed   int       // occurrences elapsed since Date without completion; only meaningful when Date is Overdue
}

// KeywordRole returns the configured agenda role of keyword ("",
// config.AgendaNext or config.AgendaSkip) — "" if it isn't a configured
// keyword.
func KeywordRole(cfg config.Config, keyword string) string {
	for _, k := range cfg.EffectiveKeywords() {
		if k.Name == keyword {
			return k.Agenda
		}
	}
	return ""
}

// Entries collects one entry per (headline, relevant date) pair across
// every non-reference file in ws, for every headline that's active (not
// done) and whose keyword isn't agenda = "skip" (SOMEDAY by default) —
// that state is excluded from the dated sections entirely, even for a
// date within the window. Only dates on or before today +
// cfg.AgendaWindowDays are collected; anything further out (or a
// SCHEDULED/DEADLINE whose text can't be parsed) is dropped. Entries come
// in file/tree order, DEADLINE before SCHEDULED for the same headline.
func Entries(ws *workspace.Workspace, cfg config.Config, today time.Time) []Entry {
	var entries []Entry
	kw := cfg.OrgKeywords()
	horizon := today.AddDate(0, 0, cfg.AgendaWindowDays)
	consider := func(ts *org.Timestamp, label string, h *org.Headline) {
		date, missed, ok := orgdate.ParseTimestampDate(ts, today)
		if !ok || date.After(horizon) {
			return
		}
		entries = append(entries, Entry{Headline: h, Label: label, Date: date, Repeater: orgdate.RepeaterCookie(ts), Missed: missed})
	}
	for _, f := range ws.Files {
		if ws.IsReference(f) {
			continue
		}
		org.Walk(f.Headlines, func(h *org.Headline) {
			if kw.IsDone(h.Keyword) || KeywordRole(cfg, h.Keyword) == config.AgendaSkip {
				return
			}
			consider(h.Deadline, "Deadline", h)
			consider(h.Scheduled, "Scheduled", h)
		})
	}
	return entries
}

// Section buckets date relative to today: Overdue (before today),
// DueToday, or Upcoming (after today — callers are expected to have
// already excluded anything beyond the window).
func Section(date, today time.Time) string {
	switch {
	case date.Before(today):
		return Overdue
	case date.Equal(today):
		return DueToday
	default:
		return Upcoming
	}
}

// BySection groups Entries(ws, cfg, today) into the DateSections, each
// sorted by date with ties kept in file/tree order. Missed is zeroed
// outside Overdue, where it has no meaning. A section with nothing in it
// is absent from the map.
func BySection(ws *workspace.Workspace, cfg config.Config, today time.Time) map[string][]Entry {
	out := make(map[string][]Entry, len(DateSections))
	for _, e := range Entries(ws, cfg, today) {
		s := Section(e.Date, today)
		if s != Overdue {
			e.Missed = 0
		}
		out[s] = append(out[s], e)
	}
	for _, es := range out {
		sort.SliceStable(es, func(i, j int) bool { return es[i].Date.Before(es[j].Date) })
	}
	return out
}

// NextActionHeadlines returns every headline whose keyword has agenda =
// "next" (NEXT by default) across every non-reference file in ws, in
// file/tree order — regardless of whether it has a SCHEDULED or DEADLINE
// date, since NEXT is GTD's "immediately actionable" state on its own
// merits. A NEXT item with a near-term date additionally appears in its
// date-based section too.
func NextActionHeadlines(ws *workspace.Workspace, cfg config.Config) []*org.Headline {
	var next []*org.Headline
	for _, f := range ws.Files {
		if ws.IsReference(f) {
			continue
		}
		org.Walk(f.Headlines, func(h *org.Headline) {
			if KeywordRole(cfg, h.Keyword) == config.AgendaNext {
				next = append(next, h)
			}
		})
	}
	return next
}
