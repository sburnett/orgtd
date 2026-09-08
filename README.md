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
| `--url-formatter` | `url_formatter` | *(disabled)* | External program invoked as `<prog> <url>` to convert a bare URL typed while editing into an org-mode link (its stdout replaces the URL) |
| `--agenda-days` | `agenda_window_days` | `14` | How many days ahead the agenda view's "Upcoming" section covers |
| `--inbox-file` | `inbox_file` | `inbox.org` | Base name of the file `:clarify` treats as the inbox |
| `--editor` | `editor` | `$EDITOR`, then `vim` | External editor launched for `i` and file edits |
| `--config` | — | `$XDG_CONFIG_HOME/orgtd/config.toml` (or `~/.config/orgtd/config.toml`) | Path to the config file below |

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
agenda_window_days = 14
inbox_file = "inbox.org"
```

## Views

- **Outline** (default) — every loaded file, its headlines, and any
  free-text body underneath them, all foldable.
- **Agenda** (`:agenda`) — a flat, date-driven view across every file:
  **Overdue**, **Due Today**, and **Upcoming** sections built from
  `SCHEDULED`/`DEADLINE` timestamps, plus a **Next Actions** section
  listing every `NEXT`-keyword headline regardless of whether it has a
  date. `SOMEDAY` items are excluded entirely. An item with both a
  schedule and a deadline can appear in two sections.
- **Clarify** (`:clarify`) — pins the inbox's first top-level headline to
  the top of the screen while you navigate the rest of the outline to
  file it away; deleting the pinned item advances to the next one.
  `:outline` returns to the plain outline from either view.

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
| `i` | Edit the current entry (and its subtree) in `$EDITOR`. On a file's own header row, edits the whole file directly instead (after a confirmation, since this discards undo history and marks for that file) |
| `o` / `O` | Insert a new entry after / before the current one (or at the end/start of a file, from a file header row) |
| `dd` | Delete the current entry and its subtree (undoable; also fills the paste register) |
| `yy` | Yank the current entry and its subtree into the paste register, without deleting it |
| `p` / `P` | Paste the register's contents after / before the current entry, re-indented to fit |
| `>>` / `<<` | Demote / promote the current entry (re-parents it, not just cosmetic indentation) |
| `r` | Rotate the current entry's TODO state |
| `R` | Open a picker to set the TODO state directly (type to filter, or use a candidate's bracketed shortcut) |
| `gd` | Set the current entry's deadline — accepts an exact date, `3d`/`2w`/`1m`/`1y` shorthand, or a fuzzy phrase like "next tuesday" |
| `u` / `ctrl-r` | Undo / redo (single global stack for the session) |

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
| `:agenda` / `:clarify` / `:outline` | Switch views |
| `:delmarks <letters>` / `:delmarks!` | See Marks, above |
| `:noh` / `:nohlsearch` | See Search, above |

Plain `q` does **not** quit — only `:q`/`:quit` do (`ctrl-c` always quits
immediately, without the unsaved-changes check).

## File format

orgtd reads and writes a practical subset of org-mode: headlines with
stars/TODO-keyword/priority/title/tags, `SCHEDULED`/`DEADLINE`/`CLOSED`
planning lines, `:PROPERTIES:` drawers, and free-text body content.
TODO keywords are fixed: `TODO`, `NEXT`, `WAITING`, `SOMEDAY` (active),
`DONE`, `CANCELLED` (done — stamps `CLOSED` automatically).

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
