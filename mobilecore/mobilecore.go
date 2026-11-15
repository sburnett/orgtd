// Package mobilecore is the iOS app's Go core, bound to Swift with
// `gomobile bind` (see scripts/build-mobilecore.sh). It is a thin facade
// over internal/gitsync, internal/capturequeue, internal/capture,
// internal/agenda and the shared org/workspace/config packages, so the
// phone buckets the agenda, parses keywords and renders captures exactly
// as the desktop does. See ios/DESIGN.md §6.
//
// gomobile only bridges simple types, so read views come back as JSON
// strings (shapes in dto.go). Every call is synchronous; Swift runs them
// off the main actor. Calls are serialized by a package mutex. Time
// always comes in from the caller (nowUnix), so the clock can be pinned.
//
// Everything lives under one directory, groupDir (the App Group
// container):
//
//	<groupDir>/repo/             the git clone, checked out at local "mobile"
//	<groupDir>/captures.json     pending-capture queue (source of truth)
//	<groupDir>/sync-state.json   pushed-tip history, last fetch/push/error
//	<groupDir>/incoming/         one JSON file per share-extension capture
package mobilecore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	_ "time/tzdata" // iOS has no zoneinfo Go can read; see SetTimeZone

	git "github.com/go-git/go-git/v5"

	"github.com/sburnett/orgtd/internal/agenda"
	"github.com/sburnett/orgtd/internal/capture"
	"github.com/sburnett/orgtd/internal/capturequeue"
	"github.com/sburnett/orgtd/internal/config"
	"github.com/sburnett/orgtd/internal/gitsync"
	"github.com/sburnett/orgtd/internal/org"
	"github.com/sburnett/orgtd/internal/orgdate"
	"github.com/sburnett/orgtd/internal/workspace"
)

var mu sync.Mutex

func repoDir(groupDir string) string     { return filepath.Join(groupDir, "repo") }
func queuePath(groupDir string) string   { return filepath.Join(groupDir, "captures.json") }
func statePath(groupDir string) string   { return filepath.Join(groupDir, "sync-state.json") }
func incomingDir(groupDir string) string { return filepath.Join(groupDir, "incoming") }

// SetTimeZone makes name (an IANA zone such as "America/Los_Angeles",
// e.g. Swift's TimeZone.current.identifier) the local time zone for
// agenda dates and CREATED stamps. Go can't find the device's zone on its
// own on iOS.
func SetTimeZone(name string) error {
	loc, err := time.LoadLocation(name)
	if err != nil {
		return err
	}
	mu.Lock()
	defer mu.Unlock()
	time.Local = loc
	return nil
}

func now(nowUnix int64) time.Time { return time.Unix(nowUnix, 0).In(time.Local) }

// GenerateSSHKey makes the phone's new ed25519 SSH key.
func GenerateSSHKey() (*KeyPair, error) {
	pub, priv, err := gitsync.GenerateKey("orgtd-ios")
	if err != nil {
		return nil, err
	}
	return &KeyPair{PublicKey: pub, PrivateKeyPEM: priv}, nil
}

// PublicKeyFor returns the authorized_keys line for a stored private key.
func PublicKeyFor(privKey []byte) (string, error) { return gitsync.PublicKey(privKey) }

// newRepo opens the phone's repo for url (the clone's own origin if
// ""), authenticating with privKey against the host key trusted at setup.
// A nil privKey means the caller needs no network (a local rebuild), so
// no auth is set up at all.
func newRepo(groupDir, url string, privKey []byte, authorName, authorEmail string) (*gitsync.Repo, error) {
	r := &gitsync.Repo{
		Dir:       repoDir(groupDir),
		QueuePath: queuePath(groupDir),
		StatePath: statePath(groupDir),
		Author:    gitsync.Author{Name: authorName, Email: authorEmail},
	}
	if privKey == nil {
		return r, nil
	}
	if url == "" {
		url = remoteURL(r.Dir)
	}
	s, err := r.LoadState()
	if err != nil {
		return nil, err
	}
	return r, setAuth(r, url, privKey, s.HostKey.Key)
}

func setAuth(r *gitsync.Repo, url string, privKey []byte, hostKey string) error {
	auth, err := gitsync.Auth(url, privKey, hostKey)
	if err != nil {
		return err
	}
	if auth != nil {
		r.Auth = auth
	}
	return nil
}

// remoteURL is the clone's origin URL, "" if there's no clone.
func remoteURL(dir string) string {
	repo, err := git.PlainOpen(dir)
	if err != nil {
		return ""
	}
	remote, err := repo.Remote("origin")
	if err != nil || len(remote.Config().URLs) == 0 {
		return ""
	}
	return remote.Config().URLs[0]
}

// ScanHostKey connects to repoURL's SSH server just long enough to read
// its host key, and returns it as JSON: {"host", "key", "fingerprint"}.
// Show the fingerprint for the user to compare with the server's own
// (`ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub`), then pass the
// JSON unchanged to Clone.
func ScanHostKey(repoURL string) (string, error) {
	hk, err := gitsync.ScanHostKey(strings.TrimSpace(repoURL), 15*time.Second)
	if err != nil {
		return "", err
	}
	return toJSON(hostKeyJSON{Host: hk.Host, Key: hk.Key, Fingerprint: hk.Fingerprint})
}

// Clone clones repoURL into groupDir, trusting only the host key in
// hostKey (from ScanHostKey, after the user confirmed it; ignored for
// a local path). groupDir/repo may already exist if it's empty (the app
// pre-creates it to set its file protection); any existing clone must be
// removed first (RemoveClone).
func Clone(groupDir, repoURL string, privKey []byte, hostKey string) error {
	mu.Lock()
	defer mu.Unlock()
	repoURL = strings.TrimSpace(repoURL)
	if err := os.MkdirAll(groupDir, 0o755); err != nil {
		return err
	}
	if entries, err := os.ReadDir(repoDir(groupDir)); err == nil && len(entries) > 0 {
		return errors.New("mobilecore: already cloned; remove the clone first")
	}
	var hk hostKeyJSON
	if gitsync.IsSSHURL(repoURL) {
		if err := json.Unmarshal([]byte(hostKey), &hk); err != nil || hk.Key == "" {
			return errors.New("mobilecore: confirm the server's host key first")
		}
	}
	r := &gitsync.Repo{Dir: repoDir(groupDir), QueuePath: queuePath(groupDir), StatePath: statePath(groupDir)}
	if err := setAuth(r, repoURL, privKey, hk.Key); err != nil {
		return err
	}
	return r.Clone(repoURL, gitsync.HostKey{Host: hk.Host, Key: hk.Key, Fingerprint: hk.Fingerprint})
}

// RemoveClone deletes the clone and sync state, keeping the capture
// queue, so a different repository can be set up without losing
// anything captured.
func RemoveClone(groupDir string) error {
	mu.Lock()
	defer mu.Unlock()
	if err := os.RemoveAll(repoDir(groupDir)); err != nil {
		return err
	}
	if err := os.Mkdir(repoDir(groupDir), 0o755); err != nil {
		return err
	}
	if err := os.Remove(statePath(groupDir)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// importIncoming folds share-extension captures into the queue.
func importIncoming(groupDir string) error {
	_, err := capturequeue.Import(queuePath(groupDir), incomingDir(groupDir))
	return err
}

// Refresh imports share-extension captures, fetches, drops merged
// captures and rebuilds the working tree (§4.3 steps 1–4) without
// pushing. A failed fetch (offline) still rebuilds from what was last
// fetched, then returns the error.
func Refresh(groupDir string, privKey []byte, authorName, authorEmail string) error {
	mu.Lock()
	defer mu.Unlock()
	if err := importIncoming(groupDir); err != nil {
		return err
	}
	r, err := newRepo(groupDir, "", privKey, authorName, authorEmail)
	if err != nil {
		return err
	}
	return r.Refresh()
}

// RefreshLocal is Refresh without touching the network: it imports
// share-extension captures and rebuilds the working tree from what was
// last fetched. The app calls it on every return to the foreground and
// fetches (Refresh) only when its back-off allows.
func RefreshLocal(groupDir, authorName, authorEmail string) error {
	mu.Lock()
	defer mu.Unlock()
	if err := importIncoming(groupDir); err != nil {
		return err
	}
	r, err := newRepo(groupDir, "", nil, authorName, authorEmail)
	if err != nil {
		return err
	}
	return r.RefreshLocal()
}

// Sync is Refresh followed by pushing local "mobile" (§4.3, all steps).
func Sync(groupDir string, privKey []byte, authorName, authorEmail string) error {
	mu.Lock()
	defer mu.Unlock()
	if err := importIncoming(groupDir); err != nil {
		return err
	}
	r, err := newRepo(groupDir, "", privKey, authorName, authorEmail)
	if err != nil {
		return err
	}
	return r.Sync()
}

// rebuild re-derives the working tree from origin's base branch and the
// queue, with no network.
func rebuild(groupDir, authorName, authorEmail string) error {
	r, err := newRepo(groupDir, "", nil, authorName, authorEmail)
	if err != nil {
		return err
	}
	return r.Rebuild()
}

// decodeCapture parses and validates a capture from Swift, including that
// its keyword (if any) is one of the repo's configured, not-done keywords.
func decodeCapture(groupDir, captureJSON string) (capture.Capture, error) {
	var c capture.Capture
	if err := json.Unmarshal([]byte(captureJSON), &c); err != nil {
		return c, fmt.Errorf("mobilecore: bad capture JSON: %w", err)
	}
	if err := c.Validate(); err != nil {
		return c, err
	}
	if c.Keyword == "" {
		return c, nil
	}
	cfg, err := repoConfig(groupDir)
	if err != nil {
		return c, err
	}
	for _, k := range cfg.Keywords {
		if k.Name == c.Keyword && !k.Done {
			return c, nil
		}
	}
	return c, fmt.Errorf("mobilecore: %q isn't one of the configured (not done) keywords", c.Keyword)
}

// repoConfig is the clone's .orgtd.toml over the built-in defaults.
func repoConfig(groupDir string) (config.Config, error) {
	cfg := config.Default()
	repoCfg, _, err := config.LoadRepo(repoDir(groupDir))
	if err != nil {
		return cfg, err
	}
	config.Overlay(&cfg, repoCfg, config.LayerRepo, nil)
	cfg.ApplyDefaults()
	return cfg, nil
}

// Capture queues a new capture (JSON, see capture.Capture; ID and
// createdAt are filled in if missing) and commits it on local "mobile",
// so the read views show it at once. It needs no network. Returns the
// capture's ID.
func Capture(groupDir, captureJSON, authorName, authorEmail string) (string, error) {
	mu.Lock()
	defer mu.Unlock()
	c, err := decodeCapture(groupDir, captureJSON)
	if err != nil {
		return "", err
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now().In(time.Local)
	}
	id, err := capturequeue.Add(queuePath(groupDir), c)
	if err != nil {
		return "", err
	}
	return id, rebuild(groupDir, authorName, authorEmail)
}

// UpdatePendingCapture replaces a still-pending capture (matched by ID)
// and rebuilds. Its createdAt is kept if the update leaves it out.
func UpdatePendingCapture(groupDir, captureJSON, authorName, authorEmail string) error {
	mu.Lock()
	defer mu.Unlock()
	c, err := decodeCapture(groupDir, captureJSON)
	if err != nil {
		return err
	}
	if c.CreatedAt.IsZero() {
		if old, ok := findCapture(groupDir, c.ID); ok {
			c.CreatedAt = old.CreatedAt
		}
	}
	if err := capturequeue.Update(queuePath(groupDir), c); err != nil {
		return err
	}
	return rebuild(groupDir, authorName, authorEmail)
}

// DeletePendingCapture drops a still-pending capture and rebuilds.
func DeletePendingCapture(groupDir, id, authorName, authorEmail string) error {
	mu.Lock()
	defer mu.Unlock()
	if err := capturequeue.Delete(queuePath(groupDir), id); err != nil {
		return err
	}
	return rebuild(groupDir, authorName, authorEmail)
}

func findCapture(groupDir, id string) (capture.Capture, bool) {
	cs, _ := capturequeue.Load(queuePath(groupDir))
	for _, c := range cs {
		if c.ID == id {
			return c, true
		}
	}
	return capture.Capture{}, false
}

// PendingCapture returns one queued capture as JSON, for the edit sheet.
func PendingCapture(groupDir, id string) (string, error) {
	mu.Lock()
	defer mu.Unlock()
	c, ok := findCapture(groupDir, id)
	if !ok {
		return "", fmt.Errorf("mobilecore: no pending capture %s", id)
	}
	return toJSON(c)
}

// ParseDate resolves a typed date exactly as the desktop's "gd" does
// ("2026-12-25", "3d", "2w", "next tue", ...) into "YYYY-MM-DD" (or
// "YYYY-MM-DD HH:MM").
func ParseDate(input string, nowUnix int64) (string, error) {
	return capture.ParseDate(input, now(nowUnix))
}

func toJSON(v any) (string, error) {
	data, err := json.Marshal(v)
	return string(data), err
}

// loaded is the working tree as the read views see it.
type loaded struct {
	cfg      config.Config
	warnings []string
	ws       *workspace.Workspace
}

// load reads the repo-level config, applies its keywords, and loads the
// workspace from the working tree.
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
	var active, done []string
	for _, k := range cfg.Keywords {
		if k.Done {
			done = append(done, k.Name)
		} else {
			active = append(active, k.Name)
		}
	}
	org.SetKeywords(active, done)
	ws, err := workspace.Load(dir, cfg.OutlineIgnoreFiles...)
	if err != nil {
		return nil, err
	}
	return &loaded{cfg: cfg, warnings: warnings, ws: ws}, nil
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
	var parts []string
	for _, i := range idx {
		parts = append(parts, strconv.Itoa(i))
	}
	ancestors := []string{}
	for p := h.Parent; p != nil; p = p.Parent {
		ancestors = append([]string{p.Title}, ancestors...)
	}
	e := entry{
		ID:        l.rel(f) + ":" + strings.Join(parts, "."),
		File:      l.rel(f),
		Ancestors: ancestors,
		Level:     h.Level,
		Keyword:   h.Keyword,
		Done:      org.IsDoneKeyword(h.Keyword),
		Priority:  h.Priority,
		Title:     h.Title,
		Tags:      append([]string{}, h.Tags...),
		Scheduled: rawOrEmpty(h.Scheduled),
		Deadline:  rawOrEmpty(h.Deadline),
		Closed:    rawOrEmpty(h.Closed),
		Body:      dedentBody(h),
	}
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

// Agenda returns the agenda as JSON (agendaResult): Overdue, Due Today,
// Upcoming (agenda_window_days) and Next Actions, by the same rules as
// the desktop's :agenda, minus Meetings.
func Agenda(groupDir string, nowUnix int64) (string, error) {
	mu.Lock()
	defer mu.Unlock()
	l, err := load(groupDir)
	if err != nil {
		return "", err
	}
	today := orgdate.TruncateToDate(now(nowUnix))
	res := agendaResult{Today: today.Format("2006-01-02"), Sections: []agendaSection{}}
	by := agenda.BySection(l.ws, l.cfg, today)
	for _, name := range agenda.DateSections {
		es := by[name]
		if len(es) == 0 {
			continue
		}
		sec := agendaSection{Name: name}
		for _, e := range es {
			sec.Items = append(sec.Items, agendaItem{Entry: l.flatEntry(e.Headline), Label: e.Label, Date: e.Date.Format("2006-01-02"), Repeater: e.Repeater, Missed: e.Missed})
		}
		res.Sections = append(res.Sections, sec)
	}
	if next := agenda.NextActionHeadlines(l.ws, l.cfg); len(next) > 0 {
		sec := agendaSection{Name: agenda.NextActions}
		for _, h := range next {
			sec.Items = append(sec.Items, agendaItem{Entry: l.flatEntry(h)})
		}
		res.Sections = append(res.Sections, sec)
	}
	return toJSON(res)
}

// Inbox returns the inbox file's top-level, not-done headlines in file
// order as JSON (inboxResult), marking the ones that are still-pending
// captures. The working tree's inbox is the base branch's plus one
// appended headline per queued capture, in queue order, so the last
// len(queue) headlines are the pending ones.
func Inbox(groupDir string) (string, error) {
	mu.Lock()
	defer mu.Unlock()
	l, err := load(groupDir)
	if err != nil {
		return "", err
	}
	res := inboxResult{File: l.cfg.InboxFile, Entries: []entry{}}
	var inbox *org.File
	for _, f := range l.ws.Files {
		if l.ws.IsNamed(f, l.cfg.InboxFile) {
			inbox = f
		}
	}
	if inbox == nil {
		return toJSON(res)
	}
	queue, err := capturequeue.Load(queuePath(groupDir))
	if err != nil {
		return "", err
	}
	firstPending := len(inbox.Headlines) - len(queue)
	for i, h := range inbox.Headlines {
		if org.IsDoneKeyword(h.Keyword) {
			continue
		}
		e := l.toEntry(inbox, h, []int{i}, false, nil)
		if firstPending >= 0 && i >= firstPending {
			e.Pending = true
			e.CaptureID = queue[i-firstPending].ID
		}
		res.Entries = append(res.Entries, e)
	}
	return toJSON(res)
}

// Outline returns every outline file's headline tree as JSON
// (outlineResult), with reference/ files separately. Unless showClosed,
// a headline closed more than hide_closed_after_hours ago is left out
// with its subtree, as the desktop outline does (reference material is
// never hidden).
func Outline(groupDir string, showClosed bool, nowUnix int64) (string, error) {
	mu.Lock()
	defer mu.Unlock()
	l, err := load(groupDir)
	if err != nil {
		return "", err
	}
	t := now(nowUnix)
	after := time.Duration(l.cfg.HideClosedAfterHours) * time.Hour
	keep := func(h *org.Headline) bool { return showClosed || !orgdate.StaleClosed(h, t, after) }
	res := outlineResult{Files: []outlineFile{}, Reference: []outlineFile{}}
	for _, f := range l.ws.Files {
		ref := l.ws.IsReference(f)
		if !ref && !l.isOutlineFile(f) {
			continue
		}
		of := outlineFile{File: l.rel(f), Entries: []entry{}}
		for i, h := range f.Headlines {
			if !ref && !keep(h) {
				continue
			}
			k := keep
			if ref {
				k = nil
			}
			of.Entries = append(of.Entries, l.toEntry(f, h, []int{i}, true, k))
		}
		if ref {
			res.Reference = append(res.Reference, of)
		} else {
			res.Files = append(res.Files, of)
		}
	}
	return toJSON(res)
}

// Search returns, as JSON (searchResult), every outline or reference
// headline whose title, tags or body contains query, case-insensitively —
// the desktop "/" match — in file order.
func Search(groupDir, query string) (string, error) {
	mu.Lock()
	defer mu.Unlock()
	l, err := load(groupDir)
	if err != nil {
		return "", err
	}
	res := searchResult{Query: query, Entries: []entry{}}
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return toJSON(res)
	}
	for _, f := range l.ws.Files {
		if !l.ws.IsReference(f) && !l.isOutlineFile(f) {
			continue
		}
		org.Walk(f.Headlines, func(h *org.Headline) {
			text := strings.ToLower(h.Title + "\n" + strings.Join(h.Tags, ":") + "\n" + strings.Join(h.Body, "\n"))
			if strings.Contains(text, q) {
				res.Entries = append(res.Entries, l.flatEntry(h))
			}
		})
	}
	return toJSON(res)
}

// Tags returns every tag used anywhere in the loaded files, sorted, as a
// JSON array of strings — for capture-sheet autocompletion.
func Tags(groupDir string) (string, error) {
	mu.Lock()
	defer mu.Unlock()
	l, err := load(groupDir)
	if err != nil {
		return "", err
	}
	seen := map[string]bool{}
	for _, f := range l.ws.Files {
		if l.ws.IsNamed(f, l.cfg.CalendarFile) {
			continue // attendee tags; noise for capture
		}
		org.Walk(f.Headlines, func(h *org.Headline) {
			for _, t := range h.Tags {
				seen[t] = true
			}
		})
	}
	tags := make([]string, 0, len(seen))
	for t := range seen {
		tags = append(tags, t)
	}
	sort.Strings(tags)
	return toJSON(tags)
}

func rfc3339(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

// Status returns sync status and display settings as JSON (statusResult).
// It works before a clone exists (Cloned false).
func Status(groupDir string) (string, error) {
	mu.Lock()
	defer mu.Unlock()
	r := &gitsync.Repo{Dir: repoDir(groupDir), QueuePath: queuePath(groupDir), StatePath: statePath(groupDir)}
	s, err := r.LoadState()
	if err != nil {
		return "", err
	}
	queue, err := capturequeue.Load(queuePath(groupDir))
	if err != nil {
		return "", err
	}
	res := statusResult{
		RepoURL:      remoteURL(r.Dir),
		BaseBranch:   s.BaseBranch,
		HostKey:      hostKeyJSON{Host: s.HostKey.Host, Key: s.HostKey.Key, Fingerprint: s.HostKey.Fingerprint},
		LastFetch:    rfc3339(s.LastFetch),
		LastPush:     rfc3339(s.LastPush),
		LastError:    s.LastError,
		LastErrorAt:  rfc3339(s.LastErrorAt),
		Pending:      len(queue),
		Keywords:     []keyword{},
		ConfigErrors: []string{},
	}
	res.Cloned = res.RepoURL != ""
	pushed := map[string]bool{}
	for _, t := range s.PushedTips {
		if t.Tip == s.LastPushed {
			for _, id := range t.CaptureIDs {
				pushed[id] = true
			}
		}
	}
	for _, c := range queue {
		if !pushed[c.ID] {
			res.Unpushed++
		}
	}

	cfg := config.Default()
	if res.Cloned {
		repoCfg, warnings, err := config.LoadRepo(r.Dir)
		if err != nil {
			res.ConfigErrors = append(res.ConfigErrors, err.Error())
		} else {
			res.ConfigErrors = append(res.ConfigErrors, warnings...)
			config.Overlay(&cfg, repoCfg, config.LayerRepo, nil)
		}
	}
	cfg.ApplyDefaults()
	res.InboxFile = cfg.InboxFile
	for _, k := range cfg.Keywords {
		res.Keywords = append(res.Keywords, keyword{Name: k.Name, Color: cfg.KeywordColor(k), Bold: k.Bold, Done: k.Done})
	}
	res.TagColor = orDefault(cfg.Colors.Tag, config.DefaultTagColor)
	res.DateColor = orDefault(cfg.Colors.Timestamp, config.DefaultTimestampColor)
	return toJSON(res)
}

func orDefault(s, def string) string {
	if s != "" {
		return s
	}
	return def
}
