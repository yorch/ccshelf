#!/usr/bin/env python3
"""Tests for scripts/ci_changes.py (standard library only)."""
import json
import os
import subprocess
import tempfile
import unittest

import ci_changes as c

ALL_TRUE = ("go", "pins", "lint", "examples", "site", "install", "mutants", "action")


def pr(*files):
    return c.build_outputs("pull_request", list(files))


def on(outputs):
    return {k for k in ALL_TRUE if outputs[k] == "true"}


class GroupTests(unittest.TestCase):
    def test_go(self):
        out, why = pr("internal/cli/run.go", "go.mod")
        self.assertIsNone(why)
        self.assertEqual(on(out), {"go", "lint", "examples", "site", "install", "action"})

    def test_installer(self):
        out, why = pr("scripts/install.sh")
        self.assertIsNone(why)
        self.assertEqual(on(out), {"install", "mutants"})

    def test_installer_tests_and_builder(self):
        for f in ("scripts/test_install.ps1", "scripts/test_install.sh", "scripts/make-e2e-release.sh", "scripts/install.ps1"):
            self.assertEqual(on(pr(f)[0]), {"install", "mutants"}, f)

    def test_action(self):
        out, why = pr("action/scripts/install.sh")
        self.assertIsNone(why)
        self.assertEqual(on(out), {"action", "pins", "lint"})

    def test_docs(self):
        for f in ("docs/DECISIONS.md", "site/index.html", "README.md", "CHANGELOG.md", ".release-please-manifest.json",
                  "scripts/build_docs.py", "scripts/check-links.sh", "docs/report.html", "scripts/ci_changes.py"):
            out, why = pr(f)
            self.assertIsNone(why, f)
            self.assertEqual(on(out), {"site"}, f)

    def test_release_pull_request_is_cheap(self):
        out, why = pr("CHANGELOG.md", ".release-please-manifest.json")
        self.assertIsNone(why)
        self.assertEqual(on(out), {"site"})
        self.assertEqual(out["full"], "false")

    def test_examples_also_runs_go_and_pins(self):
        out, why = pr("examples/org-data-repo/README.md")
        self.assertIsNone(why)
        self.assertEqual(on(out), {"go", "lint", "pins", "examples", "site", "install", "action"})

    def test_examples_script(self):
        self.assertEqual(on(pr("scripts/check-examples.sh")[0]), {"examples"})

    def test_pin_tooling(self):
        self.assertEqual(on(pr("scripts/check-pins.sh")[0]), {"pins", "lint"})

    def test_goreleaser(self):
        self.assertTrue({"go", "mutants", "action"} <= on(pr(".goreleaser.yaml")[0]))

    def test_text_conventions(self):
        for f in (".gitattributes", ".editorconfig"):
            self.assertTrue({"go", "site", "examples"} <= on(pr(f)[0]), f)

    def test_mixed(self):
        out, _ = pr("docs/README.md", "scripts/install.sh")
        self.assertEqual(on(out), {"site", "install", "mutants"})

    def test_pull_request_check_pr_title_script(self):
        self.assertIsNone(pr("scripts/check_pr_title.py")[1])


class FailOpenTests(unittest.TestCase):
    def assert_full(self, out, why):
        self.assertTrue(why)
        self.assertEqual(on(out), set(ALL_TRUE))
        self.assertEqual(out["full"], "true")

    def test_unknown_file(self):
        self.assert_full(*pr("docs/README.md", "brand-new-file.txt"))

    def test_release_config_is_unknown(self):
        self.assert_full(*pr("release-please-config.json"))

    def test_github_change(self):
        out, why = pr("docs/README.md", ".github/workflows/ci.yml")
        self.assert_full(out, why)
        self.assertIn(".github", why)
        self.assert_full(*pr(".github/CODEOWNERS"))

    def test_non_pull_request_events(self):
        for event in ("push", "merge_group", "workflow_dispatch", "", "schedule"):
            out, why = c.build_outputs(event, ["docs/README.md"])
            self.assert_full(out, why)
            out, why = c.build_outputs(event, None)
            self.assert_full(out, why)

    def test_empty_diff(self):
        self.assert_full(*pr())

    def test_diff_failure(self):
        self.assert_full(*c.build_outputs("pull_request", None))

    def test_github_lookalike_is_not_github(self):
        out, why = pr(".githubx/file")
        self.assertIn("no rule matches", why)


class MatrixTests(unittest.TestCase):
    def legs(self, event):
        out, _ = c.build_outputs(event, ["docs/README.md"])
        return json.loads(out["matrix"])["include"]

    def test_pull_request_has_no_experimental_legs(self):
        legs = self.legs("pull_request")
        self.assertEqual([x["os"] for x in legs],
                         ["ubuntu-latest", "macos-latest", "macos-15-intel", "windows-latest"])
        self.assertFalse(any(x["experimental"] for x in legs))
        self.assertTrue(all(x["race"] for x in legs))

    def test_other_events_have_all_six(self):
        for event in ("push", "merge_group", "workflow_dispatch"):
            legs = self.legs(event)
            self.assertEqual(len(legs), 6, event)
            arm = [x for x in legs if x["os"] == "windows-11-arm"][0]
            self.assertFalse(arm["race"])
            self.assertTrue(arm["experimental"])

    def test_matrix_is_compact_json(self):
        out, _ = pr("go.mod")
        self.assertNotIn(" ", out["matrix"])


class RulesTests(unittest.TestCase):
    def test_every_tracked_file_is_classified_or_github(self):
        root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
        try:
            res = subprocess.run(["git", "ls-files", "-z"], cwd=root, check=True, capture_output=True)
        except (OSError, subprocess.CalledProcessError):
            self.skipTest("not a git checkout")
        unknown = [p for p in res.stdout.decode().split("\0")
                   if p and not p.startswith(".github/") and not c.groups_for(p)]
        # Keeps the rules in step with the tree: a new file must be classified here.
        # Only the release config is deliberately left to fail open.
        self.assertEqual(unknown, ["release-please-config.json"])


class ChangedFilesTests(unittest.TestCase):
    def test_rejects_non_sha(self):
        self.assertIsNone(c.changed_files("--output=x", "abc"))
        self.assertIsNone(c.changed_files("", ""))
        self.assertIsNone(c.changed_files(None, None))

    def test_real_repository(self):
        with tempfile.TemporaryDirectory() as d:
            def git(*a):
                return subprocess.run(["git", "-C", d, *a], check=True, capture_output=True, text=True).stdout.strip()
            git("init", "-q")
            git("config", "user.email", "t@example.test")
            git("config", "user.name", "t")
            git("config", "commit.gpgsign", "false")
            with open(os.path.join(d, "a.md"), "w") as fh:
                fh.write("a\n")
            git("add", ".")
            git("commit", "-qm", "one")
            base = git("rev-parse", "HEAD")
            os.rename(os.path.join(d, "a.md"), os.path.join(d, "b.go"))
            git("add", "-A")
            git("commit", "-qm", "two")
            head = git("rev-parse", "HEAD")
            old = os.getcwd()
            os.chdir(d)
            try:
                files = c.changed_files(base, head)
            finally:
                os.chdir(old)
            self.assertEqual(sorted(files), ["a.md", "b.go"])


if __name__ == "__main__":
    unittest.main()
