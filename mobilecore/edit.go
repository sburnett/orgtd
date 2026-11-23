package mobilecore

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/sburnett/orgtd/internal/capturequeue"
	"github.com/sburnett/orgtd/internal/gitsync"
	"github.com/sburnett/orgtd/internal/org"
	"github.com/sburnett/orgtd/internal/orgdate"
)

// SetState changes the TODO keyword of the entry entryID (an entry DTO's
// ID, from the current views) to keyword ("" for none), as desktop "r"
// does: a done state stamps CLOSED, and completing a repeating item
// advances its dates instead. The change is queued and committed on
// local "mobile" at once (no network), and replayed on every rebuild
// until it's merged — by the desktop, if the entry changes there first
// (see Status's waiting list). Refused for reference material,
// for a keyword that isn't configured, and for a file .gitattributes
// gives merge=union (a union merge would keep both versions of the
// edited line). A non-blank note is logged as a closing note if the
// change closes the entry, as desktop "r" asks for one, and ignored
// otherwise.
func SetState(groupDir, entryID, keyword, note string, nowUnix int64, authorName, authorEmail string) error {
	mu.Lock()
	defer mu.Unlock()
	l, err := load(groupDir)
	if err != nil {
		return err
	}
	_, f, h, err := l.lookup(entryID)
	if err != nil {
		return err
	}
	rel := l.rel(f)
	if l.ws.IsReference(f) {
		return errors.New("mobilecore: reference material has no TODO state")
	}
	if keyword != "" {
		known := false
		for _, k := range l.cfg.Keywords {
			known = known || k.Name == keyword
		}
		if !known {
			return fmt.Errorf("mobilecore: %q isn't a configured keyword", keyword)
		}
	}
	if keyword == h.Keyword {
		return nil
	}
	if gitsync.UnionMerged(repoDir(groupDir), rel) {
		return fmt.Errorf("%s has merge=union in .gitattributes, which would duplicate entries whose state changes; remove that line (on your computer) to change states there from the phone", rel)
	}
	op := gitsync.NewStateOp(rel, h, keyword, now(nowUnix))
	op.Note = strings.TrimSpace(note)
	if _, err := capturequeue.AddOp(opsPath(groupDir), op); err != nil {
		return err
	}
	return rebuild(groupDir, authorName, authorEmail)
}

// entryForm is EditEntry's input: the edit form's fields, the same shape
// as a capture's (capture.Capture).
type entryForm struct {
	Keyword   string   `json:"keyword"`
	Title     string   `json:"title"`
	Body      string   `json:"body"`
	Tags      []string `json:"tags"`
	Scheduled string   `json:"scheduled"`
	Deadline  string   `json:"deadline"`
}

// EditEntry edits the entry entryID (an entry DTO's ID) to the form in
// editJSON: keyword, title, body (dedented, as entry.Body), tags,
// scheduled and deadline (as entry.ScheduledInput/DeadlineInput). Only
// the fields that differ from the entry are changed, so an untouched
// date keeps its repeater and time as written; a changed one keeps its
// repeater. Like SetState, the edit is queued, committed locally at once,
// replayed on every rebuild, and left for the desktop to merge if the
// entry changes there first. A still-pending capture is edited with
// UpdatePendingCapture instead.
func EditEntry(groupDir, entryID, editJSON string, nowUnix int64, authorName, authorEmail string) error {
	mu.Lock()
	defer mu.Unlock()
	var form entryForm
	if err := json.Unmarshal([]byte(editJSON), &form); err != nil {
		return fmt.Errorf("mobilecore: bad edit JSON: %w", err)
	}
	if strings.TrimSpace(form.Title) == "" {
		return errors.New("mobilecore: title is required")
	}
	l, err := load(groupDir)
	if err != nil {
		return err
	}
	_, f, h, err := l.lookup(entryID)
	if err != nil {
		return err
	}
	rel := l.rel(f)
	keyword := form.Keyword
	if keyword != h.Keyword {
		if l.ws.IsReference(f) {
			return errors.New("mobilecore: reference material has no TODO state")
		}
		if keyword != "" && !l.knownKeyword(keyword) {
			return fmt.Errorf("mobilecore: %q isn't a configured keyword", keyword)
		}
	}
	for _, d := range []string{form.Scheduled, form.Deadline} {
		if strings.TrimSpace(d) == "" {
			continue
		}
		if _, _, err := orgdate.ParseFlexible(strings.TrimSpace(d)); err != nil {
			return err
		}
	}

	var e capturequeue.EntryEdit
	changed := keyword != h.Keyword
	if title := strings.Join(strings.Fields(form.Title), " "); title != h.Title {
		e.Title, changed = &title, true
	}
	if !sameTags(form.Tags, h.Tags) {
		tags := form.Tags
		e.Tags, changed = &tags, true
	}
	if body := strings.TrimRight(form.Body, " \t\n"); body != strings.TrimRight(dedentBody(h), " \t\n") {
		e.Body, changed = &body, true
	}
	if s := strings.TrimSpace(form.Scheduled); s != dateInput(h.Scheduled) {
		e.Scheduled, changed = &s, true
	}
	if d := strings.TrimSpace(form.Deadline); d != dateInput(h.Deadline) {
		e.Deadline, changed = &d, true
	}
	if !changed {
		return nil
	}
	if gitsync.UnionMerged(repoDir(groupDir), rel) {
		return fmt.Errorf("%s has merge=union in .gitattributes, which would duplicate edited entries; remove that line (on your computer) to edit entries there from the phone", rel)
	}
	op := gitsync.NewStateOp(rel, h, keyword, now(nowUnix))
	op.Edit = &e
	if _, err := capturequeue.AddOp(opsPath(groupDir), op); err != nil {
		return err
	}
	return rebuild(groupDir, authorName, authorEmail)
}

// AddEntry adds a new entry, from the capture form in captureJSON (as
// Capture takes it), to file (relative to the repository root): first or,
// with last, last among the children of the entry parentID (an entry
// DTO's ID), or among the file's top-level entries when parentID is "".
// It is written as a capture is (with CREATED), at its parent's level
// plus one. Like SetState, the addition is queued, committed locally at
// once, replayed on every rebuild, and left for the desktop to merge if
// the parent changes there first. Returns the new entry's ID.
func AddEntry(groupDir, file, parentID string, last bool, captureJSON string, nowUnix int64, authorName, authorEmail string) (string, error) {
	mu.Lock()
	defer mu.Unlock()
	c, err := decodeCapture(groupDir, captureJSON)
	if err != nil {
		return "", err
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = now(nowUnix)
	}
	c.ID = ""
	l, err := load(groupDir)
	if err != nil {
		return "", err
	}
	var f *org.File
	var parent *org.Headline
	var pid entryID
	if parentID != "" {
		if pid, f, parent, err = l.lookup(parentID); err != nil {
			return "", err
		}
	} else {
		for _, cand := range l.ws.Files {
			if l.rel(cand) == file {
				f = cand
			}
		}
		if f == nil {
			return "", fmt.Errorf("mobilecore: no file %q", file)
		}
	}
	rel := l.rel(f)
	if rel != file {
		return "", fmt.Errorf("mobilecore: entry %q isn't in %q", parentID, file)
	}
	ref := l.ws.IsReference(f)
	if !ref && !l.isOutlineFile(f) {
		return "", fmt.Errorf("mobilecore: %s isn't part of the outline", rel)
	}
	if ref && c.Keyword != "" {
		return "", errors.New("mobilecore: reference material has no TODO state")
	}
	siblings, newID := f.Headlines, topLevelID(rel, 0)
	if parent != nil {
		siblings, newID = parent.Children, pid.child(0)
	}
	if last {
		newID = newID.sibling(len(siblings))
	}
	op := gitsync.NewInsertOp(rel, parent, last, c)
	if _, err := capturequeue.AddOp(opsPath(groupDir), op); err != nil {
		return "", err
	}
	return newID.String(), rebuild(groupDir, authorName, authorEmail)
}

// DeleteEntry deletes the entry entryID (an entry DTO's ID) with
// everything under it, as desktop "dd" does. Like SetState, the delete
// is queued, committed locally at once, replayed on every rebuild, and
// left for the desktop to merge if the entry or anything under it
// changes there first. A still-pending capture is deleted with DeletePendingCapture
// instead.
func DeleteEntry(groupDir, entryID string, nowUnix int64, authorName, authorEmail string) error {
	mu.Lock()
	defer mu.Unlock()
	l, err := load(groupDir)
	if err != nil {
		return err
	}
	_, f, h, err := l.lookup(entryID)
	if err != nil {
		return err
	}
	rel := l.rel(f)
	if !l.ws.IsReference(f) && !l.isOutlineFile(f) {
		return fmt.Errorf("mobilecore: %s isn't part of the outline", rel)
	}
	if gitsync.UnionMerged(repoDir(groupDir), rel) {
		return fmt.Errorf("%s has merge=union in .gitattributes, which would bring deleted entries back; remove that line (on your computer) to delete entries there from the phone", rel)
	}
	if _, err := capturequeue.AddOp(opsPath(groupDir), gitsync.NewDeleteOp(rel, h, now(nowUnix))); err != nil {
		return err
	}
	return rebuild(groupDir, authorName, authorEmail)
}

// ShiftEntry moves the entry entryID (an entry DTO's ID), with its
// subtree, one level deeper (delta 1: the last child of its previous
// sibling) or shallower (delta -1: the next sibling of its parent), as
// desktop ">>"/"<<" do. Like SetState, the move is queued, committed
// locally at once, replayed on every rebuild, and left for the desktop
// to merge if the entry (or, for a demote, its previous sibling) changes
// there first.
// Returns the entry's new ID.
func ShiftEntry(groupDir, entryID string, delta int, nowUnix int64, authorName, authorEmail string) (string, error) {
	mu.Lock()
	defer mu.Unlock()
	if delta != 1 && delta != -1 {
		return "", fmt.Errorf("mobilecore: can only move one level at a time, not %d", delta)
	}
	l, err := load(groupDir)
	if err != nil {
		return "", err
	}
	id, f, h, err := l.lookup(entryID)
	if err != nil {
		return "", err
	}
	rel := l.rel(f)
	if !l.ws.IsReference(f) && !l.isOutlineFile(f) {
		return "", fmt.Errorf("mobilecore: %s isn't part of the outline", rel)
	}
	if gitsync.UnionMerged(repoDir(groupDir), rel) {
		return "", fmt.Errorf("%s has merge=union in .gitattributes, which would duplicate moved entries; remove that line (on your computer) to move entries there from the phone", rel)
	}
	index := id.index()
	path := gitsync.TitlePath(h)
	var to []string
	var newID string
	if delta > 0 {
		if index == 0 {
			return "", errors.New("mobilecore: there's no entry above this one to move it under")
		}
		prev := f.Headlines[index-1]
		if h.Parent != nil {
			prev = h.Parent.Children[index-1]
		}
		to = append(append(append([]string{}, path[:len(path)-1]...), prev.Title), h.Title)
		newID = id.sibling(index - 1).child(len(prev.Children)).String()
	} else {
		if h.Parent == nil {
			return "", errors.New("mobilecore: this entry is already at the top level")
		}
		to = append(append([]string{}, path[:len(path)-2]...), h.Title)
		parent, _ := id.parent()
		newID = parent.sibling(parent.index() + 1).String()
	}
	op := gitsync.NewStateOp(rel, h, h.Keyword, now(nowUnix))
	op.Move = &capturequeue.EntryMove{Delta: delta, To: to}
	if _, err := capturequeue.AddOp(opsPath(groupDir), op); err != nil {
		return "", err
	}
	return newID, rebuild(groupDir, authorName, authorEmail)
}

// MoveEntry moves the entry entryID (an entry DTO's ID), with its
// subtree, among its siblings: right before the sibling anchorID or,
// with after, right after that sibling's subtree. It's named by ID, not
// position, because the outline may be hiding closed siblings. Like
// ShiftEntry, the move is queued, committed locally at once, replayed on
// every rebuild, and left for the desktop to merge if the entry or the
// sibling changes there first. Returns the entry's new ID.
func MoveEntry(groupDir, entryID, anchorID string, after bool, nowUnix int64, authorName, authorEmail string) (string, error) {
	mu.Lock()
	defer mu.Unlock()
	l, err := load(groupDir)
	if err != nil {
		return "", err
	}
	id, f, h, err := l.lookup(entryID)
	if err != nil {
		return "", err
	}
	aid, _, anchor, err := l.lookup(anchorID)
	if err != nil {
		return "", err
	}
	if !id.isSiblingOf(aid) || anchor == h {
		return "", errors.New("mobilecore: entries can only move among their siblings")
	}
	rel := l.rel(f)
	if !l.ws.IsReference(f) && !l.isOutlineFile(f) {
		return "", fmt.Errorf("mobilecore: %s isn't part of the outline", rel)
	}
	if gitsync.UnionMerged(repoDir(groupDir), rel) {
		return "", fmt.Errorf("%s has merge=union in .gitattributes, which would duplicate moved entries; remove that line (on your computer) to move entries there from the phone", rel)
	}
	index, newIndex := id.index(), aid.index()
	if after {
		newIndex++
	}
	if index < newIndex {
		newIndex--
	}
	if newIndex == index {
		return id.String(), nil
	}
	op := gitsync.NewStateOp(rel, h, h.Keyword, now(nowUnix))
	op.Move = &capturequeue.EntryMove{Delta: 0, To: gitsync.TitlePath(h), Anchor: anchor.Title, AnchorFingerprint: gitsync.Fingerprint(anchor), After: after}
	if _, err := capturequeue.AddOp(opsPath(groupDir), op); err != nil {
		return "", err
	}
	return id.sibling(newIndex).String(), rebuild(groupDir, authorName, authorEmail)
}

// MoveEntryToFile moves the top-level entry entryID (an entry DTO's ID),
// with its subtree, to the top level of another outline or reference
// file, destFile (relative to the repository root): right before the
// top-level entry anchorID there or, with after, right after its
// subtree — or, with anchorID "", at the end of the file. Like
// MoveEntry, it's queued, and left for the desktop to merge if the entry
// or the anchor changes there first. Returns the entry's new ID.
func MoveEntryToFile(groupDir, entryID, destFile, anchorID string, after bool, nowUnix int64, authorName, authorEmail string) (string, error) {
	mu.Lock()
	defer mu.Unlock()
	l, err := load(groupDir)
	if err != nil {
		return "", err
	}
	_, f, h, err := l.lookup(entryID)
	if err != nil {
		return "", err
	}
	if h.Parent != nil {
		return "", errors.New("mobilecore: only top-level entries can move to another file")
	}
	var df *org.File
	for _, cand := range l.ws.Files {
		if l.rel(cand) == destFile {
			df = cand
		}
	}
	if df == nil {
		return "", fmt.Errorf("mobilecore: no file %q", destFile)
	}
	if df == f {
		return "", errors.New("mobilecore: the entry is already in that file")
	}
	for _, x := range []*org.File{f, df} {
		rel := l.rel(x)
		if !l.ws.IsReference(x) && !l.isOutlineFile(x) {
			return "", fmt.Errorf("mobilecore: %s isn't part of the outline", rel)
		}
		if gitsync.UnionMerged(repoDir(groupDir), rel) {
			return "", fmt.Errorf("%s has merge=union in .gitattributes, which would duplicate moved entries; remove that line (on your computer) to move entries there from the phone", rel)
		}
	}
	move := &capturequeue.EntryMove{To: []string{h.Title}, ToFile: destFile, After: after}
	index := len(df.Headlines)
	if anchorID != "" {
		aid, af, anchor, err := l.lookup(anchorID)
		if err != nil {
			return "", err
		}
		if af != df || anchor.Parent != nil {
			return "", errors.New("mobilecore: an entry moved to another file goes next to one of its top-level entries")
		}
		move.Anchor, move.AnchorFingerprint = anchor.Title, gitsync.Fingerprint(anchor)
		index = aid.index()
		if after {
			index++
		}
	}
	op := gitsync.NewStateOp(l.rel(f), h, h.Keyword, now(nowUnix))
	op.Move = move
	if _, err := capturequeue.AddOp(opsPath(groupDir), op); err != nil {
		return "", err
	}
	return topLevelID(destFile, index).String(), rebuild(groupDir, authorName, authorEmail)
}

// AddEntryBeside adds a new entry, from the capture form in captureJSON
// (as AddEntry takes it), right before the entry entryID or, with after,
// right after its subtree, at its level — as desktop "O"/"o" place one.
// Like AddEntry, it's queued, and left for the desktop to merge if that
// entry changes there first. Returns the new entry's ID.
func AddEntryBeside(groupDir, entryID string, after bool, captureJSON string, nowUnix int64, authorName, authorEmail string) (string, error) {
	mu.Lock()
	defer mu.Unlock()
	c, err := decodeCapture(groupDir, captureJSON)
	if err != nil {
		return "", err
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = now(nowUnix)
	}
	c.ID = ""
	l, err := load(groupDir)
	if err != nil {
		return "", err
	}
	id, f, h, err := l.lookup(entryID)
	if err != nil {
		return "", err
	}
	rel := l.rel(f)
	ref := l.ws.IsReference(f)
	if !ref && !l.isOutlineFile(f) {
		return "", fmt.Errorf("mobilecore: %s isn't part of the outline", rel)
	}
	if ref && c.Keyword != "" {
		return "", errors.New("mobilecore: reference material has no TODO state")
	}
	index := id.index()
	if after {
		index++
	}
	op := gitsync.NewInsertBesideOp(rel, h, after, c)
	if _, err := capturequeue.AddOp(opsPath(groupDir), op); err != nil {
		return "", err
	}
	return id.sibling(index).String(), rebuild(groupDir, authorName, authorEmail)
}

func (l *loaded) knownKeyword(name string) bool {
	for _, k := range l.cfg.Keywords {
		if k.Name == name {
			return true
		}
	}
	return false
}

func sameTags(a, b []string) bool {
	var clean []string
	for _, t := range a {
		if t = org.SanitizeTag(strings.Trim(strings.TrimSpace(t), ":")); t != "" {
			clean = append(clean, t)
		}
	}
	return strings.Join(clean, ":") == strings.Join(b, ":")
}
