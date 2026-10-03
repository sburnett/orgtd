package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sburnett/orgtd/internal/execlog"
	"github.com/sburnett/orgtd/internal/org"
)

// editFinishedMsg reports that the external editor launched by
// launchEditor has exited. insert is non-nil when this edit session
// originated from o/O (target is then a tentative placeholder headline,
// not yet part of undo history) rather than an `i` edit of existing
// content.
type editFinishedMsg struct {
	path   string
	target *org.Headline
	insert *insertContext
	cmd    *exec.Cmd // the editor process, for execlog.LogCompleted (its Process field is only populated once tea.ExecProcess has actually started it)
	err    error
}

// startEdit ("i"/"I") writes the current headline (and its entire subtree)
// to a temp file and opens it in $EDITOR for editing in place — for a
// vim-family editor, with the cursor already placed right after the
// bullet ("* ") and insert mode already started, so typing begins
// immediately without a manual "i" or cursor motion in the editor
// itself. See startEditAppend for "A", and startEditWithPlacement for
// the shared mechanics.
func (m *Model) startEdit() tea.Cmd {
	return m.startEditWithPlacement(cursorAtEntryStart)
}

// startEditAppend ("A") is startEdit, but positions the cursor at the
// end of the entry's first line instead of right after the bullet —
// vim's own "A" (append at end of line), once the editor's open.
func (m *Model) startEditAppend() tea.Cmd {
	return m.startEditWithPlacement(cursorAtLineEnd)
}

// startEditWithPlacement is the shared implementation behind startEdit
// ("i") and startEditAppend ("A") — writes the current headline (and its
// entire subtree) to a temp file and opens it in $EDITOR, positioning
// the cursor per placement. Returns nil if there's nothing to edit or
// the editor couldn't be launched, in which case any error is left in
// m.message.
func (m *Model) startEditWithPlacement(placement editorCursorPlacement) tea.Cmd {
	if m.cursor >= 0 && m.cursor < len(m.rows) {
		if f := m.rows[m.cursor].file; f != nil {
			// A whole-file edit hands the raw file to $EDITOR directly,
			// bypassing every other guard here — if any headline in it is
			// locked by :format-links, refuse outright rather than risk
			// the user rewriting (or deleting) it in a way finishEditFile
			// has no way to detect or prevent.
			if m.fileHasImmutableHeadline(f) {
				m.message = "This file has entries being formatted by :format-links; can't edit the whole file yet"
				return nil
			}
			// Editing a whole file discards undo history for it (and
			// clears any mark/clarify-target on its headlines) even if
			// the user ends up changing nothing — confirm first rather
			// than doing that as a side effect of a single keystroke.
			// Both i and A land here identically: there's no single
			// entry to position a cursor within.
			m.mode = confirmMode
			m.pendingFileEdit = f
			m.confirmMessage = fmt.Sprintf("Edit %s in $EDITOR? This clears undo history and marks for this file. [y/N]", filepath.Base(f.Path))
			return nil
		}
	}
	h := m.currentHeadline()
	if h == nil || m.refuseIfImmutable(h) {
		return nil
	}
	return m.launchEditor(h, nil, placement)
}

// fileEditFinishedMsg reports that the external editor launched by
// startEditFile has exited, for a whole-file edit ("i" on a file row).
type fileEditFinishedMsg struct {
	target *org.File // the file being edited, identified by its old pointer
	cmd    *exec.Cmd // the editor process, for execlog.LogCompleted (its Process field is only populated once tea.ExecProcess has actually started it)
	err    error
}

// startEditFile ("i" on a file row) opens that file directly in
// $EDITOR — the real file on disk, not a temp copy, since there's no
// synthetic context wrapper needed for editing a whole file the way
// there is for a single entry (see launchEditor). On exit, the file is
// simply reloaded from disk (see finishEditFile). There's no undo for
// this — the editor already wrote the change directly to disk, so
// there's no in-memory action to record or revert.
func (m *Model) startEditFile(f *org.File) tea.Cmd {
	editorCmd := buildEditorCommand(m.editorCommand(), f.Path, "", noCursorPlacement, 0)
	return tea.ExecProcess(editorCmd, func(err error) tea.Msg {
		return fileEditFinishedMsg{target: f, cmd: editorCmd, err: err}
	})
}

// finishEditFile reloads the just-edited file from disk, replacing its
// old *org.File wholesale — every headline pointer it held is gone, so
// any mark, clarify-target, or dirty-marker referencing one of them is
// cleared (see clearRefsForFile) rather than left dangling. The
// reloaded file itself is never marked dirty: the editor already wrote
// it, so there's nothing more to save.
func (m Model) finishEditFile(msg fileEditFinishedMsg) (tea.Model, tea.Cmd) {
	execlog.LogCompleted(m.execLog, msg.cmd, msg.err)
	if msg.err != nil {
		m.message = fmt.Sprintf("Editor exited with an error: %v", msg.err)
		return m, nil
	}

	newFile, err := org.ParseFile(msg.target.Path)
	if err != nil {
		m.message = fmt.Sprintf("Could not reload %s: %v", filepath.Base(msg.target.Path), err)
		return m, nil
	}

	for i, f := range m.ws.Files {
		if f == msg.target {
			m.ws.Files[i] = newFile
			break
		}
	}
	m.clearRefsForFile(msg.target)
	m.rebuildRows()
	m.focusFile(newFile)
	return m, nil
}

// clearRefsForFile removes every mark, clarify-target reference, and
// dirty marker pointing at a headline in f, plus f's own file-level
// dirty/saved-position bookkeeping — called after f's entire headline
// tree has been discarded and replaced (a whole-file reload), since
// none of those old headline pointers exist anywhere anymore.
func (m *Model) clearRefsForFile(f *org.File) {
	org.Walk(f.Headlines, func(h *org.Headline) {
		if m.clarifyTarget == h {
			m.clarifyTarget = nil
		}
		m.clearMarksFor(h)
		delete(m.dirtyHeadlines, h)
	})
	delete(m.dirty, f)
	delete(m.savedPos, f)
}

// editorsWithLineArg lists $EDITOR basenames known to support a leading
// "+N" argument that opens the file with the cursor on line N — a
// convention shared by vi/vim, emacs, and nano. launchEditor uses this
// to land the cursor on the real content rather than line 1, which is
// now the context trailer's file-name comment. Applied only to these
// editors, since an arbitrary editor could easily misread "+N" as a
// literal filename instead of a line number.
var editorsWithLineArg = map[string]bool{
	"vi": true, "vim": true, "nvim": true, "gvim": true, "mvim": true,
	"emacs": true, "emacsclient": true,
	"nano": true,
}

// vimFamily is the subset of editorsWithLineArg that additionally
// understands vim's ex-command syntax — a "+{command}" argument
// executing an arbitrary command, not just a bare line number — used to
// start "i"/"A" directly in insert mode at a specific spot (see
// cursorPlacementArg). emacs/emacsclient and nano share the "+N" line
// convention but have no equivalent notion of "insert mode" to start
// (nano isn't modal; plain emacs isn't either), so they always just get
// a bare "+N" regardless of placement.
var vimFamily = map[string]bool{
	"vi": true, "vim": true, "nvim": true, "gvim": true, "mvim": true,
}

// editorCursorPlacement selects where launchEditor positions the cursor,
// and whether it starts the editor directly in insert mode, when the
// configured editor is vim-family (see vimFamily) — a plain "+N" line
// jump for anything else, or for noCursorPlacement.
type editorCursorPlacement int

const (
	// noCursorPlacement just lands on the entry's first line, same as
	// always — used only for a whole-file edit (startEditFile), where
	// there's no single entry to position a cursor within.
	noCursorPlacement editorCursorPlacement = iota
	// cursorAtEntryStart ("i", "o"/"O") puts the cursor at the very start
	// of the entry's own text — column 1, since the buffer never shows a
	// bullet to land after (see dedentEntry, launchEditor, and
	// resolveEntryCursorPlacement) — in insert mode, so typing
	// immediately inserts text there exactly as pressing vim's own "i" at
	// that spot would. For o/O the entry is a blank template, so this is
	// also where its title will end up starting.
	cursorAtEntryStart
	// cursorAtLineEnd ("A") puts the cursor at the end of the entry's
	// first line, in insert mode — vim's own "A" (append at end of
	// line), landing on whichever text (keyword, title, tags) the line
	// actually ends with.
	cursorAtLineEnd
)

// cursorPlacementArg returns the "+..." argument buildEditorCommand
// should pass for the given editor basename, startLine (1-based), col
// (1-based, meaningful only for cursorAtEntryStart), and placement.
// Non-vim-family editors (or noCursorPlacement) always get a bare
// "+startLine" — see editorCursorPlacement and vimFamily.
func cursorPlacementArg(editorBase string, startLine, col int, placement editorCursorPlacement) string {
	if vimFamily[editorBase] {
		switch placement {
		case cursorAtEntryStart:
			return fmt.Sprintf("+call cursor(%d,%d)|startinsert", startLine, col)
		case cursorAtLineEnd:
			// startinsert! is vim's own "A": moves to the end of the
			// current line before entering insert mode, so there's no
			// need to compute or pass a column at all.
			return fmt.Sprintf("+%d|startinsert!", startLine)
		}
	}
	return fmt.Sprintf("+%d", startLine)
}

// resolveEntryCursorPlacement returns the placement and (1-based) column
// launchEditor should actually request for a dedented entry buffer (see
// dedentEntry): cursorAtEntryStart always targets column 1, since
// there's no bullet to land after — for either an existing
// entry ("i"/"A") or a blank o/O template alike. It downgrades to
// cursorAtLineEnd when the first line is completely empty (a
// just-started o/O insert, or an existing entry with a blank title):
// vim's cursor()+startinsert needs the target column to be an existing
// character — cursor() clamps to the line's last real character rather
// than allowing a column one past the end — so requesting column 1 on a
// zero-length line would fail. cursorAtLineEnd's startinsert! (append)
// sidesteps this entirely: on an empty line, "end of line" and "column
// 1" are the exact same position anyway.
func resolveEntryCursorPlacement(entry string, placement editorCursorPlacement) (editorCursorPlacement, int) {
	if placement == cursorAtEntryStart {
		firstLine, _, _ := strings.Cut(entry, "\n")
		if len(firstLine) == 0 {
			placement = cursorAtLineEnd
		}
	}
	return placement, 1
}

// editorCommand returns the external editor to launch: m.editorOverride
// (see WithEditor) if set, else $EDITOR (README's --editor row).
func (m Model) editorCommand() string {
	if m.editorOverride != "" {
		return m.editorOverride
	}
	return os.Getenv("EDITOR")
}

// splitCommandFields splits s into command-line-style fields the way a
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
func splitCommandFields(s string) []string {
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

// expandHomeField expands a leading "~" or "~/..." in field to the
// user's home directory, the way an interactive shell expands a tilde
// word during its own word-splitting. This matters specifically for
// editor/urlFormatterCmd: typed on the command line, a value like
// "~/bin/myformatter" is already expanded by the shell before orgtd
// ever sees argv, but the identical value read from the config file
// reaches us as a raw, unexpanded string — no shell is involved there —
// so it would otherwise be handed to exec.Command completely literally
// and fail to launch (silently, from the caller's perspective, since a
// failed exec just leaves the input unchanged). Applied per-field
// (after splitCommandFields), not to the whole command string, so a
// tilde in a later argument is expanded too, not just a leading one.
func expandHomeField(field string) string {
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

// buildEditorCommand builds the *exec.Cmd for opening path in the editor
// named by editorEnv ($EDITOR's value; "vim" if empty), splitting off
// any extra words as leading arguments (e.g. "code --wait"). For an
// editor in editorsWithLineArg, it also inserts a "+..." argument (see
// cursorPlacementArg) so the editor opens with the cursor on the real
// content — or, for a vim-family editor with placement other than
// noCursorPlacement, at a specific column within it and already in
// insert mode (before is the context text written ahead of it in the
// file; its newline count is exactly the 1-based line the real content
// starts on). col is the 1-based column cursorAtEntryStart should land
// on; unused otherwise.
func buildEditorCommand(editorEnv, path, before string, placement editorCursorPlacement, col int) *exec.Cmd {
	fields := splitCommandFields(editorEnv)
	if len(fields) == 0 {
		fields = []string{"vim"}
	}
	for i, f := range fields {
		fields[i] = expandHomeField(f)
	}
	args := append([]string{}, fields[1:]...)
	base := filepath.Base(fields[0])
	if editorsWithLineArg[base] {
		startLine := strings.Count(before, "\n") + 1
		args = append(args, cursorPlacementArg(base, startLine, col, placement))
	}
	args = append(args, path)
	return exec.Command(fields[0], args...)
}

// scratchFilePath returns a path for a new editor buffer under a
// scratch/ directory inside the org dir, in a dated subdirectory
// (scratch/2026/10/02) and named after the current time down to the
// nanosecond (so two buffers opened in the same second never collide)
// rather than randomly — so a buffer is easy to find by when it was
// made if it's ever needed after the fact, e.g. because its content was
// accidentally discarded instead of saved. The file itself isn't
// created here; only its directory is, so the caller still controls
// how (and whether) the file gets written.
func (m *Model) scratchFilePath() (string, error) {
	now := time.Now()
	dir := filepath.Join(m.ws.Dir, "scratch", now.Format("2006"), now.Format("01"), now.Format("02"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, now.Format("15-04-05.000000000")+".org"), nil
}

// launchEditor writes h to a scratch file (see scratchFilePath) and
// opens it in $EDITOR (vim by default), suspending the TUI for the
// duration. ctx tags the resulting
// editFinishedMsg so finishEdit knows whether this is an o/O insert
// session or a plain `i`/`A` edit, and also which buffer format to
// expect back: both an o/O (or "gC"/"gX" capture) insert session and a
// plain `i`/`A` edit of an existing entry now share the same shape — the
// entry's own text (title, minus its bullet, plus its planning line,
// properties, and body, the whole thing dedented by one level — see
// dedentEntry and org.RenderEntry) comes first in the buffer, cursor
// already there, followed by a blank line and a git-commit-style comment
// trailer sketching the entry's place in the outline below it (see
// editEntryContext). ctx tags the resulting editFinishedMsg so finishEdit
// knows whether this is an insert session or a plain edit, and is also
// what selects the trailer's wording ("Inserting a new entry." vs
// "Editing this entry.").
// placement (see editorCursorPlacement) controls where a vim-family
// editor lands the cursor and whether it starts in insert mode already.
// Returns nil if the scratch file couldn't be created or the editor
// couldn't be started, in which case the error is left in m.message.
func (m *Model) launchEditor(h *org.Headline, ctx *insertContext, placement editorCursorPlacement) tea.Cmd {
	path, err := m.scratchFilePath()
	if err != nil {
		m.message = fmt.Sprintf("Could not create scratch file: %v", err)
		return nil
	}

	entry := dedentEntry(org.RenderEntry(h), h.Level)
	if !strings.HasSuffix(entry, "\n") {
		entry += "\n"
	}
	content := entry + "\n" + m.editEntryContext(h, ctx != nil)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		os.Remove(path)
		m.message = fmt.Sprintf("Could not write scratch file: %v", err)
		return nil
	}

	placement, col := resolveEntryCursorPlacement(entry, placement)
	editorCmd := buildEditorCommand(m.editorCommand(), path, "", placement, col)

	return tea.ExecProcess(editorCmd, func(err error) tea.Msg {
		return editFinishedMsg{path: path, target: h, insert: ctx, cmd: editorCmd, err: err}
	})
}

// dedentEntry removes one level's worth of indentation — level+1
// characters, the fixed width writeHeadlineFields always uses, both for
// the bullet ("<stars> ") on the first line and for the span of spaces
// indenting every other line (planning, properties, body) so they align
// under it — from every line of s (see org.RenderEntry). Used to build
// the buffer an entry is edited or inserted in ("i"/"A", o/O, capture):
// with the bullet gone, leaving the rest of the entry indented under
// where it used to be would look disjointed, so the whole entry is
// dedented together. Blank lines are left alone. indentEntry reverses
// this once the edit comes back.
func dedentEntry(s string, level int) string {
	width := level + 1
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if i == 0 {
			// The bullet is "<stars> ", not spaces, but is always exactly
			// width bytes wide regardless of what follows.
			if len(line) < width {
				continue
			}
			lines[i] = line[width:]
			continue
		}
		n := 0
		for n < width && n < len(line) && line[n] == ' ' {
			n++
		}
		lines[i] = line[n:]
	}
	return strings.Join(lines, "\n")
}

// indentEntry reverses dedentEntry once the edit comes back: restores
// the bullet onto s's first line, and the matching span of indentation
// onto every other non-blank line, so the whole entry re-parses as a
// real headline at its original level with its planning/properties/body
// lines indented the way writeHeadlineFields expects. Blank lines are
// left alone, matching how dedentEntry treats them.
func indentEntry(s string, level int) string {
	indent := strings.Repeat(" ", level+1)
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if i == 0 {
			lines[i] = strings.Repeat("*", level) + " " + line
			continue
		}
		if line == "" {
			continue
		}
		lines[i] = indent + line
	}
	return strings.Join(lines, "\n")
}

// editEntryContext builds the git-commit-style comment trailer for both
// editing an existing entry ("i"/"A") and inserting a new one (o/O,
// "gC"/"gX" capture): the entry's own editable text (see launchEditor
// and org.RenderEntry) comes first in the buffer, followed by a blank
// line and this whole trailer below it, sketching the whole outline the
// entry sits in — its file, parent, siblings, and (for an existing
// entry) its own children, each rendered with real org stars matching
// its own level, as a little sub-tree — with a "[THIS ENTRY HERE]"
// marker standing in for the entry itself, since its actual text is
// already sitting above, editable. A brand-new o/O/capture entry never
// has children yet, so that part of the sub-tree is simply empty; a
// dedicated org.Walk special case for it isn't needed. The children
// (when there are any) are shown for orientation only — they aren't
// part of what this buffer edits; see finishEdit. isInsert selects the
// trailer's instructional wording. Every line is an org comment
// ("# ..."), so it's inert whether the user deletes it or leaves it in
// place.
func (m *Model) editEntryContext(h *org.Headline, isInsert bool) string {
	fileName := ""
	if f := m.fileForHeadline(h); f != nil {
		fileName = filepath.Base(f.Path)
	}
	prev, next, earlierCount := m.siblingHeadlines(h)

	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n#\n", fileName)
	if h.Parent != nil {
		fmt.Fprintf(&b, "# %s\n", commentedHeadlineLine(h.Parent))
	}
	if earlierCount > 0 {
		noun := "sibling"
		if earlierCount != 1 {
			noun = "siblings"
		}
		fmt.Fprintf(&b, "#%s... %d earlier %s ...\n", strings.Repeat(" ", h.Level), earlierCount, noun)
	}
	if prev != nil {
		fmt.Fprintf(&b, "# %s\n", commentedHeadlineLine(prev))
	}
	fmt.Fprintf(&b, "# %s [THIS ENTRY HERE]\n", strings.Repeat("*", h.Level))
	org.Walk(h.Children, func(c *org.Headline) {
		fmt.Fprintf(&b, "# %s\n", commentedHeadlineLine(c))
	})
	if next != nil {
		fmt.Fprintf(&b, "# %s\n", commentedHeadlineLine(next))
	}
	action := "Editing this entry."
	if isInsert {
		action = "Inserting a new entry."
	}
	fmt.Fprintf(&b, "#\n# %s Lines starting with '#' are ignored. Save and\n", action)
	fmt.Fprintln(&b, "# exit to apply your changes, or delete the entry's content (leaving")
	fmt.Fprintln(&b, "# only these comments, or nothing) to cancel.")
	if m.urlFormatterCmd != "" {
		fmt.Fprintln(&b, "#")
		fmt.Fprintln(&b, "# Bare URLs will be formatted into org-mode links automatically. To")
		fmt.Fprintln(&b, "# format one yourself instead, write it as [[http://...]] directly.")
	}
	return b.String()
}

// commentedHeadlineLine renders h as a single line of real org syntax —
// its actual stars and keyword — for display as context (always inside
// a "# " comment, never parsed as a real headline).
func commentedHeadlineLine(h *org.Headline) string {
	stars := strings.Repeat("*", h.Level)
	if h.Keyword != "" {
		return stars + " " + h.Keyword + " " + h.Title
	}
	return stars + " " + h.Title
}

// commentLineRe matches an org comment line: '#' followed by whitespace
// or end of line. This deliberately excludes org directives like
// "#+TITLE:" (hash immediately followed by '+'), which aren't comments.
var commentLineRe = regexp.MustCompile(`^\s*#(\s|$)`)

// stripCommentLines removes every comment line from text, the same way
// git strips '#'-prefixed lines from a commit message template before
// using it. This is what makes editorContextComment's trailer inert
// regardless of whether the user deletes it.
func stripCommentLines(text string) string {
	lines := strings.Split(text, "\n")
	out := lines[:0]
	for _, l := range lines {
		if !commentLineRe.MatchString(l) {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// headlineBulletChars are plain-text/markdown list-bullet markers that
// don't count as real content on their own — the kind of thing a
// markdown habit or a paste might leave behind with nothing actually
// typed after it.
const headlineBulletChars = "-*+•"

// isBlankHeadlineTitle reports whether title amounts to no text at all:
// nothing but whitespace and/or a run of leading bullet characters (see
// headlineBulletChars). Stripping is prefix-only — never touching the
// end of the string — so real content that merely starts with one of
// these characters (e.g. "-1 lap penalty") is correctly seen as
// non-blank once whatever follows the bullet run is reached.
func isBlankHeadlineTitle(title string) bool {
	s := strings.TrimSpace(title)
	for s != "" {
		r, size := utf8.DecodeRuneInString(s)
		if !strings.ContainsRune(headlineBulletChars, r) {
			break
		}
		s = strings.TrimSpace(s[size:])
	}
	return s == ""
}

// finishEdit reads back the edited entry and reparses it. For a plain
// `i`/`A` edit, the result replaces the original headline's own text in
// place, its children carried over unchanged since the edit buffer never
// showed them (see indentEntry below and launchEditor) — or, if
// the edit left nothing parseable, the original is left untouched. For
// an o/O insert session, a successful result is committed as a single
// undo step; any failure (editor error, unreadable file, unparseable or
// emptied-out result) rolls back the tentative placeholder entirely,
// leaving no trace. msg.path (see scratchFilePath) is deliberately never
// deleted here — it stays behind as a failsafe in case what comes back
// from the editor is ever lost or discarded by mistake.
func (m Model) finishEdit(msg editFinishedMsg) (tea.Model, tea.Cmd) {
	execlog.LogCompleted(m.execLog, msg.cmd, msg.err)

	if msg.err != nil {
		m.message = fmt.Sprintf("Editor exited with an error: %v", msg.err)
		if msg.insert != nil {
			m.rollbackInsert(*msg.insert, msg.target)
		}
		return m, nil
	}

	data, err := os.ReadFile(msg.path)
	if err != nil {
		m.message = fmt.Sprintf("Could not read edited entry: %v", err)
		if msg.insert != nil {
			m.rollbackInsert(*msg.insert, msg.target)
		}
		return m, nil
	}

	// stripCommentLines can leave a trailing blank line behind — the one
	// separating the real content from editEntryContext's trailer (see
	// launchEditor) — so trim it the same way git's own cleanup mode
	// does, rather than let it accumulate as a stray blank body line on
	// every repeated edit.
	stripped := strings.TrimRight(stripCommentLines(string(data)), "\n")

	// The buffer never showed a bullet (see dedentEntry), so an
	// all-blank result unambiguously means "cancel" — indentEntry
	// below would otherwise turn it into a real, blank-titled headline
	// instead of nothing at all.
	if strings.TrimSpace(stripped) == "" {
		if msg.insert != nil {
			m.rollbackInsert(*msg.insert, msg.target)
			m.message = "Insert cancelled (empty)"
		} else {
			m.message = "Edited entry had no headline; leaving it unchanged"
		}
		return m, nil
	}
	stripped = indentEntry(stripped, msg.target.Level)

	text := m.formatURLs(stripped)

	file, err := org.Parse(strings.NewReader(text), "")
	if err != nil {
		m.message = fmt.Sprintf("Could not parse edited entry: %v", err)
		if msg.insert != nil {
			m.rollbackInsert(*msg.insert, msg.target)
		}
		return m, nil
	}

	if len(file.Headlines) == 0 {
		// Unreachable in practice — indentEntry above guarantees
		// the text starts with a real headline line — but kept as a
		// defensive fallback rather than assuming it.
		if msg.insert != nil {
			m.rollbackInsert(*msg.insert, msg.target)
			m.message = "Insert cancelled (empty)"
		} else {
			m.message = "Edited entry had no headline; leaving it unchanged"
		}
		return m, nil
	}

	if msg.insert != nil {
		if isBlankHeadlineTitle(file.Headlines[0].Title) {
			// A headline with stars and maybe a keyword but no actual
			// text (or just a stray bullet character) is just as
			// unusable as a genuinely empty result — don't create it.
			m.rollbackInsert(*msg.insert, msg.target)
			m.message = "Insert cancelled (no text)"
			return m, nil
		}
		m.commitInsert(*msg.insert, msg.target, file.Headlines)
		if msg.insert.switchToOutline && !usesOutlineRows(m.view) {
			// commitInsert's own focusHeadline couldn't find the new
			// entry's row, since the current view's rows don't include
			// it at all (e.g. captured from agenda or calendar) — bring
			// it into view explicitly rather than leaving the cursor
			// wherever it happened to be in that other view.
			m.switchToView(outlineView)
			m.focusHeadline(file.Headlines[0])
		}
		if msg.insert.thenPickMeeting {
			// commitInsert (and the switch above, if it ran) already
			// focused file.Headlines[0], so the picker (see
			// startMeetingPicker) targets the just-captured entry — see
			// startCaptureAndPickMeeting ("gX").
			m.startMeetingPicker()
		}
		// insertCalendarCapture's attachMeeting, if set, was already
		// folded into commitInsert's own undo step above — nothing left
		// to do here (see insertContext.attachMeeting).
		return m, nil
	}

	// The edit buffer never showed msg.target's children (see
	// launchEditor), so they never went through the editor at all —
	// carry them over as-is rather than leaving the edited entry
	// childless, or discarding them in favor of whatever (if anything)
	// the user happened to type at a deeper level in the buffer.
	file.Headlines[0].Children = msg.target.Children
	for _, c := range file.Headlines[0].Children {
		c.Parent = file.Headlines[0]
	}

	m.pushUndo(&subtreeReplaceAction{
		f:      m.fileForHeadline(msg.target),
		oldSet: []*org.Headline{msg.target},
		newSet: file.Headlines,
	})
	return m, nil
}
