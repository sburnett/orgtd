package ui

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// exitErrorForTest returns a real *exec.ExitError with the given exit
// code, for tests that need to simulate an editor exiting non-zero
// without actually launching one.
func exitErrorForTest(t *testing.T, code int) error {
	t.Helper()
	err := exec.Command("sh", "-c", fmt.Sprintf("exit %d", code)).Run()
	if err == nil {
		t.Fatalf("expected sh to exit non-zero")
	}
	return err
}

func TestLogCommandSwitchesToLogView(t *testing.T) {
	ws := agendaFixture(t, "* TODO Something\n")
	m := New(ws)

	m = sendKey(m, ":")
	m = typeKeys(m, "log")
	m, _ = sendKeyCmd(m, "enter")

	if m.view != logView {
		t.Fatalf("view after :log = %v, want logView", m.view)
	}
}

func TestLogViewShowsPlaceholderWhenEmpty(t *testing.T) {
	ws := agendaFixture(t, "* TODO Something\n")
	m := New(ws)
	m.switchToView(logView)

	if len(m.rows) != 1 || !strings.Contains(m.rows[0].text, "No external commands") {
		t.Errorf("rows = %#v, want a single placeholder row", m.rows)
	}
}

// timestampPrefixRe matches appendLogRows' leading "HH:MM:SS.mmm" stamp.
var timestampPrefixRe = regexp.MustCompile(`^\d{2}:\d{2}:\d{2}\.\d{3}\s+`)

func TestLogViewShowsRecordedEntriesWithTimestampsAndKinds(t *testing.T) {
	script := writeFakeFormatter(t, `echo "an output line"`)
	m := New(agendaFixture(t, "* TODO x\n"), WithURLFormatter(script))

	m.runURLFormatter("https://example.com")
	m.switchToView(logView)

	var sawStart, sawStdout, sawExit bool
	for _, r := range m.rows {
		if !timestampPrefixRe.MatchString(r.text) {
			t.Errorf("row = %q, want it to start with an HH:MM:SS.mmm timestamp", r.text)
			continue
		}
		switch {
		case strings.Contains(r.text, "START") && strings.Contains(r.text, script):
			sawStart = true
		case strings.Contains(r.text, "STDOUT") && strings.Contains(r.text, "an output line"):
			sawStdout = true
		case strings.Contains(r.text, "EXIT") && strings.Contains(r.text, "exit code 0"):
			sawExit = true
		}
	}
	if !sawStart || !sawStdout || !sawExit {
		t.Errorf("rows = %#v, missing a START, STDOUT, or EXIT entry", m.rows)
	}
}

func TestLogViewShowsStderrEntries(t *testing.T) {
	script := writeFakeFormatter(t, `echo "a warning" >&2; echo "[[$1][Formatted]]"`)
	m := New(agendaFixture(t, "* TODO x\n"), WithURLFormatter(script))

	m.runURLFormatter("https://example.com")
	m.switchToView(logView)

	var sawStderr bool
	for _, r := range m.rows {
		if strings.Contains(r.text, "STDERR") && strings.Contains(r.text, "a warning") {
			sawStderr = true
		}
	}
	if !sawStderr {
		t.Errorf("rows = %#v, want a STDERR entry for the warning", m.rows)
	}
}

func TestLogViewIncludesBatchFormatLinksRuns(t *testing.T) {
	script := writeBatchFakeFormatter(t, `echo "[[$line][Formatted]]"`)
	m := New(agendaFixture(t, "* TODO url https://example.com/a\n"), WithURLFormatter(script))

	cmd := m.startFormatLinks()
	msg := cmd().(formatLinksMsg)
	updated, _ := m.Update(msg)
	m = updated.(Model)
	m.switchToView(logView)

	var sawStart, sawExit bool
	for _, r := range m.rows {
		if strings.Contains(r.text, "START") && strings.Contains(r.text, script) {
			sawStart = true
		}
		if strings.Contains(r.text, "EXIT") && strings.Contains(r.text, "exit code 0") {
			sawExit = true
		}
	}
	if !sawStart || !sawExit {
		t.Errorf("rows = %#v, missing the batch formatter's START/EXIT entries", m.rows)
	}
}

func TestLogViewIncludesEditorStartAndExit(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")

	cmd := m.startEdit()
	if cmd == nil {
		t.Fatal("expected a non-nil edit command")
	}
	// Deliberately not invoking cmd() (that would launch a real editor);
	// launchEditor already logs the start entry synchronously, before
	// returning the tea.ExecProcess command.
	updated, _ := m.Update(editFinishedMsg{path: writeTempOrgFile(t, "* TODO Call the vet about Fido's checkup\n"), target: m.currentHeadline()})
	m = updated.(Model)
	m.switchToView(logView)

	var sawStart, sawExit bool
	for _, r := range m.rows {
		if strings.Contains(r.text, "START") {
			sawStart = true
		}
		if strings.Contains(r.text, "EXIT") && strings.Contains(r.text, "exit code 0") {
			sawExit = true
		}
	}
	if !sawStart {
		t.Errorf("rows = %#v, want a START entry for the editor", m.rows)
	}
	if !sawExit {
		t.Errorf("rows = %#v, want an EXIT entry for the editor", m.rows)
	}
}

func TestLogViewRecordsNonZeroEditorExitCode(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	h := m.currentHeadline()

	m.startEdit()
	updated, _ := m.Update(editFinishedMsg{path: "/does/not/matter", target: h, err: exitErrorForTest(t, 7)})
	m = updated.(Model)
	m.switchToView(logView)

	var sawExit7 bool
	for _, r := range m.rows {
		if strings.Contains(r.text, "EXIT") && strings.Contains(r.text, "exit code 7") {
			sawExit7 = true
		}
	}
	if !sawExit7 {
		t.Errorf("rows = %#v, want an EXIT entry with code 7", m.rows)
	}
}
