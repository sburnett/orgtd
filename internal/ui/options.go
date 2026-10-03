package ui

// Option customizes a Model at construction time. See New.
type Option func(*Model)

// WithURLFormatter enables passing bare URLs, found in text edited via the
// external editor, through cmd — an external program invoked as
// `cmd <url>`, expected to print an org-mode link (e.g.
// "[[https://foo.com][Foo Site]]") to stdout. URLs already inside an
// org-mode link are left alone. A blank cmd disables the feature (the
// default).
func WithURLFormatter(cmd string) Option {
	return func(m *Model) { m.urlFormatterCmd = cmd }
}

// WithFormatLinksURLFormatter sets the external program :format-links
// invokes in batch mode — called with no trailing URL argument, it's
// expected to read URLs one per line from stdin and print the same
// number of formatted lines to stdout (see extprog.RunBatchFormatter). A
// blank cmd (the default) means :format-links uses urlFormatterCmd
// instead, same as everything else — see formatLinksFormatterCmd.
func WithFormatLinksURLFormatter(cmd string) Option {
	return func(m *Model) { m.formatLinksURLFormatterCmd = cmd }
}

// WithURLFormatterPrefixes adds extra bare-URL prefixes formatURLs
// recognizes beyond the built-in http:// and https:// — e.g. "bit.ly/"
// for a shortlink service, or "go/" for an internal go-link convention.
// Each is matched only at a word boundary (see extprog.BareURLRegexp), so
// a short prefix like "go/" doesn't also match mid-word. Has no effect
// unless WithURLFormatter is also set, since there'd be nothing to
// format a bare URL into otherwise.
func WithURLFormatterPrefixes(prefixes []string) Option {
	return func(m *Model) { m.urlFormatterPrefixes = prefixes }
}

// WithEditor overrides $EDITOR as the external editor orgtd launches for
// `i` and file edits (see editorCommand). cmd == "" leaves $EDITOR as the
// source (the default).
func WithEditor(cmd string) Option {
	return func(m *Model) { m.editorOverride = cmd }
}

// WithAgendaDays sets how many days ahead of today the agenda view's
// "Upcoming" section covers. days <= 0 is treated as the default (14).
func WithAgendaDays(days int) Option {
	return func(m *Model) {
		if days > 0 {
			m.agendaDays = days
		}
	}
}

// WithInboxFile sets the base file name :clarify treats as the inbox
// (e.g. "inbox.org", the default). name == "" is treated as the
// default.
func WithInboxFile(name string) Option {
	return func(m *Model) {
		if name != "" {
			m.inboxFile = name
		}
	}
}

// WithCalendarFile sets the base file name excluded from the outline
// view and shown instead (grouped by day) in calendarView — the file
// :sync-calendar writes (e.g. "calendar.org", the default). name == "" is
// treated as the default.
func WithCalendarFile(name string) Option {
	return func(m *Model) {
		if name != "" {
			m.calendarFile = name
		}
	}
}

// WithMeetingTagsFile sets the base file name excluded from the outline
// view and shown instead (as an editable outline of its own) in
// meetingTagsView — the file "gt" on a calendar entry writes durable
// meeting-tag records to (e.g. "meeting-tags.org", the default). name ==
// "" is treated as the default.
func WithMeetingTagsFile(name string) Option {
	return func(m *Model) {
		if name != "" {
			m.meetingTagsFile = name
		}
	}
}

// WithHideDoneAfterHours turns on hiding DONE/CANCELLED headlines (and
// their whole subtrees — see appendHeadlines) whose CLOSED timestamp is
// more than hours in the past from the outline view; hours <= 0 keeps
// the built-in threshold (24) rather than turning filtering on with a
// meaningless one. Not passing this option at all leaves filtering off
// from the start — the zero-value Model default, which is what every
// caller that doesn't care about this feature (chiefly tests) relies
// on — even though hideDoneAfterHours itself still carries a default
// value either way. Once on, filtering can still be toggled off
// entirely at runtime with :toggledone, and back on again the same way.
func WithHideDoneAfterHours(hours int) Option {
	return func(m *Model) {
		if hours > 0 {
			m.hideDoneAfterHours = hours
		}
		m.hideDoneEnabled = true
	}
}

// WithDebug records whether main.go turned on debug logging, purely so
// the :config view can report it accurately — the Model doesn't consult
// this for anything else, since logging itself is set up once, globally,
// before the Model even exists (see main.go).
func WithDebug(enabled bool) Option {
	return func(m *Model) { m.debug = enabled }
}

// WithReadme supplies README.md's content for :help to show — the UI
// package has no file of its own to read it from at runtime (it's
// embedded into the binary elsewhere, at the module root, since
// go:embed can't reach outside that file's own directory; see
// cmd/orgtd/main.go). Empty means :help has nothing to show.
func WithReadme(text string) Option {
	return func(m *Model) { m.readme = text }
}

// WithGcalOAuthClient sets the Google OAuth2 installed-app client
// :sync-calendar authenticates with. Either being empty (the default)
// means :sync-calendar isn't configured — see startSyncCalendar.
func WithGcalOAuthClient(clientID, clientSecret string) Option {
	return func(m *Model) { m.gcalOAuthClientID, m.gcalOAuthClientSecret = clientID, clientSecret }
}

// WithGcalCalendarIDs sets which Google Calendar IDs :sync-calendar
// syncs, e.g. "primary" or an email address for a secondary/shared
// calendar. Default (if this option is never applied): ["primary"] —
// see New.
func WithGcalCalendarIDs(ids []string) Option {
	return func(m *Model) { m.gcalCalendarIDs = ids }
}

// WithGcalSyncWindow sets how many days into the past/future
// :sync-calendar's sync window extends around now. Defaults (if this
// option is never applied): 1/14 — see New.
func WithGcalSyncWindow(pastDays, futureDays int) Option {
	return func(m *Model) { m.gcalSyncPastDays, m.gcalSyncFutureDays = pastDays, futureDays }
}

// WithGcalAttendeeTagDomains restricts the "@username" attendee tags
// :sync-calendar gives a synced event to attendees whose email ends in
// one of domains, e.g. ["example.com"] to tag only coworkers. Default
// (if this option is never applied, or domains is empty): no
// restriction — every confirmed attendee is tagged.
func WithGcalAttendeeTagDomains(domains []string) Option {
	return func(m *Model) { m.gcalAttendeeTagDomains = domains }
}

// WithGcalAttendeeIgnorePatterns excludes any attendee whose email
// matches one of patterns — each a filepath.Match-style glob ("*"
// matches any run of characters, "?" a single one), compared
// case-insensitively against the whole address — from consideration
// entirely, before WithGcalAttendeeTagDomains is even checked, e.g.
// ["c_*@*"] to drop the synthetic "c_...@..." attendees Google Calendar
// attaches to represent a resource/room booking. Default (if this
// option is never applied, or patterns is empty): no exclusions.
func WithGcalAttendeeIgnorePatterns(patterns []string) Option {
	return func(m *Model) { m.gcalAttendeeIgnorePatterns = patterns }
}

// WithDirtyIcon sets the character and color of the gutter marker shown
// on any entry with unsaved changes (default: "+", color "9"). An empty
// icon or color leaves that half at its default, so the config file's
// [icons] section can set just one of the two.
func WithDirtyIcon(icon, color string) Option {
	return func(m *Model) {
		if icon != "" {
			m.dirtyIcon = icon
		}
		if color != "" {
			m.dirtyColor = color
		}
	}
}

// WithMarkColor sets the color of a vim-style mark's letter ("m<letter>"),
// both in the gutter and pinned in the info buffer at the bottom of the
// screen (default: "212"). There's no matching icon option — a mark's
// glyph is always the letter it was set with, not a fixed character.
func WithMarkColor(color string) Option {
	return func(m *Model) {
		if color != "" {
			m.markColor = color
		}
	}
}

// WithClarifyIcon sets the character and color of the marker on
// :clarify's pinned inbox item, both in the gutter and pinned in the
// info buffer at the bottom of the screen (default: "●", color "212").
// An empty icon or color leaves that half at its default.
func WithClarifyIcon(icon, color string) Option {
	return func(m *Model) {
		if icon != "" {
			m.clarifyIcon = icon
		}
		if color != "" {
			m.clarifyColor = color
		}
	}
}

// WithLockIcon sets the character and color of the gutter marker on an
// entry currently locked by an in-flight :format-links batch (default:
// "◆", color "208"). An empty icon or color leaves that half at its
// default.
func WithLockIcon(icon, color string) Option {
	return func(m *Model) {
		if icon != "" {
			m.lockIcon = icon
		}
		if color != "" {
			m.lockColor = color
		}
	}
}

// WithMeetingIcon sets the character and color of the gutter marker on
// an entry attached to a calendar meeting via "gM" (default: "▣", color
// "39"). An empty icon or color leaves that half at its default.
func WithMeetingIcon(icon, color string) Option {
	return func(m *Model) {
		if icon != "" {
			m.meetingIcon = icon
		}
		if color != "" {
			m.meetingColor = color
		}
	}
}
