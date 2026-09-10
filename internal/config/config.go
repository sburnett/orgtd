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
