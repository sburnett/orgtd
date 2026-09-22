package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/sburnett/orgtd/internal/config"
)

// settings is the fully-resolved set of values main passes to the UI,
// after merging command-line flags, $ORGTD_DIR, the config file, and
// built-in defaults per resolveSettings' precedence rules.
type settings struct {
	dir                     string
	urlFormatter            string
	urlFormatterPrefixes    []string
	formatLinksURLFormatter string
	agendaDays              int
	inboxFile               string
	calendarFile            string
	meetingTagsFile         string
	hideDoneAfterHours      int
	editor                  string
	debug                   bool

	// gcal* back :sync-calendar (see internal/ui and
	// internal/calendarsync) — config-file only, unlike everything else
	// above: there's no command-line flag for any of them, since
	// :sync-calendar is only ever triggered interactively from inside
	// the TUI, never scripted. Resolved straight from the config file's
	// [gcalsync] section, falling back to the same built-in defaults the
	// old standalone gcalsync binary used.
	gcalOAuthClientID, gcalOAuthClientSecret string
	gcalCalendarIDs                          []string
	gcalSyncPastDays, gcalSyncFutureDays     int
	gcalAttendeeTagDomains                   []string
	gcalAttendeeIgnorePatterns               []string

	// icon* customize the outline's gutter markers (see internal/ui) —
	// config-file only, like the gcal* fields above: there's no
	// command-line flag for any of them. Resolved straight from the
	// config file's [icons] section; an empty string leaves internal/ui's
	// own built-in default in place (see ui.WithDirtyIcon and its
	// siblings), so this struct doesn't need its own defaults.
	iconDirtyIcon, iconDirtyColor     string
	iconMarkColor                     string
	iconClarifyIcon, iconClarifyColor string
	iconLockIcon, iconLockColor       string
	iconMeetingIcon, iconMeetingColor string

	// colors is the rest of orgtd's color scheme (see internal/ui's
	// ColorOverrides, which this converts directly to — see main.go) —
	// config-file only, like the icon* fields above. Resolved straight
	// from the config file's [colors] section; an empty field leaves
	// internal/ui's own built-in wildcharm-dark default in place.
	colors config.ColorsConfig
}

// flagValues is the raw output of flag parsing: each flag's value
// (whatever it holds, default or user-supplied) plus which flags the
// user actually passed on the command line (via flag.Visit) — needed to
// tell "the user explicitly asked for this" apart from "this just
// happens to equal the flag's zero-value default", which resolveSettings
// must treat differently (an explicit flag always wins; an
// unset-and-still-zero one falls through to the config file).
type flagValues struct {
	dir, urlFormatter, inboxFile, calendarFile, meetingTagsFile, editor string
	formatLinksURLFormatter                                             string
	urlFormatterPrefixes                                                []string
	agendaDays                                                          int
	hideDoneAfterHours                                                  int
	debug                                                               bool
	explicit                                                            map[string]bool
}

// resolveSettings merges f, $ORGTD_DIR (orgtdDirEnv), and cfg into the
// final settings, in precedence order:
//
//  1. An explicitly-passed flag always wins.
//  2. For -dir specifically, $ORGTD_DIR comes next (it predates the
//     config file as this tool's override mechanism, and is meant for
//     ad hoc, per-shell-session overrides, which should beat a
//     persistent config file).
//  3. The config file's value, if set.
//  4. defaultOrgDir() for -dir; the flags' own zero-value defaults
//     (disabled/14/"inbox.org"/"", meaning $EDITOR) for everything else.
//
// The gcal* fields (see settings, above) sit outside this precedence
// entirely — there's no flag for them, so they're always just the config
// file's [gcalsync] values, falling back to their own built-in defaults.
func resolveSettings(f flagValues, orgtdDirEnv string, cfg *config.Config) settings {
	s := settings{
		dir:                     f.dir,
		urlFormatter:            f.urlFormatter,
		urlFormatterPrefixes:    f.urlFormatterPrefixes,
		formatLinksURLFormatter: f.formatLinksURLFormatter,
		agendaDays:              f.agendaDays,
		inboxFile:               f.inboxFile,
		calendarFile:            f.calendarFile,
		meetingTagsFile:         f.meetingTagsFile,
		hideDoneAfterHours:      f.hideDoneAfterHours,
		editor:                  f.editor,
		debug:                   f.debug,
	}

	switch {
	case f.explicit["dir"]:
	case orgtdDirEnv != "":
		s.dir = orgtdDirEnv
	case cfg.OrgDir != "":
		s.dir = cfg.OrgDir
	default:
		s.dir = defaultOrgDir()
	}

	if !f.explicit["url-formatter"] && cfg.URLFormatter != "" {
		s.urlFormatter = cfg.URLFormatter
	}
	if !f.explicit["url-formatter-prefixes"] && len(cfg.URLFormatterPrefixes) > 0 {
		s.urlFormatterPrefixes = cfg.URLFormatterPrefixes
	}
	if !f.explicit["format-links-url-formatter"] && cfg.FormatLinksURLFormatter != "" {
		s.formatLinksURLFormatter = cfg.FormatLinksURLFormatter
	}
	if !f.explicit["agenda-days"] && cfg.AgendaWindowDays != 0 {
		s.agendaDays = cfg.AgendaWindowDays
	}
	if !f.explicit["inbox-file"] && cfg.InboxFile != "" {
		s.inboxFile = cfg.InboxFile
	}
	if !f.explicit["calendar-file"] && cfg.CalendarFile != "" {
		s.calendarFile = cfg.CalendarFile
	}
	if !f.explicit["meeting-tags-file"] && cfg.MeetingTagsFile != "" {
		s.meetingTagsFile = cfg.MeetingTagsFile
	}
	if !f.explicit["hide-done-after-hours"] && cfg.HideDoneAfterHours != 0 {
		s.hideDoneAfterHours = cfg.HideDoneAfterHours
	}
	if !f.explicit["editor"] && cfg.Editor != "" {
		s.editor = cfg.Editor
	}
	if !f.explicit["debug"] && cfg.Debug {
		s.debug = true
	}

	s.gcalOAuthClientID = cfg.Gcalsync.OAuthClientID
	s.gcalOAuthClientSecret = cfg.Gcalsync.OAuthClientSecret
	if len(cfg.Gcalsync.CalendarIDs) > 0 {
		s.gcalCalendarIDs = cfg.Gcalsync.CalendarIDs
	} else {
		s.gcalCalendarIDs = []string{"primary"}
	}
	if cfg.Gcalsync.SyncPastDays != 0 {
		s.gcalSyncPastDays = cfg.Gcalsync.SyncPastDays
	} else {
		s.gcalSyncPastDays = 1
	}
	if cfg.Gcalsync.SyncFutureDays != 0 {
		s.gcalSyncFutureDays = cfg.Gcalsync.SyncFutureDays
	} else {
		s.gcalSyncFutureDays = 14
	}
	s.gcalAttendeeTagDomains = cfg.Gcalsync.AttendeeTagDomains
	s.gcalAttendeeIgnorePatterns = cfg.Gcalsync.AttendeeIgnorePatterns

	s.iconDirtyIcon = cfg.Icons.DirtyIcon
	s.iconDirtyColor = cfg.Icons.DirtyColor
	s.iconMarkColor = cfg.Icons.MarkColor
	s.iconClarifyIcon = cfg.Icons.ClarifyIcon
	s.iconClarifyColor = cfg.Icons.ClarifyColor
	s.iconLockIcon = cfg.Icons.LockIcon
	s.iconLockColor = cfg.Icons.LockColor
	s.iconMeetingIcon = cfg.Icons.MeetingIcon
	s.iconMeetingColor = cfg.Icons.MeetingColor

	s.colors = cfg.Colors

	return s
}

// defaultOrgDir is the last-resort fallback used when nothing more
// specific (the -dir flag, $ORGTD_DIR, or the config file's org_dir)
// supplies one.
func defaultOrgDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "org"
	}
	return filepath.Join(home, "org")
}

// debugLogPath returns the log file main.go directs the standard log
// package's output to when debug logging is on (see settings.debug): a
// debug.log next to whichever config file was actually loaded (or would
// be, if none exists yet — configFilePath is the resolved path
// regardless), so it's discoverable without a separate setting to
// remember. Existing logging (e.g. every URL formatter attempt, success
// or failure, including the subprocess's own stderr) lands there —
// useful for exactly the "this works on one machine but not another"
// question a status-line message alone can't answer, since the TUI's
// own screen can't share a terminal with plain log output.
func debugLogPath(configFilePath string) string {
	return filepath.Join(filepath.Dir(configFilePath), "debug.log")
}

// splitPrefixes parses the -url-formatter-prefixes flag's comma-separated
// value ("bit.ly/,go/") into a slice, trimming whitespace around each
// entry and dropping empty ones (so a trailing comma, or the flag simply
// being unset, yields nil rather than a slice with a blank entry).
func splitPrefixes(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
