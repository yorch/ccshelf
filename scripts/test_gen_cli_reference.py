#!/usr/bin/env python3
"""Regression tests for parsing grouped Cobra help (standard library only)."""
import unittest

from gen_cli_reference import parse_help


class HelpTests(unittest.TestCase):
    def test_grouped_commands_and_flags(self):
        h = parse_help('''Choose a profile.

Usage:
  ccshelf [command]

Examples:
  ccshelf ls

Profiles:
  ls          List profiles
  run         Start a profile

Discovery and diagnostics:
  search      Search the catalog

Org data repo maintenance:
  lint        Check the org data repo

Setup and utilities:
  init        Create config

Flags:
  -h, --help   help for ccshelf

Use "ccshelf [command] --help" for more information about a command.
''')
        self.assertEqual(h.desc, ["Choose a profile."])
        self.assertEqual(h.usage, ["ccshelf [command]"])
        self.assertEqual(h.examples, ["ccshelf ls"])
        self.assertEqual([name for name, _ in h.commands], ["ls", "run", "search", "lint", "init"])
        self.assertEqual(h.flags, [("-h", "--help", "", "help for ccshelf")])

    def test_ungrouped_help_and_global_flags(self):
        h = parse_help('''Catalog commands.

Usage:
  ccshelf catalog [command]

Available Commands:
  build       Build the catalog
  init        Initialize an org data repo

Flags:
  -h, --help   help for catalog

Global Flags:
      --json   machine-readable output
''')
        self.assertEqual([name for name, _ in h.commands], ["build", "init"])
        self.assertEqual(h.global_flags, [("", "--json", "", "machine-readable output")])

    def test_flag_with_an_optional_value(self):
        h = parse_help('''Start.

Usage:
  ccshelf run [flags]

Flags:
  -h, --help                      help for run
      --install-missing strings[=*]   install plugins. Without a value: all of them
      --yes                       answer yes
''')
        self.assertEqual(h.flags[1], ("", "--install-missing", "strings", "install plugins. Without a value: all of them"))
        self.assertEqual(h.flags[2][1], "--yes")

    def test_malformed_group_is_rejected(self):
        with self.assertRaises(ValueError):
            parse_help("Usage:\n  ccshelf [command]\nProfiles:\n  not a command row\n")


if __name__ == "__main__":
    unittest.main()
