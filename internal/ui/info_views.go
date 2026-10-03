package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/sburnett/orgtd/internal/execlog"
)

// appendHelpRows populates m.rows for :help: README.md's embedded
// content (see WithReadme), rendered through glamour into ANSI-styled
// terminal markdown — headers, emphasis, code blocks, and (the original
// motivation for using glamour at all) properly aligned tables — one
// row per rendered line, same plain-text-row treatment as :log/:diff
// otherwise (no further parsing here). Falls back to the raw markdown
// if rendering itself fails (glamour has no reason to fail on our own
// known-good README, but every other external-ish dependency in this
// codebase is handled defensively too). A binary built without a
// readme wired up (WithReadme never called, e.g. a bare Model{} in a
// test) shows a placeholder instead of an empty view.
func (m *Model) appendHelpRows() {
	if m.readme == "" {
		m.rows = append(m.rows, row{kind: rowText, text: "No help available."})
		return
	}
	rendered, err := renderMarkdown(m.readme, m.helpWrapWidth())
	if err != nil {
		rendered = m.readme
	}
	for _, line := range strings.Split(strings.TrimRight(rendered, "\n"), "\n") {
		m.rows = append(m.rows, row{kind: rowText, text: line})
	}
}

// helpWrapWidth is the column width :help's markdown rendering wraps
// to: the terminal's actual width once known (see the WindowSizeMsg
// case in Update, which rebuilds help view's rows on a resize since
// they're wrapped once here rather than at render time), or a
// reasonable default before that first arrives — including in tests,
// which mostly never send one at all.
func (m *Model) helpWrapWidth() int {
	if m.width > 0 {
		return m.width
	}
	return 80
}

// renderMarkdown renders src as terminal-styled markdown via glamour,
// wrapped to width, matching the app's own light/dark and color-profile
// detection (via lipgloss, already resolved and cached from ordinary
// rendering elsewhere) so :help's colors look consistent with
// everything else rather than picking their own independently.
func renderMarkdown(src string, width int) (string, error) {
	style := "light"
	if lipgloss.HasDarkBackground() {
		style = "dark"
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle(style),
		glamour.WithColorProfile(lipgloss.ColorProfile()),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return "", err
	}
	return r.Render(src)
}

// appendConfigRows populates m.rows for config view: one read-only line
// per configurable setting, showing its effective current value (after
// flags/config-file/built-in-default resolution has already happened in
// main.go — this view has no idea which of those a value came from,
// only what it ended up as).
func (m *Model) appendConfigRows() {
	line := func(format string, args ...any) {
		m.rows = append(m.rows, row{kind: rowText, text: fmt.Sprintf(format, args...)})
	}

	line("Org directory: %s", m.ws.Dir)

	editor := m.editorCommand()
	if editor == "" {
		editor = "vim (default)"
	}
	line("Editor: %s", editor)

	if m.urlFormatterCmd == "" {
		line("URL formatter: (disabled)")
	} else {
		line("URL formatter: %s", m.urlFormatterCmd)
	}
	prefixes := "(none)"
	if len(m.urlFormatterPrefixes) > 0 {
		prefixes = strings.Join(m.urlFormatterPrefixes, ", ")
	}
	line("URL formatter prefixes: %s", prefixes)

	if formatLinksCmd := m.formatLinksFormatterCmd(); formatLinksCmd == "" {
		line("Format-links URL formatter: (disabled)")
	} else if m.formatLinksURLFormatterCmd != "" {
		line("Format-links URL formatter: %s", formatLinksCmd)
	} else {
		line("Format-links URL formatter: %s (same as URL formatter)", formatLinksCmd)
	}

	line("Agenda window: %d days", m.agendaDays)
	line("Inbox file: %s", m.inboxFile)
	line("Calendar file: %s", m.calendarFile)
	line("Meeting tags file: %s", m.meetingTagsFile)
	line("Hide done after: %d hours (currently %s — :toggledone to switch)", m.hideDoneAfterHours, onOff(m.hideDoneEnabled))
	line("Debug logging: %s", onOff(m.debug))

	if m.gcalOAuthClientID == "" || m.gcalOAuthClientSecret == "" {
		line("Calendar sync: (not configured — see README's Calendar sync section)")
	} else {
		line("Calendar sync: %s, -%dd/+%dd window", strings.Join(m.gcalCalendarIDs, ", "), m.gcalSyncPastDays, m.gcalSyncFutureDays)
		if len(m.gcalAttendeeTagDomains) > 0 {
			line("Attendee tag domains: %s", strings.Join(m.gcalAttendeeTagDomains, ", "))
		} else {
			line("Attendee tag domains: (none — every confirmed attendee is tagged)")
		}
		if len(m.gcalAttendeeIgnorePatterns) > 0 {
			line("Attendee ignore patterns: %s", strings.Join(m.gcalAttendeeIgnorePatterns, ", "))
		}
	}

	line("Gutter icons: dirty %q (%s), mark (%s), clarify %q (%s), lock %q (%s), meeting %q (%s)",
		orDefault(m.dirtyIcon, defaultDirtyIcon), orDefault(m.dirtyColor, defaultDirtyColor),
		orDefault(m.markColor, defaultMarkColor),
		orDefault(m.clarifyIcon, defaultClarifyIcon), orDefault(m.clarifyColor, defaultClarifyColor),
		orDefault(m.lockIcon, defaultLockIcon), orDefault(m.lockColor, defaultLockColor),
		orDefault(m.meetingIcon, defaultMeetingIcon), orDefault(m.meetingColor, defaultMeetingColor))

	line("Colors: file (%s), todo (%s), next (%s), waiting (%s), someday (%s), done (%s), cancelled (%s), tag (%s), done-title (%s), status (%s), timestamp (%s), error (%s), body (%s), caret (%s on %s), highlight (%s), panel (%s), status-bar (%s on %s), cursor-row (%s), visual-selection (%s), search-highlight (%s)",
		orDefault(m.colors.File, defaultFileColor),
		orDefault(m.colors.TODO, defaultTODOColor),
		orDefault(m.colors.Next, defaultNextColor),
		orDefault(m.colors.Waiting, defaultWaitingColor),
		orDefault(m.colors.Someday, defaultSomedayColor),
		orDefault(m.colors.Done, defaultDoneColor),
		orDefault(m.colors.Cancelled, defaultCancelledColor),
		orDefault(m.colors.Tag, defaultTagColor),
		orDefault(m.colors.DoneTitle, defaultDoneTitleColor),
		orDefault(m.colors.Status, defaultStatusColor),
		orDefault(m.colors.Timestamp, defaultTimestampColor),
		orDefault(m.colors.Error, defaultErrorColor),
		orDefault(m.colors.Body, defaultBodyColor),
		orDefault(m.colors.CaretFg, defaultCaretFg), orDefault(m.colors.CaretBg, defaultCaretBg),
		orDefault(m.colors.HighlightBg, defaultHighlightBg),
		orDefault(m.colors.PanelBg, defaultPanelBg),
		orDefault(m.colors.StatusBarFg, defaultStatusBarFg), orDefault(m.colors.StatusBarBg, defaultStatusBarBg),
		orDefault(m.colors.CursorRowBg, defaultCursorRowBg),
		orDefault(m.colors.VisualSelectionBg, defaultVisualSelectionBg),
		orDefault(m.colors.SearchHighlightBg, defaultSearchHighlightBg))
}

// appendLogRows populates m.rows for :log — every external command
// orgtd has run since startup (see execLog), oldest first, each entry
// (a command starting — with its arguments — one line fed to its stdin,
// one of its output lines, or its exit code) stamped with its own
// timestamp, which stream it came from if applicable, and the process's
// pid ("-" if it never actually started), so entries from two commands
// that happened to run concurrently can still be told apart. A snapshot
// taken right now — if a :format-links batch (or anything else) logs
// more while this view is already open, re-run :log to see it; the view
// itself doesn't live-update.
func (m *Model) appendLogRows() {
	entries := m.execLog.Snapshot()
	if len(entries) == 0 {
		m.rows = append(m.rows, row{kind: rowText, text: "No external commands have been run yet."})
		return
	}
	for _, e := range entries {
		m.rows = append(m.rows, row{kind: rowText, text: fmt.Sprintf("%s  %-6s  pid %-7s  %s", e.Time.Format("15:04:05.000"), e.Kind.Label(), execlog.PIDLabel(e.PID), e.Text)})
	}
}
