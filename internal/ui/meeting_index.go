package ui

import (
	"github.com/sburnett/orgtd/internal/meetings"
)

// meetingCache holds the meetings.Index for the workspace as it currently
// stands, built lazily on first use and dropped whenever the workspace
// changes (see invalidateMeetingIndex). Everything that asks "which
// meetings exist" or "what's linked to this meeting" — the gutter's ▣
// marker on every visible row, the agenda's Meetings section, :calendar's
// nested items, the info buffer, the "gM" picker — goes through one shared
// index instead of re-walking every file per question, which previously
// made each redraw cost rows × meetings × workspace size.
//
// It's a pointer on Model, like execLog, because Model is copied on every
// Update and View: the cache behind every copy must be the same one, so
// invalidating it on any copy is seen by all and a lazily built index
// isn't thrown away with the copy that built it. A Model constructed as a
// bare literal (some tests) has no cache; meetingIndex then builds a fresh
// index per call, which is correct, just slower.
type meetingCache struct {
	idx    *meetings.Index
	builds int // how many times idx has been (re)built; for tests
}

// meetingIndex returns the meetings.Index for the current workspace
// state, building it if it was invalidated (or never built). The index is
// only valid until the workspace is next mutated — every mutation applies
// an undoAction (or swaps a whole file) and then calls rebuildRows, which
// invalidates it; don't hold one across such a change.
func (m *Model) meetingIndex() *meetings.Index {
	if m.meetings == nil {
		return meetings.Build(m.ws.Files, m.findMeetingTagsFile())
	}
	if m.meetings.idx == nil {
		m.meetings.idx = meetings.Build(m.ws.Files, m.findMeetingTagsFile())
		m.meetings.builds++
	}
	return m.meetings.idx
}

// invalidateMeetingIndex drops the cached index so the next meetingIndex
// call rebuilds it. Called from rebuildRows: every change to the files'
// contents (an edit, undo/redo, a calendar sync, a whole-file reload) is
// followed by one, since the rows themselves would be stale otherwise.
func (m *Model) invalidateMeetingIndex() {
	if m.meetings != nil {
		m.meetings.idx = nil
	}
}
