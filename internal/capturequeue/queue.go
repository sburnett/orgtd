// Package capturequeue keeps the iOS app's pending captures: every
// capture not yet known to be merged into origin/main. The queue file is
// the source of truth the phone's git branch is rebuilt from on every
// sync (see ios/DESIGN.md §4.1).
//
// The share extension never edits the queue file itself: it drops each
// capture as its own JSON file in an incoming directory (see Import),
// which the app folds into the queue. That way the extension and the app
// (whose Go code can't take part in NSFileCoordinator) never write the
// same file.
package capturequeue

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/sburnett/orgtd/internal/atomicfile"
	"github.com/sburnett/orgtd/internal/capture"
)

// Load reads the queue at path, oldest capture first. A missing file is
// an empty queue. It is tolerant of hand- or extension-written entries:
// unknown fields are ignored, an entry with no ID is given one, and of
// several entries sharing an ID only the first is kept.
func Load(path string) ([]capture.Capture, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("capturequeue: reading %s: %w", path, err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, nil
	}
	var cs []capture.Capture
	if err := json.Unmarshal(data, &cs); err != nil {
		return nil, fmt.Errorf("capturequeue: parsing %s: %w", path, err)
	}
	return normalize(cs), nil
}

func normalize(cs []capture.Capture) []capture.Capture {
	seen := map[string]bool{}
	out := cs[:0]
	for _, c := range cs {
		if c.ID == "" {
			c.ID = NewID()
		}
		if seen[c.ID] {
			continue
		}
		seen[c.ID] = true
		out = append(out, c)
	}
	return out
}

// NewID returns a fresh capture ID.
func NewID() string { return strings.ToUpper(uuid.NewString()) }

// Save writes cs to path atomically (temp file and rename).
func Save(path string, cs []capture.Capture) error {
	if cs == nil {
		cs = []capture.Capture{}
	}
	data, err := json.MarshalIndent(cs, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, append(data, '\n'))
}

// Add appends c to the queue at path, assigning it an ID if it has none,
// and returns the ID.
func Add(path string, c capture.Capture) (string, error) {
	cs, err := Load(path)
	if err != nil {
		return "", err
	}
	if c.ID == "" {
		c.ID = NewID()
	}
	for _, existing := range cs {
		if existing.ID == c.ID {
			return "", fmt.Errorf("capturequeue: capture %s already queued", c.ID)
		}
	}
	return c.ID, Save(path, append(cs, c))
}

// Update replaces the queued capture with c's ID, keeping its place in
// the queue. It's an error if no capture has that ID (e.g. it was merged
// and dropped since the caller last looked).
func Update(path string, c capture.Capture) error {
	cs, err := Load(path)
	if err != nil {
		return err
	}
	for i := range cs {
		if cs[i].ID == c.ID {
			cs[i] = c
			return Save(path, cs)
		}
	}
	return fmt.Errorf("capturequeue: no pending capture %s", c.ID)
}

// Delete removes the capture with id. It's an error if there is none.
func Delete(path, id string) error {
	cs, err := Load(path)
	if err != nil {
		return err
	}
	for i := range cs {
		if cs[i].ID == id {
			return Save(path, append(cs[:i], cs[i+1:]...))
		}
	}
	return fmt.Errorf("capturequeue: no pending capture %s", id)
}

// Remove drops every capture whose ID is in ids (absent ones are
// ignored), returning how many were removed.
func Remove(path string, ids []string) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	cs, err := Load(path)
	if err != nil {
		return 0, err
	}
	drop := map[string]bool{}
	for _, id := range ids {
		drop[id] = true
	}
	kept := cs[:0]
	for _, c := range cs {
		if !drop[c.ID] {
			kept = append(kept, c)
		}
	}
	n := len(cs) - len(kept)
	if n == 0 {
		return 0, nil
	}
	return n, Save(path, kept)
}

// Import moves every *.json file in incomingDir (one capture each, as the
// share extension writes them) into the queue at path, ordered by
// CreatedAt, and returns the imported captures. A capture whose ID is
// already queued is not added twice. Files that don't parse are left in
// place, renamed with a ".bad" suffix so they're neither retried nor lost.
// A missing incomingDir imports nothing.
func Import(path, incomingDir string) ([]capture.Capture, error) {
	entries, err := os.ReadDir(incomingDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("capturequeue: reading %s: %w", incomingDir, err)
	}
	var files []string
	var incoming []capture.Capture
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		p := filepath.Join(incomingDir, e.Name())
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("capturequeue: reading %s: %w", p, err)
		}
		var c capture.Capture
		if err := json.Unmarshal(data, &c); err != nil || strings.TrimSpace(c.Title) == "" {
			os.Rename(p, p+".bad")
			continue
		}
		if c.ID == "" {
			c.ID = strings.ToUpper(strings.TrimSuffix(e.Name(), ".json"))
		}
		incoming = append(incoming, c)
		files = append(files, p)
	}
	if len(incoming) == 0 {
		return nil, nil
	}
	sort.SliceStable(incoming, func(i, j int) bool { return incoming[i].CreatedAt.Before(incoming[j].CreatedAt) })

	cs, err := Load(path)
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for _, c := range cs {
		have[c.ID] = true
	}
	var added []capture.Capture
	for _, c := range incoming {
		if !have[c.ID] {
			have[c.ID] = true
			cs = append(cs, c)
			added = append(added, c)
		}
	}
	if err := Save(path, cs); err != nil {
		return nil, err
	}
	// Only once the queue holds them: a crash before this point just
	// imports the same files again next time, and IDs dedupe them.
	for _, p := range files {
		os.Remove(p)
	}
	return added, nil
}

// writeAtomic writes data to path atomically (see internal/atomicfile),
// creating its directory if need be.
func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("capturequeue: %w", err)
	}
	if err := atomicfile.Write(path, data, 0o644); err != nil {
		return fmt.Errorf("capturequeue: writing %s: %w", path, err)
	}
	return nil
}
