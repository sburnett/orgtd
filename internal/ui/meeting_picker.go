package ui

import (
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sburnett/orgtd/internal/meetings"
	"github.com/sburnett/orgtd/internal/org"
)

// startMeetingPicker ("gM") opens a fuzzy-filterable picker (see
// updateMeetingPickerMode) over every distinct recurring meeting series
// or one-off event :sync-calendar currently has synced at least one instance
// of (see meetings.Index.Candidates), letting the user toggle the
// chosen meeting's ID on or off the current entry's
// GCAL_RECURRING_EVENT_IDS or GCAL_EVENT_IDS property (matching
// whichever kind the meeting is — see meetings.Meeting.Kind) — so it
// shows up under that meeting in the agenda's Meetings section (see
// appendMeetingsSection) next time it's due (a recurring series) or
// until it happens (a one-off). A no-op (with a status message) if the
// cursor isn't on a headline, the entry is locked by an in-flight
// :format-links batch, or :sync-calendar hasn't synced anything at all — in
// which case there's nothing to offer, and no point opening an empty
// picker.
func (m *Model) startMeetingPicker() {
	h := m.currentHeadline()
	if h == nil || m.refuseIfImmutable(h) {
		return
	}
	now := m.now()
	candidates := m.meetingIndex().Candidates(now)
	if len(candidates) == 0 {
		m.message = "No calendar meetings synced yet (see :sync-calendar)"
		return
	}
	m.mode = meetingPickerMode
	m.meetingPickerTarget = h
	m.meetingPickerCandidates = candidates
	m.meetingPickerFilter = ""
	m.meetingPickerIndex = meetings.DefaultIndex(candidates, now)
}

// updateMeetingPickerMode handles key presses while the "gM" picker is
// open: typing narrows meetingPickerCandidates to those whose title
// contains what's been typed so far (see meetings.Filter), ↑/↓
// browse the (possibly filtered) result, Enter toggles the highlighted
// candidate on the target entry (see applySelectedMeeting), and Esc
// cancels. Unlike the status picker's typeSelectChar, "j"/"k" are not
// special-cased as navigation here — a meeting title is free text that
// can legitimately contain either letter, so only the arrow keys move
// the highlight while typing.
func (m Model) updateMeetingPickerMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.mode = normalMode
		m.meetingPickerTarget = nil
		m.meetingPickerCandidates = nil
		m.meetingPickerFilter = ""
		return m, nil

	case tea.KeyEnter:
		return m.applySelectedMeeting()

	case tea.KeyBackspace:
		if r := []rune(m.meetingPickerFilter); len(r) > 0 {
			m.meetingPickerFilter = string(r[:len(r)-1])
		}
		m.meetingPickerIndex = 0
		return m, nil

	case tea.KeyUp:
		m.moveMeetingHighlight(-1)
		return m, nil

	case tea.KeyDown:
		m.moveMeetingHighlight(1)
		return m, nil

	case tea.KeyRunes:
		m.meetingPickerFilter += string(msg.Runes)
		m.meetingPickerIndex = 0
		return m, nil
	}
	return m, nil
}

func (m *Model) moveMeetingHighlight(delta int) {
	n := len(meetings.Filter(m.meetingPickerCandidates, m.meetingPickerFilter))
	m.meetingPickerIndex += delta
	if m.meetingPickerIndex < 0 {
		m.meetingPickerIndex = 0
	}
	if n > 0 && m.meetingPickerIndex >= n {
		m.meetingPickerIndex = n - 1
	}
}

// applySelectedMeeting toggles the currently highlighted candidate (see
// meetings.Filter/meetingPickerIndex) on meetingPickerTarget
// and always returns to normal mode. A no-op, other than closing the
// picker, if nothing matches the typed filter.
func (m Model) applySelectedMeeting() (tea.Model, tea.Cmd) {
	matches := meetings.Filter(m.meetingPickerCandidates, m.meetingPickerFilter)
	target := m.meetingPickerTarget
	m.mode = normalMode
	m.meetingPickerTarget = nil
	m.meetingPickerCandidates = nil
	m.meetingPickerFilter = ""
	if len(matches) == 0 || target == nil {
		return m, nil
	}
	idx := m.meetingPickerIndex
	if idx < 0 {
		idx = 0
	}
	if idx >= len(matches) {
		idx = len(matches) - 1
	}
	m.pushUndo(m.buildMeetingAttachAction(target, matches[idx]))
	return m, nil
}

// buildMeetingAttachAction builds the undoAction toggling c on or off
// h's ids/links property pair — GCAL_RECURRING_EVENT_IDS/
// GCAL_RECURRING_EVENT_LINKS for a recurring series, GCAL_EVENT_IDS/
// GCAL_EVENT_LINKS for a one-off event, per c.Kind (adding both if c
// isn't yet attached, removing both if it is — see meetings.Meeting.IsAttachedTo),
// without applying or pushing it yet (see pushUndo). The two properties
// are kept index-aligned: attaching appends c's ID and (if c has a
// link) its "[[url][title]]" link to the end of each; detaching removes
// whichever ID matched, and the link at that same index, if one exists
// there (gracefully doing nothing to the links list if the two have
// drifted out of alignment — e.g. a link-less candidate was attached,
// or either property was hand-edited — rather than risk removing the
// wrong entry).
func (m *Model) buildMeetingAttachAction(h *org.Headline, c meetings.Meeting) undoAction {
	idsProp, linksProp := c.Kind.IDsProperty(), c.Kind.LinksProperty()
	oldIDsRaw, hadIDs := h.Properties[idsProp]
	oldLinksRaw, hadLinks := h.Properties[linksProp]

	ids := strings.Fields(oldIDsRaw)
	links := org.ParseLinks(oldLinksRaw)

	if idx := slices.Index(ids, c.ID); idx >= 0 {
		ids = append(ids[:idx], ids[idx+1:]...)
		if idx < len(links) {
			links = append(links[:idx], links[idx+1:]...)
		}
	} else {
		ids = append(ids, c.ID)
		if c.Link != "" {
			links = append(links, org.Link{URL: c.Link, Description: c.Title})
		}
	}

	return &meetingAttachAction{
		h:                h,
		f:                m.ws.FileOf(h),
		idsProp:          idsProp,
		linksProp:        linksProp,
		hadIDsProperty:   hadIDs,
		oldIDs:           oldIDsRaw,
		newIDs:           strings.Join(ids, " "),
		hadLinksProperty: hadLinks,
		oldLinks:         oldLinksRaw,
		newLinks:         org.FormatLinks(links),
	}
}

// meetingPickerLines renders one line per meeting candidate matching the
// "gM" picker's typed filter (see meetings.Filter) for the
// info buffer's "Attach meeting:" section — the structured, one-per-line
// counterpart of the old renderMeetingPicker, which only ever showed the
// single highlighted candidate on the command line (there was nowhere
// else to put the rest before the info buffer existed). matches is
// already in chronological order (meetings.Index.Candidates sorts it that way),
// and each line leads with its date/time — "<date>  <title>" — rather
// than the title, so the times line up in a column and are easy to
// compare down the list; " (attached)" is appended for a candidate
// already on the target entry (see meetings.Meeting.IsAttachedTo — picking it again
// detaches rather than adding a duplicate), and the currently highlighted
// candidate (see meetings.DefaultIndex for how that's chosen when the
// picker first opens) renders in reverse video, same convention as
// statusSelectorLines above. nil if the filter matches nothing.
func (m Model) meetingPickerLines() []string {
	matches := meetings.Filter(m.meetingPickerCandidates, m.meetingPickerFilter)
	if len(matches) == 0 {
		return nil
	}
	idx := m.meetingPickerIndex
	if idx < 0 {
		idx = 0
	}
	if idx >= len(matches) {
		idx = len(matches) - 1
	}

	lines := make([]string, len(matches))
	for i, c := range matches {
		text := c.When.Local().Format("2006-01-02 Mon 15:04") + "  " + c.Title
		if c.IsAttachedTo(m.meetingPickerTarget) {
			text += "  (attached)"
		}
		if i == idx {
			lines[i] = m.cursorStyle().Render(" " + text)
		} else {
			lines[i] = bgSpan(m.overlayBg(), " "+text)
		}
	}
	return lines
}

// renderMeetingPicker renders the "gM" picker's command-line prompt: how
// many candidates match the typed filter (or that none do), and the
// filter text itself. The candidate list itself — title, resolved date,
// and whether each is already attached to the target entry — lives in
// the info buffer's "Attach meeting:" section (see meetingPickerLines,
// above), the same split selectMode's own "R" status picker uses for its
// candidate list (statusSelectorLines) rather than crowding it onto this
// single command-line row.
func (m Model) renderMeetingPicker() string {
	matches := meetings.Filter(m.meetingPickerCandidates, m.meetingPickerFilter)
	line := " Attach meeting: no matches"
	if len(matches) > 0 {
		idx := m.meetingPickerIndex
		if idx < 0 {
			idx = 0
		}
		if idx >= len(matches) {
			idx = len(matches) - 1
		}
		line = fmt.Sprintf(" Attach meeting (%d/%d, Enter toggles attach)", idx+1, len(matches))
	}
	if m.meetingPickerFilter != "" {
		line += "   (" + m.meetingPickerFilter + ")"
	}
	return line
}
