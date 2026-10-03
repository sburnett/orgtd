package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/sburnett/orgtd/internal/execlog"
)

// appendHelpRows appends the rows for :help: README.md's embedded
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
func (m *Model) appendHelpRows(dst *[]row) {
	if m.readme == "" {
		*dst = append(*dst, row{kind: rowText, text: "No help available."})
		return
	}
	rendered, err := renderMarkdown(m.readme, m.helpWrapWidth())
	if err != nil {
		rendered = m.readme
	}
	for _, line := range strings.Split(strings.TrimRight(rendered, "\n"), "\n") {
		*dst = append(*dst, row{kind: rowText, text: line})
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

// appendConfigRows appends the rows for config view: one read-only line
// per configurable setting, showing its effective current value (after
// flags/config-file/built-in-default resolution has already happened in
// main.go — this view has no idea which of those a value came from,
// only what it ended up as).
func (m *Model) appendConfigRows(dst *[]row) {
	line := func(format string, args ...any) {
		*dst = append(*dst, row{kind: rowText, text: fmt.Sprintf(format, args...)})
	}

	line("Org directory: %s", m.ws.Dir)

	editor := m.editorCommand()
	if editor == "" {
		editor = "vim (default)"
	}
	line("Editor: %s", editor)

	if m.cfg.URLFormatter == "" {
		line("URL formatter: (disabled)")
	} else {
		line("URL formatter: %s", m.cfg.URLFormatter)
	}
	prefixes := "(none)"
	if len(m.cfg.URLFormatterPrefixes) > 0 {
		prefixes = strings.Join(m.cfg.URLFormatterPrefixes, ", ")
	}
	line("URL formatter prefixes: %s", prefixes)

	if formatLinksCmd := m.formatLinksFormatterCmd(); formatLinksCmd == "" {
		line("Format-links URL formatter: (disabled)")
	} else if m.cfg.FormatLinksURLFormatter != "" {
		line("Format-links URL formatter: %s", formatLinksCmd)
	} else {
		line("Format-links URL formatter: %s (same as URL formatter)", formatLinksCmd)
	}

	line("Agenda window: %d days", m.cfg.AgendaWindowDays)
	line("Inbox file: %s", m.cfg.InboxFile)
	line("Calendar file: %s", m.cfg.CalendarFile)
	line("Meeting tags file: %s", m.cfg.MeetingTagsFile)
	line("Hide done after: %d hours (currently %s — :toggledone to switch)", m.cfg.HideDoneAfterHours, onOff(m.hideDoneEnabled))
	line("Debug logging: %s", onOff(m.cfg.Debug))

	if m.cfg.Gcalsync.OAuthClientID == "" || m.cfg.Gcalsync.OAuthClientSecret == "" {
		line("Calendar sync: (not configured — see README's Calendar sync section)")
	} else {
		line("Calendar sync: %s, -%dd/+%dd window", strings.Join(m.cfg.Gcalsync.CalendarIDs, ", "), m.cfg.Gcalsync.SyncPastDays, m.cfg.Gcalsync.SyncFutureDays)
		if len(m.cfg.Gcalsync.AttendeeTagDomains) > 0 {
			line("Attendee tag domains: %s", strings.Join(m.cfg.Gcalsync.AttendeeTagDomains, ", "))
		} else {
			line("Attendee tag domains: (none — every confirmed attendee is tagged)")
		}
		if len(m.cfg.Gcalsync.AttendeeIgnorePatterns) > 0 {
			line("Attendee ignore patterns: %s", strings.Join(m.cfg.Gcalsync.AttendeeIgnorePatterns, ", "))
		}
	}

	line("Gutter icons: dirty %q (%s), mark (%s), clarify %q (%s), lock %q (%s), meeting %q (%s)",
		orDefault(m.cfg.Icons.DirtyIcon, defaultDirtyIcon), orDefault(m.cfg.Icons.DirtyColor, defaultDirtyColor),
		orDefault(m.cfg.Icons.MarkColor, defaultMarkColor),
		orDefault(m.cfg.Icons.ClarifyIcon, defaultClarifyIcon), orDefault(m.cfg.Icons.ClarifyColor, defaultClarifyColor),
		orDefault(m.cfg.Icons.LockIcon, defaultLockIcon), orDefault(m.cfg.Icons.LockColor, defaultLockColor),
		orDefault(m.cfg.Icons.MeetingIcon, defaultMeetingIcon), orDefault(m.cfg.Icons.MeetingColor, defaultMeetingColor))

	line("Colors: file (%s), todo (%s), next (%s), waiting (%s), someday (%s), done (%s), cancelled (%s), tag (%s), done-title (%s), status (%s), timestamp (%s), error (%s), body (%s), caret (%s on %s), highlight (%s), panel (%s), status-bar (%s on %s), cursor-row (%s), visual-selection (%s), search-highlight (%s)",
		orDefault(m.cfg.Colors.File, defaultFileColor),
		orDefault(m.cfg.Colors.TODO, defaultTODOColor),
		orDefault(m.cfg.Colors.Next, defaultNextColor),
		orDefault(m.cfg.Colors.Waiting, defaultWaitingColor),
		orDefault(m.cfg.Colors.Someday, defaultSomedayColor),
		orDefault(m.cfg.Colors.Done, defaultDoneColor),
		orDefault(m.cfg.Colors.Cancelled, defaultCancelledColor),
		orDefault(m.cfg.Colors.Tag, defaultTagColor),
		orDefault(m.cfg.Colors.DoneTitle, defaultDoneTitleColor),
		orDefault(m.cfg.Colors.Status, defaultStatusColor),
		orDefault(m.cfg.Colors.Timestamp, defaultTimestampColor),
		orDefault(m.cfg.Colors.Error, defaultErrorColor),
		orDefault(m.cfg.Colors.Body, defaultBodyColor),
		orDefault(m.cfg.Colors.CaretFg, defaultCaretFg), orDefault(m.cfg.Colors.CaretBg, defaultCaretBg),
		orDefault(m.cfg.Colors.HighlightBg, defaultHighlightBg),
		orDefault(m.cfg.Colors.PanelBg, defaultPanelBg),
		orDefault(m.cfg.Colors.StatusBarFg, defaultStatusBarFg), orDefault(m.cfg.Colors.StatusBarBg, defaultStatusBarBg),
		orDefault(m.cfg.Colors.CursorRowBg, defaultCursorRowBg),
		orDefault(m.cfg.Colors.VisualSelectionBg, defaultVisualSelectionBg),
		orDefault(m.cfg.Colors.SearchHighlightBg, defaultSearchHighlightBg))
}

// appendLogRows appends the rows for :log — every external command
// orgtd has run since startup (see execLog), oldest first, each entry
// (a command starting — with its arguments — one line fed to its stdin,
// one of its output lines, or its exit code) stamped with its own
// timestamp, which stream it came from if applicable, and the process's
// pid ("-" if it never actually started), so entries from two commands
// that happened to run concurrently can still be told apart. A snapshot
// taken right now — if a :format-links batch (or anything else) logs
// more while this view is already open, re-run :log to see it; the view
// itself doesn't live-update.
func (m *Model) appendLogRows(dst *[]row) {
	entries := m.execLog.Snapshot()
	if len(entries) == 0 {
		*dst = append(*dst, row{kind: rowText, text: "No external commands have been run yet."})
		return
	}
	for _, e := range entries {
		*dst = append(*dst, row{kind: rowText, text: fmt.Sprintf("%s  %-6s  pid %-7s  %s", e.Time.Format("15:04:05.000"), e.Kind.Label(), execlog.PIDLabel(e.PID), e.Text)})
	}
}
