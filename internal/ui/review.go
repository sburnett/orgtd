package ui

import (
	"github.com/sburnett/orgtd/internal/org"
)

// findInboxFile returns the workspace file :review treats as the
// inbox (see WithInboxFile), or nil if it isn't loaded.
func (m *Model) findInboxFile() *org.File {
	for _, f := range m.ws.Files {
		if m.isNamedFile(f, m.cfg.InboxFile) {
			return f
		}
	}
	return nil
}

// advanceReviewTarget sets m.reviewTarget to the inbox's first
// top-level headline that isn't DONE/CANCELLED — review mode is for
// processing pending items, so one already resolved (marked done but
// not yet filed away or deleted) is skipped rather than pinned for
// review — or nil if the inbox file is missing, empty, or every
// item in it is done.
func (m *Model) advanceReviewTarget() {
	f := m.findInboxFile()
	if f == nil {
		m.reviewTarget = nil
		return
	}
	for _, h := range f.Headlines {
		if !org.IsDoneKeyword(h.Keyword) {
			m.reviewTarget = h
			return
		}
	}
	m.reviewTarget = nil
}

// advanceReviewTargetIfDone re-pins past the current review target if
// a status change (r/R, single or bulk) just left it DONE/CANCELLED —
// there's no reason to keep a resolved item pinned in the info buffer
// waiting to be filed away. A no-op outside review view, if nothing's
// pinned, or if the target is still active.
func (m *Model) advanceReviewTargetIfDone() {
	if m.view == reviewView && m.reviewTarget != nil && org.IsDoneKeyword(m.reviewTarget.Keyword) {
		m.advanceReviewTarget()
	}
}

// reviewStep moves the review target by delta positions (1 for
// :next, -1 for :prev) among the inbox's top-level headlines, skipping
// any DONE/CANCELLED entries along the way, same as automatic
// advancement — manual navigation should never land on one either. If
// there's no current target (e.g. the inbox was empty when review view
// was entered but has since gained an item), this just establishes one
// at the natural starting point instead of stepping from nowhere. A
// no-op (with a status message) if there's nowhere left to go in that
// direction.
func (m *Model) reviewStep(delta int) {
	f := m.findInboxFile()
	if f == nil || len(f.Headlines) == 0 {
		m.message = "Inbox is empty"
		return
	}
	idx := -1
	for i, h := range f.Headlines {
		if h == m.reviewTarget {
			idx = i
			break
		}
	}
	if idx < 0 {
		m.advanceReviewTarget()
		return
	}
	for i := idx + delta; i >= 0 && i < len(f.Headlines); i += delta {
		if !org.IsDoneKeyword(f.Headlines[i].Keyword) {
			m.reviewTarget = f.Headlines[i]
			return
		}
	}
	if delta > 0 {
		m.message = "Already at the last pending inbox item"
	} else {
		m.message = "Already at the first pending inbox item"
	}
}

// enterReviewView switches to review view, pinning the inbox's first
// top-level headline for review (always the first, regardless of
// where the cursor was — :review starts a top-to-bottom pass).
func (m *Model) enterReviewView() {
	m.advanceReviewTarget()
	m.switchToView(reviewView)
}

// jumpToReviewTarget ("gc") moves the cursor to the real row of the
// item currently pinned for review, wherever it sits in the
// outline. A no-op outside review view, or if the inbox is empty.
func (m *Model) jumpToReviewTarget() {
	if m.view != reviewView || m.reviewTarget == nil {
		return
	}
	m.pushJump()
	m.focusHeadline(m.reviewTarget)
}
