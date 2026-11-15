# orgtd for iOS

An iPhone companion to orgtd: capture into the inbox from the share
sheet, read the outline and agenda, add entries anywhere in the outline
(the inbox included), and edit entries (their TODO state, title, notes,
tags and dates). Share-sheet captures are appended to `inbox_file`, new
outline entries are inserted where you put them, and edits
rewrite just the changed entry's own lines. All of these are committed
to a `mobile` branch, which you merge on your computer.
The design is in [DESIGN.md](DESIGN.md).

## Requirements

- Xcode 16 or later (the project uses synchronized folders, so new Swift
  files need no project edits) and Go (the version in `../go.mod`).
- An org directory that is a git repository, with `inbox_file`
  (default `inbox.org`) at its root, on a git server the phone can reach
  over SSH. See "Hosting the repository" below; GitHub works too, but
  isn't needed.

## Build

```sh
./scripts/build-mobilecore.sh   # from the repo root; builds ios/Frameworks/Mobilecore.xcframework
cp ios/Config/Signing.local.xcconfig.example ios/Config/Signing.local.xcconfig
# edit it: DEVELOPMENT_TEAM and a unique BUNDLE_ID_PREFIX
open ios/orgtd.xcodeproj
```

Rebuild the framework whenever anything under `mobilecore/` or the
`internal/` packages it uses changes. gomobile and gobind are pinned as
tools in `go.mod`, so the script needs nothing else installed.

Run the **orgtd** scheme on your iPhone, or, from the command line:

```sh
./scripts/install-ios.sh            # the one paired iPhone, or pass its name
```

That rebuilds the Go framework if any Go source changed, builds the app
with your signing settings, installs it over the network or USB, and
launches it. The phone needs to be unlocked. The first install can't
launch until you trust your certificate on the phone (Settings → General
→ VPN & Device Management); the script says so when that happens.

With a free Apple ID
("Personal Team"), the app stops launching after **7 days**: run it
from Xcode again each week. Running over the top of the installed app
keeps its data (the clone, the pending captures, and the key).

### App Group (share extension)

The share extension hands captures to the app through an App Group
container, `group.<BUNDLE_ID_PREFIX>.orgtd`, set in
`Config/*.entitlements`. If your signing team can't use App Groups,
Xcode's signing step fails for that capability. Remove the
`com.apple.security.application-groups` entries from both entitlements
files. The app still works and keeps its files in its own container,
but the share extension can't reach it. Settings → Share extension shows
which case you're in. (This is risk R1 in DESIGN.md. It has not been
checked on a free account.)

## Hosting the repository

Any machine you control that runs an SSH server and has git installed
will do: a home server or NAS, a small VPS, or your Mac (System
Settings → General → Sharing → Remote Login). A Mac only works as a
server while it's awake. Make a bare repository there and push your org
directory to it:

```sh
# on the server
git init --bare ~/git/org.git
# on your computer, in the org directory
git remote add origin you@server:git/org.git
git push -u origin main
```

To reach it from anywhere without opening it to the internet, put the
server and the phone on a private network, e.g.
[Tailscale](https://tailscale.com) (install it on both; the phone then
reaches the server by its Tailscale name, like `server.tailnet.ts.net`)
or WireGuard. The app connects whenever you fetch or sync, so the phone
only needs that network up at those moments.

## Setup on the phone

1. **Generate SSH key.** The private key stays in the Keychain.
2. **Authorize it on the server.** Tap *Copy authorized_keys line
   (restricted)* and append the line to `~/.ssh/authorized_keys` of the
   account that owns the repository. The line looks like

   ```
   command="git-shell -c \"$SSH_ORIGINAL_COMMAND\"",restrict ssh-ed25519 AAAA... orgtd-ios
   ```

   so the key can only run git (`git-shell`) and nothing else: no shell
   and no forwarding. A dedicated account just for the repository is
   tidier still.
3. Enter the repository's SSH URL (`you@server:git/org.git`, or
   `ssh://you@server:2222/home/you/git/org.git` for a non-standard port)
   and the name and email to use for capture commits.
4. **Check server.** The app connects and shows the server's host key
   fingerprint. Compare it with the server's own:

   ```sh
   ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub   # on the server
   ```

   (on a Mac: the same file under `/etc/ssh/`). Only if they match, tap
   **Trust this server and clone**. From then on, the app refuses to
   talk to a server that presents any other key. If you reinstall the
   server or change its keys, Settings → Remove clone, then set it up
   again.

## On the phone's storage

Everything the app stores (the clone, pending captures, sync state)
lives in one directory that uses iOS's strictest file protection, so it
is encrypted and unreadable while the phone is locked. That directory is
also excluded from iCloud and computer backups. The clone can always be
fetched again, but **captures you haven't synced yet exist only on the
phone**, so sync before you wipe or replace it. The SSH key is in the
Keychain, marked this-device-only, so it isn't backed up or migrated
either.

## Day to day

- **Capturing:** the round **+** button in the lower right corner of
  every tab opens a capture form (the fields below) from wherever you
  are. Saving adds the entry at the end of `inbox_file`, exactly as
  inserting it there from the Outline would.
- **Arranging:** in Outline, long-press any entry to start arranging,
  and tap **Done** when you're finished. While arranging, a tap on an
  entry doesn't open it, and:
  - **Drag** an entry up or down to move it, with everything under it,
    among its siblings. A drop outside its parent's list puts it back.
    To move it under another parent, drag it next to that parent, then
    indent it.
  - A **top-level** entry can also be dragged into another file, to its
    top level: among that file's entries, or onto a collapsed file's
    name, which puts it at the end of the file. This is how you file an
    inbox item into a project file (or a reference file).
  - **Swipe** an entry right or left to indent or outdent it (below).
  - Every list showing, top level and expanded children alike, starts
    and ends with a **New entry** row, for adding an entry there
    (below). To add an entry's first child, add a sibling below it and
    indent that.
  - The entry shows a clock until it's merged, and a move is dropped
    (and you're told) if the entry, or the sibling you dropped it next
    to, changed on your computer before you merged.
- **Adding entries:** while arranging (above), tap a **New entry** row
  at the start or end of a list. A file with no entries showing always
  has one. A form opens right where the entry will
  go, to fill in: an optional
  keyword (your configured, not-done keywords; it starts with the one
  you used last), title, notes, tags (autocompleted from your files),
  and SCHEDULED/DEADLINE dates. You can pick a date, or type one the
  way desktop `gd` accepts (`3d`, `2w`, `next tue`).
  - Saving adds the entry there, with a CREATED stamp as desktop
    `o`/`O` gives it. It commits locally and needs no network.
  - The new entry shows a clock until it's merged, and you can edit,
    move or delete it meanwhile.
  - Like an edit, an addition is dropped (and you're told) if the entry
    you inserted it next to or under changed on your computer before
    you merged.
  - Reference files take new entries too, without a keyword.
- **Indenting and outdenting:** while arranging, swipe an entry right to
  move it one level deeper, as desktop `>>` does: it becomes the last
  child of the entry above it. Swipe left to move it one level
  shallower, as `<<` does: it becomes the next sibling of its parent,
  after the parent's other children. Its subtree moves with it.
  - Only the stars of the moved headlines change (and, for an outdent,
    where the subtree sits); other lines keep their indentation.
  - The entry shows a clock until it's merged, and the move is dropped
    (and you're told) if the entry, or for an indent the entry above
    it, changed on your computer before you merged.
- **Deleting an entry:** open its edit form (above), tap the trash can
  beside **Cancel**, then confirm. Like desktop `dd`, this deletes everything under it too; the
  confirmation says how many entries that is.
  - The entry disappears at once, and is removed from the file when you
    merge.
  - The delete is dropped (and you're told) if the entry, or anything
    under it, changed on your computer before you merged, so the phone
    never deletes something you edited there and haven't seen.
- **Share sheet:** share a link or text to orgtd to capture it into the
  inbox with the same fields. Until it's merged it shows a clock;
  editing it edits the capture, and deleting it takes it out of the
  phone's queue for good.
- The app fetches on launch, when it returns to the foreground, and on
  pull-to-refresh. Fetching never pushes.
- **Away from the server** (no network, or out of range of a server on
  your home network), nothing is lost and nothing hammers the network:
  - Each attempt gives up after 8 seconds without an answer. A server
    that answers and then goes silent is cut off after 45 seconds of
    silence, and a fetch or push is capped at 2 minutes overall.
  - Automatic fetches on returning to the app happen at most once a
    minute. After the server couldn't be reached, they wait 1, 5, 15,
    then 30 minutes between tries. Pull-to-refresh and Sync always try
    right away.
  - There are no retries, timers or background activity. The app only
    touches the network while it's open.
  - The Sync button shows a crossed-out Wi-Fi symbol. Captures,
    including ones from the share sheet, still show up and wait for the
    next sync that gets through.
- **Sync** (↻) pushes. `mobile` is rebuilt as your main branch plus
  every capture that isn't merged yet, then force-pushed with a lease.
  If something other than this phone moved `origin/mobile`, the push is
  refused. The Sync button shows a spinner while it works, and an
  exclamation mark if it fails; Settings (the gear) has the details,
  including when the app last fetched and pushed.
- **Files** are the top level of Outline: each file's name has an
  arrow that expands or collapses its entries (tapping the name does
  too), with reference files after the others. Files start expanded;
  **Collapse all** collapses files as well, for a list of just the
  files.
- **Deadlines** show at the end of an Outline entry's line, folded or
  not, as desktop orgtd shows them when space is tight: `D:11-02` (with
  the year if it isn't this one), in the timestamp color. A long title
  is cut short to make room.
- **Details in place:** tap any entry in Outline to
  open its details right there in the list, under its parent and above
  its children: full title, dates, notes (links tappable), tags and
  properties. Tap it again, or open another, to close it. In Outline,
  editing and the state are on the swipes (below); details opened in
  Agenda have a state menu and a pencil instead. An open
  entry's row is tinted, which sets its details apart from its children
  below. In Outline, a thin line runs down from each expanded entry's
  chevron past its last child.
- **From the Agenda:** tap an entry to jump to it in Outline, expanded
  to and scrolled into view, with its details open. An entry Outline
  doesn't show (closed and hidden, or in a file it leaves out) opens
  its details in the Agenda instead.
- **Changing an entry's state:** in Outline, swipe it right: tap one of
  the states shown (an open entry gets DONE and your first two active
  states, a done entry your first three active ones; a full swipe sets
  the first), or **More** for all of them. Details opened in Agenda
  have a state menu. This works as desktop `r` does: a done state stamps
  `CLOSED`, leaving one clears it, and completing a repeating item
  advances its dates and sets `LAST_REPEAT` instead. Changed entries
  show a clock until they're merged.
  - If you change the same entry on your computer before merging, the
    phone's change is dropped rather than guessed at, and the app tells
    you so.
  - Reference material has no state.
  - The phone won't change states in a file whose `.gitattributes`
    entry sets `merge=union`, since a union merge would duplicate the
    edited line.
- **Editing an entry:** in Outline, swipe it left; the edit form opens
  in a sheet. Details opened in Agenda have a pencil that opens it in
  place. The edit form opens in place, with
  the capture form's fields: keyword, title, notes, tags and dates.
  - Only the fields you change are written back, rewriting just those
    lines.
  - A date you don't touch stays exactly as written, repeater and time
    included. A date you change keeps its repeater, as rescheduling in
    org-mode does.
  - Like state changes, an edit is dropped (and you're told) if the
    entry changed on your computer before you merged.
  - A capture that isn't merged yet is edited as a capture.

## On your computer

Fetch from the server and merge `origin/mobile` into your main branch
with a normal merge or a fast-forward, then push. Don't squash or cherry-pick it: the phone recognizes
merged captures by commit ancestry. After you merge, the phone's next
Sync drops those captures and state changes from its queues. Once
nothing is pending, it deletes the `mobile` branch.

Captures added on both sides at once, or a state change made on both
sides to the same entry, are ordinary git conflicts, for you to resolve
when merging.

Settings that should apply to the phone too (keywords and their colors,
`inbox_file`, `agenda_window_days`, `hide_closed_after_hours`,
`outline_ignore_files`, ...) go in `.orgtd.toml` at the root of the
org repository. See the root README's "Config file" section.

## Layout

```
App/              the app target (SwiftUI): Core/ (Go bridge, state), Views/
ShareExtension/   the share extension (no Go; writes one JSON file per capture)
Shared/           compiled into both targets: App Group paths, the capture model and form
Config/           xcconfig, entitlements, the extension's Info.plist
Icon/             the app icon's SVG sources; render.sh regenerates App/Assets.xcassets
Frameworks/       Mobilecore.xcframework (built, gitignored)
```
