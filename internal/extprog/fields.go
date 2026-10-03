// Package extprog builds and runs the external programs orgtd launches
// from user configuration: the editor ($EDITOR or --editor) and the URL
// formatters. It owns the parts of that which don't need UI state:
// splitting a configured command line into words (SplitFields,
// ExpandHome, ParseCommand), constructing an editor invocation with the
// cursor placed on the entry being edited (EditorCommand), and running a
// formatter on one URL or a batch of them (RunFormatter,
// RunBatchFormatter). Subprocesses are run through internal/execlog so
// they show up in :log.
package extprog

import (
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// SplitFields splits s into command-line-style fields the way a
// shell would for simple cases: runs of non-whitespace are one field
// each, except that a single- or double-quoted span is treated as part
// of the enclosing field (quotes stripped) and may itself contain
// whitespace — e.g. `myformatter --template "a template" x` splits into
// ["myformatter", "--template", "a template", "x"]. This is a minimal
// word-splitter, not a shell parser: no escape sequences, no variable
// expansion, no nesting — just enough for a configured command
// (urlFormatterCmd, $EDITOR) to carry an argument containing spaces,
// which plain strings.Fields cannot do (it would tear "a template"
// apart into two fields, quote characters and all). An unterminated
// quote isn't an error — whatever was captured is still emitted as a
// field, rather than the whole config value being discarded.
func SplitFields(s string) []string {
	var fields []string
	var cur strings.Builder
	inField := false
	var quote rune

	flush := func() {
		if inField {
			fields = append(fields, cur.String())
			cur.Reset()
			inField = false
		}
	}

	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
			inField = true
		case unicode.IsSpace(r):
			flush()
		default:
			cur.WriteRune(r)
			inField = true
		}
	}
	flush()
	return fields
}

// ExpandHome expands a leading "~" or "~/..." in field to the
// user's home directory, the way an interactive shell expands a tilde
// word during its own word-splitting. This matters specifically for
// editor/urlFormatterCmd: typed on the command line, a value like
// "~/bin/myformatter" is already expanded by the shell before orgtd
// ever sees argv, but the identical value read from the config file
// reaches us as a raw, unexpanded string — no shell is involved there —
// so it would otherwise be handed to exec.Command completely literally
// and fail to launch (silently, from the caller's perspective, since a
// failed exec just leaves the input unchanged). Applied per-field
// (after SplitFields), not to the whole command string, so a
// tilde in a later argument is expanded too, not just a leading one.
func ExpandHome(field string) string {
	if field != "~" && !strings.HasPrefix(field, "~/") {
		return field
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return field
	}
	if field == "~" {
		return home
	}
	return filepath.Join(home, field[2:])
}

// ParseCommand splits a configured command line (an editor or URL
// formatter setting) into its words with SplitFields, then expands a
// leading "~" in each word with ExpandHome. The result's first element is
// the program to run and the rest are its leading arguments; it is empty
// only if cmd is blank.
func ParseCommand(cmd string) []string {
	fields := SplitFields(cmd)
	for i, f := range fields {
		fields[i] = ExpandHome(f)
	}
	return fields
}
