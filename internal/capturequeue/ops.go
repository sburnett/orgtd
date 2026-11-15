package capturequeue

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sburnett/orgtd/internal/capture"
)

// StateOp is a change made on the phone to an existing entry — its TODO
// keyword, and with Edit its other fields too — waiting to be merged. Like a capture, it lives in a queue and is
// replayed onto the base branch on every rebuild; unlike a capture, it
// has to find its entry again, by file, title path and fingerprint (see
// internal/gitsync), and is dropped if the entry has changed since.
type StateOp struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"createdAt"` // also the CLOSED/LAST_REPEAT time, so replays are identical
	File      string    `json:"file"`      // path relative to the repository root
	// Path is the titles from the top-level ancestor down to the entry
	// itself.
	Path []string `json:"path"`
	// Fingerprint identifies the entry's own text when the change was
	// made: any edit to it since (on the desktop, or by an earlier op)
	// means the change no longer applies as intended.
	Fingerprint string `json:"fingerprint"`
	From        string `json:"from"` // keyword before ("" for none)
	To          string `json:"to"`   // keyword after
	// Edit, when set, also changes the entry's other fields — only
	// those the user actually changed, so untouched ones (a date's
	// repeater, say) are left exactly as written.
	Edit *EntryEdit `json:"edit,omitempty"`
	// Insert, when set, makes the op add a new entry rather than change
	// one. Path and Fingerprint are then the new entry's parent's (both
	// empty for a top-level entry), and the entry goes first or last
	// among the parent's children — or among the file's top-level
	// entries. With Insert.Beside, they're a sibling's instead (see
	// EntryInsert). From is unused.
	Insert *EntryInsert `json:"insert,omitempty"`
	// Move, when set, moves the entry, with its subtree, one level
	// deeper or shallower, as desktop ">>"/"<<" do, or up or down among
	// its siblings.
	Move *EntryMove `json:"move,omitempty"`
	// Delete, when set, deletes the entry with its subtree, as desktop
	// "dd" does.
	Delete *EntryDelete `json:"delete,omitempty"`
}

// EntryDelete is deleting an entry and everything under it.
type EntryDelete struct {
	// Subtree identifies the whole subtree's text when the delete was
	// made (the op's Fingerprint covers only the entry's own), so a
	// child changed since on the desktop isn't deleted unseen.
	Subtree string `json:"subtree"`
}

// EntryMove is a demote (Delta 1: the entry becomes the last child of
// its previous sibling), a promote (Delta -1: it becomes the next
// sibling of its parent), or a reorder (Delta 0: it moves, at the same
// level, right before or after Anchor, one of its siblings).
type EntryMove struct {
	Delta int `json:"delta"`
	// To is the entry's title path once moved: for marking it pending,
	// and so a demote lands under the same previous sibling it was made
	// against, or not at all.
	To []string `json:"to"`
	// Anchor, for a reorder, is the sibling the entry goes next to: its
	// title and fingerprint, so a reorder whose sibling has changed since
	// is dropped rather than landing somewhere else. The entry goes right
	// before it or, with After, right after its subtree.
	Anchor            string `json:"anchor,omitempty"`
	AnchorFingerprint string `json:"anchorFingerprint,omitempty"`
	After             bool   `json:"after,omitempty"`
	// ToFile, for a reorder of a top-level entry, is another file
	// (relative to the repository root) whose top level it moves to:
	// next to Anchor there or, with no Anchor, at its end. To is then
	// its title path in that file.
	ToFile string `json:"toFile,omitempty"`
}

// EntryInsert is a new entry an op adds.
type EntryInsert struct {
	Last bool `json:"last"` // after the existing entries (or Beside's sibling), rather than before
	// Beside places the entry next to the op's entry, at its level:
	// right before it or, with Last, right after its subtree.
	Beside bool `json:"beside,omitempty"`
	// Entry is rendered as a capture is, at the parent's level + 1 (or
	// the sibling's level).
	Entry capture.Capture `json:"entry"`
}

// EntryEdit is the fields an edit changes; nil means unchanged.
type EntryEdit struct {
	Title *string   `json:"title,omitempty"`
	Tags  *[]string `json:"tags,omitempty"`
	// Body is the entry's notes, dedented (indentation is restored on
	// writing).
	Body *string `json:"body,omitempty"`
	// Scheduled and Deadline are "YYYY-MM-DD" or "YYYY-MM-DD HH:MM", or
	// "" to remove. A changed date keeps the old timestamp's repeater
	// and warning cookies, as org-mode's rescheduling does.
	Scheduled *string `json:"scheduled,omitempty"`
	Deadline  *string `json:"deadline,omitempty"`
}

// NewTitle is the entry's title once the op is applied.
func (o StateOp) NewTitle() string {
	if o.Insert != nil {
		return strings.Join(strings.Fields(o.Insert.Entry.Title), " ")
	}
	if o.Edit != nil && o.Edit.Title != nil {
		return *o.Edit.Title
	}
	return o.Title()
}

// Title is the entry's own title (before the op), or a new entry's.
func (o StateOp) Title() string {
	if o.Insert != nil {
		return o.NewTitle()
	}
	if len(o.Path) == 0 {
		return ""
	}
	return o.Path[len(o.Path)-1]
}

// LoadOps reads the state-change queue at path, oldest first. A missing
// file is an empty queue.
func LoadOps(path string) ([]StateOp, error) {
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
	var ops []StateOp
	if err := json.Unmarshal(data, &ops); err != nil {
		return nil, fmt.Errorf("capturequeue: parsing %s: %w", path, err)
	}
	return ops, nil
}

// SaveOps writes ops to path atomically.
func SaveOps(path string, ops []StateOp) error {
	if ops == nil {
		ops = []StateOp{}
	}
	data, err := json.MarshalIndent(ops, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, append(data, '\n'))
}

// AddOp appends op (given an ID if it has none) and returns its ID.
func AddOp(path string, op StateOp) (string, error) {
	ops, err := LoadOps(path)
	if err != nil {
		return "", err
	}
	if op.ID == "" {
		op.ID = NewID()
	}
	return op.ID, SaveOps(path, append(ops, op))
}

// RemoveOps drops every op whose ID is in ids, returning how many went.
func RemoveOps(path string, ids []string) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	ops, err := LoadOps(path)
	if err != nil {
		return 0, err
	}
	drop := map[string]bool{}
	for _, id := range ids {
		drop[id] = true
	}
	kept := ops[:0]
	for _, o := range ops {
		if !drop[o.ID] {
			kept = append(kept, o)
		}
	}
	n := len(ops) - len(kept)
	if n == 0 {
		return 0, nil
	}
	return n, SaveOps(path, kept)
}
