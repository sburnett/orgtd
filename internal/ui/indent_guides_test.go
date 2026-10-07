package ui

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func TestIndentGuidesDrawnUnderAncestorArrows(t *testing.T) {
	m := New(loadFixture(t))
	i := findRow(t, m, "Implement the org file parser")
	h := m.rows[i].headline
	if h.Level != 2 || h.Parent == nil {
		t.Fatalf("fixture assumption broken: level %d, parent %v", h.Level, h.Parent)
	}
	got := ansi.Strip(m.indentGuides(h.Parent, h.Level, nil, lipgloss.NoColor{}))
	// Level 2: four columns, with the parent's guide under its arrow at column 2.
	if got != "  │ " {
		t.Errorf("indent = %q, want %q", got, "  │ ")
	}
	if top := ansi.Strip(m.indentGuides(nil, 1, nil, lipgloss.NoColor{})); top != "  " {
		t.Errorf("top-level indent = %q, want two blanks", top)
	}
}

func TestIndentGuideScopeIsBrighter(t *testing.T) {
	// The suite pins a 16-color profile, where both greys collapse to one.
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(termenv.ANSI) })
	m := New(loadFixture(t))
	i := findRow(t, m, "Implement the org file parser")
	m.cursor = i
	h := m.rows[i].headline
	if m.guideScope() != h.Parent {
		t.Fatalf("scope = %v, want the cursor's parent", m.guideScope())
	}
	plain := m.indentGuides(h.Parent, h.Level, nil, lipgloss.NoColor{})
	scoped := m.indentGuides(h.Parent, h.Level, h.Parent, lipgloss.NoColor{})
	if plain == scoped {
		t.Errorf("scope guide renders identically to a dim guide")
	}
}
