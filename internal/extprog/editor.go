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
// put the cursor on a specific column, not just a line (see CursorArg).
// emacs/emacsclient and nano share the "+N" line convention but not
// that, so they just get a bare "+N" for the placement's line.
var vimFamily = map[string]bool{
	"vi": true, "vim": true, "nvim": true, "gvim": true, "mvim": true,
}

// Placement says where EditorCommand puts the cursor within the
// content (the text after EditorCommand's before). The editor always
// starts in its own normal mode. The zero value, NoPlacement, just
// lands on the content's first line; CaretAt builds the other kind.
type Placement struct {
	// Caret says to land on Line/Col — Col only for a vim-family editor
	// (see vimFamily); any other editor in editorsWithLineArg still
	// lands on Line.
	Caret bool
	Line  int // 1-based line within the content
	Col   int // 1-based byte column within that line (vim's cursor() counts bytes)
}

// NoPlacement lands on the content's first line: for a whole-file edit,
// and for ctrl-g from orgtd's normal mode.
var NoPlacement = Placement{}

// CaretAt places the cursor on row (0-based) of text, at runeCol
// (0-based, in runes, as orgtd's own insert mode counts) — for ctrl-g
// from orgtd's insert mode, so the editor opens where its caret was. A
// caret past the line's last character lands on that character, as
// vim's cursor() clamps it, the closest normal mode can get.
func CaretAt(text string, row, runeCol int) Placement {
	lines := strings.Split(text, "\n")
	row = max(0, min(row, len(lines)-1))
	runes := []rune(lines[row])
	runeCol = max(0, min(runeCol, len(runes)))
	return Placement{
		Caret: true,
		Line:  row + 1,
		Col:   len(string(runes[:runeCol])) + 1,
	}
}

// CursorArg returns the "+..." argument EditorCommand should pass for
// the given editor basename, with the content starting on startLine
// (1-based). A vim-family editor with a caret placement gets cursor(),
// leaving it in normal mode; anything else a bare "+N" for the
// placement's line.
func CursorArg(editorBase string, startLine int, p Placement) string {
	line := startLine
	if p.Caret {
		line += p.Line - 1
		if vimFamily[editorBase] {
			return fmt.Sprintf("+call cursor(%d,%d)", line, p.Col)
		}
	}
	return fmt.Sprintf("+%d", line)
}

// EditorCommand builds the *exec.Cmd for opening path in the editor
// named by editorEnv ($EDITOR's value; "vim" if empty), splitting off
// any extra words as leading arguments (e.g. "code --wait"). For an
// editor in editorsWithLineArg, it also inserts a "+..." argument (see
// CursorArg) so the editor opens with the cursor on the real
// content — or, for a vim-family editor with placement other than
// NoPlacement, at a specific column within it (before is the context text written ahead of it in the
// file; its newline count is exactly the 1-based line the real content
// starts on).
func EditorCommand(editorEnv, path, before string, placement Placement) *exec.Cmd {
	fields := ParseCommand(editorEnv)
	if len(fields) == 0 {
		fields = []string{"vim"}
	}
	args := append([]string{}, fields[1:]...)
	base := filepath.Base(fields[0])
	if editorsWithLineArg[base] {
		startLine := strings.Count(before, "\n") + 1
		args = append(args, CursorArg(base, startLine, placement))
	}
	args = append(args, path)
	return exec.Command(fields[0], args...)
}
