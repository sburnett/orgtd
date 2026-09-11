package ui

import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// execLogKind labels one execLogEntry.
type execLogKind int

const (
	execLogStart execLogKind = iota
	execLogStdout
	execLogStderr
	execLogExit
)

// label renders k for display in the :log view.
func (k execLogKind) label() string {
	switch k {
	case execLogStart:
		return "START"
	case execLogStdout:
		return "STDOUT"
	case execLogStderr:
		return "STDERR"
	case execLogExit:
		return "EXIT"
	default:
		return "?"
	}
}

// execLogEntry is one line of :log output: something that happened at
// time, tagged kind.
type execLogEntry struct {
	time time.Time
	kind execLogKind
	text string
}

// execLog collects every external command orgtd has run since startup —
// a start entry (the resolved command line), one entry per line of
// stdout/stderr as it's produced (each independently timestamped, not
// all at once when the command finishes), and an exit entry with the
// resulting exit code — for the :log command (see appendLogRows).
//
// A *execLog is shared, via its pointer, across every copy of Model
// (see New) — Model itself is copied on every Update, but the log
// behind it must not be. Safe for concurrent use: a :format-links batch
// runs its external process on its own goroutine (see startFormatLinks),
// potentially alongside a live in-editor formatter invocation running
// synchronously on the main goroutine.
type execLog struct {
	mu      sync.Mutex
	entries []execLogEntry
}

// append is a no-op on a nil *execLog — a Model constructed as a bare
// literal (as plenty of tests, and formerly all of them, do) rather
// than through New() has no log to append to; logging is simply
// disabled for it, the same way m.bareURLRe tolerates being unbuilt.
func (l *execLog) append(kind execLogKind, text string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, execLogEntry{time: time.Now(), kind: kind, text: text})
}

// snapshot returns a copy of every entry recorded so far, for :log to
// render — a copy so the caller (rendering, on the main goroutine) never
// has to hold the lock while it works, and so what it sees can't change
// out from under it if another command starts logging concurrently. nil
// on a nil *execLog (see append).
func (l *execLog) snapshot() []execLogEntry {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]execLogEntry, len(l.entries))
	copy(out, l.entries)
	return out
}

// exitCodeFromError returns the process exit code implied by err — the
// result of exec.Cmd.Wait(), or equivalently tea.ExecProcess's own
// callback: 0 for a nil err (success), the real code for an
// *exec.ExitError, or -1 for anything else (e.g. the program was never
// found or couldn't even start, so there's no real exit code to report).
func exitCodeFromError(err error) int {
	if err == nil {
		return 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode()
	}
	return -1
}

// maxLoggedLineSize bounds bufio.Scanner's per-line buffer in
// scanIntoLog, well above bufio.MaxScanTokenSize's 64KB default — a
// formatter emitting one very long line (a huge URL, a JSON blob)
// shouldn't silently truncate the rest of its output the way the
// default limit would.
const maxLoggedLineSize = 1 << 20

// runLoggedCommand starts name (with args), records it in elog — a
// start entry, every stdout/stderr line as it's produced (each with its
// own timestamp), and an exit entry — and returns once it exits:
// combined stdout (trimmed of its own trailing newline) and an error in
// exactly the shape exec.Cmd.Output() itself would produce (including
// an *exec.ExitError with Stderr populated on a non-zero exit), so
// callers built around that convention don't need to change. stdin, if
// non-empty, is written to the child's stdin and then closed; empty
// means the child gets no stdin at all (its stdin is simply closed
// immediately, same as exec.Cmd's own zero-value Stdin).
func runLoggedCommand(elog *execLog, name string, args []string, stdin string) (string, error) {
	cmd := exec.Command(name, args...)
	elog.append(execLogStart, strings.Join(append([]string{name}, args...), " "))

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		elog.append(execLogExit, fmt.Sprintf("failed to start: %v", err))
		return "", err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		elog.append(execLogExit, fmt.Sprintf("failed to start: %v", err))
		return "", err
	}
	var stdinPipe io.WriteCloser
	if stdin != "" {
		stdinPipe, err = cmd.StdinPipe()
		if err != nil {
			elog.append(execLogExit, fmt.Sprintf("failed to start: %v", err))
			return "", err
		}
	}

	if err := cmd.Start(); err != nil {
		elog.append(execLogExit, fmt.Sprintf("failed to start: %v", err))
		return "", err
	}

	if stdinPipe != nil {
		go func() {
			io.WriteString(stdinPipe, stdin)
			stdinPipe.Close()
		}()
	}

	var stdoutBuf, stderrBuf strings.Builder
	var wg sync.WaitGroup
	wg.Add(2)
	go scanIntoLog(stdoutPipe, elog, execLogStdout, &stdoutBuf, &wg)
	go scanIntoLog(stderrPipe, elog, execLogStderr, &stderrBuf, &wg)
	wg.Wait() // must finish reading before Wait(), per exec.Cmd's own doc comment

	waitErr := cmd.Wait()
	elog.append(execLogExit, fmt.Sprintf("exit code %d", exitCodeFromError(waitErr)))

	stdout := strings.TrimRight(stdoutBuf.String(), "\n")
	if waitErr != nil {
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			exitErr.Stderr = []byte(stderrBuf.String())
			return stdout, exitErr
		}
		return stdout, waitErr
	}
	return stdout, nil
}

// scanIntoLog reads r line by line, appending each (with a trailing
// newline, reconstructing the stream for callers that want the combined
// text) to buf, and individually — tagged kind, timestamped — to elog.
// One of the two goroutines runLoggedCommand starts per child process,
// one for stdout and one for stderr.
func scanIntoLog(r io.Reader, elog *execLog, kind execLogKind, buf *strings.Builder, wg *sync.WaitGroup) {
	defer wg.Done()
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLoggedLineSize)
	for scanner.Scan() {
		line := scanner.Text()
		buf.WriteString(line)
		buf.WriteByte('\n')
		elog.append(kind, line)
	}
}
