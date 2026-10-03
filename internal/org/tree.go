package org

// Siblings identifies one ordered list of headlines in a file's tree: the
// children of Parent or, when Parent is nil, the file's top-level
// headlines. Nearly every structural edit (insert, delete, move, paste)
// is "do something at an index of one of these lists", and a headline's
// position is always one of them — so this names the list once instead of
// every caller choosing between parent.Children and file.Headlines.
//
// Parent non-nil is enough to address the list; File may be nil then. A
// Siblings with a nil Parent and a nil File is an empty list that ignores
// edits.
type Siblings struct {
	File   *File
	Parent *Headline
}

// List returns the headlines in the list. The slice is the tree's own, not
// a copy: don't modify it, and don't hold it across an edit (Splice
// replaces it).
func (s Siblings) List() []*Headline {
	switch {
	case s.Parent != nil:
		return s.Parent.Children
	case s.File != nil:
		return s.File.Headlines
	}
	return nil
}

func (s Siblings) set(list []*Headline) {
	switch {
	case s.Parent != nil:
		s.Parent.Children = list
	case s.File != nil:
		s.File.Headlines = list
	}
}

// IndexOf returns h's index in the list, or -1 if it isn't there.
func (s Siblings) IndexOf(h *Headline) int {
	for i, c := range s.List() {
		if c == h {
			return i
		}
	}
	return -1
}

// Splice replaces the removeCount headlines starting at idx with
// replacements (which may be zero, one or several), building a new slice
// rather than editing the old one in place — anything still holding the
// previous list (an undo record, say) keeps seeing exactly what it saw.
// It doesn't touch the replacements' Parent or Level: that's the caller's
// to set, since the right values depend on why they're being moved.
func (s Siblings) Splice(idx, removeCount int, replacements []*Headline) {
	list := s.List()
	out := make([]*Headline, 0, len(list)-removeCount+len(replacements))
	out = append(out, list[:idx]...)
	out = append(out, replacements...)
	out = append(out, list[idx+removeCount:]...)
	s.set(out)
}

// NearestTo returns the headline now at index, else the one before it,
// else nil if the list is empty at that point — a sensible thing to focus
// after removing whatever used to be at index.
func (s Siblings) NearestTo(index int) *Headline {
	list := s.List()
	if index >= 0 && index < len(list) {
		return list[index]
	}
	if index-1 >= 0 && index-1 < len(list) {
		return list[index-1]
	}
	return nil
}

// Neighbors returns the immediate previous and next siblings of the
// headline at index, or nil for either that doesn't exist. earlierCount is
// the number of further siblings before prev (i.e. not shown by prev
// alone).
func (s Siblings) Neighbors(index int) (prev, next *Headline, earlierCount int) {
	list := s.List()
	if index > 0 {
		prev = list[index-1]
		earlierCount = index - 1
	}
	if index+1 < len(list) {
		next = list[index+1]
	}
	return prev, next, earlierCount
}

// Move removes h from wherever it currently sits in f (its parent's
// children, or f's top-level list), shifts h's own Level and its
// descendants' by delta (see ShiftLevel), reparents it, and inserts it at
// newIndex among newParent's children (or f's top-level list, if newParent
// is nil). newIndex is an index into the destination list *after* h has
// been removed. It reports whether h was found in f to move; if not,
// nothing changes.
func (f *File) Move(h *Headline, newParent *Headline, newIndex, delta int) bool {
	from := Siblings{File: f, Parent: h.Parent}
	oldIndex := from.IndexOf(h)
	if oldIndex < 0 {
		return false
	}
	from.Splice(oldIndex, 1, nil)

	h.ShiftLevel(delta)
	h.Parent = newParent

	Siblings{File: f, Parent: newParent}.Splice(newIndex, 0, []*Headline{h})
	return true
}

// ShiftLevel adds delta to h's Level and every descendant's, preserving
// relative nesting while adapting to a new absolute depth.
func (h *Headline) ShiftLevel(delta int) {
	if delta == 0 {
		return
	}
	h.Level += delta
	for _, c := range h.Children {
		c.ShiftLevel(delta)
	}
}

// Topmost filters headlines down to those not descended from another
// headline also in headlines — for a bulk operation on a selection, where
// acting on an ancestor already covers its whole subtree, so a selected
// descendant needs no action of its own (and attempting one would be
// redundant or operate on a detached copy). Order is preserved.
func Topmost(headlines []*Headline) []*Headline {
	selected := make(map[*Headline]bool, len(headlines))
	for _, h := range headlines {
		selected[h] = true
	}
	var out []*Headline
	for _, h := range headlines {
		underSelectedAncestor := false
		for p := h.Parent; p != nil; p = p.Parent {
			if selected[p] {
				underSelectedAncestor = true
				break
			}
		}
		if !underSelectedAncestor {
			out = append(out, h)
		}
	}
	return out
}
