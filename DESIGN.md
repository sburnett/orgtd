# orgtd Design Document

This document describes how orgtd is **actually built**: its packages, its
data flow, and the invariants the code relies on. For what the program
*does* from a user's point of view (keys, commands, views, config), the
source of truth is [README.md](README.md). For the original vision, see
[CONCEPT.md](CONCEPT.md) — some of it was never built (see §10).

Keep this file current: when you move code between packages or change an
invariant listed here, update it in the same commit.

## 1. Overview

orgtd is a Go terminal UI (Bubble Tea) for a GTD workflow over plain
`.org` files. It loads every `.org` file in one directory into memory,
lets you navigate/edit/reorganize the outline with vim-flavored keys,
computes several derived views (agenda, calendar, tags, clarify), and can
sync Google Calendar events into one of the org files.

## 2. Goals & Non-Goals

**Goals**
- A single, opinionated GTD workflow, not a general org-mode client.
- Files stay valid org-mode; Emacs can open and edit them at any time.
- The UI never blocks on slow work (network, `git push`, external
  formatters) — those run as `tea.Cmd`s and report back by message.
- Vim-flavored modal keys operating on whole outline entries, not
  characters or lines.

**Non-goals**
- Arbitrary org syntax (tables, babel, LaTeX...). It is preserved as
  opaque body text but never generated or interpreted.
- Write access to Google services (the integration is read-only).
- Syncing the org directory itself between machines, or watching it for
  external changes (see §6).

## 3. Package map

```
readme.go             embeds README.md for :help (package orgtd)
cmd/orgtd/            main: flags + config + env → settings → ui.New. Wiring only.
internal/config/      TOML config file schema and loading (no precedence logic)
internal/org/         org data model, parser, renderer, atomic writer, deep clone
internal/workspace/   loads a directory of .org files; advisory directory lock
internal/ui/          the Bubble Tea application (nearly all behavior; see §5)
internal/calendarsync/ turns Google Calendar events into an *org.File
internal/gcal/        thin Google Calendar API client + OAuth installed-app flow
scripts/              example URL-formatter script
testdata/orgdir/      fixture workspace used by ui tests
```

Dependency direction (no cycles, keep it this way):

```
cmd/orgtd → ui → calendarsync → gcal
              ↘ workspace → org
              ↘ org
cmd/orgtd → config, workspace
```

`org` and `gcal` know nothing about the UI. `calendarsync` knows org and
gcal but not the UI. Only `ui` knows about everything.

### internal/org

- `Headline` is a tree node: `Level`, `Keyword`, `Priority`, `Title`,
  `Tags`, `Scheduled`/`Deadline`/`Closed` (`*Timestamp`, which keeps only
  the raw text between brackets), `Properties` + `PropertyOrder`, `Body`
  (raw lines), `Parent`, `Children`.
- `File` is `Path`, `Preamble` (raw lines before the first headline) and
  top-level `Headlines`.
- `Parse`/`ParseFile` build the tree; `RenderFile`/`RenderHeadline`/
  `RenderEntry` serialize it; `WriteFile` writes atomically (temp file in
  the same directory, then rename).
- TODO keywords are fixed (`ActiveKeywords`, `DoneKeywords`).

**Round-tripping is format-*preserving*, not byte-exact.** Body text and
preamble are stored as raw lines and survive untouched. Headline lines,
planning lines and property drawers are regenerated from parsed fields,
so incidental spacing on a changed line may shift. (The original design
wanted byte-exact output via raw spans; that was not built.)

### internal/workspace

`Load(dir)` parses every `*.org` directly inside `dir` (non-recursive,
sorted by path). `AcquireLock(dir)` takes an OS-level advisory lock
(`.orgtd.lock`) so two instances can't race to overwrite the same files.
There is no file watching: the directory is read once at startup, plus
explicit reloads (see `finishEditFile` and `finishSyncCalendar` in ui).

### internal/calendarsync and internal/gcal

`calendarsync.Sync` authenticates (`gcal.HTTPClient`: token cached in the
OS keychain via go-keyring, browser consent flow if absent), lists events
in a window, and returns a freshly built `*org.File` (`BuildFile`). It
**never touches disk** — the UI writes the file and swaps it into the
workspace. Each event becomes a headline carrying `GCAL_*` properties
(§7). `gcal` is org-agnostic.

### cmd/orgtd and internal/config

`main.go` parses flags, loads the config file (`config.Load`), and
`resolveSettings` (settings.go) merges flag > `$ORGTD_DIR` (for `dir`
only) > config file > default into one `settings` struct, which main
passes to `ui.New` as `With…` options. Precedence rules are documented in
README ("Config file"). `config` only declares and parses the schema.

## 4. Runtime model

Bubble Tea: `Model`, `Update`, `View`.

- **The org tree in `Workspace` is the single in-memory source of truth.**
  Every edit mutates it immediately; nothing touches disk until `:w`
  (`writeAll` → `org.WriteFile` per dirty file). This is deliberately
  *not* the "flush every mutation" model the original design proposed.
- **Undo** is one global stack of `undoAction`s (`undo.go`). Each action
  knows how to `apply`/`revert` itself, which `file()` it touches, and
  which headlines are `affected()`. **Dirty state is derived**, never set
  by hand: `recomputeDirty` compares each file's applied-action count to
  its `savedPos` (the count at last write). So undoing back to the saved
  point makes a file clean again. Any new edit *must* go through
  `pushUndo` (or a variant) to be undoable and to mark the file dirty.
- **Headlines are identified by pointer.** `collapsed`, `marks`,
  `register`, `immutable`, `clarifyTarget`, jump-list entries and
  undo actions all hold `*org.Headline`. Anything that replaces a file's
  tree wholesale (editing a whole file in `$EDITOR`, `:sync-calendar`)
  must call `clearRefsForFile` so no stale pointers survive. Undo/redo
  of structural edits relies on the same pointers being re-inserted.
- **Slow work runs as a `tea.Cmd`** that returns a message handled in
  `Update`: `:sync-calendar` (`syncCalendarMsg`), `:format-links`
  (`formatLinksMsg`), `:commit` (`commitPushMsg`), and the `$EDITOR`
  round trip (`editFinishedMsg`, `fileEditFinishedMsg`). While
  `:format-links` is in flight, affected entries are in `m.immutable` and
  every mutating command checks `refuseIfImmutable`/`filterImmutable`.
- **Not everything is async.** `:w`, `:diff` (a `git diff`), scratch-file
  writes and the live in-editor URL formatter run synchronously inside
  `Update`. They are fast in practice; if one ever isn't, convert it to a
  `tea.Cmd` following the `:commit` pattern.
- **All external commands go through `execLog`** (`exec_log.go`:
  `runLoggedCommand`), which is what `:log` displays. New subprocess code
  should use it.

## 5. The UI package

`internal/ui` is where nearly all behavior lives, and it is structured
around one big `Model`. This section describes the structure as it is,
including its known weak points, so changes can be made deliberately.

### Files

| File | Responsibility |
|---|---|
| `model.go` | The `Model` struct, option funcs, key handling for every mode, commands, navigation, folding, search, editing/capture, URL formatting, git diff/commit, and all rendering. **~8,000 lines; see "Known structural debt".** |
| `undo.go` | `undoAction` types, undo/redo, dirty derivation, structural-edit helpers (`insertContext`, splice/reparent actions) |
| `agenda.go` | Agenda computation and rows, repeater math, **and** the meeting-linking domain (`meetingIDKind`, `meetingCandidate`, tag matching, `entriesForMeeting`) |
| `calendar.go` | `:calendar` rows, linked-items ordering |
| `meeting_tags.go` | `meeting-tags.org` records and the `:meeting-tags` view |
| `tags.go` | `:tags` view |
| `repeat.go` | Completing a repeating item (`+1w`, `++1w`, `.+1w`) |
| `sync_calendar.go` | `:sync-calendar` command and result application |
| `exec_log.go` | Subprocess logging for `:log` |

### Rows and views

`Model.rows` is the flat, visible listing; the cursor is an index into
it. `rebuildRows` repopulates it for the current `viewKind`
(outline, agenda, clarify, config, log, diff, help, calendar,
meetingTags, tags). A `row` is a single struct with a flag per row
flavor (`isAgendaItem`, `isMeetingHeader`, `isCalendarItem`,
`isTagsItem`, `isTextLine`, `isBodyLine`, ...). Most per-entry commands
(`i`, `dd`, `r`, `gd`, marks) work in every headline-backed view because
they resolve the cursor row to its real `*org.Headline` via
`currentHeadline()` and operate on that, not on the row.

Views that are derived (agenda, tags, calendar's linked items) show the
same headline pointer in more than one row; `sameRow` exists to tell
those rows apart (search `n`/`N` depends on it), which is why `row`
carries fields such as `meetingItemTitle` and `linkedFromEvent`.

Special files, by base name from config: the **inbox** file (capture
target, `:clarify` source), the **calendar** file (excluded from the
outline and `:diff`/`:commit`; shown only in `:calendar`; wholesale
regenerated by sync), and the **meeting-tags** file (excluded from the
outline; shown in `:meeting-tags`; durable and committed like any other).

### Modes and input

`Model.mode` selects which `update*Mode` function interprets a key
(normal, command, select, deadline, search, confirm, visual,
meetingPicker, tag). Multi-key chords (`gg`, `dd`, `zo`, `m<letter>`,
counts like `3dd`) are tracked by individual `pending*` booleans and a
`pendingCount`, reset at the top of `updateNormalMode`. Colon commands
are a `switch` in `runCommand`, and `Tab` completion has its own
separate `commandNames` list — **adding a command means editing both**.

### Rendering

`View` draws `rows[offset:]` that fit, then the info buffer, status line
and command line. The info buffer (`infoBufferLines`) is a stack of
labeled sections whose content depends on mode, view and the current
headline. Colors/icons resolve through small `…Style`/`…Bg` methods with
built-in defaults overridden by `ColorOverrides`/`With…Icon` options.
**`View` runs on every keypress, so nothing it calls may be expensive**
(see debt item 2).

### Known structural debt

These are recorded here so contributors don't mistake them for design:

1. **`model.go` is too large** and mixes unrelated concerns. Pure helpers
   that don't need `Model` (git operations, date parsing, URL detection
   and formatting, editor-command construction) are good candidates to
   become their own files, then packages.
2. **Meeting linking is recomputed live and is expensive.** The gutter's
   `meetingColumn` calls `tagLinkedMeetingCandidates` for each visible
   tagged row on every render, which walks the whole workspace
   (`meetingCandidates` → `meetingTags` per candidate). It wants a cached
   index invalidated on edit/sync/undo.
3. **`row` is a tagged union without a tag**, and view-specific behavior
   is spread across `m.view ==` checks, `rebuildRows`, `View`'s
   empty-state text, `jumpToSource`, and per-view render functions.
4. **Settings are threaded through ~8 layers** (config struct, flag,
   `flagValues`, `settings`, `resolveSettings`, `With…` option, `Model`
   field, `:config` view, README, `config.example.toml`).
5. **Domain logic that belongs in `org`** lives in `ui`: timestamp
   parsing (`parseTimestampDate`, repeater math, `headlineCreatedTime`),
   and tree operations like `shiftHeadlineLevel`.
6. **`time.Now()` is called directly** in render and command paths.

## 7. Data conventions (properties and tags)

orgtd stores its own metadata as org properties, so it round-trips
through Emacs:

| Property | Set by | Meaning |
|---|---|---|
| `CREATED` | every new entry | Inactive timestamp of creation; orders items linked to a meeting |
| `LAST_REPEAT` | completing a repeating item | When it was last completed (instead of `CLOSED`) |
| `GCAL_EVENT_ID`, `GCAL_RECURRING_EVENT_ID`, `GCAL_CALENDAR_ID`, `GCAL_START`, `GCAL_END`, `GCAL_HTML_LINK`, `GCAL_SELF_RESPONSE_STATUS` | `:sync-calendar`, in the calendar file only | Identify and describe a synced event |
| `GCAL_EVENT_IDS` / `GCAL_RECURRING_EVENT_IDS` | `gM` | Attach an entry to one-off events / recurring series |
| `GCAL_EVENT_LINKS` / `GCAL_RECURRING_EVENT_LINKS` | `gM` | Title+URL snapshot taken at attach time, so the meeting still displays after it ages out of the sync window |
| `MEETING_TAG_EVENT_IDS` / `MEETING_TAG_RECURRING_EVENT_IDS` | `gt` on a calendar entry | In `meeting-tags.org`: which meetings a durable tag record applies to |

Tags matter semantically: an entry is **linked to a meeting** if it is
attached via `gM`, *or* shares a tag with the event (the event's own
tags, e.g. `@alice`, plus any `meeting-tags.org` tag for it). The
`recurring` tag is reserved and never counts as a match.

### Project↔meeting association

The product idea (CONCEPT.md): associate tasks and projects with Google
Calendar meetings so the agenda can surface them around the meeting. A
*recurring* series is identified by `GCAL_RECURRING_EVENT_ID` (stable
across occurrences); a *one-off* event by `GCAL_EVENT_ID`. Attaching to
the series ID, not an occurrence, is what lets something raised in last
week's standup show up before this week's. Hand-writing
`GCAL_EVENT_IDS`/`GCAL_RECURRING_EVENT_IDS` on any entry works the same
as `gM`.

## 8. Testing

- `internal/org`, `internal/config`, `internal/calendarsync`,
  `internal/gcal`, `internal/workspace`, `cmd/orgtd` have conventional
  unit tests.
- `internal/ui` tests construct a real `Model` over a workspace loaded
  from `testdata/orgdir` and drive it with simulated key presses
  (`sendKey`, `typeKeys`, helpers in `model_test.go`), asserting on
  `m.rows`, tree state, and rendered output. Tests that write to disk
  first copy the fixtures into a temp dir (`loadFixtureCopy`) — never
  write to the checked-in fixtures.
- There is no golden byte-for-byte round-trip suite (see §3, org).
- Run everything with `go build ./... && go vet ./... && go test ./...`.

## 9. Recipes for common changes

**Add a colon command.** Add a `case` in `runCommand` *and* the name in
`commandNames`; document it in README's command table and the
`:help`-visible text (README is embedded). Add a key-driven test.

**Add a normal-mode key or chord.** Edit `updateNormalMode`; if it's a
chord prefix add a `pending*` flag, reset it with the others, and if it
should extend a selection also handle it in `updateVisualMode`. Document
in the README's keybinding tables.

**Add an undoable edit.** Implement `undoAction` in `undo.go` (mutate in
place; record old and new values), build it where the edit happens and
call `pushUndo`. Never set `m.dirty` directly.

**Add a view.** Add a `viewKind`; handle it in `rebuildRows`, `View`'s
empty-state message, `jumpToSource` if rows can jump to the outline,
`usesOutlineRows`, and add its `:name` command. Render new row flavors in
`renderRowWithBg`. Decide how `sameRow` distinguishes duplicate rows.

**Add a config setting.** Add it to `config.Config`, the flag in
`main.go`, `flagValues`/`settings`/`resolveSettings`, a `With…` option and
`Model` field, the `:config` listing (`appendConfigRows`),
`config.example.toml`, and the README tables. (Debt item 4.)

**Run an external program.** Use `runLoggedCommand` so it appears in
`:log`; run it from a `tea.Cmd` unless it is guaranteed fast.

## 10. Original ideas that were not built

CONCEPT.md's vision still describes the direction, but these pieces do
not exist, so don't look for them in the code:

- **Guided inbox processing** (a `p` key walking an item into a
  project). `:clarify` is the implemented workflow instead: it pins the
  inbox's next item while you navigate and re-file it by hand.
- **Meeting capture from Google Docs action items.** Only Calendar is
  integrated; there is no Docs/Drive/Meet client. Capture-time meeting
  attachment is `gX`/`gM`, and `o`/`O` in `:calendar`.
- **Review ("snippets") mode** over completed items by day/week/month.
  Done items are only hidden after `hide_done_after_hours`
  (`:toggledone`).
- **Background calendar polling and `R`/`:refresh`.** Sync is manual
  (`:sync-calendar`).
- **`.orgtd/` tool-managed directory, `agenda.org` output, `:ID:` and
  `CAPTURED_AT` properties, `projects.org` as a special file.** Agenda
  is computed in memory only; the only special files are inbox, calendar
  and meeting-tags (by configurable base name).
- **File watching / auto-reload of external edits** and
  **flush-on-every-mutation.**

## 11. Open questions

- Whether TODO keywords should become configurable (everything from the
  state picker to the agenda hardcodes the current six).
- Archiving finished items; `DONE` entries currently just hide.
- External edits to a file while orgtd holds unsaved changes to it: the
  directory lock only prevents a second orgtd, not Emacs. `:w` is
  last-write-wins.
