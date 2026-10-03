package ui

import (
	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) updateNormalMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	wasPendingG := m.pendingG
	wasPendingD := m.pendingD
	wasPendingY := m.pendingY
	wasPendingGT := m.pendingGT
	wasPendingLT := m.pendingLT
	wasPendingZ := m.pendingZ
	wasPendingM := m.pendingM
	wasPendingQuote := m.pendingQuote
	m.pendingG = false
	m.pendingD = false
	m.pendingY = false
	m.pendingGT = false
	m.pendingLT = false
	m.pendingZ = false
	m.pendingM = false
	m.pendingQuote = false
	m.message = ""

	// "m<letter>" and "'<letter>" take an arbitrary a-z argument, unlike
	// every other chord here (which pairs two fixed keys) — so these are
	// intercepted before the switch below, rather than adding a
	// wasPendingM/wasPendingQuote check to every single-letter case that
	// already means something else on its own (r, d, p, ...).
	if wasPendingM {
		if len(key) == 1 && key[0] >= 'a' && key[0] <= 'z' {
			m.setMark(rune(key[0]))
		}
		m.ensureVisible()
		return m, nil
	}
	if wasPendingQuote {
		if len(key) == 1 && key[0] >= 'a' && key[0] <= 'z' {
			m.jumpToMark(rune(key[0]))
		}
		m.ensureVisible()
		return m, nil
	}

	// A digit builds up a numeric prefix for "dd"/"r"/"R" (e.g. "3dd",
	// "2r", "2R") instead of being handled by the switch below —
	// intercepted here for the same reason as m/' above. A leading zero
	// (no digits typed yet) is not a valid count on its own — there's no
	// "0" command to distinguish it from — so it falls through as a
	// plain, currently unbound key instead of starting a count. Any other
	// key that isn't "d", "r", or "R" themselves clears a pending count
	// rather than silently applying to some other command later — the
	// prefix is scoped to exactly these three.
	if len(key) == 1 && key[0] >= '0' && key[0] <= '9' {
		if d := int(key[0] - '0'); d > 0 || m.pendingCount > 0 {
			m.pendingCount = m.pendingCount*10 + d
		}
		m.ensureVisible()
		return m, nil
	}
	if key != "d" && key != "r" && key != "R" {
		m.pendingCount = 0
	}

	switch key {
	case ":":
		m.mode = commandMode
		m.commandInput = ""
		m.commandHistoryPos = len(m.commandHistory)
		m.commandHistoryDraft = ""
		return m, nil

	case "/":
		m.mode = searchMode
		m.searchForward = true
		m.searchOrigin = m.cursor
		m.searchQuery = ""
		m.message = ""
		return m, nil

	case "?":
		m.mode = searchMode
		m.searchForward = false
		m.searchOrigin = m.cursor
		m.searchQuery = ""
		m.message = ""
		return m, nil

	case "n":
		m.repeatSearch(m.lastSearchForward)

	case "N":
		m.repeatSearch(!m.lastSearchForward)

	case "j", "down":
		m.moveCursor(1)

	case "k", "up":
		m.moveCursor(-1)

	case "}":
		m.jumpParagraph(1)

	case "{":
		m.jumpParagraph(-1)

	case "l":
		m.moveDeeper()

	case "h":
		m.moveShallower()

	case "g":
		if wasPendingG {
			m.pushJump()
			m.cursor = 0
		} else {
			m.pendingG = true
		}

	case "G":
		if n := len(m.rows); n > 0 {
			m.pushJump()
			// Snap up to the entry's own title row if the very last row
			// happens to be one of its body lines, so the highlight (and
			// gc/editing commands) cover the whole entry, not just its
			// last line.
			m.cursor = m.entryStart(n - 1)
		}

	case "^":
		m.jumpToSubtreeTop()

	case "$":
		m.jumpToSubtreeBottom()

	case "enter":
		if enter := m.spec().enter; enter != nil {
			enter(&m)
		}

	case "tab":
		m.toggleFold()

	case "ctrl+o":
		m.jumpBack()

	case "r", "R":
		count := m.pendingCount
		m.pendingCount = 0
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

	case "V":
		if len(m.rows) > 0 {
			m.mode = visualMode
			m.visualAnchor = m.cursor
		}

	case "i", "I":
		if wasPendingG {
			m.jumpForward()
		} else if cmd := m.startEdit(); cmd != nil {
			return m, cmd
		}

	case "o":
		if wasPendingZ {
			m.foldOpen()
		} else if cmd := m.insertHeadline(false); cmd != nil {
			return m, cmd
		}

	case "O":
		if wasPendingZ {
			m.foldOpenAll()
		} else if cmd := m.insertHeadline(true); cmd != nil {
			return m, cmd
		}

	case "z":
		m.pendingZ = true

	case "m":
		m.pendingM = true

	case "'":
		m.pendingQuote = true

	case "c":
		switch {
		case wasPendingZ:
			m.foldClose()
		case wasPendingG:
			m.jumpToClarifyTarget()
		}

	case "C":
		if wasPendingZ {
			m.foldCloseAll()
		} else if wasPendingG {
			if cmd := m.startCapture(); cmd != nil {
				return m, cmd
			}
		}

	case "M":
		if wasPendingG {
			m.startMeetingPicker()
		}

	case "X":
		if wasPendingG {
			if cmd := m.startCaptureAndPickMeeting(); cmd != nil {
				return m, cmd
			}
		}

	case "t":
		if wasPendingG {
			m.startTagPrompt()
		}

	case "a":
		if wasPendingZ {
			m.toggleFold()
		}

	case "A":
		if wasPendingZ {
			m.foldToggleAll()
		} else if cmd := m.startEditAppend(); cmd != nil {
			return m, cmd
		}

	case "u":
		m.undo()

	case "ctrl+r":
		m.redo()

	case "d":
		if wasPendingG {
			m.startSetDeadline()
			m.pendingCount = 0
		} else if wasPendingD {
			if m.pendingCount > 1 {
				m.deleteHeadlineCount(m.pendingCount)
			} else {
				m.deleteHeadline()
			}
			m.pendingCount = 0
		} else {
			m.pendingD = true
		}

	case ">":
		if wasPendingGT {
			m.demoteHeadline()
		} else {
			m.pendingGT = true
		}

	case "<":
		if wasPendingLT {
			m.promoteHeadline()
		} else {
			m.pendingLT = true
		}

	case "y":
		if wasPendingY {
			m.yankHeadline()
		} else {
			m.pendingY = true
		}

	case "p":
		m.pasteHeadline(false)

	case "P":
		m.pasteHeadline(true)

	case "ctrl+d":
		m.moveCursor(m.pageSize() / 2)

	case "ctrl+u":
		m.moveCursor(-m.pageSize() / 2)

	case "pgdown":
		m.moveCursor(m.pageSize())

	case "pgup":
		m.moveCursor(-m.pageSize())

	case "ctrl+e":
		m.scrollView(1)
		return m, nil

	case "ctrl+y":
		m.scrollView(-1)
		return m, nil
	}

	m.ensureVisible()
	return m, nil
}
