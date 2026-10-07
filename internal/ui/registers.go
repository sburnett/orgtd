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
//   - '0' — the last yank (not overwritten by deletes), '1'-'9' — the
//     last nine deletes, most recent first. All read-only.
//   - '_' — the black hole: always empty; writing to it stores nothing.
//   - 'a'-'z' — a named register, filled by "ayy, "add and so on.
//     'A'-'Z' reads the same register as its lowercase letter.
//   - '%' — read-only: the entry :review is currently pinning (empty
//     while review is off, or when the inbox has nothing pending), so
//     "%p/"%P pastes a copy of it.
//
// An unknown name holds nothing.
func (m *Model) registerContents(name rune) []*org.Headline {
	switch {
	case name == 0 || name == '"':
		return m.register
	case name >= 'a' && name <= 'z', name >= '0' && name <= '9':
		return m.namedRegisters[name]
	case name >= 'A' && name <= 'Z':
		return m.namedRegisters[name-'A'+'a']
	case name == '%':
		if m.reviewActive && m.reviewTarget != nil {
			return []*org.Headline{m.reviewTarget}
		}
	}
	return nil
}

// isRegisterName reports whether name can follow the " prefix.
func isRegisterName(name rune) bool {
	return name == '"' || name == '%' || name == '_' || (name >= '0' && name <= '9') || (name >= 'a' && name <= 'z') || (name >= 'A' && name <= 'Z')
}

// storeRegister is where every yank and delete (isDelete) puts what it
// took. Prefixed with a register, it goes there — replacing it, or, for
// an uppercase name ("Ayy), appending to its lowercase counterpart, as in
// vim — and into the unnamed register too, so a bare p/P pastes whatever
// was stored last. Prefixed with "_ (the black hole) it goes nowhere,
// leaving every register as it was. With no prefix it's the unnamed
// register, plus the numbered history: a yank fills "0, a delete shifts
// "1-"8 down to "2-"9 and fills "1.
func (m *Model) storeRegister(headlines []*org.Headline, isDelete bool) {
	name := m.activeRegister
	switch {
	case name == '_':
		return
	case name >= 'a' && name <= 'z':
		m.setNamedRegister(name, headlines)
	case name >= 'A' && name <= 'Z':
		lower := name - 'A' + 'a'
		joined := append(append([]*org.Headline(nil), m.namedRegisters[lower]...), headlines...)
		m.setNamedRegister(lower, joined)
		headlines = joined
	case !isDelete:
		m.setNamedRegister('0', headlines)
	default:
		for r := '9'; r > '1'; r-- {
			m.setNamedRegister(r, m.namedRegisters[r-1])
		}
		m.setNamedRegister('1', headlines)
	}
	m.register = headlines
}

func (m *Model) setNamedRegister(name rune, headlines []*org.Headline) {
	if m.namedRegisters == nil {
		m.namedRegisters = map[rune][]*org.Headline{}
	}
	m.namedRegisters[name] = headlines
}

// namedRegisterNames returns the names of the non-empty named and
// numbered registers: letters alphabetically, then digits.
func (m *Model) namedRegisterNames() []rune {
	var names []rune
	for r, hs := range m.namedRegisters {
		if len(hs) > 0 {
			names = append(names, r)
		}
	}
	isDigit := func(r rune) bool { return r >= '0' && r <= '9' }
	sort.Slice(names, func(i, j int) bool {
		if di, dj := isDigit(names[i]), isDigit(names[j]); di != dj {
			return dj
		}
		return names[i] < names[j]
	})
	return names
}

// clearRegisters empties the unnamed register and every named and
// numbered one, and turns the "%" register off (it isn't stored, so
// "clearing" it means unpinning the review target).
func (m *Model) clearRegisters() {
	m.register = nil
	m.namedRegisters = nil
	m.deactivateReview()
}

// refuseReadOnlyRegister reports (with a status message) whether the
// command being run was prefixed with a register that can't be written
// to: "% holds whatever :review is pinning, and "0-"9 are the yank and
// delete history, filled automatically.
func (m *Model) refuseReadOnlyRegister() bool {
	if r := m.activeRegister; r == '%' || (r >= '0' && r <= '9') {
		m.message = "Register " + string(r) + " is read-only"
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
