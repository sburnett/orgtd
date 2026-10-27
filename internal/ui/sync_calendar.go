package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sburnett/orgtd/internal/calendarsync"
	"github.com/sburnett/orgtd/internal/execlog"
	"github.com/sburnett/orgtd/internal/org"
)

// syncCalendarMsg reports that a :sync-calendar run (see
// startSyncCalendar) or an automatic one (see autoSyncTick) has
// finished — result is the zero value if err is set.
type syncCalendarMsg struct {
	result calendarsync.Result
	err    error
	auto   bool
}

// autoSyncTickMsg asks autoSyncTick to check whether the calendar is due
// for an automatic sync.
type autoSyncTickMsg struct{}

const (
	// autoSyncCheckInterval is how often autoSyncTick looks at the
	// calendar's age — far finer than any configurable interval (whole
	// hours), so a sync starts soon after it's due, including soon after
	// waking from sleep, which a single long timer could badly overshoot.
	autoSyncCheckInterval = 10 * time.Minute

	// autoSyncRetryDelay is how long autoSyncTick waits after a failed
	// automatic sync (offline, not signed in yet, ...) before trying
	// again, so a lasting failure doesn't fill :log every ten minutes.
	autoSyncRetryDelay = time.Hour
)

// autoSyncEnabled reports whether automatic syncing applies at all: the
// OAuth client is configured, and auto_sync_hours hasn't turned it off.
func (m *Model) autoSyncEnabled() bool {
	return m.gcalConfigured() && m.cfg.Gcalsync.AutoSyncHours > 0
}

func (m *Model) gcalConfigured() bool {
	return m.cfg.Gcalsync.OAuthClientID != "" && m.cfg.Gcalsync.OAuthClientSecret != ""
}

// autoSyncTick runs on every autoSyncTickMsg — the first sent straight
// from Init, each later one autoSyncCheckInterval after the last — and
// starts an unattended sync if the calendar is more than
// cfg.Gcalsync.AutoSyncHours stale, then schedules the next check.
// Staleness is measured from the last successful sync this session or,
// before there's been one, from calendar_file's modification time on
// disk (so a fresh-enough file isn't resynced just because orgtd
// restarted; a missing one is synced right away).
func (m Model) autoSyncTick() (tea.Model, tea.Cmd) {
	if !m.autoSyncEnabled() {
		return m, nil
	}
	next := tea.Tick(autoSyncCheckInterval, func(time.Time) tea.Msg { return autoSyncTickMsg{} })

	// Round(0) strips the monotonic clock reading, so every comparison
	// here is by wall clock — the monotonic one may stop while the
	// machine sleeps, which would make an overnight sleep not count.
	now := m.now().Round(0)
	m.seedLastCalendarSync()
	interval := time.Duration(m.cfg.Gcalsync.AutoSyncHours) * time.Hour
	if m.syncingCalendar || now.Sub(m.lastCalendarSync) < interval || now.Before(m.autoSyncRetryAt) {
		return m, next
	}
	return m, tea.Batch(m.syncCalendarCmd(false, true), next)
}

// seedLastCalendarSync fills in lastCalendarSync, if it's still unset,
// from calendar_file's modification time on disk — the best guess at when
// it was last synced, before this session has synced it itself. Left
// unset if there's no such file.
func (m *Model) seedLastCalendarSync() {
	if !m.lastCalendarSync.IsZero() || m.ws == nil {
		return
	}
	if info, err := os.Stat(m.calendarPath()); err == nil {
		m.lastCalendarSync = info.ModTime()
	}
}

// calendarPath is where a sync writes calendar_file.
func (m *Model) calendarPath() string {
	return filepath.Join(m.ws.Dir, m.cfg.CalendarFile)
}

// startSyncCalendar (":sync-calendar", or ":sync-calendar!" with
// reauth) runs a calendarsync.Sync in the background — on its own
// goroutine, like startFormatLinks's batch, so the rest of the app stays
// fully usable while Google Calendar is off fetching events (including,
// on a first run or after ":sync-calendar!", however long the user takes
// to complete the browser consent flow). Refuses outright, with a status
// message, if no OAuth client is configured at all (see
// WithGcalOAuthClient) or a sync is already running.
func (m *Model) startSyncCalendar(reauth bool) tea.Cmd {
	if !m.gcalConfigured() {
		m.message = "Calendar sync isn't configured — see README's Calendar sync section"
		return nil
	}
	if m.syncingCalendar {
		m.message = "Calendar sync already in progress"
		return nil
	}
	m.message = "Syncing calendar in the background..."
	return m.syncCalendarCmd(reauth, false)
}

// syncCalendarCmd marks a sync in flight and returns the Cmd that runs
// it. An auto sync (see autoSyncTick) never runs the consent flow: with
// no cached sign-in it fails with calendarsync.ErrNotSignedIn instead.
func (m *Model) syncCalendarCmd(reauth, auto bool) tea.Cmd {
	m.syncingCalendar = true

	settings := calendarsync.Settings{
		OutputPath:             m.calendarPath(),
		CalendarIDs:            m.cfg.Gcalsync.CalendarIDs,
		SyncPastDays:           m.cfg.Gcalsync.SyncPastDays,
		SyncFutureDays:         m.cfg.Gcalsync.SyncFutureDays,
		OAuthClientID:          m.cfg.Gcalsync.OAuthClientID,
		OAuthClientSecret:      m.cfg.Gcalsync.OAuthClientSecret,
		AttendeeTagDomains:     m.cfg.Gcalsync.AttendeeTagDomains,
		AttendeeIgnorePatterns: m.cfg.Gcalsync.AttendeeIgnorePatterns,
		NoConsentFlow:          auto,
	}
	elog := m.execLog
	return func() tea.Msg {
		if reauth {
			if err := calendarsync.ForgetToken(); err != nil {
				return syncCalendarMsg{err: err, auto: auto}
			}
		}
		// Logged rather than printed to stderr (as the old standalone
		// gcalsync binary did before this was folded into orgtd) — the
		// TUI owns the terminal, so writing to it directly here would
		// corrupt the display. :log shows this
		// entry the moment it's logged, even though this Sync call is
		// still in flight (an execLog entry, unlike m.message, isn't
		// tied to any particular Update cycle).
		onConsentURL := func(url string) {
			elog.Append(execlog.Info, 0, "Opening browser for Google sign-in; if it doesn't open, visit: "+url)
		}
		result, err := calendarsync.Sync(context.Background(), settings, onConsentURL)
		return syncCalendarMsg{result: result, err: err, auto: auto}
	}
}

// finishSyncCalendar applies a completed :sync-calendar run: on success,
// writes the regenerated file to disk and folds it into the workspace —
// replacing the calendar file's old *org.File wholesale if one was
// already loaded (same swap finishEditFile does for a whole-file reload,
// clearRefsForFile included, since every headline pointer the old one
// held is now stale), or inserting it as a new workspace file, kept in
// path order, if this is the first sync of a session that started with
// no calendar file at all.
//
// An automatic sync reports its outcome only to :log, never the status
// line — it wasn't asked for, so it shouldn't interrupt — and a failed
// one holds off the next attempt for autoSyncRetryDelay.
func (m Model) finishSyncCalendar(msg syncCalendarMsg) (tea.Model, tea.Cmd) {
	m.syncingCalendar = false

	report := func(text string) {
		if msg.auto {
			m.execLog.Append(execlog.Info, 0, "(automatic) "+text)
		} else {
			m.message = text
		}
	}
	fail := func(text string) (tea.Model, tea.Cmd) {
		report(text)
		if !msg.auto {
			// Logged as well, so :calendar's "last attempt failed (see
			// :log)" (see calendarSyncStatus) holds for a manual sync
			// too, once its status message is gone.
			m.execLog.Append(execlog.Info, 0, text)
		}
		m.calendarSyncFailed = true
		if msg.auto {
			m.autoSyncRetryAt = m.now().Round(0).Add(autoSyncRetryDelay)
		}
		return m, nil
	}

	if errors.Is(msg.err, calendarsync.ErrNotSignedIn) {
		return fail("Calendar sync skipped — not signed in to Google yet; run :sync-calendar once to sign in")
	}
	if msg.err != nil {
		return fail(fmt.Sprintf("Calendar sync failed: %v", msg.err))
	}

	if err := org.WriteFile(msg.result.File); err != nil {
		return fail(fmt.Sprintf("Calendar sync: writing %s: %v", filepath.Base(msg.result.File.Path), err))
	}
	m.lastCalendarSync = m.now().Round(0)
	m.autoSyncRetryAt = time.Time{}
	m.calendarSyncFailed = false

	replaced := false
	for i, f := range m.ws.Files {
		if m.isNamedFile(f, m.cfg.CalendarFile) {
			m.clearRefsForFile(f)
			m.ws.Files[i] = msg.result.File
			replaced = true
			break
		}
	}
	if !replaced {
		m.ws.Files = append(m.ws.Files, msg.result.File)
		sort.Slice(m.ws.Files, func(i, j int) bool { return m.ws.Files[i].Path < m.ws.Files[j].Path })
	}

	m.rebuildRows()
	report(fmt.Sprintf("Synced %d event(s) from %d calendar(s) to %s", msg.result.EventCount, msg.result.CalendarCount, m.cfg.CalendarFile))
	return m, nil
}
