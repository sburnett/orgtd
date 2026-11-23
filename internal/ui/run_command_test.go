package ui

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/sburnett/orgtd/internal/config"
	"github.com/sburnett/orgtd/internal/gitstore"
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
		t.Errorf("startRun launched despite a conflict")
	}
	if !strings.Contains(m.message, "Conflict in todo.org") {
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
	initRepo(t, dir)
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
	initRepo(t, dir)
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

// contextFixture is a workspace with one nested entry, and a :run
// command ("dump") that records what it was given: the context JSON in
// ctx.json, its ORGTD_* environment in env.txt, and its arguments in
// args.txt, all in the org directory.
func contextFixture(t *testing.T, withContext bool) (Model, string) {
	t.Helper()
	// Stale context, as orgtd inherits when an outer orgtd's :run (or a
	// session it launched) started it: none of it may reach the command,
	// ORGTD_DIR aside without context.
	for _, k := range runContextVars {
		t.Setenv(k, "stale")
	}
	dir := t.TempDir()
	content := "#+TITLE: Work\n" +
		"* Work\n" +
		"** Q4\n" +
		"*** NEXT [#A] Write report   :writing:\n" +
		"    SCHEDULED: <2026-11-24 Tue>\n" +
		"    :PROPERTIES:\n" +
		"    :ID: abc\n" +
		"    :END:\n" +
		"Draft it.\n" +
		"*** TODO Review budget\n"
	if err := os.WriteFile(dir+"/work.org", []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	initRepo(t, dir)
	ws, err := workspace.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	m := New(ws)
	m.cfg.Commands = []config.CommandConfig{{
		Name:    "dump",
		Command: `sh -c 'if [ -n "$ORGTD_CONTEXT" ]; then cp "$ORGTD_CONTEXT" ctx.json; fi; env | grep ^ORGTD_ | sort > env.txt; printf "%s\n" "$@" > args.txt' sh`,
		Context: withContext,
	}}
	return m, dir
}

// runDump runs ":run <line>" to completion, the way startRun and
// finishRun would around the terminal handover.
func runDump(t *testing.T, m Model, line string) Model {
	t.Helper()
	name, cmd, contextFile, ok := m.prepareRun(line)
	if !ok {
		t.Fatalf("prepareRun(%q) refused: %s", line, m.message)
	}
	err := cmd.Run()
	res, _ := m.finishRun(runFinishedMsg{name: name, cmd: cmd, err: err, contextFile: contextFile})
	if err != nil {
		t.Fatalf("command failed: %v", err)
	}
	if contextFile != "" {
		if _, err := os.Stat(contextFile); !os.IsNotExist(err) {
			t.Errorf("context file %s not removed after the command exited", contextFile)
		}
	}
	return res.(Model)
}

func readEnv(t *testing.T, dir string) map[string]string {
	t.Helper()
	env := map[string]string{}
	for _, l := range strings.Split(strings.TrimSpace(readFile(t, dir+"/env.txt")), "\n") {
		if k, v, ok := strings.Cut(l, "="); ok {
			env[k] = v
		}
	}
	return env
}

func TestRunPassesCursorEntryAsContext(t *testing.T) {
	m, dir := contextFixture(t, true)
	m.cursor = findRow(t, m, "Write report")

	runDump(t, m, "dump")

	env := readEnv(t, dir)
	want := map[string]string{
		"ORGTD_DIR":          dir,
		"ORGTD_VIEW":         "outline",
		"ORGTD_COUNT":        "1",
		"ORGTD_FILE":         dir + "/work.org",
		"ORGTD_LINE":         "4",
		"ORGTD_TITLE":        "Write report",
		"ORGTD_TODO":         "NEXT",
		"ORGTD_TAGS":         ":writing:",
		"ORGTD_OUTLINE_PATH": "work.org/Work/Q4/Write report",
		"ORGTD_ID":           "abc",
	}
	for k, v := range want {
		if env[k] != v {
			t.Errorf("%s = %q, want %q", k, env[k], v)
		}
	}
	if env["ORGTD_CONTEXT"] == "" {
		t.Error("ORGTD_CONTEXT not set")
	}

	var ctx runContext
	if err := json.Unmarshal([]byte(readFile(t, dir+"/ctx.json")), &ctx); err != nil {
		t.Fatal(err)
	}
	if len(ctx.Entries) != 1 {
		t.Fatalf("entries = %+v, want one", ctx.Entries)
	}
	e := ctx.Entries[0]
	if e.Line != 4 || e.Level != 3 || e.Priority != "A" || e.Scheduled != "<2026-11-24 Tue>" || e.Body != "Draft it." || e.Properties["ID"] != "abc" {
		t.Errorf("entry = %+v", e)
	}
	if len(e.Ancestors) != 2 || e.Ancestors[0].Title != "Work" || e.Ancestors[0].Line != 2 || e.Ancestors[1].Title != "Q4" || e.Ancestors[1].Line != 3 {
		t.Errorf("ancestors = %+v, want Work (line 2), Q4 (line 3)", e.Ancestors)
	}
	if !strings.HasPrefix(e.Raw, "*** NEXT [#A] Write report") {
		t.Errorf("raw = %q", e.Raw)
	}
}

func TestRunContextLinesMatchDiskAfterSavingEdits(t *testing.T) {
	m, dir := contextFixture(t, true)
	m.cursor = findRow(t, m, "Review budget")
	// The edit makes :run save the file before launching; the reported
	// line must be the entry's line in the file as written. (Which line
	// numbers apply when is covered by org's HeadlineLines tests.)
	m = setStatus(m, "n")

	runDump(t, m, "dump")

	env := readEnv(t, dir)
	lines := strings.Split(readFile(t, dir+"/work.org"), "\n")
	n, _ := strconv.Atoi(env["ORGTD_LINE"])
	if n < 1 || n > len(lines) || !strings.Contains(lines[n-1], "Review budget") {
		t.Errorf("ORGTD_LINE = %q, but that line of the saved file isn't the entry: %q", env["ORGTD_LINE"], lines)
	}
}

// commitOutside writes content to dir/name and commits it to the main
// branch, as another instance's save or an Emacs edit would.
func commitOutside(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := gitstore.Open(dir, "main", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.CommitOutsideEdits(); err != nil {
		t.Fatal(err)
	}
}

// openStore opens m's store now, so it records the main branch as it is
// before a test commits to it behind m's back.
func openStore(t *testing.T, m *Model) {
	t.Helper()
	if _, err := m.gitStore(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.git.store.Close() })
}

// checkContextEntry checks that the :run whose output is in dir got
// title, in file, at its line there on disk.
func checkContextEntry(t *testing.T, dir, file, title string) {
	t.Helper()
	env := readEnv(t, dir)
	if want := filepath.Join(dir, file); env["ORGTD_FILE"] != want {
		t.Errorf("ORGTD_FILE = %q, want %q", env["ORGTD_FILE"], want)
	}
	lines := strings.Split(readFile(t, filepath.Join(dir, file)), "\n")
	n, _ := strconv.Atoi(env["ORGTD_LINE"])
	if n < 1 || n > len(lines) || !strings.Contains(lines[n-1], title) {
		t.Errorf("ORGTD_LINE = %q, but that line of %s isn't %q: %q", env["ORGTD_LINE"], file, title, lines)
	}
	var ctx runContext
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(dir, "ctx.json"))), &ctx); err != nil {
		t.Fatal(err)
	}
	if len(ctx.Entries) != 1 || ctx.Entries[0].File != filepath.Join(dir, file) || ctx.Entries[0].Line != n {
		t.Errorf("context entries = %+v, want %q at line %d of %s", ctx.Entries, title, n, file)
	}
}

// workWithInbox is contextFixture's work.org with an entry added above
// the others, moving every line after it down.
const workWithInbox = "#+TITLE: Work\n" +
	"* Inbox\n" +
	"* Work\n" +
	"** Q4\n" +
	"*** NEXT [#A] Write report   :writing:\n" +
	"    SCHEDULED: <2026-11-24 Tue>\n" +
	"    :PROPERTIES:\n" +
	"    :ID: abc\n" +
	"    :END:\n" +
	"Draft it.\n" +
	"*** TODO Review budget\n"

func TestRunContextFollowsTargetMergedBySave(t *testing.T) {
	m, dir := contextFixture(t, true)
	openStore(t, &m)
	m.cursor = findRow(t, m, "Review budget")
	m = setStatus(m, "n")
	// The save merges this in, replacing work.org's tree — the target
	// with it.
	commitOutside(t, dir, "work.org", workWithInbox)

	runDump(t, m, "dump")

	checkContextEntry(t, dir, "work.org", "Review budget")
}

func TestRunContextFollowsTargetReloadedBySave(t *testing.T) {
	m, dir := contextFixture(t, true)
	commitOutside(t, dir, "other.org", "* TODO Elsewhere\n")
	ws, err := workspace.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	cmds := m.cfg.Commands
	m = New(ws)
	m.cfg.Commands = cmds
	openStore(t, &m)
	// The unsaved edit is in other.org; work.org, the target's file, has
	// none, but the save reloads it with what reached the branch.
	m.cursor = findRow(t, m, "Elsewhere")
	m = setStatus(m, "n")
	m.cursor = findRow(t, m, "Write report")
	commitOutside(t, dir, "work.org", workWithInbox)

	runDump(t, m, "dump")

	checkContextEntry(t, dir, "work.org", "Write report")
}

func TestRunRefusesWhenSaveLosesTarget(t *testing.T) {
	m, dir := contextFixture(t, true)
	openStore(t, &m)
	m.cursor = findRow(t, m, "Review budget")
	m = setStatus(m, "n")
	m.cursor = findRow(t, m, "Write report")
	// The save merges in a rename of the target: rather than guess which
	// entry the command should get, :run doesn't run it.
	commitOutside(t, dir, "work.org", strings.Replace(workWithInbox, "Write report", "Write the report", 1))

	_, cmd, contextFile, ok := m.prepareRun("dump")

	if ok || cmd != nil || contextFile != "" {
		t.Fatalf("prepareRun ran the command with its target gone")
	}
	if !strings.Contains(m.message, "Wrote work.org") || !strings.Contains(m.message, `"Write report" can't be found`) {
		t.Errorf("message = %q, want the save and why the command didn't run", m.message)
	}
}

func TestRunPassesArguments(t *testing.T) {
	m, dir := contextFixture(t, false)

	runDump(t, m, `dump "hello world" two`)

	if got := readFile(t, dir+"/args.txt"); got != "hello world\ntwo\n" {
		t.Errorf("args = %q, want the two arguments", got)
	}
}

func TestRunWithoutContextSetsNothing(t *testing.T) {
	m, dir := contextFixture(t, false)
	m.cursor = findRow(t, m, "Write report")

	runDump(t, m, "dump")

	want := map[string]string{"ORGTD_DIR": "stale"}
	if env := readEnv(t, dir); !maps.Equal(env, want) {
		t.Errorf("env = %v, want only orgtd's own ORGTD_DIR (%v) without context = true", env, want)
	}
}

func TestRunFromVisualSelectionPassesEverySelectedEntry(t *testing.T) {
	m, dir := contextFixture(t, true)
	m.cursor = findRow(t, m, "Write report")
	m = sendKey(m, "V")
	m = sendKey(m, "j")
	m = sendKey(m, ":")
	if m.mode != commandMode || len(m.commandSelection) != 2 {
		t.Fatalf("after V j :, mode = %v, selection = %d entries; want command mode with 2", m.mode, len(m.commandSelection))
	}

	m = runDump(t, m, "dump")

	env := readEnv(t, dir)
	if env["ORGTD_COUNT"] != "2" || env["ORGTD_TITLE"] != "" || env["ORGTD_FILE"] != "" {
		t.Errorf("env = %v, want a count of 2 and no single-entry variables", env)
	}
	var ctx runContext
	if err := json.Unmarshal([]byte(readFile(t, dir+"/ctx.json")), &ctx); err != nil {
		t.Fatal(err)
	}
	if len(ctx.Entries) != 2 || ctx.Entries[0].Title != "Write report" || ctx.Entries[1].Title != "Review budget" {
		t.Errorf("entries = %+v, want both selected entries in order", ctx.Entries)
	}
	if m.commandSelection != nil {
		t.Error("selection kept after :run, so a later :run from normal mode would reuse it")
	}
}

func TestRunOnFileHeaderPassesFileOnly(t *testing.T) {
	m, dir := contextFixture(t, true)
	m.cursor = findFileRow(t, m, "work.org")

	runDump(t, m, "dump")

	env := readEnv(t, dir)
	if env["ORGTD_COUNT"] != "0" || env["ORGTD_FILE"] != dir+"/work.org" || env["ORGTD_TITLE"] != "" {
		t.Errorf("env = %v, want the file and no entries", env)
	}
}
