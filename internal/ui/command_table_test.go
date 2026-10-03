package ui

import (
	"strings"
	"testing"
)

func TestCommandTableEntriesAreWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range commandTable {
		if len(c.names) == 0 || c.run == nil {
			t.Errorf("command %+v has no names or no run function", c)
		}
		for _, n := range c.names {
			if seen[n] {
				t.Errorf("command name %q is listed twice", n)
			}
			seen[n] = true
			if _, v := viewForCommand(n); v != nil {
				t.Errorf("command name %q is also a view's command", n)
			}
		}
	}
}

func TestEveryTableCommandAndAliasIsRecognized(t *testing.T) {
	for _, c := range commandTable {
		for _, n := range c.names {
			if got, _ := lookupCommand(n); got == nil {
				t.Errorf("lookupCommand(%q) = nil", n)
			}
		}
	}
	if c, _ := lookupCommand("write"); c == nil || c.names[0] != "w" {
		t.Errorf("an alias should find the same command as its short form")
	}
}

func TestOnlyArgumentTakingCommandsAcceptArguments(t *testing.T) {
	if c, arg := lookupCommand("delmarks ab"); c == nil || arg != "ab" {
		t.Errorf("lookupCommand(\"delmarks ab\") = %v, %q; want delmarks with arg ab", c, arg)
	}
	if c, arg := lookupCommand("delmarks   a b  "); c == nil || arg != "a b" {
		t.Errorf("lookupCommand with spaces = %v, %q; want the trimmed argument \"a b\"", c, arg)
	}
	if c, _ := lookupCommand("w foo"); c != nil {
		t.Error("\":w foo\" must be an unknown command, not \":w\" with an ignored argument")
	}
	if c, _ := lookupCommand("nosuch"); c != nil {
		t.Error("lookupCommand(nosuch) found something")
	}
}

func TestUnknownCommandSetsAMessage(t *testing.T) {
	m := New(loadFixture(t))
	m = typeKeys(m, ":bogus")
	m = sendKey(m, "enter")
	if !strings.Contains(m.message, "Unknown command: bogus") {
		t.Errorf("message = %q, want an unknown-command message", m.message)
	}
	m = typeKeys(m, ":w foo")
	m = sendKey(m, "enter")
	if !strings.Contains(m.message, "Unknown command: w foo") {
		t.Errorf("message = %q, want \":w foo\" rejected", m.message)
	}
}

func TestDelmarksCommandVariants(t *testing.T) {
	m := New(loadFixture(t))
	m.cursor = 1
	m = typeKeys(m, "ma")
	m = typeKeys(m, ":delmarks")
	m = sendKey(m, "enter")
	if !strings.Contains(m.message, "Usage: :delmarks") || len(m.marks) != 1 {
		t.Errorf("bare :delmarks: message %q, marks %d; want a usage hint and the mark kept", m.message, len(m.marks))
	}
	m = typeKeys(m, ":delmarks a")
	m = sendKey(m, "enter")
	if len(m.marks) != 0 {
		t.Errorf("marks after :delmarks a = %v, want none", m.marks)
	}
}

func TestEveryCommandIsTabCompletable(t *testing.T) {
	names := " " + strings.Join(commandNames(), " ") + " "
	for _, c := range commandTable {
		for _, n := range c.names {
			if !strings.Contains(names, " "+n+" ") {
				t.Errorf("commandNames() lacks %q", n)
			}
		}
	}
}

func TestExecutingTheCommandReachesTheCaller(t *testing.T) {
	// :q on a clean workspace must come back out of Update as tea.Quit —
	// the command's tea.Cmd, not just its side effects, has to survive the
	// dispatch.
	m := New(loadFixture(t))
	m = typeKeys(m, ":q")
	_, cmd := m.Update(keyMsgFor("enter"))
	if cmd == nil {
		t.Fatal(":q returned no command")
	}
	if msg := cmd(); msg == nil {
		t.Error(":q's command produced no message (want tea.QuitMsg)")
	}
}
