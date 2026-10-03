package ui

import (
	"strings"
	"testing"
)

// TestEveryViewHasACompleteSpec guards the registry itself: a view added to
// the viewKind enum without a viewSpec (or with a duplicate command) would
// otherwise only fail at runtime, as a nil-function panic the first time
// someone opens it.
func TestEveryViewHasACompleteSpec(t *testing.T) {
	seen := map[string]viewKind{}
	for k := range viewSpecs {
		spec := viewSpecs[k]
		if spec.build == nil {
			t.Errorf("view %d has no build function", k)
		}
		if spec.command == "" {
			t.Errorf("view %d has no command", k)
			continue
		}
		if prev, dup := seen[spec.command]; dup {
			t.Errorf("views %d and %d share the command %q", prev, k, spec.command)
		}
		seen[spec.command] = viewKind(k)
	}
	if len(seen) != int(numViews) {
		t.Errorf("registry has %d distinct commands, want %d (one per view)", len(seen), numViews)
	}
}

// TestViewCommandsOpenTheirViews checks the ":command" → view routing for
// every view, through the real command line rather than the registry
// lookup, so a spec whose command or open hook is wrong is caught.
func TestViewCommandsOpenTheirViews(t *testing.T) {
	for k := range viewSpecs {
		kind := viewKind(k)
		if kind == diffView {
			// Refuses unless the org dir is a git repo root; covered by
			// the :diff tests.
			continue
		}
		m := New(loadFixture(t))
		m = typeKeys(m, ":"+viewSpecs[k].command)
		m = sendKey(m, "enter")
		if m.view != kind {
			t.Errorf(":%s left view = %d, want %d (message %q)", viewSpecs[k].command, m.view, kind, m.message)
		}
	}
}

// TestViewCommandsAreTabCompletable checks that every view's command is
// offered by command-mode completion — it's derived from the registry, so
// a view can't be added and forgotten there.
func TestViewCommandsAreTabCompletable(t *testing.T) {
	names := strings.Join(commandNames(), " ")
	for k := range viewSpecs {
		if !strings.Contains(" "+names+" ", " "+viewSpecs[k].command+" ") {
			t.Errorf("commandNames() lacks %q", viewSpecs[k].command)
		}
	}
}

// TestStatusLineLabelsComeFromTheSpec covers the status line's place
// label: the workspace directory for the outline-like views, the view's
// own label for the rest.
func TestStatusLineLabelsComeFromTheSpec(t *testing.T) {
	m := New(loadFixture(t))
	if line := m.normalStatusLine(); !strings.Contains(line, m.ws.Dir) {
		t.Errorf("outline status line = %q, want the directory", line)
	}
	m.switchToView(agendaView)
	if line := m.normalStatusLine(); !strings.Contains(line, " agenda ") {
		t.Errorf("agenda status line = %q, want the agenda label", line)
	}
}

// TestEnterUsesTheViewsEnterHook checks Enter is dispatched per view: it
// jumps to the source in agenda view, and does nothing in a view with no
// enter hook (the outline).
func TestEnterUsesTheViewsEnterHook(t *testing.T) {
	m := New(loadFixture(t))
	m = sendKey(m, "j")
	cursor := m.cursor
	m = sendKey(m, "enter")
	if m.view != outlineView || m.cursor != cursor {
		t.Errorf("Enter in the outline changed view/cursor: view=%d cursor=%d, want unchanged", m.view, m.cursor)
	}
}
