package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadRepoMissingFileIsEmpty(t *testing.T) {
	cfg, warnings, err := LoadRepo(t.TempDir())
	if err != nil || len(warnings) != 0 || !reflect.DeepEqual(*cfg, Config{}) {
		t.Errorf("LoadRepo(empty dir) = %+v, %v, %v; want empty", cfg, warnings, err)
	}
}

func TestLoadRepoDropsMachineLocalKeysWithWarnings(t *testing.T) {
	dir := t.TempDir()
	content := `org_dir = "/elsewhere"
editor = "emacs"
vcs_editor = "gitui"
url_formatter = "sh -c evil"
format_links_url_formatter = "sh -c evil"
scratch_dir = "/tmp/x"
debug = true
inbox_file = "in.org"
agenda_window_days = 3

[gcalsync]
oauth_client_secret = "shh"

[colors]
todo_color = "#ff0000"

[[keywords]]
name = "TODO"
[[keywords]]
name = "DONE"
done = true
`
	if err := os.WriteFile(filepath.Join(dir, RepoFileName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, warnings, err := LoadRepo(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 8 {
		t.Errorf("warnings = %q, want one per machine-local key (8)", warnings)
	}
	for _, w := range warnings {
		if !strings.Contains(w, "machine-local") {
			t.Errorf("warning %q doesn't say why", w)
		}
	}
	if cfg.OrgDir != "" || cfg.Editor != "" || cfg.VCSEditor != "" || cfg.URLFormatter != "" || cfg.FormatLinksURLFormatter != "" || cfg.ScratchDir != "" || cfg.Debug || cfg.Gcalsync.OAuthClientSecret != "" {
		t.Errorf("machine-local keys kept: %+v", cfg)
	}
	if cfg.InboxFile != "in.org" || cfg.AgendaWindowDays != 3 || cfg.Colors.TODO != "#ff0000" || len(cfg.Keywords) != 2 {
		t.Errorf("shared keys lost: %+v", cfg)
	}
	if cfg.Keywords[0].Shortcut != "t" {
		t.Errorf("keywords not normalized: %+v", cfg.Keywords)
	}
}

func TestLoadRepoRejectsMalformedFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, RepoFileName), []byte("inbox_file = "), 0o644)
	if _, _, err := LoadRepo(dir); err == nil {
		t.Error("LoadRepo(malformed) succeeded, want an error")
	}
}

func TestOverlayMergesSectionsKeyByKeyAndRecordsSources(t *testing.T) {
	dst := Config{InboxFile: "a.org", Colors: ColorsConfig{TODO: "#111111", Done: "#222222"}}
	src := Config{AgendaWindowDays: 9, Colors: ColorsConfig{Done: "#333333"}, OutlineIgnoreFiles: []string{"x.org"}}
	sources := Sources{}
	Overlay(&dst, &src, LayerUser, sources)
	if dst.InboxFile != "a.org" || dst.AgendaWindowDays != 9 || dst.Colors.TODO != "#111111" || dst.Colors.Done != "#333333" || len(dst.OutlineIgnoreFiles) != 1 {
		t.Errorf("Overlay result = %+v", dst)
	}
	want := Sources{"agenda_window_days": LayerUser, "colors.done_color": LayerUser, "outline_ignore_files": LayerUser}
	if !reflect.DeepEqual(sources, want) {
		t.Errorf("sources = %v, want %v", sources, want)
	}
	MarkDefaults(&dst, sources)
	if sources.Of("inbox_file") != LayerDefault || sources.Of("colors.todo_color") != LayerDefault {
		t.Errorf("MarkDefaults didn't fill unset-but-present keys: %v", sources)
	}
}

// A committed .orgtd.toml arrives via git pull, so its file-name settings
// mustn't be able to point outside the org directory: orgtd writes to
// each of them.
func TestLoadRepoRejectsFileNamesOutsideOrgDir(t *testing.T) {
	for _, line := range []string{
		`calendar_file = "../.zshrc"`,
		`inbox_file = "/etc/passwd"`,
		`meeting_tags_file = "sub/tags.org"`,
		`calendar_file = "..\\evil.org"`,
		`inbox_file = ".."`,
		`meeting_tags_file = "."`,
	} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, RepoFileName), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := LoadRepo(dir); err == nil || !strings.Contains(err.Error(), "plain file name") {
			t.Errorf("LoadRepo with %s: err = %v, want a plain-file-name error", line, err)
		}
	}
}

func TestCheckFileNamesAcceptsPlainNames(t *testing.T) {
	c := Config{InboxFile: "in.org", CalendarFile: "cal..org", MeetingTagsFile: ".tags.org"}
	if err := c.CheckFileNames(); err != nil {
		t.Errorf("CheckFileNames(%+v) = %v, want nil", c, err)
	}
	if err := (&Config{}).CheckFileNames(); err != nil {
		t.Errorf("CheckFileNames(empty) = %v, want nil", err)
	}
}
