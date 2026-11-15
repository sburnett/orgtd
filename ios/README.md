# orgtd for iOS

An iPhone companion to orgtd: capture into the inbox (in the app or from
the share sheet) and read the agenda, inbox and outline. It never
changes an existing entry. New captures are appended to `inbox_file`
and committed to a `mobile` branch, which you merge on your computer.
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

- **+** captures: title, notes, tags (autocompleted from your files), and
  SCHEDULED/DEADLINE dates. You can pick a date, or type one the way
  desktop `gd` accepts (`3d`, `2w`, `next tue`). Saving commits locally
  and needs no network.
- The app fetches on launch, when it returns to the foreground, and on
  pull-to-refresh. Fetching never pushes.
- **Sync** (↻) pushes. `mobile` is rebuilt as your main branch plus
  every capture that isn't merged yet, then force-pushed with a lease.
  If something other than this phone moved `origin/mobile`, the push is
  refused and the status line says so.
- In Inbox, captures that aren't merged yet show a clock. Swipe one to
  edit or delete it.

## On your computer

Fetch from the server and merge `origin/mobile` into your main branch
with a normal merge or a fast-forward, then push. Don't squash or cherry-pick it: the phone recognizes
merged captures by commit ancestry. Add this to the org repository's
`.gitattributes` (use your `inbox_file`):

```
inbox.org merge=union
```

Then, when both sides added inbox items, git keeps both instead of
reporting a conflict. After you merge, the phone's next Sync drops those
captures from its queue. Once nothing is pending, it deletes the
`mobile` branch.

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
Frameworks/       Mobilecore.xcframework (built, gitignored)
```
