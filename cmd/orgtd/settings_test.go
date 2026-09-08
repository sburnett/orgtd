package main

import (
	"testing"

	"github.com/sburnett/orgtd/internal/config"
)

// flags builds a flagValues as if none of the named flags were passed
// explicitly on the command line — i.e. every value shown is the flag's
// own zero-value default (dir/urlFormatter/inboxFile/editor: "";
// agendaDays: 0) unless overridden via withExplicit.
func flags() flagValues {
	return flagValues{explicit: map[string]bool{}}
}

func withExplicit(f flagValues, names ...string) flagValues {
	for _, n := range names {
		f.explicit[n] = true
	}
	return f
}

func TestResolveSettingsAllDefaultsWhenNothingSet(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	got := resolveSettings(flags(), "", &config.Config{})
	want := settings{dir: defaultOrgDir(), urlFormatter: "", agendaDays: 0, inboxFile: "", editor: ""}
	if got != want {
		t.Errorf("resolveSettings = %+v, want %+v", got, want)
	}
}

func TestResolveSettingsConfigFileOverridesDefaults(t *testing.T) {
	cfg := &config.Config{
		OrgDir:           "/from/config",
		URLFormatter:     "url2org",
		AgendaWindowDays: 30,
		InboxFile:        "capture.org",
		Editor:           "emacsclient -t",
	}
	got := resolveSettings(flags(), "", cfg)
	want := settings{dir: "/from/config", urlFormatter: "url2org", agendaDays: 30, inboxFile: "capture.org", editor: "emacsclient -t"}
	if got != want {
		t.Errorf("resolveSettings = %+v, want %+v", got, want)
	}
}

func TestResolveSettingsExplicitFlagBeatsConfigFile(t *testing.T) {
	f := withExplicit(flagValues{
		dir: "/from/flag", urlFormatter: "flag-fmt", agendaDays: 7, inboxFile: "flag-inbox.org", editor: "vim",
		explicit: map[string]bool{},
	}, "dir", "url-formatter", "agenda-days", "inbox-file", "editor")
	cfg := &config.Config{
		OrgDir:           "/from/config",
		URLFormatter:     "cfg-fmt",
		AgendaWindowDays: 30,
		InboxFile:        "cfg-inbox.org",
		Editor:           "emacs",
	}
	got := resolveSettings(f, "/from/env", cfg)
	want := settings{dir: "/from/flag", urlFormatter: "flag-fmt", agendaDays: 7, inboxFile: "flag-inbox.org", editor: "vim"}
	if got != want {
		t.Errorf("resolveSettings = %+v, want %+v", got, want)
	}
}

func TestResolveSettingsOrgtdDirEnvBeatsConfigButNotExplicitFlag(t *testing.T) {
	cfg := &config.Config{OrgDir: "/from/config"}

	got := resolveSettings(flags(), "/from/env", cfg)
	if got.dir != "/from/env" {
		t.Errorf("dir = %q, want $ORGTD_DIR to beat the config file", got.dir)
	}

	explicitDir := withExplicit(flagValues{dir: "/from/flag", explicit: map[string]bool{}}, "dir")
	got = resolveSettings(explicitDir, "/from/env", cfg)
	if got.dir != "/from/flag" {
		t.Errorf("dir = %q, want an explicit -dir flag to beat $ORGTD_DIR", got.dir)
	}
}

func TestResolveSettingsZeroAgendaDaysInConfigIsTreatedAsUnset(t *testing.T) {
	// AgendaWindowDays: 0 is TOML's zero value for an absent key, not a
	// real "0-day window" request — WithAgendaDays itself already treats
	// <= 0 as "use the built-in default", so resolveSettings shouldn't
	// even try to distinguish the two here.
	got := resolveSettings(flags(), "", &config.Config{AgendaWindowDays: 0})
	if got.agendaDays != 0 {
		t.Errorf("agendaDays = %d, want 0 (falls through to the flag's own default)", got.agendaDays)
	}
}
