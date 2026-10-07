package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// statusCandidate is one entry in the "set status" picker (R).
type statusCandidate struct {
	keyword  string // "" for "no keyword"
	label    string // display label
	shortcut rune   // single-key shortcut
}

// statusCandidates lists every state selectable via R, in display order.
// Every label starts with a distinct letter (matching its shortcut),
// except "(none)", which uses '-' instead.
var statusCandidates = []statusCandidate{
	{"TODO", "TODO", 't'},
	{"NEXT", "NEXT", 'n'},
	{"WAITING", "WAITING", 'w'},
	{"SOMEDAY", "SOMEDAY", 's'},
	{"DONE", "DONE", 'd'},
	{"CANCELLED", "CANCELLED", 'c'},
	{"", "(none)", '-'},
}

// matchesFilter reports whether c is a candidate for the typed filter
// (a lowercase prefix of its label, or its exact shortcut).
func (c statusCandidate) matchesFilter(filter string) bool {
	if filter == "" {
		return true
	}
	if strings.HasPrefix(strings.ToLower(c.label), filter) {
		return true
	}
	return len(filter) == 1 && rune(filter[0]) == c.shortcut
}

// statusCandidateIndex returns keyword's position in statusCandidates.
func statusCandidateIndex(keyword string) int {
	for i, c := range statusCandidates {
		if c.keyword == keyword {
			return i
		}
	}
	return -1
}

func filteredStatusCandidates(filter string) []statusCandidate {
	var out []statusCandidate
	for _, c := range statusCandidates {
		if c.matchesFilter(filter) {
			out = append(out, c)
		}
	}
	return out
}

// updateSelectMode handles key presses while the status picker (R) is
// open: j/k or arrows browse the (possibly filtered) candidate list,
// typing a letter narrows it — auto-applying as soon as exactly one
// candidate matches, which is immediate for any of the single-letter
// shortcuts — Enter applies the highlighted candidate, and Esc cancels.
func (m Model) updateSelectMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.mode = normalMode
		m.selectFilter = ""
		m.selectModeTargets = nil
		return m, nil

	case tea.KeyEnter:
		return m.applySelectedStatus()

	case tea.KeyBackspace:
		if r := []rune(m.selectFilter); len(r) > 0 {
			m.selectFilter = string(r[:len(r)-1])
		}
		m.selectIndex = 0
		return m, nil

	case tea.KeyUp:
		m.moveSelectHighlight(-1)
		return m, nil

	case tea.KeyDown:
		m.moveSelectHighlight(1)
		return m, nil

	case tea.KeyRunes:
		for _, r := range msg.Runes {
			m.typeSelectChar(r)
		}
		return m, nil
	}

	return m, nil
}

// typeSelectChar handles one typed rune in the status picker. j/k always
// move the highlight, matching the rest of the app's keybindings.
// Otherwise the rune is tried as a filter character: if it would leave
// zero matching candidates it's rejected outright (there's nothing
// useful to backspace back from), and if it leaves exactly one, that
// candidate is applied immediately.
func (m *Model) typeSelectChar(r rune) {
	switch r {
	case 'j':
		m.moveSelectHighlight(1)
		return
	case 'k':
		m.moveSelectHighlight(-1)
		return
	}

	candidateFilter := strings.ToLower(m.selectFilter + string(r))
	matches := filteredStatusCandidates(candidateFilter)
	if len(matches) == 0 {
		return
	}
	m.selectFilter = candidateFilter
	m.selectIndex = 0
	if len(matches) == 1 {
		m.applyChosenStatus(matches[0].keyword, matches[0].label)
		m.mode = normalMode
		m.selectFilter = ""
	}
}

func (m *Model) moveSelectHighlight(delta int) {
	n := len(filteredStatusCandidates(m.selectFilter))
	m.selectIndex += delta
	if m.selectIndex < 0 {
		m.selectIndex = 0
	}
	if m.selectIndex >= n {
		m.selectIndex = n - 1
	}
}

// applySelectedStatus applies the currently highlighted candidate and
// always returns to normal mode.
func (m Model) applySelectedStatus() (tea.Model, tea.Cmd) {
	matches := filteredStatusCandidates(m.selectFilter)
	m.mode = normalMode
	m.selectFilter = ""
	if len(matches) == 0 {
		return m, nil
	}
	idx := m.selectIndex
	if idx < 0 {
		idx = 0
	}
	if idx >= len(matches) {
		idx = len(matches) - 1
	}
	m.applyChosenStatus(matches[idx].keyword, matches[idx].label)
	return m, nil
}

// applyChosenStatus applies keyword (labeled label, for the bulk status
// message) to either selectModeTargets (set when the R picker now
// resolving was entered from visual mode or with a numeric prefix) or
// just the current headline otherwise — shared by applySelectedStatus
// (Enter) and typeSelectChar's single-match auto-apply.
func (m *Model) applyChosenStatus(keyword, label string) {
	if targets := m.selectModeTargets; len(targets) > 0 {
		m.selectModeTargets = nil
		m.applyStatusToHeadlineSet(targets, keyword, label)
		return
	}
	m.applyStatus(keyword)
}

// currentStatusIndex returns the statusCandidates index matching the
// current headline's keyword, used to pre-highlight it when R opens.
func (m *Model) currentStatusIndex() int {
	h := m.currentHeadline()
	if h == nil {
		return 0
	}
	for i, c := range statusCandidates {
		if c.keyword == h.Keyword {
			return i
		}
	}
	return 0
}

// applyStatus sets the current headline's keyword, stamping or clearing
// CLOSED to match org-mode's convention of recording when an item
// entered (or left) a DONE-class state — unless h is completing
// (transitioning from a non-done to a done-class keyword) and has a
// repeating SCHEDULED/DEADLINE, in which case repeatAdvanceForCompletion
// takes over instead: per org-mode, the keyword never actually changes
// and the repeating timestamp(s) advance rather than the item closing.
// See buildStatusChangeAction, which does the actual work (shared with
// visual-mode R's bulk apply). In review view, this also advances past
// the pinned target if it just became DONE/CANCELLED (see
// advanceReviewTargetIfDone).
func (m *Model) applyStatus(keyword string) {
	h := m.currentHeadline()
	if h == nil || m.refuseIfImmutable(h) {
		return
	}
	m.pushUndo(m.buildStatusChangeAction(h, keyword))
	m.advanceReviewTargetIfDone()
}

// statusSelectorLines renders one line per statusCandidate (the "R"
// status picker's candidates) for the info buffer's "Status:" section —
// the structured, one-per-line counterpart of the old
// renderStatusSelector, which crammed every candidate onto a single
// command-line row. Every candidate is always shown, same as before
// (matches only narrows which one is highlighted, not which are
// listed — see filteredStatusCandidates): each line is "[shortcut]
// label", with the currently highlighted candidate in reverse video.
func (m Model) statusSelectorLines() []string {
	matches := filteredStatusCandidates(m.selectFilter)
	highlighted := -1
	if len(matches) > 0 {
		idx := m.selectIndex
		if idx < 0 {
			idx = 0
		}
		if idx >= len(matches) {
			idx = len(matches) - 1
		}
		highlighted = statusCandidateIndex(matches[idx].keyword)
	}

	lines := make([]string, len(statusCandidates))
	for i, c := range statusCandidates {
		text := fmt.Sprintf("[%c] %s", c.shortcut, c.label)
		if i == highlighted {
			lines[i] = m.cursorStyle().Render(" " + text)
		} else {
			lines[i] = bgSpan(m.overlayBg(), " ") + shortcutStyle.Background(m.overlayBg()).Render(fmt.Sprintf("[%c]", c.shortcut)) + bgSpan(m.overlayBg(), " "+c.label)
		}
	}
	return lines
}
