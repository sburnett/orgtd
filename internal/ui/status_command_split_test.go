package ui

import (
	"strings"
	"testing"
)

// splitBottomLines renders m and returns its two bottom-most lines
// (status, then command line), ANSI stripped — every test below checks
// that both are present and distinct, regardless of mode, mirroring
// vim's own always-visible statusline-above-cmdline split.
func splitBottomLines(t *testing.T, m Model) (status, command string) {
	t.Helper()
	out := stripANSI(m.View())
	lines := strings.Split(out, "\n")
	if len(lines) < 2 {
		t.Fatalf("View() = %q, want at least 2 lines", out)
	}
	return lines[len(lines)-2], lines[len(lines)-1]
}

func TestStatusLineStaysVisibleInCommandMode(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.width, m.height = 100, 30

	m = sendKey(m, ":")
	m = typeKeys(m, "config")

	status, command := splitBottomLines(t, m)
	if !strings.Contains(status, "item") {
		t.Errorf("status line = %q, want it to still show the usual item count", status)
	}
	if !strings.HasPrefix(command, ":config") {
		t.Errorf("command line = %q, want it to show the typed command", command)
	}
}

func TestStatusLineStaysVisibleInSearchMode(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.width, m.height = 100, 30

	m = sendKey(m, "/")
	m = typeKeys(m, "vet")

	status, command := splitBottomLines(t, m)
	if !strings.Contains(status, "item") {
		t.Errorf("status line = %q, want it to still show the usual item count", status)
	}
	if !strings.HasPrefix(command, "/vet") {
		t.Errorf("command line = %q, want it to show the search query", command)
	}
}

func TestStatusLineStaysVisibleInVisualMode(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.width, m.height = 100, 30

	m = sendKey(m, "V")

	status, command := splitBottomLines(t, m)
	if !strings.Contains(status, "item") {
		t.Errorf("status line = %q, want it to still show the usual item count", status)
	}
	if !strings.Contains(command, "VISUAL LINE") {
		t.Errorf("command line = %q, want the visual-mode banner", command)
	}
}

func TestStatusLineStaysVisibleInSelectMode(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.width, m.height = 100, 30
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")

	m = sendKey(m, "R")

	status, command := splitBottomLines(t, m)
	if !strings.Contains(status, "item") {
		t.Errorf("status line = %q, want it to still show the usual item count", status)
	}
	if !strings.Contains(command, "Set status") {
		t.Errorf("command line = %q, want the status picker", command)
	}
}

func TestStatusLineStaysVisibleInConfirmMode(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.width, m.height = 100, 30
	m.cursor = 0
	if m.rows[0].file == nil {
		t.Fatalf("fixture assumption broken: row 0 is not a file row")
	}

	m = sendKey(m, "i")
	if m.mode != confirmMode {
		t.Fatalf("fixture assumption broken: mode = %v, want confirmMode", m.mode)
	}

	status, command := splitBottomLines(t, m)
	if !strings.Contains(status, "item") {
		t.Errorf("status line = %q, want it to still show the usual item count", status)
	}
	if command != m.confirmMessage {
		t.Errorf("command line = %q, want the confirm prompt %q", command, m.confirmMessage)
	}
}

func TestStatusLineStaysVisibleInDeadlineMode(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.width, m.height = 100, 30
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")

	m = sendKey(m, "g")
	m = sendKey(m, "d")

	status, command := splitBottomLines(t, m)
	if !strings.Contains(status, "item") {
		t.Errorf("status line = %q, want it to still show the usual item count", status)
	}
	if !strings.Contains(command, "Deadline") {
		t.Errorf("command line = %q, want the deadline prompt", command)
	}
}

func TestCommandLineBlankWhenIdle(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.width, m.height = 100, 30

	status, command := splitBottomLines(t, m)
	if !strings.Contains(status, "item") {
		t.Errorf("status line = %q, want it to still show the usual item count", status)
	}
	if command != "" {
		t.Errorf("command line = %q, want blank when idle", command)
	}
}

func TestCommandLineShowsMessageWithoutHidingStatusLine(t *testing.T) {
	ws := loadFixture(t)
	m := New(ws)
	m.width, m.height = 100, 30
	m.message = "something happened"

	status, command := splitBottomLines(t, m)
	if !strings.Contains(status, "item") {
		t.Errorf("status line = %q, want it to still show the usual item count", status)
	}
	if !strings.Contains(command, "something happened") {
		t.Errorf("command line = %q, want the message", command)
	}
}
