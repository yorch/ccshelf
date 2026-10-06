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

The whole title may be at most MAX_LEN characters (default 72). Characters of the
Unicode categories Cc, Cf, Cs, Co, Zl and Zp (control, format such as zero-width and
bidi controls, surrogate, private use, line and paragraph separators) are rejected.

A title of the form `chore(main): release ...` is reserved for the release pull
request and is accepted only when PR_AUTHOR is the release bot (github-actions[bot]).

The pull request body is not the title, but release-please reads it at run time:
BEGIN_COMMIT_OVERRIDE and BEGIN_NESTED_COMMIT blocks replace or add changelog
entries, and a `Release-As:` footer forces a version. When PR_BODY is set, a body
containing any of them is rejected unless PR_AUTHOR is the release bot. Maintainers
force a version through release-please-config.json or a `fix:`/`feat:` titled change
(docs/design/release.md), never through a pull request description.

The title, body and author are read from the environment variables PR_TITLE, PR_BODY
and PR_AUTHOR, never from argv, and the workflow passes them through `env:` only.
Usage:

    PR_TITLE='fix(settings): reject env names matching ANTHROPIC_*' \
        python3 -I scripts/check_pr_title.py

Exit status: 0 valid, 1 invalid, 2 usage error.
"""

import os
import re
import sys
import unicodedata

TYPES = ("feat", "fix", "docs", "test", "ci", "build", "refactor", "perf", "chore", "revert")
DEFAULT_MAX_LEN = 72
RELEASE_BOT = "github-actions[bot]"
FORBIDDEN_CATEGORIES = ("Cc", "Cf", "Cs", "Co", "Zl", "Zp")
BODY_MARKERS = ("BEGIN_COMMIT_OVERRIDE", "BEGIN_NESTED_COMMIT")

_RELEASE_TITLE_RE = re.compile(r"^chore\(main\): release ")
# A `Release-As:` footer at the start of any line (also after quote or list marks and
# after any Unicode line separator), in any letter case.
_RELEASE_AS_RE = re.compile(
    r"(?:^|[\r\n\x0b\x0c\x85\u2028\u2029])[\s>*_`~-]*release-as\s*:", re.IGNORECASE
)

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


def check_title(title, max_len=DEFAULT_MAX_LEN, author=None):
    """Return a list of problems with title; an empty list means it is valid.

    author is the login of the pull request author; only RELEASE_BOT may use a
    `chore(main): release ...` title.
    """
    if not isinstance(title, str) or title == "":
        return ["the title is empty"]
    if "\n" in title or "\r" in title:
        return ["the title must be a single line"]
    if any(ord(c) < 0x20 or ord(c) == 0x7F for c in title):
        return ["the title contains control characters"]
    for c in title:
        category = unicodedata.category(c)
        if category in FORBIDDEN_CATEGORIES:
            return [
                "the title contains an invisible or control character (U+%04X, category %s)"
                % (ord(c), category)
            ]
    problems = []
    if _RELEASE_TITLE_RE.match(title) and author != RELEASE_BOT:
        return [
            "titles of the form 'chore(main): release ...' are reserved for the release pull "
            "request opened by %s" % RELEASE_BOT
        ]
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


def check_body(body, author=None):
    """Return a list of problems with a pull request body (see the module docstring)."""
    if author == RELEASE_BOT:
        return []
    if not isinstance(body, str):
        return ["the body is not text"]
    problems = []
    upper = body.upper()
    for marker in BODY_MARKERS:
        if marker in upper:
            problems.append(
                "the description contains %s, which release-please would apply to the "
                "changelog; remove it" % marker
            )
    if _RELEASE_AS_RE.search(body):
        problems.append(
            "the description contains a 'Release-As:' footer, which would force the next "
            "version; remove it (maintainers use release-please-config.json, see "
            "docs/design/release.md)"
        )
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
    author = os.environ.get("PR_AUTHOR", "")
    problems = check_title(title, max_len, author)
    body = os.environ.get("PR_BODY")
    body_problems = check_body(body, author) if body is not None else []
    if not problems and not body_problems:
        print("Pull request title is valid.")
        return 0
    # The title is untrusted: print it escaped so it cannot start a workflow command
    # (a line beginning with '::') or smuggle control characters into the log.
    escaped = title.encode("unicode_escape").decode("ascii")
    print("Pull request title is not a valid Conventional Commits subject.", file=sys.stderr)
    print("  title: " + escaped, file=sys.stderr)
    for problem in problems + body_problems:
        print("  - " + problem, file=sys.stderr)
    print("Examples:", file=sys.stderr)
    for example in EXAMPLES:
        print("  " + example, file=sys.stderr)
    print("Maintainers squash-merge: this title becomes the changelog entry.", file=sys.stderr)
    return 1


if __name__ == "__main__":
    sys.exit(main())
