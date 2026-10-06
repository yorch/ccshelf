#!/usr/bin/env python3
"""Validate a pull request title against the Conventional Commits subset used here.

Maintainers squash-merge, so the pull request title becomes the commit subject on
main, and release-please builds the changelog and the next version from it. This
check keeps those subjects machine-readable.

Format:  type(scope)!: description

  type         feat fix docs test ci build refactor perf chore revert
  (scope)      optional; lowercase letters, digits and . _ / -
  !            optional; marks a breaking change
  description  starts with a lowercase letter or a digit, no trailing period,
               no leading or trailing space, one line

The whole title may be at most MAX_LEN characters (default 72).

The title is read from the environment variable PR_TITLE, never from argv, and
the workflow passes it through `env:` only. Usage:

    PR_TITLE='fix(settings): reject env names matching ANTHROPIC_*' \
        python3 -I scripts/check_pr_title.py

Exit status: 0 valid, 1 invalid, 2 usage error.
"""

import os
import re
import sys

TYPES = ("feat", "fix", "docs", "test", "ci", "build", "refactor", "perf", "chore", "revert")
DEFAULT_MAX_LEN = 72

_TITLE_RE = re.compile(
    r"^(?P<type>[a-z]+)"
    r"(?:\((?P<scope>[a-z0-9]+(?:[._/-][a-z0-9]+)*)\))?"
    r"(?P<breaking>!)?"
    r": (?P<description>\S(?:.*\S)?)$"
)

EXAMPLES = (
    "feat(catalog): add a search command",
    "fix(settings): reject env names matching ANTHROPIC_*",
    "feat(cli)!: rename --profile to --name",
    "docs: explain the release flow",
)


def check_title(title, max_len=DEFAULT_MAX_LEN):
    """Return a list of problems with title; an empty list means it is valid."""
    if not isinstance(title, str) or title == "":
        return ["the title is empty"]
    if "\n" in title or "\r" in title:
        return ["the title must be a single line"]
    if any(ord(c) < 0x20 or ord(c) == 0x7F for c in title):
        return ["the title contains control characters"]
    problems = []
    if len(title) > max_len:
        problems.append("the title is %d characters; the limit is %d" % (len(title), max_len))
    if title != title.strip():
        problems.append("the title has leading or trailing whitespace")
    match = _TITLE_RE.match(title.strip())
    if match is None:
        problems.append(
            "expected 'type(scope)!: description' with a lowercase type, one space after "
            "the colon and a non-empty description"
        )
        return problems
    kind = match.group("type")
    if kind not in TYPES:
        problems.append("type %r is not allowed; use one of: %s" % (kind, " ".join(TYPES)))
    description = match.group("description")
    if description[0].isupper():
        problems.append("the description must not start with an uppercase letter")
    if description.endswith("."):
        problems.append("the description must not end with a period")
    return problems


def main():
    title = os.environ.get("PR_TITLE")
    if title is None:
        print("PR_TITLE is not set", file=sys.stderr)
        return 2
    raw_max = os.environ.get("MAX_LEN", "")
    try:
        max_len = int(raw_max) if raw_max else DEFAULT_MAX_LEN
    except ValueError:
        print("MAX_LEN must be an integer", file=sys.stderr)
        return 2
    if max_len < 20 or max_len > 200:
        print("MAX_LEN must be between 20 and 200", file=sys.stderr)
        return 2
    problems = check_title(title, max_len)
    if not problems:
        print("Pull request title is valid.")
        return 0
    # The title is untrusted: print it escaped so it cannot start a workflow command
    # (a line beginning with '::') or smuggle control characters into the log.
    escaped = title.encode("unicode_escape").decode("ascii")
    print("Pull request title is not a valid Conventional Commits subject.", file=sys.stderr)
    print("  title: " + escaped, file=sys.stderr)
    for problem in problems:
        print("  - " + problem, file=sys.stderr)
    print("Examples:", file=sys.stderr)
    for example in EXAMPLES:
        print("  " + example, file=sys.stderr)
    print("Maintainers squash-merge: this title becomes the changelog entry.", file=sys.stderr)
    return 1


if __name__ == "__main__":
    sys.exit(main())
