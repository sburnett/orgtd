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
	dir                  string
	urlFormatter         string
	urlFormatterPrefixes []string
	agendaDays           int
	inboxFile            string
	hideDoneAfterHours   int
	editor               string
	debug                bool
}

// flagValues is the raw output of flag parsing: each flag's value
// (whatever it holds, default or user-supplied) plus which flags the
// user actually passed on the command line (via flag.Visit) — needed to
// tell "the user explicitly asked for this" apart from "this just
// happens to equal the flag's zero-value default", which resolveSettings
// must treat differently (an explicit flag always wins; an
// unset-and-still-zero one falls through to the config file).
type flagValues struct {
	dir, urlFormatter, inboxFile, editor string
	urlFormatterPrefixes                 []string
	agendaDays                           int
	hideDoneAfterHours                   int
	debug                                bool
	explicit                             map[string]bool
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
func resolveSettings(f flagValues, orgtdDirEnv string, cfg *config.Config) settings {
	s := settings{
		dir:                  f.dir,
		urlFormatter:         f.urlFormatter,
		urlFormatterPrefixes: f.urlFormatterPrefixes,
		agendaDays:           f.agendaDays,
		inboxFile:            f.inboxFile,
		hideDoneAfterHours:   f.hideDoneAfterHours,
		editor:               f.editor,
		debug:                f.debug,
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
	if !f.explicit["agenda-days"] && cfg.AgendaWindowDays != 0 {
		s.agendaDays = cfg.AgendaWindowDays
	}
	if !f.explicit["inbox-file"] && cfg.InboxFile != "" {
		s.inboxFile = cfg.InboxFile
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
