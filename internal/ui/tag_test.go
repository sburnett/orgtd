package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// The fixture's tagged entry: "** WAITING Get feedback on the keybinding
// scheme   :feedback:". "Implement the Bubble Tea viewer" has no tags.
const (
	taggedTitle   = "Get feedback on the keybinding scheme"
	untaggedTitle = "Implement the Bubble Tea viewer"
)

// gtOn presses "gt" on the entry titled title.
func gtOn(t *testing.T, m Model, title string) Model {
	t.Helper()
	m.cursor = findRow(t, m, title)
	m = sendKey(m, "g")
	return sendKey(m, "t")
}

func TestGtStartsOnANewTagAtTheEnd(t *testing.T) {
	m := gtOn(t, New(loadFixture(t)), taggedTitle)
	if m.mode != insertMode || !m.ins.inTags {
		t.Fatalf("mode %v, inTags %v; want insert mode in the tag area", m.mode, m.ins.inTags)
	}
	if got := m.ins.tags; len(got) != 2 || got[0] != "feedback" || got[1] != "" {
		t.Errorf("tags = %q, want the entry's tag plus a new empty one", got)
	}
	if m.ins.tagIdx != 1 {
		t.Errorf("caret on tag %d, want the new one (1)", m.ins.tagIdx)
	}
	if got := m.ins.buf.lines[0]; got != "WAITING "+taggedTitle {
		t.Errorf("first line while editing tags = %q, want just the title", got)
	}
}

func TestGtIsTwoKeyChordNotSingleG(t *testing.T) {
	m := New(loadFixture(t))
	m.cursor = findRow(t, m, untaggedTitle)
	m = sendKey(m, "g")
	if m.mode != normalMode || m.chord != "g" {
		t.Errorf("single g: mode %v, chord %q; want normal mode, waiting for a second key", m.mode, m.chord)
	}
}

func TestGtTypesANewTag(t *testing.T) {
	m := New(loadFixture(t))
	undoBefore := m.history.pos
	m = gtOn(t, m, untaggedTitle)
	m = typeKeys(m, "urgent")
	if got := m.ins.tags; len(got) != 2 || got[0] != "urgent" || got[1] != "" {
		t.Errorf("tags while typing = %q, want the typed one and a fresh empty one after it", got)
	}
	m = sendKey(m, "esc")
	if got := m.currentHeadline().Tags; len(got) != 1 || got[0] != "urgent" {
		t.Errorf("Tags = %v, want [urgent]", got)
	}
	if m.history.pos != undoBefore+1 {
		t.Errorf("undo steps = %d, want 1", m.history.pos-undoBefore)
	}
	m = sendKey(m, "u")
	if got := m.currentHeadline().Tags; len(got) != 0 {
		t.Errorf("after u, Tags = %v, want none", got)
	}
}

func TestGtAddsAfterExistingTags(t *testing.T) {
	m := gtOn(t, New(loadFixture(t)), taggedTitle)
	m = typeKeys(m, "urgent")
	m = sendKey(m, "esc")
	if got := m.currentHeadline().Tags; len(got) != 2 || got[0] != "feedback" || got[1] != "urgent" {
		t.Errorf("Tags = %v, want [feedback urgent]", got)
	}
}

func TestGtThenEscWithoutTypingChangesNothing(t *testing.T) {
	m := New(loadFixture(t))
	undoBefore := m.history.pos
	m = gtOn(t, m, taggedTitle)
	m = sendKey(m, "esc")
	if m.history.pos != undoBefore || len(m.dirty) != 0 {
		t.Errorf("undo steps %d, dirty %v; want nothing recorded", m.history.pos-undoBefore, m.dirty)
	}
}

func TestTagsRejectSpaces(t *testing.T) {
	m := gtOn(t, New(loadFixture(t)), untaggedTitle)
	m = typeKeys(m, "a b")
	if got := m.ins.tags[0]; got != "ab" {
		t.Errorf("tag = %q, want %q (' ' can't be typed)", got, "ab")
	}
}

func TestColonMovesToTheNextTag(t *testing.T) {
	m := gtOn(t, New(loadFixture(t)), untaggedTitle)
	m = typeKeys(m, "work:home")
	if got := m.ins.tags; len(got) != 3 || got[0] != "work" || got[1] != "home" || got[2] != "" || m.ins.tagIdx != 1 {
		t.Errorf("after \"work:home\": tags %q, caret %d; want [work home \"\"], on home", got, m.ins.tagIdx)
	}
	m = typeKeys(m, "::") // to the new tag, then nowhere further
	if m.ins.tagIdx != 2 || len(m.ins.tags) != 3 {
		t.Errorf("\":\" on the new tag: tags %q, caret %d; want it to stay put", m.ins.tags, m.ins.tagIdx)
	}

	m = sendCtrl(m, tea.KeyHome)
	m = sendCtrl(m, tea.KeyEnd)
	m = sendCtrl(m, tea.KeyRight) // onto "work"
	m = typeKeys(m, ":")
	if m.ins.tagIdx != 1 || m.ins.tags[0] != "work" {
		t.Errorf("\":\" on the first tag: caret %d, tags %q; want on to the second, unchanged", m.ins.tagIdx, m.ins.tags)
	}

	// A paste is text: a ":" in it is just dropped.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x:y"), Paste: true})
	m = updated.(Model)
	if got := m.ins.tags[1]; got != "homexy" {
		t.Errorf("pasting \"x:y\" = %q, want %q", got, "homexy")
	}
}

func TestBackspaceDeletesFromTheTagThenTheOneBefore(t *testing.T) {
	m := gtOn(t, New(loadFixture(t)), taggedTitle)
	m = sendKey(m, "backspace") // on the empty new tag: back to "feedback"
	if m.ins.tagIdx != 0 {
		t.Fatalf("backspace on the empty new tag: caret on %d, want the tag before", m.ins.tagIdx)
	}
	m = sendKey(m, "backspace")
	if got := m.ins.tags[0]; got != "feedbac" {
		t.Errorf("backspace on a tag = %q, want its last character gone", got)
	}
	for range len("feedbac") {
		m = sendKey(m, "backspace")
	}
	m = sendKey(m, "backspace") // empty first tag: out of the tag area
	if m.ins.inTags {
		t.Fatalf("still in the tag area after emptying the first tag")
	}
	if got := m.ins.buf.lines[0]; got != "WAITING "+taggedTitle {
		t.Errorf("first line = %q, want the tag gone", got)
	}
	m = sendKey(m, "esc")
	if got := m.currentHeadline().Tags; len(got) != 0 {
		t.Errorf("Tags = %v, want none", got)
	}
}

func TestTypingAddsToTheTagUnderTheCaret(t *testing.T) {
	m := gtOn(t, New(loadFixture(t)), taggedTitle)
	m = sendCtrl(m, tea.KeyLeft)
	m = typeKeys(m, "2")
	m = sendKey(m, "esc")
	if got := m.currentHeadline().Tags; len(got) != 1 || got[0] != "feedback2" {
		t.Errorf("Tags = %v, want [feedback2]", got)
	}
}

func TestArrowsMoveOneTagAtATime(t *testing.T) {
	m := sendKey(vetEntry(t), "i")
	m.ins.buf.lines[0] = "TODO Call the vet   :a:b:"
	m.ins.buf.col = m.insertTitleEnd()
	m = sendCtrl(m, tea.KeyRight)
	if !m.ins.inTags || m.ins.tagIdx != 0 {
		t.Fatalf("→ at the title's end: inTags %v, tag %d; want on the first tag", m.ins.inTags, m.ins.tagIdx)
	}
	m = sendCtrl(m, tea.KeyRight)
	m = sendCtrl(m, tea.KeyRight)
	m = sendCtrl(m, tea.KeyRight)
	if m.ins.tagIdx != 2 {
		t.Errorf("→ past the last tag: on %d, want the new empty one (2), no further", m.ins.tagIdx)
	}
	for range 3 {
		m = sendCtrl(m, tea.KeyLeft)
	}
	if m.ins.inTags || m.ins.buf.col != len("TODO Call the vet") {
		t.Errorf("← off the first tag: inTags %v col %d; want the title's end", m.ins.inTags, m.ins.buf.col)
	}
	if got := m.ins.buf.lines[0]; got != "TODO Call the vet   :a:b:" {
		t.Errorf("first line after leaving = %q, want the tags written back", got)
	}
}

func TestDeleteRemovesTheTagUnderTheCaret(t *testing.T) {
	m := sendKey(vetEntry(t), "i")
	m.ins.buf.lines[0] = "TODO Call the vet   :a:b:"
	m.ins.buf.col = m.insertTitleEnd()
	m = sendCtrl(m, tea.KeyRight)
	m = sendCtrl(m, tea.KeyDelete)
	if got := m.ins.tags; len(got) != 2 || got[0] != "b" || m.ins.tagIdx != 0 {
		t.Errorf("Delete on the first tag: tags %q, caret %d; want [b \"\"], on b", got, m.ins.tagIdx)
	}
}

func TestEnterStartsANewTagAndEmptiedTagsAreDropped(t *testing.T) {
	m := gtOn(t, New(loadFixture(t)), taggedTitle)
	m = typeKeys(m, "one")
	m = sendKey(m, "enter")
	m = typeKeys(m, "two")
	m = sendCtrl(m, tea.KeyLeft)
	m = sendCtrl(m, tea.KeyCtrlU) // empties "one"
	m = sendCtrl(m, tea.KeyLeft)  // and leaving it drops it
	if got := m.ins.tags; len(got) != 3 || got[0] != "feedback" || got[1] != "two" || m.ins.tagIdx != 0 {
		t.Errorf("tags %q, caret %d; want [feedback two \"\"], on feedback", got, m.ins.tagIdx)
	}
}

func TestRepeatedTagsAreWrittenBackOnce(t *testing.T) {
	m := gtOn(t, New(loadFixture(t)), taggedTitle)
	m = typeKeys(m, "feedback")
	m = sendKey(m, "esc")
	if got := m.currentHeadline().Tags; len(got) != 1 || got[0] != "feedback" {
		t.Errorf("Tags = %v, want [feedback] once", got)
	}
}

func TestTabCompletesATag(t *testing.T) {
	m := gtOn(t, New(loadFixture(t)), untaggedTitle)
	m = typeKeys(m, "fe")
	m = sendKey(m, "tab")
	if got := m.ins.tags[0]; got != "feedback" {
		t.Errorf("Tab on \"fe\" = %q, want feedback", got)
	}
}

func TestTabOnAnAmbiguousPrefixAndTheTagsList(t *testing.T) {
	m := New(loadFixture(t))
	m.width, m.height = 80, 40
	m.cursor = findRow(t, m, "Learn Go generics")
	m.currentHeadline().Tags = []string{"features"} // shares "fe" with feedback
	m = gtOn(t, m, untaggedTitle)
	m = typeKeys(m, "fe")
	m = sendKey(m, "tab")
	if got := m.ins.tags[0]; got != "fe" {
		t.Errorf("Tab on an ambiguous prefix = %q, want it unchanged", got)
	}
	info := stripANSI(strings.Join(m.infoBufferLines(), "\n"))
	if !strings.Contains(info, "Tags:") || !strings.Contains(info, "feedback") || !strings.Contains(info, "features") {
		t.Errorf("info buffer = %q, want a Tags: list with both matches", info)
	}
	m = typeKeys(m, "z")
	m = sendKey(m, "tab")
	if !strings.Contains(m.message, "No existing tag") {
		t.Errorf("Tab with no match: message %q", m.message)
	}
}

func TestCapitalAAndEndStopBeforeTheTags(t *testing.T) {
	m := New(loadFixture(t))
	m.cursor = findRow(t, m, taggedTitle)
	m = sendKey(m, "A")
	if m.ins.inTags || m.ins.buf.col != len("WAITING "+taggedTitle) {
		t.Fatalf("A: inTags %v col %d; want the end of the title", m.ins.inTags, m.ins.buf.col)
	}
	m = sendCtrl(m, tea.KeyHome)
	m = sendCtrl(m, tea.KeyEnd)
	if m.ins.inTags || m.ins.buf.col != len("WAITING "+taggedTitle) {
		t.Fatalf("End: inTags %v col %d; want the end of the title", m.ins.inTags, m.ins.buf.col)
	}
	m = sendCtrl(m, tea.KeyEnd)
	if !m.ins.inTags || m.ins.tagIdx != 1 {
		t.Errorf("End again: inTags %v tag %d; want on the new tag", m.ins.inTags, m.ins.tagIdx)
	}
}

func TestDeletionsAtTheTitleEndLeaveTheTags(t *testing.T) {
	for _, k := range []tea.KeyType{tea.KeyDelete, tea.KeyCtrlK} {
		m := New(loadFixture(t))
		m.cursor = findRow(t, m, taggedTitle)
		m = sendKey(m, "A")
		m = sendCtrl(m, k)
		if got := m.ins.buf.lines[0]; got != "WAITING "+taggedTitle+"   :feedback:" {
			t.Errorf("%v at the title's end: %q, want the tags untouched", k, got)
		}
	}
}

func TestUpFromTheBodyStopsBeforeTheTags(t *testing.T) {
	m := New(loadFixture(t))
	m.cursor = findRow(t, m, taggedTitle)
	m = sendKey(m, "A")
	m.ins.buf.lines = append(m.ins.buf.lines, strings.Repeat("x", 80))
	m.ins.buf.row, m.ins.buf.col, m.ins.buf.goal = len(m.ins.buf.lines)-1, 80, -1
	for m.ins.buf.row > 0 {
		m = sendKey(m, "up")
	}
	if m.ins.inTags || m.ins.buf.col != len("WAITING "+taggedTitle) {
		t.Errorf("↑ onto the first line: inTags %v col %d; want the title's end", m.ins.inTags, m.ins.buf.col)
	}
}

func TestTheTagAreaIsDrawnWithTheCaretOnOneTag(t *testing.T) {
	m := gtOn(t, New(loadFixture(t)), taggedTitle)
	lines, _ := m.insertEditorLines("", "", lipgloss.NoColor{})
	if got := stripANSI(lines[0]); !strings.HasPrefix(got, "WAITING "+taggedTitle+"   :feedback: :") {
		t.Errorf("first line = %q, want the tags with a blank new one at the end", got)
	}
	if !strings.Contains(lines[0], m.caretStyle().Render(" ")) {
		t.Errorf("first line %q has no caret on the new tag", lines[0])
	}
	m = sendCtrl(m, tea.KeyLeft)
	lines, _ = m.insertEditorLines("", "", lipgloss.NoColor{})
	if !strings.Contains(lines[0], m.caretStyle().Render("feedback")) {
		t.Errorf("first line %q doesn't draw the whole tag as the caret", lines[0])
	}
	if !strings.Contains(stripANSI(m.View()), "tags: type to edit") {
		t.Errorf("no tags hint on the command line")
	}
}

func TestGtOnLockedEntryRefuses(t *testing.T) {
	m := New(loadFixture(t))
	m.cursor = findRow(t, m, untaggedTitle)
	m.immutable[m.currentHeadline()] = true
	m = sendKey(m, "g")
	m = sendKey(m, "t")
	if m.mode != normalMode {
		t.Errorf("gt on a locked entry: mode %v, want normal mode", m.mode)
	}
}

func TestEmptyingTheLastTagLeavesOneNewTag(t *testing.T) {
	m := gtOn(t, New(loadFixture(t)), taggedTitle)
	m = sendCtrl(m, tea.KeyLeft) // onto "feedback"
	for range len("feedback") {
		m = sendKey(m, "backspace")
	}
	if got := m.ins.tags; len(got) != 1 || got[0] != "" || m.ins.tagIdx != 0 {
		t.Errorf("tags %q, caret %d; want just the one empty tag, under the caret", got, m.ins.tagIdx)
	}
	lines, _ := m.insertEditorLines("", "", lipgloss.NoColor{})
	if got := strings.TrimRight(stripANSI(lines[0]), " "); !strings.HasSuffix(got, "   : :") {
		t.Errorf("first line = %q, want it to end \"   : :\"", got)
	}
	m = typeKeys(m, "x")
	if got := m.ins.tags; len(got) != 2 || got[0] != "x" || got[1] != "" {
		t.Errorf("typing again: tags %q, want [x \"\"]", got)
	}

	// The same for a tag emptied with ctrl-u.
	m = sendCtrl(m, tea.KeyCtrlU)
	if got := m.ins.tags; len(got) != 1 || got[0] != "" {
		t.Errorf("ctrl-u on the last tag: tags %q, want just the one empty tag", got)
	}
}
