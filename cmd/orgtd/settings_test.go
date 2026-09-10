package main

import (
	"reflect"
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
	if !reflect.DeepEqual(got, want) {
		t.Errorf("resolveSettings = %+v, want %+v", got, want)
	}
}

func TestResolveSettingsConfigFileOverridesDefaults(t *testing.T) {
	cfg := &config.Config{
		OrgDir:               "/from/config",
		URLFormatter:         "url2org",
		URLFormatterPrefixes: []string{"bit.ly/", "go/"},
		AgendaWindowDays:     30,
		InboxFile:            "capture.org",
		HideDoneAfterHours:   48,
		Editor:               "emacsclient -t",
	}
	got := resolveSettings(flags(), "", cfg)
	want := settings{
		dir: "/from/config", urlFormatter: "url2org", urlFormatterPrefixes: []string{"bit.ly/", "go/"},
		agendaDays: 30, inboxFile: "capture.org", hideDoneAfterHours: 48, editor: "emacsclient -t",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("resolveSettings = %+v, want %+v", got, want)
	}
}

func TestResolveSettingsExplicitFlagBeatsConfigFile(t *testing.T) {
	f := withExplicit(flagValues{
		dir: "/from/flag", urlFormatter: "flag-fmt", urlFormatterPrefixes: []string{"flag-prefix/"},
		agendaDays: 7, inboxFile: "flag-inbox.org", hideDoneAfterHours: 12, editor: "vim",
		explicit: map[string]bool{},
	}, "dir", "url-formatter", "url-formatter-prefixes", "agenda-days", "inbox-file", "hide-done-after-hours", "editor")
	cfg := &config.Config{
		OrgDir:               "/from/config",
		URLFormatter:         "cfg-fmt",
		URLFormatterPrefixes: []string{"cfg-prefix/"},
		AgendaWindowDays:     30,
		InboxFile:            "cfg-inbox.org",
		HideDoneAfterHours:   48,
		Editor:               "emacs",
	}
	got := resolveSettings(f, "/from/env", cfg)
	want := settings{
		dir: "/from/flag", urlFormatter: "flag-fmt", urlFormatterPrefixes: []string{"flag-prefix/"},
		agendaDays: 7, inboxFile: "flag-inbox.org", hideDoneAfterHours: 12, editor: "vim",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("resolveSettings = %+v, want %+v", got, want)
	}
}

func TestResolveSettingsURLFormatterPrefixesFallsThroughToConfigWhenFlagUnset(t *testing.T) {
	cfg := &config.Config{URLFormatterPrefixes: []string{"bit.ly/", "go/"}}
	got := resolveSettings(flags(), "", cfg)
	if !reflect.DeepEqual(got.urlFormatterPrefixes, []string{"bit.ly/", "go/"}) {
		t.Errorf("urlFormatterPrefixes = %v, want the config file's list", got.urlFormatterPrefixes)
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

func TestResolveSettingsZeroHideDoneAfterHoursInConfigIsTreatedAsUnset(t *testing.T) {
	// Same reasoning as the agenda-days case above: HideDoneAfterHours: 0
	// is TOML's zero value for an absent key, and WithHideDoneAfterHours
	// itself already treats <= 0 as "use the built-in default".
	got := resolveSettings(flags(), "", &config.Config{HideDoneAfterHours: 0})
	if got.hideDoneAfterHours != 0 {
		t.Errorf("hideDoneAfterHours = %d, want 0 (falls through to the flag's own default)", got.hideDoneAfterHours)
	}
}

func TestDebugLogPath(t *testing.T) {
	cases := []struct {
		configFilePath string
		want           string
	}{
		{"/home/sam/.config/orgtd/config.toml", "/home/sam/.config/orgtd/debug.log"},
		{"/some/other/dir/orgtd.toml", "/some/other/dir/debug.log"},
	}
	for _, c := range cases {
		if got := debugLogPath(c.configFilePath); got != c.want {
			t.Errorf("debugLogPath(%q) = %q, want %q", c.configFilePath, got, c.want)
		}
	}
}

func TestSplitPrefixes(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"go/", []string{"go/"}},
		{"bit.ly/,go/", []string{"bit.ly/", "go/"}},
		{" bit.ly/ , go/ ", []string{"bit.ly/", "go/"}}, // whitespace trimmed
		{"go/,,bit.ly/", []string{"go/", "bit.ly/"}},    // empty entries dropped
		{",", nil},
	}
	for _, c := range cases {
		if got := splitPrefixes(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("splitPrefixes(%q) = %#v, want %#v", c.in, got, c.want)
		}
	}
}
