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
calendar_file = "my-calendar.org"
url_formatter_prefixes = ["bit.ly/", "go/"]
format_links_url_formatter = "batch-formatter"
hide_done_after_hours = 48
debug = true

[gcalsync]
oauth_client_id = "client-id"
oauth_client_secret = "client-secret"
calendar_ids = ["primary", "team@example.com"]
sync_past_days = 2
sync_future_days = 21
attendee_tag_domains = ["example.com", "example.org"]
attendee_ignore_patterns = ["c_*@*", "*@resource.calendar.google.com"]

[icons]
dirty_icon = "*"
dirty_color = "1"
mark_color = "2"
clarify_icon = "@"
clarify_color = "3"
lock_icon = "#"
lock_color = "4"
meeting_icon = "%"
meeting_color = "5"

[colors]
file_color = "#111111"
todo_color = "#222222"
next_color = "#333333"
waiting_color = "#444444"
someday_color = "#555555"
done_color = "#666666"
cancelled_color = "#777777"
tag_color = "#888888"
done_title_color = "#999999"
status_color = "#aaaaaa"
timestamp_color = "#bbbbbb"
error_color = "#cccccc"
body_color = "#dddddd"
caret_fg = "#eeeeee"
caret_bg = "#ffffff"
highlight_bg = "#101010"
panel_bg = "#202020"
status_bar_bg = "#303030"
status_bar_fg = "#404040"
cursor_row_bg = "#505050"
visual_selection_bg = "#606060"
search_highlight_bg = "#707070"
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
		CalendarFile:            "my-calendar.org",
		URLFormatterPrefixes:    []string{"bit.ly/", "go/"},
		FormatLinksURLFormatter: "batch-formatter",
		HideDoneAfterHours:      48,
		Debug:                   true,
		Gcalsync: GcalsyncConfig{
			OAuthClientID:          "client-id",
			OAuthClientSecret:      "client-secret",
			CalendarIDs:            []string{"primary", "team@example.com"},
			SyncPastDays:           2,
			SyncFutureDays:         21,
			AttendeeTagDomains:     []string{"example.com", "example.org"},
			AttendeeIgnorePatterns: []string{"c_*@*", "*@resource.calendar.google.com"},
		},
		Icons: IconsConfig{
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
		Colors: ColorsConfig{
			File:              "#111111",
			TODO:              "#222222",
			Next:              "#333333",
			Waiting:           "#444444",
			Someday:           "#555555",
			Done:              "#666666",
			Cancelled:         "#777777",
			Tag:               "#888888",
			DoneTitle:         "#999999",
			Status:            "#aaaaaa",
			Timestamp:         "#bbbbbb",
			Error:             "#cccccc",
			Body:              "#dddddd",
			CaretFg:           "#eeeeee",
			CaretBg:           "#ffffff",
			HighlightBg:       "#101010",
			PanelBg:           "#202020",
			StatusBarBg:       "#303030",
			StatusBarFg:       "#404040",
			CursorRowBg:       "#505050",
			VisualSelectionBg: "#606060",
			SearchHighlightBg: "#707070",
		},
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
