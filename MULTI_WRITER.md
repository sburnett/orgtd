# Multiple writers via git — design and implementation plan

Status: **proposal**, not built. Once it is, the parts that describe how
the code works move into [DESIGN.md](DESIGN.md) and the user-facing parts
into [README.md](README.md), and this file is deleted.

## 1. Goal

Let several orgtd instances run against the same org directory at once,
all able to write, and have their edits reconciled automatically. Edits
made outside orgtd (Emacs, `git pull`, a `:run` command) should be
reconciled the same way.

The org directory becomes the root of a git repository, and **git is the
store**: every save is a commit, and reconciling concurrent edits is a
3-way merge done in Go, on top of go-git. This matches the iOS app,
which already treats the same repository as its store (see
[ios/DESIGN.md](ios/DESIGN.md)).

Non-goals for now:
- Keystroke-level real-time collaboration. Other instances' edits appear
  within a couple of seconds, or at your next save if you have unsaved
  edits.
- Pushing or pulling. Remotes stay a manual or `:run` job.
- Several people sharing one checkout. This is one person running
  several instances.

## 2. Requirements

- `--dir` is the root of a git repository (`.git` directly inside it).
  If it isn't, orgtd refuses to start and suggests `git init`.
- The repository's `HEAD` is on the main branch, which the new
  `main_branch` setting names (default `main`). If `HEAD` is on any other
  branch or detached, orgtd opens but refuses to save and says why.
- No `git` binary is needed. Everything goes through go-git, which is
  already a dependency (v5.19.3). Tests also use the `git` binary, when
  one is installed, to check that real git agrees with the result.

## 3. Concepts

| Name | What it is | Where it lives |
|---|---|---|
| **instance ID** | Unique per running orgtd: `<host>-<pid>-<start unix time>` | in memory |
| **base** | The commit on `main` this instance's in-memory files were loaded from, or last merged with | in memory |
| **save commit** | This instance's files committed on top of its base | git object store |
| **pending ref** | `refs/orgtd/<instance ID>`. It points at the save commit while a save is in progress or stuck on a conflict, and doesn't exist otherwise. Not a branch: not listed by `git branch` or gitui, and never pushed | `.git/refs/orgtd/` |
| **instance lock** | A flock held for as long as the instance runs. If someone else can acquire it, the instance is dead | `.git/orgtd/instances/<ID>.lock` |
| **merge lock** | A flock held only while a save computes its merge and moves `main`. It keeps orgtd instances from racing each other | `.git/orgtd/merge.lock` |

Everything orgtd-specific lives under `.git/`, so nothing new appears in
the working tree. The current `.orgtd.lock`, which allows one instance per
directory, is removed.

## 4. Saving (`:w`)

Inputs: the base commit B, and the rendered content (`org.RenderFile`) of
every dirty file.

1. **Commit locally.** Build a tree by taking B's tree and replacing the
   blobs of the dirty files. Commit it with parent B, giving save commit
   S. This touches only the object store.
2. **Pin it.** Point `refs/orgtd/<ID>` at S. From here on, a crash can't
   lose the edits (§9).
3. **Take the merge lock.**
4. **Read `main`** and call its commit M.
5. **Commit edits made outside orgtd** (§6). If tracked `.org` files in
   the working tree differ from M, commit them on top of M first. M
   becomes that commit.
6. **Reconcile:**
   - If M == B, the result is S itself (a fast-forward).
   - Otherwise, 3-way merge the trees of B (base), S (ours) and M
     (theirs), as in §5.
     - **Clean:** commit the merged tree with M as its *only* parent.
       History stays linear, like a rebase. Call the result R.
     - **Conflicts:** release the merge lock, keep the pending ref, and
       return the conflicts (§8). Nothing else changes.
7. **Apply R to disk, in this order:**
   1. Create `.git/index.lock`, git's own lock, so `git pull` or `git
      commit` running at the same moment fails fast instead of racing.
   2. Write every file that differs between M and R, using the same
      atomic temp-and-rename as `org.WriteFile`.
   3. Update the index entries for those paths.
   4. Move `refs/heads/<main_branch>` from M to R with a compare-and-set
      (`CheckAndSetReference`). If `main` moved anyway, start again from
      step 4.
   5. Remove `.git/index.lock`.

   Files are written **before** `main` moves on purpose. If orgtd crashes
   in between, the working tree holds R while `main` still says M. The
   next save sees that as edits made outside orgtd (step 5) and commits
   them, and since the content is R, nothing is lost. The opposite order
   would turn a crash into an apparent revert of the merge.
8. **Clean up.** Delete the pending ref and release the merge lock.
9. **Update memory.** Set base = R. Reload every loaded file whose
   content in R differs from what this instance rendered, using the
   existing `swapFile` path. Files the merge didn't touch keep their undo
   history and marks; reloaded ones lose them, as with `:e!` today.

Commit author: `user.name`/`user.email` from the global git config
(go-git's `config.LoadConfig(config.GlobalScope)`), else `orgtd`.
Message: `orgtd: save <files>` for now. Generating messages from the undo
actions (`Mark DONE: Mushroom tour`) is a later improvement (§12).

## 5. Merging

### Trees

Only the paths orgtd manages take part in a merge: top-level `*.org`
files plus `reference/*.org`, minus `outline_ignore_files`. Every other
path is taken from M unchanged. Per path, with b/o/t the base, ours and
theirs versions:

| Case | Result |
|---|---|
| o == b | t |
| t == b | o |
| o == t | o |
| changed on both sides | 3-way text merge (below) |
| deleted on one side, unchanged on the other | deleted |
| deleted on one side, changed on the other | conflict |
| added on both sides with different content | 3-way text merge, with an empty base |

### File contents

**Stage 1: a line-based diff3**, in a new pure-Go package
`internal/merge3` (no git, no UI). It's a Myers line diff of base→ours and
base→theirs; non-overlapping hunks merge, and overlapping ones are a
conflict. Hunks that change both sides identically are not a conflict.
On a conflict, the output uses standard `<<<<<<< ======= >>>>>>>` markers,
so any merge tool understands it. Before writing this, evaluate
`github.com/epiclabs-io/diff3`. It's about 200 lines either way, and
owning it keeps stage 2 simple.

**Stage 2 (later): an entry-level merge** that uses the `org` parser. It
matches entries across b/o/t by `:ID:`, falling back to outline path plus
position, and merges keyword, priority, tags, planning, properties and
body independently. This removes the main false conflict of a line merge:
two instances appending entries under the same parent, such as captures
at the end of `inbox.org`. It plugs in behind the same per-file merge
function as stage 1.

## 6. Edits made outside orgtd

Any difference between the working tree and `main` in a managed path is
an outside edit: Emacs, a hand edit, a `:run` command, or a crash in §4
step 7. Staged but uncommitted changes count too.

**Proposed policy:** commit them automatically, as their own commit
(`orgtd: commit outside edits to <files>`), whenever orgtd is about to
merge (§4 step 5), and after every `:run` command exits. This replaces
the current "changed on disk" refusal: they are now just another writer
to merge with.

To detect them cheaply without a full `git status`, hash each managed
file as a git blob and compare it to M's entry. The repo is a handful of
files.

## 7. Picking up other instances' changes

Every 2 s (a `tea.Tick` like `autoSyncTickMsg`), read the `main` ref:

- **It hasn't moved:** do nothing.
- **It moved, and this instance has no unsaved changes:** reload the
  managed files that differ between base and the new `main`, then set
  base to the new `main`. This is what makes concurrent instances feel
  nearly live.
- **It moved, and there are unsaved changes:** don't reload. Show an
  indicator in the status line ("main moved — :w merges"); the next save
  merges.

Uncommitted outside edits are not picked up by polling. They become
visible after the next save or `:run` commits them. A cheap file-hash
check on the same tick could pick them up later if that turns out to
matter.

## 8. Conflicts

When a save conflicts:

- The instance keeps its edits, still unsaved. The pending ref still
  points at S, and `main` is untouched, so other instances never see a
  half-finished merge.
- The status line says `Conflict in todo.org — :resolve to fix, :w! to keep yours, :e! to take theirs`.

Commands:

- **`:resolve`** writes the conflicted files, with markers, to
  `<scratch_dir>/merge-<ID>/`. It launches the `merge_tool` setting there
  (default: `editor`, on the conflicted files) with the terminal handed
  over, like `:run`.
  - When the tool exits: if any file still has conflict markers, report
    it and change nothing.
  - Otherwise, commit the resolved tree with parent M, the `main` the
    conflict was computed against, and continue from §4 step 4. If
    `main` moved again meanwhile, that's an ordinary merge.
- **`:w!`** resolves every conflicted file in favor of this instance's
  version, as the whole file. Today `:w!` means "overwrite the disk";
  this is the same intent.
- **`:e!`** discards this instance's edits to the file and reloads it
  from `main`, like today.

If you keep editing before resolving, the next `:w` or `:resolve` merges
again from scratch: base, the current in-memory files, and the current
`main`.

The scratch folder is ordinary files, not a git worktree, since go-git
can't create linked worktrees. That's fine: it's disposable, because the
pending ref holds the edits.

## 9. Crash recovery

| Crash during | Left behind | Recovery |
|---|---|---|
| editing, before `:w` | nothing | unsaved edits are lost, as today |
| §4 steps 1–6 | pending ref, maybe unused objects | `git merge refs/orgtd/<ID>`, or orgtd's startup check |
| conflict, before `:resolve` finishes | pending ref, scratch folder | same; delete `<scratch_dir>/merge-<ID>/` |
| §4 step 7 | `.git/index.lock`, possibly some files written but `main` not moved | delete `.git/index.lock` (git's own advice); the next save commits the files as outside edits, with the same content |
| §4 step 8 | pending ref pointing at a commit already in `main` | harmless; the startup check deletes it |

**Startup check:** list `refs/orgtd/*`. For each ref whose instance lock
can be acquired (its instance is dead):
- If its commit is already merged into `main`, delete the ref silently.
- Otherwise, show `Recovered unsaved edits from a crashed orgtd — :recover`.
  `:recover` merges it like a save, conflicts included, or
  `:recover drop` deletes it.

Locks are flocks, so the OS releases them when a process dies, and
nothing goes stale. The exception is `.git/index.lock`, which is git's
convention and works the way git users already know.

## 10. Changes to existing features

| Feature | Change |
|---|---|
| `.orgtd.lock` (one instance per directory) | removed |
| "Files changed on disk" hash check (`org.ChangedOnDisk`) | replaced by merging; outside edits are committed (§6) |
| `:w` / `:wq` / `:wqa` | go through §4; `:wq` refuses to quit while a conflict is pending |
| `:w!` | keeps this instance's version of conflicted files (§8) |
| `:e!` | reloads from `main` instead of from disk |
| `:run` | saves via §4 before launching. After it exits, commits outside edits and reloads, as §7 does when `main` moved, replacing `finishRun`'s hash check |
| `:sync-calendar` | writes `calendar_file` through §4, as a save of that one file, instead of calling `org.WriteFile` directly |
| `:edit` of a new file | its first save adds it to the tree |
| `scratch_dir` | defaults to `scratch/` inside the org dir. It must be in `.gitignore` or it shows as untracked; orgtd ignores it either way, since only managed `.org` paths take part |
| iOS `mobile` branch | unaffected. You still merge it into `main` by hand, which is just another change to `main` that instances pick up |

## 11. Config and docs

- New `main_branch` (default `main`). It's a repo-wide setting, so
  `.orgtd.toml` may set it. The iOS app calls the same idea `BaseBranch`
  in its sync state; consider sharing the name.
- New `merge_tool` (default: `editor`). It's machine-local, so
  `.orgtd.toml` can't set it, like `editor`.
- README: replace the lock paragraph and "Files changed on disk". Add a
  "Running several instances" section (saving, conflicts, `:resolve`,
  `:recover`, recovering by hand with git), plus the new keys and
  commands in their tables.
- DESIGN.md: package map entries for `internal/merge3` and
  `internal/gitstore`, and the §4 step 7 ordering invariant.

## 12. Package layout

New packages, neither depending on `internal/ui`:

- **`internal/merge3`**: `Merge(base, ours, theirs []byte) (merged []byte, conflicts int)`. Pure functions.
- **`internal/gitstore`**: everything git.
  ```go
  type Store struct { /* repo, dir, main branch, instance ID, locks */ }

  func Open(dir, mainBranch string) (*Store, error)  // checks §2, takes the instance lock
  func (s *Store) Head() (plumbing.Hash, error)       // current main, for polling
  func (s *Store) Read(commit plumbing.Hash, paths []string) (map[string][]byte, error)
  func (s *Store) CommitOutsideEdits(paths []string) (plumbing.Hash, error)

  type SaveResult struct {
      NewBase   plumbing.Hash
      Changed   []string          // files that differ from what was saved, to reload
      Conflicts map[string][]byte // path → content with markers; nil if clean
      Against   plumbing.Hash     // the main the conflict was computed against
  }
  func (s *Store) Save(base plumbing.Hash, files map[string][]byte, msg string) (SaveResult, error)
  func (s *Store) Resolve(conflict SaveResult, resolved map[string][]byte) (SaveResult, error)

  func (s *Store) Orphans() ([]Orphan, error)          // dead instances' pending refs
  func (s *Store) Recover(o Orphan) (SaveResult, error)
  func (s *Store) Drop(o Orphan) error
  ```
  `Save` takes an optional failure-injection hook for tests, so a crash
  can be simulated between each of §4's steps.

The UI gains a `store *gitstore.Store` and a `base` hash on `Model`.
`writeAllResult` calls `Save` instead of `org.WriteFile`. A cleaned-up
version of `finishRun`'s reload loop becomes the shared "reload what
changed" helper for saves, polling, `:run` and `:recover`.

## 13. Implementation plan

Each phase is a commit, or a few, that leaves orgtd working.

1. **`internal/merge3`.** Diff3 with table tests: clean merges, identical
   changes on both sides, overlapping conflicts, empty base, missing
   trailing newline, CRLF. Fuzz test: merging x with itself and x gives x.
2. **`internal/gitstore`: reading and committing.** `Open`, `Head`,
   `Read`, tree building, `CommitOutsideEdits`, and author lookup. Tests
   build repos in `t.TempDir()` with go-git, and when the `git` binary is
   available, check `git fsck` and `git status --porcelain` agree.
3. **`gitstore.Save`, the clean paths.** Fast-forward and clean merges,
   the apply-to-disk ordering, `index.lock`, the compare-and-set retry,
   and the merge lock. Tests: two Stores on one repo saving in turn;
   simulated crashes after each step, then checking that recovery
   (§9) gets everything back.
4. **Wire into orgtd.** Startup requirement checks, removing
   `.orgtd.lock`, `main_branch`, `:w`/`:wq`/`:e!`, `:run`,
   `:sync-calendar`, and reloading after a save. Conflicts still refuse
   to save at this stage, with a "resolve with git" message. **First
   usable milestone.**
5. **Polling** (§7) and the "main moved" indicator.
6. **Conflicts.** `Save`'s conflict result, `:resolve`, `merge_tool`, and
   `:w!` keeping yours.
7. **Recovery.** `Orphans`, the startup check, `:recover`, and
   `:recover drop`.
8. **Docs.** README and DESIGN.md (§11); delete this file.

Later:
- Entry-level merge (§5 stage 2).
- Commit messages generated from undo actions.
- Optional autosave of unsaved edits to the pending ref for crash safety.
- Sharing `gitstore` with the iOS app.

## 14. Risks and open questions

- **Outside-edit policy (§6).** Auto-committing them is proposed. The
  alternative is refusing to save until you commit them yourself. This
  needs a decision before phase 4.
- **Non-git directories.** Starting is refused outright. A fallback to
  today's single-instance behavior would be friendlier to new users, but
  means keeping both code paths.
- **go-git index fidelity.** go-git rewrites `.git/index` without some
  extensions git may use (untracked cache, fsmonitor). Git rebuilds
  those, but phase 3's tests must confirm `git status` stays correct
  after orgtd updates the index.
- **`git pull` racing a save.** Taking `index.lock` (§4 step 7) covers
  git commands that write the working tree. Tools that bypass git's
  locking (a sync script rewriting files directly) can still race the
  very short write step.
- **Commit volume.** One commit per `:w`, plus outside-edit commits.
  It's fine for history, but makes hand-written commit messages rarer.
  Squashing on push is out of scope.
- **Line-merge false conflicts.** Appends under the same parent will
  conflict until the entry-level merge exists. Measure how often this
  happens after phase 4 before investing in stage 2.
