package main

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/sburnett/orgtd/internal/config"
)

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
	dir := defaultOrgDir()
	want := settings{
		dir:            dir,
		outputFile:     filepath.Join(dir, "calendar.org"),
		calendarIDs:    []string{"primary"},
		syncPastDays:   1,
		syncFutureDays: 14,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("resolveSettings = %+v, want %+v", got, want)
	}
}

func TestResolveSettingsConfigFileOverridesDefaults(t *testing.T) {
	cfg := &config.Config{
		OrgDir: "/from/config",
		Gcalsync: config.GcalsyncConfig{
			OutputFile:        "cal.org",
			CalendarIDs:       []string{"primary", "team@example.com"},
			SyncPastDays:      2,
			SyncFutureDays:    21,
			OAuthClientID:     "cfg-client-id",
			OAuthClientSecret: "cfg-client-secret",
		},
	}
	got := resolveSettings(flags(), "", cfg)
	want := settings{
		dir:               "/from/config",
		outputFile:        "/from/config/cal.org",
		calendarIDs:       []string{"primary", "team@example.com"},
		syncPastDays:      2,
		syncFutureDays:    21,
		oauthClientID:     "cfg-client-id",
		oauthClientSecret: "cfg-client-secret",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("resolveSettings = %+v, want %+v", got, want)
	}
}

func TestResolveSettingsExplicitFlagBeatsConfigFile(t *testing.T) {
	f := withExplicit(flagValues{
		dir:               "/from/flag",
		outputFile:        "/from/flag/cal.org",
		calendarIDs:       []string{"flag-cal"},
		syncPastDays:      3,
		syncFutureDays:    30,
		oauthClientID:     "flag-client-id",
		oauthClientSecret: "flag-client-secret",
		explicit:          map[string]bool{},
	}, "dir", "output-file", "calendar-ids", "sync-past-days", "sync-future-days", "oauth-client-id", "oauth-client-secret")
	cfg := &config.Config{
		OrgDir: "/from/config",
		Gcalsync: config.GcalsyncConfig{
			OutputFile:        "cfg-cal.org",
			CalendarIDs:       []string{"cfg-cal"},
			SyncPastDays:      2,
			SyncFutureDays:    21,
			OAuthClientID:     "cfg-client-id",
			OAuthClientSecret: "cfg-client-secret",
		},
	}
	got := resolveSettings(f, "", cfg)
	want := settings{
		dir:               "/from/flag",
		outputFile:        "/from/flag/cal.org",
		calendarIDs:       []string{"flag-cal"},
		syncPastDays:      3,
		syncFutureDays:    30,
		oauthClientID:     "flag-client-id",
		oauthClientSecret: "flag-client-secret",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("resolveSettings = %+v, want %+v", got, want)
	}
}

func TestResolveSettingsOrgtdDirEnvBeatsConfigButNotFlag(t *testing.T) {
	cfg := &config.Config{OrgDir: "/from/config"}

	got := resolveSettings(flags(), "/from/env", cfg)
	if got.dir != "/from/env" {
		t.Errorf("dir = %q, want %q ($ORGTD_DIR over config file)", got.dir, "/from/env")
	}

	f := withExplicit(flagValues{dir: "/from/flag", explicit: map[string]bool{}}, "dir")
	got = resolveSettings(f, "/from/env", cfg)
	if got.dir != "/from/flag" {
		t.Errorf("dir = %q, want %q (explicit flag over $ORGTD_DIR)", got.dir, "/from/flag")
	}
}

func TestResolveSettingsOutputFileRelativeToDir(t *testing.T) {
	got := resolveSettings(flags(), "", &config.Config{
		OrgDir:   "/my/org",
		Gcalsync: config.GcalsyncConfig{OutputFile: "nested/cal.org"},
	})
	if want := "/my/org/nested/cal.org"; got.outputFile != want {
		t.Errorf("outputFile = %q, want %q", got.outputFile, want)
	}
}

func TestSplitList(t *testing.T) {
	cases := map[string][]string{
		"":                nil,
		"primary":         {"primary"},
		"a,b":             {"a", "b"},
		" a , b ,":        {"a", "b"},
		",,":              nil,
		"a@example.com,b": {"a@example.com", "b"},
	}
	for in, want := range cases {
		if got := splitList(in); !reflect.DeepEqual(got, want) {
			t.Errorf("splitList(%q) = %v, want %v", in, got, want)
		}
	}
}
