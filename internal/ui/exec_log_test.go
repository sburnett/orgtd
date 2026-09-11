package ui

import (
	"os/exec"
	"strings"
	"testing"
)

func TestExecLogNilSafe(t *testing.T) {
	var l *execLog
	l.append(execLogStart, 123, "should not panic")
	if got := l.snapshot(); got != nil {
		t.Errorf("snapshot() on a nil *execLog = %v, want nil", got)
	}
}

func TestExecLogAppendAndSnapshot(t *testing.T) {
	l := &execLog{}
	l.append(execLogStart, 111, "running foo")
	l.append(execLogStdout, 111, "line one")
	l.append(execLogStderr, 111, "a warning")
	l.append(execLogExit, 111, "exit code 0")

	got := l.snapshot()
	if len(got) != 4 {
		t.Fatalf("snapshot = %#v, want 4 entries", got)
	}
	wantKinds := []execLogKind{execLogStart, execLogStdout, execLogStderr, execLogExit}
	wantText := []string{"running foo", "line one", "a warning", "exit code 0"}
	for i := range got {
		if got[i].kind != wantKinds[i] {
			t.Errorf("entry %d kind = %v, want %v", i, got[i].kind, wantKinds[i])
		}
		if got[i].text != wantText[i] {
			t.Errorf("entry %d text = %q, want %q", i, got[i].text, wantText[i])
		}
		if got[i].pid != 111 {
			t.Errorf("entry %d pid = %d, want 111", i, got[i].pid)
		}
		if got[i].time.IsZero() {
			t.Errorf("entry %d has a zero timestamp", i)
		}
	}
}

func TestExecLogSnapshotIsACopy(t *testing.T) {
	l := &execLog{}
	l.append(execLogStart, 1, "first")
	snap := l.snapshot()
	l.append(execLogStart, 1, "second")
	if len(snap) != 1 {
		t.Errorf("earlier snapshot = %#v, should not see entries appended after it was taken", snap)
	}
}

func TestPidLabel(t *testing.T) {
	if got := pidLabel(0); got != "-" {
		t.Errorf("pidLabel(0) = %q, want %q (no process ever started)", got, "-")
	}
	if got := pidLabel(4242); got != "4242" {
		t.Errorf("pidLabel(4242) = %q, want %q", got, "4242")
	}
}

func TestExitCodeFromErrorNilIsZero(t *testing.T) {
	if got := exitCodeFromError(nil); got != 0 {
		t.Errorf("exitCodeFromError(nil) = %d, want 0", got)
	}
}

func TestExitCodeFromErrorExitError(t *testing.T) {
	script := writeFakeFormatter(t, "exit 3")
	_, err := runLoggedCommand(&execLog{}, script, nil, "")
	if err == nil {
		t.Fatal("expected an error from a script that exits 3")
	}
	if got := exitCodeFromError(err); got != 3 {
		t.Errorf("exitCodeFromError(exit 3) = %d, want 3", got)
	}
}

func TestExitCodeFromErrorUnstartableProgram(t *testing.T) {
	_, err := runLoggedCommand(&execLog{}, "/no/such/program/anywhere", nil, "")
	if err == nil {
		t.Fatal("expected an error running a nonexistent program")
	}
	if got := exitCodeFromError(err); got != -1 {
		t.Errorf("exitCodeFromError(start failure) = %d, want -1 (no real exit code)", got)
	}
}

func TestRunLoggedCommandRecordsStartWithPidAndArgsStdoutStderrAndExit(t *testing.T) {
	script := writeFakeFormatter(t, `echo "out line 1"; echo "err line 1" >&2; echo "out line 2"`)
	l := &execLog{}

	stdout, err := runLoggedCommand(l, script, []string{"arg1"}, "")
	if err != nil {
		t.Fatalf("runLoggedCommand: %v", err)
	}
	if stdout != "out line 1\nout line 2" {
		t.Errorf("stdout = %q", stdout)
	}

	entries := l.snapshot()
	if len(entries) != 5 { // start, 2 stdout, 1 stderr, exit
		t.Fatalf("entries = %#v, want 5", entries)
	}
	start := entries[0]
	if start.kind != execLogStart || !strings.Contains(start.text, "arg1") {
		t.Errorf("first entry = %#v, want a start entry mentioning the arg", start)
	}
	if start.pid == 0 {
		t.Errorf("start entry pid = 0, want the real (non-zero) child pid")
	}
	last := entries[len(entries)-1]
	if last.kind != execLogExit || last.text != "exit code 0" {
		t.Errorf("last entry = %#v, want exit code 0", last)
	}
	var sawStdout, sawStderr bool
	for _, e := range entries[1 : len(entries)-1] {
		if e.pid != start.pid {
			t.Errorf("entry %#v pid does not match the start entry's pid %d", e, start.pid)
		}
		switch e.kind {
		case execLogStdout:
			sawStdout = true
		case execLogStderr:
			sawStderr = true
			if e.text != "err line 1" {
				t.Errorf("stderr entry text = %q, want %q", e.text, "err line 1")
			}
		default:
			t.Errorf("unexpected entry kind in the middle: %#v", e)
		}
	}
	if !sawStdout || !sawStderr {
		t.Errorf("entries = %#v, want at least one stdout and one stderr entry", entries)
	}
	if last.pid != start.pid {
		t.Errorf("exit entry pid = %d, want it to match the start entry's pid %d", last.pid, start.pid)
	}
}

func TestRunLoggedCommandFeedsStdin(t *testing.T) {
	script := writeFakeFormatter(t, `cat`)
	stdout, err := runLoggedCommand(&execLog{}, script, nil, "hello from stdin")
	if err != nil {
		t.Fatalf("runLoggedCommand: %v", err)
	}
	if stdout != "hello from stdin" {
		t.Errorf("stdout = %q, want the echoed stdin", stdout)
	}
}

func TestRunLoggedCommandLogsStdinLines(t *testing.T) {
	script := writeFakeFormatter(t, `cat >/dev/null`)
	l := &execLog{}

	_, err := runLoggedCommand(l, script, nil, "line one\nline two\nline three\n")
	if err != nil {
		t.Fatalf("runLoggedCommand: %v", err)
	}

	entries := l.snapshot()
	var stdinLines []string
	var pid int
	for _, e := range entries {
		if e.kind == execLogStdin {
			stdinLines = append(stdinLines, e.text)
			pid = e.pid
		}
	}
	want := []string{"line one", "line two", "line three"}
	if len(stdinLines) != len(want) {
		t.Fatalf("stdin entries = %#v, want %v", stdinLines, want)
	}
	for i := range want {
		if stdinLines[i] != want[i] {
			t.Errorf("stdin entry %d = %q, want %q", i, stdinLines[i], want[i])
		}
	}
	if pid == 0 {
		t.Error("stdin entries should carry the real child pid")
	}
}

func TestRunLoggedCommandLogsNoStdinEntriesWhenStdinEmpty(t *testing.T) {
	script := writeFakeFormatter(t, `true`)
	l := &execLog{}

	if _, err := runLoggedCommand(l, script, nil, ""); err != nil {
		t.Fatalf("runLoggedCommand: %v", err)
	}

	for _, e := range l.snapshot() {
		if e.kind == execLogStdin {
			t.Errorf("unexpected stdin entry %#v when no stdin was given", e)
		}
	}
}

func TestRunLoggedCommandPopulatesExitErrorStderr(t *testing.T) {
	script := writeFakeFormatter(t, `echo "boom" >&2; exit 1`)
	_, err := runLoggedCommand(&execLog{}, script, nil, "")
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("err = %v (%T), want *exec.ExitError", err, err)
	}
	if got := strings.TrimSpace(string(exitErr.Stderr)); got != "boom" {
		t.Errorf("exitErr.Stderr = %q, want %q", got, "boom")
	}
	if got := exitCodeFromError(err); got != 1 {
		t.Errorf("exit code = %d, want 1", got)
	}
}

// TestRunLoggedCommandOnUnstartableProgramLogsOnlyAFailureExit guards a
// deliberate asymmetry: a process that never actually starts gets no
// start entry at all (there's no real pid, and no meaningful "started"
// moment) — just one exit-kind entry (pid "-") naming what was
// attempted and why it failed.
func TestRunLoggedCommandOnUnstartableProgramLogsOnlyAFailureExit(t *testing.T) {
	l := &execLog{}
	_, err := runLoggedCommand(l, "/no/such/program/anywhere", []string{"arg1"}, "")
	if err == nil {
		t.Fatal("expected an error")
	}
	entries := l.snapshot()
	if len(entries) != 1 {
		t.Fatalf("entries = %#v, want exactly 1 (no start entry for a process that never started)", entries)
	}
	e := entries[0]
	if e.kind != execLogExit || e.pid != 0 {
		t.Errorf("entry = %#v, want an exit entry with pid 0", e)
	}
	if !strings.Contains(e.text, "failed to start") || !strings.Contains(e.text, "arg1") {
		t.Errorf("entry text = %q, want it to mention the failure and the attempted arguments", e.text)
	}
}
