package ui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

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

// autoSyncModel is a Model with the OAuth client configured (so
// automatic syncing is on, at the default 24 hours) over a real temp
// directory, its clock pinned.
func autoSyncModel(t *testing.T, opts ...Option) Model {
	t.Helper()
	return New(syncFixture(t), append([]Option{WithGcalOAuthClient("id", "secret"), fixedClock(pinned)}, opts...)...)
}

// writeCalendarAged writes m's calendar file to disk with an mtime age
// before m's clock.
func writeCalendarAged(t *testing.T, m Model, age time.Duration) {
	t.Helper()
	path := m.calendarPath()
	if err := os.WriteFile(path, []byte("* Standup\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mtime := pinned.Add(-age)
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func autoTick(t *testing.T, m Model) (Model, tea.Cmd) {
	t.Helper()
	updated, cmd := m.Update(autoSyncTickMsg{})
	return updated.(Model), cmd
}

func TestInitStartsAutoSyncOnlyWhenEnabled(t *testing.T) {
	if cmd := New(syncFixture(t)).Init(); cmd != nil {
		t.Errorf("Init() = non-nil without an OAuth client, want nil")
	}
	disabled := New(syncFixture(t), WithGcalOAuthClient("id", "secret"))
	disabled.cfg.Gcalsync.AutoSyncHours = -1
	if cmd := disabled.Init(); cmd != nil {
		t.Errorf("Init() = non-nil with auto_sync_hours < 0, want nil")
	}
	cmd := autoSyncModel(t).Init()
	if cmd == nil {
		t.Fatal("Init() = nil with sync configured, want the first auto-sync check")
	}
	if _, ok := cmd().(autoSyncTickMsg); !ok {
		t.Errorf("Init's Cmd should send an autoSyncTickMsg right away")
	}
}

func TestAutoSyncTickSkipsFreshCalendar(t *testing.T) {
	m := autoSyncModel(t)
	writeCalendarAged(t, m, 23*time.Hour)
	m, cmd := autoTick(t, m)
	if m.syncingCalendar {
		t.Errorf("synced a calendar only 23h old (interval 24h)")
	}
	if cmd == nil {
		t.Errorf("cmd = nil, want the next check scheduled")
	}
}

func TestAutoSyncTickSyncsStaleOrMissingCalendar(t *testing.T) {
	stale := autoSyncModel(t)
	writeCalendarAged(t, stale, 25*time.Hour)
	missing := autoSyncModel(t)
	for name, m := range map[string]Model{"stale": stale, "missing": missing} {
		m, cmd := autoTick(t, m)
		if !m.syncingCalendar {
			t.Errorf("%s: no sync started", name)
		}
		if cmd == nil {
			t.Errorf("%s: cmd = nil, want the sync plus the next check", name)
		}
		if m.message != "" {
			t.Errorf("%s: message = %q, want an automatic sync to leave the status line alone", name, m.message)
		}
		// Deliberately not invoking cmd() — see
		// TestStartSyncCalendarReturnsCmdAndMarksInProgressWhenConfigured.
	}
}

func TestAutoSyncTickHonorsConfiguredInterval(t *testing.T) {
	m := autoSyncModel(t)
	m.cfg.Gcalsync.AutoSyncHours = 6
	writeCalendarAged(t, m, 7*time.Hour)
	if m, _ = autoTick(t, m); !m.syncingCalendar {
		t.Errorf("didn't sync a 7h-old calendar with auto_sync_hours = 6")
	}
}

func TestAutoSyncTickDoesNothingWhenDisabled(t *testing.T) {
	m := autoSyncModel(t)
	m.cfg.Gcalsync.AutoSyncHours = -1
	m, cmd := autoTick(t, m)
	if m.syncingCalendar || cmd != nil {
		t.Errorf("auto sync disabled, but tick started a sync or scheduled another check")
	}
}

func TestAutoSyncFailureLogsAndHoldsOffRetry(t *testing.T) {
	m := autoSyncModel(t)
	m.message = "something the user is reading"
	m, _ = autoTick(t, m)
	updated, _ := m.Update(syncCalendarMsg{err: calendarsync.ErrNotSignedIn, auto: true})
	m = updated.(Model)

	if m.message != "something the user is reading" {
		t.Errorf("message = %q, want a failed automatic sync to leave the status line alone", m.message)
	}
	entries := m.execLog.Snapshot()
	if len(entries) == 0 || !strings.Contains(entries[len(entries)-1].Text, "not signed in") {
		t.Errorf(":log entries = %#v, want one explaining the sync was skipped for lack of sign-in", entries)
	}

	if m, _ = autoTick(t, m); m.syncingCalendar {
		t.Errorf("retried right away after a failure, want to hold off for %v", autoSyncRetryDelay)
	}
	m.clock = func() time.Time { return pinned.Add(autoSyncRetryDelay) }
	if m, _ = autoTick(t, m); !m.syncingCalendar {
		t.Errorf("didn't retry once the hold-off had passed")
	}
}

func TestSuccessfulSyncResetsAutoSyncAge(t *testing.T) {
	m := autoSyncModel(t)
	m, _ = autoTick(t, m)
	updated, _ := m.Update(syncCalendarMsg{auto: true, result: calendarsync.Result{
		File: fakeSyncedFile(m.calendarPath(), "Standup"), EventCount: 1, CalendarCount: 1,
	}})
	m = updated.(Model)
	if m.message != "" {
		t.Errorf("message = %q, want an automatic sync's success logged, not shown", m.message)
	}
	m.clock = func() time.Time { return pinned.Add(23 * time.Hour) }
	if m, _ = autoTick(t, m); m.syncingCalendar {
		t.Errorf("synced again only 23h after the last successful sync")
	}
	m.clock = func() time.Time { return pinned.Add(24 * time.Hour) }
	if m, _ = autoTick(t, m); !m.syncingCalendar {
		t.Errorf("didn't sync 24h after the last successful sync")
	}
}

func TestCalendarStatusLineShowsSyncState(t *testing.T) {
	m := autoSyncModel(t)
	m.switchToView(calendarView)
	if line := m.normalStatusLine(); !strings.Contains(line, "never synced") {
		t.Errorf("status line = %q, want it to say the calendar was never synced", line)
	}

	m.lastCalendarSync = pinned.Add(-time.Hour)
	if line := m.normalStatusLine(); !strings.HasSuffix(line, "  —  synced 14:09") {
		t.Errorf("status line = %q, want a sync earlier today shown as a bare time", line)
	}
	m.lastCalendarSync = pinned.AddDate(0, 0, -1)
	if line := m.normalStatusLine(); !strings.HasSuffix(line, "  —  synced Mar 13 15:09") {
		t.Errorf("status line = %q, want an older sync shown with its date", line)
	}

	m.syncingCalendar = true
	if line := m.normalStatusLine(); !strings.Contains(line, "syncing now") {
		t.Errorf("status line = %q, want an in-flight sync shown", line)
	}

	updated, _ := m.Update(syncCalendarMsg{err: errors.New("offline")})
	m = updated.(Model)
	if line := m.normalStatusLine(); !strings.Contains(line, "synced Mar 13 15:09, last attempt failed (see :log)") {
		t.Errorf("status line = %q, want the failure shown alongside the last good sync", line)
	}
	if entries := m.execLog.Snapshot(); len(entries) == 0 || !strings.Contains(entries[len(entries)-1].Text, "offline") {
		t.Errorf(":log entries = %#v, want a manual sync's failure logged too", entries)
	}

	updated, _ = m.Update(syncCalendarMsg{result: calendarsync.Result{File: fakeSyncedFile(m.calendarPath(), "Standup")}})
	m = updated.(Model)
	m.switchToView(calendarView)
	if line := m.normalStatusLine(); !strings.HasSuffix(line, "  —  synced 15:09") {
		t.Errorf("status line = %q, want a successful sync to clear the failure and update the time", line)
	}
}

func TestNewSeedsLastSyncFromCalendarFile(t *testing.T) {
	ws := syncFixture(t)
	path := filepath.Join(ws.Dir, "calendar.org")
	if err := os.WriteFile(path, []byte("* Standup\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mtime := pinned.Add(-2 * time.Hour)
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	if m := New(ws, fixedClock(pinned)); !m.lastCalendarSync.Equal(mtime) {
		t.Errorf("lastCalendarSync = %v, want the calendar file's mtime %v", m.lastCalendarSync, mtime)
	}
}

func TestStatusLineOutsideCalendarViewOmitsSyncState(t *testing.T) {
	m := autoSyncModel(t)
	if line := m.normalStatusLine(); strings.Contains(line, "synced") {
		t.Errorf("status line = %q, want sync state only in :calendar", line)
	}
}
