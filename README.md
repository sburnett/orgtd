# orgtd

A terminal UI for working through a GTD-style workflow directly on plain
`.org` files, with vim-flavored keybindings. It's a viewer and editor —
navigate, fold, edit, reorganize, and search your outline — plus an
agenda view computed from `SCHEDULED`/`DEADLINE` dates and `NEXT` items,
and a "clarify" mode for working through an inbox one item at a time.

Every edit is applied in memory immediately; nothing touches disk until
you `:w`. Files are otherwise your own — orgtd never manages a directory
structure or format beyond what's described below, and any editor
(including real Emacs org-mode) can open the same files.

> This whole project — design, implementation, and this README — was
> vibe-coded with [Claude Code](https://claude.com/claude-code).

## Install

```sh
go install github.com/sburnett/orgtd/cmd/orgtd@latest
```

or from a checkout:

```sh
go build -o orgtd ./cmd/orgtd
```

## Usage

```sh
orgtd --dir ~/org
```

| Flag | Config key | Default | Meaning |
|---|---|---|---|
| `--dir` | `org_dir` | `$ORGTD_DIR`, then `org_dir`, then `~/org` | Directory containing `.org` files (loaded non-recursively) |
| `--url-formatter` | `url_formatter` | *(disabled)* | External program invoked as `<prog> <url>` to convert a bare URL typed while editing into an org-mode link (its stdout replaces the URL). May include extra arguments, e.g. `myformatter --template "a template"` — split shell-style (a double- or single-quoted argument can contain spaces), and a leading `~` in any word is expanded, same as typing it in a shell. Also used by `:format-links` (see below) unless `--format-links-url-formatter` overrides it |
| `--url-formatter-prefixes` | `url_formatter_prefixes` | *(none)* | Extra bare-URL prefixes recognized beyond `http://`/`https://`, e.g. `bit.ly/` or `go/` for shortlinks — comma-separated on the flag, a TOML array in the config file. Each is only matched at a word boundary, so `go/` won't match inside `embargo/foo` |
| `--format-links-url-formatter` | `format_links_url_formatter` | *(same as `url_formatter`)* | External program `:format-links` (see below) invokes in batch mode — called with no trailing URL argument, it should instead read URLs one per line from stdin and print the same number of formatted lines to stdout. Configured separately from `url_formatter` since a batch-capable command may differ from (or take different arguments than) whatever handles a single URL while editing |
| `--agenda-days` | `agenda_window_days` | `14` | How many days ahead the agenda view's "Upcoming" section covers |
| `--inbox-file` | `inbox_file` | `inbox.org` | Base name of the file `:clarify` treats as the inbox |
| `--calendar-file` | `calendar_file` | `calendar.org` | Base name of the file (e.g. one gcalsync writes) excluded from the outline view and shown instead, grouped by day, in the `:calendar` view |
| `--hide-done-after-hours` | `hide_done_after_hours` | `24` | How many hours after a `DONE`/`CANCELLED` item's `CLOSED` timestamp it's hidden from the outline view (and its whole subtree with it). `:toggledone` shows everything again, and toggles back |
| `--editor` | `editor` | `$EDITOR`, then `vim` | External editor launched for `i` and file edits |
| `--debug` | `debug` | *(off)* | Log debug info (see Debug log, below) to `debug.log` next to the config file |
| `--config` | — | `$XDG_CONFIG_HOME/orgtd/config.toml` (or `~/.config/orgtd/config.toml`) | Path to the config file below |

`:config` shows the current, effective value of everything above.

Every `.org` file directly inside `--dir` is loaded, sorted alphabetically.
Subdirectories are not scanned.

orgtd takes an exclusive lock on `--dir` for as long as it's running (a
`.orgtd.lock` file inside it), so a second orgtd instance accidentally
started against the same directory refuses to run instead of racing the
first one to overwrite the same files — it exits immediately with an
error rather than opening. Released automatically when orgtd exits, for
any reason (including a crash), so there's never a stale lock to clean
up by hand. This is an OS-level advisory lock, so it's a best-effort
safety net rather than a hard guarantee — some filesystems (older NFS
in particular) don't enforce it reliably.

### Config file

Any setting above can also go in a TOML config file, so you don't have to
repeat flags on every invocation. An explicit flag always overrides the
config file; `--dir` can additionally be overridden by `$ORGTD_DIR`,
which takes precedence over the config file but not over an explicit
flag. The config file itself, and every key in it, is optional — orgtd
works fine with none of it present.

```toml
org_dir = "~/org"          # "~" is expanded
editor = "emacsclient -t"  # falls back to $EDITOR, then vim, if unset
url_formatter = ""
url_formatter_prefixes = ["bit.ly/", "go/"]
format_links_url_formatter = ""  # falls back to url_formatter if unset
agenda_window_days = 14
inbox_file = "inbox.org"
calendar_file = "calendar.org"
hide_done_after_hours = 24
debug = false
```

### Debug log

Off by default. With `--debug` (or `debug = true` in the config file),
orgtd logs to a `debug.log` file next to whichever config file it loaded
(or would load — the location doesn't depend on one actually existing),
e.g. `~/.config/orgtd/debug.log`. The main use today is the URL
formatter: every attempt is logged with the exact command run, and any
failure includes the subprocess's own stderr — useful for tracking down
why a formatter that works on one machine doesn't on another (a bad
path, a missing interpreter, a script erroring out) without needing to
leave the TUI mid-edit to find out. A failure also shows a message on
orgtd's own status line pointing at the log, whether or not debug
logging is on.

## Views

- **Outline** (default) — every loaded file except `calendar_file` (see
  the `:calendar` view, below), its headlines, and any free-text body
  underneath them, all foldable. A `DONE`/`CANCELLED` headline (and its
  whole subtree) whose `CLOSED` timestamp is older than
  `hide_done_after_hours` (default 24) is hidden — `:toggledone` shows
  everything again, and toggles back.
- **Calendar** (`:calendar`) — every event in `calendar_file` (default
  `calendar.org`; see gcalsync, below), grouped under one flush-left
  header per calendar day, both the days and the events within each day
  in chronological order. This is the only place `calendar_file`'s
  contents are shown, since it's excluded from the outline view (above)
  entirely. Each event shows its time before its title (`14:00-14:30
  Standup`, or `All day` for an all-day event) in place of a TODO
  keyword, and starts folded — its Location/description/link body is
  detail you don't need at a glance, so it stays one `Tab` away rather
  than cluttering every day's listing by default (an event you've
  explicitly unfolded stays that way across redraws). The link is still
  always one glance away regardless — on the status line, same as any
  other entry's link (see above). Otherwise an ordinary foldable list of
  headlines — `i`, `dd`, `r`, `gd`, marks, and every other per-entry
  command all work exactly as they do in the outline, though any edit
  only lasts until gcalsync's next sync overwrites the file regardless.
- **Agenda** (`:agenda`) — a flat, date-driven view across every file:
  **Overdue**, **Due Today**, and **Upcoming** sections built from
  `SCHEDULED`/`DEADLINE` timestamps, plus a **Next Actions** section
  listing every `NEXT`-keyword headline regardless of whether it has a
  date. `SOMEDAY` items are excluded entirely. An item with both a
  schedule and a deadline can appear in two sections. A **Meetings**
  section follows: for each calendar meeting gcalsync has synced —
  a recurring series or a one-off event alike — that starts sometime
  today or within the next 24 hours (current ones included), a header
  row grouping every item — anywhere in the org directory,
  `DONE`/`CANCELLED` excluded — whose `GCAL_RECURRING_EVENT_IDS` (for a
  recurring series) or `GCAL_EVENT_IDS` (for a one-off event) property
  (set by `gM`, see below) names that same meeting, i.e. something
  raised during a past occurrence (or, for a one-off event, just
  attached ahead of time) that might need attention this time around.
  Meetings are in chronological order; a meeting with nothing linked to
  it is left out entirely, and an item linked to more than one meeting
  legitimately shows up under each.
- **Clarify** (`:clarify`) — pins the inbox's first non-`DONE`/`CANCELLED`
  top-level headline to the top of the screen, alongside its `CREATED`
  property (so you can see how long it's been sitting there) and any
  `SCHEDULED`/`DEADLINE` it already has, while you navigate the rest of
  the outline to file it away; deleting the pinned item, or marking it
  `DONE`/`CANCELLED`, advances to the next pending one. `:next` / `:prev`
  step to the next/previous pending inbox item manually, and `:outline`
  returns to the plain outline from either view.
- **Config** (`:config`) — a read-only listing of every configurable
  setting's current, effective value (after flags/config
  file/built-in-default resolution), including whether hide-done
  filtering is currently on or off. `:outline` returns to the outline.
- **Log** (`:log`) — a read-only, chronological list of every external
  command orgtd has run since it started (`$EDITOR`, and any configured
  URL formatter, live or batch): a line when it starts (with its pid and
  full argument list), one line per line fed to its stdin (if any, e.g.
  every URL a `:format-links` batch sends), one line per stdout/stderr
  line as it's produced, and a line for its exit code, each individually
  timestamped, tagged `START`/`STDIN`/`STDOUT`/`STDERR`/`EXIT`, and
  marked with the process's pid (`-` if it never actually started) —
  useful for telling apart two commands that happen to run at once, e.g.
  a `:format-links` batch alongside a live in-editor formatter
  invocation. Doesn't live-update — re-run `:log` to see anything logged
  since it was last opened.
  `:outline` returns to the outline.
- **Diff** (`:diff`) — the raw `git diff HEAD` output for every file
  currently open in the outline. Refuses outright unless the org
  directory is itself the *root* of its git repository (same
  requirement, and the same reason, as `:commit` below) — since a diff
  can't offer to `git add` an untracked file it isn't safe to touch,
  showing one at all from a nested workspace (e.g. this project's own
  `testdata/orgdir`) would just be misleading about what `:commit` could
  actually do with it. When it does run: if any open file isn't tracked
  by git yet, asks first (`[y/N]`) whether to `git add` it — declining
  just leaves it out of the diff. Shows a placeholder if there are no
  changes or no files open. Also recorded in `:log`, like any other
  external command. `:outline` returns to the outline. `:commit` (only
  available here — see below) commits and pushes what `:diff` is
  showing.
- **Help** (`:help`) — this README, rendered as terminal-styled markdown
  (via [glamour](https://github.com/charmbracelet/glamour) — headings,
  tables, code blocks and all), embedded into the binary at build time
  (see `go:embed`) so it's available even if README.md isn't sitting
  next to wherever the binary was installed. Re-wraps automatically if
  the terminal is resized while it's open. `:outline` returns to the
  outline.

Marks (see below) stay pinned at the top of the screen in every view.

The bottom of the screen is always split into two lines, like vim's own
statusline-above-command-line layout: the top one is the status line
(current view/directory, item position, and any link on the current
entry — see the tables above for what each view's "place" shows there),
always visible regardless of mode; the bottom one is the command line —
where `:`/`/`/`?` input, prompts (deadline, commit message, the status
picker), the visual-mode banner, and messages all appear, blank when
there's nothing to show. Neither ever replaces the other.

"Any link" includes an org-mode link literally in the entry's title; if
the entry is itself a synced calendar event (i.e. you're browsing
`:calendar`, above), its own `GCAL_HTML_LINK`; and, if the entry has
been attached to a meeting via `gM` (see below) — a recurring series or
a one-off event alike — one `<meeting name>: <url>` entry per attached
meeting, read from its `GCAL_RECURRING_EVENT_LINKS` (recurring) or
`GCAL_EVENT_LINKS` (one-off) property. Since that property is a
snapshot taken at attach time rather than a live lookup, it keeps
working indefinitely — you can jump straight to the meeting no matter
how long ago it was attached, even long after gcalsync has resynced
`calendar.org` and the meeting no longer has anything cached. A
`GCAL_RECURRING_EVENT_IDS`/`GCAL_EVENT_IDS` with no matching
`GCAL_RECURRING_EVENT_LINKS`/`GCAL_EVENT_LINKS` entry (e.g. one
hand-attached to a task directly, per DESIGN.md's project↔meeting
association, rather than via `gM`) falls back to resolving those IDs
against whatever `calendar.org` currently has cached, which — unlike
the `..._LINKS` properties — can come up empty if the meeting has aged
out; an ID that resolves neither way is simply left off.

## Keybindings

Everything operates on whole outline entries, not characters — `dd`
deletes a headline and its subtree, `j`/`k` move entry by entry (an
entry's own body text moves and highlights with it, never as a separate
stop), and so on.

### Navigation

| Key | Action |
|---|---|
| `j` / `k` / `↓` / `↑` | Move to the next/previous entry |
| `gg` / `G` | Jump to the first / last entry |
| `{` / `}` | Jump to the previous/next entry at the same level, hopping up a level once there's nothing more at this one (mirrors vim's paragraph motion) |
| `l` / `h` | Move one level deeper (into the first child) / shallower (to the parent, or the file header for a top-level entry), falling back to `{`/`}`-style movement when there's nowhere deeper/shallower to go |
| `^` / `$` | Move to the top / bottom of the current level (parent or file header; last child) |
| `ctrl-d` / `ctrl-u` | Half-page down / up |
| `Page Down` / `Page Up` | Full-page down / up |
| `ctrl-e` / `ctrl-y` | Scroll the view down/up by one line, like vim — the cursor stays put unless the scroll would push it off-screen, in which case it's dragged along just enough to stay visible |
| `Tab`, `za`/`zo`/`zc`/`zA`/`zO`/`zC` | Toggle / open / close a fold, one level (lowercase) or recursively (uppercase) |
| `Enter` | In agenda view, jump to that item's real place in the outline |
| `ctrl-o` / `gi` | Jump back / forward through the jump list — vim's own `ctrl-o`/`ctrl-i`, tracking where you were before a "large" move: `gg`/`G`, `{`/`}`, a confirmed search (`/`/`?`, not `n`/`N` repeats), jumping to a mark (`'<letter>`) or to the clarify target (`gc`), and switching views entirely (`:agenda`, `Enter` from it, `:calendar`, `:clarify`, `:outline`, ...) — never for `j`/`k` or fold/edit commands, or the list would be useless clutter. Bound to `gi` rather than `ctrl-i`: in a plain terminal `ctrl-i` and `Tab` are the same byte, so there's no way to bind it separately from the fold-toggle key above. A no-op at either end of the list |

### Editing

| Key | Action |
|---|---|
| `i` | Edit the current entry (and its subtree) in `$EDITOR`. For a vim-family `$EDITOR` (vi/vim/nvim/gvim/mvim), the cursor lands right after the bullet (`* `) already in insert mode, so typing starts immediately. On a file's own header row, edits the whole file directly instead (after a confirmation, since this discards undo history and marks for that file) |
| `A` | Same as `i`, but the cursor lands at the end of the entry's first line instead (vim's own "append" position) |
| `o` / `O` | Insert a new entry after / before the current one (or at the end/start of a file, from a file header row). The template opened in `$EDITOR` is prefilled with a `CREATED` property set to now — edit or delete it like anything else before saving. For a vim-family `$EDITOR`, the cursor starts right after the bullet in insert mode, same as `i` (see above), ready to type the new title immediately |
| `dd` | Delete the current entry and its subtree (undoable; also fills the paste register). The cursor stays at the same screen row, landing on whatever now occupies it (or the new last row, if it was the last one) — matching vim's own `dd` |
| `<N>dd` | Delete the current entry and the next N-1 entries and their subtrees, as one undo step (e.g. `3dd` deletes 3 entries). A count of 1 (or none) is exactly plain `dd`. A higher count fills the register with all of the deleted entries (top-to-bottom order preserved), pasted back together as a group by a single `p`/`P` |
| `yy` | Yank the current entry and its subtree into the paste register, without deleting it |
| `p` / `P` | Paste the register's contents after / before the current entry, re-indented to fit. If the register holds more than one entry (from `<N>dd` or a visual-mode `d`), all of them are pasted together, in the same order they were deleted in |
| `>>` / `<<` | Demote / promote the current entry (re-parents it, not just cosmetic indentation) |
| `r` / `R` | Open a picker to set the TODO state directly (type to filter, or use a candidate's bracketed shortcut) |
| `<N>r` / `<N>R` | Open the same picker, but apply the chosen state to the current entry and the next N-1 (each independently, nesting included), as one undo step (e.g. `2R` sets the current and next entry) |
| `gd` | Set the current entry's deadline — accepts an exact date, `3d`/`2w`/`1m`/`1y` shorthand, or a fuzzy phrase like "next tuesday" |
| `gC` | Capture: append a new entry to the end of the inbox file and open it in `$EDITOR`, regardless of the current cursor position or view (same as `:capture`). Deliberately doesn't guess at a calendar meeting to attach, even one in progress at the moment of capture — see `gM` below, the interactive way to do that |
| `gM` | Open a picker (type to filter by title, ↑/↓ to browse, Enter to pick, Esc to cancel) over every distinct meeting gcalsync currently has synced at least one instance of — a recurring series (deduped by series) or a one-off event alike — and toggle it on or off the current entry's `GCAL_RECURRING_EVENT_IDS`/`GCAL_RECURRING_EVENT_LINKS` (recurring) or `GCAL_EVENT_IDS`/`GCAL_EVENT_LINKS` (one-off) properties (the `..._LINKS` one is a title/link snapshot, used by the status line — see above — to keep showing the meeting's name and link even after it drops off the calendar entirely; see the agenda's Meetings section, also above, for what the IDs are for). Picking a meeting already attached detaches it instead of adding a duplicate. A no-op (with a status message) if gcalsync hasn't synced anything at all — there's nothing to offer. An attached entry shows a `▣` in its own gutter column (alongside any mark, lock, or dirty marker), so whether it has a meeting attached is visible at a glance, in every view, without opening it |
| `gX` | `gC` immediately followed by `gM`: capture as usual, and once the editor session commits, the meeting picker opens automatically on the just-captured entry — for capturing something during a meeting and attaching that meeting in one motion, without a separate `gM` bracketing the (possibly slow) editor round-trip. Cancelling the capture (empty or blank result, or the editor failing to run) never opens the picker; if nothing's synced yet, the capture still commits, just without the picker (same no-op message as a bare `gM`) |
| `u` / `ctrl-r` | Undo / redo (single global stack for the session) |

### Visual selection

| Key | Action |
|---|---|
| `V` | Enter visual line selection at the current entry. Navigation keys (`j`/`k`, `gg`/`G`, `{`/`}`, `l`/`h`, `^`/`$`, `ctrl-d`/`ctrl-u`, `Page Down`/`Page Up`, `ctrl-e`/`ctrl-y`) extend the selection instead of just moving; `V` again or `Esc` cancels it |
| `d` | (in visual mode) Delete every selected entry and its subtree. A selected entry whose ancestor is also selected isn't deleted separately — deleting the ancestor already removes it. One undo step per file touched (almost always just one). Fills the paste register with everything deleted (top-to-bottom order preserved), so `p`/`P` pastes the whole selection back as a group |
| `r` / `R` | (in visual mode) Open the same status picker as normal-mode `r`/`R`, but apply the chosen state to every selected entry independently (nested entries included, unlike `d`) — also one undo step per file touched |

The cursor's own entry keeps the usual highlight color; the rest of the
selection is shaded differently, so which end is the actual cursor is
always clear.

### Marks

| Key | Action |
|---|---|
| `m<letter>` | Mark the current entry with a letter (a–z). Marking an entry that already has a different letter replaces it; marking it with the *same* letter again clears it — an entry holds at most one mark |
| `'<letter>` | Jump to a mark |
| `:delmarks <letters>` / `:delmarks!` | Delete specific marks / delete all marks |

Every active mark stays pinned to the top of the screen, in every view,
until cleared or moved elsewhere.

### Search

| Key | Action |
|---|---|
| `/` / `?` | Incremental forward / backward search — jumps as you type, wraps around the ends, case-insensitive. `Enter` confirms and stays at the match; `Esc` (or backspacing past an empty query) reverts to where you started |
| `n` / `N` | Repeat the last search forward / backward |
| `:noh` | Clear search-match highlighting |

Matches highlight everywhere they occur (title, tags, file names, body
text) and stay highlighted after you move on, until the next search or
`:noh`.

### Command mode (`:`)

`<Tab>` completes a partial command, listing every match if it's
ambiguous. `↑`/`↓` recall previous commands, most recent first —
matching vim's own cmdline history: every command actually run is
recorded (whether or not it turned out valid, and without deduplicating
repeats), and `↑` past the oldest entry stops there rather than
wrapping. If you'd already started typing something before pressing
`↑`, `↓` will walk back to it once you're past the most recent entry.
History doesn't persist between sessions.

| Command | Action |
|---|---|
| `:w` / `:write` | Write every file with unsaved changes |
| `:wq` | Write, then quit |
| `:q` / `:quit` | Quit (refuses if there are unsaved changes) |
| `:q!` / `:quit!` | Quit, discarding unsaved changes |
| `:undo` / `:redo` | Same as `u` / `ctrl-r` |
| `:agenda` / `:clarify` / `:outline` / `:config` / `:log` / `:diff` / `:help` / `:calendar` | Switch views |
| `:capture` | Same as `gC`: append a new entry to the end of the inbox file and open it in `$EDITOR` |
| `:next` / `:prev` | Clarify view only: manually step to the next/previous pending (not `DONE`/`CANCELLED`) inbox item |
| `:format-links` | Find every entry with a bare URL not already an org-mode link, and reformat them all via `format_links_url_formatter` (or `url_formatter`, if that's unset — see above) in the background. Affected entries lock — shown with a `◆` in the gutter and rendered faint/dimmed — uneditable, undeletable, and excluded from bulk operations — until their batch finishes; the rest of the app stays fully usable in the meantime |
| `:commit` | Diff view only (see Views, above) — refuses unless the org directory is itself the *root* of its git repository (not merely somewhere inside one, e.g. this project's own `testdata/orgdir`), since `git push` isn't scoped to particular files — it pushes the whole current branch, which for a nested workspace would mean pushing an unrelated repository's real history. Otherwise asks about any untracked file first (same `[y/N]` prompt as `:diff`; note a file left untracked here won't actually be committed, since `git commit` never picks up a file that's never been `git add`ed at all), then prompts for a commit message, runs `git commit` scoped to the same files `:diff` shows, followed by `git push`. Refuses outside diff view too. Runs synchronously (both commands can briefly block the UI, `git push` for as long as the remote takes to respond); a failed commit (e.g. nothing to commit) never attempts the push, while a failed push still leaves the commit in place locally. Diff view refreshes afterward either way, so the result is immediately visible |
| `:delmarks <letters>` / `:delmarks!` | See Marks, above |
| `:noh` / `:nohlsearch` | See Search, above |
| `:toggledone` | Toggle hiding stale `DONE`/`CANCELLED` items in the outline view on/off — see Views, above |

Plain `q` does **not** quit — only `:q`/`:quit` do. `ctrl-c` quits
immediately if there's nothing unsaved; with unsaved changes it refuses
the same way `:q` does (a status-line message, `:w` to save), except a
second `ctrl-c` right after that first one forces the quit anyway,
discarding them — any other key in between cancels that, so it takes
two `ctrl-c`s in a row, not just two at some point in the session.

## File format

orgtd reads and writes a practical subset of org-mode: headlines with
stars/TODO-keyword/priority/title/tags, `SCHEDULED`/`DEADLINE`/`CLOSED`
planning lines, `:PROPERTIES:` drawers, and free-text body content.
TODO keywords are fixed: `TODO`, `NEXT`, `WAITING`, `SOMEDAY` (active),
`DONE`, `CANCELLED` (done — stamps `CLOSED` automatically).

Marking an item with a repeating `SCHEDULED`/`DEADLINE` (a trailing
cookie like `+1w`, `++1w`, or `.+1w`) as done matches org-mode: the
keyword doesn't actually change, and the repeating timestamp(s) advance
to their next occurrence instead, with the completion recorded via a
`:LAST_REPEAT:` property rather than `CLOSED`. `+1w` advances exactly one
interval from the old date (however overdue that leaves it); `++1w`
advances however many intervals it takes to land on or after today;
`.+1w` advances one interval from *today*, ignoring the old date
entirely.

Round-tripping is format-*preserving* rather than byte-exact: body text
and file preamble pass through untouched, but a headline, planning, or
property line orgtd re-serializes after any edit is regenerated from its
parsed fields, so incidental formatting (exact alignment, spacing) on a
*changed* line can shift slightly. Everything else in the file is left
alone.

## gcalsync

`gcalsync` is a separate, standalone program (not part of the `orgtd`
binary) that syncs Google Calendar events into an org file — by default
`calendar.org` at the top of your org directory, matching `orgtd`'s own
`calendar_file` setting (see above) so it's automatically excluded from
the outline view and shown instead, grouped by day, in the `:calendar`
view. It's a one-shot CLI: run it yourself on whatever schedule you like
(cron, launchd, a systemd timer); it doesn't loop or poll on its own,
and it never writes anywhere except that one output file.

Each synced event becomes a plain headline (no TODO keyword) with a
timestamp, location, description, and a link back to the event, e.g.:

```org
* Q3 planning sync                                                :recurring:
  :PROPERTIES:
  :GCAL_EVENT_ID:            abc123-20260910
  :GCAL_CALENDAR_ID:         primary
  :GCAL_RECURRING_EVENT_ID:  abc123
  :GCAL_START:               2026-09-10T14:00:00-07:00
  :GCAL_END:                 2026-09-10T15:00:00-07:00
  :GCAL_HTML_LINK:           https://calendar.google.com/event?eid=abc123
  :END:
  <2026-09-10 Thu 14:00-15:00>
  Location: Room 5

  Agenda: review roadmap, staffing

  [[https://calendar.google.com/event?eid=abc123][Open in Google Calendar]]
```

The timestamp is deliberately *not* `SCHEDULED`/`DEADLINE` — events
aren't tasks, so they don't show up in orgtd's agenda view, only in the
plain outline. `GCAL_START`/`GCAL_END` are a machine-readable copy of the
same start/end, used by the agenda's Meetings section and `gM`'s picker
(see above) to find and order current/upcoming meetings; `GCAL_HTML_LINK`
is a machine-readable copy of the link at the bottom, which `gM` copies
onto an attached entry's own `GCAL_RECURRING_EVENT_LINKS` (recurring) or
`GCAL_EVENT_LINKS` (one-off) property so the status line can keep
showing this event's title and URL long after this cached headline is
gone; the timestamp and link in the body are the human-readable ones,
for browsing calendar.org itself.

A recurring meeting is expanded into one headline per occurrence within
the sync window (each with its own `GCAL_EVENT_ID`, unique per instance),
tagged `:recurring:` and carrying a `GCAL_RECURRING_EVENT_ID` — stable
across every occurrence of the series, unlike `GCAL_EVENT_ID` — so `gM`
can attach a task or project to "this recurring meeting" in general
(via `GCAL_RECURRING_EVENT_IDS`) rather than one specific occurrence of
it (per DESIGN.md's project↔meeting concept). A one-off event has
neither the tag nor that property — `gM` attaches to it instead by its
own `GCAL_EVENT_ID` (via `GCAL_EVENT_IDS`), which works just as well
since, unlike a recurring series, a one-off event only ever has the one
occurrence to begin with.

The whole output file is wholesale-regenerated on
every run (it's a cache, not something to hand-edit — a comment at the
top says so); declined and cancelled events are left out.

### Setup

1. In [Google Cloud Console](https://console.cloud.google.com/), create
   a project (or use an existing one), enable the **Google Calendar
   API**, and create an OAuth client ID of type **Desktop app**. Every
   user of gcalsync needs their own client — a shared one baked into the
   binary couldn't keep its secret secret.
2. Add the client ID/secret, and anything else you want to override, to
   the same config file `orgtd` uses (`~/.config/orgtd/config.toml` by
   default — see Config file, above) under a `[gcalsync]` section:

   ```toml
   org_dir = "~/org"   # shared with orgtd

   [gcalsync]
   oauth_client_id = "...apps.googleusercontent.com"
   oauth_client_secret = "..."
   calendar_ids = ["primary"]
   sync_past_days = 1
   sync_future_days = 14
   output_file = "calendar.org"
   ```

3. Run `gcalsync`. The first run opens your system browser to Google's
   consent screen (scope: read-only calendar access); the resulting
   refresh token is cached in your OS keychain (macOS Keychain / Linux
   Secret Service / Windows Credential Manager), so later runs (e.g. from
   cron) don't need a browser at all. `-reauth` discards the cached token
   and runs the consent flow again.

| Flag | Config key | Default | Meaning |
|---|---|---|---|
| `-dir` | `org_dir` | same resolution as `orgtd`'s `--dir` | Org directory `-output-file` is resolved relative to |
| `-output-file` | `gcalsync.output_file` | `calendar.org` | Org file to regenerate, relative to `-dir` unless absolute |
| `-calendar-ids` | `gcalsync.calendar_ids` | `primary` | Comma-separated Google Calendar IDs to sync |
| `-sync-past-days` / `-sync-future-days` | `gcalsync.sync_past_days` / `gcalsync.sync_future_days` | `1` / `14` | Sync window around now |
| `-oauth-client-id` / `-oauth-client-secret` | `gcalsync.oauth_client_id` / `gcalsync.oauth_client_secret` | *(required)* | Your Google OAuth2 installed-app client |
| `-reauth` | — | off | Discard the cached token and re-run the consent flow |
| `-config` | — | same as `orgtd`'s `--config` | Path to the shared TOML config file |

## Development

```sh
go build ./...
go test ./...
```

Tests live alongside the code they cover (`internal/org`, `internal/ui`).
`internal/ui` tests drive the real `Model` through simulated key
presses; a handful that need to write to disk copy the fixtures under
`testdata/` into a temp directory first rather than touching the
checked-in copies.
