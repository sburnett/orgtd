package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadMissingFileReturnsZeroValueNoError(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "does-not-exist.toml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(*c, Config{}) {
		t.Errorf("Load(missing) = %+v, want zero value", *c)
	}
}

func TestLoadParsesAllFields(t *testing.T) {
	path := writeConfig(t, `
org_dir = "/tmp/myorg"
editor = "emacsclient -t"
url_formatter = "url2org"
agenda_window_days = 30
inbox_file = "capture.org"
url_formatter_prefixes = ["bit.ly/", "go/"]
format_links_url_formatter = "batch-formatter"
hide_done_after_hours = 48
debug = true
`)
	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := Config{
		OrgDir:                  "/tmp/myorg",
		Editor:                  "emacsclient -t",
		URLFormatter:            "url2org",
		AgendaWindowDays:        30,
		InboxFile:               "capture.org",
		URLFormatterPrefixes:    []string{"bit.ly/", "go/"},
		FormatLinksURLFormatter: "batch-formatter",
		HideDoneAfterHours:      48,
		Debug:                   true,
	}
	if !reflect.DeepEqual(*c, want) {
		t.Errorf("Load = %+v, want %+v", *c, want)
	}
}

func TestLoadExpandsHomeInOrgDir(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory available")
	}

	cases := []struct{ raw, want string }{
		{"~", home},
		{"~/org", filepath.Join(home, "org")},
		{"~/deeply/nested", filepath.Join(home, "deeply/nested")},
		{"/absolute/path", "/absolute/path"}, // unaffected
		{"", ""},                             // unaffected (unset)
	}
	for _, c := range cases {
		path := writeConfig(t, "org_dir = \""+c.raw+"\"\n")
		got, err := Load(path)
		if err != nil {
			t.Fatalf("Load(%q): %v", c.raw, err)
		}
		if got.OrgDir != c.want {
			t.Errorf("Load(org_dir=%q).OrgDir = %q, want %q", c.raw, got.OrgDir, c.want)
		}
	}
}

func TestLoadRejectsMalformedTOML(t *testing.T) {
	path := writeConfig(t, "this is not valid toml [[[")
	if _, err := Load(path); err == nil {
		t.Error("Load(malformed) = nil error, want a parse error")
	}
}

func TestLoadPropagatesUnreadableFile(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root: file permissions don't block reads")
	}
	path := writeConfig(t, "org_dir = \"/tmp/x\"\n")
	if err := os.Chmod(path, 0); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	if _, err := Load(path); err == nil {
		t.Error("Load(unreadable) = nil error, want a read error")
	}
}

func TestDefaultPathUsesXDGConfigHomeWhenSet(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/xdg-home")
	if got, want := DefaultPath(), filepath.Join("/xdg-home", "orgtd", "config.toml"); got != want {
		t.Errorf("DefaultPath() = %q, want %q", got, want)
	}
}

func TestDefaultPathFallsBackToDotConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory available")
	}
	if got, want := DefaultPath(), filepath.Join(home, ".config", "orgtd", "config.toml"); got != want {
		t.Errorf("DefaultPath() = %q, want %q", got, want)
	}
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}
