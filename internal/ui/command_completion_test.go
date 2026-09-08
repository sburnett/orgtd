package ui

import (
	"strings"
	"testing"
)

func TestTabCompletesUniquePrefix(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m = sendKey(m, ":")
	m = typeKeys(m, "a")

	m = sendKey(m, "tab")

	if m.commandInput != "agenda" {
		t.Errorf("commandInput = %q, want %q", m.commandInput, "agenda")
	}
	if m.mode != commandMode {
		t.Errorf("mode = %v, want commandMode (tab shouldn't run the command)", m.mode)
	}
}

func TestTabThenEnterRunsTheCompletedCommand(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m = sendKey(m, ":")
	m = typeKeys(m, "a")
	m = sendKey(m, "tab")
	m, _ = sendKeyCmd(m, "enter")

	if m.view != agendaView {
		t.Fatalf("view after :a<Tab><Enter> = %v, want agendaView", m.view)
	}
}

func TestTabWithAmbiguousPrefixListsMatchesAndExtendsCommonPrefix(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m = sendKey(m, ":")
	m = typeKeys(m, "w")

	m = sendKey(m, "tab")

	// "w", "wq", "write" all start with "w"; common prefix is just "w",
	// so the input shouldn't change, but all three should be listed.
	if m.commandInput != "w" {
		t.Errorf("commandInput = %q, want unchanged %q (no longer common prefix)", m.commandInput, "w")
	}
	for _, want := range []string{"w", "wq", "write"} {
		if !strings.Contains(m.commandCompletions, want) {
			t.Errorf("commandCompletions = %q, missing %q", m.commandCompletions, want)
		}
	}
}

func TestTabExtendsToLongestCommonPrefix(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m = sendKey(m, ":")
	m = typeKeys(m, "q")

	m = sendKey(m, "tab")

	// "q" and "q!" both start with "q", but so would nothing else — the
	// common prefix of the matches ("q", "q!", "quit", "quit!") is just
	// "q" itself, so input stays "q" and all four are listed.
	if m.commandInput != "q" {
		t.Errorf("commandInput = %q, want %q", m.commandInput, "q")
	}
	for _, want := range []string{"q", "q!", "quit", "quit!"} {
		if !strings.Contains(m.commandCompletions, want) {
			t.Errorf("commandCompletions = %q, missing %q", m.commandCompletions, want)
		}
	}
}

func TestTabNoMatchesShowsMessage(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m = sendKey(m, ":")
	m = typeKeys(m, "zzz")

	m = sendKey(m, "tab")

	if m.message == "" {
		t.Errorf("expected a message when no command matches the typed prefix")
	}
	if m.commandInput != "zzz" {
		t.Errorf("commandInput = %q, want unchanged %q", m.commandInput, "zzz")
	}
}

// TestTabNoMatchesErrorIsActuallyVisible guards against the same class
// of bug as TestDeadlineInvalidInputErrorIsActuallyVisible: m.message
// was set correctly (per TestTabNoMatchesShowsMessage above), but the
// status bar's rendering switch checked "mode == commandMode" before
// "message != empty", so it was silently never drawn while still in
// commandMode.
func TestTabNoMatchesErrorIsActuallyVisible(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.width, m.height = 120, 30
	m = sendKey(m, ":")
	m = typeKeys(m, "zzz")
	m = sendKey(m, "tab")

	if m.message == "" {
		t.Fatalf("fixture assumption broken: expected m.message to be set")
	}
	lines := strings.Split(m.View(), "\n")
	last := lines[len(lines)-1]
	if !strings.Contains(last, m.message) {
		t.Errorf("status line = %q, missing the error message %q", last, m.message)
	}
}

func TestTabNoMatchesErrorClearedByNextKeystroke(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m = sendKey(m, ":")
	m = typeKeys(m, "zzz")
	m = sendKey(m, "tab")
	if m.message == "" {
		t.Fatalf("fixture assumption broken: expected m.message to be set")
	}

	m = typeKeys(m, "q")
	if m.message != "" {
		t.Errorf("m.message = %q, want cleared after typing another key", m.message)
	}
}

func TestTabNoopOnceAnArgumentIsBeingTyped(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m = sendKey(m, ":")
	m = typeKeys(m, "delmarks a")

	m = sendKey(m, "tab")

	if m.commandInput != "delmarks a" {
		t.Errorf("commandInput = %q, want unchanged %q (no completion past the command word)", m.commandInput, "delmarks a")
	}
}

func TestCompletionListClearedByNextKeystroke(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m = sendKey(m, ":")
	m = typeKeys(m, "w")
	m = sendKey(m, "tab")
	if m.commandCompletions == "" {
		t.Fatalf("fixture assumption broken: expected a completion list after tab")
	}

	m = typeKeys(m, "q")

	if m.commandCompletions != "" {
		t.Errorf("commandCompletions = %q, want cleared after typing another key", m.commandCompletions)
	}
}

func TestCommonPrefixHelper(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{[]string{"agenda"}, "agenda"},
		{[]string{"w", "wq", "write"}, "w"},
		{[]string{"quit", "quit!"}, "quit"},
		{[]string{"outline", "outline"}, "outline"},
	}
	for _, c := range cases {
		if got := commonPrefix(c.in); got != c.want {
			t.Errorf("commonPrefix(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}
