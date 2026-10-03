package extprog

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// editorsWithLineArg lists $EDITOR basenames known to support a leading
// "+N" argument that opens the file with the cursor on line N — a
// convention shared by vi/vim, emacs, and nano. EditorCommand uses this
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
// CursorArg). emacs/emacsclient and nano share the "+N" line
// convention but have no equivalent notion of "insert mode" to start
// (nano isn't modal; plain emacs isn't either), so they always just get
// a bare "+N" regardless of placement.
var vimFamily = map[string]bool{
	"vi": true, "vim": true, "nvim": true, "gvim": true, "mvim": true,
}

// Placement selects where EditorCommand positions the cursor,
// and whether it starts the editor directly in insert mode, when the
// configured editor is vim-family (see vimFamily) — a plain "+N" line
// jump for anything else, or for NoPlacement.
type Placement int

const (
	// NoPlacement just lands on the entry's first line, same as
	// always — used only for a whole-file edit (startEditFile), where
	// there's no single entry to position a cursor within.
	NoPlacement Placement = iota
	// AtEntryStart ("i", "o"/"O") puts the cursor at the very start
	// of the entry's own text — column 1, since the buffer never shows a
	// bullet to land after (see dedentEntry, launchEditor, and
	// ResolveEntryPlacement) — in insert mode, so typing
	// immediately inserts text there exactly as pressing vim's own "i" at
	// that spot would. For o/O the entry is a blank template, so this is
	// also where its title will end up starting.
	AtEntryStart
	// AtLineEnd ("A") puts the cursor at the end of the entry's
	// first line, in insert mode — vim's own "A" (append at end of
	// line), landing on whichever text (keyword, title, tags) the line
	// actually ends with.
	AtLineEnd
)

// CursorArg returns the "+..." argument EditorCommand
// should pass for the given editor basename, startLine (1-based), col
// (1-based, meaningful only for AtEntryStart), and placement.
// Non-vim-family editors (or NoPlacement) always get a bare
// "+startLine" — see Placement and vimFamily.
func CursorArg(editorBase string, startLine, col int, placement Placement) string {
	if vimFamily[editorBase] {
		switch placement {
		case AtEntryStart:
			return fmt.Sprintf("+call cursor(%d,%d)|startinsert", startLine, col)
		case AtLineEnd:
			// startinsert! is vim's own "A": moves to the end of the
			// current line before entering insert mode, so there's no
			// need to compute or pass a column at all.
			return fmt.Sprintf("+%d|startinsert!", startLine)
		}
	}
	return fmt.Sprintf("+%d", startLine)
}

// ResolveEntryPlacement returns the placement and (1-based) column
// launchEditor should actually request for a dedented entry buffer (see
// dedentEntry): AtEntryStart always targets column 1, since
// there's no bullet to land after — for either an existing
// entry ("i"/"A") or a blank o/O template alike. It downgrades to
// AtLineEnd when the first line is completely empty (a
// just-started o/O insert, or an existing entry with a blank title):
// vim's cursor()+startinsert needs the target column to be an existing
// character — cursor() clamps to the line's last real character rather
// than allowing a column one past the end — so requesting column 1 on a
// zero-length line would fail. AtLineEnd's startinsert! (append)
// sidesteps this entirely: on an empty line, "end of line" and "column
// 1" are the exact same position anyway.
func ResolveEntryPlacement(entry string, placement Placement) (Placement, int) {
	if placement == AtEntryStart {
		firstLine, _, _ := strings.Cut(entry, "\n")
		if len(firstLine) == 0 {
			placement = AtLineEnd
		}
	}
	return placement, 1
}

// EditorCommand builds the *exec.Cmd for opening path in the editor
// named by editorEnv ($EDITOR's value; "vim" if empty), splitting off
// any extra words as leading arguments (e.g. "code --wait"). For an
// editor in editorsWithLineArg, it also inserts a "+..." argument (see
// CursorArg) so the editor opens with the cursor on the real
// content — or, for a vim-family editor with placement other than
// NoPlacement, at a specific column within it and already in
// insert mode (before is the context text written ahead of it in the
// file; its newline count is exactly the 1-based line the real content
// starts on). col is the 1-based column AtEntryStart should land
// on; unused otherwise.
func EditorCommand(editorEnv, path, before string, placement Placement, col int) *exec.Cmd {
	fields := ParseCommand(editorEnv)
	if len(fields) == 0 {
		fields = []string{"vim"}
	}
	args := append([]string{}, fields[1:]...)
	base := filepath.Base(fields[0])
	if editorsWithLineArg[base] {
		startLine := strings.Count(before, "\n") + 1
		args = append(args, CursorArg(base, startLine, col, placement))
	}
	args = append(args, path)
	return exec.Command(fields[0], args...)
}
