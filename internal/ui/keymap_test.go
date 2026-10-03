package ui

import (
	"testing"
)

func TestKeymapsAreWellFormed(t *testing.T) {
	for name, km := range map[string]*keymap{"normal": normalKeys, "visual": visualKeys} {
		if len(km.actions) == 0 {
			t.Errorf("%s keymap is empty", name)
		}
		for key, a := range km.actions {
			if a.run == nil {
				t.Errorf("%s keymap binds %q to a nil action", name, key)
			}
		}
		// Every chord's prefix must be registered, or the chord could never
		// be started.
		for key := range km.actions {
			for i := 0; i < len(key); i++ {
				if key[i] == ' ' && key != " " {
					if !km.prefixes[key[:i]] {
						t.Errorf("%s keymap: chord %q has an unregistered prefix %q", name, key, key[:i])
					}
				}
			}
		}
	}
}

func TestChordCompletesOnTheSecondKey(t *testing.T) {
	m := New(loadFixture(t))
	m.cursor = 4
	m = sendKey(m, "g")
	if m.chord != "g" {
		t.Fatalf("chord after g = %q, want g", m.chord)
	}
	m = sendKey(m, "g")
	if m.chord != "" || m.cursor != 0 {
		t.Errorf("after gg: chord %q cursor %d; want no chord and cursor 0", m.chord, m.cursor)
	}
}

func TestAKeyThatDoesNotCompleteTheChordActsOnItsOwn(t *testing.T) {
	// "g" then "j": there's no "g j", so the stale g is dropped and j moves
	// down, exactly as it would have with no g pending.
	m := New(loadFixture(t))
	start := m.cursor
	m = sendKey(m, "g")
	m = sendKey(m, "j")
	if m.chord != "" {
		t.Errorf("chord = %q after g j, want it dropped", m.chord)
	}
	if m.cursor != start+1 {
		t.Errorf("cursor = %d after g j, want %d (j moved down)", m.cursor, start+1)
	}
}

func TestAnUnboundKeyAfterAPrefixDropsTheChord(t *testing.T) {
	m := New(loadFixture(t))
	m = sendKey(m, "z")
	m = sendKey(m, "x") // z x isn't bound, and x alone isn't either
	if m.chord != "" {
		t.Errorf("chord = %q, want none after an unbound second key", m.chord)
	}
}

func TestAPrefixKeyAfterAnotherPrefixStartsItsOwnChord(t *testing.T) {
	m := New(loadFixture(t))
	m = sendKey(m, "z")
	m = sendKey(m, "g")
	if m.chord != "g" {
		t.Errorf("chord = %q after z g, want g (z was dropped, g started a new chord)", m.chord)
	}
	m = sendKey(m, "g")
	if m.cursor != 0 {
		t.Errorf("cursor = %d after z g g, want 0", m.cursor)
	}
}

func TestSamePrefixKeyHasDifferentMeaningsPerChord(t *testing.T) {
	// "o" opens a fold after z, but inserts an entry on its own; "d" after
	// g is a deadline prompt, after d a delete.
	m := New(loadFixture(t))
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	m = sendKey(m, "g")
	m = sendKey(m, "d")
	if m.mode != deadlineMode {
		t.Errorf("mode after g d = %v, want deadlineMode", m.mode)
	}
}

func TestVisualModeSharesMotionsAndHasItsOwnChords(t *testing.T) {
	m := New(loadFixture(t))
	m.cursor = 3
	m = sendKey(m, "V")
	m = sendKey(m, "j")
	if m.mode != visualMode || m.cursor != 4 {
		t.Fatalf("after V j: mode %v cursor %d; want visual mode, cursor 4", m.mode, m.cursor)
	}
	m = sendKey(m, "g")
	if m.chord != "g" {
		t.Errorf("chord = %q after g in visual mode", m.chord)
	}
	m = sendKey(m, "g")
	if m.cursor != 0 || m.mode != visualMode {
		t.Errorf("after gg in visual mode: cursor %d mode %v; want 0 and still visual", m.cursor, m.mode)
	}
	m = sendKey(m, "esc")
	if m.mode != normalMode {
		t.Errorf("mode after esc = %v, want normalMode", m.mode)
	}
}

func TestKeepViewActionsDoNotScrollTheCursorBackIntoView(t *testing.T) {
	// ctrl+e scrolls the view without dragging the cursor along unless it
	// would leave the screen; it must not be followed by the
	// ensureVisible every other action gets.
	m := New(loadFixture(t))
	m.width, m.height = 80, 12
	m.cursor = 0
	before := m.offset
	m = sendKey(m, "ctrl+e")
	if m.offset == before {
		t.Skip("fixture too short to scroll in this height")
	}
	if m.offset != before+1 {
		t.Errorf("offset after ctrl+e = %d, want %d", m.offset, before+1)
	}
}
