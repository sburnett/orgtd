package mobilecore

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/sburnett/orgtd/internal/capture"
	"github.com/sburnett/orgtd/internal/capturequeue"
	"github.com/sburnett/orgtd/internal/config"
)

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
// that hasn't been sent yet (see isSent), and rebuilds. Its createdAt is kept if the update leaves it out.
func UpdatePendingCapture(groupDir, captureJSON, authorName, authorEmail string) error {
	mu.Lock()
	defer mu.Unlock()
	c, err := decodeCapture(groupDir, captureJSON)
	if err != nil {
		return err
	}
	if sent, err := isSent(groupDir, c.ID); err != nil {
		return err
	} else if sent {
		return errSent
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

// errSent refuses a queue edit of a capture already sent (see isSent).
var errSent = errors.New("mobilecore: that capture was already sent to your computer; refresh and edit the entry instead")

// DeletePendingCapture drops a still-pending capture that hasn't been
// sent yet (see isSent), and rebuilds.
func DeletePendingCapture(groupDir, id, authorName, authorEmail string) error {
	mu.Lock()
	defer mu.Unlock()
	if sent, err := isSent(groupDir, id); err != nil {
		return err
	} else if sent {
		return errSent
	}
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

// isSent reports whether the queued capture id has been sent. A sent
// capture is no longer edited or deleted in the queue: the desktop may
// already have merged it as sent, and the phone would then drop the
// queue item, edit and all. It's changed like any other entry instead.
func isSent(groupDir, id string) (bool, error) {
	s, err := syncState(groupDir)
	if err != nil {
		return false, err
	}
	return sentIDs(s)[id], nil
}
