package ui

import "github.com/sburnett/orgtd/internal/org"

// registerContents returns what the named register holds, vim-style:
//
//   - 0 or '"' — the unnamed register, filled by dd/yy and pasted by a bare p/P.
//   - '%' — read-only: the entry :review is currently pinning (empty
//     outside review view, or when the inbox has nothing pending), so
//     "%p/"%P pastes a copy of it.
//
// An unknown name holds nothing.
func (m *Model) registerContents(name rune) []*org.Headline {
	switch name {
	case 0, '"':
		return m.register
	case '%':
		if m.view == reviewView && m.reviewTarget != nil {
			return []*org.Headline{m.reviewTarget}
		}
	}
	return nil
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
