package ui

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sburnett/orgtd/internal/calendarsync"
	"github.com/sburnett/orgtd/internal/org"
)

// syncCalendarMsg reports that a :sync-calendar run (see
// startSyncCalendar) has finished — result is the zero value if err is
// set.
type syncCalendarMsg struct {
	result calendarsync.Result
	err    error
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
	if m.gcalOAuthClientID == "" || m.gcalOAuthClientSecret == "" {
		m.message = "Calendar sync isn't configured — see README's Calendar sync section"
		return nil
	}
	if m.syncingCalendar {
		m.message = "Calendar sync already in progress"
		return nil
	}
	m.syncingCalendar = true
	m.message = "Syncing calendar in the background..."

	settings := calendarsync.Settings{
		OutputPath:        filepath.Join(m.ws.Dir, m.calendarFile),
		CalendarIDs:       m.gcalCalendarIDs,
		SyncPastDays:      m.gcalSyncPastDays,
		SyncFutureDays:    m.gcalSyncFutureDays,
		OAuthClientID:     m.gcalOAuthClientID,
		OAuthClientSecret: m.gcalOAuthClientSecret,
	}
	elog := m.execLog
	return func() tea.Msg {
		if reauth {
			if err := calendarsync.ForgetToken(); err != nil {
				return syncCalendarMsg{err: err}
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
			elog.append(execLogInfo, 0, "Opening browser for Google sign-in; if it doesn't open, visit: "+url)
		}
		result, err := calendarsync.Sync(context.Background(), settings, onConsentURL)
		return syncCalendarMsg{result: result, err: err}
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
func (m Model) finishSyncCalendar(msg syncCalendarMsg) (tea.Model, tea.Cmd) {
	m.syncingCalendar = false

	if msg.err != nil {
		m.message = fmt.Sprintf("Calendar sync failed: %v", msg.err)
		return m, nil
	}

	if err := org.WriteFile(msg.result.File); err != nil {
		m.message = fmt.Sprintf("Calendar sync: writing %s: %v", filepath.Base(msg.result.File.Path), err)
		return m, nil
	}

	replaced := false
	for i, f := range m.ws.Files {
		if filepath.Base(f.Path) == m.calendarFile {
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
	m.message = fmt.Sprintf("Synced %d event(s) from %d calendar(s) to %s", msg.result.EventCount, msg.result.CalendarCount, m.calendarFile)
	return m, nil
}
