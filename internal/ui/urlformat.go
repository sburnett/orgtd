package ui

import (
	"fmt"
	"log"
	"os/exec"
	"regexp"
	"strings"
)

// debugLogHint returns a parenthesized suffix pointing a failure message
// at debug.log when debug logging is actually on (see WithDebug); when
// it's off, nothing was written there, so this instead points at how to
// turn it on rather than sending the user to a file that doesn't exist.
func (m Model) debugLogHint() string {
	if m.debug {
		return " (see debug.log)"
	}
	return " (rerun with --debug for details)"
}

// defaultURLSchemes are always recognized as bare-URL prefixes,
// independent of whatever extra prefixes the user configures (see
// WithURLFormatterPrefixes) for things like a shortlink service
// ("bit.ly/...") or an internal go-link convention ("go/...") that don't
// carry a scheme.
var defaultURLSchemes = []string{"https://", "http://"}

// buildBareURLRegexp compiles the regexp formatURLs uses to find a bare
// URL not already wrapped in link brackets: one of defaultURLSchemes, or
// one of extraPrefixes, followed by a run of non-whitespace,
// non-bracket characters (stopping at '[' or ']' so a match can never
// span into or out of an org-mode link). Each extra prefix is guarded
// with \b so it only matches at a word boundary — without that, a short
// prefix like "go/" would also match mid-word inside something like
// "embargo/foo". The built-in schemes don't need this guard: nothing
// realistic precedes "https://" mid-word.
func buildBareURLRegexp(extraPrefixes []string) *regexp.Regexp {
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

// formatURLs runs every bare URL in text (i.e. not already part of an
// org-mode link) through the configured urlFormatterCmd, replacing it
// with that program's output. It's a no-op if no formatter is configured.
func (m *Model) formatURLs(text string) string {
	if m.urlFormatterCmd == "" {
		return text
	}
	if m.bareURLRe == nil {
		// New() normally builds this from urlFormatterPrefixes; fall
		// back to building it here too, so a Model constructed as a
		// literal (as several tests do) never panics on a nil regexp.
		m.bareURLRe = buildBareURLRegexp(m.urlFormatterPrefixes)
	}
	spans := bareURLSpansOutsideLinks(m.bareURLRe, text)
	if len(spans) == 0 {
		return text
	}

	cache := make(map[string]string)
	var b strings.Builder
	last := 0
	for _, span := range spans {
		start, end := span[0], span[1]
		url := text[start:end]
		formatted, ok := cache[url]
		if !ok {
			formatted = m.runURLFormatter(url)
			cache[url] = formatted
		}
		b.WriteString(text[last:start])
		b.WriteString(formatted)
		last = end
	}
	b.WriteString(text[last:])
	return b.String()
}

// bareURLSpansOutsideLinks returns the start/end byte offsets (as
// FindAllStringIndex would) of every match of re in text that doesn't
// fall inside an existing org-mode link, in order — the shared
// "which bare URLs actually need formatting" logic behind formatURLs
// (one text at a time, during editing) and collectFormatLinksTargets
// (every entry at once, for :format-links).
func bareURLSpansOutsideLinks(re *regexp.Regexp, text string) [][2]int {
	matches := re.FindAllStringIndex(text, -1)
	if len(matches) == 0 {
		return nil
	}
	linkSpans := orgLinkRe.FindAllStringIndex(text, -1)
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

// runURLFormatter invokes the configured urlFormatterCmd as
// `<program> <extra args...> <url>` and returns its trimmed stdout.
// urlFormatterCmd is split via splitCommandFields, the same quote-aware
// splitter buildEditorCommand uses for $EDITOR (e.g. `myformatter
// --template "a template"` runs "myformatter" with "--template" and "a
// template" as leading arguments before url), and each field has a
// leading "~" expanded via expandHomeField — without either of these, a
// formatter configured with any extra arguments, or with a path under
// the home directory, would fail every time: exec.Command treats its
// first argument as a literal executable name, so an unsplit
// "myformatter -x" means "look for a program literally named
// 'myformatter -x'", and an unexpanded "~/bin/myformatter" (correct on
// the command line, where the shell expands it, but not from the config
// file, where nothing does) means "look for a program literally named
// '~/bin/myformatter'" — neither ever exists. Every attempt is logged
// via the standard log package, including the subprocess's stderr on
// failure — cmd/orgtd redirects it to a file at startup, since the TUI
// itself owns the terminal and plain log output can't share it. Without
// this, silently leaving the URL unchanged on any error gives no clue
// why; a failure also sets m.message so it's visible without leaving
// the app or checking the log. The actual run goes through
// runLoggedCommand, which also records it (start, every output line, and
// its exit code) in m.execLog for :log.
func (m *Model) runURLFormatter(url string) string {
	fields := splitCommandFields(m.urlFormatterCmd)
	if len(fields) == 0 {
		log.Printf("url formatter: urlFormatterCmd %q has no fields after splitting; skipping %q", m.urlFormatterCmd, url)
		return url
	}
	for i, f := range fields {
		fields[i] = expandHomeField(f)
	}
	args := append(append([]string{}, fields[1:]...), url)
	log.Printf("url formatter: running %v", append([]string{fields[0]}, args...))

	out, err := runLoggedCommand(m.execLog, fields[0], args, "")
	if err != nil {
		detail := err.Error()
		if exitErr, ok := err.(*exec.ExitError); ok && len(exitErr.Stderr) > 0 {
			detail = fmt.Sprintf("%v (stderr: %s)", err, strings.TrimSpace(string(exitErr.Stderr)))
		}
		log.Printf("url formatter: %s failed: %s", fields[0], detail)
		m.message = fmt.Sprintf("URL formatter failed%s: %s", m.debugLogHint(), detail)
		return url
	}

	formatted := strings.TrimSpace(out)
	if formatted == "" {
		log.Printf("url formatter: %s produced no output for %q", fields[0], url)
		m.message = "URL formatter produced no output" + m.debugLogHint()
		return url
	}
	log.Printf("url formatter: %s -> %q", fields[0], formatted)
	return formatted
}

// runBatchURLFormatter invokes urlFormatterCmd once, feeding it every
// url (one per line) on its stdin instead of one at a time via a
// trailing argument (see runURLFormatter) — used by :format-links to
// format many URLs with a single external process instead of one
// process per URL, which could be prohibitively slow for a large batch.
// Returns exactly len(urls) formatted strings, in the same order;
// anything else (a run failure, or a line-count mismatch) is an error,
// since there'd be no reliable way to match output back to input. The
// run itself goes through runLoggedCommand, which records it in elog
// (start, every output line as it's produced, and its exit code) for
// :log — this runs on its own goroutine (see startFormatLinks), so elog
// must be safe for concurrent use, which is exactly what it's for.
func runBatchURLFormatter(elog *execLog, urlFormatterCmd string, urls []string) ([]string, error) {
	fields := splitCommandFields(urlFormatterCmd)
	if len(fields) == 0 {
		return nil, fmt.Errorf("url formatter command is empty")
	}
	for i, f := range fields {
		fields[i] = expandHomeField(f)
	}

	stdin := strings.Join(urls, "\n") + "\n"
	log.Printf("url formatter (batch): running %v with %d url(s) on stdin", append([]string{fields[0]}, fields[1:]...), len(urls))

	out, err := runLoggedCommand(elog, fields[0], fields[1:], stdin)
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
