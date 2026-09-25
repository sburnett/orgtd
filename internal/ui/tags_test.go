package ui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sburnett/orgtd/internal/org"
)

func TestTagsViewGroupsEntriesByTagAlphabetically(t *testing.T) {
	ws := loadFixture(t)
	zebra := &org.Headline{Level: 1, Title: "Zebra task", Tags: []string{"zzz"}}
	alpha := &org.Headline{Level: 1, Title: "Alpha task", Tags: []string{"aaa"}}
	ws.Files = append(ws.Files, &org.File{
		Path:      filepath.Join(ws.Dir, "extra.org"),
		Headlines: []*org.Headline{zebra, alpha},
	})
	m := New(ws)
	m.switchToView(tagsView)

	var sections []string
	for _, r := range m.rows {
		if r.section != "" {
			sections = append(sections, r.section)
		}
	}
	if len(sections) < 2 {
		t.Fatalf("sections = %v, want at least 2", sections)
	}
	idxA, idxZ := -1, -1
	for i, s := range sections {
		if s == "aaa" {
			idxA = i
		}
		if s == "zzz" {
			idxZ = i
		}
	}
	if idxA < 0 || idxZ < 0 || idxA > idxZ {
		t.Errorf("sections = %v, want \"aaa\" before \"zzz\"", sections)
	}
}

func TestTagsViewOrdersEntriesWithinATagByCreatedTime(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	newer := &org.Headline{Level: 1, Title: "Captured second", Tags: []string{"proj"}}
	newer.SetProperty("CREATED", "["+now.Add(10*time.Minute).Format("2006-01-02 Mon 15:04")+"]")
	older := &org.Headline{Level: 1, Title: "Captured first", Tags: []string{"proj"}}
	older.SetProperty("CREATED", "["+now.Format("2006-01-02 Mon 15:04")+"]")

	ws.Files = append(ws.Files,
		// "aaa_project.org" sorts before "zzz_project.org", so file order
		// alone would put newer ahead of older here — the opposite of
		// CREATED order.
		&org.File{Path: filepath.Join(ws.Dir, "aaa_project.org"), Headlines: []*org.Headline{newer}},
		&org.File{Path: filepath.Join(ws.Dir, "zzz_project.org"), Headlines: []*org.Headline{older}},
	)
	m := New(ws)
	m.switchToView(tagsView)

	var order []string
	for _, r := range m.rows {
		if r.isTagsItem && r.tagsItemTag == "proj" {
			order = append(order, r.headline.Title)
		}
	}
	want := []string{"Captured first", "Captured second"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Errorf("order[%d] = %q, want %q", i, order[i], want[i])
		}
	}
}

func TestTagsViewEntryWithMultipleTagsAppearsUnderEach(t *testing.T) {
	ws := loadFixture(t)
	h := &org.Headline{Level: 1, Title: "Multi-tagged task", Tags: []string{"one", "two"}}
	ws.Files = append(ws.Files, &org.File{
		Path:      filepath.Join(ws.Dir, "extra.org"),
		Headlines: []*org.Headline{h},
	})
	m := New(ws)
	m.switchToView(tagsView)

	var tagsSeen []string
	for _, r := range m.rows {
		if r.isTagsItem && r.headline == h {
			tagsSeen = append(tagsSeen, r.tagsItemTag)
		}
	}
	want := []string{"one", "two"}
	if len(tagsSeen) != len(want) {
		t.Fatalf("tagsSeen = %v, want %v", tagsSeen, want)
	}
	for i := range want {
		if tagsSeen[i] != want[i] {
			t.Errorf("tagsSeen[%d] = %q, want %q", i, tagsSeen[i], want[i])
		}
	}
}

func TestTagsViewExcludesCalendarAndMeetingTagsFiles(t *testing.T) {
	ws := loadFixture(t)
	now := time.Now()
	event := calendarEventHeadline("abc123", now, now.Add(time.Hour))
	event.Tags = []string{"recurring", "@alice"}
	record := &org.Headline{Level: 1, Title: "Meeting record", Tags: []string{"standup"}}
	record.SetProperty("MEETING_TAG_EVENT_IDS", "abc123")
	ws.Files = append(ws.Files,
		&org.File{Path: filepath.Join(ws.Dir, "calendar.org"), Headlines: []*org.Headline{event}},
		&org.File{Path: filepath.Join(ws.Dir, "meeting-tags.org"), Headlines: []*org.Headline{record}},
	)
	m := New(ws)
	m.switchToView(tagsView)

	for _, r := range m.rows {
		if r.section == "recurring" || r.section == "@alice" || r.section == "standup" {
			t.Errorf("tagsView shows section %q from an excluded file", r.section)
		}
		if r.headline == event || r.headline == record {
			t.Errorf("tagsView shows a row for a headline from an excluded file")
		}
	}
}

func TestTagsViewHidesStaleDoneEntriesWhenToggled(t *testing.T) {
	ws := loadFixture(t)
	stale := &org.Headline{Level: 1, Title: "Old done task", Keyword: "DONE", Tags: []string{"proj"}}
	stale.Closed = &org.Timestamp{Raw: time.Now().Add(-48 * time.Hour).Format("2006-01-02 Mon 15:04")}
	ws.Files = append(ws.Files, &org.File{
		Path:      filepath.Join(ws.Dir, "extra.org"),
		Headlines: []*org.Headline{stale},
	})
	m := New(ws)
	m.hideDoneEnabled = true
	m.hideDoneAfterHours = 24
	m.switchToView(tagsView)

	for _, r := range m.rows {
		if r.headline == stale {
			t.Errorf("stale DONE entry shown in tagsView while hide-done is enabled")
		}
	}
}

func TestTagsCommandSwitchesViewAndBack(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)

	m = typeKeys(m, ":")
	m = typeKeys(m, "tags")
	m = sendKey(m, "enter")
	if m.view != tagsView {
		t.Fatalf("view = %v, want tagsView", m.view)
	}

	m = typeKeys(m, ":")
	m = typeKeys(m, "outline")
	m = sendKey(m, "enter")
	if m.view != outlineView {
		t.Fatalf("view = %v, want outlineView", m.view)
	}
}

func TestTagsViewEmptyShowsFriendlyMessageAndStatusLine(t *testing.T) {
	ws := loadFixture(t)
	// Strip every tag from the fixture so tagsView has nothing to show.
	for _, f := range ws.Files {
		org.Walk(f.Headlines, func(h *org.Headline) { h.Tags = nil })
	}
	m := New(ws)
	m.width, m.height = 100, 20
	m.switchToView(tagsView)

	out := m.View()
	if !strings.Contains(out, "No tags found") {
		t.Errorf("View() = %q, want a friendly empty-tags message", out)
	}
	lines := strings.Split(out, "\n")
	status := lines[len(lines)-2]
	if !strings.Contains(status, "tags") {
		t.Errorf("status line = %q, want it to mention the tags view", status)
	}
}

func TestTagsViewEnterJumpsToSource(t *testing.T) {
	ws := loadFixture(t)
	h := &org.Headline{Level: 1, Title: "Tagged task", Tags: []string{"proj"}}
	f := &org.File{Path: filepath.Join(ws.Dir, "extra.org"), Headlines: []*org.Headline{h}}
	ws.Files = append(ws.Files, f)
	m := New(ws)
	m.switchToView(tagsView)

	for i, r := range m.rows {
		if r.isTagsItem && r.headline == h {
			m.cursor = i
			break
		}
	}
	m = sendKey(m, "enter")
	if m.view != outlineView {
		t.Fatalf("view = %v, want outlineView", m.view)
	}
	if m.currentHeadline() != h {
		t.Errorf("cursor headline = %v, want %v", m.currentHeadline(), h)
	}
}
