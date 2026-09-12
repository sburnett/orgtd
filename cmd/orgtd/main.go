// Command orgtd is a terminal GTD workflow tool backed by org-mode files.
// This initial version only loads, renders, and lets you navigate the org
// files in a directory.
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	orgtd "github.com/sburnett/orgtd"
	"github.com/sburnett/orgtd/internal/config"
	"github.com/sburnett/orgtd/internal/ui"
	"github.com/sburnett/orgtd/internal/workspace"
)

func main() {
	dir := flag.String("dir", "", "directory containing org files (default: $ORGTD_DIR, then the config file's org_dir, then ~/org)")
	urlFormatter := flag.String("url-formatter", "", "external program invoked as `<prog> <url>` to convert a bare URL, found while editing an entry, into an org-mode link (its stdout replaces the URL); disabled if empty (default: the config file's url_formatter, else disabled)")
	urlFormatterPrefixes := flag.String("url-formatter-prefixes", "", "comma-separated extra bare-URL prefixes beyond http:// and https://, e.g. \"bit.ly/,go/\" (default: the config file's url_formatter_prefixes, else none)")
	formatLinksURLFormatter := flag.String("format-links-url-formatter", "", "external program :format-links invokes in batch mode (no url argument; reads urls one per line from stdin, prints the same number of formatted lines to stdout) (default: the config file's format_links_url_formatter, else the same as -url-formatter)")
	agendaDays := flag.Int("agenda-days", 0, "how many days ahead the agenda view's \"Upcoming\" section covers (default: the config file's agenda_window_days, else 14)")
	inboxFile := flag.String("inbox-file", "", "base name of the file :clarify treats as the inbox (default: the config file's inbox_file, else inbox.org)")
	hideDoneAfterHours := flag.Int("hide-done-after-hours", 0, "how many hours after a DONE/CANCELLED item's CLOSED timestamp it's hidden from the outline view; :toggledone shows everything again (default: the config file's hide_done_after_hours, else 24)")
	editor := flag.String("editor", "", "external editor command for i and file edits (default: the config file's editor, else $EDITOR, else vim)")
	debug := flag.Bool("debug", false, "log debug info (URL formatter attempts/failures, etc.) to debug.log next to the config file; off by default (default: the config file's debug, else off)")
	configPath := flag.String("config", "", "path to the TOML config file (default: "+config.DefaultPath()+")")
	flag.Parse()

	explicit := make(map[string]bool)
	flag.Visit(func(f *flag.Flag) { explicit[f.Name] = true })

	path := *configPath
	if path == "" {
		path = config.DefaultPath()
	}
	cfg, err := config.Load(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "orgtd: %v\n", err)
		os.Exit(1)
	}

	s := resolveSettings(flagValues{
		dir:                     *dir,
		urlFormatter:            *urlFormatter,
		urlFormatterPrefixes:    splitPrefixes(*urlFormatterPrefixes),
		formatLinksURLFormatter: *formatLinksURLFormatter,
		agendaDays:              *agendaDays,
		inboxFile:               *inboxFile,
		hideDoneAfterHours:      *hideDoneAfterHours,
		editor:                  *editor,
		debug:                   *debug,
		explicit:                explicit,
	}, os.Getenv("ORGTD_DIR"), cfg)

	// The TUI owns the terminal once it starts, so plain log output
	// can't share it — redirect to a file instead of leaving it on
	// stderr. Off entirely unless debug logging is on (see settings.debug):
	// most runs have nothing worth logging, and the file would otherwise
	// grow forever with no way to know it's there. Discarding (rather
	// than falling back to stderr) if the file can't be opened keeps the
	// "never touches the TUI's terminal" guarantee even then, at the cost
	// of losing the log entirely in that rare case.
	if s.debug {
		if logFile, err := os.OpenFile(debugLogPath(path), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			defer logFile.Close()
			log.SetOutput(logFile)
		} else {
			log.SetOutput(io.Discard)
		}
		log.Printf("orgtd starting: dir=%q editor=%q url_formatter=%q url_formatter_prefixes=%v format_links_url_formatter=%q agenda_days=%d inbox_file=%q hide_done_after_hours=%d",
			s.dir, s.editor, s.urlFormatter, s.urlFormatterPrefixes, s.formatLinksURLFormatter, s.agendaDays, s.inboxFile, s.hideDoneAfterHours)
	} else {
		log.SetOutput(io.Discard)
	}

	ws, err := workspace.Load(s.dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "orgtd: %v\n", err)
		os.Exit(1)
	}

	p := tea.NewProgram(
		ui.New(ws,
			ui.WithURLFormatter(s.urlFormatter),
			ui.WithURLFormatterPrefixes(s.urlFormatterPrefixes),
			ui.WithFormatLinksURLFormatter(s.formatLinksURLFormatter),
			ui.WithAgendaDays(s.agendaDays),
			ui.WithInboxFile(s.inboxFile),
			ui.WithHideDoneAfterHours(s.hideDoneAfterHours),
			ui.WithEditor(s.editor),
			ui.WithDebug(s.debug),
			ui.WithReadme(orgtd.Readme),
		),
		tea.WithAltScreen(),
	)
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "orgtd: %v\n", err)
		os.Exit(1)
	}
}
