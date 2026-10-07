#!/usr/bin/env python3
"""Generate docs/reference/cli.md, the command reference, from the real ccshelf binary.

Usage (normally through scripts/gen-cli-reference.sh, which builds the binary first):
  python3 -I scripts/gen_cli_reference.py --bin <ccshelf> [--write | --check] [--out docs/reference/cli.md]

It runs `ccshelf --help` and `ccshelf <command> --help` for every command and subcommand it
discovers from the help output (never `claude`, never the real configuration: the child process gets
a throwaway HOME and XDG/APPDATA directories and nothing else of the environment), parses the cobra
help layout and writes Markdown in the repository's documentation subset. Exit codes are read from
internal/ui/exit.go. The output is deterministic: the same binary gives the same bytes.

  (no flag)  print the Markdown to stdout
  --write    write the file
  --check    exit 1 when the committed file differs from what the binary prints now

Standard library only.
"""
from __future__ import annotations

import argparse
import os
import re
import subprocess
import sys
import tempfile

ROOT = os.path.normpath(os.path.join(os.path.dirname(os.path.abspath(__file__)), ".."))
DEFAULT_OUT = os.path.join(ROOT, "docs", "reference", "cli.md")
EXIT_GO = os.path.join(ROOT, "internal", "ui", "exit.go")
PROG = "ccshelf"
# Cobra command groups have application-defined headings. Recognize headings
# generically, then require command-shaped rows in non-reserved sections.
SECTION_RE = re.compile(r"^([A-Za-z][^:\n]*):$")
FLAG_RE = re.compile(r"^ {2}(?:(-\w), | {4})(--[\w-]+)(?: ([\w\[\]]+))?\s{2,}(\S.*)$")
CMD_RE = re.compile(r"^ {2}([\w-]+)\s{2,}(\S.*)$")


class Help:
    """One parsed help page."""

    def __init__(self) -> None:
        self.desc: list[str] = []
        self.usage: list[str] = []
        self.aliases: list[str] = []
        self.examples: list[str] = []
        self.commands: list[tuple[str, str]] = []
        self.flags: list[tuple[str, str, str, str]] = []  # (short, long, value, description)
        self.global_flags: list[tuple[str, str, str, str]] = []


def parse_help(text: str) -> Help:
    h = Help()
    lines = text.replace("\r\n", "\n").split("\n")
    i = 0
    while i < len(lines) and lines[i] != "Usage:":
        h.desc.append(lines[i])
        i += 1
    if i >= len(lines):
        raise ValueError("no Usage: section in the help output")
    while h.desc and not h.desc[-1].strip():
        h.desc.pop()
    section = ""
    for line in lines[i:]:
        m = SECTION_RE.match(line)
        if m:
            section = m.group(1)
            continue
        if line.startswith("Use \"") or not line.strip():
            if line.startswith("Use \""):
                section = ""
            continue
        if section == "Usage":
            h.usage.append(line.strip())
        elif section == "Aliases":
            h.aliases.extend(a.strip() for a in line.split(","))
        elif section == "Examples":
            h.examples.append(line[2:] if line.startswith("  ") else line)
        elif section and section not in ("Flags", "Global Flags"):
            m = CMD_RE.match(line)
            if not m:
                raise ValueError("cannot parse command line: %r" % line)
            h.commands.append((m.group(1), m.group(2)))
        elif section in ("Flags", "Global Flags"):
            m = FLAG_RE.match(line)
            if not m:
                raise ValueError("cannot parse flag line: %r" % line)
            entry = (m.group(1) or "", m.group(2), m.group(3) or "", m.group(4).strip())
            (h.flags if section == "Flags" else h.global_flags).append(entry)
        else:
            raise ValueError("unexpected line outside a known section: %r" % line)
    return h


def run_help(binary: str, path: list[str], env: dict[str, str]) -> str:
    r = subprocess.run([binary, *path, "--help"], env=env, capture_output=True, text=True, timeout=60, check=False)
    if r.returncode != 0:
        raise SystemExit("gen-cli-reference: %s %s --help exited %d: %s" % (PROG, " ".join(path), r.returncode, r.stderr.strip()))
    return r.stdout


def scrub_env(home: str) -> dict[str, str]:
    """A minimal environment: a throwaway home, no PATH (so no claude can be found) and no color."""
    return {
        "HOME": home,
        "USERPROFILE": home,
        "XDG_CONFIG_HOME": os.path.join(home, "config"),
        "XDG_CACHE_HOME": os.path.join(home, "cache"),
        "XDG_DATA_HOME": os.path.join(home, "data"),
        "APPDATA": os.path.join(home, "config"),
        "LOCALAPPDATA": os.path.join(home, "cache"),
        "PATH": os.path.join(home, "empty"),
        "NO_COLOR": "1",
        "TERM": "dumb",
    }


def discover(binary: str, env: dict[str, str]) -> list[tuple[list[str], str, Help]]:
    """Every command, depth first in help order: (path, short description, parsed help)."""
    out: list[tuple[list[str], str, Help]] = []

    def walk(path: list[str], short: str) -> None:
        h = parse_help(run_help(binary, path, env))
        out.append((path, short, h))
        for name, desc in h.commands:
            if name == "help":
                continue
            walk(path + [name], desc)

    walk([], "")
    return out


def exit_codes(path: str = EXIT_GO) -> list[tuple[int, str]]:
    """(code, meaning) from the documented constants in internal/ui/exit.go."""
    with open(path, encoding="utf-8") as f:
        lines = f.read().split("\n")
    rows: list[tuple[int, str]] = []
    comment: list[str] = []
    for line in lines:
        s = line.strip()
        if s.startswith("//"):
            comment.append(s[2:].strip())
            continue
        m = re.match(r"^(Exit\w+)\s*=\s*(\d+)$", s)
        if m and comment:
            text = " ".join(comment)
            text = re.sub(r"^%s means " % m.group(1), "", text).rstrip(".")
            rows.append((int(m.group(2)), text))
        comment = []
    if not rows:
        raise SystemExit("gen-cli-reference: no exit codes found in %s" % path)
    return sorted(rows)


def code_fence(lines: list[str]) -> list[str]:
    return ["```text", *lines, "```", ""]


def desc_blocks(desc: list[str]) -> list[str]:
    """Description lines to Markdown: prose paragraphs joined on one line, indented runs as code."""
    out: list[str] = []
    para: list[str] = []
    code: list[str] = []

    def flush_para() -> None:
        if para:
            text = " ".join(x.strip() for x in para)
            if re.match(r"^([-*+>#|]|\d+[.)])\s", text):
                raise ValueError("help text paragraph would parse as a Markdown block: %r" % text[:60])
            out.extend([text, ""])
            para.clear()

    def flush_code() -> None:
        if code:
            out.extend(code_fence([c[2:] for c in code]))
            code.clear()

    for line in desc:
        if not line.strip():
            flush_para()
            flush_code()
        elif line.startswith("  "):
            flush_para()
            code.append(line)
        else:
            flush_code()
            para.append(line)
    flush_para()
    flush_code()
    return out


# The inline patterns of docs/build_report.py's renderer (the one the site uses): text matching any of
# them would be reformatted. A lone "*" (as in "profile-*") matches none and stays as written.
_CELL_MARKUP = re.compile(
    r"(`+)(.+?)\1(?!`)|\*\*(.+?)\*\*|(?<![\*\w])\*(?!\s)(.+?)(?<!\s)\*(?![\*\w])|~~(.+?)~~|\{[VRU]\}|\[([^\]]+)\]\(([^)\s]+)\)"
)


def md_cell(text: str) -> str:
    """Help text as one Markdown table cell. A pipe is escaped; text the renderer would read as markup
    (emphasis, strikethrough, a code span, a link, a confidence marker) is refused, because the site's Markdown subset
    has no backslash escape for those and silently reformatting a flag description would misstate it."""
    if _CELL_MARKUP.search(text):
        raise ValueError("help text would be read as Markdown markup in a table cell: %r" % text[:60])
    return text.replace("|", "\\|")


def flag_table(flags: list[tuple[str, str, str, str]]) -> list[str]:
    rows = ["| Flag | Value | Description |", "|---|---|---|"]
    for short, long_, value, desc in flags:
        names = ("`%s`, " % short if short else "") + "`%s`" % long_
        rows.append("| %s | %s | %s |" % (names, "`%s`" % value if value else "", md_cell(desc)))
    return rows + [""]


def title_of(path: list[str]) -> str:
    return " ".join([PROG, *path])


def anchor_of(path: list[str]) -> str:
    return re.sub(r"[^a-z0-9]+", "-", title_of(path).lower()).strip("-")


def render(pages: list[tuple[list[str], str, Help]], codes: list[tuple[int, str]]) -> str:
    root = pages[0][2]
    globals_ = None
    for path, _short, h in pages[1:]:
        if h.global_flags:
            if globals_ is None:
                globals_ = h.global_flags
            elif globals_ != h.global_flags:
                raise SystemExit("gen-cli-reference: the global flags differ between commands (%s)" % title_of(path))
    if globals_ is None:
        raise SystemExit("gen-cli-reference: no global flags found")
    names = {f[1] for f in globals_}
    root_only = [f for f in root.flags if f[1] not in names]

    o: list[str] = []
    o += ["# Command reference", ""]
    o += [
        "**Generated file: do not edit by hand.** Run `scripts/gen-cli-reference.sh --write` to regenerate it.",
        "",
        "{V} This page is **generated from the real `%s` binary**: every section below is the output of `%s --help` or "
        "`%s <command> --help`, parsed and laid out here, and the exit codes are read from `internal/ui/exit.go`. "
        "`scripts/gen-cli-reference.sh --check` (run in CI) fails when this file is stale. It documents the command line as built from "
        "the repository; no version has been released yet." % (PROG, PROG, PROG),
        "",
        "## Global flags",
        "",
        "These flags are accepted by every command. Flags of ccshelf itself go before a profile name: everything after the profile is passed to `claude` unchanged.",
        "",
    ]
    o += flag_table(globals_ + root_only)
    o += ["## Exit codes", "", "Scripts can rely on these; they never change meaning.", "", "| Code | Meaning |", "|---|---|"]
    o += ["| `%d` | %s |" % (c, md_cell(m)) for c, m in codes]
    o += ["", "## Commands", "", "| Command | What it does |", "|---|---|"]
    for path, short, _h in pages[1:]:
        o.append("| [`%s`](#%s) | %s |" % (title_of(path), anchor_of(path), md_cell(short)))
    o.append("")

    for path, short, h in pages[1:]:
        o += ["## " + title_of(path), ""]
        o += desc_blocks(h.desc)
        o += ["**Usage**", ""]
        o += code_fence(h.usage)
        if h.aliases:
            o += ["**Aliases:** " + ", ".join("`%s`" % a for a in h.aliases), ""]
        if h.examples:
            o += ["**Examples**", ""]
            o += code_fence(h.examples)
        own = h.flags
        if own:
            o += ["**Flags**", ""]
            o += flag_table(own)
        subs = [(n, d) for n, d in h.commands if n != "help"]
        if subs:
            o += ["**Subcommands**", "", "| Command | What it does |", "|---|---|"]
            for n, d in subs:
                sub = path + [n]
                o.append("| [`%s`](#%s) | %s |" % (title_of(sub), anchor_of(sub), md_cell(d)))
            o.append("")
    text = "\n".join(o).rstrip("\n") + "\n"
    return text


def generate(binary: str) -> str:
    with tempfile.TemporaryDirectory(prefix="ccshelf-cli-ref-") as home:
        os.makedirs(os.path.join(home, "empty"), exist_ok=True)
        env = scrub_env(home)
        pages = discover(binary, env)
        text = render(pages, exit_codes())
        if home in text:
            raise SystemExit("gen-cli-reference: the throwaway home directory leaked into the output")
        return text


def main(argv: list[str]) -> int:
    ap = argparse.ArgumentParser(description=__doc__.split("\n", 1)[0])
    ap.add_argument("--bin", required=True, help="path to a built ccshelf binary")
    ap.add_argument("--out", default=DEFAULT_OUT)
    mode = ap.add_mutually_exclusive_group()
    mode.add_argument("--write", action="store_true")
    mode.add_argument("--check", action="store_true")
    args = ap.parse_args(argv)
    text = generate(args.bin)
    if args.check:
        cur = ""
        if os.path.exists(args.out):
            with open(args.out, encoding="utf-8", newline="") as f:
                cur = f.read()
        if cur != text:
            print("%s is out of date: run scripts/gen-cli-reference.sh --write" % os.path.relpath(args.out, ROOT), file=sys.stderr)
            return 1
        print("%s is up to date" % os.path.relpath(args.out, ROOT))
        return 0
    if args.write:
        os.makedirs(os.path.dirname(args.out), exist_ok=True)
        with open(args.out, "w", encoding="utf-8", newline="\n") as f:
            f.write(text)
        print("wrote %s (%d bytes)" % (os.path.relpath(args.out, ROOT), len(text)))
        return 0
    sys.stdout.write(text)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
