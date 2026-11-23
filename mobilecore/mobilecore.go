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
//
// The code is split by job: sync.go (clone, fetch, push, rebuild, sync
// status), captures.go (the pending-capture queue), workspace.go
// (loading the working tree and turning entries into DTOs), views.go
// (the read views), edit.go (entry changes, queued as state ops),
// entry_id.go (entry IDs) and dto.go (the JSON shapes).
package mobilecore

import (
	"encoding/json"
	"path/filepath"
	"sync"
	"time"
	_ "time/tzdata" // iOS has no zoneinfo Go can read; see SetTimeZone
)

var mu sync.Mutex

func repoDir(groupDir string) string     { return filepath.Join(groupDir, "repo") }
func queuePath(groupDir string) string   { return filepath.Join(groupDir, "captures.json") }
func statePath(groupDir string) string   { return filepath.Join(groupDir, "sync-state.json") }
func opsPath(groupDir string) string     { return filepath.Join(groupDir, "state-ops.json") }
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

func toJSON(v any) (string, error) {
	data, err := json.Marshal(v)
	return string(data), err
}

func rfc3339(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

func orDefault(s, def string) string {
	if s != "" {
		return s
	}
	return def
}
