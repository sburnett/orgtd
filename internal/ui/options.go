package ui

import (
	"time"

	"github.com/sburnett/orgtd/internal/config"
)

// Option customizes a Model at construction time. See New.
type Option func(*Model)

// WithConfig installs a fully resolved set of settings — what cmd/orgtd
// builds from flags, the config file and the built-in defaults (see
// internal/config) — in one step, replacing the defaults New starts with.
// It also turns on hiding of stale DONE/CANCELLED items, as running the
// real program always does. The other With... options below override
// individual settings on top of New's defaults, which is mostly what
// tests want.
func WithConfig(cfg config.Config) Option {
	return func(m *Model) {
		m.cfg = cfg
		m.hideDoneEnabled = true
	}
}

// WithURLFormatter enables passing bare URLs, found in text edited via the
// external editor, through cmd — an external program invoked as
// `cmd <url>`, expected to print an org-mode link (e.g.
// "[[https://foo.com][Foo Site]]") to stdout. URLs already inside an
// org-mode link are left alone. A blank cmd disables the feature (the
// default).
func WithURLFormatter(cmd string) Option {
	return func(m *Model) { m.cfg.URLFormatter = cmd }
}

// WithFormatLinksURLFormatter sets the external program :format-links
// invokes in batch mode — called with no trailing URL argument, it's
// expected to read URLs one per line from stdin and print the same
// number of formatted lines to stdout (see extprog.RunBatchFormatter). A
// blank cmd (the default) means :format-links uses urlFormatterCmd
// instead, same as everything else — see formatLinksFormatterCmd.
func WithFormatLinksURLFormatter(cmd string) Option {
	return func(m *Model) { m.cfg.FormatLinksURLFormatter = cmd }
}

// WithURLFormatterPrefixes adds extra bare-URL prefixes formatURLs
// recognizes beyond the built-in http:// and https:// — e.g. "bit.ly/"
// for a shortlink service, or "go/" for an internal go-link convention.
// Each is matched only at a word boundary (see extprog.BareURLRegexp), so
// a short prefix like "go/" doesn't also match mid-word. Has no effect
// unless WithURLFormatter is also set, since there'd be nothing to
// format a bare URL into otherwise.
func WithURLFormatterPrefixes(prefixes []string) Option {
	return func(m *Model) { m.cfg.URLFormatterPrefixes = prefixes }
}

// WithEditor overrides $EDITOR as the external editor orgtd launches for
// `i` and file edits (see editorCommand). cmd == "" leaves $EDITOR as the
// source (the default).
func WithEditor(cmd string) Option {
	return func(m *Model) { m.cfg.Editor = cmd }
}

// WithAgendaDays sets how many days ahead of today the agenda view's
// "Upcoming" section covers. days <= 0 is treated as the default (14).
func WithAgendaDays(days int) Option {
	return func(m *Model) {
		if days > 0 {
			m.cfg.AgendaWindowDays = days
		}
	}
}

// WithInboxFile sets the base file name :review treats as the inbox
// (e.g. "inbox.org", the default). name == "" is treated as the
// default.
func WithInboxFile(name string) Option {
	return func(m *Model) {
		if name != "" {
			m.cfg.InboxFile = name
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
			m.cfg.CalendarFile = name
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
			m.cfg.HideDoneAfterHours = hours
		}
		m.hideDoneEnabled = true
	}
}

// WithDebug records whether main.go turned on debug logging, purely so
// the :config view can report it accurately — the Model doesn't consult
// this for anything else, since logging itself is set up once, globally,
// before the Model even exists (see main.go).
func WithDebug(enabled bool) Option {
	return func(m *Model) { m.cfg.Debug = enabled }
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
	return func(m *Model) {
		m.cfg.Gcalsync.OAuthClientID, m.cfg.Gcalsync.OAuthClientSecret = clientID, clientSecret
	}
}

// WithGcalAttendeeTagDomains restricts the "@username" attendee tags
// :sync-calendar gives a synced event to attendees whose email ends in
// one of domains, e.g. ["example.com"] to tag only coworkers. Default
// (if this option is never applied, or domains is empty): no
// restriction — every confirmed attendee is tagged.
func WithGcalAttendeeTagDomains(domains []string) Option {
	return func(m *Model) { m.cfg.Gcalsync.AttendeeTagDomains = domains }
}

// WithDirtyIcon sets the character and color of the gutter marker shown
// on any entry with unsaved changes (default: "+", color "9"). An empty
// icon or color leaves that half at its default, so the config file's
// [icons] section can set just one of the two.
func WithDirtyIcon(icon, color string) Option {
	return func(m *Model) {
		if icon != "" {
			m.cfg.Icons.DirtyIcon = icon
		}
		if color != "" {
			m.cfg.Icons.DirtyColor = color
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
			m.cfg.Icons.MarkColor = color
		}
	}
}

// WithReviewIcon sets the character and color of the marker on
// :review's pinned inbox item, both in the gutter and pinned in the
// info buffer at the bottom of the screen (default: "●", color "212").
// An empty icon or color leaves that half at its default.
func WithReviewIcon(icon, color string) Option {
	return func(m *Model) {
		if icon != "" {
			m.cfg.Icons.ReviewIcon = icon
		}
		if color != "" {
			m.cfg.Icons.ReviewColor = color
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
			m.cfg.Icons.LockIcon = icon
		}
		if color != "" {
			m.cfg.Icons.LockColor = color
		}
	}
}

// WithMeetingIcon sets the character and color of the gutter marker on
// an entry attached to a calendar meeting via "gM" (default: "▣", color
// "39"). An empty icon or color leaves that half at its default.
func WithMeetingIcon(icon, color string) Option {
	return func(m *Model) {
		if icon != "" {
			m.cfg.Icons.MeetingIcon = icon
		}
		if color != "" {
			m.cfg.Icons.MeetingColor = color
		}
	}
}

// WithClock replaces the real clock with now, for everything
// time-dependent in the UI (see Model.now) — what lets a test run at a
// fixed moment instead of building every fixture relative to the real one.
func WithClock(now func() time.Time) Option {
	return func(m *Model) { m.clock = now }
}
