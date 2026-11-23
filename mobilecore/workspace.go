package mobilecore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/sburnett/orgtd/internal/capture"
	"github.com/sburnett/orgtd/internal/capturequeue"
	"github.com/sburnett/orgtd/internal/config"
	"github.com/sburnett/orgtd/internal/gitsync"
	"github.com/sburnett/orgtd/internal/org"
	"github.com/sburnett/orgtd/internal/workspace"
)

// loaded is the working tree as the read views see it.
type loaded struct {
	cfg      config.Config
	kw       org.Keywords // cfg.OrgKeywords(), what ws was parsed with
	warnings []string
	ws       *workspace.Workspace
	// changed holds file + "\x00" + title path of every entry with a
	// queued state change, to mark it pending.
	changed map[string]bool
	// added holds the same key for every entry added on the phone
	// (AddEntry) and not merged yet.
	added map[string]bool
	// captures maps the entry ID of each still-pending capture to its
	// queue ID (see markCaptures).
	captures map[string]string
	// sent holds the queue IDs the phone has sent (sentIDs).
	sent map[string]bool
	// unsent holds the changedKey of every entry with a queued change
	// not sent yet, or waiting for the desktop to merge it (see
	// gitsync.State.Waiting).
	unsent map[string]bool
}

func changedKey(file string, path []string) string {
	return file + "\x00" + strings.Join(path, "\x00")
}

// load reads the repo-level config and loads the workspace from the
// working tree, parsed with the config's keywords.
func load(groupDir string) (*loaded, error) {
	dir := repoDir(groupDir)
	if _, err := os.Stat(dir); err != nil {
		return nil, errors.New("mobilecore: no repository cloned yet")
	}
	repoCfg, warnings, err := config.LoadRepo(dir)
	if err != nil {
		return nil, err
	}
	cfg := config.Default()
	config.Overlay(&cfg, repoCfg, config.LayerRepo, nil)
	cfg.ApplyDefaults()
	kw := cfg.OrgKeywords()
	ws, err := workspace.Load(dir, kw, cfg.OutlineIgnoreFiles...)
	if err != nil {
		return nil, err
	}
	l := &loaded{cfg: cfg, kw: kw, warnings: warnings, ws: ws, changed: map[string]bool{}, added: map[string]bool{}, captures: map[string]string{}, unsent: map[string]bool{}}
	ops, err := capturequeue.LoadOps(opsPath(groupDir))
	if err != nil {
		return nil, err
	}
	st, err := syncState(groupDir)
	if err != nil {
		return nil, err
	}
	l.sent = sentIDs(st)
	waiting := map[string]bool{}
	for _, t := range st.Waiting {
		waiting[t] = true
	}
	mark := func(key string, o capturequeue.StateOp) {
		if !l.sent[o.ID] || waiting[o.NewTitle()] {
			l.unsent[key] = true
		}
	}
	if err := l.markCaptures(groupDir); err != nil {
		return nil, err
	}
	for _, o := range ops {
		if o.Insert != nil {
			parent := o.Path
			if o.Insert.Beside && len(parent) > 0 {
				parent = parent[:len(parent)-1]
			}
			key := changedKey(o.File, append(append([]string{}, parent...), o.NewTitle()))
			l.added[key] = true
			mark(key, o)
			continue
		}
		if o.Move != nil {
			file := o.File
			if o.Move.ToFile != "" {
				file = o.Move.ToFile
			}
			key := changedKey(file, o.Move.To)
			l.changed[key] = true
			mark(key, o)
			continue
		}
		if o.Delete != nil {
			continue
		}
		if len(o.Path) == 0 {
			continue
		}
		path := append(append([]string{}, o.Path[:len(o.Path)-1]...), o.NewTitle())
		key := changedKey(o.File, path)
		l.changed[key] = true
		mark(key, o)
	}
	return l, nil
}

// markCaptures finds the still-pending captures among the inbox's
// top-level headlines, for toEntry to mark. A rebuild appends one
// headline per queued capture, but later changes can add entries among
// them, so they're matched by content, not position: the CREATED stamp
// and title the capture was rendered with (a pending capture is edited
// in the queue, so these stay its own). Matching runs from the end of
// the file, newest capture first, so an older entry that happens to
// share both isn't taken for one.
func (l *loaded) markCaptures(groupDir string) error {
	queue, err := capturequeue.Load(queuePath(groupDir))
	if err != nil || len(queue) == 0 {
		return err
	}
	for _, f := range l.ws.Files {
		if !l.ws.IsNamed(f, l.cfg.InboxFile) {
			continue
		}
		rel := l.rel(f)
		claimed := map[int]bool{}
		for q := len(queue) - 1; q >= 0; q-- {
			c := queue[q]
			stamp := capture.CreatedStamp(c.CreatedAt)
			title := strings.Join(strings.Fields(c.Title), " ")
			for i := len(f.Headlines) - 1; i >= 0; i-- {
				h := f.Headlines[i]
				if !claimed[i] && h.Title == title && h.Properties["CREATED"] == stamp {
					claimed[i] = true
					l.captures[topLevelID(rel, i).String()] = c.ID
					break
				}
			}
		}
	}
	return nil
}

func (l *loaded) rel(f *org.File) string {
	r, err := filepath.Rel(l.ws.Dir, f.Path)
	if err != nil {
		return filepath.Base(f.Path)
	}
	return filepath.ToSlash(r)
}

// toEntry converts h (at index path idx in file f) to its DTO, with
// children when withChildren is set. keep filters children (nil keeps
// all).
func (l *loaded) toEntry(f *org.File, h *org.Headline, idx []int, withChildren bool, keep func(*org.Headline) bool) entry {
	ancestors := []string{}
	for p := h.Parent; p != nil; p = p.Parent {
		ancestors = append([]string{p.Title}, ancestors...)
	}
	e := entry{
		ID:        entryID{file: l.rel(f), path: idx}.String(),
		File:      l.rel(f),
		Ancestors: ancestors,
		Level:     h.Level,
		Keyword:   h.Keyword,
		Done:      l.kw.IsDone(h.Keyword),
		Priority:  h.Priority,
		Title:     h.Title,
		Tags:      append([]string{}, h.Tags...),
		Scheduled: rawOrEmpty(h.Scheduled),
		Deadline:  rawOrEmpty(h.Deadline),
		Closed:    rawOrEmpty(h.Closed),
		Body:      dedentBody(h),

		ScheduledInput: dateInput(h.Scheduled),
		DeadlineInput:  dateInput(h.Deadline),
	}
	if id, ok := l.captures[e.ID]; ok {
		e.Pending = true
		if !l.sent[id] {
			e.CaptureID, e.Unsent = id, true
		}
	}
	key := changedKey(e.File, gitsync.TitlePath(h))
	e.PendingNew = l.added[key]
	e.PendingState = l.changed[key] || e.PendingNew
	e.Unsent = e.Unsent || l.unsent[key]
	org.Walk(h.Children, func(*org.Headline) { e.Descendants++ })
	e.Properties = []property{}
	for _, k := range h.PropertyOrder {
		e.Properties = append(e.Properties, property{Key: k, Value: h.Properties[k]})
	}
	if withChildren {
		for i, c := range h.Children {
			if keep != nil && !keep(c) {
				continue
			}
			e.Children = append(e.Children, l.toEntry(f, c, append(append([]int{}, idx...), i), true, keep))
		}
	}
	return e
}

func rawOrEmpty(ts *org.Timestamp) string {
	if ts == nil {
		return ""
	}
	return ts.Raw
}

// dedentBody is h's body with the common leading indentation removed.
func dedentBody(h *org.Headline) string {
	lines := h.TrimmedBody()
	minIndent := -1
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		n := len(line) - len(strings.TrimLeft(line, " "))
		if minIndent < 0 || n < minIndent {
			minIndent = n
		}
	}
	out := make([]string, len(lines))
	for i, line := range lines {
		if len(line) >= minIndent && minIndent > 0 {
			line = line[minIndent:]
		}
		out[i] = strings.TrimRight(line, " \t")
	}
	return strings.Join(out, "\n")
}

// locate finds h's file and index path, for an entry ID.
func (l *loaded) locate(h *org.Headline) (*org.File, []int) {
	var idx []int
	for cur := h; cur != nil; cur = cur.Parent {
		f, _, i := l.ws.Locate(cur)
		idx = append([]int{i}, idx...)
		if cur.Parent == nil {
			return f, idx
		}
	}
	return nil, nil
}

func (l *loaded) flatEntry(h *org.Headline) entry {
	f, idx := l.locate(h)
	return l.toEntry(f, h, idx, false, nil)
}

// isOutlineFile is the plain outline's file set: not reference material,
// not the calendar or meeting-tags file.
func (l *loaded) isOutlineFile(f *org.File) bool {
	return !l.ws.IsReference(f) && !l.ws.IsNamed(f, l.cfg.CalendarFile) && !l.ws.IsNamed(f, l.cfg.MeetingTagsFile)
}

// dateInputRe picks the date and optional time out of a timestamp's
// text, skipping the weekday and any repeater or warning cookie:
// "2026-10-20 Tue 14:00 +1m" → "2026-10-20", "14:00".
var dateInputRe = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})(?:\s+[^\s\d+.-]+)?(?:\s+(\d{1,2}:\d{2}))?`)

// dateInput is a SCHEDULED/DEADLINE as the edit form shows it:
// "2026-10-20" or "2026-10-20 14:00" ("" for none). Cookies aren't shown;
// an edit that changes the date keeps them (see gitsync's reschedule).
func dateInput(ts *org.Timestamp) string {
	if ts == nil {
		return ""
	}
	m := dateInputRe.FindStringSubmatch(ts.Raw)
	if m == nil {
		return ts.Raw
	}
	if m[2] != "" {
		return m[1] + " " + m[2]
	}
	return m[1]
}

// lookup parses an entry DTO's ID (see entryID) and resolves it.
func (l *loaded) lookup(s string) (entryID, *org.File, *org.Headline, error) {
	id, err := parseEntryID(s)
	if err != nil {
		return entryID{}, nil, nil, err
	}
	for _, f := range l.ws.Files {
		if l.rel(f) != id.file {
			continue
		}
		list := f.Headlines
		var h *org.Headline
		for _, i := range id.path {
			if i >= len(list) {
				return entryID{}, nil, nil, fmt.Errorf("mobilecore: no entry %q (the outline changed; refresh)", s)
			}
			h = list[i]
			list = h.Children
		}
		return id, f, h, nil
	}
	return entryID{}, nil, nil, fmt.Errorf("mobilecore: no file %q", id.file)
}
