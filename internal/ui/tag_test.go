package ui

import (
	"strings"
	"testing"
)

func TestGtOpensTagPromptBlank(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Implement the Bubble Tea viewer")

	m = sendKey(m, "g")
	m = sendKey(m, "t")

	if m.mode != tagMode {
		t.Fatalf("mode = %v, want tagMode", m.mode)
	}
	if m.tagInput != "" {
		t.Errorf("tagInput = %q, want empty", m.tagInput)
	}
}

func TestGtIsTwoKeyChordNotSingleG(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Implement the Bubble Tea viewer")

	m = sendKey(m, "g")
	if m.mode == tagMode {
		t.Fatalf("single g opened tag mode")
	}
	if !m.pendingG {
		t.Errorf("expected pendingG after a single g")
	}
}

func TestGtAddsNewTag(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Implement the Bubble Tea viewer")
	h := m.currentHeadline()
	if len(h.Tags) != 0 {
		t.Fatalf("fixture assumption broken: expected no tags, got %v", h.Tags)
	}

	m = sendKey(m, "g")
	m = sendKey(m, "t")
	m = typeKeys(m, "urgent")
	m = sendKey(m, "enter")

	if m.mode != normalMode {
		t.Fatalf("mode = %v, want normalMode", m.mode)
	}
	h = m.currentHeadline()
	if got := h.Tags; len(got) != 1 || got[0] != "urgent" {
		t.Errorf("Tags = %v, want [urgent]", got)
	}
}

func TestGtTypingSameTagAgainRemovesIt(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Get feedback on the keybinding scheme")
	h := m.currentHeadline()
	if got := h.Tags; len(got) != 1 || got[0] != "feedback" {
		t.Fatalf("fixture assumption broken: Tags = %v, want [feedback]", got)
	}

	m = sendKey(m, "g")
	m = sendKey(m, "t")
	m = typeKeys(m, "feedback")
	m = sendKey(m, "enter")

	h = m.currentHeadline()
	if len(h.Tags) != 0 {
		t.Errorf("Tags = %v, want empty (tag removed by retyping it)", h.Tags)
	}
}

func TestGtAddingSecondTagKeepsFirst(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Get feedback on the keybinding scheme")

	m = sendKey(m, "g")
	m = sendKey(m, "t")
	m = typeKeys(m, "urgent")
	m = sendKey(m, "enter")

	h := m.currentHeadline()
	if got := h.Tags; len(got) != 2 || got[0] != "feedback" || got[1] != "urgent" {
		t.Errorf("Tags = %v, want [feedback urgent]", got)
	}
}

func TestGtEmptyInputCancelsWithoutChanges(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Get feedback on the keybinding scheme")
	before := append([]string(nil), m.currentHeadline().Tags...)

	m = sendKey(m, "g")
	m = sendKey(m, "t")
	m = sendKey(m, "enter")

	if m.mode != normalMode {
		t.Fatalf("mode = %v, want normalMode", m.mode)
	}
	h := m.currentHeadline()
	if strings.Join(h.Tags, ",") != strings.Join(before, ",") {
		t.Errorf("Tags = %v, want unchanged %v", h.Tags, before)
	}
}

func TestGtEscCancelsWithoutChanges(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Implement the Bubble Tea viewer")

	m = sendKey(m, "g")
	m = sendKey(m, "t")
	m = typeKeys(m, "urgent")
	m = sendKey(m, "esc")

	if m.mode != normalMode {
		t.Fatalf("mode = %v, want normalMode", m.mode)
	}
	if len(m.currentHeadline().Tags) != 0 {
		t.Errorf("Tags = %v, want empty (Esc should not apply)", m.currentHeadline().Tags)
	}
}

func TestGtTabCompletesUniquePrefix(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Implement the Bubble Tea viewer")

	// "feedback" and "work" already exist in the fixture (see
	// projects.org); "fe" is a prefix of only "feedback".
	m = sendKey(m, "g")
	m = sendKey(m, "t")
	m = typeKeys(m, "fe")
	m = sendKey(m, "tab")

	if m.tagInput != "feedback" {
		t.Errorf("tagInput = %q, want %q", m.tagInput, "feedback")
	}
}

func TestGtTabListsAmbiguousMatches(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Implement the Bubble Tea viewer")
	// Seed a second tag sharing a prefix with "feedback" so completion is
	// genuinely ambiguous, rather than relying on incidental fixture data.
	other := m.currentHeadline()
	other.Tags = []string{"features"}

	m = sendKey(m, "g")
	m = sendKey(m, "t")
	m = typeKeys(m, "fe")
	m = sendKey(m, "tab")

	if m.tagInput != "fe" {
		t.Errorf("tagInput = %q, want unchanged %q (ambiguous)", m.tagInput, "fe")
	}
	if !strings.Contains(m.tagCompletions, "feedback") || !strings.Contains(m.tagCompletions, "features") {
		t.Errorf("tagCompletions = %q, want both feedback and features listed", m.tagCompletions)
	}
}

// TestGtTabAmbiguousMatchesShowInInfoBufferNotOnPromptLine guards the
// "Tags:" section of the info buffer (see infoBufferLines): the
// completion matches themselves live there, one per line, above the
// status line — the "gt" prompt's own command line only ever shows the
// typed input and the caret.
func TestGtTabAmbiguousMatchesShowInInfoBufferNotOnPromptLine(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.width, m.height = 100, 30
	m.cursor = findRow(t, m, "Implement the Bubble Tea viewer")
	other := m.currentHeadline()
	other.Tags = []string{"features"}

	m = sendKey(m, "g")
	m = sendKey(m, "t")
	m = typeKeys(m, "fe")
	m = sendKey(m, "tab")

	lines := strings.Split(m.View(), "\n")
	last := stripANSI(lines[len(lines)-1])
	if strings.Contains(last, "feedback") || strings.Contains(last, "features") {
		t.Errorf("command line = %q, completions should not appear here anymore", last)
	}

	var bufLines []string
	for _, l := range m.infoBufferLines() {
		bufLines = append(bufLines, stripANSI(l))
	}
	if !containsSubstring(bufLines, "Tags:") {
		t.Errorf("info buffer missing \"Tags:\" section: %#v", bufLines)
	}
	if !containsSubstring(bufLines, "feedback") || !containsSubstring(bufLines, "features") {
		t.Errorf("info buffer missing completion matches: %#v", bufLines)
	}
}

func TestGtTabNoMatchShowsMessage(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Implement the Bubble Tea viewer")

	m = sendKey(m, "g")
	m = sendKey(m, "t")
	m = typeKeys(m, "zzz")
	m = sendKey(m, "tab")

	if m.message == "" {
		t.Errorf("expected a message about no matching tag")
	}
}

func TestGtIgnoresSpaces(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Implement the Bubble Tea viewer")

	m = sendKey(m, "g")
	m = sendKey(m, "t")
	m = typeKeys(m, "no")
	m = sendKey(m, " ")
	m = typeKeys(m, "spaces")

	if m.tagInput != "nospaces" {
		t.Errorf("tagInput = %q, want %q (space dropped)", m.tagInput, "nospaces")
	}
}

func TestGtUndoRedo(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Implement the Bubble Tea viewer")

	m = sendKey(m, "g")
	m = sendKey(m, "t")
	m = typeKeys(m, "urgent")
	m = sendKey(m, "enter")

	if got := m.currentHeadline().Tags; len(got) != 1 || got[0] != "urgent" {
		t.Fatalf("Tags = %v, want [urgent]", got)
	}

	m = sendKey(m, "u")
	if got := m.currentHeadline().Tags; len(got) != 0 {
		t.Errorf("after undo, Tags = %v, want empty", got)
	}

	m = sendKey(m, "ctrl+r")
	if got := m.currentHeadline().Tags; len(got) != 1 || got[0] != "urgent" {
		t.Errorf("after redo, Tags = %v, want [urgent]", got)
	}
}

func TestGtOnLockedEntryRefuses(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.cursor = findRow(t, m, "Implement the Bubble Tea viewer")
	h := m.currentHeadline()
	m.immutable[h] = true

	m = sendKey(m, "g")
	m = sendKey(m, "t")

	if m.mode == tagMode {
		t.Errorf("expected gt to refuse on a locked entry")
	}
}
