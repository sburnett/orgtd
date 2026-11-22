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
	formatLinksURLFormatter, scratchDir, mainBranch, mergeTool          string
	urlFormatterPrefixes                                                string // comma-separated; see splitList
	outlineIgnoreFiles                                                  string // comma-separated; see splitList
	agendaDays                                                          int
	snippetsDays                                                        int
	hideClosedAfterHours                                                int
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
	fs.IntVar(&f.snippetsDays, "snippets-days", 0, "how many days, today included, the :snippets view looks back over (default: the config file's snippets_window_days, else 7)")
	fs.IntVar(&f.agendaDays, "agenda-days", 0, "how many days ahead the agenda view's \"Upcoming\" section covers (default: the config file's agenda_window_days, else 14)")
	fs.StringVar(&f.inboxFile, "inbox-file", "", "base name of the file :review treats as the inbox (default: the config file's inbox_file, else inbox.org)")
	fs.StringVar(&f.calendarFile, "calendar-file", "", "base name of the file (e.g. the one :sync-calendar writes) excluded from the outline view and shown instead, grouped by day, in the :calendar view (default: the config file's calendar_file, else calendar.org)")
	fs.StringVar(&f.meetingTagsFile, "meeting-tags-file", "", "base name of the file holding durable meeting tags, excluded from the outline view and shown instead in the :meeting-tags view (default: the config file's meeting_tags_file, else meeting-tags.org)")
	fs.StringVar(&f.outlineIgnoreFiles, "outline-ignore-files", "", "comma-separated globs of base file names orgtd never loads, e.g. \"archive*.org,old.org\" (default: the config file's outline_ignore_files, else none)")
	fs.StringVar(&f.scratchDir, "scratch-dir", "", "directory editor buffers are written to and kept in; a leading ~ is expanded, and a relative path is relative to the org directory (default: the config file's scratch_dir, else scratch)")
	fs.IntVar(&f.hideClosedAfterHours, "hide-closed-after-hours", 0, "how many hours after an item's CLOSED timestamp it's hidden from the outline view; :toggleclosed shows everything again (default: the config file's hide_closed_after_hours, else 24)")
	fs.StringVar(&f.mainBranch, "main-branch", "", "git branch every save commits to; the org directory must have it checked out (default: the config file's main_branch, else main)")
	fs.StringVar(&f.mergeTool, "merge-tool", "", "command :resolve runs on files a save couldn't merge, with their paths appended (default: the config file's merge_tool, else the editor)")
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

// resolveConfig is resolveLayers with no repo-level config file, for
// callers that only care about the result.
func resolveConfig(f flagValues, orgtdDirEnv string, cfg *config.Config) config.Config {
	out, _ := resolveLayers(f, orgtdDirEnv, cfg, nil)
	return out
}

// resolveOrgDir picks the org directory, which has to be known before
// the rest of the settings can be resolved, since the repo-level config
// file (config.LoadRepo) lives inside it: an explicit -dir, then
// $ORGTD_DIR (it predates the config file as this tool's override
// mechanism, and is meant for ad hoc, per-shell-session overrides, which
// should beat a persistent config file), then the user config file's
// org_dir, then defaultOrgDir().
func resolveOrgDir(f flagValues, orgtdDirEnv string, user *config.Config) (dir, layer string) {
	switch {
	case f.explicit["dir"]:
		return f.dir, config.LayerFlag
	case orgtdDirEnv != "":
		return orgtdDirEnv, config.LayerEnv
	case user.OrgDir != "":
		return user.OrgDir, config.LayerUser
	default:
		return defaultOrgDir(), config.LayerDefault
	}
}

// resolveLayers merges the command line (f), $ORGTD_DIR (orgtdDirEnv),
// the user's config file (user), the org directory's own committed
// .orgtd.toml (repo, may be nil) and the built-in defaults into the one
// fully resolved Config the rest of the program uses, in precedence
// order:
//
//  1. An explicitly-passed flag always wins.
//  2. For -dir specifically, $ORGTD_DIR comes next (see resolveOrgDir).
//  3. The user config file's value, if set.
//  4. The repo file's value, if set (config.LoadRepo has already dropped
//     the machine-local keys it may not set).
//  5. The built-in default (config.ApplyDefaults); defaultOrgDir() for
//     the org directory.
//
// The settings that have no flag (the gcalsync, icons and colors
// sections) are simply the config files', plus defaults. Neither input
// is modified. The returned Sources says which of those layers each
// setting came from, for :config.
func resolveLayers(f flagValues, orgtdDirEnv string, user, repo *config.Config) (config.Config, config.Sources) {
	var out config.Config
	src := config.Sources{}
	config.Overlay(&out, repo, config.LayerRepo, src)
	config.Overlay(&out, user, config.LayerUser, src)
	out.OrgDir, src["org_dir"] = resolveOrgDir(f, orgtdDirEnv, user)

	set := func(name, key string, apply func()) {
		if f.explicit[name] {
			apply()
			src[key] = config.LayerFlag
		}
	}
	set("url-formatter", "url_formatter", func() { out.URLFormatter = f.urlFormatter })
	set("url-formatter-prefixes", "url_formatter_prefixes", func() { out.URLFormatterPrefixes = splitList(f.urlFormatterPrefixes) })
	set("format-links-url-formatter", "format_links_url_formatter", func() { out.FormatLinksURLFormatter = f.formatLinksURLFormatter })
	set("agenda-days", "agenda_window_days", func() { out.AgendaWindowDays = f.agendaDays })
	set("snippets-days", "snippets_window_days", func() { out.SnippetsWindowDays = f.snippetsDays })
	set("inbox-file", "inbox_file", func() { out.InboxFile = f.inboxFile })
	set("calendar-file", "calendar_file", func() { out.CalendarFile = f.calendarFile })
	set("meeting-tags-file", "meeting_tags_file", func() { out.MeetingTagsFile = f.meetingTagsFile })
	set("outline-ignore-files", "outline_ignore_files", func() { out.OutlineIgnoreFiles = splitList(f.outlineIgnoreFiles) })
	set("scratch-dir", "scratch_dir", func() { out.ScratchDir = f.scratchDir })
	set("hide-closed-after-hours", "hide_closed_after_hours", func() { out.HideClosedAfterHours = f.hideClosedAfterHours })
	set("main-branch", "main_branch", func() { out.MainBranch = f.mainBranch })
	set("merge-tool", "merge_tool", func() { out.MergeTool = f.mergeTool })
	set("editor", "editor", func() { out.Editor = f.editor })
	set("debug", "debug", func() { out.Debug = f.debug })

	// An explicit zero flag (e.g. -agenda-days 0) means "use the
	// default", so a flag-set key that ApplyDefaults then changes is
	// reported as the default's.
	before := out
	out.ApplyDefaults()
	for key, layer := range src {
		if layer == config.LayerFlag && !sameKey(before, out, key) {
			delete(src, key)
		}
	}
	config.MarkDefaults(&out, src)
	return out, src
}

// sameKey reports whether a and b agree on the top-level setting key.
func sameKey(a, b config.Config, key string) bool {
	switch key {
	case "agenda_window_days":
		return a.AgendaWindowDays == b.AgendaWindowDays
	case "snippets_window_days":
		return a.SnippetsWindowDays == b.SnippetsWindowDays
	case "inbox_file":
		return a.InboxFile == b.InboxFile
	case "calendar_file":
		return a.CalendarFile == b.CalendarFile
	case "meeting_tags_file":
		return a.MeetingTagsFile == b.MeetingTagsFile
	case "scratch_dir":
		return a.ScratchDir == b.ScratchDir
	case "hide_closed_after_hours":
		return a.HideClosedAfterHours == b.HideClosedAfterHours
	case "main_branch":
		return a.MainBranch == b.MainBranch
	}
	return true
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

// splitList parses a comma-separated flag's value ("bit.ly/,go/") into a
// slice, trimming whitespace around each entry and dropping empty ones (so
// a trailing comma, or the flag simply being unset, yields nil rather than
// a slice with a blank entry).
func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
