package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/sburnett/orgtd/internal/config"
)

// settings is the fully-resolved set of values main uses, after merging
// command-line flags, $ORGTD_DIR, the config file, and built-in
// defaults per resolveSettings' precedence rules.
type settings struct {
	dir               string
	outputFile        string
	calendarIDs       []string
	syncPastDays      int
	syncFutureDays    int
	oauthClientID     string
	oauthClientSecret string
	reauth            bool
}

// flagValues is the raw output of flag parsing: each flag's value
// (whatever it holds, default or user-supplied) plus which flags the
// user actually passed on the command line (via flag.Visit) — needed to
// tell "the user explicitly asked for this" apart from "this just
// happens to equal the flag's zero-value default", which resolveSettings
// must treat differently (an explicit flag always wins; an
// unset-and-still-zero one falls through to the config file).
type flagValues struct {
	dir               string
	outputFile        string
	calendarIDs       []string
	syncPastDays      int
	syncFutureDays    int
	oauthClientID     string
	oauthClientSecret string
	reauth            bool
	explicit          map[string]bool
}

// resolveSettings merges f, $ORGTD_DIR (orgtdDirEnv), and cfg (the
// config file gcalsync shares with orgtd — org_dir at the top level,
// everything else under its own [gcalsync] section) into the final
// settings, in precedence order:
//
//  1. An explicitly-passed flag always wins.
//  2. For -dir specifically, $ORGTD_DIR comes next, matching orgtd's own
//     precedence (see cmd/orgtd/settings.go) since both tools share the
//     same org directory.
//  3. The config file's value, if set.
//  4. defaultOrgDir() for -dir; built-in defaults for everything else
//     (calendar_ids=["primary"], sync_past_days=1, sync_future_days=14,
//     output_file="calendar.org").
func resolveSettings(f flagValues, orgtdDirEnv string, cfg *config.Config) settings {
	s := settings{
		dir:               f.dir,
		outputFile:        f.outputFile,
		calendarIDs:       f.calendarIDs,
		syncPastDays:      f.syncPastDays,
		syncFutureDays:    f.syncFutureDays,
		oauthClientID:     f.oauthClientID,
		oauthClientSecret: f.oauthClientSecret,
		reauth:            f.reauth,
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

	if !f.explicit["output-file"] {
		if cfg.Gcalsync.OutputFile != "" {
			s.outputFile = cfg.Gcalsync.OutputFile
		} else {
			s.outputFile = "calendar.org"
		}
	}
	if !f.explicit["calendar-ids"] {
		if len(cfg.Gcalsync.CalendarIDs) > 0 {
			s.calendarIDs = cfg.Gcalsync.CalendarIDs
		} else {
			s.calendarIDs = []string{"primary"}
		}
	}
	if !f.explicit["sync-past-days"] {
		if cfg.Gcalsync.SyncPastDays != 0 {
			s.syncPastDays = cfg.Gcalsync.SyncPastDays
		} else {
			s.syncPastDays = 1
		}
	}
	if !f.explicit["sync-future-days"] {
		if cfg.Gcalsync.SyncFutureDays != 0 {
			s.syncFutureDays = cfg.Gcalsync.SyncFutureDays
		} else {
			s.syncFutureDays = 14
		}
	}
	if !f.explicit["oauth-client-id"] && cfg.Gcalsync.OAuthClientID != "" {
		s.oauthClientID = cfg.Gcalsync.OAuthClientID
	}
	if !f.explicit["oauth-client-secret"] && cfg.Gcalsync.OAuthClientSecret != "" {
		s.oauthClientSecret = cfg.Gcalsync.OAuthClientSecret
	}

	if !filepath.IsAbs(s.outputFile) {
		s.outputFile = filepath.Join(s.dir, s.outputFile)
	}

	return s
}

// defaultOrgDir mirrors cmd/orgtd's own fallback: ~/org, so both tools
// agree on where the org directory lives with no config at all.
func defaultOrgDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "org"
	}
	return filepath.Join(home, "org")
}

// splitList parses a comma-separated flag value into a slice, trimming
// whitespace around each entry and dropping empty ones (so a trailing
// comma, or the flag simply being unset, yields nil rather than a slice
// with a blank entry).
func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
