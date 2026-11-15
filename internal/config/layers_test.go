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
	if len(warnings) != 6 {
		t.Errorf("warnings = %q, want one per machine-local key (6)", warnings)
	}
	for _, w := range warnings {
		if !strings.Contains(w, "machine-local") {
			t.Errorf("warning %q doesn't say why", w)
		}
	}
	if cfg.OrgDir != "" || cfg.Editor != "" || cfg.VCSEditor != "" || cfg.ScratchDir != "" || cfg.Debug || cfg.Gcalsync.OAuthClientSecret != "" {
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
