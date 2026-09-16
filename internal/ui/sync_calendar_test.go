package ui

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/sburnett/orgtd/internal/calendarsync"
	"github.com/sburnett/orgtd/internal/org"
	"github.com/sburnett/orgtd/internal/workspace"
)

// syncFixture builds a workspace over a real temp directory (unlike
// agendaFixture's synthetic Dir) — finishSyncCalendar writes to disk, so
// it needs somewhere real to write to.
func syncFixture(t *testing.T, files ...*org.File) *workspace.Workspace {
	t.Helper()
	dir := t.TempDir()
	for _, f := range files {
		f.Path = filepath.Join(dir, filepath.Base(f.Path))
	}
	return &workspace.Workspace{Dir: dir, Files: files}
}

func TestStartSyncCalendarRefusesWhenNotConfigured(t *testing.T) {
	m := New(syncFixture(t))
	cmd := m.startSyncCalendar(false)
	if cmd != nil {
		t.Errorf("cmd = %v, want nil (not configured)", cmd)
	}
	if m.syncingCalendar {
		t.Errorf("syncingCalendar = true, want false")
	}
	if m.message == "" {
		t.Errorf("expected a status message explaining sync isn't configured")
	}
}

func TestStartSyncCalendarRefusesWhenAlreadyInProgress(t *testing.T) {
	m := New(syncFixture(t), WithGcalOAuthClient("id", "secret"))
	m.syncingCalendar = true
	cmd := m.startSyncCalendar(false)
	if cmd != nil {
		t.Errorf("cmd = %v, want nil (sync already in progress)", cmd)
	}
	if m.message == "" {
		t.Errorf("expected a status message explaining a sync is already running")
	}
}

func TestStartSyncCalendarReturnsCmdAndMarksInProgressWhenConfigured(t *testing.T) {
	m := New(syncFixture(t), WithGcalOAuthClient("id", "secret"))
	cmd := m.startSyncCalendar(false)
	if cmd == nil {
		t.Fatal("cmd = nil, want a background Cmd")
	}
	if !m.syncingCalendar {
		t.Errorf("syncingCalendar = false, want true")
	}
	if want := "Syncing calendar in the background..."; m.message != want {
		t.Errorf("message = %q, want %q", m.message, want)
	}
	// Deliberately not invoking cmd(): it calls calendarsync.Sync, which
	// talks to the real Google API and OS keychain — out of bounds for a
	// unit test.
}

func fakeSyncedFile(path string, title string) *org.File {
	return &org.File{
		Path:      path,
		Headlines: []*org.Headline{{Level: 1, Title: title}},
	}
}

func TestFinishSyncCalendarCreatesNewCalendarFile(t *testing.T) {
	ws := syncFixture(t)
	m := New(ws)
	m.syncingCalendar = true

	result := calendarsync.Result{
		File:          fakeSyncedFile(filepath.Join(ws.Dir, "calendar.org"), "Standup"),
		EventCount:    1,
		CalendarCount: 1,
	}
	updated, cmd := m.Update(syncCalendarMsg{result: result})
	m = updated.(Model)
	if cmd != nil {
		t.Errorf("finishSyncCalendar should not return a follow-up command")
	}
	if m.syncingCalendar {
		t.Errorf("syncingCalendar = true, want false after finishing")
	}

	if len(m.ws.Files) != 1 || m.ws.Files[0].Headlines[0].Title != "Standup" {
		t.Fatalf("ws.Files = %#v, want the synced file", m.ws.Files)
	}

	data, err := os.ReadFile(filepath.Join(ws.Dir, "calendar.org"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(data) == 0 {
		t.Errorf("calendar.org wasn't written to disk")
	}

	if want := "Synced 1 event(s) from 1 calendar(s) to calendar.org"; m.message != want {
		t.Errorf("message = %q, want %q", m.message, want)
	}
}

func TestFinishSyncCalendarReplacesExistingCalendarFileAndClearsStaleRefs(t *testing.T) {
	oldFile := fakeSyncedFile("calendar.org", "Old meeting")
	oldHeadline := oldFile.Headlines[0]
	ws := syncFixture(t, oldFile)
	m := New(ws)
	m.marks = map[rune]*org.Headline{'a': oldHeadline}

	result := calendarsync.Result{
		File:          fakeSyncedFile(filepath.Join(ws.Dir, "calendar.org"), "New meeting"),
		EventCount:    1,
		CalendarCount: 1,
	}
	updated, _ := m.Update(syncCalendarMsg{result: result})
	m = updated.(Model)

	if len(m.ws.Files) != 1 {
		t.Fatalf("ws.Files = %#v, want exactly the replaced file", m.ws.Files)
	}
	if newFile := m.ws.Files[0]; newFile == oldFile || newFile.Headlines[0].Title != "New meeting" {
		t.Fatalf("calendar file wasn't replaced with the synced one: %#v", newFile)
	}
	if _, ok := m.marks['a']; ok {
		t.Errorf("mark on the old calendar file's headline should have been cleared")
	}
}

func TestFinishSyncCalendarErrorSetsMessageAndClearsInProgress(t *testing.T) {
	ws := syncFixture(t)
	m := New(ws)
	m.syncingCalendar = true

	updated, cmd := m.Update(syncCalendarMsg{err: errors.New("sync test error")})
	m = updated.(Model)
	if cmd != nil {
		t.Errorf("finishSyncCalendar should not return a follow-up command on error")
	}
	if m.syncingCalendar {
		t.Errorf("syncingCalendar = true, want false after a failed sync")
	}
	if len(m.ws.Files) != 0 {
		t.Errorf("ws.Files = %#v, want unchanged (empty) after a failed sync", m.ws.Files)
	}
	if want := "Calendar sync failed: sync test error"; m.message != want {
		t.Errorf("message = %q, want %q", m.message, want)
	}
}
