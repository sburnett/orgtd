package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sburnett/orgtd/internal/org"
	"github.com/sburnett/orgtd/internal/workspace"
)

// referenceFixture is a workspace with one ordinary file and one file in
// reference/, whose entries carry exactly the task metadata :reference is
// supposed to ignore.
func referenceFixture() (ws *workspace.Workspace, task, ref *org.Headline) {
	task = &org.Headline{Level: 1, Keyword: "TODO", Title: "Pay the bill", Tags: []string{"@alice"}}
	ref = &org.Headline{Level: 1, Keyword: "NEXT", Title: "Wifi passwords", Tags: []string{"@alice"}}
	ref.Deadline = &org.Timestamp{Raw: "<2020-01-01 Wed>"}
	ref.Body = []string{"guest: hunter2"}
	ws = &workspace.Workspace{Dir: "refdir", Files: []*org.File{
		{Path: filepath.Join("refdir", "projects.org"), Headlines: []*org.Headline{task}},
		{Path: filepath.Join("refdir", workspace.ReferenceDir, "home.org"), Headlines: []*org.Headline{ref}},
	}}
	return ws, task, ref
}

func rowTitles(m Model) []string {
	var out []string
	for _, r := range m.rows {
		switch r.kind {
		case rowFile:
			out = append(out, "file:"+filepath.Base(r.file.Path))
		case rowBody:
		default:
			if r.headline != nil {
				out = append(out, r.headline.Title)
			}
		}
	}
	return out
}

func TestReferenceViewShowsOnlyReferenceFilesAndOutlineOmitsThem(t *testing.T) {
	ws, _, _ := referenceFixture()
	m := New(ws)
	if got := strings.Join(rowTitles(m), "|"); got != "file:projects.org|Pay the bill" {
		t.Errorf("outline rows = %q, want only the non-reference file", got)
	}

	m = typeKeys(m, ":reference")
	m = sendKey(m, "enter")
	if m.view != referenceView {
		t.Fatalf("view = %d, want referenceView (message %q)", m.view, m.message)
	}
	if got := strings.Join(rowTitles(m), "|"); got != "file:home.org|Wifi passwords" {
		t.Errorf("reference rows = %q, want only the reference file", got)
	}
}

func TestReferenceViewFoldsLikeTheOutline(t *testing.T) {
	ws, _, ref := referenceFixture()
	m := New(ws)
	m.switchToView(referenceView)
	if !hasBodyRow(m, ref) {
		t.Fatal("reference body line not shown while unfolded")
	}
	m = sendKey(m, "j") // onto the entry
	m = sendKey(m, "tab")
	if hasBodyRow(m, ref) {
		t.Error("Tab did not fold the reference entry")
	}
}

func hasBodyRow(m Model, h *org.Headline) bool {
	for _, r := range m.rows {
		if r.kind == rowBody && r.headline == h {
			return true
		}
	}
	return false
}

func TestReferenceViewIgnoresTaskStateAndDeadlines(t *testing.T) {
	ws, _, ref := referenceFixture()
	ref.Keyword = "DONE"
	ref.Closed = &org.Timestamp{Raw: "[2020-01-01 Wed 10:00]"} // long stale
	m := New(ws)
	m.hideDoneEnabled = true

	m.switchToView(referenceView)
	if !strings.Contains(strings.Join(rowTitles(m), "|"), "Wifi passwords") {
		t.Fatal("a stale DONE reference entry was hidden")
	}

	m = sendKey(m, "j")
	line := stripANSI(m.renderRow(m.rows[m.cursor]))
	if strings.Contains(line, "DEADLINE") || strings.Contains(line, "CLOSED") {
		t.Errorf("reference row shows planning info: %q", line)
	}
	if !strings.Contains(line, "DONE Wifi passwords") {
		t.Errorf("reference row = %q, want the keyword as plain title text", line)
	}
	if strings.Contains(m.renderRow(m.rows[m.cursor]), m.doneTitleStyle().Render("Wifi passwords")) {
		t.Error("reference title was rendered with the done (struck-through) style")
	}
}

func TestReferenceViewRefusesStatusAndDeadlineKeys(t *testing.T) {
	ws, _, ref := referenceFixture()
	m := New(ws)
	m.switchToView(referenceView)
	m = sendKey(m, "j")

	for _, key := range []string{"r", "R", "g d"} {
		m.message = ""
		m = typeKeys(m, strings.ReplaceAll(key, " ", ""))
		if m.mode != normalMode {
			t.Errorf("%q left mode = %d, want normal", key, m.mode)
			m.mode = normalMode
		}
		if !strings.Contains(m.message, "no task state") {
			t.Errorf("%q message = %q, want a refusal", key, m.message)
		}
	}
	if ref.Keyword != "NEXT" {
		t.Errorf("keyword changed to %q", ref.Keyword)
	}
}

func TestAgendaIgnoresReferenceEntries(t *testing.T) {
	ws, _, ref := referenceFixture()
	ref.Deadline = &org.Timestamp{Raw: "<" + time.Now().Format("2006-01-02 Mon") + ">"}
	m := New(ws)
	m.switchToView(agendaView)
	for _, r := range m.rows {
		if r.headline == ref {
			t.Errorf("agenda shows reference entry via row kind %d", r.kind)
		}
	}
}

func TestTagsViewListsReferenceEntries(t *testing.T) {
	ws, task, ref := referenceFixture()
	m := New(ws)
	m.switchToView(tagsView)
	var got []*org.Headline
	for _, r := range m.rows {
		if r.kind == rowTagsItem && r.tagsItemTag == "@alice" {
			got = append(got, r.headline)
		}
	}
	if len(got) != 2 || got[0] != task || got[1] != ref {
		t.Errorf("@alice entries = %v, want the task and the reference entry", got)
	}
}

func TestCalendarShowsTagMatchedReferenceEntry(t *testing.T) {
	ws, _, ref := referenceFixture()
	now := time.Now()
	meeting := oneOffCalendarEventHeadline("kickoff-1", "Kickoff", now, now.Add(time.Hour))
	meeting.Tags = []string{"@alice"}
	ws.Files = append(ws.Files, &org.File{Path: filepath.Join("refdir", "calendar.org"), Headlines: []*org.Headline{meeting}})
	m := New(ws)
	m.switchToView(calendarView)
	found := false
	for _, r := range m.rows {
		if r.kind == rowCalendarLinked && r.headline == ref {
			found = true
		}
	}
	if !found {
		t.Error("calendar doesn't nest the tag-matched reference entry under its meeting")
	}
}

func TestReferenceFileNamedLikeASpecialFileIsNotThatFile(t *testing.T) {
	inbox := &org.Headline{Level: 1, Title: "Real inbox item"}
	imposter := &org.Headline{Level: 1, Title: "Reference note"}
	ws := &workspace.Workspace{Dir: "refdir", Files: []*org.File{
		{Path: filepath.Join("refdir", "inbox.org"), Headlines: []*org.Headline{inbox}},
		{Path: filepath.Join("refdir", workspace.ReferenceDir, "inbox.org"), Headlines: []*org.Headline{imposter}},
	}}
	m := New(ws)
	if f := m.findInboxFile(); f == nil || f.Headlines[0] != inbox {
		t.Errorf("findInboxFile picked %v, want the top-level inbox.org", f)
	}
}

func editFixture(t *testing.T) Model {
	t.Helper()
	return New(loadFixtureCopy(t))
}

func runEdit(m Model, cmd string) Model {
	return sendKey(typeKeys(m, ":"+cmd), "enter")
}

func TestEditCreatesAnEmptyReferenceFileAndFocusesItsHeader(t *testing.T) {
	m := editFixture(t)
	m = runEdit(m, "e reference/wifi")
	if m.view != referenceView {
		t.Fatalf("view = %d, want referenceView (message %q)", m.view, m.message)
	}
	if r := m.rows[m.cursor]; r.kind != rowFile || filepath.Base(r.file.Path) != "wifi.org" {
		t.Errorf("cursor row = %+v, want wifi.org's header", r)
	}
	path := filepath.Join(m.ws.Dir, "reference", "wifi.org")
	if data, err := os.ReadFile(path); err != nil || len(data) != 0 {
		t.Errorf("file on disk = %q, %v; want empty", data, err)
	}
	if !strings.Contains(m.message, "Created reference/wifi.org") {
		t.Errorf("message = %q", m.message)
	}
}

func TestEditOnAnExistingFileJustMovesTheCursorToItsHeader(t *testing.T) {
	m := editFixture(t)
	n := len(m.ws.Files)
	m = runEdit(m, "edit projects.org")
	if len(m.ws.Files) != n {
		t.Errorf("files = %d, want %d (nothing created)", len(m.ws.Files), n)
	}
	if m.view != outlineView {
		t.Errorf("view = %d, want outline", m.view)
	}
	if r := m.rows[m.cursor]; r.kind != rowFile || filepath.Base(r.file.Path) != "projects.org" {
		t.Errorf("cursor row = %+v, want projects.org's header", r)
	}
	if m.message != "" {
		t.Errorf("message = %q, want none", m.message)
	}
}

func TestEditLoadsAFileCreatedOutsideOrgtd(t *testing.T) {
	m := editFixture(t)
	path := filepath.Join(m.ws.Dir, "reference", "later.org")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("* From a shell\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m = runEdit(m, "e reference/later.org")
	if m.view != referenceView || !strings.Contains(strings.Join(rowTitles(m), "|"), "From a shell") {
		t.Errorf("view %d rows %v, want the loaded file in :reference", m.view, rowTitles(m))
	}
	if strings.Contains(m.message, "Created") {
		t.Errorf("message = %q, the file already existed", m.message)
	}
}

func TestEditRejectsPathsOutsideTheScannedDirectories(t *testing.T) {
	for _, arg := range []string{"", "scratch/x", "reference/a/b", "../x", "/etc/x", ".hidden", "reference/"} {
		m := editFixture(t)
		n := len(m.ws.Files)
		m = runEdit(m, "e "+arg)
		if len(m.ws.Files) != n || m.message == "" {
			t.Errorf(":e %q: files %d→%d, message %q; want refused with a message", arg, n, len(m.ws.Files), m.message)
		}
	}
}
