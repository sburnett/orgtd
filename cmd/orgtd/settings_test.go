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
	want := settings{
		dir: defaultOrgDir(), urlFormatter: "", agendaDays: 0, inboxFile: "", editor: "",
		gcalCalendarIDs: []string{"primary"}, gcalSyncPastDays: 1, gcalSyncFutureDays: 14,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("resolveSettings = %+v, want %+v", got, want)
	}
}

func TestResolveSettingsGcalSettingsFollowConfigFileWithNoFlagOverride(t *testing.T) {
	cfg := &config.Config{
		Gcalsync: config.GcalsyncConfig{
			OAuthClientID:     "client-id",
			OAuthClientSecret: "client-secret",
			CalendarIDs:       []string{"primary", "team@example.com"},
			SyncPastDays:      2,
			SyncFutureDays:    21,
		},
	}
	got := resolveSettings(flags(), "", cfg)
	if got.gcalOAuthClientID != "client-id" || got.gcalOAuthClientSecret != "client-secret" {
		t.Errorf("gcalOAuthClientID/Secret = %q/%q, want client-id/client-secret", got.gcalOAuthClientID, got.gcalOAuthClientSecret)
	}
	if !reflect.DeepEqual(got.gcalCalendarIDs, []string{"primary", "team@example.com"}) {
		t.Errorf("gcalCalendarIDs = %v, want the config file's list", got.gcalCalendarIDs)
	}
	if got.gcalSyncPastDays != 2 || got.gcalSyncFutureDays != 21 {
		t.Errorf("gcalSyncPastDays/FutureDays = %d/%d, want 2/21", got.gcalSyncPastDays, got.gcalSyncFutureDays)
	}
}

func TestResolveSettingsIconSettingsFollowConfigFile(t *testing.T) {
	cfg := &config.Config{
		Icons: config.IconsConfig{
			DirtyIcon:    "*",
			DirtyColor:   "1",
			MarkColor:    "2",
			ClarifyIcon:  "@",
			ClarifyColor: "3",
			LockIcon:     "#",
			LockColor:    "4",
			MeetingIcon:  "%",
			MeetingColor: "5",
		},
	}
	got := resolveSettings(flags(), "", cfg)
	if got.iconDirtyIcon != "*" || got.iconDirtyColor != "1" {
		t.Errorf("iconDirtyIcon/Color = %q/%q, want */1", got.iconDirtyIcon, got.iconDirtyColor)
	}
	if got.iconMarkColor != "2" {
		t.Errorf("iconMarkColor = %q, want 2", got.iconMarkColor)
	}
	if got.iconClarifyIcon != "@" || got.iconClarifyColor != "3" {
		t.Errorf("iconClarifyIcon/Color = %q/%q, want @/3", got.iconClarifyIcon, got.iconClarifyColor)
	}
	if got.iconLockIcon != "#" || got.iconLockColor != "4" {
		t.Errorf("iconLockIcon/Color = %q/%q, want #/4", got.iconLockIcon, got.iconLockColor)
	}
	if got.iconMeetingIcon != "%" || got.iconMeetingColor != "5" {
		t.Errorf("iconMeetingIcon/Color = %q/%q, want %%/5", got.iconMeetingIcon, got.iconMeetingColor)
	}
}

func TestResolveSettingsConfigFileOverridesDefaults(t *testing.T) {
	cfg := &config.Config{
		OrgDir:                  "/from/config",
		URLFormatter:            "url2org",
		URLFormatterPrefixes:    []string{"bit.ly/", "go/"},
		FormatLinksURLFormatter: "batch-formatter",
		AgendaWindowDays:        30,
		InboxFile:               "capture.org",
		CalendarFile:            "my-calendar.org",
		HideDoneAfterHours:      48,
		Editor:                  "emacsclient -t",
		Debug:                   true,
	}
	got := resolveSettings(flags(), "", cfg)
	want := settings{
		dir: "/from/config", urlFormatter: "url2org", urlFormatterPrefixes: []string{"bit.ly/", "go/"},
		formatLinksURLFormatter: "batch-formatter",
		agendaDays:              30, inboxFile: "capture.org", calendarFile: "my-calendar.org", hideDoneAfterHours: 48, editor: "emacsclient -t", debug: true,
		gcalCalendarIDs: []string{"primary"}, gcalSyncPastDays: 1, gcalSyncFutureDays: 14,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("resolveSettings = %+v, want %+v", got, want)
	}
}

func TestResolveSettingsExplicitFlagBeatsConfigFile(t *testing.T) {
	f := withExplicit(flagValues{
		dir: "/from/flag", urlFormatter: "flag-fmt", urlFormatterPrefixes: []string{"flag-prefix/"},
		formatLinksURLFormatter: "flag-batch-fmt",
		agendaDays:              7, inboxFile: "flag-inbox.org", calendarFile: "flag-calendar.org", hideDoneAfterHours: 12, editor: "vim", debug: false,
		explicit: map[string]bool{},
	}, "dir", "url-formatter", "url-formatter-prefixes", "format-links-url-formatter", "agenda-days", "inbox-file", "calendar-file", "hide-done-after-hours", "editor", "debug")
	cfg := &config.Config{
		OrgDir:                  "/from/config",
		URLFormatter:            "cfg-fmt",
		URLFormatterPrefixes:    []string{"cfg-prefix/"},
		FormatLinksURLFormatter: "cfg-batch-fmt",
		AgendaWindowDays:        30,
		InboxFile:               "cfg-inbox.org",
		CalendarFile:            "cfg-calendar.org",
		HideDoneAfterHours:      48,
		Editor:                  "emacs",
		Debug:                   true,
	}
	got := resolveSettings(f, "/from/env", cfg)
	want := settings{
		dir: "/from/flag", urlFormatter: "flag-fmt", urlFormatterPrefixes: []string{"flag-prefix/"},
		formatLinksURLFormatter: "flag-batch-fmt",
		agendaDays:              7, inboxFile: "flag-inbox.org", calendarFile: "flag-calendar.org", hideDoneAfterHours: 12, editor: "vim", debug: false,
		gcalCalendarIDs: []string{"primary"}, gcalSyncPastDays: 1, gcalSyncFutureDays: 14,
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

func TestResolveSettingsCalendarFileFallsThroughToConfigWhenFlagUnset(t *testing.T) {
	cfg := &config.Config{CalendarFile: "my-calendar.org"}
	got := resolveSettings(flags(), "", cfg)
	if got.calendarFile != "my-calendar.org" {
		t.Errorf("calendarFile = %q, want the config file's value", got.calendarFile)
	}
}

func TestResolveSettingsFormatLinksURLFormatterFallsThroughToConfigWhenFlagUnset(t *testing.T) {
	cfg := &config.Config{FormatLinksURLFormatter: "batch-formatter"}
	got := resolveSettings(flags(), "", cfg)
	if got.formatLinksURLFormatter != "batch-formatter" {
		t.Errorf("formatLinksURLFormatter = %q, want the config file's value", got.formatLinksURLFormatter)
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

func TestResolveSettingsDebugDefaultsToOffAndFollowsConfigFile(t *testing.T) {
	if got := resolveSettings(flags(), "", &config.Config{}).debug; got {
		t.Errorf("debug = %v, want false with nothing set", got)
	}
	if got := resolveSettings(flags(), "", &config.Config{Debug: true}).debug; !got {
		t.Errorf("debug = %v, want true when the config file sets debug = true", got)
	}
}

func TestResolveSettingsExplicitDebugFlagBeatsConfigFile(t *testing.T) {
	f := withExplicit(flagValues{debug: false, explicit: map[string]bool{}}, "debug")
	got := resolveSettings(f, "", &config.Config{Debug: true})
	if got.debug {
		t.Error("an explicit -debug=false should beat the config file's debug = true")
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
