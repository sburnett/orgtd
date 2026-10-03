// Command orgtd is a terminal GTD workflow tool backed by org-mode files.
// This package is wiring only: it resolves flags, $ORGTD_DIR and the config
// file into one config.Config (see settings.go), takes the directory lock,
// loads the workspace, and hands everything to internal/ui. See DESIGN.md §3.
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
	flags := defineFlags(flag.CommandLine)
	flag.Parse()
	flags.recordExplicit(flag.CommandLine)

	path := flags.configPath
	if path == "" {
		path = config.DefaultPath()
	}
	fileCfg, err := config.Load(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "orgtd: %v\n", err)
		os.Exit(1)
	}
	cfg := resolveConfig(*flags, os.Getenv("ORGTD_DIR"), fileCfg)

	// The TUI owns the terminal once it starts, so plain log output
	// can't share it — redirect to a file instead of leaving it on
	// stderr. Off entirely unless debug logging is on (see Config.Debug):
	// most runs have nothing worth logging, and the file would otherwise
	// grow forever with no way to know it's there. Discarding (rather
	// than falling back to stderr) if the file can't be opened keeps the
	// "never touches the TUI's terminal" guarantee even then, at the cost
	// of losing the log entirely in that rare case.
	if cfg.Debug {
		if logFile, err := os.OpenFile(debugLogPath(path), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			defer logFile.Close()
			log.SetOutput(logFile)
		} else {
			log.SetOutput(io.Discard)
		}
		// Deliberately not the whole config: it holds the Google OAuth
		// client secret.
		log.Printf("orgtd starting: dir=%q editor=%q url_formatter=%q url_formatter_prefixes=%v format_links_url_formatter=%q agenda_days=%d inbox_file=%q calendar_file=%q meeting_tags_file=%q hide_done_after_hours=%d",
			cfg.OrgDir, cfg.Editor, cfg.URLFormatter, cfg.URLFormatterPrefixes, cfg.FormatLinksURLFormatter, cfg.AgendaWindowDays, cfg.InboxFile, cfg.CalendarFile, cfg.MeetingTagsFile, cfg.HideDoneAfterHours)
	} else {
		log.SetOutput(io.Discard)
	}

	// Acquired before Load so two instances pointed at the same
	// directory can't both load it into memory and race each other to
	// :w the same files — see workspace.AcquireLock. Released
	// automatically by the OS on exit either way, crash included, so
	// there's no cleanup to defer for correctness; the explicit Release
	// just gives it up a little sooner than process exit would.
	lock, ok, err := workspace.AcquireLock(cfg.OrgDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "orgtd: %v\n", err)
		os.Exit(1)
	}
	if !ok {
		fmt.Fprintf(os.Stderr, "orgtd: another orgtd instance already has %s open\n", cfg.OrgDir)
		os.Exit(1)
	}
	defer lock.Release()

	ws, err := workspace.Load(cfg.OrgDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "orgtd: %v\n", err)
		os.Exit(1)
	}

	p := tea.NewProgram(ui.New(ws, ui.WithConfig(cfg), ui.WithReadme(orgtd.Readme)), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "orgtd: %v\n", err)
		os.Exit(1)
	}
}
