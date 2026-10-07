package main

import (
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	orgtd "github.com/sburnett/orgtd"
	"github.com/sburnett/orgtd/internal/config"
)

// flags builds a flagValues as if none of the flags were passed on the
// command line — every value is the flag's own zero-value default.
func flags() flagValues {
	return flagValues{explicit: map[string]bool{}}
}

// withExplicit marks the named flags as passed explicitly.
func withExplicit(f flagValues, names ...string) flagValues {
	for _, n := range names {
		f.explicit[n] = true
	}
	return f
}

func TestResolveConfigAllDefaultsWhenNothingSet(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	got := resolveConfig(flags(), "", &config.Config{})
	want := config.Default()
	want.OrgDir = defaultOrgDir()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("resolveConfig = %+v, want the built-in defaults %+v", got, want)
	}
}

func TestResolveConfigKeepsEverythingTheConfigFileSets(t *testing.T) {
	// One config file setting every kind of value, including the sections
	// that have no flag (gcalsync, icons, colors): none of it may be lost
	// or altered on the way through.
	cfg := &config.Config{
		OrgDir:                  "/from/config",
		URLFormatter:            "url2org",
		URLFormatterPrefixes:    []string{"bit.ly/", "go/"},
		FormatLinksURLFormatter: "batch-formatter",
		AgendaWindowDays:        30,
		InboxFile:               "capture.org",
		CalendarFile:            "my-calendar.org",
		MeetingTagsFile:         "tags.org",
		HideDoneAfterHours:      48,
		Editor:                  "emacsclient -t",
		Debug:                   true,
		Gcalsync: config.GcalsyncConfig{
			OAuthClientID: "id", OAuthClientSecret: "secret",
			CalendarIDs: []string{"primary", "team@example.com"}, SyncPastDays: 2, SyncFutureDays: 21,
			AttendeeTagDomains: []string{"example.com"}, AttendeeIgnorePatterns: []string{"c_*@*"},
		},
		Icons:  config.IconsConfig{DirtyIcon: "*", DirtyColor: "1", MarkColor: "2", ReviewIcon: "@", LockColor: "4", MeetingIcon: "%"},
		Colors: config.ColorsConfig{File: "#111111", TODO: "#222222", SearchHighlightBg: "#707070"},
	}
	want := *cfg
	got := resolveConfig(flags(), "", cfg)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("resolveConfig = %+v, want the config file verbatim: %+v", got, want)
	}
}

func TestResolveConfigDoesNotModifyItsInput(t *testing.T) {
	cfg := &config.Config{}
	resolveConfig(withExplicit(flagValues{dir: "/x", inboxFile: "i.org", explicit: map[string]bool{}}, "dir", "inbox-file"), "", cfg)
	if !reflect.DeepEqual(*cfg, config.Config{}) {
		t.Errorf("resolveConfig modified the config file's Config: %+v", *cfg)
	}
}

func TestResolveConfigExplicitFlagBeatsConfigFile(t *testing.T) {
	f := withExplicit(flagValues{
		dir: "/from/flag", urlFormatter: "flag-fmt", urlFormatterPrefixes: "flag-prefix/, other/",
		formatLinksURLFormatter: "flag-batch-fmt",
		agendaDays:              7, inboxFile: "flag-inbox.org", calendarFile: "flag-calendar.org",
		meetingTagsFile: "flag-tags.org", hideDoneAfterHours: 12, editor: "vim", debug: false,
		explicit: map[string]bool{},
	}, "dir", "url-formatter", "url-formatter-prefixes", "format-links-url-formatter", "agenda-days",
		"inbox-file", "calendar-file", "meeting-tags-file", "hide-done-after-hours", "editor", "debug")
	cfg := &config.Config{
		OrgDir: "/from/config", URLFormatter: "cfg-fmt", URLFormatterPrefixes: []string{"cfg-prefix/"},
		FormatLinksURLFormatter: "cfg-batch-fmt", AgendaWindowDays: 30, InboxFile: "cfg-inbox.org",
		CalendarFile: "cfg-calendar.org", MeetingTagsFile: "cfg-tags.org", HideDoneAfterHours: 48,
		Editor: "emacs", Debug: true,
	}
	got := resolveConfig(f, "/from/env", cfg)
	want := config.Default()
	want.OrgDir, want.URLFormatter, want.URLFormatterPrefixes = "/from/flag", "flag-fmt", []string{"flag-prefix/", "other/"}
	want.FormatLinksURLFormatter, want.AgendaWindowDays, want.InboxFile = "flag-batch-fmt", 7, "flag-inbox.org"
	want.CalendarFile, want.MeetingTagsFile, want.HideDoneAfterHours = "flag-calendar.org", "flag-tags.org", 12
	want.Editor, want.Debug = "vim", false
	if !reflect.DeepEqual(got, want) {
		t.Errorf("resolveConfig = %+v, want %+v", got, want)
	}
}

func TestResolveConfigUnsetFlagsLeaveConfigFileValuesAlone(t *testing.T) {
	// A flag at its zero value that wasn't passed must not clobber the
	// config file's value — the whole reason explicit is tracked.
	cfg := &config.Config{
		URLFormatter: "u", URLFormatterPrefixes: []string{"go/"}, FormatLinksURLFormatter: "b",
		InboxFile: "i.org", CalendarFile: "c.org", MeetingTagsFile: "m.org", AgendaWindowDays: 9,
		HideDoneAfterHours: 5, Editor: "e", Debug: true,
	}
	got := resolveConfig(flags(), "", cfg)
	if got.URLFormatter != "u" || !reflect.DeepEqual(got.URLFormatterPrefixes, []string{"go/"}) || got.FormatLinksURLFormatter != "b" ||
		got.InboxFile != "i.org" || got.CalendarFile != "c.org" || got.MeetingTagsFile != "m.org" ||
		got.AgendaWindowDays != 9 || got.HideDoneAfterHours != 5 || got.Editor != "e" || !got.Debug {
		t.Errorf("an unset flag clobbered the config file: %+v", got)
	}
}

func TestResolveConfigOrgtdDirEnvBeatsConfigButNotExplicitFlag(t *testing.T) {
	cfg := &config.Config{OrgDir: "/from/config"}

	if got := resolveConfig(flags(), "/from/env", cfg); got.OrgDir != "/from/env" {
		t.Errorf("OrgDir = %q, want $ORGTD_DIR to beat the config file", got.OrgDir)
	}
	explicitDir := withExplicit(flagValues{dir: "/from/flag", explicit: map[string]bool{}}, "dir")
	if got := resolveConfig(explicitDir, "/from/env", cfg); got.OrgDir != "/from/flag" {
		t.Errorf("OrgDir = %q, want an explicit -dir flag to beat $ORGTD_DIR", got.OrgDir)
	}
}

func TestResolveConfigZeroInConfigFileMeansUnset(t *testing.T) {
	// AgendaWindowDays/HideDoneAfterHours of 0 is TOML's zero value for an
	// absent key, not a real request for a zero-length window.
	got := resolveConfig(flags(), "", &config.Config{AgendaWindowDays: 0, HideDoneAfterHours: 0})
	if got.AgendaWindowDays != 14 || got.HideDoneAfterHours != 24 {
		t.Errorf("windows = %d/%d, want the defaults 14/24", got.AgendaWindowDays, got.HideDoneAfterHours)
	}
}

func TestResolveConfigExplicitZeroFlagOverridesConfigWithTheDefault(t *testing.T) {
	// An explicit -agenda-days=0 wins over the config file's 30 — and 0
	// isn't a usable window, so what's left is the built-in default, not
	// the config file's value.
	f := withExplicit(flagValues{agendaDays: 0, explicit: map[string]bool{}}, "agenda-days")
	if got := resolveConfig(f, "", &config.Config{AgendaWindowDays: 30}); got.AgendaWindowDays != 14 {
		t.Errorf("AgendaWindowDays = %d, want the default 14", got.AgendaWindowDays)
	}
}

func TestResolveConfigExplicitDebugFlagBeatsConfigFile(t *testing.T) {
	f := withExplicit(flagValues{debug: false, explicit: map[string]bool{}}, "debug")
	if got := resolveConfig(f, "", &config.Config{Debug: true}); got.Debug {
		t.Error("an explicit -debug=false should beat the config file's debug = true")
	}
}

func TestDefineFlagsRecordsOnlyExplicitlyPassedFlags(t *testing.T) {
	fs := flag.NewFlagSet("orgtd", flag.ContinueOnError)
	f := defineFlags(fs)
	if err := fs.Parse([]string{"-inbox-file", "x.org", "-debug=false"}); err != nil {
		t.Fatal(err)
	}
	f.recordExplicit(fs)
	if !f.explicit["inbox-file"] || !f.explicit["debug"] || f.explicit["editor"] || len(f.explicit) != 2 {
		t.Errorf("explicit = %v, want exactly inbox-file and debug", f.explicit)
	}
	if f.inboxFile != "x.org" {
		t.Errorf("inboxFile = %q, want x.org", f.inboxFile)
	}
}

// TestReadmeDocumentsEveryFlagAndConfigKey keeps the README, which is
// also the :help text, from drifting behind the code: every command-line
// flag and every config-file key (including the [gcalsync], [icons] and
// [colors] sections) must be mentioned in it. Adding a setting without
// documenting it fails here.
func TestReadmeDocumentsEveryFlagAndConfigKey(t *testing.T) {
	fs := flag.NewFlagSet("orgtd", flag.ContinueOnError)
	defineFlags(fs)
	fs.VisitAll(func(fl *flag.Flag) {
		if !strings.Contains(orgtd.Readme, "`--"+fl.Name+"`") {
			t.Errorf("README.md doesn't document the --%s flag", fl.Name)
		}
	})

	var keys []string
	var walk func(t reflect.Type)
	walk = func(rt reflect.Type) {
		for i := 0; i < rt.NumField(); i++ {
			field := rt.Field(i)
			key, _, _ := strings.Cut(field.Tag.Get("toml"), ",")
			if key == "" {
				continue
			}
			if field.Type.Kind() == reflect.Struct {
				walk(field.Type) // a section: document its keys, not the header
				continue
			}
			keys = append(keys, key)
		}
	}
	walk(reflect.TypeOf(config.Config{}))
	for _, key := range keys {
		if !strings.Contains(orgtd.Readme, key) {
			t.Errorf("README.md doesn't mention the config key %q", key)
		}
	}
	if len(keys) < 40 {
		t.Errorf("found only %d config keys by reflection; the walk is broken", len(keys))
	}
}

// TestExampleConfigParses checks config.example.toml stays loadable: the
// keys it shows (commented out, so uncommenting them is the user's step)
// are real, which uncommenting everything and loading proves.
func TestExampleConfigParses(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "config.example.toml"))
	if err != nil {
		t.Fatal(err)
	}
	var uncommented []string
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		// Uncomment "# key = value" and "# [section]" lines, leave prose.
		if rest, ok := strings.CutPrefix(trimmed, "# "); ok && (strings.Contains(rest, " = ") || strings.HasPrefix(rest, "[")) {
			uncommented = append(uncommented, rest)
			continue
		}
		uncommented = append(uncommented, line)
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(strings.Join(uncommented, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(path); err != nil {
		t.Errorf("config.example.toml doesn't load once uncommented: %v", err)
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
