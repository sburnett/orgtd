package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/sburnett/orgtd/internal/config"
	"github.com/sburnett/orgtd/internal/org"
)

// The whole color scheme below matches the dark variant of "wildcharm"
// (https://github.com/vim/colorschemes/blob/master/colors/wildcharm.vim):
// each orgtd role is mapped to whichever wildcharm highlight group it's
// closest to in meaning, not just in hue, so the mapping stays sensible
// if either side's palette shifts later. Since matching one specific
// dark theme is the whole point, these are plain (non-adaptive) colors
// rather than light/dark pairs — they always render as wildcharm-dark by
// default, regardless of the terminal's own background, unless
// overridden (see config.ColorsConfig/WithColors and the config file's
// "[colors]" section, README.md).
//
// Every one of these is a Model method rather than a package-level
// value, resolving m.cfg.Colors' matching field (falling back to the
// default* constant below when unset — see orDefault) fresh on each
// call, so a Model built with WithColors renders with its own overrides
// while a zero-value Model (as most tests construct directly) still gets
// the built-in wildcharm-dark look for free.
const (
	defaultFileColor = "#00afff" // Directory: bold blue, the closest match for "a path, not an item"

	// TODO/Next/Waiting: wildcharm's most alarming/attention colors.
	// Someday/Done/Cancelled are deliberately unbold — wildcharm reserves
	// bold for things that need attention.
	defaultTODOColor      = "#d7005f" // Error/Removed red
	defaultNextColor      = "#ffaf00" // Type/WarningMsg/WildMenu orange
	defaultWaitingColor   = "#875fff" // Special purple
	defaultSomedayColor   = "#767676" // Comment grey
	defaultDoneColor      = "#00d75f" // Constant/Added green
	defaultCancelledColor = "#585858" // LineNr/Conceal dark grey

	defaultTagColor       = "#00d7d7" // PreProc cyan
	defaultDoneTitleColor = "#767676" // Comment grey
	defaultStatusColor    = "#767676" // Comment grey, for muted status/info text
	defaultTimestampColor = "#ff87ff" // Identifier/Question magenta
	defaultErrorColor     = "#d7005f" // Error/Removed red
	defaultBodyColor      = "#a8a8a8" // Lighter grey, for easier reading than Comment grey

	// defaultCaretFg/Bg color the command-line's text-cursor caret (a
	// lone space standing in for the terminal's own cursor block, since
	// there's no in-line editing to place a real one) — wildcharm's
	// Cursor: a solid, unmissable block regardless of what's behind it,
	// unlike defaultHighlightBg below (which only ever tints a
	// background, leaving whatever foreground was already there alone).
	defaultCaretFg = "#000000"
	defaultCaretBg = "#ffffff"

	// defaultHighlightBg tints the selected line within an overlay
	// list — the "R"/status picker and "gM" meeting picker's highlighted
	// candidate (see statusSelectorLines/meetingPickerLines) — wildcharm's
	// PmenuSel: like real Vim's own popup-menu selection, it changes only
	// the background, leaving the row's own text color alone, rather than
	// inverting video the way a plain terminal cursor block does.
	defaultHighlightBg = "#585858"

	// defaultPanelBg tints the info buffer (review/marks/register,
	// links, meeting detail, tag/command-completion matches, the status
	// and "gM" meeting pickers) — wildcharm's Pmenu: its popup-menu
	// panel color.
	defaultPanelBg = "#303030"

	// defaultStatusBarBg/Fg tint the one-line status bar at the bottom of
	// the screen (see normalStatusLine) — wildcharm's StatusLine, which
	// is defined as light-grey-on-black with a "reverse" attribute;
	// rather than rely on terminal reverse-video (whose effect on top of
	// our own explicit colors elsewhere would be inconsistent), the swap
	// is baked in directly: these are the *visual* result, StatusLine's
	// colors once reversed.
	defaultStatusBarBg = "#9e9e9e"
	defaultStatusBarFg = "#000000"

	// defaultCursorRowBg highlights the row under the cursor, filling the
	// whole terminal width — wildcharm's Visual (its selection color),
	// the obvious match for "the entry you currently have selected". In
	// visual mode, only the cursor's own entry keeps this shade (see
	// defaultVisualSelectionBg for the rest of the selection), so which
	// end of a multi-entry selection is the actual cursor is always
	// unambiguous.
	defaultCursorRowBg = "#204060"

	// defaultVisualSelectionBg highlights the part of a visual-mode
	// selection that isn't the cursor's own entry — wildcharm has no
	// second selection shade of its own, so this is defaultCursorRowBg's
	// same hue blended halfway toward Normal's black background, staying
	// recognizably the same color family while reading as less prominent
	// than the cursor.
	defaultVisualSelectionBg = "#102030"

	// defaultSearchHighlightBg marks every occurrence of the active
	// search term (see activeSearchQuery) — vim's 'hlsearch' — layered on
	// top of whatever background (if any) a segment already carries.
	// Wildcharm's own Search background.
	defaultSearchHighlightBg = "#3a4a3a"
)

// shortcutStyle is the one piece of styling with no configurable color
// at all — it only ever sets Bold, matching wildcharm's own Title group
// (gui=bold, no guifg) — used for a picker candidate's bracketed
// shortcut ("[t]").
var shortcutStyle = lipgloss.NewStyle().Bold(true)

// WithColors overrides orgtd's built-in wildcharm-dark color scheme —
// see config.ColorsConfig for what each field controls. Fields left at ""
// (the zero value, including every field when this option is never
// applied at all) keep their built-in default.
func WithColors(c config.ColorsConfig) Option {
	return func(m *Model) { m.cfg.Colors = c }
}

// fileStyle marks a file's own header row.
func (m Model) fileStyle() lipgloss.Style {
	return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(orDefault(m.cfg.Colors.File, defaultFileColor)))
}

// keywordStyle returns the style for a headline's TODO keyword, and
// whether it's a recognized one at all (an unrecognized/absent keyword
// gets ok == false, same as the old keywordStyles map's own comma-ok
// lookup) — see keywordStyles' old doc comment (now folded into the
// default* constants above) for why each keyword gets the color it does.
func (m Model) keywordStyle(keyword string) (lipgloss.Style, bool) {
	switch keyword {
	case "TODO":
		return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(orDefault(m.cfg.Colors.TODO, defaultTODOColor))), true
	case "NEXT":
		return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(orDefault(m.cfg.Colors.Next, defaultNextColor))), true
	case "WAITING":
		return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(orDefault(m.cfg.Colors.Waiting, defaultWaitingColor))), true
	case "SOMEDAY":
		return lipgloss.NewStyle().Foreground(lipgloss.Color(orDefault(m.cfg.Colors.Someday, defaultSomedayColor))), true
	case "DONE":
		return lipgloss.NewStyle().Foreground(lipgloss.Color(orDefault(m.cfg.Colors.Done, defaultDoneColor))), true
	case "CANCELLED":
		return lipgloss.NewStyle().Foreground(lipgloss.Color(orDefault(m.cfg.Colors.Cancelled, defaultCancelledColor))), true
	default:
		return lipgloss.Style{}, false
	}
}

func (m Model) tagStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(orDefault(m.cfg.Colors.Tag, defaultTagColor)))
}

func (m Model) doneTitleStyle() lipgloss.Style {
	return lipgloss.NewStyle().Strikethrough(true).Foreground(lipgloss.Color(orDefault(m.cfg.Colors.DoneTitle, defaultDoneTitleColor)))
}

// statusStyle is for muted status/info text: the directory path on the
// status line, register/overflow summaries, and the visual-mode banner.
func (m Model) statusStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(orDefault(m.cfg.Colors.Status, defaultStatusColor)))
}

func (m Model) timestampStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(orDefault(m.cfg.Colors.Timestamp, defaultTimestampColor)))
}

func (m Model) errorStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(orDefault(m.cfg.Colors.Error, defaultErrorColor)))
}

func (m Model) bodyStyle() lipgloss.Style {
	return lipgloss.NewStyle().Italic(true).Foreground(lipgloss.Color(orDefault(m.cfg.Colors.Body, defaultBodyColor)))
}

// caretStyle renders the command-line's text-cursor caret.
func (m Model) caretStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(orDefault(m.cfg.Colors.CaretFg, defaultCaretFg))).
		Background(lipgloss.Color(orDefault(m.cfg.Colors.CaretBg, defaultCaretBg)))
}

// cursorStyle highlights the selected line within an overlay list — the
// "R"/status picker and "gM" meeting picker's highlighted candidate (see
// statusSelectorLines/meetingPickerLines).
func (m Model) cursorStyle() lipgloss.Style {
	return lipgloss.NewStyle().Background(lipgloss.Color(orDefault(m.cfg.Colors.HighlightBg, defaultHighlightBg)))
}

// overlayBg is the background tint for the info buffer (review/marks/
// register, links, meeting detail, tag/command-completion matches, the
// status and "gM" meeting pickers).
func (m Model) overlayBg() lipgloss.TerminalColor {
	return lipgloss.Color(orDefault(m.cfg.Colors.PanelBg, defaultPanelBg))
}

// statusBarBg/statusBarFg tint the one-line status bar at the bottom of
// the screen (see normalStatusLine).
func (m Model) statusBarBg() lipgloss.TerminalColor {
	return lipgloss.Color(orDefault(m.cfg.Colors.StatusBarBg, defaultStatusBarBg))
}

func (m Model) statusBarFg() lipgloss.TerminalColor {
	return lipgloss.Color(orDefault(m.cfg.Colors.StatusBarFg, defaultStatusBarFg))
}

// cursorBg highlights the row under the cursor, filling the whole
// terminal width. In visual mode, only the cursor's own entry keeps this
// shade (see visualSelectionBg for the rest of the selection), so which
// end of a multi-entry selection is the actual cursor is always
// unambiguous.
func (m Model) cursorBg() lipgloss.TerminalColor {
	return lipgloss.Color(orDefault(m.cfg.Colors.CursorRowBg, defaultCursorRowBg))
}

// visualSelectionBg highlights the part of a visual-mode selection that
// isn't the cursor's own entry.
func (m Model) visualSelectionBg() lipgloss.TerminalColor {
	return lipgloss.Color(orDefault(m.cfg.Colors.VisualSelectionBg, defaultVisualSelectionBg))
}

// searchHighlightBg marks every occurrence of the active search term
// (see activeSearchQuery) — vim's 'hlsearch' — layered on top of
// whatever background (if any) a segment already carries.
func (m Model) searchHighlightBg() lipgloss.TerminalColor {
	return lipgloss.Color(orDefault(m.cfg.Colors.SearchHighlightBg, defaultSearchHighlightBg))
}

// bgSpan renders s with only a background color — no other styling —
// for the plain-text gaps (join separators, padding) inside a
// background-tinted line, so they don't leave un-tinted holes once an
// adjacent styled segment's own reset code fires.
func bgSpan(bg lipgloss.TerminalColor, s string) string {
	return lipgloss.NewStyle().Background(bg).Render(s)
}

// highlightMatches renders s with every case-insensitive occurrence of
// query given an extra searchHighlightBg background layered on top of
// base, and everything else rendered plainly with base. Each segment is
// rendered independently (not nested) so this composes correctly
// regardless of what background base itself already carries. A blank
// query renders s with base unchanged. A method (rather than a free
// function) purely so it can resolve m's own searchHighlightBg (see
// config.ColorsConfig) — it doesn't otherwise depend on any Model state.
func (m Model) highlightMatches(s, query string, base lipgloss.Style) string {
	if query == "" {
		return base.Render(s)
	}
	lowerS := strings.ToLower(s)
	lowerQ := strings.ToLower(query)
	highlight := base.Background(m.searchHighlightBg())

	var b strings.Builder
	last := 0
	for {
		rel := strings.Index(lowerS[last:], lowerQ)
		if rel < 0 {
			break
		}
		start := last + rel
		end := start + len(query)
		if start > last {
			b.WriteString(base.Render(s[last:start]))
		}
		b.WriteString(highlight.Render(s[start:end]))
		last = end
	}
	if last < len(s) {
		b.WriteString(base.Render(s[last:]))
	}
	return b.String()
}

// joinBg joins parts with a bg-tinted single space, the background-aware
// equivalent of strings.Join(parts, " ").
func joinBg(parts []string, bg lipgloss.TerminalColor) string {
	return strings.Join(parts, bgSpan(bg, " "))
}

// padLineToWidth extends line with bg-tinted spaces up to m.width, so a
// background tint fills the whole terminal row rather than stopping
// wherever the visible text ends. A no-op if the width is unknown
// (m.width <= 0) or line already reaches or exceeds it.
func (m Model) padLineToWidth(line string, bg lipgloss.TerminalColor) string {
	if m.width <= 0 {
		return line
	}
	pad := m.width - lipgloss.Width(line)
	if pad <= 0 {
		return line
	}
	return line + bgSpan(bg, strings.Repeat(" ", pad))
}

// fitRowLine joins prefix (a headline's keyword/title, plus whatever
// comes before it — mark/lock/gutter/indent/fold columns) to suffix
// (trailing metadata: tags, a date, a filename, ...), truncating prefix
// with a "…" — bg-styled like every other segment on the row, via
// ansi.Truncate, which is ANSI/wide-rune aware so it never cuts an
// escape code or a multi-byte glyph in half — if the combined line
// would exceed width. Only prefix ever gives way: a long title alone
// shouldn't make trailing metadata disappear off the right edge with no
// indication anything was cut, so suffix is always shown in full (or,
// in the degenerate case where even suffix alone exceeds width, prefix
// simply vanishes rather than also being cut). width <= 0 (m.width
// unset — e.g. before the first WindowSizeMsg, or in a test that never
// sets it) skips truncation entirely, matching padLineToWidth's own
// convention.
func fitRowLine(prefix, suffix string, width int, bg lipgloss.TerminalColor) string {
	if width <= 0 {
		return prefix + suffix
	}
	full := prefix + suffix
	if lipgloss.Width(full) <= width {
		return full
	}
	avail := width - lipgloss.Width(suffix)
	if avail < 0 {
		avail = 0
	}
	return ansi.Truncate(prefix, avail, bgSpan(bg, "…")) + suffix
}

// maxTagsSuffixWidth caps how wide the ":tag1:tag2:...:" portion of a
// row's suffix (see renderTagsSuffix) is allowed to be, ellipsized
// beyond that. fitRowLine always keeps suffix intact and shrinks prefix
// (the title) instead — right for an ordinary tag or two, but a
// meeting's attendee tags (see calendarDisplayTags) can run well past a
// dozen for a large invite list, and left uncapped that would consume
// the whole row and crowd the title down to nothing. Fixed rather than
// proportional to m.width: the point is to guarantee the title a usable
// share of the row on any reasonably sized terminal, not to let a wide
// terminal grow the tag list without bound either.
const maxTagsSuffixWidth = 40

// renderTagsSuffix builds the "  :tag1:tag2:...:" suffix shared by
// every row renderer that shows a headline's tags (the outline's own
// default row, agenda items, calendar events and their linked items,
// and meeting-tags.org records) — capped to maxTagsSuffixWidth (see
// its own doc comment) rather than left to fitRowLine's usual
// "suffix always wins" rule. tags is the tag list to render: h.Tags for
// every caller except the calendar event row, which unions in
// meeting-tags.org tags too (see calendarDisplayTags). Empty if tags is.
func (m Model) renderTagsSuffix(h *org.Headline, tags []string, query string, bg lipgloss.TerminalColor) string {
	if len(tags) == 0 {
		return ""
	}
	joined := ansi.Truncate(":"+strings.Join(tags, ":")+":", maxTagsSuffixWidth, "…")
	return bgSpan(bg, "  ") + m.highlightMatches(joined, query, m.fadeIfImmutable(m.tagStyle(), h).Background(bg))
}

// Default gutter icon characters/colors — used whenever the
// corresponding Model field is unset, which includes both a Model built
// by New() with no matching WithXxxIcon option applied, and a Model
// built as a bare zero value (as plenty of tests do) without going
// through New() at all. See dirtyIcon/dirtyColor and friends, above, and
// orDefault, below. Colors are drawn from the wildcharm dark palette
// (see the package doc comment above the style vars, near fileStyle) —
// dirty's blue matches its "Changed" highlight, mark/review share its
// magenta Identifier/Question color, lock takes its orange Type/Warning
// color, and meeting takes its blue Statement/Directory color.
const (
	defaultDirtyIcon    = "+"
	defaultDirtyColor   = "#0087d7"
	defaultMarkColor    = "#ff87ff"
	defaultReviewIcon   = "●"
	defaultReviewColor  = "#ff87ff"
	defaultLockIcon     = "◆"
	defaultLockColor    = "#ffaf00"
	defaultMeetingIcon  = "▣"
	defaultMeetingColor = "#00afff"
)

// orDefault returns s, or def if s is empty — used to fall back to a
// gutter icon's built-in character/color when the Model field backing it
// was never set (see the defaultXxx constants, above).
func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// fadeIfImmutable applies a faint (dim) rendering attribute to style
// when h is locked by :format-links, on top of whatever foreground
// color style already carries — so a locked entry's keyword, title,
// tags, and timestamp all wash out together, distinguishing it at a
// glance from an ordinary row, without needing a special case for every
// possible keyword color. Unchanged otherwise.
func (m Model) fadeIfImmutable(style lipgloss.Style, h *org.Headline) lipgloss.Style {
	if m.immutable[h] {
		return style.Faint(true)
	}
	return style
}
