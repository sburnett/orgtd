package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// A keymap says what each key does in one of the modes that browse the
// outline (normal and visual). Keys are the strings bubbletea reports
// ("j", "down", "ctrl+d", "enter"); a two-key chord is written as its
// prefix key, a space, then the second key: "g g", "d d", "z o". Adding a
// key or chord is one bind call in normalKeys/visualKeys below.
//
// Dispatch (see Model.runKey): if a chord is pending, "<chord> <key>" is
// looked up first; failing that — or with no chord pending — the plain key
// is. A key that is the start of some chord and has no plain binding of its
// own becomes the pending chord. So a key that doesn't complete a chord
// simply does what it does on its own, with the stale prefix dropped, as in
// vim.
type keymap struct {
	actions  map[string]keyAction
	prefixes map[string]bool // keys that start a chord
}

// keyAction is what a key does.
type keyAction struct {
	// run does it, and returns a tea.Cmd for the runtime to execute (an
	// $EDITOR session, say), or nil.
	run func(m *Model) tea.Cmd

	// keepView says the action manages the viewport or the mode itself, so
	// the cursor mustn't be scrolled back into view afterward. Every other
	// action is followed by ensureVisible.
	keepView bool
}

func newKeymap() *keymap {
	return &keymap{actions: map[string]keyAction{}, prefixes: map[string]bool{}}
}

// bind makes each of keys run run. A chord key ("g g") also registers its
// prefix.
func (km *keymap) bind(run func(m *Model) tea.Cmd, keys ...string) {
	km.add(keyAction{run: run}, keys)
}

// bindKeepView is bind for an action that handles the viewport or mode
// itself (see keyAction.keepView).
func (km *keymap) bindKeepView(run func(m *Model) tea.Cmd, keys ...string) {
	km.add(keyAction{run: run, keepView: true}, keys)
}

// do adapts an action that returns no tea.Cmd, which is nearly all of them.
func do(f func(m *Model)) func(m *Model) tea.Cmd {
	return func(m *Model) tea.Cmd { f(m); return nil }
}

func (km *keymap) add(a keyAction, keys []string) {
	for _, k := range keys {
		km.actions[k] = a
		if prefix, _, isChord := strings.Cut(k, " "); isChord {
			km.prefixes[prefix] = true
		}
	}
}

// addPrefix registers key as starting a chord whose second key isn't a
// fixed binding (the mark commands, "m<letter>" and "'<letter>").
func (km *keymap) addPrefix(key string) { km.prefixes[key] = true }

// lookup finds what key does given the pending chord ("" for none).
func (km *keymap) lookup(chord, key string) (keyAction, bool) {
	if chord != "" {
		if a, ok := km.actions[chord+" "+key]; ok {
			return a, true
		}
	}
	a, ok := km.actions[key]
	return a, ok
}

// runKey carries out key in km: runs its action (consuming the pending
// chord, which the caller has already cleared from m), or starts a new
// chord, or does nothing for an unbound key. Returns the action's tea.Cmd.
func (m *Model) runKey(km *keymap, chord, key string) tea.Cmd {
	act, ok := km.lookup(chord, key)
	if !ok {
		if km.prefixes[key] {
			m.chord = key
		}
		m.ensureVisible()
		return nil
	}
	cmd := act.run(m)
	if cmd == nil && !act.keepView {
		m.ensureVisible()
	}
	return cmd
}

// addMotions binds the cursor motions normal and visual mode share —
// in visual mode they extend the selection instead of just moving, which
// needs no special case because the selection is simply anchor-to-cursor.
func (km *keymap) addMotions() {
	km.bind(do(func(m *Model) { m.moveCursor(1) }), "j", "down")
	km.bind(do(func(m *Model) { m.moveCursor(-1) }), "k", "up")
	km.bind(do(func(m *Model) { m.jumpParagraph(1) }), "}")
	km.bind(do(func(m *Model) { m.jumpParagraph(-1) }), "{")
	km.bind(do((*Model).moveDeeper), "l")
	km.bind(do((*Model).moveShallower), "h")
	km.bind(do((*Model).jumpToSubtreeTop), "^")
	km.bind(do((*Model).jumpToSubtreeBottom), "$")
	km.bind(do(func(m *Model) { m.moveCursor(m.pageSize() / 2) }), "ctrl+d")
	km.bind(do(func(m *Model) { m.moveCursor(-m.pageSize() / 2) }), "ctrl+u")
	km.bind(do(func(m *Model) { m.moveCursor(m.pageSize()) }), "pgdown")
	km.bind(do(func(m *Model) { m.moveCursor(-m.pageSize()) }), "pgup")
	km.bindKeepView(do(func(m *Model) { m.scrollView(1) }), "ctrl+e")
	km.bindKeepView(do(func(m *Model) { m.scrollView(-1) }), "ctrl+y")
}

// normalKeys and visualKeys are built in init rather than as var
// initializers: their handlers reach Model methods that read them, which
// Go rejects as an initialization cycle.
var normalKeys, visualKeys *keymap

func init() {
	normalKeys = buildNormalKeys()
	visualKeys = buildVisualKeys()
}

func buildNormalKeys() *keymap {
	km := newKeymap()
	km.addMotions()

	// Mode switches that open a prompt.
	km.bindKeepView(do(func(m *Model) {
		m.mode = commandMode
		m.commandInput = ""
		m.commandHistoryPos = len(m.commandHistory)
		m.commandHistoryDraft = ""
	}), ":")
	km.bindKeepView(do(func(m *Model) { m.startSearch(true) }), "/")
	km.bindKeepView(do(func(m *Model) { m.startSearch(false) }), "?")
	km.bind(do(func(m *Model) { m.repeatSearch(m.lastSearchForward) }), "n")
	km.bind(do(func(m *Model) { m.repeatSearch(!m.lastSearchForward) }), "N")

	// Jumps.
	km.bind(do(func(m *Model) { m.pushJump(); m.cursor = 0 }), "g g")
	km.bind(do(func(m *Model) {
		if n := len(m.rows); n > 0 {
			m.pushJump()
			// Snap up to the entry's own title row if the very last row
			// happens to be one of its body lines, so the highlight (and
			// gc/editing commands) cover the whole entry, not just its
			// last line.
			m.cursor = m.entryStart(n - 1)
		}
	}), "G")
	km.bind(do(func(m *Model) {
		if enter := m.spec().enter; enter != nil {
			enter(m)
		}
	}), "enter")
	km.bind(do((*Model).jumpBack), "ctrl+o")
	km.bind(do((*Model).jumpForward), "g i", "g I")
	km.bind(do((*Model).jumpToReviewTarget), "g c")
	km.addPrefix("\"") // "\"<register>" selects the register for the next p/P; see updateNormalMode
	km.addPrefix("m")  // "m<letter>" sets a mark; see updateNormalMode
	km.addPrefix("'")  // "'<letter>" jumps to one

	// Folding.
	km.bind(do((*Model).toggleFold), "tab", "z a")
	km.bind(do((*Model).foldOpen), "z o")
	km.bind(do((*Model).foldOpenAll), "z O")
	km.bind(do((*Model).foldClose), "z c")
	km.bind(do((*Model).foldCloseAll), "z C")
	km.bind(do((*Model).foldToggleAll), "z A")

	// Editing a single entry.
	km.bind(func(m *Model) tea.Cmd { return m.startEdit() }, "i", "I")
	km.bind(func(m *Model) tea.Cmd { return m.startEditAppend() }, "A")
	km.bind(func(m *Model) tea.Cmd { return m.insertHeadline(false) }, "o")
	km.bind(func(m *Model) tea.Cmd { return m.insertHeadline(true) }, "O")
	km.bind(func(m *Model) tea.Cmd { return m.startCapture() }, "g C")
	km.bind(func(m *Model) tea.Cmd { return m.startCaptureAndPickMeeting() }, "g X")
	km.bind(do((*Model).startMeetingPicker), "g M")
	km.bind(do((*Model).startTagPrompt), "g t")
	km.bind(do(func(m *Model) {
		m.pendingCount = 0
		if m.refuseTaskStateInReference() {
			return
		}
		m.startSetDeadline()
	}), "g d")
	km.bind(do(func(m *Model) {
		count := m.pendingCount
		m.pendingCount = 0
		if m.refuseTaskStateInReference() {
			return
		}
		if m.currentHeadline() != nil {
			m.mode = selectMode
			if count > 1 {
				m.selectModeTargets = m.headlinesInRowRange(m.countRowRange(count))
			} else {
				m.selectModeTargets = nil
			}
			m.selectFilter = ""
			m.selectIndex = m.currentStatusIndex()
		}
	}), "r", "R")

	// Delete, yank, paste, move.
	km.bind(do(func(m *Model) {
		if m.pendingCount > 1 {
			m.deleteHeadlineCount(m.pendingCount)
		} else {
			m.deleteHeadline()
		}
		m.pendingCount = 0
	}), "d d")
	km.bind(do((*Model).yankHeadline), "y y")
	km.bind(do(func(m *Model) { m.pasteHeadline(false) }), "p")
	km.bind(do(func(m *Model) { m.pasteHeadline(true) }), "P")
	km.bind(do((*Model).demoteHeadline), "> >")
	km.bind(do((*Model).promoteHeadline), "< <")

	// Visual mode, undo.
	km.bind(do(func(m *Model) {
		if len(m.rows) > 0 {
			m.mode = visualMode
			m.visualAnchor = m.cursor
		}
	}), "V")
	km.bind(do((*Model).undo), "u")
	km.bind(do((*Model).redo), "ctrl+r")
	return km
}

func buildVisualKeys() *keymap {
	km := newKeymap()
	km.addMotions()

	km.bind(do((*Model).exitVisualMode), "esc", "V")
	km.bind(do(func(m *Model) { m.cursor = 0 }), "g g")
	km.bind(do(func(m *Model) {
		if n := len(m.rows); n > 0 {
			m.cursor = m.entryStart(n - 1)
		}
	}), "G")
	km.bind(do((*Model).deleteVisualSelection), "d")
	km.bind(do((*Model).yankVisualSelection), "y")
	km.bind(do(func(m *Model) {
		if m.refuseTaskStateInReference() {
			return
		}
		if headlines := m.visualSelectedHeadlines(); len(headlines) > 0 {
			m.mode = selectMode
			m.selectModeTargets = headlines
			m.selectFilter = ""
			m.selectIndex = m.currentStatusIndex()
		}
	}), "r", "R")
	return km
}
