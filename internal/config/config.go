// Package config loads orgtd's optional TOML config file (DESIGN.md
// §10). Every field is optional and defaults to its zero value when
// absent, so the tool works fully with no config file present at all —
// callers treat a zero value as "no override" and fall back to their own
// built-in default.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Config is the config file's contents. Every field mirrors a
// command-line flag of the same purpose; an explicitly-passed flag
// always overrides the corresponding config value (see cmd/orgtd/main.go).
type Config struct {
	OrgDir           string `toml:"org_dir"`
	Editor           string `toml:"editor"`
	URLFormatter     string `toml:"url_formatter"`
	AgendaWindowDays int    `toml:"agenda_window_days"`
	InboxFile        string `toml:"inbox_file"`

	// CalendarFile is the base name of the file excluded from the
	// outline view and shown instead, grouped by day, in the :calendar
	// view — the file gcalsync writes. Empty falls back to the built-in
	// default, calendar.org.
	CalendarFile string `toml:"calendar_file"`

	// HideDoneAfterHours is how many hours after a DONE/CANCELLED
	// headline's CLOSED timestamp it's hidden from the outline view (the
	// feature can still be toggled off at runtime with :toggledone,
	// regardless of this value). Zero means "not set", per this
	// package's own zero-value convention, and falls back to the
	// built-in default of 24.
	HideDoneAfterHours int `toml:"hide_done_after_hours"`

	// URLFormatterPrefixes are extra bare-URL prefixes recognized beyond
	// the built-in http:// and https://, e.g. "bit.ly/" for a shortlink
	// service or "go/" for an internal go-link convention — text
	// starting with one of these (at a word boundary) gets passed
	// through URLFormatter the same as a real http(s) URL would.
	URLFormatterPrefixes []string `toml:"url_formatter_prefixes"`

	// FormatLinksURLFormatter is the external program :format-links
	// invokes in batch mode (one URL per stdin line, the same number of
	// formatted lines back on stdout) — configured separately from
	// URLFormatter since a batch-capable command may differ from (or
	// take different arguments than) whatever handles a single URL while
	// editing. Empty means "use URLFormatter for :format-links too".
	FormatLinksURLFormatter string `toml:"format_links_url_formatter"`

	// Debug turns on logging (URL formatter attempts/failures, etc.) to
	// a debug.log file next to this config file. Off by default — false
	// is indistinguishable from "not set" (this package's usual
	// zero-value convention), which is fine here since false is also the
	// built-in default.
	Debug bool `toml:"debug"`

	// Gcalsync holds settings for :sync-calendar (see internal/ui and
	// internal/calendarsync), kept under its own section so they read as
	// a distinct, optional block rather than cluttering the top level.
	Gcalsync GcalsyncConfig `toml:"gcalsync"`

	// Icons customizes the outline's gutter markers (see internal/ui's
	// gutter/markColumn/lockColumn/meetingColumn) — kept under its own
	// section, like Gcalsync, since it's an optional block of unrelated
	// settings rather than something that belongs at the top level.
	Icons IconsConfig `toml:"icons"`
}

// IconsConfig is the "[icons]" section of the config file: the
// character and color of each marker the outline draws in its gutter
// column, to the left of every entry. Every field is optional and falls
// back to orgtd's own built-in glyph/color when unset, same as the rest
// of Config. There's no command-line flag for any of these — like
// Gcalsync, this is config-file only.
type IconsConfig struct {
	// DirtyIcon/DirtyColor style the marker shown on any entry with
	// unsaved changes (default: "+", a warm red).
	DirtyIcon  string `toml:"dirty_icon"`
	DirtyColor string `toml:"dirty_color"`

	// MarkColor styles a vim-style mark's letter ("m<letter>"), both in
	// the gutter and pinned to the top of the screen (default: a pink).
	// There's no MarkIcon since the glyph is always the mark's own
	// letter, chosen by whoever set the mark, not a fixed character.
	MarkColor string `toml:"mark_color"`

	// ClarifyIcon/ClarifyColor style the marker on :clarify's pinned
	// inbox item, both in the gutter and pinned to the top of the screen
	// (default: "●", the same pink as MarkColor).
	ClarifyIcon  string `toml:"clarify_icon"`
	ClarifyColor string `toml:"clarify_color"`

	// LockIcon/LockColor style the marker on an entry currently locked
	// by an in-flight :format-links batch (default: "◆", an orange).
	LockIcon  string `toml:"lock_icon"`
	LockColor string `toml:"lock_color"`

	// MeetingIcon/MeetingColor style the marker on an entry attached to
	// a calendar meeting via "gM" (default: "▣", a blue).
	MeetingIcon  string `toml:"meeting_icon"`
	MeetingColor string `toml:"meeting_color"`
}

// GcalsyncConfig is the "[gcalsync]" section of the config file: settings
// for :sync-calendar, which regenerates CalendarFile (see
// Config.CalendarFile) from Google Calendar.
type GcalsyncConfig struct {
	// OAuthClientID and OAuthClientSecret are the installed-app OAuth2
	// client credentials :sync-calendar uses to authenticate to the
	// Google Calendar API. Every user is expected to create their own
	// OAuth client (Google Cloud Console, "Desktop app" type) rather
	// than share one baked into the binary, since a distributed client
	// secret can't actually stay secret.
	OAuthClientID     string `toml:"oauth_client_id"`
	OAuthClientSecret string `toml:"oauth_client_secret"`

	// CalendarIDs are the Google Calendar IDs to sync, e.g. "primary" or
	// an email address for a secondary/shared calendar.
	CalendarIDs []string `toml:"calendar_ids"`

	// SyncPastDays and SyncFutureDays bound the sync window around now
	// (e.g. 1/14 means from yesterday through two weeks from now).
	SyncPastDays   int `toml:"sync_past_days"`
	SyncFutureDays int `toml:"sync_future_days"`

	// AttendeeTagDomains restricts the "@username" tags synced events
	// get for their confirmed attendees (see internal/calendarsync's
	// BuildFile) to attendees whose email address ends in one of these
	// domains, e.g. ["example.com"] to tag only coworkers and drop
	// external guests, vendors, room/resource calendars, etc. Empty (the
	// default) means no restriction — every confirmed attendee is
	// tagged, regardless of domain, same as before this setting existed.
	// Matched case-insensitively; a leading "@" on a configured domain is
	// ignored, so "example.com" and "@example.com" behave the same.
	AttendeeTagDomains []string `toml:"attendee_tag_domains"`

	// AttendeeIgnorePatterns excludes any attendee whose email matches
	// one of these glob patterns from consideration entirely — checked
	// before AttendeeTagDomains, and unrelated to it. Each pattern is
	// matched case-insensitively against the whole address using
	// filepath.Match syntax ("*" matches any run of characters, "?" a
	// single one), e.g. ["c_*@*"] to drop the synthetic "c_...@..."
	// attendees Google Calendar attaches to represent a resource/room
	// booking. Empty (the default) means no exclusions.
	AttendeeIgnorePatterns []string `toml:"attendee_ignore_patterns"`
}

// DefaultPath returns the config file location orgtd reads unless
// overridden: $XDG_CONFIG_HOME/orgtd/config.toml, or
// ~/.config/orgtd/config.toml if $XDG_CONFIG_HOME is unset, per the XDG
// Base Directory spec's own fallback.
func DefaultPath() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "orgtd", "config.toml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".config", "orgtd", "config.toml")
	}
	return filepath.Join(home, ".config", "orgtd", "config.toml")
}

// Load reads and parses the TOML config file at path. A missing file is
// not an error — it returns a zero-value Config, so every field falls
// back to its caller's default — since the config file is entirely
// optional; any other read or parse failure is returned so a malformed
// or unreadable file the user actually created isn't silently ignored.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{}, nil
		}
		return nil, fmt.Errorf("config: reading %s: %w", path, err)
	}
	var c Config
	if err := toml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("config: parsing %s: %w", path, err)
	}
	c.OrgDir = expandHome(c.OrgDir)
	return &c, nil
}

// expandHome expands a leading "~" or "~/..." in path to the user's home
// directory, the way a shell would — TOML string values get no such
// treatment on their own, but DESIGN.md's own config example
// (org_dir = "~/org") relies on it.
func expandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, path[2:])
}
