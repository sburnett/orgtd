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
	execLogStdin
	execLogStdout
	execLogStderr
	execLogExit
)

// label renders k for display in the :log view.
func (k execLogKind) label() string {
	switch k {
	case execLogStart:
		return "START"
	case execLogStdin:
		return "STDIN"
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
// time, tagged kind. pid is 0 if the process never actually started
// (e.g. an execLogExit entry recording a failure to even launch it) —
// see pidLabel for how that's distinguished from a real, if unlikely,
// pid of 0 in the rendered view.
type execLogEntry struct {
	time time.Time
	kind execLogKind
	pid  int
	text string
}

// pidLabel renders pid for display: "-" for 0 (no process ever
// started), the number otherwise.
func pidLabel(pid int) string {
	if pid == 0 {
		return "-"
	}
	return fmt.Sprintf("%d", pid)
}

// execLog collects every external command orgtd has run since startup —
// a start entry (its pid and full argument list), one entry per line
// fed to its stdin (if any), one entry per line of stdout/stderr as
// it's produced (each independently timestamped, not all at once when
// the command finishes), and an exit entry with the resulting exit
// code — for the :log command (see appendLogRows). Every entry but a
// "failed to even start" exit carries the pid of the process it came
// from, so entries from two commands that happen to run concurrently
// (e.g. a :format-links batch alongside a live in-editor formatter
// invocation) can still be told apart in the merged timeline.
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
func (l *execLog) append(kind execLogKind, pid int, text string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, execLogEntry{time: time.Now(), kind: kind, pid: pid, text: text})
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
// start entry (once actually running, so its pid is known and real —
// see logStartFailure for the alternative when it never gets that far),
// one entry per line of stdin (if any) up front, every stdout/stderr
// line as it's produced (each with its own timestamp), and an exit
// entry — and returns once it exits: combined stdout (trimmed of its
// own trailing newline) and an error in exactly the shape
// exec.Cmd.Output() itself would produce (including an *exec.ExitError
// with Stderr populated on a non-zero exit), so callers built around
// that convention don't need to change. stdin, if non-empty, is written
// to the child's stdin and then closed; empty means the child gets no
// stdin at all (its stdin is simply closed immediately, same as
// exec.Cmd's own zero-value Stdin) and nothing is logged for it.
func runLoggedCommand(elog *execLog, name string, args []string, stdin string) (string, error) {
	cmd := exec.Command(name, args...)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		logStartFailure(elog, name, args, err)
		return "", err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		logStartFailure(elog, name, args, err)
		return "", err
	}
	var stdinPipe io.WriteCloser
	if stdin != "" {
		stdinPipe, err = cmd.StdinPipe()
		if err != nil {
			logStartFailure(elog, name, args, err)
			return "", err
		}
	}

	if err := cmd.Start(); err != nil {
		logStartFailure(elog, name, args, err)
		return "", err
	}

	pid := cmd.Process.Pid
	elog.append(execLogStart, pid, strings.Join(append([]string{name}, args...), " "))

	if stdinPipe != nil {
		// Logged up front, all at once — unlike stdout/stderr, the whole
		// content is already in hand rather than arriving progressively
		// from the child, so there's nothing to wait on before recording
		// it (writing it to the pipe happens concurrently, below).
		for _, line := range strings.Split(strings.TrimRight(stdin, "\n"), "\n") {
			elog.append(execLogStdin, pid, line)
		}
		go func() {
			io.WriteString(stdinPipe, stdin)
			stdinPipe.Close()
		}()
	}

	var stdoutBuf, stderrBuf strings.Builder
	var wg sync.WaitGroup
	wg.Add(2)
	go scanIntoLog(stdoutPipe, elog, execLogStdout, pid, &stdoutBuf, &wg)
	go scanIntoLog(stderrPipe, elog, execLogStderr, pid, &stderrBuf, &wg)
	wg.Wait() // must finish reading before Wait(), per exec.Cmd's own doc comment

	waitErr := cmd.Wait()
	elog.append(execLogExit, pid, fmt.Sprintf("exit code %d", exitCodeFromError(waitErr)))

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

// logStartFailure records a command that never actually started (pipe
// setup or Start() itself failed) as a single exit entry — pid 0, since
// none was ever assigned — naming what was being attempted (including
// its arguments) alongside why. There's deliberately no separate start
// entry in this case: a "start" that never happened isn't one.
func logStartFailure(elog *execLog, name string, args []string, err error) {
	elog.append(execLogExit, 0, fmt.Sprintf("failed to start %s: %v", strings.Join(append([]string{name}, args...), " "), err))
}

// scanIntoLog reads r line by line, appending each (with a trailing
// newline, reconstructing the stream for callers that want the combined
// text) to buf, and individually — tagged kind, timestamped, with pid —
// to elog. One of the two goroutines runLoggedCommand starts per child
// process, one for stdout and one for stderr.
func scanIntoLog(r io.Reader, elog *execLog, kind execLogKind, pid int, buf *strings.Builder, wg *sync.WaitGroup) {
	defer wg.Done()
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLoggedLineSize)
	for scanner.Scan() {
		line := scanner.Text()
		buf.WriteString(line)
		buf.WriteByte('\n')
		elog.append(kind, pid, line)
	}
}

// logCompletedProcess records an already-finished process in elog — a
// start entry (its pid and full argument list) if it actually started,
// then an exit entry either way (pid 0, and err's own message standing
// in for a real exit code, if it never did). Used for $EDITOR
// invocations (see launchEditor/startEditFile): tea.ExecProcess gives no
// hook for "the process just started", only a callback once it's
// finished, so unlike runLoggedCommand's own live start entry, both of
// these are necessarily logged together, after the fact — cmd.Process
// (populated by Start(), called internally by tea.ExecProcess on the
// very *exec.Cmd passed to it) is the only place the real pid comes
// from, and it's only readable once we're back here.
func logCompletedProcess(elog *execLog, cmd *exec.Cmd, err error) {
	pid := 0
	if cmd != nil && cmd.Process != nil {
		pid = cmd.Process.Pid
		elog.append(execLogStart, pid, strings.Join(cmd.Args, " "))
	}
	elog.append(execLogExit, pid, fmt.Sprintf("exit code %d", exitCodeFromError(err)))
}
