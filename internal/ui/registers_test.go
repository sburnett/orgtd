package ui

import (
	"strings"
	"testing"
)

const (
	vetTitle = "Call the vet about Fido's checkup"
	rfcTitle = "Read the RFC linked in yesterday's design review"
)

func countTitle(m Model, title string) (n int) {
	for _, r := range m.rows {
		if r.kind == rowHeadline && r.headline.Title == title {
			n++
		}
	}
	return n
}

func TestYankIntoNamedRegisterAndPasteFromIt(t *testing.T) {
	m := New(loadFixture(t))
	m.cursor = findRow(t, m, vetTitle)
	m = typeKeys(m, `"ayy`)

	m.cursor = findRow(t, m, rfcTitle)
	m = typeKeys(m, `yy`) // overwrites the unnamed register only
	if got := m.namedRegisters['a']; len(got) != 1 || got[0].Title != vetTitle {
		t.Fatalf("register a = %v, want the vet entry", got)
	}
	if m.register[0].Title != rfcTitle {
		t.Fatalf("unnamed register = %v, want the RFC entry", m.register)
	}

	m = typeKeys(m, `"ap`)
	if got := countTitle(m, vetTitle); got != 2 {
		t.Errorf(`"ap: vet entry appears %d times, want 2`, got)
	}
	m = typeKeys(m, `"aP`)
	if got := countTitle(m, vetTitle); got != 3 {
		t.Errorf(`"aP: vet entry appears %d times, want 3`, got)
	}
}

func TestDeleteIntoNamedRegister(t *testing.T) {
	m := New(loadFixture(t))
	m.cursor = findRow(t, m, vetTitle)
	m = typeKeys(m, `"add`)

	if countTitle(m, vetTitle) != 0 {
		t.Errorf(`"add left the entry in place`)
	}
	if got := m.namedRegisters['a']; len(got) != 1 || got[0].Title != vetTitle {
		t.Errorf("register a = %v, want the deleted entry", got)
	}
	if len(m.register) != 1 || m.register[0].Title != vetTitle {
		t.Errorf("unnamed register = %v, want it to follow the last write", m.register)
	}
}

func TestCapitalYankCapitalYAppendsAndBigYYanks(t *testing.T) {
	m := New(loadFixture(t))
	m.cursor = findRow(t, m, vetTitle)
	m = typeKeys(m, `"aY`)
	m.cursor = findRow(t, m, rfcTitle)
	m = typeKeys(m, `"AY`)

	got := m.namedRegisters['a']
	if len(got) != 2 || got[0].Title != vetTitle || got[1].Title != rfcTitle {
		t.Fatalf("register a = %v, want [vet, rfc]", got)
	}
	before := countTitle(m, vetTitle)
	m = typeKeys(m, `"ap`)
	if countTitle(m, vetTitle) != before+1 || countTitle(m, rfcTitle) != 2 {
		t.Errorf(`"ap should paste both appended entries`)
	}
}

func TestVisualYankAndDeleteIntoNamedRegister(t *testing.T) {
	m := New(loadFixture(t))
	m.cursor = findRow(t, m, vetTitle)
	m = typeKeys(m, `Vj"by`)
	if len(m.namedRegisters['b']) != 2 {
		t.Fatalf("register b = %v, want 2 entries from the visual yank", m.namedRegisters['b'])
	}
	m.cursor = findRow(t, m, vetTitle)
	m = typeKeys(m, `V"cd`)
	if len(m.namedRegisters['c']) != 1 || countTitle(m, vetTitle) != 0 {
		t.Errorf("visual \"cd: register c = %v, vet entries left = %d", m.namedRegisters['c'], countTitle(m, vetTitle))
	}
}

func TestCountSurvivesRegisterPrefix(t *testing.T) {
	m := New(loadFixture(t))
	m.cursor = findRow(t, m, vetTitle)
	m = typeKeys(m, `"a2dd`)
	if len(m.namedRegisters['a']) != 2 {
		t.Errorf(`"a2dd: register a = %v, want 2 entries`, m.namedRegisters['a'])
	}
}

func TestRegistersSectionListsNamedRegisters(t *testing.T) {
	m := New(loadFixture(t))
	m.cursor = findRow(t, m, vetTitle)
	m = typeKeys(m, `"zyy`)
	lines := stripANSILines(m.infoBufferLines())
	var found bool
	for _, l := range lines {
		if strings.HasPrefix(l, "z ") && strings.Contains(l, vetTitle) {
			found = true
		}
	}
	if !found {
		t.Errorf("info buffer = %q, want a row for register z", lines)
	}
}

func TestClearRegistersClearsNamedToo(t *testing.T) {
	m := New(loadFixture(t))
	m.cursor = findRow(t, m, vetTitle)
	m = typeKeys(m, `"ayy`)
	m = typeKeys(m, `:clear-registers`)
	m, _ = sendKeyCmd(m, "enter")
	if len(m.namedRegisters['a']) != 0 || m.register != nil {
		t.Errorf("registers not cleared: a=%v unnamed=%v", m.namedRegisters['a'], m.register)
	}
}

func TestCountedYankYanksThatManyEntries(t *testing.T) {
	for _, keys := range []string{`3yy`, `3Y`, `"a3yy`} {
		m := New(loadFixture(t))
		m.cursor = findRow(t, m, vetTitle)
		before := len(m.rows)
		m = typeKeys(m, keys)
		if len(m.register) != 3 {
			t.Errorf("%s: register has %d entries, want 3", keys, len(m.register))
		}
		if len(m.rows) != before {
			t.Errorf("%s changed the outline", keys)
		}
		if keys == `"a3yy` && len(m.namedRegisters['a']) != 3 {
			t.Errorf("%s: register a has %d entries, want 3", keys, len(m.namedRegisters['a']))
		}
	}
}
