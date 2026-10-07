package ui

import (
	"sort"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sburnett/orgtd/internal/org"
)

// registerContents returns what the named register holds, vim-style:
//
//   - 0 or '"' — the unnamed register, filled by dd/yy (and by whichever
//     register those last wrote to) and pasted by a bare p/P.
//   - 'a'-'z' — a named register, filled by "ayy, "add and so on.
//     'A'-'Z' reads the same register as its lowercase letter.
//   - '%' — read-only: the entry :review is currently pinning (empty
//     outside review view, or when the inbox has nothing pending), so
//     "%p/"%P pastes a copy of it.
//
// An unknown name holds nothing.
func (m *Model) registerContents(name rune) []*org.Headline {
	switch {
	case name == 0 || name == '"':
		return m.register
	case name >= 'a' && name <= 'z':
		return m.namedRegisters[name]
	case name >= 'A' && name <= 'Z':
		return m.namedRegisters[name-'A'+'a']
	case name == '%':
		if m.view == reviewView && m.reviewTarget != nil {
			return []*org.Headline{m.reviewTarget}
		}
	}
	return nil
}

// isRegisterName reports whether name can follow the " prefix.
func isRegisterName(name rune) bool {
	return name == '"' || name == '%' || (name >= 'a' && name <= 'z') || (name >= 'A' && name <= 'Z')
}

// storeRegister is where every yank and delete puts what it took: into
// the register the command was prefixed with — replacing it, or, for an
// uppercase name ("Ayy), appending to its lowercase counterpart, as in
// vim — and always into the unnamed register too, so a bare p/P pastes
// whatever was stored last. With no prefix it's just the unnamed register.
func (m *Model) storeRegister(headlines []*org.Headline) {
	name := m.activeRegister
	switch {
	case name >= 'a' && name <= 'z':
		m.setNamedRegister(name, headlines)
	case name >= 'A' && name <= 'Z':
		lower := name - 'A' + 'a'
		joined := append(append([]*org.Headline(nil), m.namedRegisters[lower]...), headlines...)
		m.setNamedRegister(lower, joined)
		headlines = joined
	}
	m.register = headlines
}

func (m *Model) setNamedRegister(name rune, headlines []*org.Headline) {
	if m.namedRegisters == nil {
		m.namedRegisters = map[rune][]*org.Headline{}
	}
	m.namedRegisters[name] = headlines
}

// namedRegisterLetters returns the letters of the non-empty named
// registers, in alphabetical order.
func (m *Model) namedRegisterLetters() []rune {
	var letters []rune
	for r, hs := range m.namedRegisters {
		if len(hs) > 0 {
			letters = append(letters, r)
		}
	}
	sort.Slice(letters, func(i, j int) bool { return letters[i] < letters[j] })
	return letters
}

// clearRegisters empties the unnamed and every named register (the "%"
// register isn't stored, so there's nothing to clear there).
func (m *Model) clearRegisters() {
	m.register = nil
	m.namedRegisters = nil
}

// refuseReadOnlyRegister reports (with a status message) whether the
// command being run was prefixed with a register that can't be written
// to — "%" holds whatever :review is pinning, so "%dd or "%yy would
// have nothing sensible to store.
func (m *Model) refuseReadOnlyRegister() bool {
	if m.activeRegister == '%' {
		m.message = "Register % is read-only"
		return true
	}
	return false
}

// runWithRegister runs key in km with the pending "<register> prefix (if
// any) handed to it as m.activeRegister, so the command's own
// storeRegister/registerContents calls see it. The prefix applies to
// exactly one command: it's cleared afterward, unless key only started a
// chord ("d" of "dd"), in which case it's kept for the key that completes
// it. A key that is itself the register name after a pending " is
// consumed here and returns handled=true.
func (m *Model) runWithRegister(km *keymap, chord, key string) (cmd tea.Cmd, handled bool) {
	if chord == "\"" {
		if r := []rune(key); len(r) == 1 && isRegisterName(r[0]) {
			m.pendingRegister = r[0]
		} else {
			m.message = "Unknown register: " + key
		}
		m.ensureVisible()
		return nil, true
	}
	m.activeRegister, m.pendingRegister = m.pendingRegister, 0
	cmd = m.runKey(km, chord, key)
	if m.chord != "" {
		m.pendingRegister = m.activeRegister
	}
	m.activeRegister = 0
	return cmd, false
}
