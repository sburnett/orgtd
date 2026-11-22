// Command orgtd is a terminal GTD workflow tool backed by org-mode files.
// This package is wiring only: it resolves flags, $ORGTD_DIR and the config
// file into one config.Config (see settings.go), opens the org
// directory's git repository (internal/gitstore), loads the workspace, and hands everything to internal/ui. See DESIGN.md §3.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	orgtd "github.com/sburnett/orgtd"
	"github.com/sburnett/orgtd/internal/config"
	"github.com/sburnett/orgtd/internal/gitstore"
	"github.com/sburnett/orgtd/internal/org"
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
	orgDir, _ := resolveOrgDir(*flags, os.Getenv("ORGTD_DIR"), fileCfg)
	repoCfg, warnings, err := config.LoadRepo(orgDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "orgtd: %v\n", err)
		os.Exit(1)
	}
	cfg, sources := resolveLayers(*flags, os.Getenv("ORGTD_DIR"), fileCfg, repoCfg)
	// Both config files were already checked as they loaded; this
	// catches the same mistakes in a command-line flag.
	if err := cfg.CheckFileNames(); err != nil {
		fmt.Fprintf(os.Stderr, "orgtd: %v\n", err)
		os.Exit(1)
	}

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
		log.Printf("orgtd starting: dir=%q editor=%q url_formatter=%q url_formatter_prefixes=%v format_links_url_formatter=%q agenda_days=%d snippets_days=%d inbox_file=%q calendar_file=%q meeting_tags_file=%q outline_ignore_files=%v scratch_dir=%q hide_closed_after_hours=%d",
			cfg.OrgDir, cfg.Editor, cfg.URLFormatter, cfg.URLFormatterPrefixes, cfg.FormatLinksURLFormatter, cfg.AgendaWindowDays, cfg.SnippetsWindowDays, cfg.InboxFile, cfg.CalendarFile, cfg.MeetingTagsFile, cfg.OutlineIgnoreFiles, cfg.ScratchDir, cfg.HideClosedAfterHours)
	} else {
		log.SetOutput(io.Discard)
	}

	// The org directory is a git repository, and every save is a commit
	// merged with whatever other instances (or other editors) saved
	// meanwhile — see internal/gitstore. Opening takes this instance's
	// lock, which the OS drops when the process exits, however it exits.
	store, err := gitstore.Open(cfg.OrgDir, cfg.MainBranch, cfg.OutlineIgnoreFiles)
	if errors.Is(err, gitstore.ErrNotRepo) {
		fmt.Fprintf(os.Stderr, "orgtd: %s must be the root of a git repository, since orgtd saves by committing to it.\nTo start one: git -C %s init -b %s\n", cfg.OrgDir, cfg.OrgDir, cfg.MainBranch)
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "orgtd: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()
	if err := store.CheckBranch(); err != nil {
		warnings = append(warnings, "Saving is off: "+err.Error())
	}
	// Read before loading the files, so they're at least as new as it.
	head, err := store.Head()
	if err != nil {
		fmt.Fprintf(os.Stderr, "orgtd: %v\n", err)
		os.Exit(1)
	}
	orphans, err := store.Orphans()
	if err != nil {
		warnings = append(warnings, "Checking for a crashed orgtd's edits: "+err.Error())
	}
	if len(orphans) > 0 {
		warnings = append(warnings, "Recovered unsaved edits from a crashed orgtd — :recover to save them, :recover drop to discard")
	}

	var active, done []string
	for _, k := range cfg.Keywords {
		if k.Done {
			done = append(done, k.Name)
		} else {
			active = append(active, k.Name)
		}
	}
	org.SetKeywords(active, done)

	ws, err := workspace.Load(cfg.OrgDir, cfg.OutlineIgnoreFiles...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "orgtd: %v\n", err)
		os.Exit(1)
	}

	p := tea.NewProgram(ui.New(ws, ui.WithConfig(cfg), ui.WithConfigSources(sources), ui.WithStore(store, head), ui.WithOrphans(orphans), ui.WithStartupMessage(strings.Join(warnings, "; ")), ui.WithReadme(orgtd.Readme)), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "orgtd: %v\n", err)
		os.Exit(1)
	}
}
