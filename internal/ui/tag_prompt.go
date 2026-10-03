package ui

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sburnett/orgtd/internal/meetings"
	"github.com/sburnett/orgtd/internal/org"
)

// startTagPrompt opens the tag-entry prompt ("gt") for the current
// headline. A no-op on file rows or a locked (:format-links) entry.
func (m *Model) startTagPrompt() {
	h := m.currentHeadline()
	if h == nil || m.refuseIfImmutable(h) {
		return
	}
	m.mode = tagMode
	m.tagTarget = h
	m.tagInput = ""
	m.tagCompletions = ""
}

// isTagRune reports whether r is a character org-mode allows inside a
// tag (see tagsRe in internal/org) — letters, digits, and "_@%#+".
// Anything else typed at the tag prompt (spaces in particular — a tag
// can never contain one) is silently dropped rather than accepted and
// later rejected, since there's no valid tag it could ever become part
// of.
func isTagRune(r rune) bool {
	if unicode.IsLetter(r) || unicode.IsDigit(r) {
		return true
	}
	switch r {
	case '_', '@', '%', '#', '+':
		return true
	}
	return false
}

// workspaceTags returns every distinct tag used anywhere in the
// workspace, sorted, for the "gt" prompt's Tab-completion (see
// completeTagInput).
func (m *Model) workspaceTags() []string {
	seen := make(map[string]bool)
	for _, f := range m.ws.Files {
		org.Walk(f.Headlines, func(h *org.Headline) {
			for _, t := range h.Tags {
				seen[t] = true
			}
		})
	}
	tags := make([]string, 0, len(seen))
	for t := range seen {
		tags = append(tags, t)
	}
	sort.Strings(tags)
	return tags
}

// updateTagMode handles key presses while the "gt" prompt is open: Tab
// completes against every existing tag in the workspace (same
// prefix/longest-common-prefix behavior as command-mode's
// completeCommand), Enter toggles the typed tag on the target entry
// (see applyTagInput), and Esc cancels.
func (m Model) updateTagMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type != tea.KeyTab {
		// Same reasoning as updateCommandMode: a shown completion list (or
		// a "no match" message it left) is a one-shot hint for the
		// keystroke right after Tab, not something that should linger.
		m.tagCompletions = ""
	}
	if msg.Type != tea.KeyEnter {
		m.message = ""
	}

	switch msg.Type {
	case tea.KeyEsc:
		m.mode = normalMode
		m.tagTarget = nil
		m.tagInput = ""
		m.tagCompletions = ""
		return m, nil

	case tea.KeyEnter:
		return m.applyTagInput()

	case tea.KeyBackspace:
		if r := []rune(m.tagInput); len(r) > 0 {
			m.tagInput = string(r[:len(r)-1])
		}
		return m, nil

	case tea.KeyRunes:
		for _, r := range msg.Runes {
			if isTagRune(r) {
				m.tagInput += string(r)
			}
		}
		return m, nil

	case tea.KeyTab:
		m.completeTagInput()
		return m, nil
	}
	return m, nil
}

// completeTagInput implements the "gt" prompt's Tab-completion: if the
// text typed so far is a prefix of exactly one existing tag, the input
// is completed to it in full; if it's a prefix of several, the input is
// extended to their longest common prefix and the matches are listed
// after it (see tagCompletions); if it matches none, a message says so.
// An empty prompt lists every existing tag, so what's available to
// complete toward is visible even before typing a character of it.
func (m *Model) completeTagInput() {
	word := m.tagInput
	all := m.workspaceTags()

	if word == "" {
		if len(all) == 0 {
			m.message = "No existing tags"
			return
		}
		m.tagCompletions = strings.Join(all, "  ")
		return
	}

	var matches []string
	for _, tag := range all {
		if strings.HasPrefix(tag, word) {
			matches = append(matches, tag)
		}
	}

	switch len(matches) {
	case 0:
		m.message = fmt.Sprintf("No existing tag starting with %q", word)
	case 1:
		m.tagInput = matches[0]
	default:
		if common := commonPrefix(matches); len(common) > len(word) {
			m.tagInput = common
		}
		m.tagCompletions = strings.Join(matches, "  ")
	}
}

// applyTagInput toggles the typed tag (trimmed) on tagTarget: appended
// if not already present, removed if it is — so pressing "gt" and
// retyping the same tag is how a tag comes back off, matching the
// keybinding table's own "adding a tag again removes it". A blank
// input (Enter with nothing typed) cancels without changes, same as
// Esc, rather than clearing every tag — there's no way to distinguish
// "clear all tags" from "typed nothing" otherwise, and gd's own
// empty-clears convention doesn't apply here since a headline can carry
// more than one tag. Always returns to normal mode.
//
// If target is itself a synced calendar event (meetings.Identity), the
// tag is recorded in meeting-tags.org instead (see applyMeetingTag) —
// editing target's own Tags here would just be undone by the next
// :sync-calendar, which wholesale-regenerates the calendar file.
func (m Model) applyTagInput() (tea.Model, tea.Cmd) {
	m.message = ""
	tag := strings.TrimSpace(m.tagInput)
	target := m.tagTarget
	m.mode = normalMode
	m.tagTarget = nil
	m.tagInput = ""
	m.tagCompletions = ""
	if tag == "" || target == nil {
		return m, nil
	}

	if key, ok := meetings.Identity(target); ok {
		m.applyMeetingTag(target, key.Kind, key.ID, tag)
		return m, nil
	}

	old := append([]string(nil), target.Tags...)
	var newTags []string
	removed := false
	for _, t := range target.Tags {
		if t == tag {
			removed = true
			continue
		}
		newTags = append(newTags, t)
	}
	if !removed {
		newTags = append(newTags, tag)
	}
	m.pushUndo(&tagChangeAction{h: target, f: m.fileForHeadline(target), oldTags: old, newTags: newTags})
	return m, nil
}
