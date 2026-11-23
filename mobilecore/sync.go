package mobilecore

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"

	git "github.com/go-git/go-git/v5"

	"github.com/sburnett/orgtd/internal/capturequeue"
	"github.com/sburnett/orgtd/internal/config"
	"github.com/sburnett/orgtd/internal/gitsync"
)

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
		OpsPath:   opsPath(groupDir),
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
	r := &gitsync.Repo{Dir: repoDir(groupDir), QueuePath: queuePath(groupDir), OpsPath: opsPath(groupDir), StatePath: statePath(groupDir)}
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

// sentIDs is the queued captures and state changes the phone has sent:
// those in what it last left origin/mobile at. Until the desktop merges
// them they stay queued, but they're on the server.
func sentIDs(s gitsync.State) map[string]bool {
	sent := map[string]bool{}
	for _, t := range s.PushedTips {
		if t.Tip == s.LastPushed {
			for _, id := range t.CaptureIDs {
				sent[id] = true
			}
		}
	}
	return sent
}

// syncState is sync-state.json.
func syncState(groupDir string) (gitsync.State, error) {
	r := &gitsync.Repo{Dir: repoDir(groupDir), QueuePath: queuePath(groupDir), OpsPath: opsPath(groupDir), StatePath: statePath(groupDir)}
	return r.LoadState()
}

// DismissDropped forgets the state changes Status reported as dropped,
// once the app has shown them.
func DismissDropped(groupDir string) error {
	mu.Lock()
	defer mu.Unlock()
	r := &gitsync.Repo{Dir: repoDir(groupDir), QueuePath: queuePath(groupDir), OpsPath: opsPath(groupDir), StatePath: statePath(groupDir)}
	return r.DismissDropped()
}

// Status returns sync status and display settings as JSON (statusResult).
// It works before a clone exists (Cloned false).
func Status(groupDir string) (string, error) {
	mu.Lock()
	defer mu.Unlock()
	r := &gitsync.Repo{Dir: repoDir(groupDir), QueuePath: queuePath(groupDir), OpsPath: opsPath(groupDir), StatePath: statePath(groupDir)}
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
	ops, err := capturequeue.LoadOps(opsPath(groupDir))
	if err != nil {
		return "", err
	}
	res.PendingStates = len(ops)
	res.Waiting = append([]string{}, s.Waiting...)
	res.Dropped = []droppedJSON{}
	for _, d := range s.Dropped {
		res.Dropped = append(res.Dropped, droppedJSON{Title: d.Title, From: d.From, To: d.To, Added: d.Added, Move: d.Move, Reordered: d.Reordered, Deleted: d.Deleted, Reason: d.Reason})
	}
	pushed := sentIDs(s)
	for _, c := range queue {
		if !pushed[c.ID] {
			res.Unpushed++
		}
	}
	for _, o := range ops {
		if !pushed[o.ID] {
			res.Unpushed++
		}
	}
	res.Sent = len(queue) + len(ops) - res.Unpushed

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
