package ui

import (
	"fmt"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sburnett/orgtd/internal/extprog"
	"github.com/sburnett/orgtd/internal/org"
)

// formatLinksSpan locates one bare URL within a headline's Title
// (field == formatLinksTitleField) or one of its Body lines
// (field == that line's index) — see collectFormatLinksTargets.
type formatLinksSpan struct {
	field      int
	start, end int
}

// formatLinksTitleField is formatLinksSpan.field's sentinel for "this
// span is in the headline's Title", distinguishing it from any
// (non-negative) Body line index.
const formatLinksTitleField = -1

// formatLinksTarget is one headline :format-links found bare URLs in,
// plus exactly where each one is — the pending edits, once formatted
// text comes back for each (see finishFormatLinks).
type formatLinksTarget struct {
	h     *org.Headline
	spans []formatLinksSpan
}

// collectFormatLinksTargets walks every headline in every loaded file
// except the calendar file (see WithCalendarFile) — it's regenerated
// wholesale by :sync-calendar, so any bare URL there (e.g. a meeting
// link pasted into an event's description) would just be reformatted
// again, or dropped, on the next sync — and returns each one that
// contains a bare URL not already wrapped in an org-mode link, together
// with the flat, ordered list of those URLs (target by target, Title
// then each Body line in order within a target) — the same order
// finishFormatLinks later consumes the external formatter's output in.
// A headline already locked by an earlier, still-in-flight
// :format-links batch (see m.immutable) is skipped: it's already
// queued, and rescanning it here against text that batch hasn't
// rewritten yet would just queue the same URLs a second time.
func (m *Model) collectFormatLinksTargets() ([]*formatLinksTarget, []string) {
	if m.bareURLRe == nil {
		m.bareURLRe = extprog.BareURLRegexp(m.cfg.URLFormatterPrefixes)
	}
	var targets []*formatLinksTarget
	var urls []string
	for _, f := range m.ws.Files {
		if filepath.Base(f.Path) == m.cfg.CalendarFile {
			continue
		}
		org.Walk(f.Headlines, func(h *org.Headline) {
			if m.immutable[h] {
				return
			}
			var spans []formatLinksSpan
			for _, sp := range extprog.BareURLSpans(m.bareURLRe, h.Title) {
				spans = append(spans, formatLinksSpan{field: formatLinksTitleField, start: sp[0], end: sp[1]})
				urls = append(urls, h.Title[sp[0]:sp[1]])
			}
			for i, line := range h.Body {
				for _, sp := range extprog.BareURLSpans(m.bareURLRe, line) {
					spans = append(spans, formatLinksSpan{field: i, start: sp[0], end: sp[1]})
					urls = append(urls, line[sp[0]:sp[1]])
				}
			}
			if len(spans) > 0 {
				targets = append(targets, &formatLinksTarget{h: h, spans: spans})
			}
		})
	}
	return targets, urls
}

// formatLinksMsg reports that a :format-links batch (see
// startFormatLinks) has finished — targets and urls are exactly what
// startFormatLinks sent, echoed back so finishFormatLinks doesn't need
// any other state to line formatted back up with the entries it came
// from. formatted is nil if err is set.
type formatLinksMsg struct {
	targets   []*formatLinksTarget
	formatted []string
	err       error
}

// formatLinksFormatterCmd returns the external program :format-links
// should invoke in batch mode: formatLinksURLFormatterCmd if set (see
// WithFormatLinksURLFormatter), else urlFormatterCmd — the same command
// used for live in-editor formatting, so configuring only url_formatter
// (as before this option existed) still works for :format-links too.
func (m *Model) formatLinksFormatterCmd() string {
	if m.cfg.FormatLinksURLFormatter != "" {
		return m.cfg.FormatLinksURLFormatter
	}
	return m.cfg.URLFormatter
}

// startFormatLinks (":format-links") locks every entry with an
// unformatted bare URL (see collectFormatLinksTargets) — immediately,
// on the main goroutine, before this Cmd even runs — then hands all of
// their URLs to formatLinksFormatterCmd in one external process, running
// in the background so the rest of the app stays fully usable while
// it's in flight (unlike the synchronous, foreground formatting a
// single `i` edit does). Locked entries show a gutter marker (see
// lockColumn) and refuse any command that would change them (see
// refuseIfImmutable/filterImmutable) until finishFormatLinks unlocks
// them.
func (m *Model) startFormatLinks() tea.Cmd {
	formatterCmd := m.formatLinksFormatterCmd()
	if formatterCmd == "" {
		m.message = "No URL formatter configured (see :config)"
		return nil
	}
	targets, urls := m.collectFormatLinksTargets()
	if len(targets) == 0 {
		m.message = "No unformatted URLs found"
		return nil
	}
	for _, t := range targets {
		m.immutable[t.h] = true
	}
	m.message = fmt.Sprintf("Formatting links for %d entries in the background...", len(targets))

	elog := m.execLog
	return func() tea.Msg {
		formatted, err := extprog.RunBatchFormatter(elog, formatterCmd, urls)
		return formatLinksMsg{targets: targets, formatted: formatted, err: err}
	}
}

// formatLinksFieldEdit pairs one formatLinksSpan with the formatted text
// that should replace it, for applyFormatLinksEdits.
type formatLinksFieldEdit struct {
	span      formatLinksSpan
	formatted string
}

// applyFormatLinksEdits rewrites text, replacing each edit's span with
// its formatted text. edits must be in ascending span order (as
// collectFormatLinksTargets produces them) and all refer to spans within
// text; they're applied back to front so replacing a later span never
// invalidates an earlier span's still-pending offsets.
func applyFormatLinksEdits(text string, edits []formatLinksFieldEdit) string {
	for i := len(edits) - 1; i >= 0; i-- {
		e := edits[i]
		text = text[:e.span.start] + e.formatted + text[e.span.end:]
	}
	return text
}

// finishFormatLinks applies a completed :format-links batch: every
// targeted headline is unlocked (whether or not formatting actually
// succeeded — a failed batch shouldn't leave entries stuck immutable
// forever with no way to retry other than restarting orgtd), and on
// success, each one's Title/Body is rewritten with its bare URLs
// replaced by the formatter's output, grouped into one undo step per
// file touched (a batchAction — see undo.go) the same way other bulk
// operations are.
func (m Model) finishFormatLinks(msg formatLinksMsg) (tea.Model, tea.Cmd) {
	for _, t := range msg.targets {
		delete(m.immutable, t.h)
	}

	if msg.err != nil {
		m.message = fmt.Sprintf("Link formatting failed%s: %v", m.debugLogHint(), msg.err)
		m.rebuildRows()
		return m, nil
	}

	idx := 0
	var order []*org.File
	byFile := make(map[*org.File][]undoAction)
	for _, t := range msg.targets {
		editsByField := make(map[int][]formatLinksFieldEdit)
		for _, sp := range t.spans {
			editsByField[sp.field] = append(editsByField[sp.field], formatLinksFieldEdit{span: sp, formatted: msg.formatted[idx]})
			idx++
		}

		newTitle := t.h.Title
		if edits, ok := editsByField[formatLinksTitleField]; ok {
			newTitle = applyFormatLinksEdits(newTitle, edits)
		}
		newBody := append([]string(nil), t.h.Body...)
		for i := range newBody {
			if edits, ok := editsByField[i]; ok {
				newBody[i] = applyFormatLinksEdits(newBody[i], edits)
			}
		}

		f := m.fileForHeadline(t.h)
		action := &linkFormatAction{h: t.h, f: f, oldTitle: t.h.Title, newTitle: newTitle, oldBody: t.h.Body, newBody: newBody}
		if _, ok := byFile[f]; !ok {
			order = append(order, f)
		}
		byFile[f] = append(byFile[f], action)
	}
	for _, f := range order {
		m.pushUndo(&batchAction{actions: byFile[f]})
	}
	m.message = fmt.Sprintf("Formatted links for %d entries", len(msg.targets))
	return m, nil
}
