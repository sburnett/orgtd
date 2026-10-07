package ui

import (
	"fmt"

	"github.com/sburnett/orgtd/internal/org"
	"github.com/sburnett/orgtd/internal/workspace"
)

// viewKind identifies a view: one way of listing the workspace's entries
// (or, for the read-only views, some other text) as m.rows. The behavior
// that differs per view lives in its viewSpec, in the viewSpecs table
// below — not in switches scattered around the package.
type viewKind int

const (
	outlineView viewKind = iota
	agendaView
	clarifyView
	configView
	logView
	diffView
	helpView
	calendarView
	meetingTagsView
	tagsView
	referenceView

	numViews // not a view: the number of views, sizing viewSpecs
)

// viewSpec describes everything that varies from one view to the next.
// Only build is required; every other field has a sensible default, noted
// on it.
//
// To add a view: add a viewKind constant above, give it an entry in
// viewSpecs (in init, below), and write its build function. Its
// ":command", status-line label, empty message, Tab completion and
// Enter behavior all come from the spec.
type viewSpec struct {
	// command is the ":" command that opens the view (":agenda", ...).
	command string

	// label is the view's name in the status line. Empty means the
	// workspace directory, which is what the outline-like views show.
	label string

	// build appends the view's rows to dst. ignoreFold asks it to descend
	// into every fold as if everything were expanded, which search uses to
	// scan text the user can't currently see (see searchRows); it only
	// matters to views with folds.
	build func(m *Model, dst *[]row, ignoreFold bool)

	// folds reports whether the view's rows honor m.collapsed (and so can
	// hide matches search needs to find and reveal — see searchRows and
	// revealRow).
	folds bool

	// outlineRows reports whether the view's rows are the full outline,
	// every loaded file in full (outlineView itself, and clarifyView,
	// which merely adds an info-buffer panel on top of the same rows).
	// editor.go uses this to decide whether a freshly captured entry is
	// already visible where the cursor is, or needs an explicit switch to
	// the outline to bring it into focus.
	outlineRows bool

	// empty is the message shown when the view has no rows. Nil means the
	// generic "No org files found."
	empty func(m *Model) string

	// enter is what Enter does on the cursor's row. Nil means nothing.
	enter func(m *Model)

	// open is what the view's ":command" does. Nil means switchToView —
	// views that need to prepare something first (clarify pins its
	// target, calendar lands on the current meeting, diff runs git) set
	// it.
	open func(m *Model)

	// rebuildOnResize is true for a view whose rows are pre-wrapped to the
	// terminal width when built (:help's glamour output), so a resize
	// while it's open needs a rebuild rather than just a new page size.
	rebuildOnResize bool

	// info returns extra lines for the info buffer while the view is
	// showing (clarify's pinned inbox item). Nil means none.
	info func(m *Model) []string
}

// viewSpecs is indexed by viewKind. Populated in init rather than as a
// var initializer because the functions it refers to reach rebuildRows,
// which reads viewSpecs — Go rejects that as an initialization cycle.
var viewSpecs [numViews]viewSpec

func init() {
	viewSpecs = [numViews]viewSpec{
		outlineView: {
			command:     "outline",
			build:       (*Model).appendOutlineRows,
			folds:       true,
			outlineRows: true,
		},
		clarifyView: {
			command:     "clarify",
			build:       (*Model).appendOutlineRows,
			folds:       true,
			outlineRows: true,
			open:        (*Model).enterClarifyView,
			info:        (*Model).clarifyInfoLines,
		},
		agendaView: {
			command: "agenda",
			label:   "agenda",
			build:   flat((*Model).appendAgendaRows),
			empty: func(m *Model) string {
				return fmt.Sprintf("Nothing due in the next %d days. :outline to go back.", m.cfg.AgendaWindowDays)
			},
			enter: (*Model).jumpToSource,
		},
		calendarView: {
			command: "calendar",
			label:   "calendar",
			build:   (*Model).appendCalendarRows,
			folds:   true,
			empty:   func(*Model) string { return "No calendar events found. :outline to go back." },
			open:    (*Model).enterCalendarView,
			enter:   (*Model).jumpToLinkedSource,
		},
		meetingTagsView: {
			command: "meeting-tags",
			label:   "meeting-tags",
			build:   (*Model).appendMeetingTagsRows,
			folds:   true,
			empty:   func(*Model) string { return "No meeting tags yet. :outline to go back." },
		},
		tagsView: {
			command: "tags",
			label:   "tags",
			build:   flat((*Model).appendTagsRows),
			empty:   func(*Model) string { return "No tags found. :outline to go back." },
			enter:   (*Model).jumpToSource,
		},
		referenceView: {
			command: "reference",
			label:   "reference",
			build:   (*Model).appendReferenceRows,
			folds:   true,
			empty: func(m *Model) string {
				return fmt.Sprintf("No org files found in %s/. :outline to go back.", workspace.ReferenceDir)
			},
		},
		configView: {
			command: "config",
			label:   "config",
			build:   flat((*Model).appendConfigRows),
		},
		logView: {
			command: "log",
			label:   "log",
			build:   flat((*Model).appendLogRows),
		},
		diffView: {
			command: "diff",
			label:   "diff",
			build:   flat((*Model).appendDiffRows),
			open:    (*Model).showDiff,
		},
		helpView: {
			command:         "help",
			label:           "help",
			build:           flat((*Model).appendHelpRows),
			rebuildOnResize: true,
		},
	}
}

// flat adapts a row builder that has no folds to honor to viewSpec.build's
// signature.
func flat(f func(m *Model, dst *[]row)) func(m *Model, dst *[]row, ignoreFold bool) {
	return func(m *Model, dst *[]row, _ bool) { f(m, dst) }
}

// spec returns the viewSpec for the view currently showing.
func (m *Model) spec() *viewSpec {
	return &viewSpecs[m.view]
}

// viewForCommand returns the view whose ":command" is cmd, or nil if cmd
// isn't a view's command.
func viewForCommand(cmd string) (viewKind, *viewSpec) {
	for k := range viewSpecs {
		if viewSpecs[k].command == cmd {
			return viewKind(k), &viewSpecs[k]
		}
	}
	return 0, nil
}

// openView runs a view's ":command": its own open hook, or a plain
// switchToView.
func (m *Model) openView(k viewKind) {
	if open := viewSpecs[k].open; open != nil {
		open(m)
		return
	}
	m.switchToView(k)
}

// appendOutlineRows appends the full outline: every loaded file (except
// the calendar and meeting-tags files, which have views of their own)
// followed by its headlines. Reference files have :reference instead.
// Shared by the outline and clarify views, and by search's fully expanded
// copy of either (ignoreFold).
func (m *Model) appendOutlineRows(dst *[]row, ignoreFold bool) {
	for _, f := range m.ws.Files {
		if m.isNonOutlineFile(f) {
			continue
		}
		*dst = append(*dst, row{kind: rowFile, file: f})
		m.appendHeadlines(dst, f.Headlines, ignoreFold)
	}
}

// appendReferenceRows appends the reference view's rows: the outline's
// own shape (a file header row, then its foldable headlines), but over
// only the files in reference/. Task state doesn't apply to reference
// material, so see referenceView's special cases elsewhere: no stale-DONE
// hiding (hiddenAsStaleDone) and keyword/planning left unstyled
// (renderRowWithBg).
func (m *Model) appendReferenceRows(dst *[]row, ignoreFold bool) {
	for _, f := range m.ws.Files {
		if !m.ws.IsReference(f) {
			continue
		}
		*dst = append(*dst, row{kind: rowFile, file: f})
		m.appendHeadlines(dst, f.Headlines, ignoreFold)
	}
}

// isNonOutlineFile reports whether f is one the plain outline leaves out
// because another view shows it: the calendar file (:calendar), the
// meeting-tags file (:meeting-tags), or reference material (:reference).
func (m *Model) isNonOutlineFile(f *org.File) bool {
	return m.ws.IsReference(f) || m.isNamedFile(f, m.cfg.CalendarFile) || m.isNamedFile(f, m.cfg.MeetingTagsFile)
}
