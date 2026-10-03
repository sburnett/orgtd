package ui

import (
	"errors"
	"fmt"
	"strings"

	"github.com/sburnett/orgtd/internal/extprog"
)

// debugLogHint returns a parenthesized suffix pointing a failure message
// at debug.log when debug logging is actually on (see WithDebug); when
// it's off, nothing was written there, so this instead points at how to
// turn it on rather than sending the user to a file that doesn't exist.
func (m Model) debugLogHint() string {
	if m.cfg.Debug {
		return " (see debug.log)"
	}
	return " (rerun with --debug for details)"
}

// formatURLs runs every bare URL in text (i.e. not already part of an
// org-mode link) through the configured urlFormatterCmd, replacing it
// with that program's output. It's a no-op if no formatter is configured.
func (m *Model) formatURLs(text string) string {
	if m.cfg.URLFormatter == "" {
		return text
	}
	if m.bareURLRe == nil {
		// New() normally builds this from urlFormatterPrefixes; fall
		// back to building it here too, so a Model constructed as a
		// literal (as several tests do) never panics on a nil regexp.
		m.bareURLRe = extprog.BareURLRegexp(m.cfg.URLFormatterPrefixes)
	}
	spans := extprog.BareURLSpans(m.bareURLRe, text)
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

// runURLFormatter runs the configured urlFormatterCmd on one url (see
// extprog.RunFormatter, which logs the attempt and records it for :log)
// and returns its output — or url unchanged on any failure, since
// silently dropping the URL would be worse than leaving it bare. A
// failure also sets m.message so it's visible without leaving the app or
// checking the log.
func (m *Model) runURLFormatter(url string) string {
	formatted, err := extprog.RunFormatter(m.execLog, m.cfg.URLFormatter, url)
	switch {
	case err == nil:
		return formatted
	case errors.Is(err, extprog.ErrEmptyCommand):
		// Nothing configured to run; leave the url alone, silently.
	case errors.Is(err, extprog.ErrNoOutput):
		m.message = err.Error() + m.debugLogHint()
	default:
		m.message = fmt.Sprintf("URL formatter failed%s: %s", m.debugLogHint(), err)
	}
	return url
}
