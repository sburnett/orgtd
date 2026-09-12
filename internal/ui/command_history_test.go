package ui

import "testing"

// runAndReturn types cmd into the command line and presses Enter,
// returning to normal mode.
func runAndReturn(m Model, cmd string) Model {
	m = sendKey(m, ":")
	m = typeKeys(m, cmd)
	return sendKey(m, "enter")
}

func TestCommandHistoryUpRecallsMostRecentCommand(t *testing.T) {
	ws := agendaFixture(t, "* TODO Something\n")
	m := New(ws)
	m = runAndReturn(m, "config")
	m = runAndReturn(m, "log")

	m = sendKey(m, ":")
	m = sendKey(m, "up")

	if m.commandInput != "log" {
		t.Errorf("commandInput after one ↑ = %q, want %q (the most recent command)", m.commandInput, "log")
	}
}

func TestCommandHistoryUpWalksBackThroughOlderCommands(t *testing.T) {
	ws := agendaFixture(t, "* TODO Something\n")
	m := New(ws)
	m = runAndReturn(m, "config")
	m = runAndReturn(m, "log")
	m = runAndReturn(m, "diff")

	m = sendKey(m, ":")
	m = sendKey(m, "up")
	m = sendKey(m, "up")
	m = sendKey(m, "up")

	if m.commandInput != "config" {
		t.Errorf("commandInput after three ↑ = %q, want %q (the oldest command)", m.commandInput, "config")
	}
}

func TestCommandHistoryUpStopsAtTheOldestEntry(t *testing.T) {
	ws := agendaFixture(t, "* TODO Something\n")
	m := New(ws)
	m = runAndReturn(m, "config")

	m = sendKey(m, ":")
	m = sendKey(m, "up")
	m = sendKey(m, "up") // one more than there is history for
	m = sendKey(m, "up")

	if m.commandInput != "config" {
		t.Errorf("commandInput after over-shooting ↑ = %q, want it clamped at %q", m.commandInput, "config")
	}
}

func TestCommandHistoryDownReturnsTowardTheDraft(t *testing.T) {
	ws := agendaFixture(t, "* TODO Something\n")
	m := New(ws)
	m = runAndReturn(m, "config")
	m = runAndReturn(m, "log")

	m = sendKey(m, ":")
	m = typeKeys(m, "unsent")
	m = sendKey(m, "up") // saves "unsent" as the draft, shows "log"
	m = sendKey(m, "up") // shows "config"
	m = sendKey(m, "down")
	m = sendKey(m, "down")

	if m.commandInput != "unsent" {
		t.Errorf("commandInput after ↑↑↓↓ = %q, want the original draft %q restored", m.commandInput, "unsent")
	}
}

func TestCommandHistoryDownStopsAtTheDraft(t *testing.T) {
	ws := agendaFixture(t, "* TODO Something\n")
	m := New(ws)
	m = runAndReturn(m, "config")

	m = sendKey(m, ":")
	m = sendKey(m, "up")
	m = sendKey(m, "down")
	m = sendKey(m, "down") // one more than there is to come back from

	if m.commandInput != "" {
		t.Errorf("commandInput after over-shooting ↓ = %q, want it clamped at the empty draft", m.commandInput)
	}
}

func TestCommandHistoryDownWithNoUpFirstIsANoOp(t *testing.T) {
	ws := agendaFixture(t, "* TODO Something\n")
	m := New(ws)
	m = runAndReturn(m, "config")

	m = sendKey(m, ":")
	m = typeKeys(m, "still typing")
	m = sendKey(m, "down")

	if m.commandInput != "still typing" {
		t.Errorf("commandInput after ↓ with no prior ↑ = %q, want it untouched", m.commandInput)
	}
}

func TestCommandHistoryEmptyIsANoOp(t *testing.T) {
	ws := agendaFixture(t, "* TODO Something\n")
	m := New(ws)

	m = sendKey(m, ":")
	m = typeKeys(m, "abc")
	m = sendKey(m, "up")

	if m.commandInput != "abc" {
		t.Errorf("commandInput after ↑ with empty history = %q, want it untouched", m.commandInput)
	}
}

func TestCommandHistoryRecordsEveryCommandIncludingUnknownOnes(t *testing.T) {
	ws := agendaFixture(t, "* TODO Something\n")
	m := New(ws)
	m = runAndReturn(m, "bogus")

	m = sendKey(m, ":")
	m = sendKey(m, "up")

	if m.commandInput != "bogus" {
		t.Errorf("commandInput after ↑ = %q, want %q — history records everything run, valid or not", m.commandInput, "bogus")
	}
}

func TestCommandHistoryDoesNotRecordEmptyOrDismissedInput(t *testing.T) {
	ws := agendaFixture(t, "* TODO Something\n")
	m := New(ws)
	// Enter on a blank command line just dismisses it — nothing to
	// recall.
	m = sendKey(m, ":")
	m = sendKey(m, "enter")

	m = sendKey(m, ":")
	m = sendKey(m, "up")

	if m.commandInput != "" {
		t.Errorf("commandInput after ↑ = %q, want empty — an empty command line shouldn't be recorded", m.commandInput)
	}
}

func TestCommandHistoryDoesNotDeduplicateRepeatedCommands(t *testing.T) {
	ws := agendaFixture(t, "* TODO Something\n")
	m := New(ws)
	m = runAndReturn(m, "config")
	m = runAndReturn(m, "config")

	if len(m.commandHistory) != 2 {
		t.Fatalf("commandHistory = %#v, want 2 entries — vim doesn't dedupe cmdline history either", m.commandHistory)
	}
}

func TestCommandHistoryResetsNavigationOnReopeningCommandMode(t *testing.T) {
	ws := agendaFixture(t, "* TODO Something\n")
	m := New(ws)
	m = runAndReturn(m, "config")
	m = runAndReturn(m, "log")

	m = sendKey(m, ":")
	m = sendKey(m, "up") // now viewing "log"
	m = sendKey(m, "esc")

	// Reopening the command line should start fresh, not resume
	// wherever the last session's ↑/↓ browsing left off.
	m = sendKey(m, ":")
	if m.commandInput != "" {
		t.Fatalf("commandInput on reopening = %q, want empty", m.commandInput)
	}
	m = sendKey(m, "up")
	if m.commandInput != "log" {
		t.Errorf("commandInput after ↑ on a freshly reopened command line = %q, want %q", m.commandInput, "log")
	}
}

func TestCommandHistoryEscDoesNotRecordAbandonedInput(t *testing.T) {
	ws := agendaFixture(t, "* TODO Something\n")
	m := New(ws)
	m = sendKey(m, ":")
	m = typeKeys(m, "abandoned")
	m = sendKey(m, "esc")

	m = sendKey(m, ":")
	m = sendKey(m, "up")

	if m.commandInput != "" {
		t.Errorf("commandInput after ↑ = %q, want empty — Esc-abandoned input should never enter history", m.commandInput)
	}
}
