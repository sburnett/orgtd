package ui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sburnett/orgtd/internal/extprog"
	"github.com/sburnett/orgtd/internal/meetings"
	"github.com/sburnett/orgtd/internal/org"
)

// siblingHeadlines returns h's immediate previous and next siblings
// (within its parent's children, or its file's top-level list if h is
// top-level), or nil for either that doesn't exist. earlierCount is the
// number of further siblings before prev (i.e. not shown by prev alone).
func (m *Model) siblingHeadlines(h *org.Headline) (prev, next *org.Headline, earlierCount int) {
	f, parent, idx := m.ws.Locate(h)
	if idx < 0 {
		return nil, nil, 0
	}
	return org.Siblings{File: f, Parent: parent}.Neighbors(idx)
}

// resolveInsertPosition computes where a new entry belongs relative to
// the row under the cursor, for o/O and p/P alike: on a headline row, a
// sibling placed immediately after (before=false) or before (before=true)
// the current headline (after/before its whole subtree, if it has
// children); on a file row, the end (before=false) or beginning
// (before=true) of that file. ok is false if there's nothing sensible to
// place relative to.
func (m *Model) resolveInsertPosition(before bool) (f *org.File, parent *org.Headline, idx, level int, origin *org.Headline, ok bool) {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return nil, nil, 0, 0, nil, false
	}
	row := m.rows[m.cursor]

	switch {
	case row.headline != nil:
		h := row.headline
		origin, level = h, h.Level
		f, parent, idx = m.ws.Locate(h)
		if idx < 0 {
			return nil, nil, 0, 0, nil, false
		}
		if !before {
			idx++
		}
		return f, parent, idx, level, origin, true

	case row.file != nil:
		f, level = row.file, 1
		if !before {
			idx = len(f.Headlines)
		}
		return f, nil, idx, level, nil, true
	}
	return nil, nil, 0, 0, nil, false
}

// insertHeadline inserts a blank headline (see resolveInsertPosition for
// where) and opens it in $EDITOR. The insert isn't recorded in undo
// history until the editor session finishes successfully (see
// commitInsert), so the whole "open a headline, type into it" session is
// one undo step, matching vim's o/O. A row associated with a meeting —
// the event's own row/body, or (in calendarView) an item already linked
// to it — is special-cased to insertCalendarCapture instead, regardless
// of which view surfaces that row (calendarView's own rows, or a synced
// event nested under a record in meetingTagsView — see
// appendMeetingTagsHeadlines) — see insertCalendarCapture for why
// "before" doesn't apply to that path. This has to be a property of the
// row's own headline (via calendarEventForRow), not of m.view: any other
// view-scoped check would miss meetingTagsView's own nested event rows,
// falling back to resolveInsertPosition's default sibling-insert and
// splicing the new entry directly into calendar.org, where it would
// silently vanish on the next :sync-calendar.
func (m *Model) insertHeadline(before bool) tea.Cmd {
	if cmd, handled := m.insertCalendarCapture(); handled {
		return cmd
	}
	f, parent, idx, level, origin, ok := m.resolveInsertPosition(before)
	if !ok {
		return nil
	}
	return m.insertHeadlineAt(f, parent, idx, level, origin, nil, false, false, nil)
}

// insertCalendarCapture is o/O's behavior on a row associated with a
// meeting — recognized by calendarEventForRow regardless of which view
// is showing it (calendarView's own rows, or a synced event nested under
// a record in meetingTagsView) — rather than inserting a sibling
// relative to the cursor (resolveInsertPosition, insertHeadline's
// default) — which for a calendar event's own row would mean editing
// calendar.org itself, lost on the next :sync-calendar, and for an
// already-linked item (calendarView only — meetingTagsView never shows
// those) would mean a sibling in whatever unrelated file that item
// happens to live in — o/O here instead appends a new headline to the
// end of the inbox, the same target as "gC"/":capture" (see
// startCaptureImpl), and attaches it outright to the same meeting the
// cursor's row belongs to (same properties "gM"/buildMeetingAttachAction
// would set, chosen automatically rather than through the picker, since
// the meeting is already unambiguous from the cursor's row) — the insert
// and the attach are folded into one undo step by commitInsert (see
// insertContext.attachMeeting), so a single "u" removes both together.
//
// o and O behave identically here: once the insert always targets the
// end of one shared file rather than a position relative to the cursor,
// there's no "before" vs "after" left to distinguish (this mirrors gC/gX,
// which also ignore cursor position entirely). Their relative order under
// the event in calendarView (see linkedMeetingItems) is instead governed
// by CREATED, so repeated o/O presses still show up in the order they
// were actually inserted, regardless of which file each one is later
// filed into.
//
// switchToOutline is false only from calendarView itself (unlike
// gC/gX): once the meeting attach commits alongside the insert, the new
// entry already shows up nested under its meeting in calendarView's own
// rows, so the cursor stays right there rather than jumping to outline
// view. Any other view that can surface a meeting row (meetingTagsView)
// has no such nested listing of its own to land on, so there switching
// to outline view is what actually gets the cursor onto the new entry —
// same as gC/gX.
//
// handled is false (cmd always nil then) for a row that isn't associated
// with any meeting at all (a calendar day's section-header row, a plain
// meeting-tags record row, or anything in some other view entirely) —
// insertHeadline falls back to its ordinary resolveInsertPosition path,
// which already no-ops on rows with no headline or file of their own.
func (m *Model) insertCalendarCapture() (cmd tea.Cmd, handled bool) {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return nil, false
	}
	eventH, ok := calendarEventForRow(m.rows[m.cursor])
	if !ok {
		return nil, false
	}
	cand, ok := meetings.FromEvent(eventH)
	if !ok {
		return nil, false
	}
	f := m.findInboxFile()
	if f == nil {
		m.message = fmt.Sprintf("No %s file in this org directory", m.cfg.InboxFile)
		return nil, true
	}
	return m.insertHeadlineAt(f, nil, len(f.Headlines), 1, m.currentHeadline(), m.currentRowFile(), false, m.view != calendarView, &cand), true
}

// startCapture (:capture, "gC") appends a blank top-level headline to
// the end of the inbox file and opens it in $EDITOR — a dedicated
// quick-add path, distinct from o/O, that always targets the inbox
// regardless of the current cursor position or view (agenda, clarify,
// or scrolled to some other file entirely in outline). A no-op (with a
// status message) if the inbox file isn't loaded. Once the editor
// session commits, the outline view is focused on the newly captured
// entry (switching to it first if the current view's rows don't
// already include it — see insertContext.switchToOutline and
// viewSpec.outlineRows), ready for further edits (promote/demote, tag,
// schedule, ...) right away.
func (m *Model) startCapture() tea.Cmd {
	return m.startCaptureImpl(false)
}

// startCaptureAndPickMeeting ("gX") is startCapture immediately followed
// by "gM" (see startMeetingPicker) once the capture's editor session
// finishes successfully (see insertContext.thenPickMeeting/finishEdit) —
// a shortcut for the common case of capturing something during a
// meeting and wanting to attach that meeting to it right away, without
// two separate keystrokes bracketing the (possibly slow) editor
// round-trip. Cancelling the capture (empty/blank result, or the editor
// failing to run) never opens the picker — there's nothing to attach it
// to.
func (m *Model) startCaptureAndPickMeeting() tea.Cmd {
	return m.startCaptureImpl(true)
}

func (m *Model) startCaptureImpl(thenPickMeeting bool) tea.Cmd {
	f := m.findInboxFile()
	if f == nil {
		m.message = fmt.Sprintf("No %s file in this org directory", m.cfg.InboxFile)
		return nil
	}
	// origin is the headline the cursor is currently on, if any, so
	// cancelling the capture returns focus there rather than to the
	// inbox — capture is meant to not disturb whatever you were doing.
	// originFile covers the file-row case (origin nil): without it,
	// rollback would fall back to insertContext.f, which for capture is
	// always the inbox, not necessarily wherever the cursor actually was.
	return m.insertHeadlineAt(f, nil, len(f.Headlines), 1, m.currentHeadline(), m.currentRowFile(), thenPickMeeting, true, nil)
}

// insertHeadlineAt is the shared machinery behind insertHeadline (o/O)
// and startCapture: splices a blank headline into f (at index within
// parent's children, or f's top-level list if parent is nil) and opens
// it in $EDITOR — for a vim-family editor, cursor already right after
// the bullet and in insert mode (see extprog.AtEntryStart), so typing the
// new title can start immediately — pre-filled with a CREATED property
// set to now — org-mode's standard (if not automatic) convention for
// recording an entry's creation time, e.g. via org-capture's %U escape.
// It's part of the editable template, not stamped after the fact, so
// it's just as overridable or deletable as anything else the user types
// before saving. origin (and originFile, its fallback when origin is
// nil) is refocused if the session is rolled back — see rollbackInsert.
// The insert isn't recorded in undo history until the editor session
// finishes successfully (see commitInsert), so the whole "open a
// headline, type into it" session is one undo step.
//
// Deliberately does not attach any calendar-meeting info even from
// startCapture, unlike an earlier version of this feature: capture isn't
// interactive, so a wrong guess (any meeting merely in progress at the
// moment of capture, whether or not it's actually relevant) could only
// be undone by hand-editing properties afterward. "gM" (see
// startMeetingPicker) is the deliberate, interactive way to attach a
// meeting instead — nothing here does it for you, though
// thenPickMeeting (set only by startCaptureAndPickMeeting, "gX") queues
// it up to run automatically right after the editor session commits —
// see insertContext.thenPickMeeting and finishEdit. attachMeeting (set
// only by insertCalendarCapture, o/O from calendarView) instead attaches
// a specific, already-known meeting outright, with no picker — see
// insertContext.attachMeeting.
func (m *Model) insertHeadlineAt(f *org.File, parent *org.Headline, idx, level int, origin *org.Headline, originFile *org.File, thenPickMeeting, switchToOutline bool, attachMeeting *meetings.Meeting) tea.Cmd {
	tentative := &org.Headline{Level: level, Parent: parent}
	tentative.SetProperty("CREATED", "["+time.Now().Format("2006-01-02 Mon 15:04")+"]")
	org.Siblings{File: f, Parent: parent}.Splice(idx, 0, []*org.Headline{tentative})
	m.rebuildRows()
	m.focusHeadline(tentative)

	ctx := insertContext{f: f, parent: parent, index: idx, origin: origin, originFile: originFile, thenPickMeeting: thenPickMeeting, switchToOutline: switchToOutline, attachMeeting: attachMeeting}
	cmd := m.launchEditor(tentative, &ctx, extprog.AtEntryStart)
	if cmd == nil {
		// Couldn't even launch the editor; don't leave a blank
		// placeholder headline behind with no way to remove it.
		m.rollbackInsert(ctx, tentative)
	}
	return cmd
}
