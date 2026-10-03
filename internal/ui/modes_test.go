package ui

import (
	"strings"
	"testing"
)

func TestEveryModeHasAnUpdateHandler(t *testing.T) {
	for k := range modeSpecs {
		if modeSpecs[k].update == nil {
			t.Errorf("mode %d has no update handler (Update would panic the first time it's entered)", k)
		}
	}
}

// lastLine returns the last non-empty line of m's rendered view: the
// command-line row.
func lastLine(m Model) string {
	m.width, m.height = 100, 30
	lines := strings.Split(stripANSI(m.View()), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			return lines[i]
		}
	}
	return ""
}

func TestPromptModesRenderTheirPrompt(t *testing.T) {
	cases := []struct {
		name string
		keys func(m Model) Model
		want string
	}{
		{"command", func(m Model) Model { return typeKeys(m, ":wq") }, ":wq"},
		{"search forward", func(m Model) Model { return typeKeys(m, "/fido") }, "/fido"},
		{"search backward", func(m Model) Model { return typeKeys(m, "?fido") }, "?fido"},
		{"deadline", func(m Model) Model { return typeKeys(m, "gd3d") }, "Deadline"},
		{"tag", func(m Model) Model { return typeKeys(m, "gtpro") }, "Tag (Tab completes"},
		{"status picker", func(m Model) Model { return typeKeys(m, "r") }, "Set status"},
		{"visual", func(m Model) Model { return typeKeys(m, "V") }, "-- VISUAL LINE --"},
	}
	for _, c := range cases {
		m := New(loadFixture(t))
		m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
		m = c.keys(m)
		if got := lastLine(m); !strings.Contains(got, c.want) {
			t.Errorf("%s: command row = %q, want it to contain %q", c.name, got, c.want)
		}
	}
}

func TestNormalModeShowsOnlyTheMessageOnTheCommandRow(t *testing.T) {
	m := New(loadFixture(t))
	if modeSpecs[normalMode].prompt != nil {
		t.Fatal("normal mode should have no prompt")
	}
	m.message = "Something happened"
	if got := lastLine(m); !strings.Contains(got, "Something happened") {
		t.Errorf("command row = %q, want the message", got)
	}
}

func TestPromptShowsAMessageSetWithoutLeavingTheMode(t *testing.T) {
	// An invalid deadline keeps the prompt open and reports why: both must
	// be on the command row.
	m := New(loadFixture(t))
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")
	m = typeKeys(m, "gdnot a date")
	m = sendKey(m, "enter")
	if m.mode != deadlineMode {
		t.Fatalf("mode = %v, want the prompt to stay open", m.mode)
	}
	line := lastLine(m)
	if !strings.Contains(line, "Deadline") || !strings.Contains(line, "invalid date") {
		t.Errorf("command row = %q, want the prompt and the error", line)
	}
}

func TestModeInfoSectionsAppearOnlyInTheirMode(t *testing.T) {
	m := New(loadFixture(t))
	m.cursor = findRow(t, m, "Call the vet about Fido's checkup")

	if joined := strings.Join(stripANSILines(m.infoBufferLines()), "\n"); strings.Contains(joined, "Status:") {
		t.Errorf("normal mode shows the Status section:\n%s", joined)
	}
	m = typeKeys(m, "r")
	if joined := strings.Join(stripANSILines(m.infoBufferLines()), "\n"); !strings.Contains(joined, "Status:") {
		t.Errorf("the status picker's info section is missing:\n%s", joined)
	}
	m = sendKey(m, "esc")
	m = typeKeys(m, ":")
	m = sendKey(m, "tab") // lists every command: a "Matches:" section below
	if joined := strings.Join(stripANSILines(m.infoBufferLines()), "\n"); !strings.Contains(joined, "Matches:") {
		t.Errorf("command-mode completion matches are missing:\n%s", joined)
	}
}
