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
| `--calendar-file` | `calendar_file` | `calendar.org` | Base name of the file (e.g. the one `:sync-calendar` writes) excluded from the outline view and shown instead, grouped by day, in the `:calendar` view |
| `--meeting-tags-file` | `meeting_tags_file` | `meeting-tags.org` | Base name of the file holding durable meeting tags (see Tag-based meeting links, below) — excluded from the outline view and shown instead, as an editable outline of its own, in the `:meeting-tags` view |
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
meeting_tags_file = "meeting-tags.org"
hide_done_after_hours = 24
debug = false

# Customizes the character and color of each marker the outline draws in
# its gutter column, to the left of every entry (see Keybindings, below,
# for what each one means: "+" for unsaved changes, a mark's own letter,
# "●" for the :clarify target, "◆" for a :format-links lock, "▣" for a
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
clarify_icon  = "●"    # the :clarify view's pinned inbox item
clarify_color = "#ff87ff"
lock_icon     = "◆"    # locked by an in-flight :format-links batch
lock_color    = "#ffaf00"
meeting_icon  = "▣"    # linked to a calendar meeting, via "gM" or a shared tag
meeting_color = "#00afff"

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
panel_bg                = "#303030"  # the info buffer (clarify/marks/register, links, meeting detail, completions, pickers)
status_bar_fg           = "#000000"  # the one-line status bar at the bottom of the screen
status_bar_bg           = "#9e9e9e"
cursor_row_bg           = "#204060"  # the row under the cursor
visual_selection_bg     = "#102030"  # the rest of a visual-mode selection, besides the cursor's own row
search_highlight_bg     = "#3a4a3a"  # every match of the active search term (vim's 'hlsearch')
```

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
  underneath them, all foldable. A `DONE`/`CANCELLED` headline (and its
  whole subtree) whose `CLOSED` timestamp is older than
  `hide_done_after_hours` (default 24) is hidden — `:toggledone` shows
  everything again, and toggles back.
- **Calendar** (`:calendar`) — every event in `calendar_file` (default
  `calendar.org`; see Calendar sync, below), grouped under one flush-left
  header per calendar day, both the days and the events within each day
  in chronological order. The cursor starts on whichever meeting is
  currently in progress, or the most recently started past meeting if
  none is, and that row centered on screen, so you land right where the
  day already is rather than at the top — falling back to the very first
  row if nothing has started yet (every synced event is still upcoming).
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
- **Meeting tags** (`:meeting-tags`) — every entry in `meeting_tags_file`
  (default `meeting-tags.org`; see Tag-based meeting links, below), an
  ordinary foldable/editable outline (same `i`, `dd`, `r`, marks, etc. as
  any other file) rather than something tool-managed like `calendar_file`
  — nothing ever regenerates it, so it's exactly as durable as any other
  org file, and gets committed alongside them by `:diff`/`:commit`. A
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
- **Clarify** (`:clarify`) — pins the inbox's first non-`DONE`/`CANCELLED`
  top-level headline in the info buffer at the bottom of the screen (see
  below), alongside its `CREATED` property (so you can see how long it's
  been sitting there) and any
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
  currently open in the outline. Refuses outright if any of them has
  unsaved changes — `git diff` only ever sees what's actually on disk,
  and nothing is written until `:w`, so showing a diff anyway would
  silently leave out whatever's still only in memory. Also refuses
  outright unless the org directory is itself the *root* of its git
  repository (same requirement, and the same reason, as `:commit`
  below) — since a diff can't offer to `git add` an untracked file it
  isn't safe to touch, showing one at all from a nested workspace (e.g.
  this project's own `testdata/orgdir`) would just be misleading about
  what `:commit` could actually do with it. When it does run: if any
  open file isn't tracked by git yet, asks first (`[y/N]`) whether to
  `git add` it — declining just leaves it out of the diff. Shows a
  placeholder if there are no changes or no files open. Also recorded in
  `:log`, like any other external command. `:outline` returns to the
  outline. `:commit` (only available here — see below) commits and
  pushes what `:diff` is showing.
- **Help** (`:help`) — this README, rendered as terminal-styled markdown
  (via [glamour](https://github.com/charmbracelet/glamour) — headings,
  tables, code blocks and all), embedded into the binary at build time
  (see `go:embed`) so it's available even if README.md isn't sitting
  next to wherever the binary was installed. Re-wraps automatically if
  the terminal is resized while it's open. `:outline` returns to the
  outline.

The bottom of the screen has up to three parts. The status line (current
view/directory and item position only) is always exactly one line,
always visible regardless of mode; the command line below it — where
`:`/`/`/`?` input, prompts (deadline, the status picker), the
visual-mode banner, and messages all appear, blank when there's nothing
to show — is always exactly one more. Neither ever
replaces the other, mirroring vim's own statusline-above-command-line
layout.

Above both, an info buffer holds whatever might need more than one
line, grouped into labeled sections — collapsed entirely (zero height)
when none of them apply, so it never costs a permanent row on screen:

- **Clarifying** — in `:clarify` view (see below), the inbox item
  currently pinned for clarification, alongside its `CREATED` property
  and any `SCHEDULED`/`DEADLINE` it already has (or a message when the
  inbox is empty) — kept a fixed two lines so the layout doesn't jump
  around as the inbox empties out.
- **Active marks** — every active mark (see Marks, below), sorted by
  letter, one row each — visible in every view until cleared.
- **Register** — whatever `dd`/`<N>dd`/visual-mode `d`/`y`/`yy` last put
  in the paste register (see Marks, below), one row per entry.
- **Links** — every org-mode link literally in the current entry's
  title.
- **Meeting** — one line per calendar meeting the current entry is
  linked to (see below for how a link is established), each showing the
  meeting's name, start time (when still resolvable), and link.
- **Tags** — while `gt` (see Keybindings, below) is prompting for a tag
  and more than one existing tag matches what's typed so far, the
  matches themselves, one per line.
- **Matches** — the same, for command-mode (`:`) `Tab` completion (see
  Command mode, below) when more than one command name matches.
- **Status** — while the `r`/`R` status picker (see Keybindings, below)
  is open, every selectable state, one per line, with its bracketed
  shortcut and the currently highlighted one in reverse video.
- **Attach meeting** — while the `gM` picker (see Keybindings, below) is
  open, every meeting matching what's been typed so far, in chronological
  order, one per line — resolved date/time first (so times line up in a
  column and are easy to compare down the list), then title, then
  `(attached)` for one already attached to the target entry — with the
  currently highlighted one in reverse video.

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
| `Enter` | In agenda view, jump to that item's real place in the outline. In calendar view, on an item attached to a meeting via `gM` (see below), same thing — a no-op on the meeting's own row, since `calendar_file` isn't part of the outline at all |
| `ctrl-o` / `gi` | Jump back / forward through the jump list — vim's own `ctrl-o`/`ctrl-i`, tracking where you were before a "large" move: `gg`/`G`, `{`/`}`, a confirmed search (`/`/`?`, not `n`/`N` repeats), jumping to a mark (`'<letter>`) or to the clarify target (`gc`), and switching views entirely (`:agenda`, `Enter` from it, `:calendar`, `:clarify`, `:outline`, ...) — never for `j`/`k` or fold/edit commands, or the list would be useless clutter. Bound to `gi` rather than `ctrl-i`: in a plain terminal `ctrl-i` and `Tab` are the same byte, so there's no way to bind it separately from the fold-toggle key above. A no-op at either end of the list |

### Editing

| Key | Action |
|---|---|
| `i` / `I` | Edit the current entry's own text (title, planning line, properties, and body — not its children, which are left untouched) in `$EDITOR`. The buffer has no leading bullet (`*`) to edit around, and the whole entry — not just the title — is dedented by one level to match, so properties/planning/body lines that would otherwise still be indented under the now-absent bullet start flush left too; it's just the entry's text, plain, starting on line 1, re-indented automatically once you save. A vim-family `$EDITOR` (vi/vim/nvim/gvim/mvim) starts right there already in insert mode. Below it, git-commit-style, is a comment block sketching the entry's place in the outline (its file, parent, siblings, and its own children, each with their real bullets) with a `[THIS ENTRY HERE]` marker standing in for the entry itself — for orientation only; editing it has no effect. On a file's own header row, edits the whole file directly instead (after a confirmation, since this discards undo history and marks for that file) |
| `A` | Same as `i`, but the cursor lands at the end of the entry's first line instead (vim's own "append" position) |
| `o` / `O` | Insert a new entry after / before the current one (or at the end/start of a file, from a file header row). The template opened in `$EDITOR` uses the same bullet-free buffer as `i` (see above) — blank, starting on line 1, cursor already there — prefilled with a `CREATED` property set to now (edit or delete it like anything else before saving), with the same comment block below it showing where the new entry will land: its file, parent, and siblings (it has no children yet, being new). For a vim-family `$EDITOR`, typing the new title can start immediately. In `:calendar` view, on a row associated with a meeting — the event's own row/body, or an item already linked to it (see below) — `o`/`O` behave identically instead: both append the new entry to the end of the inbox (same target as `gC`) and attach it to that meeting outright, the same properties `gM` would set but chosen automatically, with no picker step, since the meeting is already unambiguous from the cursor's row. There's no "before"/"after" left to distinguish once the target is always the end of one shared file rather than a position relative to the cursor; the entry's place among any others linked to the same meeting (see `:calendar`, below) is decided by `CREATED` instead, so repeated `o`/`O` presses still show up in the order they were actually captured regardless of which file each one is later filed into |
| `dd` | Delete the current entry and its subtree (undoable; also fills the paste register). The cursor stays at the same screen row, landing on whatever now occupies it (or the new last row, if it was the last one) — matching vim's own `dd` |
| `<N>dd` | Delete the current entry and the next N-1 entries and their subtrees, as one undo step (e.g. `3dd` deletes 3 entries). A count of 1 (or none) is exactly plain `dd`. A higher count fills the register with all of the deleted entries (top-to-bottom order preserved), pasted back together as a group by a single `p`/`P` |
| `yy` | Yank the current entry and its subtree into the paste register, without deleting it |
| `p` / `P` | Paste the register's contents after / before the current entry, re-indented to fit. If the register holds more than one entry (from `<N>dd` or a visual-mode `d`/`y`), all of them are pasted together, in the same order they were deleted/yanked in |
| `>>` / `<<` | Demote / promote the current entry (re-parents it, not just cosmetic indentation) |
| `r` / `R` | Open a picker to set the TODO state directly (type to filter, or use a candidate's bracketed shortcut) |
| `<N>r` / `<N>R` | Open the same picker, but apply the chosen state to the current entry and the next N-1 (each independently, nesting included), as one undo step (e.g. `2R` sets the current and next entry) |
| `gd` | Set the current entry's deadline — accepts an exact date, `3d`/`2w`/`1m`/`1y` shorthand, or a fuzzy phrase like "next tuesday" |
| `gC` | Capture: append a new entry to the end of the inbox file and open it in `$EDITOR`, regardless of the current cursor position or view (same as `:capture`). Once the editor session commits, switches to outline view with the cursor on the newly captured entry (unless already in outline or clarify view, whose listing already includes it), ready for further edits right away. Deliberately doesn't guess at a calendar meeting to attach, even one in progress at the moment of capture — see `gM` below, the interactive way to do that |
| `gM` | Open a picker (type to filter by title or attendee tag — e.g. "alice" matches a meeting tagged `@alice`, same tags `:sync-calendar` stamps on for Tag-based meeting links, below — ↑/↓ to browse, Enter to pick, Esc to cancel) over every distinct meeting `:sync-calendar` currently has synced at least one instance of — a recurring series (deduped by series) or a one-off event alike — and toggle it on or off the current entry's `GCAL_RECURRING_EVENT_IDS`/`GCAL_RECURRING_EVENT_LINKS` (recurring) or `GCAL_EVENT_IDS`/`GCAL_EVENT_LINKS` (one-off) properties (the `..._LINKS` one is a title/link snapshot, used by the info buffer's "Meeting" section — see above — to keep showing the meeting's name and link even after it drops off the calendar entirely; see the agenda's Meetings section, also above, for what the IDs are for). Picking a meeting already attached detaches it instead of adding a duplicate. A no-op (with a status message) if `:sync-calendar` hasn't synced anything at all — there's nothing to offer. A linked entry (attached via `gM`, or tag-matched — see Tag-based meeting links, above) shows a `▣` in its own gutter column (alongside any mark, lock, or dirty marker), so whether it's linked to a meeting is visible at a glance, in every view, without opening it |
| `gX` | `gC` immediately followed by `gM`: capture as usual, and once the editor session commits, the meeting picker opens automatically on the just-captured entry — for capturing something during a meeting and attaching that meeting in one motion, without a separate `gM` bracketing the (possibly slow) editor round-trip. Cancelling the capture (empty or blank result, or the editor failing to run) never opens the picker; if nothing's synced yet, the capture still commits, just without the picker (same no-op message as a bare `gM`) |
| `gt` | Prompt for a tag and toggle it on the current entry: typing one already on the entry removes it, anything else is added. `Tab` completes against every tag already used anywhere in the workspace (extending to the longest common prefix and listing the matches, same as command-mode `:<Tab>`); an empty prompt's `Tab` lists all of them. Enter with nothing typed, or `Esc`, cancels without changes. Tags can also be added/removed by hand in `i`/edit mode (a trailing `:tag1:tag2:` on the title line) — `gt` is just the faster path for one at a time. A tag matching one on a synced calendar event automatically links the two — see Tag-based meeting links, above. On a `:calendar` entry's own row, `gt` instead records the tag in `meeting_tags_file` so it survives `:sync-calendar` (see Tag-based meeting links, above); `"recurring"` is refused there as a reserved name |
| `u` / `ctrl-r` | Undo / redo (single global stack for the session) |

### Visual selection

| Key | Action |
|---|---|
| `V` | Enter visual line selection at the current entry. Navigation keys (`j`/`k`, `gg`/`G`, `{`/`}`, `l`/`h`, `^`/`$`, `ctrl-d`/`ctrl-u`, `Page Down`/`Page Up`, `ctrl-e`/`ctrl-y`) extend the selection instead of just moving; `V` again or `Esc` cancels it |
| `d` | (in visual mode) Delete every selected entry and its subtree. A selected entry whose ancestor is also selected isn't deleted separately — deleting the ancestor already removes it. One undo step per file touched (almost always just one). Fills the paste register with everything deleted (top-to-bottom order preserved), so `p`/`P` pastes the whole selection back as a group |
| `y` | (in visual mode) Yank every selected entry and its subtree into the paste register, without deleting them — the visual-mode equivalent of `yy`. Same ancestor-covers-descendant rule as `d`. Leaves normal mode afterward, same as `d`/`r`/`R` |
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

Every active mark stays pinned in the info buffer at the bottom of the
screen (see Views, above), in every view, until cleared or moved
elsewhere.

Whatever `dd`/`<N>dd`/visual-mode `d`/`y`/`yy` last put in the paste
register stays pinned in the info buffer too, under its own "Register:"
label, right alongside marks — so it's obvious what `p`/`P` will paste
next even for `yy`/visual-mode `y`, which otherwise leave the screen
looking unchanged. Capped at 5 entries shown at once (a big `<N>dd` or
visual-mode delete/yank collapses the rest into a trailing "...and N
more" line) so a large register can't push the actual listing
off-screen. `:clear-registers`
empties it (and its pinned display) without needing another `dd`/`yy`.

### Search

| Key | Action |
|---|---|
| `/` / `?` | Incremental forward / backward search — jumps as you type, wraps around the ends, case-insensitive. `Enter` confirms and stays at the match; `Esc` (or backspacing past an empty query) reverts to where you started. A query matching nothing shows "No match for ..." on the command line, and the cursor stays put |
| `n` / `N` | Repeat the last search forward / backward — "No match for ..." the same way if it finds nothing |
| `:noh` | Clear search-match highlighting |

Matches highlight everywhere they occur (title, tags, file names, body
text) and stay highlighted after you move on, until the next search or
`:noh`.

### Command mode (`:`)

`<Tab>` completes a partial command, listing every match (in the info
buffer's "Matches" section — see above) if it's ambiguous. `↑`/`↓`
recall previous commands, most recent first —
matching vim's own cmdline history: every command actually run is
recorded (whether or not it turned out valid, and without deduplicating
repeats), and `↑` past the oldest entry stops there rather than
wrapping. If you'd already started typing something before pressing
`↑`, `↓` will walk back to it once you're past the most recent entry.
History doesn't persist between sessions.

| Command | Action |
|---|---|
| `:w` / `:write` | Write every file with unsaved changes. Refuses while a `:commit`'s `git commit`/`git push` is still running in the background (see `:commit` below) |
| `:wq` | Write, then quit. Same `git`-running refusal as `:w` above |
| `:q` / `:quit` | Quit (refuses if there are unsaved changes) |
| `:q!` / `:quit!` | Quit, discarding unsaved changes |
| `:undo` / `:redo` | Same as `u` / `ctrl-r` |
| `:agenda` / `:clarify` / `:outline` / `:config` / `:log` / `:diff` / `:help` / `:calendar` / `:meeting-tags` | Switch views |
| `:capture` | Same as `gC`: append a new entry to the end of the inbox file and open it in `$EDITOR` |
| `:next` / `:prev` | Clarify view only: manually step to the next/previous pending (not `DONE`/`CANCELLED`) inbox item |
| `:format-links` | Find every entry with a bare URL not already an org-mode link, and reformat them all via `format_links_url_formatter` (or `url_formatter`, if that's unset — see above) in the background. Skips `calendar_file` (`calendar.org` by default) — it's rewritten wholesale by `:sync-calendar`, so formatting a bare URL there would just be redone, or lost, on the next sync. Affected entries lock — shown with a `◆` in the gutter and rendered faint/dimmed — uneditable, undeletable, and excluded from bulk operations — until their batch finishes; the rest of the app stays fully usable in the meantime |
| `:sync-calendar` / `:sync-calendar!` | Sync Google Calendar into `calendar_file` in the background — see Calendar sync, below. The bang variant discards any cached Google sign-in first, forcing the consent flow to run again |
| `:commit` | Diff view only (see Views, above) — refuses if any open file has unsaved changes (same as `:diff`; diff view's own content isn't re-diffed on every keystroke, so this is checked again here even if `:diff` already refused it once on entry), and refuses unless the org directory is itself the *root* of its git repository (not merely somewhere inside one, e.g. this project's own `testdata/orgdir`), since `git push` isn't scoped to particular files — it pushes the whole current branch, which for a nested workspace would mean pushing an unrelated repository's real history. Otherwise asks about any untracked file first (same `[y/N]` prompt as `:diff`; declining leaves it untracked, which makes the commit that follows fail outright, since `git commit`'s pathspec rejects a file that's never been `git add`ed at all), then runs `git commit` with a stock message ("orgtd commit"), scoped to the same files `:diff` shows, followed by `git push` — no prompt for a commit message. Refuses outside diff view too, and refuses a second `:commit` while one is already running. Unlike `:diff`, `git commit` and `git push` run in the background (same as `:format-links`/`:sync-calendar`), showing a status message while they're in flight so the rest of the app stays usable, including however long `git push` takes to reach the remote; `:w`/`:wq` refuse to write in the meantime, since the commit already captured which files and content it's committing when it started. A commit that fails for a real reason never attempts the push, but `git commit` finding nothing to commit (e.g. `:commit` run twice in a row, or after committing by hand outside orgtd) isn't treated as a failure — the push still runs, since there could be earlier local commits not yet on the remote. A failed push still leaves the commit in place locally either way. The diff data refreshes once the background run finishes either way (so it's never left showing a stale pre-commit diff), but only pulls diff view back up if you're still on it — if you've since switched to another view, finishing the commit doesn't yank you back to diff view |
| `:delmarks <letters>` / `:delmarks!` | See Marks, above |
| `:clear-registers` | Clear the paste register (see above) — `p`/`P` have nothing to paste until the next `dd`/`<N>dd`/visual-mode `d`/`y`/`yy` |
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

## Calendar sync (`:sync-calendar`)

`:sync-calendar` syncs Google Calendar events into an org file —
`calendar_file` (see above; `calendar.org` by default), so it's
automatically excluded from the outline view and shown instead, grouped
by day, in the `:calendar` view. It's triggered by hand, whenever you
want fresh calendar data — there's nothing running in the background or
on a schedule, and it never writes anywhere except that one file.
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

There are no command-line flags for these — `:sync-calendar` only ever
runs interactively from inside orgtd, so they're config-file only.

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
