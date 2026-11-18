package ui

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// refuseTaskStateInReference reports whether the current view is
// :reference, where TODO state and deadlines are deliberately ignored
// (see appendReferenceRows) — setting one there would have no visible
// effect, so r/R and gd refuse with a message instead of silently doing
// something invisible.
func (m *Model) refuseTaskStateInReference() bool {
	if m.view != referenceView {
		return false
	}
	m.message = "Reference entries have no task state or deadline — see :outline"
	return true
}

// startSetDeadline ("gd") puts the current entry into insert mode with
// the caret on its deadline's date, ready for Enter to change it (see
// insert_timestamp.go). With no deadline yet, it adds a "DEADLINE: "
// label to the planning line — making one if need be — with a date
// field open after it right away (see dateField); leaving that empty
// takes the label back out. Once the deadline's set — or that's cancelled — insert mode ends
// too (see insertSession.exitWhenDone). A no-op on file rows.
func (m *Model) startSetDeadline() {
	h := m.currentHeadline()
	if h == nil || m.refuseIfImmutable(h) {
		return
	}
	m.enterInsertMode(h, nil, caretAtTitle)
	m.ins.exitWhenDone = true
	b := &m.ins.buf
	if row, span, ok := m.planningDate("DEADLINE"); ok {
		b.row, b.col, b.goal = row, span[0], -1
		return
	}

	// Where org puts it: on the planning line, the line just below the
	// title, after SCHEDULED and before CLOSED.
	var label dateLabel
	if planning := len(b.lines) > 1 && isPlanningLine(b.lines[1]); planning {
		line := b.runes(1)
		if i := strings.Index(string(line), "CLOSED:"); i >= 0 {
			at := utf8.RuneCountInString(string(line)[:i])
			b.row, b.col = 1, at
			b.insert("DEADLINE:  ")
			label = dateLabel{1, at, 1, at + len("DEADLINE:  ")}
			b.col = at + len("DEADLINE: ")
		} else {
			text := "DEADLINE: "
			if len(line) > 0 && line[len(line)-1] != ' ' {
				text = " " + text
			}
			b.row, b.col = 1, len(line)
			b.insert(text)
			label = dateLabel{1, len(line), 1, b.col}
		}
	} else {
		title := len(b.runes(0))
		b.row, b.col = 0, title
		b.insert("\nDEADLINE: ")
		label = dateLabel{0, title, 1, b.col}
	}
	b.goal = -1
	m.ins.date = &dateField{row: b.row, start: b.col, active: true, label: &label, input: newTextBuffer("")}
}

// planningDate finds keyword's timestamp ("DEADLINE", ...) on insert
// mode's planning line: its row and [start, end) runes.
func (m *Model) planningDate(keyword string) (row int, span [2]int, ok bool) {
	b := m.ins.buf
	if len(b.lines) < 2 || !isPlanningLine(b.lines[1]) {
		return 0, span, false
	}
	line := b.lines[1]
	i := strings.Index(line, keyword+":")
	if i < 0 {
		return 0, span, false
	}
	after := utf8.RuneCountInString(line[:i]) + utf8.RuneCountInString(keyword) + 1
	rs := []rune(line)
	for _, s := range timestampSpans(line) {
		if s[0] >= after && strings.TrimSpace(string(rs[after:s[0]])) == "" {
			return 1, s, true
		}
	}
	return 0, span, false
}

// isPlanningLine reports whether line, in an entry's dedented text, is
// its planning line — as org.Parse decides, by how it starts.
func isPlanningLine(line string) bool {
	for _, k := range []string{"SCHEDULED:", "DEADLINE:", "CLOSED:"} {
		if strings.HasPrefix(line, k) {
			return true
		}
	}
	return false
}

// planningLabelRe matches one planning keyword's label, and any space
// after it.
var planningLabelRe = regexp.MustCompile(`(?:SCHEDULED|DEADLINE|CLOSED):[ \t]*`)

// dropDatelessPlanningLabels removes, from insert mode's planning line,
// every "SCHEDULED:"/"DEADLINE:"/"CLOSED:" left with no timestamp after
// it — as deleting a deadline's date leaves "DEADLINE: " — and the line
// itself if that empties it. Run as insert mode ends (see finishInsert):
// a label with nothing after it isn't planning to org, and would
// otherwise be kept as stray body text.
func (m *Model) dropDatelessPlanningLabels() {
	b := &m.ins.buf
	if len(b.lines) < 2 || !isPlanningLine(b.lines[1]) {
		return
	}
	line := b.lines[1]
	var kept strings.Builder
	last := 0
	for _, loc := range planningLabelRe.FindAllStringIndex(line, -1) {
		if ts := timestampRe.FindStringIndex(line[loc[1]:]); ts != nil && ts[0] == 0 {
			continue
		}
		kept.WriteString(line[last:loc[0]])
		last = loc[1]
	}
	kept.WriteString(line[last:])
	cleaned := strings.Join(strings.Fields(kept.String()), " ")
	if cleaned == strings.Join(strings.Fields(line), " ") {
		return
	}
	lines := append([]string(nil), b.lines...)
	if cleaned == "" {
		lines = append(lines[:1], lines[2:]...)
	} else {
		lines[1] = cleaned
	}
	b.lines = lines
	b.clamp()
}
