package main

import (
	"flag"
	"os"
	"path/filepath"
	"strings"

	"github.com/sburnett/orgtd/internal/config"
)

// flagValues is the raw output of flag parsing: each flag's value
// (whatever it holds, default or user-supplied) plus which flags the
// user actually passed on the command line (see recordExplicit) — needed
// to tell "the user explicitly asked for this" apart from "this just
// happens to equal the flag's zero-value default", which resolveConfig
// must treat differently (an explicit flag always wins; an unset one
// leaves whatever the config file said).
type flagValues struct {
	dir, urlFormatter, inboxFile, calendarFile, meetingTagsFile, editor string
	formatLinksURLFormatter                                             string
	urlFormatterPrefixes                                                string // comma-separated; see splitPrefixes
	agendaDays                                                          int
	hideDoneAfterHours                                                  int
	debug                                                               bool
	configPath                                                          string
	explicit                                                            map[string]bool
}

// defineFlags registers every command-line flag on fs, bound to the
// returned flagValues. Call recordExplicit after fs.Parse.
func defineFlags(fs *flag.FlagSet) *flagValues {
	f := &flagValues{explicit: map[string]bool{}}
	fs.StringVar(&f.dir, "dir", "", "directory containing org files (default: $ORGTD_DIR, then the config file's org_dir, then ~/org)")
	fs.StringVar(&f.urlFormatter, "url-formatter", "", "external program invoked as `<prog> <url>` to convert a bare URL, found while editing an entry, into an org-mode link (its stdout replaces the URL); disabled if empty (default: the config file's url_formatter, else disabled)")
	fs.StringVar(&f.urlFormatterPrefixes, "url-formatter-prefixes", "", "comma-separated extra bare-URL prefixes beyond http:// and https://, e.g. \"bit.ly/,go/\" (default: the config file's url_formatter_prefixes, else none)")
	fs.StringVar(&f.formatLinksURLFormatter, "format-links-url-formatter", "", "external program :format-links invokes in batch mode (no url argument; reads urls one per line from stdin, prints the same number of formatted lines to stdout) (default: the config file's format_links_url_formatter, else the same as -url-formatter)")
	fs.IntVar(&f.agendaDays, "agenda-days", 0, "how many days ahead the agenda view's \"Upcoming\" section covers (default: the config file's agenda_window_days, else 14)")
	fs.StringVar(&f.inboxFile, "inbox-file", "", "base name of the file :review treats as the inbox (default: the config file's inbox_file, else inbox.org)")
	fs.StringVar(&f.calendarFile, "calendar-file", "", "base name of the file (e.g. the one :sync-calendar writes) excluded from the outline view and shown instead, grouped by day, in the :calendar view (default: the config file's calendar_file, else calendar.org)")
	fs.StringVar(&f.meetingTagsFile, "meeting-tags-file", "", "base name of the file holding durable meeting tags, excluded from the outline view and shown instead in the :meeting-tags view (default: the config file's meeting_tags_file, else meeting-tags.org)")
	fs.IntVar(&f.hideDoneAfterHours, "hide-done-after-hours", 0, "how many hours after a DONE/CANCELLED item's CLOSED timestamp it's hidden from the outline view; :toggledone shows everything again (default: the config file's hide_done_after_hours, else 24)")
	fs.StringVar(&f.editor, "editor", "", "external editor command for i and file edits (default: the config file's editor, else $EDITOR, else vim)")
	fs.BoolVar(&f.debug, "debug", false, "log debug info (URL formatter attempts/failures, etc.) to debug.log next to the config file; off by default (default: the config file's debug, else off)")
	fs.StringVar(&f.configPath, "config", "", "path to the TOML config file (default: "+config.DefaultPath()+")")
	return f
}

// recordExplicit notes which flags were actually passed on the command
// line, after fs.Parse.
func (f *flagValues) recordExplicit(fs *flag.FlagSet) {
	fs.Visit(func(fl *flag.Flag) { f.explicit[fl.Name] = true })
}

// resolveConfig merges the command line (f), $ORGTD_DIR (orgtdDirEnv),
// the config file (cfg) and the built-in defaults into the one fully
// resolved Config the rest of the program uses, in precedence order:
//
//  1. An explicitly-passed flag always wins.
//  2. For -dir specifically, $ORGTD_DIR comes next (it predates the
//     config file as this tool's override mechanism, and is meant for
//     ad hoc, per-shell-session overrides, which should beat a
//     persistent config file).
//  3. The config file's value, if set.
//  4. The built-in default (config.ApplyDefaults); defaultOrgDir() for
//     the org directory.
//
// The settings that have no flag (the gcalsync, icons and colors
// sections) are simply the config file's, plus defaults. cfg itself is
// not modified.
func resolveConfig(f flagValues, orgtdDirEnv string, cfg *config.Config) config.Config {
	out := *cfg

	switch {
	case f.explicit["dir"]:
		out.OrgDir = f.dir
	case orgtdDirEnv != "":
		out.OrgDir = orgtdDirEnv
	case out.OrgDir == "":
		out.OrgDir = defaultOrgDir()
	}

	if f.explicit["url-formatter"] {
		out.URLFormatter = f.urlFormatter
	}
	if f.explicit["url-formatter-prefixes"] {
		out.URLFormatterPrefixes = splitPrefixes(f.urlFormatterPrefixes)
	}
	if f.explicit["format-links-url-formatter"] {
		out.FormatLinksURLFormatter = f.formatLinksURLFormatter
	}
	if f.explicit["agenda-days"] {
		out.AgendaWindowDays = f.agendaDays
	}
	if f.explicit["inbox-file"] {
		out.InboxFile = f.inboxFile
	}
	if f.explicit["calendar-file"] {
		out.CalendarFile = f.calendarFile
	}
	if f.explicit["meeting-tags-file"] {
		out.MeetingTagsFile = f.meetingTagsFile
	}
	if f.explicit["hide-done-after-hours"] {
		out.HideDoneAfterHours = f.hideDoneAfterHours
	}
	if f.explicit["editor"] {
		out.Editor = f.editor
	}
	if f.explicit["debug"] {
		out.Debug = f.debug
	}

	out.ApplyDefaults()
	return out
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
