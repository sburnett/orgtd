package mobilecore

import (
	"fmt"
	"strconv"
	"strings"
)

// entryID names an entry by position: its file (relative to the
// repository root, slash-separated) and its index path from the file's
// top level, so the entry at f.Headlines[1].Children[0] in todo.org is
// {"todo.org", [1, 0]}. Its string form, "todo.org:1.0", is the ID in an
// entry DTO, and the Swift side (ios/App/Core/Models.swift) splits it
// the same way: at the last ":", then at each ".".
//
// An ID is only good until the outline changes; lookup resolves it
// against the current workspace.
type entryID struct {
	file string
	path []int // never empty
}

// parseEntryID parses an entry DTO's ID.
func parseEntryID(s string) (entryID, error) {
	colon := strings.LastIndex(s, ":")
	if colon < 0 {
		return entryID{}, fmt.Errorf("mobilecore: bad entry id %q", s)
	}
	id := entryID{file: s[:colon]}
	for _, part := range strings.Split(s[colon+1:], ".") {
		i, err := strconv.Atoi(part)
		if err != nil || i < 0 || part != strconv.Itoa(i) {
			return entryID{}, fmt.Errorf("mobilecore: bad entry id %q", s)
		}
		id.path = append(id.path, i)
	}
	return id, nil
}

// topLevelID is the ID of the index'th top-level entry of file.
func topLevelID(file string, index int) entryID {
	return entryID{file: file, path: []int{index}}
}

func (id entryID) String() string {
	parts := make([]string, len(id.path))
	for i, n := range id.path {
		parts[i] = strconv.Itoa(n)
	}
	return id.file + ":" + strings.Join(parts, ".")
}

// index is the entry's position among its siblings.
func (id entryID) index() int { return id.path[len(id.path)-1] }

// parent is the ID of the entry's parent; ok is false for a top-level
// entry.
func (id entryID) parent() (p entryID, ok bool) {
	if len(id.path) == 1 {
		return entryID{}, false
	}
	return entryID{file: id.file, path: id.path[: len(id.path)-1 : len(id.path)-1]}, true
}

// sibling is the ID of the entry at index among id's siblings.
func (id entryID) sibling(index int) entryID {
	path := append([]int{}, id.path...)
	path[len(path)-1] = index
	return entryID{file: id.file, path: path}
}

// child is the ID of the entry's index'th child.
func (id entryID) child(index int) entryID {
	return entryID{file: id.file, path: append(append([]int{}, id.path...), index)}
}

// isSiblingOf reports whether id and o share a parent (or are both
// top-level in the same file).
func (id entryID) isSiblingOf(o entryID) bool {
	if id.file != o.file || len(id.path) != len(o.path) {
		return false
	}
	for i := 0; i < len(id.path)-1; i++ {
		if id.path[i] != o.path[i] {
			return false
		}
	}
	return true
}
