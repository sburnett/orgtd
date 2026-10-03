package extprog

import (
	"errors"
	"fmt"
	"log"
	"os/exec"
	"regexp"
	"strings"

	"github.com/sburnett/orgtd/internal/execlog"
	"github.com/sburnett/orgtd/internal/org"
)

// ErrEmptyCommand is returned by RunFormatter and RunBatchFormatter when
// the configured formatter command has no words in it.
var ErrEmptyCommand = errors.New("url formatter command is empty")

// ErrNoOutput is returned by RunFormatter when the formatter ran
// successfully but printed nothing, so there's nothing to substitute for
// the URL.
var ErrNoOutput = errors.New("URL formatter produced no output")

// RunFormatter invokes urlFormatterCmd as `<program> <extra args...>
// <url>` and returns its trimmed stdout. The command is split with
// ParseCommand — quote-aware, with a leading "~" expanded in each word —
// without which a formatter configured with any extra arguments, or with
// a path under the home directory, would fail every time: exec.Command
// treats its first argument as a literal executable name, so an unsplit
// "myformatter -x" means "look for a program literally named
// 'myformatter -x'", and an unexpanded "~/bin/myformatter" (correct on
// the command line, where the shell expands it, but not from the config
// file, where nothing does) means "look for a program literally named
// '~/bin/myformatter'" — neither ever exists. Every attempt is logged via
// the standard log package, including the subprocess's stderr on failure
// — cmd/orgtd redirects it to a file at startup, since the TUI itself
// owns the terminal and plain log output can't share it. The run goes
// through execlog.Run, which also records it (start, every output line,
// and its exit code) in elog for :log.
//
// Errors: ErrEmptyCommand if urlFormatterCmd is blank, ErrNoOutput if the
// formatter printed nothing, and otherwise an error whose text is the
// failure detail (including the subprocess's stderr, when it had any).
func RunFormatter(elog *execlog.Log, urlFormatterCmd, url string) (string, error) {
	fields := ParseCommand(urlFormatterCmd)
	if len(fields) == 0 {
		log.Printf("url formatter: urlFormatterCmd %q has no fields after splitting; skipping %q", urlFormatterCmd, url)
		return "", ErrEmptyCommand
	}
	args := append(append([]string{}, fields[1:]...), url)
	log.Printf("url formatter: running %v", append([]string{fields[0]}, args...))

	out, err := execlog.Run(elog, fields[0], args, "")
	if err != nil {
		detail := err.Error()
		if exitErr, ok := err.(*exec.ExitError); ok && len(exitErr.Stderr) > 0 {
			detail = fmt.Sprintf("%v (stderr: %s)", err, strings.TrimSpace(string(exitErr.Stderr)))
		}
		log.Printf("url formatter: %s failed: %s", fields[0], detail)
		return "", fmt.Errorf("%s", detail)
	}

	formatted := strings.TrimSpace(out)
	if formatted == "" {
		log.Printf("url formatter: %s produced no output for %q", fields[0], url)
		return "", ErrNoOutput
	}
	log.Printf("url formatter: %s -> %q", fields[0], formatted)
	return formatted, nil
}

// defaultURLSchemes are always recognized as bare-URL prefixes,
// independent of whatever extra prefixes the user configures (see
// WithURLFormatterPrefixes) for things like a shortlink service
// ("bit.ly/...") or an internal go-link convention ("go/...") that don't
// carry a scheme.
var defaultURLSchemes = []string{"https://", "http://"}

// BareURLRegexp compiles the regexp formatURLs uses to find a bare
// URL not already wrapped in link brackets: one of defaultURLSchemes, or
// one of extraPrefixes, followed by a run of non-whitespace,
// non-bracket characters (stopping at '[' or ']' so a match can never
// span into or out of an org-mode link). Each extra prefix is guarded
// with \b so it only matches at a word boundary — without that, a short
// prefix like "go/" would also match mid-word inside something like
// "embargo/foo". The built-in schemes don't need this guard: nothing
// realistic precedes "https://" mid-word.
func BareURLRegexp(extraPrefixes []string) *regexp.Regexp {
	alts := make([]string, 0, len(defaultURLSchemes)+len(extraPrefixes))
	for _, p := range defaultURLSchemes {
		alts = append(alts, regexp.QuoteMeta(p))
	}
	for _, p := range extraPrefixes {
		if p == "" {
			continue
		}
		alts = append(alts, `\b`+regexp.QuoteMeta(p))
	}
	return regexp.MustCompile(`(?:` + strings.Join(alts, "|") + `)[^\s\[\]]+`)
}

// BareURLSpans returns the start/end byte offsets (as
// FindAllStringIndex would) of every match of re in text that doesn't
// fall inside an existing org-mode link, in order — the shared
// "which bare URLs actually need formatting" logic behind formatURLs
// (one text at a time, during editing) and collectFormatLinksTargets
// (every entry at once, for :format-links).
func BareURLSpans(re *regexp.Regexp, text string) [][2]int {
	matches := re.FindAllStringIndex(text, -1)
	if len(matches) == 0 {
		return nil
	}
	linkSpans := org.LinkIndexes(text)
	withinLink := func(pos int) bool {
		for _, s := range linkSpans {
			if pos >= s[0] && pos < s[1] {
				return true
			}
		}
		return false
	}
	var out [][2]int
	for _, span := range matches {
		if !withinLink(span[0]) {
			out = append(out, [2]int{span[0], span[1]})
		}
	}
	return out
}

// RunBatchFormatter invokes urlFormatterCmd once, feeding it every
// url (one per line) on its stdin instead of one at a time via a
// trailing argument (see runURLFormatter) — used by :format-links to
// format many URLs with a single external process instead of one
// process per URL, which could be prohibitively slow for a large batch.
// Returns exactly len(urls) formatted strings, in the same order;
// anything else (a run failure, or a line-count mismatch) is an error,
// since there'd be no reliable way to match output back to input. The
// run itself goes through execlog.Run, which records it in elog
// (start, every output line as it's produced, and its exit code) for
// :log — this runs on its own goroutine (see startFormatLinks), so elog
// must be safe for concurrent use, which is exactly what it's for.
func RunBatchFormatter(elog *execlog.Log, urlFormatterCmd string, urls []string) ([]string, error) {
	fields := ParseCommand(urlFormatterCmd)
	if len(fields) == 0 {
		return nil, ErrEmptyCommand
	}

	stdin := strings.Join(urls, "\n") + "\n"
	log.Printf("url formatter (batch): running %v with %d url(s) on stdin", append([]string{fields[0]}, fields[1:]...), len(urls))

	out, err := execlog.Run(elog, fields[0], fields[1:], stdin)
	if err != nil {
		detail := err.Error()
		if exitErr, ok := err.(*exec.ExitError); ok && len(exitErr.Stderr) > 0 {
			detail = fmt.Sprintf("%v (stderr: %s)", err, strings.TrimSpace(string(exitErr.Stderr)))
		}
		log.Printf("url formatter (batch): %s failed: %s", fields[0], detail)
		return nil, fmt.Errorf("%s", detail)
	}

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(urls) == 0 {
		return nil, nil
	}
	if len(lines) != len(urls) {
		log.Printf("url formatter (batch): %s produced %d line(s), want %d", fields[0], len(lines), len(urls))
		return nil, fmt.Errorf("%s produced %d line(s) of output, want %d (one per url)", fields[0], len(lines), len(urls))
	}
	log.Printf("url formatter (batch): %s -> %d formatted url(s)", fields[0], len(lines))
	return lines, nil
}
