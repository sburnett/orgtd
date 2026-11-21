package ui

import (
	"os"
	"strings"
	"testing"

	"github.com/sburnett/orgtd/internal/config"
	"github.com/sburnett/orgtd/internal/workspace"
)

func TestRunRefusesUnknownCommand(t *testing.T) {
	m, _ := diskChangeFixture(t)
	m.cfg.Commands = []config.CommandConfig{{Name: "vcs", Command: "true"}, {Name: "sync", Command: "true"}}

	cmd := m.startRun("nope")

	if cmd != nil {
		t.Errorf("startRun launched something for an unknown name")
	}
	if !strings.Contains(m.message, `"nope"`) || !strings.Contains(m.message, "vcs, sync") {
		t.Errorf("message = %q, want the unknown name and the available ones", m.message)
	}
}

func TestRunWithoutNameShowsUsage(t *testing.T) {
	m, _ := diskChangeFixture(t)

	cmd := m.startRun("")

	if cmd != nil {
		t.Errorf("startRun launched something with no name")
	}
	if !strings.Contains(m.message, "Usage") || !strings.Contains(m.message, "[[commands]]") {
		t.Errorf("message = %q, want usage pointing at [[commands]]", m.message)
	}
}

func TestRunDispatchesFromCommandLine(t *testing.T) {
	m, _ := diskChangeFixture(t)
	m.cfg.Commands = []config.CommandConfig{{Name: "vcs", Command: "true"}}

	m.execCommand("run nope")

	if !strings.Contains(m.message, `"nope"`) {
		t.Errorf("message = %q, want :run to look up its argument", m.message)
	}
}

func TestRunCompletesCommandNames(t *testing.T) {
	m, _ := diskChangeFixture(t)
	m.cfg.Commands = []config.CommandConfig{{Name: "vcs", Command: "true"}, {Name: "sync", Command: "true"}, {Name: "vim", Command: "true"}}

	got := m.completeRunArg("v")

	if strings.Join(got, ",") != "vcs,vim" {
		t.Errorf("completeRunArg(\"v\") = %q, want [vcs vim]", got)
	}
}

func TestRunRefusesWhenSaveFails(t *testing.T) {
	m, path := diskChangeFixture(t)
	m.cfg.Commands = []config.CommandConfig{{Name: "vcs", Command: "true"}}

	cmd := m.startRun("vcs")

	if cmd != nil {
		t.Errorf("startRun launched despite a file changed on disk")
	}
	if !strings.Contains(m.message, "changed on disk") {
		t.Errorf("message = %q, want the save refusal", m.message)
	}
	if got := readFile(t, path); got != "* TODO Upstream title\n" {
		t.Errorf("disk = %q, want it untouched", got)
	}
}

func TestRunSavesBeforeLaunching(t *testing.T) {
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
	m.cfg.Commands = []config.CommandConfig{{Name: "vcs", Command: "true"}}
	m.cursor = findRow(t, m, "Old title")
	m = setStatus(m, "n")

	if cmd := m.startRun("vcs"); cmd == nil {
		t.Fatalf("startRun returned no command; message = %q", m.message)
	}

	if got := readFile(t, path); !strings.Contains(got, "NEXT Old title") {
		t.Errorf("disk = %q, want the unsaved edit written before launch", got)
	}
}

func TestRunReloadsFilesChangedOnDisk(t *testing.T) {
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

	res, _ := m.finishRun(runFinishedMsg{name: "vcs"})
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
