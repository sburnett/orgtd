package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/sburnett/orgtd/internal/org"
)

// TestMeetingIndexIsBuiltOncePerWorkspaceState guards the render-path fix
// the meetings index exists for: the gutter's ▣ marker asks "is this
// entry linked to a meeting" for every visible row on every redraw, which
// used to re-walk the whole workspace each time. Redrawing without
// changing anything must reuse one index; an edit must drop it, so the
// marker reflects the change.
func TestMeetingIndexIsBuiltOncePerWorkspaceState(t *testing.T) {
	now := time.Now()
	meeting := oneOffCalendarEventHeadline("kickoff-1", "Client Kickoff", now, now.Add(time.Hour))
	meeting.Tags = []string{"@alice"}
	item := &org.Headline{Level: 1, Title: "Prep slides", Tags: []string{"@alice"}}
	other := &org.Headline{Level: 1, Title: "Unrelated chore", Tags: []string{"@bob"}}
	projects := &org.File{Path: "projects.org", Headlines: []*org.Headline{item, other}}
	ws := meetingsFixture(
		&org.File{Path: "calendar.org", Headlines: []*org.Headline{meeting}},
		projects,
	)
	m := New(ws)
	m.width, m.height = 100, 30

	for i := 0; i < 5; i++ {
		m.View()
	}
	if got := m.meetings.builds; got != 1 {
		t.Errorf("index built %d times across 5 redraws of an unchanged workspace, want 1", got)
	}
	if view := stripANSI(m.View()); !strings.Contains(view, "▣") {
		t.Errorf("view has no ▣ marker for the tag-linked entry:\n%s", view)
	}

	// An edit (here, dropping the tag that linked the entry) invalidates
	// the index, and the next redraw reflects it.
	m.pushUndo(&tagChangeAction{h: item, f: projects, oldTags: item.Tags, newTags: nil})
	view := stripANSI(m.View())
	if got := m.meetings.builds; got != 2 {
		t.Errorf("index built %d times after one edit, want 2 (rebuilt once for the new state)", got)
	}
	if strings.Contains(view, "▣") {
		t.Errorf("view still shows ▣ after the linking tag was removed:\n%s", view)
	}

	// Undo brings the link, and the marker, back.
	m.undo()
	if view := stripANSI(m.View()); !strings.Contains(view, "▣") {
		t.Errorf("view lost the ▣ marker after undoing the tag removal:\n%s", view)
	}
}

// TestMeetingIndexWorksWithoutACache covers a Model built as a bare
// literal (several older tests do): with no cache, every query builds a
// fresh index, which is slower but still correct.
func TestMeetingIndexWorksWithoutACache(t *testing.T) {
	now := time.Now()
	meeting := oneOffCalendarEventHeadline("kickoff-1", "Client Kickoff", now, now.Add(time.Hour))
	meeting.Tags = []string{"@alice"}
	item := &org.Headline{Level: 1, Title: "Prep slides", Tags: []string{"@alice"}}
	m := Model{ws: meetingsFixture(
		&org.File{Path: "calendar.org", Headlines: []*org.Headline{meeting}},
		&org.File{Path: "projects.org", Headlines: []*org.Headline{item}},
	)}

	if got := m.meetingIndex().TagLinked(item, now); len(got) != 1 || got[0].Title != "Client Kickoff" {
		t.Errorf("TagLinked on a cacheless Model = %+v, want the Client Kickoff meeting", got)
	}
}
