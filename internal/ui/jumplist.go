package ui

import (
	"github.com/sburnett/orgtd/internal/org"
)

// jumpToSource ("Enter" on an agenda row or a tagsView row) switches to
// outline view with the cursor on that row's real headline. A no-op on a
// row with no headline of its own (a section header).
func (m *Model) jumpToSource() {
	h := m.currentHeadline()
	if h == nil {
		return
	}
	m.switchToView(outlineView)
	m.focusHeadline(h)
}

// jumpToLinkedSource is Enter in calendarView: on an item linked to a
// calendar event (see linkedMeetingItems) it jumps to that item's real
// place in the outline, like jumpToSource. A no-op on a calendar event's
// own row, since calendar_file is excluded from the outline entirely (see
// appendCalendarRows), so there'd be nowhere to jump to.
func (m *Model) jumpToLinkedSource() {
	if m.cursor >= 0 && m.cursor < len(m.rows) && m.rows[m.cursor].kind == rowCalendarLinked {
		m.jumpToSource()
	}
}

// switchToView changes which view rebuildRows populates m.rows with,
// after recording the current position in the jump list (see pushJump)
// so ctrl-o can return to it — resets the cursor to the top, since the
// two views have entirely different row sets and there's no sensible
// position to preserve otherwise. Jump-list restoration (restoreJumpEntry)
// uses switchToViewNoJump instead, to avoid a jump-back pushing a new
// jump of its own.
func (m *Model) switchToView(v viewKind) {
	m.pushJump()
	m.switchToViewNoJump(v)
}

func (m *Model) switchToViewNoJump(v viewKind) {
	m.view = v
	m.cursor = 0
	m.offset = 0
	m.rebuildRows()
}

// jumpEntry is one recorded position in the jump list (see pushJump) —
// the view it was recorded in, plus enough to relocate within that
// view once restored: headline is nil only for a row with no headline
// of its own (a file/section/day-header row), in which case file is
// used as a fallback the same way focusTarget already does elsewhere.
type jumpEntry struct {
	view     viewKind
	headline *org.Headline
	file     *org.File
}

// pushJump records the cursor's current position onto the jump list —
// vim calls this "before a large move", and orgtd applies it in the
// same spirit: gg/G, {/}, a confirmed search, jumping to a mark or to
// the review target, and switching views (see switchToView) entirely,
// since that already resets the cursor to row 0 with no way back
// otherwise. Deliberately not called for ordinary j/k or fold/edit
// commands — recording those would make the list useless clutter
// instead of a "where was I before that" trail.
//
// Jumping to the exact same spot the list's current top already holds
// is a no-op that still reports ok (matches vim not recording a
// redundant entry — see jumpBack, which relies on that to tell "nothing
// to bookmark here" apart from "already bookmarked"), and — same as
// typing after an undo discards redo history — pushing a genuinely new
// entry while jumpPos is behind the end of the list discards whatever
// "newer" entries ("gi" could have reached) came after it. Capped at
// maxJumpEntries, oldest dropped first.
//
// ok is false only when idx doesn't name an actual row of a nonempty
// row list (out of range, negative) — a row with neither a headline nor
// a file of its own (a section/day-header row, only possible in agenda
// or calendar view) still records a "just this view" entry (both nil),
// and so does idx==0 when the view has no rows at all (an empty agenda
// or calendar), so restoreJumpEntry can still switch back to that view
// even though it has no specific row, or even any row, to pinpoint
// within it. That matters most for jumpBack's own implicit bookmark of
// the live position (see below): a view switch commonly lands the
// cursor on exactly such a row (row 0 is a section header more often
// than not, and an empty agenda/calendar has no rows at all), and
// without a bookmark there, "gi" would have nothing to return to —
// silently unable to jump back to whichever view ctrl-o had just left,
// even though vim's own jumplist has no such gap switching between
// buffers.
func (m *Model) pushJump() bool {
	return m.pushJumpAt(m.cursor)
}

// pushJumpAt is pushJump, but records the position at row idx rather
// than m.cursor — needed by updateSearchMode's Enter case, where
// incremental search has already moved the cursor by confirm time, so
// what belongs in the jump list is searchOrigin, not the live cursor.
func (m *Model) pushJumpAt(idx int) bool {
	var h *org.Headline
	var f *org.File
	switch {
	case len(m.rows) == 0 && idx == 0:
		// Nothing to point at, but the view itself is still a valid
		// jump target (e.g. an empty agenda) — fall through with h/f
		// left nil.
	case idx < 0 || idx >= len(m.rows):
		return false
	default:
		r := m.rows[idx]
		h = r.headline
		f = r.file
		if f == nil && h != nil {
			f = m.ws.FileOf(h)
		}
	}
	entry := jumpEntry{view: m.view, headline: h, file: f}
	if m.jumpPos > 0 && m.jumpList[m.jumpPos-1] == entry {
		return true
	}
	m.jumpList = append(m.jumpList[:m.jumpPos], entry)
	m.jumpPos = len(m.jumpList)

	const maxJumpEntries = 100
	if len(m.jumpList) > maxJumpEntries {
		m.jumpList = m.jumpList[len(m.jumpList)-maxJumpEntries:]
		m.jumpPos = len(m.jumpList)
	}
	return true
}

// jumpBack ("ctrl-o") moves to the previous position in the jump list,
// same as vim's own ctrl-o. The first press from a "live" position (not
// already mid-navigation) also records that live position itself, so
// "gi" can bring you back to exactly where you started jumping from —
// mirroring vim's own jumplist quirk of the same shape. A no-op at the
// oldest entry, or with an empty list.
func (m *Model) jumpBack() {
	if len(m.jumpList) == 0 {
		return
	}
	if m.jumpPos == len(m.jumpList) {
		if m.pushJump() {
			// pushJump just bookmarked the live position (appended it,
			// or found it already at the top — either way ok) and, for
			// a real append, set jumpPos to the list's new (longer)
			// length, pointing just past what it appended. Step back
			// off that bookmark first, onto the last *real* one
			// recorded before it, which is what this first ctrl-o
			// press should land on. Skipped entirely if there was
			// nothing representable to bookmark at all (e.g. the
			// cursor's on a section/day-header row) — jumpPos is then
			// untouched, so the plain decrement below already lands on
			// the right entry.
			m.jumpPos--
		}
	}
	if m.jumpPos == 0 {
		return
	}
	m.jumpPos--
	m.restoreJumpEntry(m.jumpList[m.jumpPos])
}

// jumpForward ("gi") moves to the next (more recent) position in the
// jump list, same as vim's own ctrl-i — see the README for why orgtd
// binds this to "gi" instead: ctrl-i is the same byte as Tab, already
// bound to fold-toggle, and this app's terminal library can't tell the
// two apart. A no-op once already at the newest recorded entry.
func (m *Model) jumpForward() {
	if m.jumpPos >= len(m.jumpList)-1 {
		return
	}
	m.jumpPos++
	m.restoreJumpEntry(m.jumpList[m.jumpPos])
}

// restoreJumpEntry moves to entry's recorded position: switching view
// first (without disturbing the jump list itself — see
// switchToViewNoJump) if it differs from the current one, then focusing
// its headline (or file, as a fallback — see focusTarget). Silently a
// no-op beyond the view switch if the headline/file is no longer
// present in that view (e.g. deleted since, or no longer meeting
// whatever criteria the view filters on) — same as focusHeadline itself
// already does, rather than erroring on a stale entry.
func (m *Model) restoreJumpEntry(entry jumpEntry) {
	if m.view != entry.view {
		m.switchToViewNoJump(entry.view)
	}
	m.focusTarget(entry.headline, entry.file)
}
