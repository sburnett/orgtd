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
// top-level headline that isn't DONE/CANCELLED — review is for
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
// waiting to be filed away. A no-op if review is off, nothing's
// pinned, or the target is still active.
func (m *Model) advanceReviewTargetIfDone() {
	if m.reviewActive && m.reviewTarget != nil && org.IsDoneKeyword(m.reviewTarget.Keyword) {
		m.advanceReviewTarget()
	}
}

// reviewStep moves the review target by delta positions (1 for
// :next, -1 for :prev) among the inbox's top-level headlines, skipping
// any DONE/CANCELLED entries along the way, same as automatic
// advancement — manual navigation should never land on one either. If
// there's no current target (e.g. the inbox was empty when review
// was turned on but has since gained an item), this just establishes one
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

// toggleReview (":review") turns the "%" register on or
// off. On, it pins the inbox's first pending item (always the first,
// regardless of where the cursor was — review starts a top-to-bottom
// pass); off, it unpins it. Either way the current view is untouched.
func (m *Model) toggleReview() {
	if m.reviewActive {
		m.deactivateReview()
		m.message = "Review off"
		return
	}
	m.activateReview()
	if m.reviewTarget == nil {
		m.message = "Review on, but the inbox has nothing pending"
	}
}

// activateReview turns the "%" register on, pinning the inbox's first
// pending item.
func (m *Model) activateReview() {
	m.reviewActive = true
	m.advanceReviewTarget()
}

// deactivateReview turns the "%" register off.
func (m *Model) deactivateReview() {
	m.reviewActive = false
	m.reviewTarget = nil
}

// jumpToReviewTarget ("gc") moves the cursor to the real row of the
// item currently pinned for review, wherever it sits in the outline —
// switching to the outline first if the current view doesn't show it. A
// no-op if review is off or the inbox is empty.
func (m *Model) jumpToReviewTarget() {
	if !m.reviewActive || m.reviewTarget == nil {
		return
	}
	m.pushJump()
	if !m.spec().outlineRows {
		m.switchToViewNoJump(outlineView)
	}
	m.focusHeadline(m.reviewTarget)
}
