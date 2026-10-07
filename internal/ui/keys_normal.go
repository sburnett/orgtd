package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// updateNormalMode handles a key in normal mode: first the things that
// aren't a fixed key or chord — the argument chords "m<letter>" and
// "'<letter>", and the numeric count prefix — then the key itself via the
// normalKeys table (see keymap.go).
func (m Model) updateNormalMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	chord := m.chord
	m.chord = ""
	m.message = ""

	// The register name after a '"' prefix (see runWithRegister).
	if chord == "\"" {
		m.runWithRegister(normalKeys, chord, key)
		return m, nil
	}

	// "m<letter>" and "'<letter>" take an arbitrary a-z argument, unlike
	// every other chord (which pairs two fixed keys), so they can't be
	// table entries.
	if chord == "m" || chord == "'" {
		if len(key) == 1 && key[0] >= 'a' && key[0] <= 'z' {
			if chord == "m" {
				m.setMark(rune(key[0]))
			} else {
				m.jumpToMark(rune(key[0]))
			}
		}
		m.ensureVisible()
		return m, nil
	}

	// A digit builds up a numeric prefix for "dd"/"r"/"R" and the fold
	// commands (e.g. "3dd", "2r", "2R", "2zc"). A leading zero (no digits
	// typed yet) is not a valid count on its own — there's no "0" command to distinguish it from —
	// so it falls through as a plain, currently unbound key instead of
	// starting a count. Any other key clears a pending count rather than
	// silently applying to some other command later (see keepCount below).
	if len(key) == 1 && key[0] >= '0' && key[0] <= '9' {
		if d := int(key[0] - '0'); d > 0 || m.pendingCount > 0 {
			m.pendingCount = m.pendingCount*10 + d
		}
		m.ensureVisible()
		return m, nil
	}
	// The fold commands take a count too ("2zc"): the "z" itself and the
	// key completing it keep it pending; a fold action then consumes it.
	keepCount := key == "\"" || key == "d" || key == "y" || key == "Y" || key == "r" || key == "R" || key == "z" || key == "tab"
	if chord == "z" && strings.Contains("aoc", key) {
		keepCount = true
	}
	if !keepCount {
		m.pendingCount = 0
	}

	cmd, _ := m.runWithRegister(normalKeys, chord, key)
	return m, cmd
}
