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

func TestBlackHoleDeleteLeavesRegistersAlone(t *testing.T) {
	m := New(loadFixture(t))
	m.cursor = findRow(t, m, rfcTitle)
	m = typeKeys(m, `yy`)
	m.cursor = findRow(t, m, vetTitle)
	m = typeKeys(m, `"_dd`)

	if countTitle(m, vetTitle) != 0 {
		t.Errorf(`"_dd didn't delete the entry`)
	}
	if len(m.register) != 1 || m.register[0].Title != rfcTitle {
		t.Errorf("unnamed register = %v, want the earlier RFC yank untouched", m.register)
	}
	if len(m.namedRegisters['1']) != 0 {
		t.Errorf(`"_dd filled register 1: %v`, m.namedRegisters['1'])
	}
	m = typeKeys(m, `"_p`)
	if m.message != "Nothing to paste" {
		t.Errorf(`"_p message = %q, want Nothing to paste`, m.message)
	}
}

func TestYankRegisterSurvivesDeletes(t *testing.T) {
	m := New(loadFixture(t))
	m.cursor = findRow(t, m, rfcTitle)
	m = typeKeys(m, `yy`)
	m.cursor = findRow(t, m, vetTitle)
	m = typeKeys(m, `dd`)

	if m.register[0].Title != vetTitle {
		t.Fatalf("unnamed register = %v, want the deleted entry", m.register)
	}
	if got := m.namedRegisters['0']; len(got) != 1 || got[0].Title != rfcTitle {
		t.Fatalf("register 0 = %v, want the yanked RFC entry", got)
	}
	before := countTitle(m, rfcTitle)
	m = typeKeys(m, `"0p`)
	if countTitle(m, rfcTitle) != before+1 {
		t.Errorf(`"0p didn't paste the yanked entry`)
	}
}

func TestNamedYankLeavesYankRegisterAlone(t *testing.T) {
	m := New(loadFixture(t))
	m.cursor = findRow(t, m, vetTitle)
	m = typeKeys(m, `"ayy`)
	if len(m.namedRegisters['0']) != 0 {
		t.Errorf(`"ayy filled register 0: %v`, m.namedRegisters['0'])
	}
}

func TestDeleteHistoryShiftsDown(t *testing.T) {
	m := New(loadFixture(t))
	var titles []string
	for _, h := range findInboxHeadlines(t, m)[:3] {
		titles = append(titles, h.Title)
	}
	for range titles {
		m.cursor = findRow(t, m, findInboxHeadlines(t, m)[0].Title)
		m = typeKeys(m, `dd`)
	}
	for i, want := range []string{titles[2], titles[1], titles[0]} {
		reg := rune('1' + i)
		if got := m.namedRegisters[reg]; len(got) != 1 || got[0].Title != want {
			t.Errorf("register %c = %v, want %q", reg, got, want)
		}
	}
}

func TestNumberedRegistersAreReadOnlyAndNamedDeleteSkipsHistory(t *testing.T) {
	m := New(loadFixture(t))
	m.cursor = findRow(t, m, vetTitle)
	m = typeKeys(m, `"1yy`)
	if !strings.Contains(m.message, "read-only") || len(m.namedRegisters['1']) != 0 {
		t.Errorf(`"1yy: message = %q, register 1 = %v, want a refusal`, m.message, m.namedRegisters['1'])
	}
	m = typeKeys(m, `"add`)
	if len(m.namedRegisters['1']) != 0 {
		t.Errorf(`"add shifted the delete history: %v`, m.namedRegisters['1'])
	}
}

func TestCountedPasteRepeatsRegister(t *testing.T) {
	m := New(loadFixture(t))
	m.cursor = findRow(t, m, vetTitle)
	m = typeKeys(m, `yy`)
	before := countTitle(m, vetTitle)
	m = typeKeys(m, `3p`)
	if got := countTitle(m, vetTitle); got != before+3 {
		t.Errorf("3p: %d copies, want %d", got, before+3)
	}
	m = typeKeys(m, `u`)
	if got := countTitle(m, vetTitle); got != before {
		t.Errorf("one undo after 3p left %d copies, want %d", got, before)
	}
	m = typeKeys(m, `2"0P`)
	if got := countTitle(m, vetTitle); got != before+2 {
		t.Errorf(`2"0P: %d copies, want %d`, got, before+2)
	}
	m = typeKeys(m, `p`)
	if got := countTitle(m, vetTitle); got != before+3 {
		t.Errorf("a later bare p reused the count: %d copies, want %d", got, before+3)
	}
}

func TestRegistersSectionIsCappedOverall(t *testing.T) {
	m := New(loadFixture(t))
	m.cursor = findRow(t, m, vetTitle)
	for _, r := range "abcdefghijklmnopqrstuvwxyz" {
		m = typeKeys(m, `"`+string(r)+`yy`)
	}
	lines := stripANSILines(m.registerPinnedLines())
	if len(lines) != 1+maxInfoBufferLines+1 {
		t.Fatalf("registers section has %d lines, want label + %d rows + summary", len(lines), maxInfoBufferLines)
	}
	if last := lines[len(lines)-1]; !strings.Contains(last, "more registers") {
		t.Errorf("last line = %q, want an overflow summary", last)
	}
	if !strings.HasPrefix(lines[1], `" `) || !strings.HasPrefix(lines[2], "a ") {
		t.Errorf("lines = %q, want the unnamed then named registers first", lines[1:3])
	}
}

func TestNamedRegistersOutlastNumberedHistoryWhenTruncated(t *testing.T) {
	m := New(loadFixture(t))
	names := m.namedRegisterNames()
	if len(names) != 0 {
		t.Fatalf("fixture assumption broken: %q", string(names))
	}
	for i := '1'; i <= '9'; i++ {
		m.setNamedRegister(i, m.ws.Files[0].Headlines[:1])
	}
	m.setNamedRegister('z', m.ws.Files[0].Headlines[:1])
	if got := string(m.namedRegisterNames()); got != "z123456789" {
		t.Errorf("names = %q, want letters before digits", got)
	}
}
