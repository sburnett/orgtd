package mobilecore

// The JSON shapes returned to Swift. Field names are a stable contract,
// mirrored by Codable structs in ios/orgtd/Core/Models.swift — change
// both together.

// entry is one headline, everything the read-only detail view shows.
type entry struct {
	// ID locates the entry: "<file path relative to the org dir>:<index
	// path>", e.g. "work.org:0.2" is the third child of the file's first
	// top-level headline. Stable until the file changes.
	ID        string   `json:"id"`
	File      string   `json:"file"`      // path relative to the org dir, e.g. "work.org" or "reference/wifi.org"
	Ancestors []string `json:"ancestors"` // titles from the top-level headline down to the parent
	Level     int      `json:"level"`     // 1 for top-level
	Keyword   string   `json:"keyword"`   // "" if none
	Done      bool     `json:"done"`      // Keyword is a done state
	Priority  string   `json:"priority"`  // "" if none
	Title     string   `json:"title"`
	Tags      []string `json:"tags"`
	Scheduled string   `json:"scheduled"` // raw timestamp text, e.g. "2026-11-18 Wed +1w"; "" if none
	// ScheduledInput and DeadlineInput are the dates as the edit form
	// shows them ("2026-11-18", or "2026-11-18 14:00"); EditEntry
	// compares against these to tell what changed.
	ScheduledInput string     `json:"scheduledInput"`
	DeadlineInput  string     `json:"deadlineInput"`
	Deadline       string     `json:"deadline"`
	Closed         string     `json:"closed"`
	Properties     []property `json:"properties"`
	Body           string     `json:"body"`               // body text, dedented
	Children       []entry    `json:"children,omitempty"` // outline only
	// Descendants counts everything under the entry, hidden closed
	// entries included (Children may leave those out).
	Descendants int `json:"descendants,omitempty"`
	// Pending is set on an entry that is a capture still in
	// the queue — not yet merged into the base branch — with CaptureID
	// naming it, so it can be edited or deleted.
	Pending   bool   `json:"pending,omitempty"`
	CaptureID string `json:"captureId,omitempty"`
	// PendingState is set on an entry whose state was changed on the
	// phone and isn't merged yet.
	PendingState bool `json:"pendingState,omitempty"`
	// PendingNew is set (with PendingState) on an entry added on the
	// phone (AddEntry) and not merged yet.
	PendingNew bool `json:"pendingNew,omitempty"`
}

// property is one property drawer line.
type property struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// agendaItem is one agenda row.
type agendaItem struct {
	Entry    entry  `json:"entry"`
	Label    string `json:"label"`    // "Scheduled", "Deadline", or "" in Next Actions
	Date     string `json:"date"`     // "2006-01-02", or "" in Next Actions
	Repeater string `json:"repeater"` // e.g. "+1w"
	Missed   int    `json:"missed"`   // only in Overdue
}

// agendaSection is one titled group of agenda rows.
type agendaSection struct {
	Name  string       `json:"name"`
	Items []agendaItem `json:"items"`
}

// agendaResult is Agenda()'s result.
type agendaResult struct {
	Today    string          `json:"today"`
	Sections []agendaSection `json:"sections"` // non-empty sections only, in display order
}

// outlineFile is one file in the outline.
type outlineFile struct {
	File    string  `json:"file"`
	Entries []entry `json:"entries"`
}

// outlineResult is Outline()'s result.
type outlineResult struct {
	Files     []outlineFile `json:"files"`
	Reference []outlineFile `json:"reference"`
}

// searchResult is Search()'s result.
type searchResult struct {
	Query   string  `json:"query"`
	Entries []entry `json:"entries"`
}

// keyword is a configured TODO keyword and how to draw it.
type keyword struct {
	Name  string `json:"name"`
	Color string `json:"color"` // "#rrggbb", an ANSI code "0".."255", or "" for none
	Bold  bool   `json:"bold"`
	Done  bool   `json:"done"`
}

// statusResult is Status()'s result: what the status line and Settings show.
type statusResult struct {
	Cloned       bool        `json:"cloned"`
	RepoURL      string      `json:"repoURL"`
	BaseBranch   string      `json:"baseBranch"`
	HostKey      hostKeyJSON `json:"hostKey"` // empty for a local remote
	InboxFile    string      `json:"inboxFile"`
	LastFetch    string      `json:"lastFetch"`   // RFC 3339, "" if never
	LastPush     string      `json:"lastPush"`    // RFC 3339, "" if never
	LastError    string      `json:"lastError"`   // "" if the last operation succeeded
	LastErrorAt  string      `json:"lastErrorAt"` // RFC 3339
	Pending      int         `json:"pending"`     // captures not yet merged
	Unpushed     int         `json:"unpushed"`    // of those, not in what was last pushed
	Keywords     []keyword   `json:"keywords"`
	TagColor     string      `json:"tagColor"`
	DateColor    string      `json:"dateColor"`
	ConfigErrors []string    `json:"configErrors"` // warnings from .orgtd.toml
	// PendingStates counts state changes not yet merged (Pending counts
	// captures); Unpushed counts both.
	PendingStates int `json:"pendingStates"`
	// Waiting is the titles of queued changes whose entries changed on
	// the desktop too: they wait for the desktop to merge them (and the
	// user to resolve any conflict) rather than being dropped.
	Waiting []string `json:"waiting"`
	// Dropped are state changes an older version dropped because their
	// entry changed on the desktop first; show them, then DismissDropped.
	// Nothing is dropped any more.
	Dropped []droppedJSON `json:"dropped"`
}

// hostKeyJSON is a server's SSH host key: ScanHostKey's result, Clone's
// input, and part of statusResult.
type hostKeyJSON struct {
	Host        string `json:"host"`        // host:port
	Key         string `json:"key"`         // authorized_keys form
	Fingerprint string `json:"fingerprint"` // "SHA256:..."
}

// droppedJSON is a state change that couldn't be applied.
type droppedJSON struct {
	Title     string `json:"title"`
	From      string `json:"from"`
	To        string `json:"to"`
	Added     bool   `json:"added,omitempty"`     // a new entry (To its keyword) whose parent changed
	Move      int    `json:"move,omitempty"`      // a demote (1) or promote (-1) of an entry that changed
	Reordered bool   `json:"reordered,omitempty"` // a move up or down among siblings, whose entry or neighbour changed
	Deleted   bool   `json:"deleted,omitempty"`   // a delete of an entry that changed
	Reason    string `json:"reason"`
}

// KeyPair is a freshly generated SSH key.
type KeyPair struct {
	PublicKey     string // authorized_keys line, for the server's ~/.ssh/authorized_keys
	PrivateKeyPEM []byte // OpenSSH PEM; store it in the Keychain
}
