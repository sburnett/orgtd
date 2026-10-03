package ui

import (
	"path/filepath"

	"github.com/sburnett/orgtd/internal/org"
)

// findInboxFile returns the workspace file :clarify treats as the
// inbox (see WithInboxFile), or nil if it isn't loaded.
func (m *Model) findInboxFile() *org.File {
	for _, f := range m.ws.Files {
		if filepath.Base(f.Path) == m.inboxFile {
			return f
		}
	}
	return nil
}

// advanceClarifyTarget sets m.clarifyTarget to the inbox's first
// top-level headline that isn't DONE/CANCELLED — clarify mode is for
// processing pending items, so one already resolved (marked done but
// not yet filed away or deleted) is skipped rather than pinned for
// clarification — or nil if the inbox file is missing, empty, or every
// item in it is done.
func (m *Model) advanceClarifyTarget() {
	f := m.findInboxFile()
	if f == nil {
		m.clarifyTarget = nil
		return
	}
	for _, h := range f.Headlines {
		if !org.IsDoneKeyword(h.Keyword) {
			m.clarifyTarget = h
			return
		}
	}
	m.clarifyTarget = nil
}

// advanceClarifyTargetIfDone re-pins past the current clarify target if
// a status change (r/R, single or bulk) just left it DONE/CANCELLED —
// there's no reason to keep a resolved item pinned in the info buffer
// waiting to be filed away. A no-op outside clarify view, if nothing's
// pinned, or if the target is still active.
func (m *Model) advanceClarifyTargetIfDone() {
	if m.view == clarifyView && m.clarifyTarget != nil && org.IsDoneKeyword(m.clarifyTarget.Keyword) {
		m.advanceClarifyTarget()
	}
}

// clarifyStep moves the clarify target by delta positions (1 for
// :next, -1 for :prev) among the inbox's top-level headlines, skipping
// any DONE/CANCELLED entries along the way, same as automatic
// advancement — manual navigation should never land on one either. If
// there's no current target (e.g. the inbox was empty when clarify view
// was entered but has since gained an item), this just establishes one
// at the natural starting point instead of stepping from nowhere. A
// no-op (with a status message) if there's nowhere left to go in that
// direction.
func (m *Model) clarifyStep(delta int) {
	f := m.findInboxFile()
	if f == nil || len(f.Headlines) == 0 {
		m.message = "Inbox is empty"
		return
	}
	idx := -1
	for i, h := range f.Headlines {
		if h == m.clarifyTarget {
			idx = i
			break
		}
	}
	if idx < 0 {
		m.advanceClarifyTarget()
		return
	}
	for i := idx + delta; i >= 0 && i < len(f.Headlines); i += delta {
		if !org.IsDoneKeyword(f.Headlines[i].Keyword) {
			m.clarifyTarget = f.Headlines[i]
			return
		}
	}
	if delta > 0 {
		m.message = "Already at the last pending inbox item"
	} else {
		m.message = "Already at the first pending inbox item"
	}
}

// enterClarifyView switches to clarify view, pinning the inbox's first
// top-level headline for clarification (always the first, regardless of
// where the cursor was — :clarify starts a top-to-bottom pass).
func (m *Model) enterClarifyView() {
	m.advanceClarifyTarget()
	m.switchToView(clarifyView)
}

// jumpToClarifyTarget ("gc") moves the cursor to the real row of the
// item currently pinned for clarification, wherever it sits in the
// outline. A no-op outside clarify view, or if the inbox is empty.
func (m *Model) jumpToClarifyTarget() {
	if m.view != clarifyView || m.clarifyTarget == nil {
		return
	}
	m.pushJump()
	m.focusHeadline(m.clarifyTarget)
}
