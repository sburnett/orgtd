# orgtd for iOS — Design & Implementation Plan

Status: **built** (phases 1–9). Phase 0's on-device checks, R1 (App
Groups under free signing) and a real clone and push from a phone, are
still unverified: everything was tested in Go and in the simulator only.
This document records the design decisions and the order the work was
built in. §10 lists where the build departs from the plan. Build and
setup instructions are in [README.md](README.md). The
desktop app's own design is in [../DESIGN.md](../DESIGN.md); its
behavior is specified by [../README.md](../README.md).

## 1. Summary

An iPhone app, touch-native (no vim keys), that does two things:

1. **Captures** new items into the inbox — from inside the app or from
   the iOS share sheet.
2. **Reads** the org directory — an agenda, the inbox, and a read-only
   outline browser.

The phone's writes are **appending new top-level entries to
`inbox.org`** and **changing an entry's TODO keyword** (added later; see
§10), committed to a dedicated `mobile` git branch that the user merges
into `main` by hand on the Mac. The phone never merges and never
resolves a conflict.

## 2. Decisions

| Topic | Decision |
|---|---|
| Devices / UX | iPhone only, touch-native (lists, sheets, swipe). No hardware-keyboard/vim mode |
| File access | The org directory is a git repo; the app keeps its own clone |
| Git host / auth | Any git server reachable over SSH (originally GitHub; changed so the org files needn't be hosted by a third party — a self-hosted server, typically reached over Tailscale). An ed25519 key generated on the device and stored in the Keychain; the user adds it to the server account's `authorized_keys`, restricted to `git-shell`. The server's host key is shown at setup for the user to verify by fingerprint, then pinned |
| Phone writes | Appends to `inbox_file` (default `inbox.org`), and TODO-keyword changes to existing entries (§10). No other edits, deletes, or IDs added to existing entries |
| Branch | All phone changes live on `mobile`. The user merges `mobile` → `main` on the Mac with a normal merge or fast-forward (never squash/cherry-pick) |
| Commit / push | Each capture is committed automatically; pushing only happens on an explicit **Sync** |
| Branch upkeep | On Sync, `mobile` is rebuilt as `origin/main` + the still-unmerged captures and force-pushed (with lease) |
| Fetch | On launch, on returning to foreground, and on Sync. Fetch is read-only and can't conflict |
| Capture fields | Keyword (optional; one of the configured not-done keywords), title, body, tags (autocompleted), SCHEDULED / DEADLINE. `CREATED` is stamped automatically |
| Capture entry points | In-app and share-sheet extension. No widget, Siri, or Shortcuts for now |
| Read views | Agenda (Overdue / Due Today / Upcoming / Next Actions), Inbox, read-only Outline browser with search |
| Not in scope (yet) | Editing existing entries beyond their keyword, meetings/calendar, `:review`, Google integration, iPad |
| Config | A `.orgtd.toml` committed to the org repo, read by both desktop and phone |
| Stack | SwiftUI app + share extension; Go core compiled with `gomobile bind` |
| Code location | This repo, under `ios/` (Xcode project) plus a Go package `mobilecore/` |
| Distribution | Free Xcode personal-team provisioning (7-day re-signing) |

## 3. Architecture

```
┌──────────────────────────── iOS app (SwiftUI) ───────────────────────────┐
│  AgendaView   InboxView   OutlineView/SearchView   CaptureSheet   Settings│
│        │            │              │                    │           │     │
│        └────────────┴──────┬───────┴────────────────────┘           │     │
│                      CoreClient (Swift wrapper, JSON decode)   KeychainKey│
└────────────────────────────┼──────────────────────────────────────────┬──┘
                             │ gomobile bind (Mobilecore.xcframework)   │
┌────────────────────────────▼──────────────────────────────────────────▼──┐
│ mobilecore (Go)                                                           │
│   Sync / Agenda / Inbox / Outline / Search / Tags / Capture / Status      │
│   ├── internal/gitsync   (go-git: clone, fetch, rebuild mobile, push)     │
│   ├── internal/capturequeue (pending-capture JSON queue)                  │
│   ├── internal/agenda    (extracted from internal/ui — shared w/ desktop) │
│   ├── internal/workspace, internal/org, internal/orgdate, internal/config │
└───────────────────────────────────────────────────────────────────────────┘
        ▲
        │ appends to queue only (no git, no Go runtime required)
┌───────┴────────────────┐
│ Share extension (Swift)│
└────────────────────────┘
```

### 3.1 Why a Go core

- **Behavioral parity.** Agenda bucketing, repeater math and missed
  counts, SOMEDAY/`agenda = "skip"` handling, configurable keywords, and
  capture rendering already exist in Go. Porting them to Swift means two
  implementations that drift.
- **Git.** go-git is pure Go and supports SSH (via `x/crypto/ssh`) and
  `ForceWithLease` pushes. In Swift, the only real option is building libgit2
  for iOS yourself, and the Swift wrappers for it are mostly unmaintained.
- **Cost.** gomobile only bridges simple types, so `mobilecore` exposes a
  small, coarse API whose results are JSON strings. Swift decodes them
  into `Codable` structs.

### 3.2 Storage on device

All under the App Group container (shared by app + share extension; see
§7, risk R1):

```
<group>/repo/            go-git working clone (checked out at local `mobile`)
<group>/captures.json    pending-capture queue (source of truth, §4.1)
<group>/sync-state.json  pushed-tip history, last fetch/sync times, last error
```

The SSH private key lives in the Keychain (shared access group), not on
disk. Swift reads it and passes it to `mobilecore.Sync` per call.

## 4. Git flow

### 4.1 The capture queue is the source of truth

`captures.json` holds every capture not yet known to be on `origin/main`:

```json
{
  "id": "6F1C…",               // UUID; queue-internal, never written to org files
  "createdAt": "2026-11-14T09:12:00-08:00",
  "keyword": "TODO",             // optional
  "title": "Call the plumber",
  "body": "re: kitchen sink",
  "tags": ["home"],
  "scheduled": null,
  "deadline": "2026-11-18"
}
```

The git branch is *derived* from the queue every Sync, so the phone
never has to reason about its own commit history.

### 4.2 Capture (no network)

1. Append the capture to `captures.json` (share extension does only this).
2. In the app: `mobilecore.Capture` also appends the rendered entry to
   `inbox_file` in the working tree and commits it on local `mobile`, so
   the read views show it immediately. ("Auto-commit, manual push.")

### 4.3 Sync

```
1. fetch origin (main, mobile)
2. for each pushed tip T recorded in sync-state.json:
       if T is an ancestor of origin/main:
           remove T's capture IDs from the queue; drop T from the history
3. reset local mobile to origin/main (hard; working tree too)
4. for each pending capture, oldest first:
       append rendered entry to inbox_file; commit
           message: "Capture: <title>"   author: settings name/email
5. if local mobile == origin/main: delete remote mobile if it exists (nothing pending)
   else: push local mobile → origin/mobile, force, with lease = last-seen origin/mobile
6. record new tip → [capture IDs it contains] in sync-state.json
```

Properties:

- `origin/mobile` always differs from `origin/main` only by lines
  appended to the end of `inbox_file`.
- If the user merged an *older* pushed tip (fetched before a later
  Sync), step 2 still recognizes exactly the captures that tip held.
- A lease failure (someone else moved `origin/mobile`) aborts the push and
  surfaces an error. The phone never overwrites a branch it didn't write
  last.
- Fetch-only refresh (launch/foreground) runs steps 1–4 locally but
  skips 5–6, so the read views always reflect current `main` + pending
  captures without pushing.

### 4.4 Rendering an appended entry

The rendering must match what desktop `gC` produces (see
`internal/ui/capture.go`, `insertHeadlineAt`):

```org
* Call the plumber                                                :home:
  DEADLINE: <2026-11-18 Wed>
  :PROPERTIES:
  :CREATED:  [2026-11-14 Fri 09:12]
  :END:
  re: kitchen sink
```

- Render the one headline with `internal/org` and append its bytes to the
  file's **raw** contents, after making sure the file ends in a newline. Do
  not re-render the whole file, so the diff is purely an append.
- `CREATED` uses the capture's `createdAt` (the moment you captured it,
  not the moment of Sync).
- Dates: the capture sheet offers a date picker *and* a text field
  parsed by `orgdate` (so `3d`, `2w`, `next tue` mean exactly what `gd`
  accepts on the desktop).

### 4.5 On the Mac

- Merge `mobile` into `main` normally (gitui / jjui / git). Concurrent
  appends to the inbox on both sides are an ordinary merge conflict,
  resolved by keeping both. (The original plan recommended
  `merge=union` for the inbox; that was dropped once the phone could
  edit existing lines, which a union merge would duplicate.)

## 5. Read views

All computed in Go from the working tree (`origin/main` + pending
captures), returned as JSON.

| View | Content | Interaction |
|---|---|---|
| **Agenda** | Overdue, Due Today, Upcoming (`agenda_window_days`), Next Actions, the same rules as desktop `:agenda` minus Meetings | Tap → details in place; pull-to-refresh = fetch |
| **Inbox** | Top-level non-done headlines of `inbox_file`, in file order; pending (unpushed or unmerged) captures badged | Tap → details in place. A pending capture can be **deleted or edited before it's merged**, since it's still only in the queue |
| **Outline** | Every loaded file except `calendar_file`/`meeting_tags_file`, honoring `outline_ignore_files` and `hide_closed_after_hours` (with a toggle), plus a separate read-only Reference section for `reference/` | Chevrons expand/collapse children; tap → details in place |
| **Search** | Case-insensitive substring over title, tags, body, as desktop `/` | Results list → opens the outline at that entry, details open |
| **Entry details** | Opened in place within the list (one at a time), so the entry is seen in context: full title, dates, body with tappable org links, tags, properties, state menu and pencil | The pencil swaps the details for the edit form, also in place (see §10) |
| **Status bar / Settings** | Last fetch, last successful push, number of pending captures, last error; SSH public key (copy button), repo URL, author name/email | |

Editing or deleting a capture that is still pending is allowed (it isn't
an existing entry on `main` yet). The next Sync rebuilds `mobile` from
the queue, so this needs nothing special in git.

## 6. `mobilecore` API (gomobile)

All calls are synchronous; Swift runs them off the main actor. Errors are
returned as Go `error` (bridged to Swift `throws`).

```go
package mobilecore

// Setup / status
func GenerateSSHKey() (pubKeyOpenSSH string, privKeyPEM []byte, err error)
func Clone(groupDir, repoURL string, privKey []byte) error
func Status(groupDir string) (json string, err error)

// Git
func Refresh(groupDir string, privKey []byte) error             // §4.3 steps 1–4
func Sync(groupDir string, privKey []byte, authorName, authorEmail string) error // §4.3 all steps

// Capture
func Capture(groupDir, captureJSON, authorName, authorEmail string) error
func UpdatePendingCapture(groupDir, captureJSON string) error
func DeletePendingCapture(groupDir, id string) error
func ParseDate(input string, nowUnix int64) (isoDate string, err error)
func Tags(groupDir string) (json string, err error)

// Read views
func Agenda(groupDir string, nowUnix int64) (json string, err error)
func Inbox(groupDir string) (json string, err error)
func Outline(groupDir string, showClosed bool, nowUnix int64) (json string, err error)
func Search(groupDir, query string) (json string, err error)
```

Time always comes in from the caller (`nowUnix`), mirroring the desktop's
`m.now()` rule, so tests can pin the clock.

The share extension does **not** link `Mobilecore.xcframework`. It
appends to `captures.json` with a small Swift `Codable` writer (file
coordination via `NSFileCoordinator`), which keeps it within extension
memory limits. The app picks up new entries on next foreground (then
`Capture`-commits them locally).

## 7. Risks / open questions

- **R1: App Groups on free provisioning.** The share extension needs an
  App Group (shared container + Keychain access group). I haven't
  confirmed whether free personal-team signing allows App Groups.
  **Check this first (phase 0).** Fallbacks, in order: (a) expose capture as
  an App Intent, which appears in the share sheet through Shortcuts and runs
  inside the app, so no group is needed; (b) a paid developer account.
- **R2: gomobile + go-git binary size / build friction.** go-git pulls a
  sizeable dependency tree. Acceptable for a personal app, but verify
  the xcframework builds for `ios` and `iossimulator` with the Go version
  in `go.mod`.
- **R3: Clone size.** go-git clones fully into memory-backed packfile
  handling. If the org repo history is large, use a shallow clone
  (`Depth: 1`) for `main`. The merge-detection ancestor check (§4.3 step
  2) then needs enough depth to reach pushed tips. Deepen on demand, or
  fall back to matching `CREATED` + title in `origin/main:inbox_file`.
- **R4: 7-day re-signing** under free provisioning. Data survives
  reinstall-over-top. Document "re-run from Xcode weekly".

## 8. Implementation plan

Each phase ends in something testable. Phases 1–2 are desktop-side
changes that ship on their own and must keep `go test ./...` green and
README/DESIGN.md current (per repo convention).

### Phase 0 — Feasibility spike (½ day)

- Minimal Xcode project in `ios/` with an app + share extension, signed
  with the free personal team, sharing an App Group file and a Keychain
  item. **Answers R1.**
- `gomobile bind` of a hello-world package that imports go-git and does
  an SSH `ls-remote` against the real repo. **Answers R2.**
- Exit: both work on a physical iPhone, or switch to the R1 fallback.

### Phase 1 — Extract `internal/agenda` (desktop refactor)

- Move `agendaEntries`, `agendaSection`, `nextActionHeadlines`,
  `keywordAgenda` (and the `agendaEntry` type) from `internal/ui/agenda.go`
  into a pure package `internal/agenda` taking a `*workspace.Workspace`,
  the resolved `config.Config`, and `today`. `ui` keeps only row building
  (`appendAgendaRows` etc.) on top of it.
- Likewise extract the outline visibility predicates the phone needs
  (stale-`CLOSED` hiding, `outline_ignore_files`, special-file exclusion)
  if they currently live in `ui`.
- Move the pure parts of agenda tests to `internal/agenda`.
- Update DESIGN.md §3 package map and dependency graph.
- Exit: no behavior change; all tests pass.

### Phase 2 — `.orgtd.toml` in the org directory (desktop feature)

- `config.Load` gains a repo-level layer: `<org_dir>/.orgtd.toml`. New
  precedence: flag > `$ORGTD_DIR` (dir only) > `~/.config/orgtd/config.toml`
  > `<org_dir>/.orgtd.toml` > built-in default. The repo file can't set
  `org_dir` itself (circular), or machine-local keys (`editor`,
  `vcs_editor`, `scratch_dir`, `debug`, `[gcalsync]` secrets). Reject or
  ignore those with a warning.
- Note: `org_dir` must be resolved before the repo file is read, which
  changes `resolveConfig`'s order.
- `:config` shows which layer each value came from.
- README "Config file" section updated; tests for precedence.
- Exit: desktop reads keywords/inbox_file/agenda days from the repo
  file; phone can reuse the same loader.

### Phase 3 — Capture rendering & queue (Go, no git)

- `internal/capturequeue`: load/save `captures.json` with atomic writes;
  add/update/delete by ID; tolerant of entries appended by the share
  extension.
- A shared capture renderer used by both desktop `gC`'s template and the
  phone, so `CREATED` format, property drawer and planning line are
  identical. At minimum, a golden test asserting phone output == desktop
  output for the same input.
- `AppendEntry(path, rendered)`: raw-byte append with newline fixup.
- Exit: unit tests incl. round-trip (append → `org.ParseFile` → same
  fields).

### Phase 4 — `internal/gitsync` (Go)

- go-git wrapper: `Clone`, `Fetch`, `RebuildMobile(queue)`, `Push` with
  `ForceWithLease`, ancestor check, pushed-tip history in
  `sync-state.json`. SSH auth from an in-memory key (`ssh.NewPublicKeys`),
  the server's host key pinned (confirmed by fingerprint at setup; see §10).
- Tests against a local bare repo (file:// remote) in a temp dir:
  - capture → sync → `origin/mobile` = main + appended lines only
  - main advances → sync rebuilds on new main, still append-only
  - user merges older tip → only that tip's captures leave the queue
  - user merges latest tip → queue empty, remote `mobile` deleted
  - someone else moves `origin/mobile` → lease failure, nothing pushed
- Exit: all scenarios green with `go test`.

### Phase 5 — `mobilecore` package + xcframework

- Thin facade over phases 1–4 implementing §6, JSON DTOs with stable
  field names (documented in Go doc comments, mirrored as Swift
  `Codable` structs).
- `scripts/build-mobilecore.sh`: `gomobile bind -target=ios,iossimulator
  -o ios/Frameworks/Mobilecore.xcframework ./mobilecore`. Add the
  xcframework to `.gitignore`.
- Exit: Go tests for each API call against `testdata/orgdir`; framework
  builds.

### Phase 6 — iOS app: setup & read views

- Onboarding: generate key → show the restricted `authorized_keys` line
  with a copy button → enter repo SSH URL and author name/email → check
  server (shows its host key fingerprint to verify) → trust and clone.
- `CoreClient` actor wrapping `Mobilecore` calls off the main thread.
- Tab bar: **Inbox** (with a large **Capture** button), **Outline**
  (with search), **Agenda**; a toolbar **Sync** button, whose icon
  shows sync status (running, unreachable, failed). There is no status
  line; Settings has the details.
- Refresh on launch / `scenePhase == .active`; pull-to-refresh on each
  list.
- Keyword colors from config (hex values map directly; ANSI codes
  mapped via a 256-color table).
- Exit: on device, the three views match desktop `:agenda`/outline for
  the same commit.

### Phase 7 — Capture UI & sync

- Capture sheet: title (required), body, tags token field with
  autocomplete from `Tags()`, SCHEDULED/DEADLINE each with a date picker
  and a free-text field run through `ParseDate`.
- Pending captures badged in Inbox; swipe to edit/delete while pending.
- Sync button → `Sync`; failures shown on the Sync button's icon, with
  details in Settings (lease failure, auth failure, network).
- Exit: capture on phone → Sync → merge `mobile` on Mac → desktop orgtd
  shows it in the inbox → phone's next Sync clears its pending badge.

### Phase 8 — Share extension

- Share text or URL → capture sheet prefilled (URL into body; page title
  into title when provided by the host app) → write to `captures.json`
  only.
- App imports new queue entries into the working tree on foreground.
- Exit: share from Safari → item visible as pending in app.

### Phase 9 — Docs

- `ios/README.md`: build prerequisites (Xcode, gomobile), phase-0 signing
  notes, weekly re-sign, server and key setup, recommended `.gitattributes`.
- Root README: mention the iOS companion, `.orgtd.toml`, and the
  `mobile` branch workflow.
- Root DESIGN.md: link here; add `mobilecore`, `internal/agenda`,
  `internal/gitsync`, `internal/capturequeue` to the package map.

## 9. Possible later work (explicitly deferred)

- State changes / edits on the phone (would need stable `:ID:`s and
  operation replay; see design discussion).
- Meetings section (from `calendar.org` as last pushed by the Mac).
- Widgets, Control Center / Action button, Siri / Shortcuts capture.
- iPad layout and hardware-keyboard (vim-style) navigation.
- Desktop orgtd auto-merging `mobile` during `:vcs`.

## 10. As built: departures from the plan

- **Share extension → app handoff.** The extension doesn't append to
  `captures.json`. It writes one file per capture to
  `<group>/incoming/<id>.json`, and `Refresh`/`Sync` fold those into
  the queue (`capturequeue.Import`). The app's Go code can't take part
  in `NSFileCoordinator`, so this way the two processes never write the
  same file.
- **Keychain.** Only the app reads the SSH key, so the Keychain item is
  app-private, with no shared access group.
- **API (§6).** gomobile can't return three values, so
  `GenerateSSHKey` returns a `KeyPair` object. `Refresh`, `Capture`,
  `UpdatePendingCapture` and `DeletePendingCapture` also take the author:
  each one rebuilds `mobile`, which commits. `Capture` returns the new
  ID. The plan's list gained a few calls: `SetTimeZone` (Go can't find
  the device's zone on iOS; `time/tzdata` is embedded), `PublicKeyFor`,
  `PendingCapture` (for the edit sheet) and `RemoveClone`.
- **Deterministic rebuilds.** Each capture commit uses the capture's
  `createdAt` as its author and committer time. Rebuilding the same
  queue on the same base therefore gives the same hashes, so a refresh
  followed by a sync pushes nothing new.
- **Lease.** Before pushing, the phone requires `origin/mobile` to be
  absent or equal to what it last pushed (`lastPushed` in
  `sync-state.json`). The push itself also carries go-git's
  `ForceWithLease`, which catches a move between fetch and push. go-git
  can't lease against a ref that doesn't exist, so the very first push
  relies on the pre-check alone.
- **Pending captures.** The working-tree inbox is the base branch's
  inbox plus one appended headline per queued capture, in order, so the
  last `len(queue)` headlines are the pending ones (less any added last
  at the inbox's top level afterwards; see "Adding entries" below). No
  marker is written to the file. Every view marks them (`entry.Pending`,
  `CaptureID`), so they can be edited as captures or deleted anywhere.
- **Base branch.** It's the remote's default branch at clone time
  (recorded in `sync-state.json`), not a hard-coded `main`. The org
  directory must be the repository root.
- **Shallow clones (R3).** Not implemented. Clones are full.
- **Colors.** The built-in keyword, tag and timestamp colors moved into
  `internal/config` (`Config.KeywordColor`), so both front ends resolve
  them the same way.
- **Any SSH server, not just GitHub.** The URL may be scp-like
  (`user@host:path`) or `ssh://user@host:port/path`, and its user is the
  SSH login (default `git`). There are no built-in host keys: at setup,
  `ScanHostKey` connects without authenticating, preferring an ed25519
  host key, and shows its SHA-256 fingerprint. Once the user confirms it,
  `Clone` records it in `sync-state.json` (`hostKey`). Every later
  connection asks for that key type only and refuses any other key
  (`gitsync.ErrHostKeyMismatch`). A changed server key means removing
  the clone and setting up again.
- **Phone storage.** Everything lives under `<group>/data/`, which is
  excluded from backups and has `NSFileProtectionComplete`. The flags are
  set on the directory, not on individual files: files there are replaced
  by temp-file-and-rename, which would drop a per-file backup exclusion,
  and new files inherit the directory's protection class. The trade-off
  is that captures not yet synced aren't in any backup.
- **Keyword at capture.** A capture may carry a keyword. `mobilecore`
  rejects any keyword that isn't one of the repo config's not-done
  keywords. The share extension can't read the config, so the app
  writes the list to `keywords-cache.json` for it after each refresh.
  A keyword the extension sends from a stale cache isn't re-checked on
  import; if it's no longer configured, the desktop reads it as part of
  the title.
- **Unreachable server.** The phone is often out of reach of its server
  (one on the home network, for instance), so every network operation
  is bounded:
  - The TCP connect and SSH handshake give up after `ConnectTimeout`
    (8 s), once the server has spoken, after `IdleTimeout` (45 s) of
    silence, and overall after `OperationTimeout` (2 min; a clone,
    10 min).
  - go-git doesn't bound the handshake and takes no dialer, so SSH
    connections go through a registered `x/net/proxy` scheme
    (`orgtd-direct`) that dials directly and sets the deadlines.
  - Failures to connect at all are reported as `ErrUnreachable`.
  - On the Swift side, returning to the foreground always runs
    `RefreshLocal` (import share captures and rebuild, no network). It
    fetches only when the back-off allows: at most once a minute, and
    after an unreachable server 1, 5, 15, then 30 minutes.
    Pull-to-refresh and Sync always try.
  - Nothing retries on its own or runs in the background.
- **State changes** (added after the original design, which deferred
  them). The phone can set an entry's TODO keyword, with the same
  effects as desktop `r`: CLOSED, or a repeater's advance and
  LAST_REPEAT. Both front ends now compute this with
  `orgdate.ChangeState`.
  - **Queue.** A change is queued in `state-ops.json`
    (`capturequeue.StateOp`): file, title path, a fingerprint of the
    entry's own text, from/to keyword, and the time, which is also its
    CLOSED stamp, so replays are identical. Captures and changes share
    the pushed-tip bookkeeping and leave their queues the same way.
  - **Replay.** On every rebuild, captures are committed first, so a
    change to a still-pending capture finds it. Then each change is
    replayed as its own commit.
  - **Minimal diff.** `org.Parse` now records each entry's own line
    range (`SrcStart`/`SrcEnd`), and only the lines that change are
    rewritten: the headline line, the planning line if it changed, and
    LAST_REPEAT. The diff is one or two lines, and the rest of the file
    is untouched byte for byte.
  - **Stale changes.** A change whose entry no longer matches (renamed,
    moved, edited or already changed on the desktop) is dropped, not
    guessed at, and reported once (`State.Dropped`, `DismissDropped`).
  - **merge=union guard.** Not recommended anywhere, but a repository
    might already set it: changes are refused in any file
    `.gitattributes` gives `merge=union`, which would duplicate the
    edited line (`gitsync.UnionMerged`).
- **Editing entries.** The same queued change can also carry an
  `EntryEdit`: title, tags, body, SCHEDULED and DEADLINE, each set only
  if the user changed it.
  - `mobilecore.EditEntry` compares the form against the values it was
    filled with (`entry.ScheduledInput`/`DeadlineInput`, dates without
    cookies; the dedented body), so untouched fields aren't rewritten.
  - On replay, `patchEntry` rewrites only what changed: the headline
    line, the planning line, and the body. The body is re-indented to
    its old common indentation, and the property drawer and the entry's
    trailing blank lines are kept.
  - A changed date keeps the old timestamp's repeater and warning
    cookies (`reschedule`).
  - Commit message: "Edit: <new title>". The pending marker follows a
    renamed entry.
- **Adding entries in the outline.** Every list in the Outline (a
  file's top level, or an expanded entry's children) starts and ends
  with a "New entry" row. Tapping one opens the capture fields in place,
  and saving adds the entry first or last in that list
  (`mobilecore.AddEntry`).
  - **Queue.** An addition is another queued change: a `StateOp` with
    `Insert` set (`capturequeue.EntryInsert`: first or last, and the
    entry as a `capture.Capture`). Its title path and fingerprint are
    the parent's, empty for a top-level entry. It shares the replay,
    pushed-tip bookkeeping and dropping of the other changes.
  - **Rendering.** The entry is rendered as a capture is, with CREATED
    (as desktop `o`/`O` stamp it), at the parent's level plus one
    (`capture.RenderAt`).
  - **Minimal diff.** On replay, `applyInsert` splices the entry's lines
    in and touches nothing else. First goes right after the parent's own
    lines, or before the file's first headline. Last goes after the
    parent's last descendant, or at the end of the file. When the line
    before the new entry is blank, the entry gets a trailing blank line
    too, so blank-line spacing stays even.
  - **Stale parents.** If the parent no longer matches (renamed, edited
    or removed on the desktop), the addition is dropped and reported
    like any other change (`DroppedOp.Added`).
  - **IDs.** Entry IDs are index paths, so an entry added first
    renumbers its later siblings. `AddEntry` returns the new entry's ID,
    and the Outline renumbers the expanded rows it holds in the same
    update that shows the new outline (`AddSlot.shiftedForInsert`).
  - **After adding.** A pending added entry can be edited or have its
    state changed like any other entry: those are later ops that find it
    after it's replayed. There's no way to delete one yet.
  - The merge=union guard doesn't apply: an addition only adds lines.
    Reference files take additions, without a keyword.
- **Indenting and outdenting.** Swiping an Outline row right or left
  moves the entry one level, as desktop `>>`/`<<` do
  (`mobilecore.ShiftEntry`): a demote makes it the last child of its
  previous sibling, a promote the next sibling of its parent.
  - **Queue.** Another queued change: a `StateOp` with `Move` set
    (`capturequeue.EntryMove`: the delta, and the entry's title path
    once moved, which marks it pending).
  - **Minimal diff.** `applyMove` changes only the stars of the
    subtree's headline lines. A demoted entry already follows its new
    parent in the file, so nothing moves. A promoted entry's subtree is
    cut and put back after its parent's subtree, past any later
    siblings, which stay children of the parent. Planning, drawer and
    body indentation is left as written: org reads it either way, and
    rewriting it would touch every line of the subtree.
  - **Stale moves.** A move is dropped like other changes if the entry
    changed, and a demote also if the previous sibling isn't the one it
    was made against, so it never lands under a different entry.
  - **IDs.** `ShiftEntry` returns the entry's new ID, and the Outline
    carries its expanded and open rows over to their new IDs
    (`Renumbering`, which also handles additions), expanding the new
    parent so the entry stays in sight.
  - Indenting under a closed entry the Outline is hiding makes the
    entry disappear with it until closed entries are shown.
- **No Inbox tab.** Once the Outline could add entries, its last
  "New entry" row in the inbox file did everything the Inbox tab's
  Capture button did, so the tab, the capture sheet and
  `mobilecore.Inbox` were removed. The tabs are Outline and Agenda. A
  pending capture's Delete moved from the Inbox's swipe action to the
  long-press menu of any of its rows. The share extension still
  captures as before.
