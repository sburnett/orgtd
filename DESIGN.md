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
internal/meetings/      calendar meetings and how entries link to them (pure; cached Index)
internal/extprog/      editor command building and URL formatter running (user-configured programs)
internal/gitrepo/      the git commands :diff/:commit use (root guard, diff, add, commit, push)
internal/execlog/      logged subprocess runner + the timeline :log shows
internal/orgdate/     pure date logic: typed-date parsing, timestamp/repeater parsing and math
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
              ↘ orgdate → org
              ↘ meetings → org
              ↘ extprog → execlog, org
              ↘ gitrepo → execlog
              ↘ execlog
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
- **Tree edits go through `org.Siblings`** (`tree.go`): one ordered list of
  headlines — a parent's children, or a file's top level — with `IndexOf`,
  `Splice` (builds a new slice, so undo records holding the old one stay
  valid), `NearestTo` and `Neighbors`. `File.Move` reparents a subtree and
  shifts its levels; `Headline.ShiftLevel`, `org.Topmost`,
  `SetOrDeleteProperty`/`RestoreProperty`, `TrimmedBody`, `ParseLinks`,
  and `DedentEntry`/`IndentEntry` (the editor buffer's bullet-free form)
  live here too. Nothing in `org` knows about the UI, undo, or a workspace.
- TODO keywords are fixed (`ActiveKeywords`, `DoneKeywords`).

**Round-tripping is format-*preserving*, not byte-exact.** Body text and
preamble are stored as raw lines and survive untouched. Headline lines,
planning lines and property drawers are regenerated from parsed fields,
so incidental spacing on a changed line may shift. (The original design
wanted byte-exact output via raw spans; that was not built.)

### internal/workspace

`Load(dir)` parses every `*.org` directly inside `dir`, plus those
directly inside `dir/reference/` (`ReferenceDir`) — nothing else is
scanned — sorted by path. `IsReference(f)` tells a reference file by its
directory; reference files are ordinary `*org.File`s in `Files`, so
editing, undo, dirty tracking, `:w` and `:diff`/`:commit` need no special
casing. `AcquireLock(dir)` takes an OS-level advisory lock
(`.orgtd.lock`) so two instances can't race to overwrite the same files.
There is no file watching: the directory is read once at startup, plus
explicit reloads (see `finishEditFile` and `finishSyncCalendar` in ui).
`FileOf(h)` and `Locate(h)` find which file, parent and index a headline
sits at.

### internal/calendarsync and internal/gcal

`calendarsync.Sync` authenticates (`gcal.HTTPClient`: token cached in the
OS keychain via go-keyring, browser consent flow if absent), lists events
in a window, and returns a freshly built `*org.File` (`BuildFile`). It
**never touches disk** — the UI writes the file and swaps it into the
workspace. Each event becomes a headline carrying `GCAL_*` properties
(§7). `gcal` is org-agnostic.

### cmd/orgtd and internal/config

**There is one settings value: `config.Config`.** `config` declares the
schema (it's also the TOML file's shape), loads it (`config.Load`), and
owns the built-in defaults (`config.Default`, `Config.ApplyDefaults`).
`main.go` registers the flags (`defineFlags`), loads the file, and
`resolveConfig` (settings.go) overlays — flag > `$ORGTD_DIR` (for `dir`
only) > config file > default — into one fully resolved `Config`, which
goes to `ui.New` as `ui.WithConfig(cfg)` and lives on the model as
`m.cfg`. Precedence rules are documented in README ("Config file").

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
- **"Now" comes from `m.now()`, never `time.Now()`.** `Model.now` returns
  the injected clock (`WithClock`) or the real one, and everything
  time-dependent in `ui` — agenda buckets, `CREATED`/`CLOSED` stamps,
  relative deadline phrases, which meeting is in progress — asks it, so a
  test can run at a fixed moment. Packages below `ui` take `now` as a
  parameter. (`execlog` stamping its own entries is the one exception.)
- **Derived indexes are invalidated in `rebuildRows`.** The meetings
  index (`internal/meetings`, held in `m.meetings`) is a snapshot of every
  loaded file, built lazily and dropped by `rebuildRows`. The invariant
  that makes this safe: **every change to a file's contents (an
  `undoAction` apply/revert, a whole-file reload, a calendar sync) must
  be followed by `rebuildRows`** — which every such path already does,
  since the rows would be stale otherwise. If you add a path that mutates
  headlines without rebuilding rows, the meeting links shown in the
  gutter, agenda and calendar will go stale. Don't hold a
  `m.meetingIndex()` result across a mutation.
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
- **All external commands go through `internal/execlog`** (`execlog.Run`),
  which is what `:log` displays. New subprocess code
  should use it.

## 5. The UI package

`internal/ui` is where nearly all behavior lives, and it is structured
around one big `Model`. This section describes the structure as it is,
including its known weak points, so changes can be made deliberately.

### Files

| File | Responsibility |
|---|---|
| `model.go` | The `Model` struct, `New`, `Update` (mode dispatch), the `mode`/`viewKind` enums, confirm-mode handling, workspace file lookups |
| `options.go` | `WithConfig` (what `main` uses) and a few single-setting `With…` options, mostly for tests |
| `views.go` | The `viewKind`s and the `viewSpecs` registry — everything that varies per view |
| `row.go` | The `row` type and its `rowKind`s, `sameRow`, `rowSearchText` |
| `rows.go` | `rebuildRows` and the outline's row building (fold, hide-done, body lines) |
| `nav.go`, `fold.go` | Cursor motions, viewport/scrolling; fold commands |
| `modes.go` | The `mode`s and the `modeSpecs` registry — each mode's key handler, prompt row, and info-buffer sections |
| `keymap.go` | The `keymap` type and the `normalKeys`/`visualKeys` tables: what every key and two-key chord does, and the dispatch (`runKey`) |
| `keys_normal.go`, `keys_visual.go` | Normal- and visual-mode entry points: the argument chords (`m<letter>`), counts, then the table lookup |
| `commands.go` | `:` command line: input, history, completion, `runCommand`, `:w`/`:wq` |
| `command_table.go` | `commandTable` (every non-view `:` command), lookup, and dispatch |
| `search.go` | `/` `?` `n` `N` incremental search and the search row space |
| `edit_ops.go` | Delete/yank/paste/promote/demote and bulk status changes |
| `status_picker.go`, `deadline.go`, `tag_prompt.go`, `meeting_picker.go` | The `r`/`R`, `gd`, `gt`, `gM` prompts |
| `editor.go` | `$EDITOR` round trip: scratch files, entry context, `finishEdit` (the command line itself is built by `internal/extprog`) |
| `capture.go` | Inserting entries (`o`/`O`/`gC`/`gX`) and calendar-view capture |
| `urlformat.go`, `format_links.go` | Live URL formatting while editing; the `:format-links` batch (both run formatters through `internal/extprog`) |
| `gitops.go` | `:diff`/`:commit` UI flow (confirm prompts, diff rows, background commit+push) over `internal/gitrepo` |
| `clarify.go`, `marks.go`, `jumplist.go` | `:clarify` target, vim-style marks, ctrl-o/`gi` jump list and view switching |
| `style.go` | Color/icon resolution, text-width and highlight helpers |
| `render_rows.go` | Per-row rendering (gutter, outline/agenda/calendar/tags row flavors) |
| `view.go`, `info_buffer.go` | `View`, status line; the info buffer's sections and pinned register/marks |
| `info_views.go` | Rows for the read-only `:help`/`:config`/`:log` views |
| `meeting_index.go` | The cached `meetings.Index` (`m.meetingIndex()`) and its invalidation |
| `meeting_links.go` | Resolving an entry's calendar-meeting links for display |
| `undo.go` | `undoAction` types, undo/redo, dirty derivation, structural-edit helpers (`insertContext`, splice/reparent actions) |
| `agenda.go` | Agenda computation and rows, including the Meetings section (items grouped under upcoming meetings, via the meetings index) |
| `calendar.go`, `meeting_tags.go`, `tags.go` | The `:calendar`, `:meeting-tags` and `:tags` views |
| `repeat.go` | Completing a repeating item (`+1w`, `++1w`, `.+1w`) |
| `sync_calendar.go` | `:sync-calendar` command and result application |
| `util.go` | Tiny shared helpers |

All of these are one package and share `Model`, so the file boundaries are
organizational, not enforced: any file can reach any `Model` field. See
"Known structural debt".

### Rows and views

`Model.rows` is the flat, visible listing; the cursor is an index into
it. `rebuildRows` repopulates it by calling the current view's `build`
function. **Everything that varies per view lives in one `viewSpec`** in
`views.go`'s `viewSpecs` table — its `:command`, status-line label, row
builder, whether its rows fold, the empty-state message, what Enter does,
any `open` preparation (clarify, calendar, diff), and info-buffer lines —
and the rest of the package asks the spec instead of switching on
`m.view`. The views are outline, agenda, clarify, config, log, diff, help,
calendar, meetingTags, tags and reference. A `row` (`row.go`) carries a `kind`
(`rowHeadline`, `rowFile`, `rowBody`, `rowSection`, `rowText`,
`rowAgendaItem`, `rowMeetingHeader`, `rowCalendarEvent`,
`rowMeetingTagsRecord`, `rowCalendarLinked`, `rowTagsItem`) that says
which of its fields apply and how it renders, searches and compares;
`row.go` documents each kind. Most per-entry commands
(`i`, `dd`, `r`, `gd`, marks) work in every headline-backed view because
they resolve the cursor row to its real `*org.Headline` via
`currentHeadline()` and operate on that, not on the row.

Views that are derived (agenda, tags, calendar's linked items) show the
same headline pointer in more than one row; `sameRow` exists to tell
those rows apart (search `n`/`N` depends on it), which is why `row`
carries fields such as `meetingItemTitle` and `linkedFromEvent`.

**Reference files** (`reference/*.org`) are in `m.ws.Files` like any
other, and a view decides what it shows of them: `:reference`
(`appendReferenceRows`) is the only view that lists them as an outline,
and it ignores task state (no stale-DONE hiding in `hiddenAsStaleDone`,
keyword/planning left plain in `renderRowWithBg`, `r`/`R`/`gd` refused by
`refuseTaskStateInReference`); the plain outline skips them
(`isNonOutlineFile`) and the agenda's date and Next Actions sections skip
them. `:tags` and the meetings index (so `:calendar`'s linked items, the
gutter's ▣ marker and the agenda's Meetings section) deliberately include
them — tags are how reference is surfaced. Any lookup of a special file by
base name goes through `m.isNamedFile`, which excludes reference files, so
`reference/inbox.org` is never the inbox.

Special files, by base name from config: the **inbox** file (capture
target, `:clarify` source), the **calendar** file (excluded from the
outline and `:diff`/`:commit`; shown only in `:calendar`; wholesale
regenerated by sync), and the **meeting-tags** file (excluded from the
outline; shown in `:meeting-tags`; durable and committed like any other).

### Modes and input

`Model.mode` selects how a key is interpreted (normal, command, select,
deadline, search, confirm, visual, meetingPicker, tag). Everything that
varies per mode is one `modeSpec` in `modes.go`'s `modeSpecs` table: its
key handler, how its prompt row is drawn, and any info-buffer sections it
adds — `Update`, `View` and the info buffer just look the spec up. Multi-key chords (`gg`, `dd`, `zo`, `m<letter>`,
counts like `3dd`) are driven by one `chord` field (the pending prefix
key) and a `pendingCount`; the keys themselves are `keymap` tables in
`keymap.go`, written `"g g"`, `"z o"`, `"d d"`. Colon commands
are the `commandTable` in `command_table.go` (names, optional argument,
handler); `runCommand` records history and calls `execCommand`, which
dispatches to a view (`viewSpecs`) or a table command. Tab completion
(`commandNames()`) is derived from both, so a command is defined in
exactly one place.

### Rendering

`View` draws `rows[offset:]` that fit, then the info buffer, status line
and command line. The info buffer (`infoBufferLines`) is a stack of
labeled sections whose content depends on mode, view and the current
headline. Colors/icons resolve through small `…Style`/`…Bg` methods with
built-in defaults overridden by `m.cfg.Colors`/`m.cfg.Icons`.
**`View` runs on every keypress, so nothing it calls may be expensive**:
anything that needs workspace-wide knowledge (the gutter's ▣ marker asks
it for every visible row) must go through a cached index like
`m.meetingIndex()`, never rescan the files.

### Known structural debt

These are recorded here so contributors don't mistake them for design:

1. **One `Model` struct, one package.** The files above split the code by
   topic, but they still all share a ~100-field `Model`, so nothing stops
   a file from touching state that isn't its own. Helpers that don't need
   `Model` have been moving out — date logic (`internal/orgdate`),
   subprocess logging (`internal/execlog`), git (`internal/gitrepo`),
   editor and URL-formatter programs (`internal/extprog`), meeting linking
   (`internal/meetings`), and org link parsing and tree operations
   (`internal/org`) — but the remaining ~30 files still share the
   one `Model`.

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

**Add a colon command.** (A command that just opens a view is a `viewSpec`
— see below.) Add an entry to `commandTable` (`command_table.go`): its
names, whether it takes an argument, and its handler. Completion and
dispatch need no other edits. Document it in README's command table and the
`:help`-visible text (README is embedded). Add a key-driven test.

**Add a normal-mode key or chord.** Add a `bind` line to `buildNormalKeys`
(`keymap.go`): the key (`"x"`, or `"g x"` for a chord — its prefix is
registered automatically) and the action. Use `bindKeepView` if the action
manages the viewport or mode itself. If it should work in visual mode,
bind it in `buildVisualKeys` too (shared cursor motions are in
`addMotions`). Document it in the README's keybinding tables.

**Add an undoable edit.** Implement `undoAction` in `undo.go` (mutate in
place; record old and new values), build it where the edit happens and
call `pushUndo`. Never set `m.dirty` directly.

**Add a mode** (a new prompt or sub-state). Add a `mode` constant (before
`numModes`), an `update` function, and an entry in `modeSpecs` (`modes.go`)
with its `prompt` and any `infoAbove`/`infoBelow`; enter it by setting
`m.mode`. `TestEveryModeHasAnUpdateHandler` fails if the spec is missing.

**Add a view.** Add a `viewKind` constant (before `numViews`) and an
entry in `viewSpecs` (`views.go`): its `command`, `label`, a `build`
function that appends rows to `dst`, and any of the optional fields
(`folds`, `empty`, `enter`, `open`, ...). Render any new row flavor by
adding a `rowKind` and a case in `renderRowWithBg`, and decide how
`sameRow` distinguishes duplicate rows. The `:command`, Tab completion,
status label, empty message and Enter behavior need no other edits;
`TestEveryViewHasACompleteSpec` fails if the spec is incomplete.

**Add a config setting.** Add the field (with its `toml` tag) to
`config.Config` — or one of its sections — and, if it has a built-in
default, to `config.Default` and `ApplyDefaults`. If it needs a flag, add
it in `defineFlags` and an `if f.explicit[...]` line in `resolveConfig`.
Read it in `ui` as `m.cfg.<Field>`. Add a line to the `:config` listing
(`appendConfigRows`), `config.example.toml`, and the README.
`TestReadmeDocumentsEveryFlagAndConfigKey` fails if the README misses it.

**Run an external program.** Use `execlog.Run` so it appears in
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
