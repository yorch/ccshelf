"""Unit tests for check_pr_title.py. Run with:

    python3 -I -m unittest discover -s scripts -p 'test_*.py'
"""

import os
import subprocess
import sys
import unittest

import check_pr_title as cpt

HERE = os.path.dirname(os.path.abspath(__file__))
SCRIPT = os.path.join(HERE, "check_pr_title.py")


class CheckTitleTest(unittest.TestCase):
    def assertValid(self, title, max_len=cpt.DEFAULT_MAX_LEN):
        self.assertEqual(cpt.check_title(title, max_len), [], title)

    def assertInvalid(self, title, fragment=None, max_len=cpt.DEFAULT_MAX_LEN):
        problems = cpt.check_title(title, max_len)
        self.assertTrue(problems, "expected %r to be invalid" % (title,))
        if fragment is not None:
            self.assertTrue(any(fragment in p for p in problems), (title, problems))

    def test_valid_titles(self):
        for title in (
            "feat: add search",
            "fix(settings): reject env names matching ANTHROPIC_*",
            "feat(cli)!: rename the flag",
            "fix!: drop the legacy key",
            "docs(release): explain the flow",
            "chore(deps): bump golang.org/x/sys from 0.5.0 to 0.6.0",
            "chore(main): release 0.1.0",
            "revert: undo the cache change",
            "perf(cache): hash once",
            "build: pin goreleaser",
            "ci(release): wait for ci",
            "test: cover the empty profile",
            "refactor(trust): split accept",
            "fix: 404 pages keep their status",
            "chore: pin checksums for v0.1.0",
            "feat(a.b/c_d-e): scope characters",
        ):
            self.assertValid(title)

    def test_every_allowed_type(self):
        for kind in cpt.TYPES:
            self.assertValid(kind + ": do something")

    def test_unknown_type(self):
        self.assertInvalid("feature: add search", "not allowed")
        self.assertInvalid("style: tidy", "not allowed")
        self.assertInvalid("wip: stuff", "not allowed")

    def test_type_is_lowercase(self):
        self.assertInvalid("Feat: add search")
        self.assertInvalid("FIX: it")

    def test_missing_pieces(self):
        for title in (
            "add search",
            "feat add search",
            "feat:add search",
            "feat:  add search",
            "feat(): add search",
            "feat(Scope): add search",
            "feat(scope: add search",
            "feat!(scope): add search",
            "feat: ",
            "feat:",
            "",
        ):
            self.assertInvalid(title)

    def test_description_rules(self):
        self.assertInvalid("feat: Add search", "uppercase")
        self.assertInvalid("feat: add search.", "period")
        self.assertInvalid("feat: add search...", "period")
        self.assertValid("feat: add search!")
        self.assertValid("feat: 3d output")

    def test_whitespace(self):
        self.assertInvalid(" feat: add search", "whitespace")
        self.assertInvalid("feat: add search ", "whitespace")
        self.assertInvalid("feat: add\nsearch", "single line")
        self.assertInvalid("feat: add search\r", "single line")
        self.assertInvalid("feat: add\tsearch", "control")

    def test_length(self):
        base = "feat: "
        self.assertValid(base + "a" * (72 - len(base)))
        self.assertInvalid(base + "a" * (73 - len(base)), "limit is 72")
        long_title = "chore(deps): bump the github-actions group across 3 directories with 5 updates"
        self.assertInvalid(long_title, "limit")
        self.assertValid(long_title, max_len=120)

    def test_github_default_revert_title_is_rejected(self):
        self.assertInvalid('Revert "feat: add search"')

    def test_non_string(self):
        self.assertInvalid(None)
        self.assertInvalid(123)


class CommandTest(unittest.TestCase):
    def run_script(self, **env):
        clean = {k: v for k, v in os.environ.items() if k not in ("PR_TITLE", "MAX_LEN")}
        clean.update(env)
        return subprocess.run(
            [sys.executable, "-I", SCRIPT], env=clean, capture_output=True, text=True, check=False
        )

    def test_valid(self):
        result = self.run_script(PR_TITLE="fix(cli): handle an empty profile")
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_invalid_exit_status_and_escaped_output(self):
        result = self.run_script(PR_TITLE="::set-output name=x::y")
        self.assertEqual(result.returncode, 1)
        for line in (result.stdout + result.stderr).splitlines():
            self.assertFalse(line.startswith("::"), line)

    def test_control_characters_are_escaped_in_output(self):
        result = self.run_script(PR_TITLE="feat: ok\x1b[31m")
        self.assertEqual(result.returncode, 1)
        self.assertNotIn("\x1b", result.stderr)

    def test_missing_title(self):
        result = self.run_script()
        self.assertEqual(result.returncode, 2)

    def test_max_len(self):
        title = "chore(deps): bump the github-actions group across 3 directories with 5 updates"
        self.assertEqual(self.run_script(PR_TITLE=title).returncode, 1)
        self.assertEqual(self.run_script(PR_TITLE=title, MAX_LEN="120").returncode, 0)
        self.assertEqual(self.run_script(PR_TITLE=title, MAX_LEN="abc").returncode, 2)
        self.assertEqual(self.run_script(PR_TITLE=title, MAX_LEN="5").returncode, 2)


if __name__ == "__main__":
    unittest.main()
