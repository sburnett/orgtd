# orgtd

A terminal UI for working through a GTD-style workflow directly on plain
`.org` files, with vim-flavored keybindings. It's a viewer and editor —
navigate, fold, edit, reorganize, and search your outline — plus an
agenda view computed from `SCHEDULED`/`DEADLINE` dates and `NEXT` items,
and a "review" mode for working through an inbox one item at a time.

Every edit is applied in memory immediately; nothing touches disk until
you `:w`, which commits it to the git repository the org directory must
be — so several orgtd instances can run on the same files at once, and
their saves are merged. Files are otherwise your own — orgtd never manages a directory
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
| `--snippets-days` | `snippets_window_days` | `7` | How many days, today included, the `:snippets` view looks back over |
| `--inbox-file` | `inbox_file` | `inbox.org` | Base name of the file `:review` treats as the inbox |
| `--calendar-file` | `calendar_file` | `calendar.org` | Base name of the file (e.g. the one `:sync-calendar` writes) excluded from the outline view and shown instead, grouped by day, in the `:calendar` view |
| `--meeting-tags-file` | `meeting_tags_file` | `meeting-tags.org` | Base name of the file holding durable meeting tags (see Tag-based meeting links, below) — excluded from the outline view and shown instead, as an editable outline of its own, in the `:meeting-tags` view |
| `--outline-ignore-files` | `outline_ignore_files` | *(none)* | Files orgtd never loads, so their entries appear nowhere — globs (`*`, `?`, as in a shell) matched against each file's base name, in `--dir` and `reference/` alike, e.g. `archive*.org`; comma-separated on the flag, a TOML array in the config file. `:edit` refuses them too. A malformed glob stops orgtd at startup |
| `--scratch-dir` | `scratch_dir` | `scratch` | Directory every `$EDITOR` buffer is written to and kept in (see Editing, below). A leading `~` is expanded, and a relative path is relative to `--dir`, so it can live outside the org directory, e.g. `~/.local/state/orgtd/scratch`. Created when first needed |
| `--hide-closed-after` | `hide_closed_after` | `5m` | How long after an item's `CLOSED` timestamp it's hidden from the outline view (and its whole subtree with it), as a duration: `5m`, `1h30m`, `24h`. `:toggleclosed` shows everything again, and toggles back |
| `--main-branch` | `main_branch` | `main` | The git branch every save commits to. `--dir` must be the root of a git repository with this branch checked out — see Saving and git, below |
| `--merge-tool` | `merge_tool` | *(same as `editor`)* | Command `:resolve` runs on files a save couldn't merge, with their paths appended — see Conflicts, below |
| `--editor` | `editor` | `$EDITOR`, then `vim` | External editor launched by `ctrl-g` (see Editing, below) and for whole-file edits |
| `--debug` | `debug` | *(off)* | Log debug info (see Debug log, below) to `debug.log` next to the config file |
| `--config` | — | `$XDG_CONFIG_HOME/orgtd/config.toml` (or `~/.config/orgtd/config.toml`) | Path to the config file below |

`:config` shows the current, effective value of everything above.

`inbox_file`, `calendar_file` and `meeting_tags_file` must be plain file
names inside `--dir`: one containing `/` or `\`, or set to `.` or `..`,
stops orgtd at startup, wherever it was set (flag, config file, or
`.orgtd.toml`) — so a shared `.orgtd.toml` can't aim orgtd's writes at a
file outside the org directory.

Every `.org` file directly inside `--dir` is loaded, sorted alphabetically,
except any matching `outline_ignore_files`.
The one subdirectory that's scanned too is `reference/` (its `.org` files
only, not its own subdirectories) — reference material in GTD's sense,
information with no action attached; see the Reference view, below. Any
other subdirectory is not scanned. `reference/` is created by hand (there's
no setting for its name), and, like the rest of `--dir`, is read once at
startup — to add a file while orgtd is running, use `:edit` (below).

### Saving and git

`--dir` must be the root of a git repository (a `.git` directory right
inside it) with `main_branch` (default `main`) checked out. orgtd refuses
to start otherwise, and says how to `git init` one. On any other branch,
or a detached `HEAD`, it opens but won't save until you check out the
main branch again. No `git` program is needed: orgtd reads and writes
the repository itself.

Every `:w` is a commit on the main branch, authored with your
`user.name` and `user.email` from git's config. Its message says which
entries changed, compared with the file as last loaded or saved: the one
change, as in `orgtd: todo.org: mark DONE "Call the dentist"`, or else a
count of each kind (`orgtd: inbox.org: remove 2; todo.org: add 2, edit
1`) with every change listed in the body. Entries are matched across the
two versions by outline path, then by title (a refiled entry), then by
`:ID:` or `:CREATED:` (a retitled one); an entry that matches nothing was
added or removed. Only the top of an added, removed or moved subtree is
listed. A save that changes no entry — only the text before the first
one, say — is `orgtd: todo.org: update`. Only
the files orgtd loads take part — the `.org` files in `--dir` and
`reference/`, minus `outline_ignore_files` — and everything else in the
repository is left as it is. A file git ignores and doesn't track (a
`calendar.org` cache in `.gitignore`, say) is just written to disk.
History stays linear: each save's commit has one parent, the branch as it
was, like a rebase. The one exception is merging the iOS companion's
branch, below. Pushing and pulling are up to you (a `:run`
command is a good place for them, such as the `git-push` command in the
example config below).

The status line says when there's something to push:
`3 unpushed (origin/main)` means the main branch has 3 commits that a
plain `git push`, run in the org directory, would send. orgtd works out
where that push would go the way git does: the branch's `pushRemote`,
else `remote.pushDefault`, else its upstream's remote, else `origin`,
then the remote's push refspecs or `push.default` (`simple` unless set)
for the branch there. It reads your repository's, global and system git
config, though not files they `[include]`. Nothing is fetched, so the
count is against what the org directory last fetched from or pushed to
that branch, as `git status` reports; with no record of it at all
(never fetched or pushed there), it says `unpushed (origin/main)`,
without a number. With nothing to push, or nowhere a plain `git push`
would push (no remote, or no upstream where `push.default` needs one),
it says nothing.

#### Running several instances

Any number of orgtd instances can run on the same directory, on one
machine. Each `:w` merges your edits with whatever reached the main
branch since you loaded those files: another instance's saves, a `git
pull`, or edits made in Emacs. The merge is line by line, as git's own
is, so edits to different entries of the same file merge cleanly; files
the merge changed are reloaded to show the result.

Every 2 seconds, orgtd checks the main branch. When another instance
saved, the files it changed are reloaded right away, unless you have
unsaved edits to them: then the status line says `main moved — :w
merges`, and your next `:w` merges. Nothing is reloaded while you're in
insert mode or a prompt; it waits until you're back in normal mode. A
reloaded file loses its undo history and marks, as with `:e!`; files the
other instance didn't touch keep theirs.

The status line also says how many other instances are running on the
same directory (`2 other instances`), when there are any. Like the
saving itself, it only sees instances on this machine.

The same check brings in the iOS companion's changes (see iOS companion,
below), when the phone pushes them straight into the org directory, to
a `mobile` branch. If that's your main branch plus new commits, orgtd
fast-forwards the main branch to it and says so (`Applied changes from
mobile to inbox.org`). If the main branch has moved on since (you saved
after the phone last synced, or you changed an entry the phone changed
too), orgtd merges `mobile` into it, line by line as a save merges, and
commits the result with both as parents (`Merged changes from mobile
into todo.org`). Either way it then reloads the files as for another
instance's save. When it can't, the status line says why and what to
do:

- `phone changes conflict in todo.org — :resolve`: you and the phone
  changed the same lines. Nothing changes until you resolve it:
  `:resolve` opens the conflicted files in `merge_tool`, with the
  phone's side marked `mobile` and yours `main`, as for a save's
  conflict (see Conflicts, below), and commits the merge once no
  markers are left. To discard the phone's change, keep the `main`
  side; it stays in the history either way, as the merge's second
  parent. Until then the phone keeps its change and shows it as
  waiting.
- `mobile waits on outside edits to inbox.org — :w`: a file the phone
  changed has edits on disk that aren't committed yet. `:w` commits
  them, and the next check merges.
- `mobile changes notes.txt — run git merge mobile`: `mobile` changes
  something other than the files orgtd loads, which orgtd won't write.
  Running that in the org directory finishes the job, and orgtd picks
  up the result. The phone never does this.

The note goes away once `mobile` is applied, or merged by hand.

Edits made outside orgtd — Emacs, a hand edit, a `:run` script — are
committed for you (`orgtd: outside edits to inbox.org: add "Captured
in Emacs"`) at the
next `:w`, `:e!` or `:run`, then merged like any other change. Until then
they aren't picked up by the 2-second check.

#### Conflicts

When two instances change the same lines, the second `:w` can't merge.
Nothing is saved: your edits stay unsaved in orgtd, the main branch is
untouched, and the status line says `Conflict in todo.org — :resolve to
fix, :w! to keep yours, :e! to take theirs`:

- `:resolve` writes the conflicted files, with the usual `<<<<<<<`,
  `=======`, `>>>>>>>` markers around each clash, to
  `<scratch_dir>/merge-<instance>/`, and opens them in `merge_tool`
  (default: your editor) with the terminal handed over. Edit them into
  shape and quit. If any markers are left, nothing changes; otherwise
  the result replaces your version and is saved.
- `:w!` saves your version of each conflicted file whole, discarding the
  other side's changes to it. Every other file still merges.
- `:e!` reloads the file from the main branch, discarding your edits to
  it.

Keep editing first if you like: the next `:w` or `:resolve` merges again
from scratch. `:wq` won't quit while a save conflicts.

`:resolve` also resolves a conflict between the iOS companion's
`mobile` branch and the main branch (see Running several instances,
above), once any save conflict of your own is out of the way. `:w!`
and `:e!` don't apply to it: your unsaved edits aren't involved, so
choose a side in the merge tool instead.

#### If orgtd crashes

A save first records your edits in the repository, under
`refs/orgtd/<instance>` (not a branch: `git branch` doesn't list it), and
deletes that ref once they're on the main branch. If orgtd dies before
that, or while a save is stuck on a conflict, the next orgtd to start
says `Recovered unsaved edits from a crashed orgtd`. `:recover` loads
them and saves them as a normal save, merging and all; `:recover drop`
discards them. By hand: `git log refs/orgtd/<instance>` shows them, and
`git update-ref -d refs/orgtd/<instance>` deletes them.

A crash in the middle of writing files can leave a `.git/index.lock`
behind, which makes the next save (and git itself) refuse, saying so.
Delete the file, as git suggests; whatever was written is committed at
the next save. Edits you never saved are lost, as in any editor.

### Config file

Any setting above can also go in a TOML config file, so you don't have to
repeat flags on every invocation. An explicit flag always overrides the
config file; `--dir` can additionally be overridden by `$ORGTD_DIR`,
which takes precedence over the config file but not over an explicit
flag. The config file itself, and every key in it, is optional — orgtd
works fine with none of it present.

Settings you want on every machine checked out from the same org
directory — and on the iOS companion app (see below) — can instead go
in a `.orgtd.toml` committed to the org directory itself. It takes the
same keys, and sits below your own config file: an explicit flag wins,
then (for the directory only) `$ORGTD_DIR`, then your config file, then
`<org_dir>/.orgtd.toml`, then the built-in default. It can't set
`org_dir` (it's found through it) or anything machine-local — `editor`,
`url_formatter`, `format_links_url_formatter`,
`scratch_dir`, `debug`, `[[commands]]`, or the `[gcalsync]` section —
those are ignored with a warning on the command line at startup. A
malformed `.orgtd.toml` stops orgtd at startup, like a malformed config
file. `:config` shows, after each setting, which of these layers its
value came from (`[flag]`, `[env]`, `[config]`, `[repo]`, `[default]`).

```toml
org_dir = "~/org"          # "~" is expanded
editor = "emacsclient -t"  # falls back to $EDITOR, then vim, if unset
url_formatter = ""
url_formatter_prefixes = ["bit.ly/", "go/"]
format_links_url_formatter = ""  # falls back to url_formatter if unset
agenda_window_days = 14
snippets_window_days = 7
inbox_file = "inbox.org"
calendar_file = "calendar.org"
meeting_tags_file = "meeting-tags.org"
outline_ignore_files = ["archive*.org", "old.org"]
scratch_dir = "scratch"    # "~" is expanded; relative to org_dir
hide_closed_after = "5m"  # a duration: "5m", "1h30m", "24h"
debug = false

# Customizes the character and color of each marker the outline draws in
# its gutter column, to the left of every entry (see Keybindings, below,
# for what each one means: "+" for unsaved changes, a mark's own letter,
# "●" for the :review target, "◆" for a :format-links lock, "▣" for a
# meeting link, via "gM" or a shared tag). Every key here is optional and falls back
# to the built-in glyph/color shown below when unset — delete whichever
# lines you don't want to override.
#
# A color is either an ANSI color code ("0"-"255" — see
# https://www.ditig.com/256-colors-cheat-sheet for a chart of what each
# number looks like) or a hex RGB string ("#ff8700"), per lipgloss's own
# Color type: https://github.com/charmbracelet/lipgloss#colors
#
# An icon is any single character. Stick to single-width glyphs (found
# by browsing a Unicode reference, e.g. https://symbl.cc/en/unicode-table/
# — the Geometric Shapes block, U+25A0-25FF, is a good hunting ground for
# more marks in the same style as the built-in ones) — a wide character
# (most emoji, many CJK characters) will throw off the gutter's column
# alignment with the rows around it.
[icons]
dirty_icon    = "+"    # unsaved changes
dirty_color   = "#0087d7"
mark_color    = "#ff87ff"  # a vim-style mark's own letter ("m<letter>") — no matching icon, since the glyph is the letter itself
review_icon  = "●"    # the :review pinned inbox item
review_color = "#ff87ff"
lock_icon     = "◆"    # locked by an in-flight :format-links batch
lock_color    = "#ffaf00"
meeting_icon  = "▣"    # linked to a calendar meeting, via "gM" or a shared tag
meeting_color = "#00afff"
claude_icon   = "✻"    # not a gutter marker: the tag shown on an entry with a Claude session (see Passing context to a command)
claude_color  = "#d7875f"

# Customizes the rest of the built-in color scheme, beyond the gutter
# markers above. Every key here is optional and independent, and falls
# back to its own built-in color (shown below) when unset. A color is
# either an ANSI color code or a hex RGB string, same as [icons] above.
[colors]
file_color              = "#00afff"  # a file's own header row
todo_color              = "#d7005f"  # the TODO keyword
next_color              = "#ffaf00"  # the NEXT keyword
waiting_color           = "#875fff"  # the WAITING keyword
someday_color           = "#767676"  # the SOMEDAY keyword
done_color              = "#00d75f"  # the DONE keyword
cancelled_color         = "#585858"  # the CANCELLED keyword
tag_color               = "#00d7d7"  # an entry's ":tag:" text
done_title_color        = "#767676"  # a DONE/CANCELLED entry's (struck-through) title
status_color            = "#767676"  # muted status/info text (the status line's directory path, register/overflow summaries, the visual-mode banner)
timestamp_color         = "#ff87ff"  # a SCHEDULED/DEADLINE/CREATED/CLOSED date
error_color             = "#d7005f"  # an error message on the command line
body_color              = "#a8a8a8"  # an entry's free-text body lines
caret_fg                = "#000000"  # the command line's text-cursor caret
caret_bg                = "#ffffff"
highlight_bg            = "#585858"  # the highlighted candidate in an overlay list (the "R"/status picker, "gM"'s meeting picker)
panel_bg                = "#303030"  # the info buffer (review/marks/register, links, meeting detail, completions, pickers)
panel_fg                = "#d0d0d0"  # the plain item text in the info buffer's links, meeting and completion lists
status_bar_fg           = "#000000"  # the one-line status bar at the bottom of the screen
status_bar_bg           = "#9e9e9e"
status_bar_inactive_fg  = "#000000"  # the status bar of every window but the active one, once split (see Windows)
status_bar_inactive_bg  = "#767676"
cursor_row_bg           = "#204060"  # the row under the cursor
inactive_cursor_row_bg  = "#1c2c3c"  # the row under the cursor in every window but the active one, once split
visual_selection_bg     = "#102030"  # the rest of a visual-mode selection, besides the cursor's own row
insert_bg               = "#1f3f1f"  # the entry being edited in insert mode
search_highlight_bg     = "#ffd75f"  # every match of the active search term (vim's 'hlsearch'), drawn with black text

# The TODO keywords: one [[keywords]] table each, in the order the
# "r"/"R" status picker lists them. Omit every one to get the built-in
# set shown here; if you define any, they replace the built-in set
# entirely (so list DONE-style states too). Existing files keep working
# only for keywords still listed here — a headline starting with an
# unlisted word is read as plain title text.
[[keywords]]
name     = "TODO"      # as written in a headline: non-empty, no spaces
shortcut = "t"         # one character that picks it in the status picker; derived from the name's first free letter if omitted ("j", "k" and "-" are reserved)
color    = "#d7005f"   # ANSI code or hex RGB; optional (a built-in name falls back to its [colors] key, e.g. todo_color)
bold     = true
[[keywords]]
name     = "NEXT"
shortcut = "n"
bold     = true
agenda   = "next"      # listed in the agenda's Next Actions section, dated or not
[[keywords]]
name     = "WAITING"
shortcut = "w"
bold     = true
[[keywords]]
name     = "SOMEDAY"
shortcut = "s"
agenda   = "skip"      # never shown in the agenda's Overdue/Due Today/Upcoming
[[keywords]]
name     = "DONE"
shortcut = "d"
done     = true        # a finished state: stamps CLOSED, strikes through the title, skipped by the agenda and :review
[[keywords]]
name     = "CANCELLED"
shortcut = "c"
done     = true
snippets = "skip"      # never shown in the :snippets view

# Custom commands, run with ":run <name>" (see Running your own commands,
# below): one [[commands]] table each. None by default.
[[commands]]
name    = "vcs"        # what follows :run; non-empty, no spaces, unique
command = "gitui"      # program and arguments, same syntax as editor
[[commands]]
name    = "git-push"
command = "git push"   # see Saving and git, above, for when there's something to push
[[commands]]
name    = "sync"
command = "~/bin/org-sync --quiet"
[[commands]]
name    = "open"
command = "~/bin/org-open"
context = true         # pass the current entry (or selection); see Passing context to a command
```

### Running your own commands (`:run`)

`:run <name> [args...]` runs the `[[commands]]` entry called `<name>`
from your config file — a version-control UI such as `gitui` or `jjui`,
a sync script, anything that might read or rewrite your `.org` files.
Anything after the name is split shell-style and appended to the
command's own arguments, so `:run sync --dry-run` runs
`~/bin/org-sync --quiet --dry-run`. `<Tab>` completes the name, and
`:run` alone lists the configured ones. Each run:

1. Saves every file with unsaved changes, as `:w` does. If the save
   doesn't go through — it conflicts, say (see Conflicts, above) —
   nothing is launched, so the program never sees a state that doesn't
   match what orgtd is showing.
2. Runs the command in `--dir`, handing it the terminal until it exits.
3. Commits whatever the command changed (see Running several
   instances, above) and reloads every file that changed, discarding that
   file's undo history and marks as `:e!` does. The message line lists
   what was reloaded and whether the command exited with an error.

A new file the command created and committed is loaded too. `[[commands]]` is machine-local: a committed
`.orgtd.toml` can't define commands, since anyone who can push to the
repo could otherwise make orgtd run anything they like.

This replaces the old `vcs_editor` setting, `--vcs-editor` flag and
`:vcs` command. A config file still setting `vcs_editor` stops orgtd at
startup with a message saying what to write instead.

#### Passing context to a command

A command with `context = true` is told which entry `:run` was typed on:
the one under the cursor, or — typing `:` in visual mode (see Visual
selection, below) — every selected entry. On a file's header row it's
told the file and no entries. It gets these environment variables:

| Variable | Value |
|---|---|
| `ORGTD_DIR` | The org directory (`--dir`) |
| `ORGTD_VIEW` | The current view: `outline`, `agenda`, `calendar`, `reference`, ... |
| `ORGTD_COUNT` | How many entries were passed: `1` for the cursor's entry, the selection's size, or `0` |
| `ORGTD_CONTEXT` | Path of a JSON file with all of it (below) |
| `ORGTD_FILE` | Absolute path of the entry's file, or of the file whose header row the cursor is on |
| `ORGTD_LINE` | The entry's headline line in that file, counting from 1 |
| `ORGTD_TITLE` | Its title, without keyword, priority or tags |
| `ORGTD_TODO` | Its keyword (`NEXT`), or empty |
| `ORGTD_TAGS` | Its tags in org form (`:work:writing:`), or empty |
| `ORGTD_OUTLINE_PATH` | Its file name and every ancestor's title, then its own, joined with `/`: `projects.org/Work/Q4/Write report` |
| `ORGTD_ID` | Its `:ID:` property, or empty |

The variables from `ORGTD_FILE` down describe a single entry, so with a
visual selection of more than one they aren't set (`ORGTD_FILE` aside,
on a header row). The JSON file has everything, for every entry:

```json
{
  "dir": "/Users/you/org",
  "view": "outline",
  "entries": [
    {
      "file": "/Users/you/org/projects.org",
      "line": 142,
      "level": 3,
      "todo": "NEXT",
      "priority": "A",
      "title": "Write report",
      "tags": ["work", "writing"],
      "scheduled": "<2026-11-24 Tue>",
      "deadline": "",
      "closed": "",
      "properties": {"ID": "abc"},
      "body": "Draft it.",
      "outline_path": ["projects.org", "Work", "Q4", "Write report"],
      "ancestors": [
        {"line": 3, "level": 1, "todo": "", "title": "Work"},
        {"line": 88, "level": 2, "todo": "", "title": "Q4"}
      ],
      "raw": "*** NEXT [#A] Write report   :work:writing:\n..."
    }
  ]
}
```

A top-level `"file"` is added on a header row. `raw` is the entry's own
text (headline, planning, properties, body), not its children. Line
numbers are the file as saved on disk — `:run` saves before launching —
so `$EDITOR +"$ORGTD_LINE" "$ORGTD_FILE"` opens right at the entry, and
a script can rewrite it there and have orgtd reload the change. If that
save merges in a change that renames or removes one of the entries,
`:run` says so and doesn't launch the command, rather than pass it the
wrong entry. The JSON file is written to the system temp directory, readable only by
you, and deleted once the command exits. A command without
`context = true` gets none of this. Nor does any command get these
variables from orgtd's own environment — say, an orgtd started by
another's `:run` — so a command never mistakes the outer entry for its
own; the one exception is `ORGTD_DIR` for a command without
`context = true`, since it's also orgtd's own `--dir` default.

For example, `scripts/claude_entry.py` starts a Claude Code session
about the entry you're on: its system prompt holds the entry, everything
nested under it and each ancestor heading's own notes, and Claude opens
by summarizing the task and proposing a first step. It starts in the
directory named by a `:PROJECT_DIR:` property on the entry or its
nearest ancestor that has one (else the org directory), so setting one
on a project heading covers every task under it. It saves the session's
ID in a `:CLAUDE_SESSION:` property on the entry, so running it there
again resumes that conversation, with the entry's notes refreshed. While
that session is open — in another orgtd instance, say — running it on
the entry again refuses rather than opening the conversation twice. An
entry with one shows a `✻` among its tags (`:work:✻:`), in its own
color (`claude_icon`/`claude_color` under `[icons]`, above), and is
listed under `✻` in `:tags`. It's generated from the property, not
stored as a tag, so insert mode doesn't show it and tag completion
doesn't offer it; deleting the property removes it.
Anything after the name goes to `claude`, e.g. `:run claude --model
opus`:

```toml
[[commands]]
name    = "claude"
command = "~/git/orgtd/scripts/claude_entry.py"   # wherever your checkout is
context = true
```

See the script's own header for the details.

The built-in color scheme (`[colors]` and `[icons]` above) matches the
dark variant of the [wildcharm](https://github.com/vim/colorschemes/blob/master/colors/wildcharm.vim)
vim colorscheme — it's fixed rather than adapting to the terminal's own
light/dark setting, since the whole point is to render as that specific
theme, though every individual color can still be overridden as shown
above. `:config` shows the current, effective value of every one of
them.

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
  underneath them, all foldable. A headline with a `CLOSED` stamp (and its
  whole subtree) whose `CLOSED` timestamp is older than
  `hide_closed_after` (default 5 minutes) is hidden — `:toggleclosed` shows
  everything again, and toggles back.
- **Calendar** (`:calendar`) — every event in `calendar_file` (default
  `calendar.org`; see Calendar sync, below), grouped under one flush-left
  header per calendar day, both the days and the events within each day
  in chronological order. The cursor starts on whichever meeting is
  currently in progress, or the most recently started past meeting if
  none is, and that row centered on screen, so you land right where the
  day already is rather than at the top — falling back to the very first
  row if nothing has started yet (every synced event is still upcoming).
  The status bar adds when the calendar was last synced successfully
  (`synced 14:05`, or `synced Oct 25 14:05` if not today — before this
  session's first sync, that's `calendar_file`'s modification time; or
  `never synced` with no file at all), plus `syncing now...` while a sync
  is running, or `last attempt failed (see :log)` if the most recent
  one, manual or automatic, failed — `:log` has the error.
  This is the only place `calendar_file`'s
  contents are shown, since it's excluded from the outline view (above)
  entirely. Each event shows its time before its title (`14:00-14:30
  Standup`, or `All day` for an all-day event) in place of a TODO
  keyword, and starts folded — its Location/description/link body is
  detail you don't need at a glance, so it stays one `Tab` away rather
  than cluttering every day's listing by default (an event you've
  explicitly unfolded stays that way across redraws). The link is still
  always one glance away regardless — in the info buffer's "Links"/
  "Meeting" sections, same as any other entry's link (see above).
  Otherwise an ordinary foldable list of
  headlines — `i`, `dd`, `r`, `gd`, marks, and every other per-entry
  command all work exactly as they do in the outline, though any edit
  only lasts until the next `:sync-calendar` overwrites the file
  regardless — except `gt` (see Keybindings, below), which records the
  tag in `meeting_tags_file` instead of editing the event's own headline,
  so it survives every resync (see Tag-based meeting links, below).
  Every entry, anywhere in the org directory, linked to an event —
  attached via `gM` (see below), or sharing a tag with it (see Tag-based
  meeting links, below) — is shown nested right under it, after its
  Location/description/link body (if unfolded) — the same items the
  agenda's Meetings section groups by meeting (below), but shown here
  regardless of when the meeting falls, rather than only one starting
  today or within the next 24 hours, and regardless of whether the event
  itself is folded — unlike the body, a linked item needs attention,
  not just detail, so it isn't worth hiding behind an extra `Tab`. Ordered
  by `CREATED` (oldest first) rather than by where each item currently
  sits in the outline, so entries `o`/`O` capture straight to a meeting
  (see Keybindings, below) keep showing up in the order they were
  actually captured even after being filed away somewhere else entirely.
- **Reference** (`:reference`) — the outline's counterpart for reference
  material: every `.org` file in `reference/` (see above), shown exactly
  like the outline — a header row per file, then its foldable headlines
  and body text, with `i`, `o`, `dd`, `p`, `>>`, marks, search, `gt` and
  every other editing command working the same — but with task state
  ignored. A headline's TODO keyword is shown as ordinary title text
  (unstyled, never struck through) and its `SCHEDULED`/`DEADLINE`/`CLOSED`
  dates aren't shown; stale `DONE` entries are never hidden (see
  `:toggleclosed`); and `r`/`R`/`gd` refuse with a message rather than set
  state that nothing would use. Reference entries never appear in the
  plain outline, and the agenda's Overdue/Due Today/Upcoming/Next Actions
  sections skip them, so nothing in `reference/` is ever mistaken for a
  task. Tags are how reference material surfaces elsewhere: an entry
  tagged `@alice` or `kubernetes` shows up in `:tags` under that tag like
  any other entry, and — since a shared tag links an entry to a calendar
  event (see Tag-based meeting links, below) — nested under every
  matching meeting in `:calendar` (and in the agenda's Meetings section),
  so the runbook or notes you need are right there when the meeting is.
  (A reference entry marked `DONE`/`CANCELLED` is still excluded from
  meeting links, as with any entry.) A file in `reference/` is never one
  of the special files above: `reference/inbox.org` isn't the inbox.
- **Meeting tags** (`:meeting-tags`) — every entry in `meeting_tags_file`
  (default `meeting-tags.org`; see Tag-based meeting links, below), an
  ordinary foldable/editable outline (same `i`, `dd`, `r`, marks, etc. as
  any other file) rather than something tool-managed like `calendar_file`
  — nothing ever regenerates it, so it's exactly as durable as any other
  org file, and is as versionable as any other file. A
  record's own row skips the indent/fold column every other headline row
  reserves — it's always effectively top-level here, and never has
  foldable content of its own in practice.
  Each headline records one or more tags (its own `:tag:`s) that apply to
  one or more meetings, named by `MEETING_TAG_RECURRING_EVENT_IDS`/
  `MEETING_TAG_EVENT_IDS` properties (space-separated, either or both) —
  `gt` on a `:calendar` entry creates or updates one of these
  automatically (see below), but hand-adding a second ID to an existing
  entry's property here is how you group two otherwise-unrelated
  meetings (different recurring series, or a recurring series and a
  one-off event) under the same tag. Every calendar event a record's IDs
  currently resolve to — every synced occurrence, for a recurring series
  — is shown nested one level under it (`l`/`h` step down to/back up from
  it like any other parent/child pair, though its rendered indent is
  capped rather than growing with how deeply the record itself happens to
  be nested), the same `:calendar`-style event row (time before title,
  its own body a `Tab` away) since it's the very same synced headline;
  edits made here, `gt`
  included, apply to it exactly as they would from `:calendar` itself. A
  record with nothing nested under it is a stale
  one — its ID(s) no longer match
  anything currently synced (a deleted/recreated series, or a one-off
  event that's aged out of the sync window).
- **Tags** (`:tags`) — every entry, across every file (including those in
  `reference/`, which is how reference material is found by topic;
  `calendar_file` and
  `meeting_tags_file` excluded, same as the outline view above — a synced
  calendar event's own attendee/`recurring` tags, and a meeting-tags.org
  record's own tag, aren't outline entries to catalog here), grouped
  under one flush-left header row per tag, tags listed alphabetically —
  `✻` last, listing every entry with a Claude session (see Passing
  context to a command, above).
  Within each tag, entries are ordered by `CREATED` (oldest first) rather
  than by file/tree position — the same ordering `:calendar` uses for the
  items nested under each meeting (see above) — so where something's
  eventually filed away doesn't reshuffle the order it was actually
  captured in. An entry with more than one tag appears once under each,
  and a `DONE`/`CANCELLED` entry stale enough to be hidden from the
  outline (see `hide_closed_after`, above) is hidden here too,
  subject to the same `:toggleclosed`. Each row is flat — title, tags, and
  a `[file › parent]` place tag, same shape as an agenda item or one of
  `:calendar`'s linked items — rather than a foldable subtree, since the
  same entry can legitimately appear more than once across the whole
  listing; `Enter` jumps to its real place in the outline, same as from
  agenda view. Otherwise an ordinary listing — `i`, `dd`, `r`, `gd`,
  marks, and every other per-entry command all work exactly as they do
  in the outline, operating on the entry's real place in its own file
  regardless of where it's shown here.
- **Agenda** (`:agenda`) — a flat, date-driven view across every file:
  **Overdue**, **Due Today**, and **Upcoming** sections built from
  `SCHEDULED`/`DEADLINE` timestamps, plus a **Next Actions** section
  listing every `NEXT`-keyword headline regardless of whether it has a
  date. `SOMEDAY` items are excluded entirely. An item with both a
  schedule and a deadline can appear in two sections. A **Meetings**
  section follows: for each calendar meeting `:sync-calendar` has synced —
  a recurring series or a one-off event alike — that starts sometime
  today or within the next 24 hours (current ones included), a header
  row grouping every item — anywhere in the org directory,
  `DONE`/`CANCELLED` excluded — linked to that same meeting: explicitly,
  via a `GCAL_RECURRING_EVENT_IDS` (for a recurring series) or
  `GCAL_EVENT_IDS` (for a one-off event) property (set by `gM`, see
  below); or automatically, by sharing a tag with it (see Tag-based
  meeting links, below) — i.e. something raised during a past occurrence
  (or, for a one-off event, just attached ahead of time), or otherwise
  connected to it, that might need attention this time around. Meetings
  are in chronological order; a meeting with nothing linked to it is left
  out entirely, and an item linked to more than one meeting legitimately
  shows up under each.
- **Snippets** (`:snippets`) — the agenda's backward-looking counterpart:
  everything completed today or in the previous 6 days
  (`snippets_window_days`), grouped under one flush-left header per day,
  both the days and the entries within each day newest first; a day with
  nothing completed is left out. An entry counts if it's in a done state
  with a `CLOSED` stamp in the window, or if it's a repeating item whose
  `LAST_REPEAT` is. A repeating item records only its latest completion
  (see File format, below), so it appears at most once, on that day, and
  still shows its active keyword; that includes a repeating item last
  marked `CANCELLED`, which records the same thing as `DONE`. A keyword
  with `snippets = "skip"` is left out however it was completed, as the
  built-in `CANCELLED` is (if you define your own `[[keywords]]`, set it
  on any you don't want here). Every completed entry gets its own row,
  even one nested under another completed entry, with the closing note
  from that completion, if it has one (see File format, below), right
  under it. `hide_closed_after`
  doesn't apply here, and neither `reference/` nor `calendar_file` nor
  `meeting_tags_file` is included. Rows are flat, with a `[file › parent]`
  place tag, and every per-entry command works as in `:tags`. `Enter`
  jumps to the entry's real place in the outline; if the outline is hiding
  it as stale, that also switches the hiding off, as `:toggleclosed`
  does.
- **Config** (`:config`) — a read-only listing of every configurable
  setting's current, effective value (after flags/config
  file/built-in-default resolution), including whether hide-closed
  filtering is currently on or off. `:outline` returns to the outline.
- **Log** (`:log`) — a read-only, chronological list of every external
  command orgtd has run since it started (`$EDITOR`, `:run` commands,
  and any configured URL formatter, live or batch): a line when it starts (with its pid and
  full argument list), one line per line fed to its stdin (if any, e.g.
  every URL a `:format-links` batch sends), one line per stdout/stderr
  line as it's produced, and a line for its exit code, each individually
  timestamped, tagged `START`/`STDIN`/`STDOUT`/`STDERR`/`EXIT`, and
  marked with the process's pid (`-` if it never actually started) —
  useful for telling apart two commands that happen to run at once, e.g.
  a `:format-links` batch alongside a live in-editor formatter
  invocation. An entry too long for the window wraps, its continuation
  lines indented to line up under its text, and rewraps on resize — so
  nothing (a long URL, an error message) is cut off at the right edge.
  Doesn't live-update — re-run `:log` to see anything logged
  since it was last opened.
  `:outline` returns to the outline.
- **Help** (`:help`) — this README, rendered as terminal-styled markdown
  (via [glamour](https://github.com/charmbracelet/glamour) — headings,
  tables, code blocks and all), embedded into the binary at build time
  (see `go:embed`) so it's available even if README.md isn't sitting
  next to wherever the binary was installed. Re-wraps automatically if
  the terminal is resized while it's open. `:outline` returns to the
  outline.

The bottom of the screen has up to three parts. The status line (current
view/directory and item position, plus a `⟳` at its far right while
background work — a calendar sync, manual or automatic, or a
`:format-links` batch — is still running) is always exactly one line,
always visible regardless of mode; the command line below it — where
`:`/`/`/`?` input, prompts (deadline, the status picker), the
visual-mode banner, and messages all appear, blank when there's nothing
to show — is always exactly one more. Neither ever
replaces the other, mirroring vim's own statusline-above-command-line
layout. With the screen split into windows (see Windows, below), each
window has its own info buffer and status line along its bottom edge
instead — the info buffer showing that window's current entry's links
and meetings, marks, registers, and (in the active window) any picker
or completion list — and the one command line sits below them all.

Above both, an info buffer holds whatever might need more than one
line, grouped into labeled sections — collapsed entirely (zero height)
when none of them apply, so it never costs a permanent row on screen.
Sections are ordered so that the longer one tends to stay open, the
closer to the bottom (i.e. the closer to the always-visible status/
command lines below) it sits — keeping whatever's actually on screen
from shifting position any more than it has to:

- **Tags** — while `gt` (see Keybindings, below) is prompting for a tag
  and more than one existing tag matches what's typed so far, the
  matches themselves, one per line.
- **Status** — while the `r`/`R` status picker (see Keybindings, below)
  is open, every selectable state, one per line, with its bracketed
  shortcut and the currently highlighted one in reverse video.
- **Attach meeting** — while the `gM` picker (see Keybindings, below) is
  open, every meeting matching what's been typed so far, in chronological
  order, one per line — resolved date/time first (so times line up in a
  column and are easy to compare down the list), then title, then
  `(attached)` for one already attached to the target entry — with the
  currently highlighted one in reverse video.
- **Links** — every org-mode link literally in the current entry's
  title or body.

  In the outline itself, a link is shown as just its description (or
  its URL, if it has none), underlined. When it points at an `http://`,
  `https://` or `mailto:` URL, that text is also a terminal hyperlink
  (OSC 8): in a terminal that supports them (iTerm2, kitty, WezTerm,
  Ghostty, and others) it opens on a click — cmd-click in iTerm2.
  Terminals without support show the same underlined text. Any other
  link (`id:`, `file:`, a heading search) stays plain underlined text.
  orgtd strips escape sequences from org text before drawing it, so a
  file can't plant a hyperlink of its own. Only orgtd's links get through.
- **Meeting** — one line per calendar meeting the current entry is
  linked to (see below for how a link is established), each showing the
  meeting's name, start time (when still resolvable), and link.
- **Registers** — the read-only `%` register (the inbox item pinned by
  `:review`, in whichever view you're in; see Reviewing the inbox, below, with its `CREATED` property and any
  `SCHEDULED`/`DEADLINE`; see Registers, below), then the unnamed paste
  register (whatever `dd`/`<N>dd`/visual-mode `d`/`y`/`yy` last stored),
  then every non-empty named (`a`–`z`) and numbered (`0`–`9`) register, one row per entry. Hidden by default — `:registers` toggles showing them — except `%`, which is always shown while `:review` is on. The whole section is capped at 20 rows: a register that wouldn't fit whole is dropped along with the ones after it, summarized as "...and N more registers" — the numbered history goes first, so the registers you chose by name last longest.
- **Active marks** — every active mark (see Marks, below), sorted by
  letter, one row each — visible in every view until cleared.
- **Matches** — the same idea as **Tags** above, for command-mode (`:`)
  `Tab` completion (see Command mode, below) when more than one command
  name matches.

Any section past 20 lines collapses the rest into a trailing "...and N
more" summary rather than pushing the outline listing off-screen.

A meeting link comes from: the entry's own `GCAL_HTML_LINK`, if it's
itself a synced calendar event (i.e. you're browsing `:calendar`,
above); or, if it's been attached to a meeting via `gM` (see below) — a
recurring series or a one-off event alike — its
`GCAL_RECURRING_EVENT_LINKS` (recurring) or `GCAL_EVENT_LINKS` (one-off)
property, one `<meeting name>` per attached meeting. Since that
property is a snapshot taken at attach time rather than a live lookup,
the name and link keep working indefinitely — you can jump straight to
the meeting no matter how long ago it was attached, even long after
`:sync-calendar` has resynced `calendar.org` and the meeting no longer
has anything cached (though its start time, unlike the name/link, is
always a live lookup, so it stops showing once the meeting ages out —
see below). A `GCAL_RECURRING_EVENT_IDS`/`GCAL_EVENT_IDS` with no
matching `GCAL_RECURRING_EVENT_LINKS`/`GCAL_EVENT_LINKS` entry (e.g. one
hand-attached to a task directly, per DESIGN.md's project↔meeting
association, rather than via `gM`) falls back to resolving those IDs
against whatever `calendar.org` currently has cached, which — unlike
the `..._LINKS` properties — can come up empty if the meeting has aged
out; an ID that resolves neither way is simply left off. Finally, one
more meeting per meeting the entry is linked to purely by a shared tag
(see Tag-based meeting links, below), resolved live against whatever
`calendar.org` currently has cached (there's no attach-time snapshot for
a tag match, since there was never an explicit attach) — skipped if it
names a meeting already covered by one of the property-based entries
above, so a meeting that's both `gM`-attached and tag-matched isn't
listed twice. In every case, the meeting's start time shown alongside
its name/link is always a live lookup against whatever `calendar.org`
currently has cached — it simply doesn't show once the meeting ages out
of the sync window, the one part of a meeting entry that can't fall
back to a snapshot.

## Tag-based meeting links

Tagging an entry (`gt`, or by hand in `i`/edit mode — see Keybindings,
below) with the same tag a synced calendar event carries links the two
together automatically — no `gM` attach needed. This is mainly useful
with `:sync-calendar`'s own attendee tags (see Calendar sync, below):
tag a task `@alice`, and it's now linked to every meeting `:sync-calendar`
has tagged alice as an attendee of, the same as if you'd `gM`-attached
it to each one by hand. A tag-based link behaves exactly like a `gM`
attachment everywhere orgtd shows one: the entry shows up in the
agenda's Meetings section and nested under the event in `:calendar`
(both above), the event's title/link show up on the entry's own status
line, and the entry's row shows the gutter's `▣` meeting marker. The
`recurring` tag `:sync-calendar` stamps onto every occurrence of a
recurring series (see Calendar sync) never counts as a match on its
own — every recurring meeting carries it, so matching on it would link
any entry tagged `recurring` to all of them, which is noise rather than
a meaningful connection. A synced calendar event is never treated as
linked to its own meeting (or a sibling occurrence of the same series)
just for sharing its own tags with itself — only entries elsewhere in
the org directory can be linked this way.

Tagging a *calendar event itself* (`gt` on its row in `:calendar`) works
the same way from the outside, but since `calendar.org` is wholesale
regenerated by every `:sync-calendar` run, the tag can't live there —
instead it's recorded durably in `meeting_tags_file` (default
`meeting-tags.org`; see Views, above, and `:meeting-tags`), keyed by the
event's `GCAL_RECURRING_EVENT_ID` (a recurring series, tagged as a
whole) or `GCAL_EVENT_ID` (a one-off event). Every place a tag-based
link shows up — the agenda's Meetings section, `:calendar`'s nested
linked items, the gutter's `▣` marker, the info buffer's "Meeting"
section, `gM`'s filter-by-tag — treats a `meeting_tags_file` tag exactly
like one literally present on the calendar event, computed live so a
`gt` edit (or a hand-edit in `:meeting-tags`) takes effect immediately
and keeps working across every future resync. `"recurring"` is reserved
here too and can't be used as a meeting tag. To group two distinct
meetings (different recurring series, or a recurring series and a
one-off event) under the same tag, add a second ID to the relevant
`MEETING_TAG_RECURRING_EVENT_IDS`/`MEETING_TAG_EVENT_IDS` property by
hand in `:meeting-tags`, rather than tagging each meeting separately —
one entry, one tag set, multiple meeting IDs.

## Reviewing the inbox

`:review` is a toggle, not a view: it turns
the read-only `%` register on or off, so whatever view you're in stays
exactly as it is. On, it pins the inbox's first non-`DONE`/`CANCELLED`
top-level headline in `%`, shown in the info buffer's "Registers:"
section (see Views, above) alongside its `CREATED` property — so you can
see how long it's been sitting there — and any `SCHEDULED`/`DEADLINE` it
already has, and marked with a `●` in the gutter of its real row. Then
navigate the rest of the outline and file it away: `"%p` / `"%P` paste a
copy of it (see Registers, below), and `gc` jumps to its real row
(switching to the outline first if the current view doesn't show it).
Deleting the pinned item, or marking it `DONE`/`CANCELLED`, advances to
the next pending one; `:next` / `:prev` step through pending inbox items
manually. Running `:review` again turns `%` off, as does
`:clear-registers`. With nothing pending in the inbox, `%` is simply
empty.

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
| `Page Down` / `Page Up` | Full-page down / up. A page is what fits on screen, body and wrapped lines included, and the view scrolls with the cursor, so a page turn never skips anything unseen. The cursor settles on the start of an entry, so a page can come up a little short; an entry taller than a page is stepped past whole |
| `ctrl-e` / `ctrl-y` | Scroll the view down/up by one line, like vim — the cursor stays put unless the scroll would push it off-screen, in which case it's dragged along just enough to stay visible |
| `Tab`, `za`/`zo`/`zc`, `zA`/`zO`/`zC` | Vim-style folds. `zo` opens, `zc` closes, `za`/`Tab` toggles the current entry's fold, one level only; `zc` on an entry that is already closed (or has nothing to fold) closes its parent instead and moves the cursor there. A count repeats them: `2zc` closes the entry then its parent; `2zo` opens the entry and its children's folds. `zA`/`zO`/`zC` act on the whole subtree recursively (`zA` opens it if the entry is folded, closes it otherwise). On a file's own row, `zA`/`zO`/`zC` act on every entry in the file at once (`zA` opens them all if every one is folded, closes them all otherwise); the one-level `za`/`zo`/`zc` do nothing there, since the file itself can't be folded. A folded entry shows how many descendants it hides, as a muted `(N)` after its title (entries hidden by `hide_closed_after` aren't counted). An entry too wide for one line can be folded even with no body or children — see Long entries, below |
| `Enter` | In agenda, `:tags` or `:snippets` view, jump to that item's real place in the outline. In calendar view, on an item attached to a meeting via `gM` (see below), same thing — a no-op on the meeting's own row, since `calendar_file` isn't part of the outline at all |
| `ctrl-o` / `gi` | Jump back / forward through the jump list — vim's own `ctrl-o`/`ctrl-i`, tracking where you were before a "large" move: `gg`/`G`, `{`/`}`, a confirmed search (`/`/`?`, not `n`/`N` repeats), jumping to a mark (`'<letter>`) or to the review target (`gc`, which switches to the outline if the current view doesn't show it), and switching views entirely (`:agenda`, `Enter` from it, `:calendar`, `:outline`, ...) — never for `j`/`k` or fold/edit commands, or the list would be useless clutter. Bound to `gi` rather than `ctrl-i`: in a plain terminal `ctrl-i` and `Tab` are the same byte, so there's no way to bind it separately from the fold-toggle key above. A no-op at either end of the list |

### Long entries

An entry that doesn't fit on one line is laid out one of two ways. Folded,
it stays on one line: its dates switch to a short form (`S:10-31`,
`D:11-02`, `C:10-30`, with the year only if it isn't this year), then the
title keeps its full width up to about 60% of the row, and tags and dates
share what's left, ellipsized as needed, with any share one part doesn't
use going to the others. Unfolded, nothing is cut: title, tags and dates
each wrap within a column of their own, all starting on the first line,
so a long title never pushes the tags down. Tags break between tags, and
dates stack one per line in their full form. A wrapped entry's extra lines
belong to it the way its body lines do, so `j`/`k` step over them.

A leaf too wide for one line gets a fold arrow, and unlike any other
entry it starts folded, so long leaves don't stretch the outline; `Tab`
shows it whole. Widen the window until it fits and the arrow goes away.
The agenda, `:tags`, the items linked under a `:calendar` event and
`:meeting-tags` records have no fold to open, so they stay on one line
with the same division of space; their `[file › parent]` tag loses the
parent before the file name.

### Editing

| Key | Action |
|---|---|
| `i` | Edit the current entry's own text (title, planning line, properties, and body — not its children, which are left untouched) right where it is, in insert mode (see Insert mode, below), caret at the start of the title — after any TODO keyword and priority (`TODO [#A] `), or at the very start in `:reference`, which shows a keyword as plain title text. The text has no leading bullet (`*`) to edit around, and the whole entry — not just the title — is dedented by one level to match, so properties/planning/body lines that would otherwise still be indented under the now-absent bullet start flush left too; it's just the entry's text, plain, re-indented automatically when you're done. On a file's own header row, edits the whole file in `$EDITOR` instead (after a confirmation, since this discards undo history and marks for that file) |
| `I` | Same as `i`, but the caret lands on the entry's TODO keyword — or the empty slot where one goes — to change, add or remove it with a single key (see The keyword, under Insert mode, below); vim's own "insert at the start of the line". In `:reference`, which has no keyword slot, the very start of the line |
| `a` | Same as `i`, but the caret lands one character further right — vim's own "append after the cursor", with `i`'s position standing in for the cursor (so after the title's first character) |
| `A` | Same as `i`, but the caret lands at the end of the title instead (vim's own "append" position) — before any tags |
| `ctrl-g` | Edit the current entry in `$EDITOR` instead, and apply the result when it exits — the same text as `i`, starting on line 1 (a vim-family `$EDITOR` — vi/vim/nvim/gvim/mvim — starts there in its normal mode, as you were in orgtd's). Below it, git-commit-style, is a comment block sketching the entry's place in the outline (its file, parent, siblings, and its own children, each with their real bullets) with a `[THIS ENTRY HERE]` marker standing in for the entry itself — for orientation only; editing it has no effect. On a file's own header row, same as `i`. In insert mode, `ctrl-g` hands what you've typed so far to `$EDITOR` instead (see Insert mode, below) |
| `o` / `O` | Insert a new entry after / before the current one (or at the end/start of a file, from a file header row), and start typing it in insert mode — the same bullet-free text as `i`, blank, caret on line 1, prefilled with a `CREATED` property set to now (edit or delete it like anything else before leaving insert mode). Leaving insert mode with no title typed drops the new entry again. In `:calendar` view, on a row associated with a meeting — the event's own row/body, or an item already linked to it (see below) — `o`/`O` behave identically instead: both append the new entry to the end of the inbox (same target as `gC`) and attach it to that meeting outright, the same properties `gM` would set but chosen automatically, with no picker step, since the meeting is already unambiguous from the cursor's row — the capture and the attach are one undo step together, unlike tagging a meeting via `gt`/`gM` elsewhere, which is always its own separate step. There's no "before"/"after" left to distinguish once the target is always the end of one shared file rather than a position relative to the cursor; the entry's place among any others linked to the same meeting (see `:calendar`, below) is decided by `CREATED` instead, so repeated `o`/`O` presses still show up in the order they were actually captured regardless of which file each one is later filed into. Since `:calendar` can't show the new entry until it's attached, it's typed in outline view, at the end of the inbox, as with `gC`; but unlike `gC`/`gX` (below), once you leave insert mode it goes back to `:calendar` view, right on the newly captured entry now nested under its meeting (or, if cancelled, back where it was) |
| `dd` | Delete the current entry and its subtree (undoable; also fills the paste register, or the named one given by a `"<register>` prefix — see Registers, below). The cursor stays at the same screen row, landing on whatever now occupies it (or the new last row, if it was the last one) — matching vim's own `dd` |
| `<N>dd` | Delete the current entry and the next N-1 entries and their subtrees, as one undo step (e.g. `3dd` deletes 3 entries). A count of 1 (or none) is exactly plain `dd`. A higher count fills the register with all of the deleted entries (top-to-bottom order preserved), pasted back together as a group by a single `p`/`P` |
| `yy` / `Y` | Yank the current entry and its subtree into the paste register, without deleting it |
| `<N>yy` / `<N>Y` | Yank the current entry and the next N-1 entries and their subtrees, as a group pasted back together by a single `p`/`P` — the yank counterpart of `<N>dd`. A count of 1 (or none) is plain `yy` |
| `p` / `P` | Paste the unnamed register's contents (or the one named by a `"<register>` prefix); a count repeats it (`3p`) after / before the current entry, re-indented to fit. If the register holds more than one entry (from `<N>dd` or a visual-mode `d`/`y`), all of them are pasted together, in the same order they were deleted/yanked in |
| `>>` / `<<` | Demote / promote the current entry (re-parents it, not just cosmetic indentation) |
| `r` / `R` | Set the TODO state: the entry goes into insert mode with the caret on its keyword (see The keyword, under Insert mode, below), the `Status:` list of states below it with the current one highlighted. Type a state's bracketed shortcut (configurable, see `[[keywords]]`) to set it — straight away, back in normal mode — or `-`/`Delete` for none; `Esc` leaves it as it was. Choosing a done state for an entry that isn't already done first asks for a one-line closing note, typed right in the entry: it's shown as it'll be — the new state, its `CLOSED` stamp (or a repeating item's advanced dates), and the note's heading — with the caret on an empty line under the heading. `Enter` or `Esc` closes it with that note, or with none if left empty; `u` undoes the state change and note together. Moving off the keyword into the entry's text (`→`, …) makes it an ordinary edit instead. See File format, below |
| `<N>r` / `<N>R` | Open a picker of the same states — type to filter, or use a shortcut; `↑`/`↓`/`j`/`k` and `Enter` choose too, `Esc` cancels — and apply the chosen state to the current entry and the next N-1 (each independently, nesting included), as one undo step (e.g. `2R` sets the current and next entry). A closing note is asked for once, and goes on every entry this closes: while it's typed, it shows under each of them, a caret in every copy; `Enter` or `Esc` applies it, as for one entry |
| `gd` | Set the current entry's deadline, in insert mode (see Timestamps, under Insert mode, below): the caret lands on its `DEADLINE` date, ready for `Enter` to change it. With no deadline yet, `DEADLINE: ` is added to the planning line (just below the title, after any `SCHEDULED`, before any `CLOSED`; the line is created if need be) with an empty date field already open after it — an exact date, `3d`/`2w`/`1m`/`1y` shorthand, or a fuzzy phrase like "next tuesday", previewed as you type. Leaving it empty takes the label back out. As soon as the date's done with — set, or left as it was — insert mode ends too, with the change applied, back in normal mode |
| `gC` | Capture: append a new entry to the end of the inbox file and start typing it in insert mode, as `o` does, regardless of the current cursor position or view (same as `:capture`). From a view that doesn't list it (the agenda, say), it first switches to outline view, on the new entry at the end of the inbox, so it's typed in place there like any other; once you leave insert mode the cursor stays on it, ready for further edits right away (`ctrl-o` jumps back to where you were). Cancelling — leaving insert mode with no title typed — returns to the view you started from. Deliberately doesn't guess at a calendar meeting to attach, even one in progress at the moment of capture — see `gM` below, the interactive way to do that |
| `gM` | Open a picker (type to filter by title or attendee tag — e.g. "alice" matches a meeting tagged `@alice`, same tags `:sync-calendar` stamps on for Tag-based meeting links, below — ↑/↓ to browse, Enter to pick, Esc to cancel) over every distinct meeting `:sync-calendar` currently has synced at least one instance of — a recurring series (deduped by series) or a one-off event alike — and toggle it on or off the current entry's `GCAL_RECURRING_EVENT_IDS`/`GCAL_RECURRING_EVENT_LINKS` (recurring) or `GCAL_EVENT_IDS`/`GCAL_EVENT_LINKS` (one-off) properties (the `..._LINKS` one is a title/link snapshot, used by the info buffer's "Meeting" section — see above — to keep showing the meeting's name and link even after it drops off the calendar entirely; see the agenda's Meetings section, also above, for what the IDs are for). Picking a meeting already attached detaches it instead of adding a duplicate. A no-op (with a status message) if `:sync-calendar` hasn't synced anything at all — there's nothing to offer. A linked entry (attached via `gM`, or tag-matched — see Tag-based meeting links, above) shows a `▣` in its own gutter column (alongside any mark, lock, or dirty marker), so whether it's linked to a meeting is visible at a glance, in every view, without opening it |
| `gX` | `gC` immediately followed by `gM`: capture as usual, and once you leave insert mode, the meeting picker opens automatically on the just-captured entry — for capturing something during a meeting and attaching that meeting in one motion. Cancelling the capture (no title typed) never opens the picker; if nothing's synced yet, the capture still commits, just without the picker (same no-op message as a bare `gM`) |
| `gt` | Edit the current entry's tags, in insert mode (see Tags, under Insert mode, below), with the caret on a new, empty tag after the existing ones — type it, `Tab` to complete it against every tag used anywhere in the workspace, `Enter` for another, `Esc` when done. A tag matching one on a synced calendar event automatically links the two — see Tag-based meeting links, above. On a synced calendar event's own row (in `:calendar`, or nested in `:meeting-tags`), `gt` instead prompts for one tag and toggles it in `meeting_tags_file`, so it survives `:sync-calendar` (see Tag-based meeting links, above) — typing one already recorded removes it, `Tab` completes, and Enter with nothing typed, or `Esc`, cancels; `"recurring"` is refused there as a reserved name |
| `u` / `ctrl-r` | Undo / redo (single global stack for the session) |

### Insert mode

`i`/`I`/`A`, `o`/`O`, `gC`/`gX` and `:capture` all edit an entry's text
inside orgtd, vim-style: the entry's rows in the outline turn into its
raw text (the same bullet-free text `ctrl-g` hands to `$EDITOR`), with a
caret, and `-- INSERT --` shows on the command line. Everything typed
goes into the entry, normal-mode commands included, until you leave.

| Key | Action |
|---|---|
| `Esc` / `ctrl-c` | Leave insert mode, applying the edit — one undo step for the whole session, like vim's `i`…`Esc`. Leaving an existing entry's text unchanged records nothing; emptying it out leaves it as it was; a new entry left without a title is dropped |
| `ctrl-g` | Open what you've typed so far in `$EDITOR` (with the same comment block as normal-mode `ctrl-g`; a vim-family `$EDITOR` starts with the cursor where your caret was, in its normal mode). When it exits, the editor's version of the text is applied and you're back in normal mode, as if you'd pressed `Esc` — exiting the editor means you're done editing. If the editor fails, you stay in insert mode with what you'd typed |
| `Enter` | New line (planning, properties and body lines go below the title, as in the file) |
| `←`/`→`/`↑`/`↓`, `ctrl-b`/`ctrl-f`/`ctrl-p`/`ctrl-n` | Move by character / line |
| `Home`/`End`, `ctrl-a`/`ctrl-e` | Start / end of the line — on the first line, the start and end of the title, then (pressed again) the keyword and the tags |
| `alt-b`/`alt-f`, `ctrl-←`/`ctrl-→` | Back / forward a word |
| `Backspace`, `Delete`/`ctrl-d` | Delete the character before / under the caret, joining lines at either end of one |
| `ctrl-w`/`alt-Backspace`, `alt-d` | Delete the word before / after the caret |
| `ctrl-u`, `ctrl-k` | Delete to the start / end of the line (`ctrl-k` at the end joins the next line) |

#### The keyword

An entry's TODO keyword counts as a single character in insert mode,
sitting in a slot of its own at the start of the first line — holding
the keyword, or nothing. `I`, or `←`/`Home` from the start of the title,
puts the caret on it: the whole keyword (or a blank cell, if there's
none) is highlighted, the same `Status:` list as `r` opens below it, and
the only things you can type are its shortcuts.

| Key, on the keyword | Action |
|---|---|
| A shortcut (`t`, `n`, `d`, …) | Set that keyword, with everything `r` does along with it — choosing a done state stamps `CLOSED` and asks for a closing note, on an empty line under its heading (`Enter` keeps the note, or none if it's blank, and carries on editing; `Esc` keeps it too, and ends insert mode, as always), and on a repeating item advances its dates instead — right away, in the text you're editing. The caret then moves to the start of the title. All of it lands in the outline with the rest of the edit when you leave insert mode, as one undo step |
| `-`, `Backspace`, `Delete`/`ctrl-d` | Remove the keyword |
| `→`, `End`, `↓` | Back into the text |

Elsewhere, the cursor motions step over the keyword as one character,
and `Backspace` (or `ctrl-w`) at the start of the title, or `ctrl-u`
anywhere on the first line, removes it whole, the same as `-`. A new
entry starts without a keyword, the caret on its title; press `←` to
give it one. `:reference`, which shows a keyword as plain title text,
has no slot: it's ordinary characters there. Typing a keyword's name
out at the start of the title makes it the keyword too — but only as
text, without the `CLOSED` stamp and the rest of what choosing it does.

#### Timestamps

Every org timestamp in the entry — active `<2026-11-17 Tue>` or inactive
`[2026-11-17 Tue 18:50]`, on the planning line, in a property like
`:CREATED:`, or in the title or body — counts as a single character too.
With the caret on one, the whole timestamp is highlighted as the caret,
and:

| Key, on a timestamp | Action |
|---|---|
| `Enter` | Change its date, right there: the timestamp becomes an empty field in its own brackets (`<▌>` or `[▌]`), with a preview after it — the date it's replacing until you type, then what you've typed resolves to and how far off that is (`→ Fri 2026-11-20, in 3 days`), or `✗ not a date`. It takes an exact date (optionally with a time), `3d`/`2w`/`1m`/`1y`, or a phrase like `next tue`, and the usual line-editing keys (`←`/`→`, `Backspace`, `ctrl-u`, `ctrl-w`, …). `Enter` puts the new date in place, the caret just past it; `Esc` does too, and ends insert mode, as always. Left empty, either keeps the old date; a date that doesn't parse keeps the field open, saying why. The new one stays active or inactive, as the old one was; like `gd`, a phrase without a time gives a plain date, and a repeater (`+1w`) isn't kept |
| `Backspace`, `Delete`/`ctrl-d` | Remove it — the timestamp's highlighted, so either delete key takes it (a Mac's "delete" key is `Backspace`) |
| Letters | Nothing — `Enter` changes the date |

Elsewhere, the cursor motions step over a timestamp as one character, and
`Backspace` just past one, or a word deletion (`ctrl-w`, `alt-d`) that
would cut into one, removes it whole. Each half of a range
(`<…>--<…>`) is a timestamp of its own. A label like `DEADLINE:` is
ordinary text while you edit, but a `SCHEDULED:`, `DEADLINE:` or
`CLOSED:` left on the planning line with no date after it is dropped
when you leave insert mode — and the planning line too, if that empties
it.

#### Tags

Each of an entry's tags — the `:home:errand:` at the end of its first
line — counts as a single character too. `→` from the end of the title,
`End` (twice, from elsewhere on the line), or `gt` moves into the tags:
an empty tag is added at the end for a new one (`:home:errand::`), the
caret highlights one tag at a time, and a `Tags:` list below shows the
workspace's tags that the one under the caret could be.

| Key, in the tags | Action |
|---|---|
| Letters, digits, `_@%#+` | Add to the end of the tag under the caret. Spaces and other characters a tag can't hold can't be typed |
| `:` | On to the next tag, if there is one — so typing `work:home` gives two tags |
| `Backspace` | Delete the tag's last character; on an empty tag, move to the one before, to carry on deleting there |
| `ctrl-w`/`ctrl-u`/`alt-Backspace` | Empty the tag under the caret |
| `Delete`/`ctrl-d` | Remove the tag under the caret |
| `Tab` | Complete the tag under the caret to the workspace tag it starts, or as far as several share |
| `Enter`, `End` | On to the new tag at the end, to type another |
| `←`/`→` | The tag before / after; `←` from the first leaves the tags, to the end of the title |
| `Home`, `↓` | Leave the tags, to the title's start / the line below |

When the caret leaves the tags (or insert mode ends), they're written
back: empty ones dropped, and any that repeat an earlier one. Elsewhere
on the first line, the caret stops at the end of the title, and
`Delete` or `ctrl-k` there leave the tags alone.

Long lines wrap at the window's edge. Bare URLs are formatted into org
links when you leave insert mode, the same as after `$EDITOR` (see
`--url-formatter`).

Every `$EDITOR` session above (`ctrl-g`, in either mode) writes its
buffer to a file under `scratch_dir` (by default, a `scratch/` directory
inside `--dir`), in a
dated subdirectory per day (e.g. `scratch/2026/10/02/`) and named after
the moment the session started, down to the nanosecond (so two sessions
started in the same second never collide) rather than a random name.
Unlike a plain temp file, it's never deleted — once written, it stays
there indefinitely as a failsafe, so whatever came back from the editor
is still recoverable by hand even if it's ever lost some other way
(an editor crash, accidentally discarding the buffer unsaved, or any
other reason a round-trip through the outline doesn't end up reflecting
it). Since the buffers sit in those dated subdirectories, they're never
scanned as `--dir`'s own `.org` files (see above) and never show up in
the outline, wherever `scratch_dir` points; they're just there on disk if
you need to go looking (and in your VCS, if `scratch_dir` is inside
`--dir`, unless you ignore it or point `scratch_dir` elsewhere).

### Windows

Like vim, the screen can be split into windows, each showing any view
with its own cursor, scroll position and jump list (`ctrl-o`/`gi`) — e.g.
the agenda beside the outline, or two far-apart parts of the outline at
once. Everything else is shared: every window shows the same files, so an
edit made in one appears in all of them right away (without scrolling
the others: rows added or removed above a window don't shift it), and folds, marks,
registers, undo history and the last search are shared too. A new window
opens on the same view and entry as the current one, above it (`:split`)
or to its left (`:vsplit`), and becomes the active one. Windows split the
space evenly; each has its own info buffer and, below it, its own status
line. The active window stands out twice over: its status bar is the
usual bold one on light grey while the others' are a darker grey, and only its cursor
row gets the bright highlight — the other windows' cursor rows are a
faint tint, so you can still see where each one is (both configurable:
`status_bar_inactive_fg`/`_bg` and `inactive_cursor_row_bg` under
`[colors]`, above). Since each info buffer grows and shrinks inside its own window,
the windows themselves never move as you scroll. Side-by-side windows are
separated by a `│` column.

| Key | Action |
|---|---|
| `ctrl-w s` / `ctrl-w S` / `ctrl-w ctrl-s` | Split the current window in two, stacked (same as `:split`) |
| `ctrl-w v` / `ctrl-w ctrl-v` | Split the current window in two, side by side (same as `:vsplit`) |
| `ctrl-w h` / `j` / `k` / `l` | Move to the window to the left / below / above / right (also `ctrl-w` then the `ctrl-` variant of each, or an arrow key). Where several windows border that side, the one level with the cursor is chosen |
| `ctrl-w w` / `ctrl-w ctrl-w` / `ctrl-w W` | Move to the next / previous window, top-left to bottom-right, wrapping around |
| `ctrl-w p` | Move to the previously active window |
| `ctrl-w t` / `ctrl-w b` | Move to the top-left / bottom-right window |
| `ctrl-w c` | Close the current window (same as `:close`); refused for the last one |
| `ctrl-w q` | Close the current window, or quit if it's the last one (same as `:q`) |
| `ctrl-w o` | Close every other window (same as `:only`) |

A closed window's space goes to the window above it (or to its left), or
below (to its right) if it was the first, and the cursor goes with it.

### Visual selection

| Key | Action |
|---|---|
| `V` | Enter visual line selection at the current entry. Navigation keys (`j`/`k`, `gg`/`G`, `{`/`}`, `l`/`h`, `^`/`$`, `ctrl-d`/`ctrl-u`, `Page Down`/`Page Up`, `ctrl-e`/`ctrl-y`) extend the selection instead of just moving; `V` again or `Esc` cancels it |
| `d` | (in visual mode) Delete every selected entry and its subtree. A selected entry whose ancestor is also selected isn't deleted separately — deleting the ancestor already removes it. One undo step per file touched (almost always just one). Fills the paste register with everything deleted (top-to-bottom order preserved), so `p`/`P` pastes the whole selection back as a group |
| `y` | (in visual mode) Yank every selected entry and its subtree into the paste register, without deleting them — the visual-mode equivalent of `yy`. Same ancestor-covers-descendant rule as `d`. Leaves normal mode afterward, same as `d`/`r`/`R` |
| `:` | (in visual mode) Open the command line, as in normal mode, remembering the selection — `:run` passes every selected entry to a command with `context = true` (see Passing context to a command, above); every other command ignores it. The command line shows how many entries are selected |
| `r` / `R` | (in visual mode) Open the same status picker as `<N>r`, applying the chosen state to every selected entry independently (nested entries included, unlike `d`) — also one undo step per file touched. A closing note is asked for once, shown under each entry it goes on, as with `<N>r` |

The cursor's own entry keeps the usual highlight color; the rest of the
selection is shaded differently, so which end is the actual cursor is
always clear.

### Marks

| Key | Action |
|---|---|
| `m<letter>` | Mark the current entry with a letter (a–z). Marking an entry that already has a different letter replaces it; marking it with the *same* letter again clears it — an entry holds at most one mark |
| `'<letter>` | Jump to a mark |
| `:delmarks <letters>` / `:delmarks!` | Delete specific marks / delete all marks |

Every active mark stays pinned in the info buffer at the bottom of the
screen (see Views, above), in every view, until cleared or moved
elsewhere.

After `:registers` (a toggle; off by default), whatever
`dd`/`<N>dd`/visual-mode `d`/`y`/`yy` last put in the paste register
stays pinned in the info buffer too, under the "Registers:" label, right
alongside marks — so it's obvious what `p`/`P` will paste
next even for `yy`/visual-mode `y`, which otherwise leave the screen
looking unchanged. Capped at 5 entries shown at once (a big `<N>dd` or
visual-mode delete/yank collapses the rest into a trailing "...and N
more" line) so a large register can't push the actual listing
off-screen. `:clear-registers`
empties every register (and the pinned display) without needing another `dd`/`yy`.

### Registers

| Key | Action |
|---|---|
| `"<register>` | Name the register the next command uses, like vim. Applies to exactly one command, and survives a count (`"a2dd`) |
| `"<a-z>yy` / `"<a-z>Y` / `"<a-z>dd` / `"<a-z><N>yy` / `"<a-z><N>dd` | Yank or delete into a named register instead of (only) the unnamed one. Also works in visual mode (`"ay`, `"ad`). `Y` is `yy` |
| `"<A-Z>yy` etc. | An uppercase name *appends* to the lowercase register (`"Ayy` adds to `a`) instead of replacing it |
| `"<a-z>p` / `"<a-z>P` | Paste from a named register |
| `"%p` / `"%P` | Paste a copy of the entry `:review` is currently pinning. `%` is read-only: it's empty while review is off or with nothing pending in the inbox, and `"%dd`/`"%yy` are refused (as are `"0`–`"9`, which fill themselves) |
| `""` | The unnamed register, explicitly — what a bare `dd`/`yy`/`p` uses |
| `"0p` | Paste the last yank. Unlike the unnamed register, a delete never overwrites it, so `yy`, then `dd` on something else, then `"0p` still pastes the `yy`. Read-only, and not set by a yank into a named register |
| `"1p` … `"9p` | Paste from the delete history: `"1` is the last delete, `"2` the one before it, and so on up to nine, each delete shifting the older ones down. Read-only, and not touched by a delete into a named register (`"add`) or the black hole |
| `"_` | The black hole: `"_dd` / `"_yy` store nothing anywhere, leaving every register as it was; `"_p` has nothing to paste |
| `<N>p` / `<N>P` | Paste the register N times, as one undo step (`3p`, `2"aP`) |

Every write except to the black hole also lands in the unnamed register (as in
vim), so a bare `p` pastes whatever was stored last, wherever it went. An unknown register
name is rejected. `:registers` toggles showing all of them in the info
buffer's "Registers:" section (see Views, above), each row starting with
its register's name — hidden by default, apart from `%` while `:review`
is on;
`:clear-registers` empties every one. Registers last for the session,
not across restarts.

### Search

| Key | Action |
|---|---|
| `/` / `?` | Incremental forward / backward search — jumps as you type, wraps around the ends, case-insensitive. `Enter` confirms and stays at the match; `Esc` (or backspacing past an empty query) reverts to where you started. A query matching nothing shows "No match for ..." on the command line, and the cursor stays put; a query with at least one match instead shows `[index/total]` — the current match's 1-based position (top-to-bottom, regardless of search direction) among every match — right there on the command line. A match inside a folded subtree opens the folds hiding it — as does a folded entry whose body, not its title, matches — but only provisionally: once the query moves on to a match that doesn't need them (or to no match at all), they close again, so a short prefix that happens to match something unrelated doesn't leave its subtree open. `Enter` keeps the final match's folds open; `Esc` closes every fold the search opened |
| `n` / `N` | Repeat the last search forward / backward — same "No match for ..." or `[index/total]` feedback as above |
| `:noh` | Clear search-match highlighting |

Matches highlight everywhere they occur (title, tags, file names, body
text) and stay highlighted after you move on, until the next search or
`:noh`.

### Command mode (`:`)

`<Tab>` completes a partial command, listing every match (in the info
buffer's "Matches" section — see above) if it's ambiguous. `:edit`'s
file argument completes too, the same way: the `.org` files in `--dir`,
`reference/`, and the files in it — whether loaded already or only on
disk, so a file created from a shell is found too. `↑`/`↓`
recall previous commands, most recent first —
matching vim's own cmdline history: every command actually run is
recorded (whether or not it turned out valid, and without deduplicating
repeats), and `↑` past the oldest entry stops there rather than
wrapping. If you'd already started typing something before pressing
`↑`, `↓` will walk back to it once you're past the most recent entry.
History doesn't persist between sessions.

| Command | Action |
|---|---|
| `:w` / `:write` | Save every file with unsaved changes: a commit on the main branch, merged with whatever reached it since you loaded them (see Saving and git, above). A save that conflicts saves nothing and says so |
| `:w!` / `:write!` | Like `:w`, but a file that conflicts is saved as you have it, whole, discarding the other side's changes to it (see Conflicts, above) |
| `:wq` | Write, then quit — or, with more than one window open (see Windows, above), close the current window. A conflict keeps orgtd open |
| `:wq!` | Like `:wq`, but saved as `:w!` does |
| `:resolve` | Save again, and if files still conflict, open them, with conflict markers, in `merge_tool`; once they're resolved, save the result (see Conflicts, above) |
| `:recover` / `:recover drop` | Save, or discard, the unsaved edits of an orgtd that crashed (see If orgtd crashes, above) |
| `:q` / `:quit` | Quit (refuses if there are unsaved changes) — or, with more than one window open, close the current window, as vim does; that never loses an edit, since every window shows the same files |
| `:q!` / `:quit!` | Quit, discarding unsaved changes — or, with more than one window open, close the current window |
| `:qa` / `:qall` | Quit, whatever windows are open (refuses if there are unsaved changes) |
| `:qa!` / `:qall!` | Quit, discarding unsaved changes, whatever windows are open |
| `:wqa` / `:wqall` / `:wqa!` / `:wqall!` | Write, then quit whatever windows are open — like `:wq`/`:wq!` with a single window |
| `:split [file]` / `:sp [file]` | Split the current window in two, stacked, and — given a file, named as for `:edit` — open it in the new window. See Windows, above |
| `:vsplit [file]` / `:vs [file]` | Same as `:split`, but side by side |
| `:close` / `:clo` | Close the current window (refused for the last one) |
| `:only` / `:on` | Close every window but the current one |
| `:undo` / `:redo` | Same as `u` / `ctrl-r` |
| `:agenda` / `:snippets` / `:outline` / `:reference` / `:config` / `:log` / `:help` / `:calendar` / `:meeting-tags` / `:tags` | Switch views |
| `:edit <file>` / `:e <file>` | Open a file, like vim's `:e`: the cursor goes to its header row — in `:reference` for a file in `reference/`, in the outline for any other. `<file>` is relative to `--dir` (`:e notes`, `:e reference/wifi`); `.org` is added if missing, and only `--dir` itself and `reference/` are accepted, since nothing else is ever scanned. A file that already exists is just jumped to — including one created outside orgtd since startup, which is loaded first. Otherwise a new, empty file is opened — in memory only, like vim: add entries with `o`/`O` on its header row, and nothing (not even `reference/`) is written to disk until `:w` saves them. A new file left unedited is never written. If another instance saves the same file first, `:w` merges the two, which usually conflicts (`:w!` keeps yours, `:e!` takes theirs); `:e!` on a still-unsaved new file just empties it again. For the calendar and meeting-tags files, which have no header row in the views that show them, it opens `:calendar`/`:meeting-tags` instead |
| `:edit! [file]` / `:e! [file]` | Reload a file from the main branch (after committing any edit made to it outside orgtd), discarding its unsaved changes, undo history and marks — e.g. to take the other side of a conflict. `<file>` is named as for `:edit` and must already be loaded; with none, reloads the file under the cursor |
| `:capture` | Same as `gC`: append a new entry to the end of the inbox file and start typing it in insert mode |
| `:review` | Toggle the read-only `%` register — see Reviewing the inbox, below. The current view doesn't change. Also turned off by `:clear-registers` |
| `:next` / `:prev` | Only while `:review` is on: manually step to the next/previous pending (not `DONE`/`CANCELLED`) inbox item |
| `:format-links` | Find every entry with a bare URL not already an org-mode link, and reformat them all via `format_links_url_formatter` (or `url_formatter`, if that's unset — see above) in the background. Skips `calendar_file` (`calendar.org` by default) — it's rewritten wholesale by `:sync-calendar`, so formatting a bare URL there would just be redone, or lost, on the next sync. Affected entries lock — shown with a `◆` in the gutter and rendered faint/dimmed — uneditable, undeletable, and excluded from bulk operations — until their batch finishes; the rest of the app stays fully usable in the meantime |
| `:sync-calendar` / `:sync-calendar!` | Sync Google Calendar into `calendar_file` in the background — see Calendar sync, below. The bang variant discards any cached Google sign-in first, forcing the consent flow to run again |
| `:run <name> [args...]` | Save every file with unsaved changes, run the `[[commands]]` entry called `<name>` (with `args` appended) in `--dir` with the terminal handed over, and reload every file it changed — see Running your own commands, above. `<Tab>` completes `<name>`; `:run` alone lists the configured commands |
| `:delmarks <letters>` / `:delmarks!` | See Marks, above |
| `:registers` / `:reg` | Toggle showing the registers' contents in the info buffer (off by default; see Registers, above). `%` is shown while `:review` is on either way |
| `:clear-registers` | Clear the paste register (see above) — `p`/`P` have nothing to paste until the next `dd`/`<N>dd`/visual-mode `d`/`y`/`yy` |
| `:noh` / `:nohlsearch` | See Search, above |
| `:toggleclosed` | Toggle hiding stale items (any entry whose `CLOSED` stamp is older than `hide_closed_after`) in the outline view on/off — see Views, above |

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
TODO keywords default to `TODO`, `NEXT`, `WAITING`, `SOMEDAY` (active),
`DONE`, `CANCELLED` (done — stamps `CLOSED` automatically), and are
configurable, along with their picker shortcuts and colors, via
`[[keywords]]` in the config file (see Config file, above).

Marking an item with a repeating `SCHEDULED`/`DEADLINE` (a trailing
cookie like `+1w`, `++1w`, or `.+1w`) as done matches org-mode: the
keyword doesn't actually change, and the repeating timestamp(s) advance
to their next occurrence instead, with the completion recorded via a
`:LAST_REPEAT:` property rather than `CLOSED`. `+1w` advances exactly one
interval from the old date (however overdue that leaves it); `++1w`
advances however many intervals it takes to land on or after today;
`.+1w` advances one interval from *today*, ignoring the old date
entirely.

A closing note (see `r`/`R`, above) is written the way org-mode's
`org-log-done` set to `note` writes one: at the top of the entry's body,
newest first, where it can be edited like any other body text. Reopening
the item leaves it there as history.

```org
* DONE Ship the report
  CLOSED: [2026-10-31 Sat 14:05]
  - CLOSING NOTE [2026-10-31 Sat 14:05] \\
    Sent to the team, follow-up next week
```

A repeating item records its note under a state-change line instead,
since it never really closes, e.g. `- State "DONE"       from "TODO"
[2026-10-31 Sat 14:05] \\`. With no note, nothing is added to the body
either way.

Saving leaves every entry you didn't change byte-for-byte as it was,
along with the file preamble. An entry you did change has its headline,
planning, and property lines regenerated from what orgtd parsed (body
text is always kept as is). So on that entry alone, incidental
formatting (alignment, spacing) can shift, a property drawer placed
after body text moves up to the top, and anything orgtd doesn't model is
dropped: text after a planning timestamp, the second half of a
`<a>--<b>` range, and all but the last of a repeated property.

## Calendar sync (`:sync-calendar`)

`:sync-calendar` syncs Google Calendar events into an org file —
`calendar_file` (see above; `calendar.org` by default), so it's
automatically excluded from the outline view and shown instead, grouped
by day, in the `:calendar` view. It never writes anywhere except that
one file, saved like any `:w` (a commit, unless git ignores the file),
except that the synced version always replaces the old one whole. Run it by hand whenever you want fresh calendar data; orgtd
also syncs on its own, while it's running, whenever that file is more
than `gcalsync.auto_sync_hours` (default 24) old — checked at startup
(against the file's modification time on disk, so restarting orgtd
doesn't by itself trigger a sync) and every ten minutes after. An
automatic sync never opens a browser: until a manual `:sync-calendar`
has signed in to Google once, it's skipped. Its outcome, success or
failure, goes to `:log` rather than the status line, and after a
failure it waits an hour before trying again. Set `auto_sync_hours` to
a negative number to turn automatic syncing off.
`:sync-calendar!` (with a bang, same convention as `:q!`) discards any
cached Google sign-in first, forcing the consent flow to run again —
useful if the cached token stops working (e.g. access was revoked).

Each synced event becomes a plain headline (no TODO keyword) with a
timestamp, location, description, and a link back to the event, e.g.:

```org
* Q3 planning sync                                    :recurring:@alice:@bob:
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
(see above) to find and order current/upcoming meetings, and by the
info buffer's "Meeting" section (see above) to show a linked meeting's
start time; `GCAL_HTML_LINK` is a machine-readable copy of the link at
the bottom, which `gM` copies onto an attached entry's own
`GCAL_RECURRING_EVENT_LINKS` (recurring) or `GCAL_EVENT_LINKS` (one-off)
property so the info buffer can keep showing this event's title and URL
long after this cached headline is gone (its start time, unlike the
title/URL, isn't snapshotted this way, so it stops showing once this
cached headline does); the timestamp and link in the body are the human-readable ones,
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

Each attendee is potentially tagged onto the headline as `@username` —
the portion of their email address before the `@`, e.g.
`john@example.com` becomes `@john` (any character not otherwise valid in
an org tag, like the `.` in `first.last@...`, is replaced with `_` so
the tag still round-trips cleanly). Confirmed (accepted) attendees are
prioritized: an invite of 7 or fewer people (anyone who's declined
doesn't count) is small enough that RSVP status doesn't matter, and
everyone still tentative or pending is tagged right alongside anyone
who's confirmed. Past that size, only confirmed attendees are
considered — a large invite list with only a few acceptances still gets
those tagged — and if even the confirmed attendees number more than 7,
tagging is skipped entirely (no attendee tags at all), rather than
showing a partial list. `gcalsync.attendee_tag_domains`
(below) additionally restricts this to attendees whose email is on one
of a set of domains — useful for tagging only coworkers, not every
external guest, vendor, or room/resource calendar an event happens to
list as an attendee. `gcalsync.attendee_ignore_patterns` (below) instead
drops matching attendees from consideration entirely, before that
domain restriction is even checked — useful for excluding the synthetic
`c_...@...`-style attendees Google Calendar attaches to some events on
its own. These are what Tag-based meeting links (see
Keybindings, below) matches against — tag a task `@alice` and it's
automatically linked to every meeting she's tagged as an attendee of.

### Setup

1. In [Google Cloud Console](https://console.cloud.google.com/), create
   a project (or use an existing one), enable the **Google Calendar
   API**, and create an OAuth client ID of type **Desktop app**. Every
   user needs their own client — a shared one baked into the binary
   couldn't keep its secret secret.
2. Add the client ID/secret, and anything else you want to override, to
   the config file (`~/.config/orgtd/config.toml` by default — see
   Config file, above) under a `[gcalsync]` section:

   ```toml
   org_dir = "~/org"

   [gcalsync]
   oauth_client_id = "...apps.googleusercontent.com"
   oauth_client_secret = "..."
   calendar_ids = ["primary"]
   sync_past_days = 1
   sync_future_days = 14
   attendee_tag_domains = ["example.com"]
   attendee_ignore_patterns = ["c_*@*"]
   auto_sync_hours = 24
   ```

3. Run `:sync-calendar`. The first run opens your system browser to
   Google's consent screen (scope: read-only calendar access); the
   resulting refresh token is cached in your OS keychain (macOS
   Keychain / Linux Secret Service / Windows Credential Manager), so
   later runs don't need a browser at all. If the browser can't be
   opened automatically (e.g. orgtd is running over SSH), the consent
   URL is logged instead — see it with `:log`. `:sync-calendar!`
   discards the cached token and runs the consent flow again.

| Config key | Default | Meaning |
|---|---|---|
| `gcalsync.oauth_client_id` / `gcalsync.oauth_client_secret` | *(required)* | Your Google OAuth2 installed-app client |
| `gcalsync.calendar_ids` | `["primary"]` | Google Calendar IDs to sync |
| `gcalsync.sync_past_days` / `gcalsync.sync_future_days` | `1` / `14` | Sync window around now |
| `gcalsync.attendee_tag_domains` | *(none — every confirmed attendee tagged)* | Restricts `@username` attendee tags (see above) to attendees whose email domain matches one of these, e.g. `["example.com"]` to tag only coworkers and drop external guests/vendors/room calendars. Matched case-insensitively; a leading `@` is optional (`"example.com"` and `"@example.com"` behave the same) |
| `gcalsync.attendee_ignore_patterns` | *(none — no exclusions)* | Excludes any attendee whose email matches one of these glob patterns from consideration entirely, before `attendee_tag_domains` is even checked, e.g. `["c_*@*"]` to drop the synthetic `c_...@...` attendees Google Calendar attaches to some events. Uses Go's `filepath.Match` syntax (`*` matches any run of characters, `?` a single one) and is matched case-insensitively against the whole address |
| `gcalsync.auto_sync_hours` | `24` | Sync automatically, while orgtd is running, whenever `calendar_file` is more than this many hours old (see above). Never opens a browser — skipped until you've signed in with a manual `:sync-calendar`. Negative turns it off |

There are no command-line flags for these — `:sync-calendar` only ever
runs interactively from inside orgtd, so they're config-file only.

## iOS companion

`ios/` holds an iPhone app for the same org directory. It shows an
outline and agenda, adds entries anywhere in the outline (the inbox
included), captures into the inbox from the share sheet, and edits
entries (TODO state, title, notes, tags, dates).

- **Hosting:** the org directory must be a git repository on a server
  the phone can reach over SSH. Your own machine is fine; nothing needs
  to be hosted by a third party.
- **What the phone writes:** new entries appended to `inbox_file`, new
  entries inserted first or last under an entry (or a file's top level)
  in the outline, entries moved a level deeper or shallower (as `>>`/`<<`
  do), entries moved up or down among their siblings (top-level ones
  into another file, too), entries deleted with their subtrees (as `dd` does), and edits to
  existing entries (keyword changes with `CLOSED` and repeaters
  handled as `r` does; title, notes, tags and dates), touching only the
  changed lines of that entry. Everything is
  committed to a `mobile` branch, rebuilt on every Sync as your main
  branch plus the phone's changes. A phone change is never dropped: if
  you've changed its entry on your computer since, `mobile` stays on
  the main branch the phone last applied its changes to, the phone says
  the change is waiting for your computer, and the merge happens there,
  where you resolve any conflict. When the phone pushes into
  the org directory itself, a running orgtd fast-forwards or merges the
  main branch to `mobile` within a couple of seconds (see Running
  several instances, above). Otherwise, merge `mobile` into your main
  branch yourself (a normal merge or fast-forward, never a squash or
  rebase), and running orgtd instances pick the merge up like any other
  change to the main branch.
- **Settings:** it reads the keywords, inbox name, agenda window and
  colors from `.orgtd.toml` (above).

See [ios/README.md](ios/README.md) for building and setup.

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
