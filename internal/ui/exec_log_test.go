package ui

import (
	"os/exec"
	"strings"
	"testing"
)

func TestExecLogNilSafe(t *testing.T) {
	var l *execLog
	l.append(execLogStart, "should not panic")
	if got := l.snapshot(); got != nil {
		t.Errorf("snapshot() on a nil *execLog = %v, want nil", got)
	}
}

func TestExecLogAppendAndSnapshot(t *testing.T) {
	l := &execLog{}
	l.append(execLogStart, "running foo")
	l.append(execLogStdout, "line one")
	l.append(execLogStderr, "a warning")
	l.append(execLogExit, "exit code 0")

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
		if got[i].time.IsZero() {
			t.Errorf("entry %d has a zero timestamp", i)
		}
	}
}

func TestExecLogSnapshotIsACopy(t *testing.T) {
	l := &execLog{}
	l.append(execLogStart, "first")
	snap := l.snapshot()
	l.append(execLogStart, "second")
	if len(snap) != 1 {
		t.Errorf("earlier snapshot = %#v, should not see entries appended after it was taken", snap)
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

func TestRunLoggedCommandRecordsStartStdoutStderrAndExit(t *testing.T) {
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
	if entries[0].kind != execLogStart || !strings.Contains(entries[0].text, "arg1") {
		t.Errorf("first entry = %#v, want a start entry mentioning the arg", entries[0])
	}
	last := entries[len(entries)-1]
	if last.kind != execLogExit || last.text != "exit code 0" {
		t.Errorf("last entry = %#v, want exit code 0", last)
	}
	var sawStdout, sawStderr bool
	for _, e := range entries[1 : len(entries)-1] {
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

func TestRunLoggedCommandOnUnstartableProgramLogsStartAndFailure(t *testing.T) {
	l := &execLog{}
	_, err := runLoggedCommand(l, "/no/such/program/anywhere", nil, "")
	if err == nil {
		t.Fatal("expected an error")
	}
	entries := l.snapshot()
	if len(entries) != 2 {
		t.Fatalf("entries = %#v, want 2 (start, exit/failure)", entries)
	}
	if entries[0].kind != execLogStart {
		t.Errorf("first entry = %#v, want a start entry", entries[0])
	}
	if entries[1].kind != execLogExit || !strings.Contains(entries[1].text, "failed to start") {
		t.Errorf("second entry = %#v, want a failure exit entry", entries[1])
	}
}
