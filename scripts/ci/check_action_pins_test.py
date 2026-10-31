#!/usr/bin/env python3
"""Tests for check_action_pins.py."""
from __future__ import annotations

import pathlib
import sys
import tempfile
import unittest

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))

import check_action_pins  # noqa: E402

SHA = "3d3c42e5aac5ba805825da76410c181273ba90b1"


class ActionPinTest(unittest.TestCase):
    def check(self, workflow: str) -> list[str]:
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            (root / ".github/workflows").mkdir(parents=True)
            (root / ".github/workflows/w.yml").write_text(workflow)
            return check_action_pins.unpinned(root)

    def test_pinned_local_and_own_actions_pass(self):
        self.assertEqual(self.check(f"""jobs:
  a:
    steps:
      - uses: actions/checkout@{SHA} # v7
      - uses: actions/cache/restore@{SHA} # v4
      - uses: ./.github/actions/deploy
      - uses: poyrazK/faas/.github/actions/deploy@v0
      - name: x
        uses: "actions/setup-go@{SHA}"
"""), [])

    def test_tags_and_branches_fail(self):
        found = self.check("""jobs:
  a:
    steps:
      - uses: actions/checkout@v7
      - uses: google-github-actions/auth@main
        with: {}
      - uses: actions/setup-go@b7ad1dad # short sha
""")
        self.assertEqual(len(found), 3)
        self.assertIn("w.yml:4: actions/checkout@v7", found[0])

    def test_repository_is_clean(self):
        self.assertEqual(check_action_pins.unpinned(check_action_pins.ROOT), [])


if __name__ == "__main__":
    unittest.main()
