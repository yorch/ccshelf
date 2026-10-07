#!/usr/bin/env python3
"""Decide which ci.yml jobs a pull request needs, from the files it changes.

ci.yml runs a `changes` job first; it runs this script, and the other jobs are gated
with `if: needs.changes.outputs.<name> == 'true'`. The required check `ci-ok` treats a
skipped job as passed, so a documentation-only pull request skips the Go matrix.

Fail open: everything runs (every output is true) when
  - the event is not `pull_request` (a push to main, a merge group and a manual
    dispatch always run the full suite; release.yml waits for the push run and the
    release pull request relies on the dispatched run),
  - the changed files cannot be computed or the diff is empty,
  - any changed file is under .github/ (workflows, actions, CODEOWNERS, Dependabot),
  - any changed file matches no known path rule (a new top-level file or directory).

Input, from the environment only (never argv, never `${{ }}` in `run:`):
  EVENT_NAME   github.event_name
  BASE_SHA     github.event.pull_request.base.sha
  HEAD_SHA     github.event.pull_request.head.sha
Output: `name=value` lines appended to $GITHUB_OUTPUT and a short Markdown summary
appended to $GITHUB_STEP_SUMMARY (both printed to stdout when unset).

Groups (a path may belong to several): go, installer, action, docs, examples,
workflows. Job outputs derived from them:
  go        test, vuln, build, lint (Go steps), smoke steps inside test
  pins      lint's check-pins.sh and actionlint steps
  lint      go or pins
  examples  examples job (Go changes too: it runs the built binary)
  site      site job (needs the binary for the CLI reference check)
  install   install-test (the end-to-end step builds the real binary)
  mutants   install-mutants (the installer and its tests only)
  action    action-e2e (it builds the real binary too)
  matrix    the `test` job matrix: the experimental legs run off pull requests only

Standard library only. Run with `python3 -I`.
"""

import json
import os
import re
import subprocess
import sys

GROUPS = ("go", "installer", "action", "docs", "examples", "workflows")

# Each rule: (kind, value, groups). kind is "exact" (whole path), "prefix" (path starts
# with value), "suffix" (path ends with value) or "root_md" (a Markdown file at the
# repository root). A path takes the union of all matching rules.
_GO = ("go",)
_DOCS = ("docs",)
RULES = [
    # Go code, modules, schemas (embedded and tested by Go), lint configuration.
    ("suffix", ".go", _GO),
    ("exact", "go.mod", _GO),
    ("exact", "go.sum", _GO),
    ("exact", ".golangci.yml", _GO),
    ("exact", ".golangci.yaml", _GO),
    ("prefix", "cmd/", _GO),
    ("prefix", "internal/", _GO),
    ("prefix", "schema/", _GO),
    ("exact", "scripts/check-cover.sh", _GO),
    ("exact", "scripts/cover-exceptions.txt", _GO),
    # The release archive naming is read by the installer, Action and Go-adjacent tests.
    ("exact", ".goreleaser.yaml", ("go", "installer", "action")),
    # End-user installers and what builds their end-to-end release tree.
    ("exact", "scripts/install.sh", ("installer",)),
    ("exact", "scripts/install.ps1", ("installer",)),
    ("exact", "scripts/test_install.sh", ("installer",)),
    ("exact", "scripts/test_install.ps1", ("installer",)),
    ("exact", "scripts/make-e2e-release.sh", ("installer",)),
    # The GitHub Action (its action.yml is also a pinned-action file).
    ("prefix", "action/", ("action", "workflows")),
    # The starter template holds workflows, and internal/scaffold's golden tests compare
    # it with generated output, so a change there also runs the Go tests.
    ("prefix", "examples/", ("examples", "go", "workflows")),
    ("exact", "scripts/check-examples.sh", ("examples",)),
    # Documentation, website and the scripts that build or check them.
    ("prefix", "docs/", _DOCS),
    ("prefix", "site/", _DOCS),
    ("root_md", "", _DOCS),
    ("exact", "LICENSE", _DOCS),
    ("exact", ".gitignore", _DOCS),
    # Release-please state: the release pull request changes only these two (the
    # manifest here; CHANGELOG.md is a root Markdown file). The config is not listed,
    # so changing it fails open.
    ("exact", ".release-please-manifest.json", _DOCS),
    ("exact", "scripts/build_docs.py", _DOCS),
    ("exact", "scripts/test_build_docs.py", _DOCS),
    ("exact", "scripts/build-site.sh", _DOCS),
    ("exact", "scripts/check-site.sh", _DOCS),
    ("exact", "scripts/check_site.py", _DOCS),
    ("exact", "scripts/test_check_site.py", _DOCS),
    ("exact", "scripts/gen-cli-reference.sh", _DOCS),
    ("exact", "scripts/gen_cli_reference.py", _DOCS),
    ("exact", "scripts/test_gen_cli_reference.py", _DOCS),
    ("exact", "scripts/regen-site-demo.sh", _DOCS),
    ("exact", "scripts/check-links.sh", _DOCS),
    ("exact", "scripts/check-eol.sh", _DOCS),
    ("prefix", "scripts/testdata/", _DOCS),
    # Scripts tested by workflows other than ci.yml, and this classifier (its unit tests
    # run in the docs job).
    ("exact", "scripts/check_pr_title.py", _DOCS),
    ("exact", "scripts/test_check_pr_title.py", _DOCS),
    ("exact", "scripts/ci_changes.py", _DOCS),
    ("exact", "scripts/test_ci_changes.py", _DOCS),
    # Action pin tooling.
    ("exact", "scripts/check-pins.sh", ("workflows",)),
    ("exact", "scripts/pin-actions.sh", ("workflows",)),
    # Repository-wide text conventions touch the EOL check, the Go golden files and the
    # starter template.
    ("exact", ".gitattributes", ("go", "docs", "examples")),
    ("exact", ".editorconfig", ("go", "docs", "examples")),
    ("exact", "Makefile", ("go", "docs")),
]

FULL_EVENTS_NOTE = "not a pull request"


def groups_for(path):
    """Return the set of groups a path belongs to; empty when no rule matches."""
    found = set()
    for kind, value, groups in RULES:
        if kind == "exact":
            ok = path == value
        elif kind == "prefix":
            ok = path.startswith(value)
        elif kind == "suffix":
            ok = path.endswith(value)
        else:  # root_md
            ok = "/" not in path and path.endswith(".md")
        if ok:
            found.update(groups)
    return found


def classify(files):
    """Return (groups, reason). reason is None when the groups come from the files, or
    a string saying why everything must run (fail open)."""
    if files is None:
        return set(GROUPS), "the changed files could not be computed"
    if not files:
        return set(GROUPS), "the diff is empty"
    groups = set()
    for path in files:
        if path == ".github" or path.startswith(".github/"):
            return set(GROUPS), "a file under .github/ changed (" + path + ")"
        g = groups_for(path)
        if not g:
            return set(GROUPS), "no rule matches " + path
        groups |= g
    return groups, None


MATRIX_ALL = [
    {"os": "ubuntu-latest", "experimental": False, "race": True},
    {"os": "ubuntu-24.04-arm", "experimental": True, "race": True},
    {"os": "macos-latest", "experimental": False, "race": True},  # arm64
    {"os": "macos-15-intel", "experimental": False, "race": True},  # x64
    {"os": "windows-latest", "experimental": False, "race": True},
    # The Go race detector does not support windows/arm64.
    {"os": "windows-11-arm", "experimental": True, "race": False},
]


def build_outputs(event_name, files):
    """Return (outputs, reason): outputs maps names to 'true'/'false' (matrix to JSON)."""
    if event_name != "pull_request":
        groups, reason = set(GROUPS), FULL_EVENTS_NOTE + " (" + (event_name or "unknown event") + ")"
    else:
        groups, reason = classify(files)
    g = groups.__contains__
    legs = [x for x in MATRIX_ALL if event_name != "pull_request" or not x["experimental"]]
    out = {
        "go": g("go"),
        "pins": g("workflows"),
        "lint": g("go") or g("workflows"),
        "examples": g("go") or g("examples"),
        "site": g("go") or g("docs"),
        "install": g("go") or g("installer"),
        "mutants": g("installer"),
        "action": g("go") or g("action"),
    }
    outputs = {k: "true" if v else "false" for k, v in out.items()}
    outputs["matrix"] = json.dumps({"include": legs}, separators=(",", ":"))
    outputs["full"] = "true" if reason else "false"
    return outputs, reason


_SHA = re.compile(r"\A[0-9a-f]{40,64}\Z")


def changed_files(base, head):
    """Files changed between merge-base(base, head) and head, renames reported as a
    delete plus an add; None on any failure."""
    if not (_SHA.match(base or "") and _SHA.match(head or "")):
        return None
    try:
        res = subprocess.run(
            ["git", "diff", "--name-only", "--no-renames", "-z", base + "..." + head, "--"],
            check=True,
            capture_output=True,
        )
    except (OSError, subprocess.CalledProcessError):
        return None
    return [p.decode("utf-8", "surrogateescape") for p in res.stdout.split(b"\0") if p]


def summary(outputs, reason, files):
    lines = ["### ci job selection", ""]
    if reason:
        lines.append("Running **everything** (fail open): " + reason + ".")
    else:
        lines.append("Selected from %d changed file(s)." % len(files or []))
    lines += ["", "| output | value |", "| --- | --- |"]
    for k in ("go", "pins", "lint", "examples", "site", "install", "mutants", "action", "full"):
        lines.append("| %s | %s |" % (k, outputs[k]))
    lines.append("")
    lines.append("Always run: docs, ci-ok. Test matrix legs: %d." % len(json.loads(outputs["matrix"])["include"]))
    return "\n".join(lines) + "\n"


def emit(path_env, text):
    target = os.environ.get(path_env)
    if target:
        with open(target, "a", encoding="utf-8") as fh:
            fh.write(text)
    else:
        sys.stdout.write(text)


def main():
    event = os.environ.get("EVENT_NAME", "")
    files = None
    if event == "pull_request":
        files = changed_files(os.environ.get("BASE_SHA", ""), os.environ.get("HEAD_SHA", ""))
    outputs, reason = build_outputs(event, files)
    emit("GITHUB_OUTPUT", "".join("%s=%s\n" % kv for kv in outputs.items()))
    emit("GITHUB_STEP_SUMMARY", summary(outputs, reason, files))
    return 0


if __name__ == "__main__":
    sys.exit(main())
