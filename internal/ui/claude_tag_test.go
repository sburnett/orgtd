package ui

import (
	"slices"
	"strings"
	"testing"
)

const claudeFixture = "* TODO Talked it over :work:\n  :PROPERTIES:\n  :CLAUDE_SESSION: 0b9c6a1e\n  :END:\n" +
	"* TODO Untagged chat\n  :PROPERTIES:\n  :CLAUDE_SESSION: 7d2f\n  :END:\n" +
	"* TODO No session :work:\n"

func TestClaudeSessionShowsAsAGeneratedTag(t *testing.T) {
	m := layoutModel(t, claudeFixture, 100)
	tests := []struct {
		title, want string
	}{
		{"Talked it over", ":work:✻:"},
		{"Untagged chat", ":✻:"},
		{"No session", ":work:"},
	}
	for _, tt := range tests {
		row := stripANSI(m.renderRow(m.rows[findRow(t, *m, tt.title)]))
		if !strings.HasSuffix(row, tt.want) {
			t.Errorf("row = %q, want it to end with %q", row, tt.want)
		}
		if tt.title == "No session" && strings.Contains(row, "✻") {
			t.Errorf("row = %q, want no Claude tag without a CLAUDE_SESSION", row)
		}
	}
	if got := m.ws.Files[0].Headlines[0].Tags; !slices.Equal(got, []string{"work"}) {
		t.Errorf("Tags = %q, want the entry's own tags left untouched", got)
	}
}

func TestClaudeTagHasItsOwnColor(t *testing.T) {
	m := layoutModel(t, claudeFixture, 100)
	m2 := New(m.ws, WithClaudeIcon("", "1"))
	m2.width = 100
	m2.rebuildRows()
	i := findRow(t, *m, "Talked it over")
	if a, b := m.renderRow(m.rows[i]), m2.renderRow(m2.rows[i]); a == b {
		t.Errorf("row renders the same with claude_color changed, want the tag drawn in that color: %q", a)
	}
}

func TestClaudeTagUsesConfiguredIcon(t *testing.T) {
	m := New(agendaFixture(t, claudeFixture), WithClaudeIcon("§", ""))
	m.width = 100
	m.rebuildRows()
	row := stripANSI(m.renderRow(m.rows[findRow(t, m, "Talked it over")]))
	if !strings.HasSuffix(row, ":work:§:") {
		t.Errorf("row = %q, want the configured icon as the tag", row)
	}
}

func TestClaudeTagIsNotEditable(t *testing.T) {
	m := New(agendaFixture(t, claudeFixture))
	m.width, m.height = 100, 30
	m.rebuildRows()
	m.cursor = findRow(t, m, "Talked it over")
	m = sendKey(m, "g")
	m = sendKey(m, "t")
	if m.mode != insertMode || !m.ins.inTags {
		t.Fatalf("mode %v, inTags %v; want insert mode in the tag area", m.mode, m.ins.inTags)
	}
	if !slices.Equal(m.ins.tags, []string{"work", ""}) {
		t.Errorf("tags while editing = %q, want just the entry's own tag and a new one", m.ins.tags)
	}
	for _, line := range strings.Split(stripANSI(m.View()), "\n") {
		if strings.Contains(line, "Talked it over") && strings.Contains(line, "✻") {
			t.Errorf("line being edited = %q, want the Claude tag hidden", line)
		}
	}
	m = sendKey(m, "esc")
	if got := m.ws.Files[0].Headlines[0].Tags; !slices.Equal(got, []string{"work"}) {
		t.Errorf("Tags after editing = %q, want the Claude tag never written to the file", got)
	}
}

func TestClaudeTagIsNotOfferedForCompletion(t *testing.T) {
	m := New(agendaFixture(t, claudeFixture))
	if tags := m.workspaceTags(); slices.Contains(tags, "✻") {
		t.Errorf("workspaceTags = %q, want no Claude tag", tags)
	}
}

func TestTagsViewListsEntriesWithAClaudeSession(t *testing.T) {
	m := New(agendaFixture(t, claudeFixture))
	m.switchToView(tagsView)

	var got []string
	for _, r := range m.rows {
		if r.kind == rowTagsItem && r.tagsItemTag == "✻" {
			got = append(got, r.headline.Title)
		}
	}
	if want := []string{"Talked it over", "Untagged chat"}; !slices.Equal(got, want) {
		t.Errorf("entries under ✻ = %q, want %q", got, want)
	}
}
