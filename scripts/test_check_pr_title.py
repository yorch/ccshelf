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

    def test_double_bang_and_scope_with_space(self):
        self.assertInvalid("feat!!: add search")
        self.assertInvalid("feat(cli)!!: add search")
        self.assertInvalid("feat(a b): add search")
        self.assertInvalid("feat( cli): add search")

    def test_del_and_c1_controls(self):
        self.assertInvalid("feat: add\x7fsearch", "control")
        self.assertInvalid("feat: add\x85search")
        self.assertInvalid("feat: add\x9bsearch")

    def test_invisible_unicode(self):
        for name, char in (
            ("line separator", "\u2028"),
            ("paragraph separator", "\u2029"),
            ("next line", "\u0085"),
            ("right-to-left override", "\u202e"),
            ("zero width space", "\u200b"),
            ("zero width joiner", "\u200d"),
            ("byte order mark", "\ufeff"),
            ("soft hyphen", "\u00ad"),
            ("private use", "\ue000"),
            ("word joiner", "\u2060"),
        ):
            self.assertInvalid("feat: add" + char + "search", "U+")
            self.assertInvalid("feat: add search" + char, None)
            self.assertInvalid(char + "feat: add search", None)
        self.assertValid("feat: add caf\u00e9 support")
        self.assertValid("feat: add \u65e5\u672c\u8a9e names")

    def test_release_title_is_reserved_for_the_bot(self):
        title = "chore(main): release 0.1.0"
        self.assertEqual(cpt.check_title(title, author=cpt.RELEASE_BOT), [])
        for author in (None, "", "octocat", "dependabot[bot]", "github-actions", "Github-Actions[bot]"):
            problems = cpt.check_title(title, author=author)
            self.assertTrue(any("reserved" in p for p in problems), (author, problems))
        self.assertTrue(cpt.check_title("chore(main): release the cache", author="octocat"))
        # Look-alikes that are ordinary titles stay valid for everyone.
        self.assertEqual(cpt.check_title("chore: release notes wording", author="octocat"), [])
        self.assertEqual(cpt.check_title("chore(main): relax the cache rule", author="octocat"), [])

    def test_non_string(self):
        self.assertInvalid(None)
        self.assertInvalid(123)


class CheckBodyTest(unittest.TestCase):
    def assertRejected(self, body, fragment, author="octocat"):
        problems = cpt.check_body(body, author)
        self.assertTrue(any(fragment in p for p in problems), (body, problems))

    def test_ordinary_bodies_pass(self):
        for body in (
            "",
            "Adds a search command.\n\nCloses #12",
            "feat: this line is only prose here\nBREAKING CHANGE: not handled by this check",
            "We mention release-as in a sentence, and Release-As: only mid-line is prose.",
            "Bumps x from 1 to 2.\n\n- abc123 chore: tidy",
        ):
            self.assertEqual(cpt.check_body(body, "octocat"), [], body)

    def test_override_markers(self):
        self.assertRejected("BEGIN_COMMIT_OVERRIDE\nfeat: x\nEND_COMMIT_OVERRIDE", "BEGIN_COMMIT_OVERRIDE")
        self.assertRejected("text\nBEGIN_NESTED_COMMIT\nfeat: x\nEND_NESTED_COMMIT", "BEGIN_NESTED_COMMIT")
        self.assertRejected("begin_commit_override", "BEGIN_COMMIT_OVERRIDE")
        self.assertRejected("x BEGIN_COMMIT_OVERRIDE", "BEGIN_COMMIT_OVERRIDE")

    def test_release_as_footer(self):
        for body in (
            "Release-As: 1.0.0",
            "text\n\nRelease-As: 1.0.0",
            "text\r\nrelease-as: 2.0.0",
            "text\n  Release-As:1.0.0",
            "text\n> Release-As: 1.0.0",
            "text\n- Release-As: 1.0.0",
            "text\u2028Release-As: 1.0.0",
            "text\x85Release-As: 1.0.0",
            "text\nRELEASE-AS : 1.0.0",
        ):
            self.assertRejected(body, "Release-As")

    def test_bot_is_exempt(self):
        body = "BEGIN_COMMIT_OVERRIDE\nRelease-As: 1.0.0"
        self.assertEqual(cpt.check_body(body, cpt.RELEASE_BOT), [])
        self.assertTrue(cpt.check_body(body, "github-actions"))
        self.assertTrue(cpt.check_body(body, None))
        self.assertTrue(cpt.check_body(body, ""))

    def test_non_string(self):
        self.assertTrue(cpt.check_body(None, "octocat"))
        self.assertEqual(cpt.check_body(None, cpt.RELEASE_BOT), [])


class CommandTest(unittest.TestCase):
    def run_script(self, **env):
        clean = {
            k: v
            for k, v in os.environ.items()
            if k not in ("PR_TITLE", "MAX_LEN", "PR_BODY", "PR_AUTHOR")
        }
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

    def test_body_and_author(self):
        title = "fix(cli): handle an empty profile"
        self.assertEqual(self.run_script(PR_TITLE=title, PR_BODY="Plain text.").returncode, 0)
        self.assertEqual(self.run_script(PR_TITLE=title, PR_BODY="").returncode, 0)
        result = self.run_script(PR_TITLE=title, PR_BODY="Fixes it.\n\nRelease-As: 1.0.0\n")
        self.assertEqual(result.returncode, 1)
        self.assertIn("Release-As", result.stderr)
        self.assertNotIn("1.0.0", result.stderr)
        result = self.run_script(PR_TITLE=title, PR_BODY="BEGIN_COMMIT_OVERRIDE", PR_AUTHOR="octocat")
        self.assertEqual(result.returncode, 1)
        # With no PR_BODY the body is not checked (push and merge_group events).
        self.assertEqual(self.run_script(PR_TITLE=title).returncode, 0)

    def test_release_title_needs_the_bot(self):
        title = "chore(main): release 0.2.0"
        self.assertEqual(self.run_script(PR_TITLE=title).returncode, 1)
        self.assertEqual(self.run_script(PR_TITLE=title, PR_AUTHOR="octocat").returncode, 1)
        self.assertEqual(
            self.run_script(PR_TITLE=title, PR_AUTHOR="github-actions[bot]").returncode, 0
        )

    def test_missing_title(self):
        result = self.run_script()
        self.assertEqual(result.returncode, 2)

    def test_max_len(self):
        title = "chore(deps): bump the github-actions group across 3 directories with 5 updates"
        self.assertEqual(self.run_script(PR_TITLE=title).returncode, 1)
        self.assertEqual(self.run_script(PR_TITLE=title, MAX_LEN="120").returncode, 0)
        self.assertEqual(self.run_script(PR_TITLE=title, MAX_LEN="abc").returncode, 2)
        self.assertEqual(self.run_script(PR_TITLE=title, MAX_LEN="5").returncode, 2)
        # Both ends of the allowed range 20..200.
        self.assertEqual(self.run_script(PR_TITLE="feat: a", MAX_LEN="19").returncode, 2)
        self.assertEqual(self.run_script(PR_TITLE="feat: a", MAX_LEN="20").returncode, 0)
        self.assertEqual(self.run_script(PR_TITLE="feat: a", MAX_LEN="200").returncode, 0)
        self.assertEqual(self.run_script(PR_TITLE="feat: a", MAX_LEN="201").returncode, 2)


if __name__ == "__main__":
    unittest.main()
