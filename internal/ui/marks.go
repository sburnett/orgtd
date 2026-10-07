package ui

import (
	"fmt"
	"sort"

	"github.com/sburnett/orgtd/internal/org"
)

// setMark ("m<letter>") marks the current headline as letter: '<letter>
// jumps back to it later, and it stays pinned in the info buffer at the
// bottom of the screen (in every view, alongside any other active marks —
// see infoBufferLines) until the mark is deleted or moved elsewhere with
// another "m<letter>". A no-op on a file/section row (nothing to mark).
//
// Each entry holds at most one mark: marking an entry that already has
// a different letter replaces it (the old letter is freed up). Marking
// an entry with the *same* letter it already has toggles the mark off
// instead — a quick way to clear one without dropping into command mode
// for :delmarks.
func (m *Model) setMark(letter rune) {
	h := m.currentHeadline()
	if h == nil {
		return
	}
	if existing, ok := m.markLetterFor(h); ok {
		delete(m.marks, existing)
		if existing == letter {
			m.message = fmt.Sprintf("Mark '%c' cleared", letter)
			return
		}
	}
	if m.marks == nil {
		m.marks = make(map[rune]*org.Headline)
	}
	m.marks[letter] = h
	m.message = fmt.Sprintf("Mark '%c' set", letter)
}

// jumpToMark ("'<letter>") moves the cursor to the headline marked
// letter. If it isn't present among the current view's rows (e.g. it
// has no due date and the current view is agenda), this switches to
// outline view first, since every headline is reachable there.
func (m *Model) jumpToMark(letter rune) {
	h, ok := m.marks[letter]
	if !ok {
		m.message = fmt.Sprintf("Mark '%c' is not set", letter)
		return
	}
	m.pushJump()
	if !m.rowsContainHeadline(h) {
		m.switchToViewNoJump(outlineView)
	}
	m.focusHeadline(h)
}

// rowsContainHeadline reports whether h is one of the headlines
// currently present in m.rows.
func (m *Model) rowsContainHeadline(h *org.Headline) bool {
	for _, r := range m.rows {
		if r.headline == h {
			return true
		}
	}
	return false
}

// markLetterFor returns the letter marking h, if any — an entry holds at
// most one (see setMark) — for showing a marker on its row in the
// listing.
func (m *Model) markLetterFor(h *org.Headline) (rune, bool) {
	best := rune(0)
	found := false
	for letter, target := range m.marks {
		if target == h && (!found || letter < best) {
			best, found = letter, true
		}
	}
	return best, found
}

// clearMarksFor removes every mark pointing at h — called after h is
// deleted, since a mark can't meaningfully point at a removed item.
func (m *Model) clearMarksFor(h *org.Headline) {
	for letter, target := range m.marks {
		if target == h {
			delete(m.marks, letter)
		}
	}
}

// remapHeadlineRefs keeps marks and :review's pin correct across an `i`
// edit (subtreeReplaceAction), which always replaces a headline with a
// freshly parsed one — a distinct pointer, even though nothing else
// about the edit changed. oldSet's own root (oldSet[0]) is remapped
// directly to newSet's root (or cleared, if the edit emptied the entry
// out entirely); anything else in oldSet — a descendant, or another
// top-level entry when a whole file is edited at once — has no reliable
// counterpart in the freshly-parsed tree, so its marks/review-target
// are cleared rather than left dangling on a headline no longer in any
// tree. Also used, with oldSet/newSet swapped, when the edit is undone.
func (m *Model) remapHeadlineRefs(oldSet, newSet []*org.Headline) {
	var oldRoot, newRoot *org.Headline
	if len(oldSet) > 0 {
		oldRoot = oldSet[0]
	}
	if len(newSet) > 0 {
		newRoot = newSet[0]
	}
	org.Walk(oldSet, func(h *org.Headline) {
		if h != oldRoot {
			if m.reviewTarget == h {
				m.reviewTarget = nil
			}
			m.clearMarksFor(h)
			return
		}
		if m.reviewTarget == h {
			m.reviewTarget = newRoot
		}
		for letter, target := range m.marks {
			if target != h {
				continue
			}
			if newRoot != nil {
				m.marks[letter] = newRoot
			} else {
				delete(m.marks, letter)
			}
		}
	})
}

// deleteMarks handles ":delmarks <letters>" (space-separated or run
// together, e.g. "a b" or "ab"), removing each and reporting any that
// weren't set.
func (m *Model) deleteMarks(arg string) {
	var removed, missing []rune
	for _, r := range arg {
		if r == ' ' {
			continue
		}
		if _, ok := m.marks[r]; ok {
			delete(m.marks, r)
			removed = append(removed, r)
		} else {
			missing = append(missing, r)
		}
	}
	switch {
	case len(removed) == 0 && len(missing) == 0:
		m.message = "Usage: :delmarks <letters> or :delmarks!"
	case len(missing) == 0:
		m.message = fmt.Sprintf("Deleted mark(s): %s", string(removed))
	case len(removed) == 0:
		m.message = fmt.Sprintf("No such mark(s): %s", string(missing))
	default:
		m.message = fmt.Sprintf("Deleted %s; no such mark(s): %s", string(removed), string(missing))
	}
}

// sortedMarkLetters returns the letters of every active mark, sorted —
// for a deterministic display order in infoBufferLines.
func (m Model) sortedMarkLetters() []rune {
	letters := make([]rune, 0, len(m.marks))
	for letter := range m.marks {
		letters = append(letters, letter)
	}
	sort.Slice(letters, func(i, j int) bool { return letters[i] < letters[j] })
	return letters
}
