package ui

import (
	"strings"
	"testing"
)

func TestStatusLineHasNoBusyIconWhenIdle(t *testing.T) {
	m := New(loadFixture(t))
	m.width, m.height = 100, 30

	status, _ := splitBottomLines(t, m)
	if strings.Contains(status, busyIcon) {
		t.Errorf("status line = %q, want no %q with nothing running", status, busyIcon)
	}
}

func TestStatusLineShowsBusyIconFlushRightWhileCalendarSyncs(t *testing.T) {
	m := New(loadFixture(t))
	m.width, m.height = 100, 30
	m.syncingCalendar = true

	status, _ := splitBottomLines(t, m)
	if !strings.HasSuffix(strings.TrimRight(status, " "), busyIcon) {
		t.Errorf("status line = %q, want it to end with %q", status, busyIcon)
	}
	if w := len([]rune(status)); w != m.width {
		t.Errorf("status line width = %d, want %d", w, m.width)
	}
}

func TestStatusLineBusyIconSurvivesNarrowTerminal(t *testing.T) {
	m := New(loadFixture(t))
	m.width, m.height = 20, 30
	m.syncingCalendar = true

	status, _ := splitBottomLines(t, m)
	if !strings.Contains(status, busyIcon) {
		t.Errorf("status line = %q, want %q kept even when the rest is cut", status, busyIcon)
	}
	if !strings.Contains(status, "…") {
		t.Errorf("status line = %q, want its text truncated with …", status)
	}
	if w := len([]rune(status)); w != m.width {
		t.Errorf("status line width = %d, want %d", w, m.width)
	}
}

func TestBusyIconTracksFormatLinksBatch(t *testing.T) {
	ws := agendaFixture(t, "* TODO Has https://example.com/a\n")
	m := New(ws, WithURLFormatter(writeBatchFakeFormatter(t, `echo "[[$line][A]]"`)))
	m.width, m.height = 100, 30

	cmd := m.startFormatLinks()
	if cmd == nil {
		t.Fatal("startFormatLinks returned no command")
	}
	if m.busyIndicator() != busyIcon {
		t.Errorf("busyIndicator() = %q while the batch is in flight, want %q", m.busyIndicator(), busyIcon)
	}

	next, _ := m.Update(cmd())
	m = next.(Model)
	if got := m.busyIndicator(); got != "" {
		t.Errorf("busyIndicator() = %q after the batch finished, want empty", got)
	}
}
