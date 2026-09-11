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
| `--hide-done-after-hours` | `hide_done_after_hours` | `24` | How many hours after a `DONE`/`CANCELLED` item's `CLOSED` timestamp it's hidden from the outline view (and its whole subtree with it). `:toggledone` shows everything again, and toggles back |
| `--editor` | `editor` | `$EDITOR`, then `vim` | External editor launched for `i` and file edits |
| `--debug` | `debug` | *(off)* | Log debug info (see Debug log, below) to `debug.log` next to the config file |
| `--config` | — | `$XDG_CONFIG_HOME/orgtd/config.toml` (or `~/.config/orgtd/config.toml`) | Path to the config file below |

`:config` shows the current, effective value of everything above.

Every `.org` file directly inside `--dir` is loaded, sorted alphabetically.
Subdirectories are not scanned.

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

- **Outline** (default) — every loaded file, its headlines, and any
  free-text body underneath them, all foldable. A `DONE`/`CANCELLED`
  headline (and its whole subtree) whose `CLOSED` timestamp is older
  than `hide_done_after_hours` (default 24) is hidden — `:toggledone`
  shows everything again, and toggles back.
- **Agenda** (`:agenda`) — a flat, date-driven view across every file:
  **Overdue**, **Due Today**, and **Upcoming** sections built from
  `SCHEDULED`/`DEADLINE` timestamps, plus a **Next Actions** section
  listing every `NEXT`-keyword headline regardless of whether it has a
  date. `SOMEDAY` items are excluded entirely. An item with both a
  schedule and a deadline can appear in two sections.
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
  full argument list), one line per stdout/stderr line as it's produced,
  and a line for its exit code, each individually timestamped, tagged
  `START`/`STDOUT`/`STDERR`/`EXIT`, and marked with the process's pid
  (`-` if it never actually started) — useful for telling apart two
  commands that happen to run at once, e.g. a `:format-links` batch
  alongside a live in-editor formatter invocation. Doesn't live-update —
  re-run `:log` to see anything logged since it was last opened.
  `:outline` returns to the outline.

Marks (see below) stay pinned at the top of the screen in every view.

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
| `Tab`, `za`/`zo`/`zc`/`zA`/`zO`/`zC` | Toggle / open / close a fold, one level (lowercase) or recursively (uppercase) |
| `Enter` | In agenda view, jump to that item's real place in the outline |

### Editing

| Key | Action |
|---|---|
| `i` | Edit the current entry (and its subtree) in `$EDITOR`. For a vim-family `$EDITOR` (vi/vim/nvim/gvim/mvim), the cursor lands right after the bullet (`* `) already in insert mode, so typing starts immediately. On a file's own header row, edits the whole file directly instead (after a confirmation, since this discards undo history and marks for that file) |
| `A` | Same as `i`, but the cursor lands at the end of the entry's first line instead (vim's own "append" position) |
| `o` / `O` | Insert a new entry after / before the current one (or at the end/start of a file, from a file header row). The template opened in `$EDITOR` is prefilled with a `CREATED` property set to now — edit or delete it like anything else before saving. For a vim-family `$EDITOR`, the cursor starts right after the bullet in insert mode, same as `i` (see above), ready to type the new title immediately |
| `dd` | Delete the current entry and its subtree (undoable; also fills the paste register) |
| `<N>dd` | Delete the current entry and the next N-1 entries and their subtrees, as one undo step (e.g. `3dd` deletes 3 entries). A count of 1 (or none) is exactly plain `dd`, register included; a higher count doesn't fill the register, since there'd be more than one entry to put there |
| `yy` | Yank the current entry and its subtree into the paste register, without deleting it |
| `p` / `P` | Paste the register's contents after / before the current entry, re-indented to fit |
| `>>` / `<<` | Demote / promote the current entry (re-parents it, not just cosmetic indentation) |
| `r` | Rotate the current entry's TODO state |
| `R` | Open a picker to set the TODO state directly (type to filter, or use a candidate's bracketed shortcut) |
| `<N>R` | Open the same picker, but apply the chosen state to the current entry and the next N-1 (each independently, nesting included), as one undo step (e.g. `2R` sets the current and next entry) |
| `gd` | Set the current entry's deadline — accepts an exact date, `3d`/`2w`/`1m`/`1y` shorthand, or a fuzzy phrase like "next tuesday" |
| `gC` | Capture: append a new entry to the end of the inbox file and open it in `$EDITOR`, regardless of the current cursor position or view (same as `:capture`) |
| `u` / `ctrl-r` | Undo / redo (single global stack for the session) |

### Visual selection

| Key | Action |
|---|---|
| `V` | Enter visual line selection at the current entry. Navigation keys (`j`/`k`, `gg`/`G`, `{`/`}`, `l`/`h`, `^`/`$`, `ctrl-d`/`ctrl-u`) extend the selection instead of just moving; `V` again or `Esc` cancels it |
| `d` | (in visual mode) Delete every selected entry and its subtree. A selected entry whose ancestor is also selected isn't deleted separately — deleting the ancestor already removes it. One undo step per file touched (almost always just one) |
| `R` | (in visual mode) Open the same status picker as normal-mode `R`, but apply the chosen state to every selected entry independently (nested entries included, unlike `d`) — also one undo step per file touched |

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
ambiguous.

| Command | Action |
|---|---|
| `:w` / `:write` | Write every file with unsaved changes |
| `:wq` | Write, then quit |
| `:q` / `:quit` | Quit (refuses if there are unsaved changes) |
| `:q!` / `:quit!` | Quit, discarding unsaved changes |
| `:undo` / `:redo` | Same as `u` / `ctrl-r` |
| `:agenda` / `:clarify` / `:outline` / `:config` / `:log` | Switch views |
| `:capture` | Same as `gC`: append a new entry to the end of the inbox file and open it in `$EDITOR` |
| `:next` / `:prev` | Clarify view only: manually step to the next/previous pending (not `DONE`/`CANCELLED`) inbox item |
| `:format-links` | Find every entry with a bare URL not already an org-mode link, and reformat them all via `format_links_url_formatter` (or `url_formatter`, if that's unset — see above) in the background. Affected entries lock — shown with a `◆` in the gutter and rendered faint/dimmed — uneditable, undeletable, and excluded from bulk operations — until their batch finishes; the rest of the app stays fully usable in the meantime |
| `:delmarks <letters>` / `:delmarks!` | See Marks, above |
| `:noh` / `:nohlsearch` | See Search, above |
| `:toggledone` | Toggle hiding stale `DONE`/`CANCELLED` items in the outline view on/off — see Views, above |

Plain `q` does **not** quit — only `:q`/`:quit` do (`ctrl-c` always quits
immediately, without the unsaved-changes check).

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

Because the keyword never actually changes on a repeating item, `r`
(which always computes "the next state after the current one") can get
stuck: once it reaches a done-class state and the repeat fires, the next
`r` press starts from that same pre-completion state and just re-triggers
the repeat again, rather than advancing to `CANCELLED` or wrapping
around. This matches real org-mode's own bare-cycling command, which has
the identical quirk for the same reason. Use `R` (then the state's
letter, e.g. `c` for `CANCELLED`) to jump straight to a specific state in
one step instead.

Round-tripping is format-*preserving* rather than byte-exact: body text
and file preamble pass through untouched, but a headline, planning, or
property line orgtd re-serializes after any edit is regenerated from its
parsed fields, so incidental formatting (exact alignment, spacing) on a
*changed* line can shift slightly. Everything else in the file is left
alone.

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
