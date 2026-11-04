#!/usr/bin/env python3
"""Tests for the cli-channel pointer ordering (ADR-20261025092617)."""

import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import cli_channel  # noqa: E402


class NewerTest(unittest.TestCase):
    def test_semver_precedence(self):
        for candidate, current in [
            ("v0.1.18-rc.292", "v0.1.18-rc.291"),
            ("v0.1.18-rc.292", "v0.1.18-rc.99"),  # numeric, not lexical
            ("v0.1.18", "v0.1.18-rc.292"),  # a release beats its prereleases
            ("v0.1.19-rc.1", "v0.1.18"),
            ("v1.0.0", "v0.99.99"),
            ("v1.0.0-rc.1.1", "v1.0.0-rc.1"),  # more identifiers, same prefix
            ("v1.0.0-rc.x", "v1.0.0-rc.9"),  # alphanumeric above numeric
        ]:
            with self.subTest(candidate=candidate, current=current):
                self.assertTrue(cli_channel.newer(candidate, current))
                self.assertFalse(cli_channel.newer(current, candidate))

    def test_only_forward(self):
        # A re-run of the same tag rewrites nothing.
        self.assertFalse(cli_channel.newer("v0.1.18-rc.292", "v0.1.18-rc.292"))

    def test_missing_or_unreadable_pointer_is_replaced(self):
        self.assertTrue(cli_channel.newer("v0.1.18-rc.1", ""))
        self.assertTrue(cli_channel.newer("v0.1.18-rc.1", "<html>not found</html>"))

    def test_candidate_must_be_a_release_tag(self):
        for tag in ["cli-channel", "0.1.18", "v0.1.18-rc.1;touch x", "v01.1.1"]:
            with self.subTest(tag=tag):
                with self.assertRaises(ValueError):
                    cli_channel.newer(tag, "")


class CommandTest(unittest.TestCase):
    def test_exit_codes(self):
        self.assertEqual(cli_channel.main(["newer", "v0.1.18-rc.2", "v0.1.18-rc.1"]), 0)
        self.assertEqual(cli_channel.main(["newer", "v0.1.18-rc.1", "v0.1.18-rc.2"]), 1)
        self.assertEqual(cli_channel.main(["newer", "v0.1.18-rc.1"]), 0)
        self.assertEqual(cli_channel.main(["stable", "v1.0.0"]), 0)
        self.assertEqual(cli_channel.main(["stable", "v1.0.0-rc.1"]), 1)
        self.assertEqual(cli_channel.main(["newer", "main"]), 2)
        self.assertEqual(cli_channel.main([]), 2)


if __name__ == "__main__":
    unittest.main()
