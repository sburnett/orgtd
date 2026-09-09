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

	"github.com/sburnett/orgtd/internal/config"
	"github.com/sburnett/orgtd/internal/ui"
	"github.com/sburnett/orgtd/internal/workspace"
)

func main() {
	dir := flag.String("dir", "", "directory containing org files (default: $ORGTD_DIR, then the config file's org_dir, then ~/org)")
	urlFormatter := flag.String("url-formatter", "", "external program invoked as `<prog> <url>` to convert a bare URL, found while editing an entry, into an org-mode link (its stdout replaces the URL); disabled if empty (default: the config file's url_formatter, else disabled)")
	urlFormatterPrefixes := flag.String("url-formatter-prefixes", "", "comma-separated extra bare-URL prefixes beyond http:// and https://, e.g. \"bit.ly/,go/\" (default: the config file's url_formatter_prefixes, else none)")
	agendaDays := flag.Int("agenda-days", 0, "how many days ahead the agenda view's \"Upcoming\" section covers (default: the config file's agenda_window_days, else 14)")
	inboxFile := flag.String("inbox-file", "", "base name of the file :clarify treats as the inbox (default: the config file's inbox_file, else inbox.org)")
	editor := flag.String("editor", "", "external editor command for i and file edits (default: the config file's editor, else $EDITOR, else vim)")
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
		dir:                  *dir,
		urlFormatter:         *urlFormatter,
		urlFormatterPrefixes: splitPrefixes(*urlFormatterPrefixes),
		agendaDays:           *agendaDays,
		inboxFile:            *inboxFile,
		editor:               *editor,
		explicit:             explicit,
	}, os.Getenv("ORGTD_DIR"), cfg)

	// The TUI owns the terminal once it starts, so plain log output
	// can't share it — redirect to a file instead. Discarding (rather
	// than falling back to stderr) if it can't be opened keeps that
	// guarantee even then, at the cost of losing the log entirely in
	// that rare case.
	if logFile, err := os.OpenFile(debugLogPath(path), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		defer logFile.Close()
		log.SetOutput(logFile)
	} else {
		log.SetOutput(io.Discard)
	}
	log.Printf("orgtd starting: dir=%q editor=%q url_formatter=%q url_formatter_prefixes=%v agenda_days=%d inbox_file=%q",
		s.dir, s.editor, s.urlFormatter, s.urlFormatterPrefixes, s.agendaDays, s.inboxFile)

	ws, err := workspace.Load(s.dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "orgtd: %v\n", err)
		os.Exit(1)
	}

	p := tea.NewProgram(
		ui.New(ws,
			ui.WithURLFormatter(s.urlFormatter),
			ui.WithURLFormatterPrefixes(s.urlFormatterPrefixes),
			ui.WithAgendaDays(s.agendaDays),
			ui.WithInboxFile(s.inboxFile),
			ui.WithEditor(s.editor),
		),
		tea.WithAltScreen(),
	)
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "orgtd: %v\n", err)
		os.Exit(1)
	}
}
