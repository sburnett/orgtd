package ui

import (
	"os"
	"strings"
	"testing"

	"github.com/sburnett/orgtd/internal/workspace"
)

func TestVCSRefusesWithoutConfiguredEditor(t *testing.T) {
	m, _ := diskChangeFixture(t)

	cmd := m.startVCS()

	if cmd != nil {
		t.Errorf("startVCS launched something with no vcs_editor set")
	}
	if !strings.Contains(m.message, "vcs_editor") {
		t.Errorf("message = %q, want it to mention vcs_editor", m.message)
	}
}

func TestVCSRefusesWhenSaveFails(t *testing.T) {
	m, path := diskChangeFixture(t)
	m.cfg.VCSEditor = "true"

	cmd := m.startVCS()

	if cmd != nil {
		t.Errorf("startVCS launched despite a file changed on disk")
	}
	if !strings.Contains(m.message, "changed on disk") {
		t.Errorf("message = %q, want the save refusal", m.message)
	}
	if got := readFile(t, path); got != "* TODO Upstream title\n" {
		t.Errorf("disk = %q, want it untouched", got)
	}
}

func TestVCSSavesBeforeLaunching(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/todo.org"
	if err := os.WriteFile(path, []byte("* TODO Old title\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws, err := workspace.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	m := New(ws)
	m.cfg.VCSEditor = "true"
	m.cursor = findRow(t, m, "Old title")
	m = setStatus(m, "n")

	if cmd := m.startVCS(); cmd == nil {
		t.Fatalf("startVCS returned no command; message = %q", m.message)
	}

	if got := readFile(t, path); !strings.Contains(got, "NEXT Old title") {
		t.Errorf("disk = %q, want the unsaved edit written before launch", got)
	}
}

func TestVCSReloadsFilesChangedOnDisk(t *testing.T) {
	dir := t.TempDir()
	changed, untouched := dir+"/a.org", dir+"/b.org"
	for p, c := range map[string]string{changed: "* TODO Old title\n", untouched: "* TODO Other\n"} {
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ws, err := workspace.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	m := New(ws)
	other := ws.Files[1]
	if err := os.WriteFile(changed, []byte("* TODO Pulled title\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, _ := m.finishVCS(vcsFinishedMsg{})
	m = res.(Model)

	if findRowOrNeg(m, "Pulled title") < 0 || findRowOrNeg(m, "Old title") >= 0 {
		t.Errorf("rows don't reflect the file reloaded from disk: %#v", m.rows)
	}
	if m.ws.Files[1] != other {
		t.Errorf("a file that didn't change on disk was reloaded")
	}
	if !strings.Contains(m.message, "a.org") || strings.Contains(m.message, "b.org") {
		t.Errorf("message = %q, want only a.org reported reloaded", m.message)
	}
	if len(m.dirty) != 0 {
		t.Errorf("dirty = %v, want nothing dirty after a reload", m.dirty)
	}
}

func findRowOrNeg(m Model, text string) int {
	for i, r := range m.rows {
		if strings.Contains(stripANSI(r.text), text) || (r.headline != nil && strings.Contains(r.headline.Title, text)) {
			return i
		}
	}
	return -1
}
