package ui

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sburnett/orgtd/internal/execlog"
	"github.com/sburnett/orgtd/internal/extprog"
	"github.com/sburnett/orgtd/internal/org"
)

// editFinishedMsg reports that the external editor launched by
// launchEditor has exited. resume is set when the session was ctrl-g
// from insert mode: what comes back goes back into insert mode's buffer
// (see resumeInsertFromEditor) — for a new entry (o/O, capture), the
// only way one reaches $EDITOR — instead of being applied to target.
type editFinishedMsg struct {
	path   string
	target *org.Headline
	resume bool
	cmd    *exec.Cmd // the editor process, for execlog.LogCompleted (its Process field is only populated once tea.ExecProcess has actually started it)
	err    error
}

// startEdit ("i") puts the current entry into insert mode (see
// insert_mode.go), caret at the start of its title, so typing begins
// right away — the inline counterpart of what used to be an $EDITOR
// session, which ctrl-g (startEditInEditor) still opens. See
// startEditAtLineStart for "I", startEditAfter for "a",
// startEditAppend for "A", and startEditAt for the shared mechanics.
func (m *Model) startEdit() tea.Cmd {
	return m.startEditAt(caretAtTitle, false)
}

// startEditAtLineStart ("I") is startEdit, but with the caret at the
// very start of the entry's first line, before any TODO keyword and
// priority — vim's own "I" (insert at the start of the line), and the
// quick way to change or add a keyword.
func (m *Model) startEditAtLineStart() tea.Cmd {
	return m.startEditAt(caretAtLineStart, false)
}

// startEditAfter ("a") is startEdit, but with the caret one character
// further right — vim's own "a" (append after the cursor), with "i"'s
// position standing in for the cursor.
func (m *Model) startEditAfter() tea.Cmd {
	return m.startEditAt(caretAfterTitleStart, false)
}

// startEditAppend ("A") is startEdit, but with the caret at the end of
// the entry's first line instead — vim's own "A" (append at end of line).
func (m *Model) startEditAppend() tea.Cmd {
	return m.startEditAt(caretAtLineEnd, false)
}

// startEditInEditor (ctrl-g in normal mode) edits the current entry in
// $EDITOR, applying the result once the editor exits — the round trip
// "i" used to make before insert mode existed. Coming from normal mode,
// a vim-family editor starts in normal mode too, on the entry's first
// line (extprog.NoPlacement); ctrl-g from insert mode (see
// editInsertInEditor) lands on the caret instead, still in normal mode.
func (m *Model) startEditInEditor() tea.Cmd {
	return m.startEditAt(caretAtTitle, true)
}

// startEditAt is the shared implementation behind startEdit ("i"),
// startEditAtLineStart ("I"), startEditAfter ("a"), startEditAppend
// ("A") and startEditInEditor (ctrl-g): on a file row it asks to open the whole file in $EDITOR
// (there's no inline equivalent); on an entry, it enters insert mode
// with the caret placed per caret, or — external — writes the entry to
// a scratch file and opens it in $EDITOR, in the editor's normal mode
// (extprog.NoPlacement). Returns nil if there's nothing to edit or the
// editor couldn't be launched, in which case any error is left in
// m.message.
func (m *Model) startEditAt(caret caretStart, external bool) tea.Cmd {
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
			// clears any mark/review-target on its headlines) even if
			// the user ends up changing nothing — confirm first rather
			// than doing that as a side effect of a single keystroke.
			// i, I, a, A and ctrl-g all land here identically: there's no
			// single entry to position a cursor within.
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
	if external {
		return m.launchEditor(h)
	}
	m.enterInsertMode(h, nil, caret)
	return nil
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
	editorCmd := extprog.EditorCommand(m.editorCommand(), f.Path, "", extprog.NoPlacement)
	return tea.ExecProcess(editorCmd, func(err error) tea.Msg {
		return fileEditFinishedMsg{target: f, cmd: editorCmd, err: err}
	})
}

// finishEditFile reloads the just-edited file from disk, replacing its
// old *org.File wholesale — every headline pointer it held is gone, so
// any mark, review-target, or dirty-marker referencing one of them is
// cleared (see clearRefsForFile) rather than left dangling. The
// reloaded file itself is never marked dirty: the editor already wrote
// it, so there's nothing more to save.
func (m Model) finishEditFile(msg fileEditFinishedMsg) (tea.Model, tea.Cmd) {
	execlog.LogCompleted(m.execLog, msg.cmd, msg.err)
	if msg.err != nil {
		m.message = fmt.Sprintf("Editor exited with an error: %v", msg.err)
		return m, nil
	}

	if msg.target.New {
		if _, err := os.Stat(msg.target.Path); errors.Is(err, fs.ErrNotExist) {
			// A new file the editor quit without writing: nothing changed.
			return m, nil
		}
	}
	// The editor wrote the file itself; commit that like any outside
	// edit, so other instances see it, and load it as committed.
	newFile, err := m.reloadFromMain(msg.target)
	if err != nil {
		m.message = fmt.Sprintf("Could not reload %s: %v", filepath.Base(msg.target.Path), err)
		return m, nil
	}
	m.rebuildRows()
	m.focusFile(newFile)
	m.recomputeDirty()
	return m, nil
}

// clearRefsForFile removes every mark, review-target reference, and
// dirty marker pointing at a headline in f, plus f's own file-level
// dirty/saved-position bookkeeping — called after f's entire headline
// tree has been discarded and replaced (a whole-file reload), since
// none of those old headline pointers exist anywhere anymore.
func (m *Model) clearRefsForFile(f *org.File) {
	org.Walk(f.Headlines, func(h *org.Headline) {
		if m.reviewTarget == h {
			m.reviewTarget = nil
		}
		m.clearMarksFor(h)
		delete(m.dirtyHeadlines, h)
	})
	delete(m.dirty, f)
	delete(m.savedPos, f)
	delete(m.savedFork, f)
}

// editorCommand returns the external editor to launch: m.cfg.Editor
// (see WithEditor) if set, else $EDITOR (README's --editor row).
func (m Model) editorCommand() string {
	if m.cfg.Editor != "" {
		return m.cfg.Editor
	}
	return os.Getenv("EDITOR")
}

// scratchDir returns the directory editor buffers go under, resolving
// Config.ScratchDir: a leading "~" is expanded, and a relative path is
// taken from the org dir, wherever orgtd itself was started.
func (m Model) scratchDir() string {
	dir := extprog.ExpandHome(m.cfg.ScratchDir)
	if filepath.IsAbs(dir) {
		return dir
	}
	return filepath.Join(m.ws.Dir, dir)
}

// scratchFilePath returns a path for a new editor buffer under
// scratchDir, in a dated subdirectory (scratch/2026/10/02) and named
// after the current time down to the nanosecond (so two buffers opened
// in the same second never collide)
// rather than randomly — so a buffer is easy to find by when it was
// made if it's ever needed after the fact, e.g. because its content was
// accidentally discarded instead of saved. The file itself isn't
// created here; only its directory is, so the caller still controls
// how (and whether) the file gets written.
func (m *Model) scratchFilePath() (string, error) {
	now := m.now()
	dir := filepath.Join(m.scratchDir(), now.Format("2006"), now.Format("01"), now.Format("02"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, now.Format("15-04-05.000000000")+".org"), nil
}

// launchEditor writes h's own text to a scratch file (see
// scratchFilePath and entryEditText) and opens it in $EDITOR (vim by
// default), suspending the TUI for the duration, with the cursor on its
// first line in the editor's normal mode — ctrl-g from orgtd's normal
// mode. The text is followed by a blank line and a git-commit-style
// comment trailer sketching the entry's place in the outline (see
// editEntryContext). finishEdit applies the result once the editor
// exits. Returns nil if the scratch file couldn't be created or the
// editor couldn't be started, in which case the error is left in
// m.message.
func (m *Model) launchEditor(h *org.Headline) tea.Cmd {
	return m.launchEditorText(h, entryEditText(h), nil, extprog.NoPlacement, false)
}

// entryEditText is h's own text as both $EDITOR and insert mode edit
// it: no bullet, dedented by one level (see org.DedentEntry), and
// without a trailing newline.
func entryEditText(h *org.Headline) string {
	return strings.TrimRight(org.DedentEntry(org.RenderEntry(h), h.Level), "\n")
}

// launchEditorText is launchEditor with the buffer's entry text given
// explicitly — insert mode's ctrl-g hands over whatever it has typed so
// far rather than h's current content — along with where the cursor
// goes (placement), whether it's a new entry (ctx non-nil, which picks
// the trailer's wording), and resume saying where the result goes: back
// into insert mode (see resumeInsertFromEditor) rather than straight
// into the outline.
func (m *Model) launchEditorText(h *org.Headline, entry string, ctx *insertContext, placement extprog.Placement, resume bool) tea.Cmd {
	path, err := m.scratchFilePath()
	if err != nil {
		m.message = fmt.Sprintf("Could not create scratch file: %v", err)
		return nil
	}

	if !strings.HasSuffix(entry, "\n") {
		entry += "\n"
	}
	content := entry + "\n" + m.editEntryContextFor(h, ctx != nil, resume)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		os.Remove(path)
		m.message = fmt.Sprintf("Could not write scratch file: %v", err)
		return nil
	}

	editorCmd := extprog.EditorCommand(m.editorCommand(), path, "", placement)

	return tea.ExecProcess(editorCmd, func(err error) tea.Msg {
		return editFinishedMsg{path: path, target: h, resume: resume, cmd: editorCmd, err: err}
	})
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
	return m.editEntryContextFor(h, isInsert, false)
}

// editEntryContextFor is editEntryContext, with resume (ctrl-g from
// insert mode) changing the instructions to say that exiting returns to
// insert mode rather than applying the change.
func (m *Model) editEntryContextFor(h *org.Headline, isInsert, resume bool) string {
	fileName := ""
	if f := m.ws.FileOf(h); f != nil {
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
	if resume {
		fmt.Fprintln(&b, "# exit to go back to editing it in orgtd, where Esc applies it.")
	} else {
		fmt.Fprintln(&b, "# exit to apply your changes, or delete the entry's content (leaving")
		fmt.Fprintln(&b, "# only these comments, or nothing) to cancel.")
	}
	if m.cfg.URLFormatter != "" {
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

// finishEdit reads back the edited entry and applies it to msg.target
// (see applyEditedEntry) — or, for a ctrl-g from insert mode
// (msg.resume), puts it back into insert mode's buffer instead (see
// resumeInsertFromEditor). Any failure (editor error, unreadable file)
// leaves the entry, or insert mode's buffer, untouched. msg.path (see scratchFilePath) is deliberately never
// deleted here — it stays behind as a failsafe in case what comes back
// from the editor is ever lost or discarded by mistake.
func (m Model) finishEdit(msg editFinishedMsg) (tea.Model, tea.Cmd) {
	execlog.LogCompleted(m.execLog, msg.cmd, msg.err)

	var data []byte
	err := msg.err
	if err != nil {
		m.message = fmt.Sprintf("Editor exited with an error: %v", err)
	} else if data, err = os.ReadFile(msg.path); err != nil {
		m.message = fmt.Sprintf("Could not read edited entry: %v", err)
	}

	switch {
	case err != nil:
	case msg.resume:
		m.resumeInsertFromEditor(editorResultText(data))
	default:
		m.applyEditedEntry(editorResultText(data), msg.target, nil)
	}
	return m, nil
}

// editorResultText is the entry text an $EDITOR buffer came back with:
// every comment line dropped — editEntryContext's trailer, and anything
// else the user commented out — and the trailing newlines that leaves
// behind trimmed, the same way git's own cleanup mode does, rather than
// let them accumulate as stray blank body lines on every repeated edit.
func editorResultText(data []byte) string {
	return strings.TrimRight(stripCommentLines(string(data)), "\n")
}

// applyEditedEntry applies an edited entry's text — bullet-free and
// dedented, as $EDITOR and insert mode both edit it (see entryEditText)
// — to target. For a plain `i`/`A` edit, the result replaces target's
// own text in place, its children carried over unchanged since the
// buffer never showed them (see org.IndentEntry below) — or, if the
// edit left nothing parseable or changed nothing at all, target is left
// untouched, with no undo step recorded. For an o/O insert session
// (insert non-nil), a successful result is committed as a single undo
// step; an unparseable or emptied-out one rolls back the tentative
// placeholder entirely.
func (m *Model) applyEditedEntry(text string, target *org.Headline, insert *insertContext) {
	text = strings.TrimRight(text, "\n")
	if insert == nil && text == entryEditText(target) {
		return
	}

	// The buffer never showed a bullet (see org.DedentEntry), so an
	// all-blank result unambiguously means "cancel" — org.IndentEntry
	// below would otherwise turn it into a real, blank-titled headline
	// instead of nothing at all.
	if strings.TrimSpace(text) == "" {
		if insert != nil {
			m.rollbackInsert(*insert, target)
			m.message = "Insert cancelled (empty)"
		} else {
			m.message = "Edited entry had no headline; leaving it unchanged"
		}
		return
	}
	text = m.formatURLs(org.IndentEntry(text, target.Level))

	file, err := org.Parse(strings.NewReader(text), "")
	if err != nil {
		m.message = fmt.Sprintf("Could not parse edited entry: %v", err)
		if insert != nil {
			m.rollbackInsert(*insert, target)
		}
		return
	}

	if len(file.Headlines) == 0 {
		// Unreachable in practice — org.IndentEntry above guarantees
		// the text starts with a real headline line — but kept as a
		// defensive fallback rather than assuming it.
		if insert != nil {
			m.rollbackInsert(*insert, target)
			m.message = "Insert cancelled (empty)"
		} else {
			m.message = "Edited entry had no headline; leaving it unchanged"
		}
		return
	}

	if insert != nil {
		if isBlankHeadlineTitle(file.Headlines[0].Title) {
			// A headline with stars and maybe a keyword but no actual
			// text (or just a stray bullet character) is just as
			// unusable as a genuinely empty result — don't create it.
			m.rollbackInsert(*insert, target)
			m.message = "Insert cancelled (no text)"
			return
		}
		m.commitInsert(*insert, target, file.Headlines)
		if insert.switched && !insert.switchToOutline {
			// Typed in the outline only because the view it was started
			// from couldn't show it yet; now that it's committed (and
			// attached to its meeting), it can — see
			// insertContext.switchToOutline.
			m.switchToViewNoJump(insert.switchedFrom)
			m.focusHeadline(file.Headlines[0])
		}
		if insert.thenPickMeeting {
			// commitInsert (and the switch above, if it ran) already
			// focused file.Headlines[0], so the picker (see
			// startMeetingPicker) targets the just-captured entry — see
			// startCaptureAndPickMeeting ("gX").
			m.startMeetingPicker()
		}
		// insertCalendarCapture's attachMeeting, if set, was already
		// folded into commitInsert's own undo step above — nothing left
		// to do here (see insertContext.attachMeeting).
		return
	}

	// The buffer never showed target's children, so they never went
	// through the edit at all — carry them over as-is rather than
	// leaving the edited entry childless, or discarding them in favor of
	// whatever (if anything) the user happened to type at a deeper level.
	file.Headlines[0].Children = target.Children
	for _, c := range file.Headlines[0].Children {
		c.Parent = file.Headlines[0]
	}

	m.pushUndo(&subtreeReplaceAction{
		f:      m.ws.FileOf(target),
		oldSet: []*org.Headline{target},
		newSet: file.Headlines,
	})
}
