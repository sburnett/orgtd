#!/usr/bin/env python3
"""Start a Claude Code session seeded with the org entry you're on.

Meant to be run by orgtd's :run with context = true (see README's
"Passing context to a command"):

    [[commands]]
    name = "claude"
    command = "~/git/orgtd/scripts/claude_entry.py"
    context = true

Then ":run claude" on an entry (or on a visual selection) opens Claude
Code with the entry in its system prompt: the entry itself, everything
under it, and each ancestor heading's own notes. Claude opens by
summarizing the task and proposing a first step. Anything after the
command name is passed on to claude, e.g. ":run claude --model opus".

Claude starts in the directory named by a PROJECT_DIR property on the
entry or, failing that, the nearest ancestor that has one, so setting it
once on a project heading covers every task under it:

    * Orgtd
      :PROPERTIES:
      :PROJECT_DIR: ~/git/orgtd
      :END:

"~" is expanded, and a relative path is relative to the org directory.
With no PROJECT_DIR, Claude starts in the org directory. The org
directory is always made available to Claude (--add-dir), so it can
update your notes when asked; orgtd reloads any file changed once
Claude exits.

Environment:
    CLAUDE_ENTRY_BIN      program to run instead of "claude"
    CLAUDE_ENTRY_DRY_RUN  if set, print the directory, arguments and
                          system prompt instead of starting Claude
"""

import json
import os
import re
import sys
from typing import NoReturn

DIR_PROPERTY = "PROJECT_DIR"

# Caps on how much of your notes goes into the system prompt, so one huge
# subtree can't crowd out everything else.
MAX_SUBTREE_CHARS = 30_000
MAX_ANCESTOR_BODY_CHARS = 4_000

HEADLINE_RE = re.compile(r"^(\*+)\s")
PROPERTY_RE = re.compile(r"^\s*:([^:\s]+):\s*(.*?)\s*$")

OPENING_ONE = (
    "I'd like to start working on the org entry described in your system "
    "prompt. Summarize the task in a sentence or two, mention anything in "
    "its notes or context that looks important, and propose a first step. "
    "Don't make any changes yet."
)
OPENING_MANY = (
    "I'd like to start working on the {n} org entries described in your "
    "system prompt. Summarize them briefly, note how they relate, and "
    "propose where to begin. Don't make any changes yet."
)


def fail(message) -> NoReturn:
    """Report message and exit. orgtd has handed over the terminal, so
    wait for Enter first: otherwise the message would vanish as soon as
    orgtd redraws."""
    print(f"claude_entry: {message}", file=sys.stderr)
    if sys.stdin.isatty():
        try:
            input("Press Enter to return to orgtd.")
        except EOFError:
            pass
    sys.exit(1)


def headline_level(line):
    m = HEADLINE_RE.match(line)
    return len(m.group(1)) if m else 0


def own_text_end(lines, start):
    """Index just past the entry starting at lines[start]: its headline,
    planning, drawers and body, but not its children."""
    i = start + 1
    while i < len(lines) and not headline_level(lines[i]):
        i += 1
    return i


def subtree_end(lines, start):
    """Index just past the entry starting at lines[start] and everything
    nested under it."""
    level = headline_level(lines[start])
    i = start + 1
    while i < len(lines):
        lv = headline_level(lines[i])
        if lv and lv <= level:
            break
        i += 1
    return i


def properties(text_lines):
    """The property drawer of an entry's own text, keys uppercased (org
    property names are case-insensitive)."""
    props, inside = {}, False
    for line in text_lines:
        s = line.strip()
        if s.upper() == ":PROPERTIES:":
            inside = True
        elif s.upper() == ":END:" and inside:
            break
        elif inside:
            m = PROPERTY_RE.match(line)
            if m:
                props[m.group(1).upper()] = m.group(2)
    return props


def body(text_lines):
    """An entry's own body: its text minus the headline, planning line
    and property drawer."""
    out, inside = [], False
    for line in text_lines[1:]:
        s = line.strip()
        if s.upper() == ":PROPERTIES:":
            inside = True
            continue
        if inside:
            if s.upper() == ":END:":
                inside = False
            continue
        if re.match(r"^(SCHEDULED|DEADLINE|CLOSED):", s):
            continue
        out.append(line)
    return "\n".join(out).strip()


def clip(text, limit):
    if len(text) <= limit:
        return text
    return text[:limit] + f"\n[... truncated; {len(text) - limit} more characters in the file]"


class FileCache:
    def __init__(self):
        self.files = {}

    def lines(self, path):
        if path not in self.files:
            try:
                with open(path, encoding="utf-8") as f:
                    self.files[path] = f.read().split("\n")
            except OSError:
                self.files[path] = None
        return self.files[path]

    def headline_index(self, path, line):
        """The 0-based index of the headline orgtd said is on 1-based
        line, or None if the file doesn't have one there (it changed, or
        couldn't be read)."""
        lines = self.lines(path)
        if not lines or not (1 <= line <= len(lines)):
            return None
        i = line - 1
        return i if headline_level(lines[i]) else None


def describe_entry(entry, cache):
    """The system-prompt section for one entry, and its PROJECT_DIR
    (from the entry or its nearest ancestor that has one), or None."""
    path = entry["file"]
    lines = cache.lines(path)
    idx = cache.headline_index(path, entry["line"])

    parts = [
        f"## {' / '.join(entry['outline_path'])}",
        f"File: {path}, line {entry['line']}",
    ]

    # Ancestors, outermost first, with their own notes.
    project_dir = None
    ancestor_parts = []
    for a in entry["ancestors"]:
        a_idx = cache.headline_index(path, a["line"])
        if a_idx is None:
            ancestor_parts.append(f"- {'*' * a['level']} {a['title']}")
            continue
        own = lines[a_idx:own_text_end(lines, a_idx)]
        project_dir = properties(own).get(DIR_PROPERTY, project_dir)
        text = body(own)
        ancestor_parts.append(f"- {lines[a_idx].strip()}")
        if text:
            ancestor_parts.append(clip(text, MAX_ANCESTOR_BODY_CHARS))
    if ancestor_parts:
        parts += ["", "### Ancestor headings, outermost first", *ancestor_parts]

    if entry["properties"].get(DIR_PROPERTY):
        project_dir = entry["properties"][DIR_PROPERTY]

    # The entry and everything under it, straight from the file; orgtd's
    # rendering of the entry alone if the file doesn't match.
    if idx is not None:
        subtree = "\n".join(lines[idx:subtree_end(lines, idx)]).rstrip()
        parts += ["", "### The entry, with everything nested under it", "```org", clip(subtree, MAX_SUBTREE_CHARS), "```"]
    else:
        parts += ["", "### The entry", "```org", entry["raw"].rstrip(), "```"]

    return "\n".join(parts), project_dir


def resolve_dir(raw, org_dir):
    path = os.path.expanduser(raw.strip())
    if not os.path.isabs(path):
        path = os.path.join(org_dir, path)
    return os.path.normpath(path)


def main():
    ctx_path = os.environ.get("ORGTD_CONTEXT")
    if not ctx_path:
        fail("no $ORGTD_CONTEXT; run this from orgtd with :run, from a [[commands]] entry with context = true")
    try:
        with open(ctx_path, encoding="utf-8") as f:
            ctx = json.load(f)
    except (OSError, ValueError) as e:
        fail(f"reading {ctx_path}: {e}")

    entries = ctx.get("entries") or []
    if not entries:
        fail("no entry under the cursor; move to an entry (or select some with V) and :run again")

    org_dir = ctx["dir"]
    cache = FileCache()
    sections, project_dir = [], None
    for e in entries:
        text, d = describe_entry(e, cache)
        sections.append(text)
        if project_dir is None and d:
            project_dir = d

    workdir = org_dir
    note = ""
    if project_dir:
        candidate = resolve_dir(project_dir, org_dir)
        if os.path.isdir(candidate):
            workdir = candidate
        else:
            note = f"\n(The entry's {DIR_PROPERTY}, {candidate}, doesn't exist, so this session started in the org directory instead.)\n"

    what = "an org-mode entry" if len(entries) == 1 else f"{len(entries)} org-mode entries"
    system_prompt = f"""\
# Task context from orgtd

The user started this session from orgtd, their org-mode task manager, \
to work on {what} from their notes. Treat the entry as the task at hand. \
Its notes, sub-entries and the ancestor headings above it are background \
the user wrote for themselves: terse, possibly out of date, and \
sometimes mixed with unrelated items.

Their org files are in {org_dir}. If asked to update an entry (mark it \
done, add a note, record progress), edit its file directly: the line \
numbers below are as of the start of this session, so re-read the file \
before editing. orgtd saved every file before starting this session and \
reloads whatever changed when it ends.
{note}
{chr(10).join(sections)}
"""

    opening = OPENING_ONE if len(entries) == 1 else OPENING_MANY.format(n=len(entries))
    name = entries[0]["title"] if len(entries) == 1 else f"{entries[0]['title']} (+{len(entries) - 1} more)"
    prog = os.environ.get("CLAUDE_ENTRY_BIN", "claude")
    # The opening message goes first: --add-dir takes any number of
    # directories, so a prompt after it would be read as another one.
    argv = [prog, opening, "--append-system-prompt", system_prompt, "--name", name]
    if os.path.realpath(workdir) != os.path.realpath(org_dir):
        argv += ["--add-dir", org_dir]
    argv += sys.argv[1:]

    if os.environ.get("CLAUDE_ENTRY_DRY_RUN"):
        print(f"cwd: {workdir}")
        print("argv:", json.dumps([a for a in argv if a != system_prompt], indent=2))
        print("--- system prompt ---")
        print(system_prompt)
        return

    os.chdir(workdir)
    try:
        os.execvp(prog, argv)
    except OSError as e:
        fail(f"running {prog}: {e}")


if __name__ == "__main__":
    main()
