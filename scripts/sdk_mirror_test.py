#!/usr/bin/env python3
"""Tests for scripts/sdk_mirror.py."""
import importlib.util
import tempfile
import unittest
from pathlib import Path

spec = importlib.util.spec_from_file_location("sdk_mirror", Path(__file__).with_name("sdk_mirror.py"))
sdk_mirror = importlib.util.module_from_spec(spec)
spec.loader.exec_module(sdk_mirror)

PAGES = {
    "docs/sdks.md": "https://gregale.dev/docs/sdks",
    "": "https://github.com/acme/platform/blob/main/",
}
JS = [
    {"from": "sdk/node", "to": "packages/sdk-node"},
    {"from": "sdk/node/LICENSE", "to": "LICENSE"},
]
GO = [{"from": "sdk/go", "to": ""}]


class RewriteTargetTest(unittest.TestCase):
    def rewrite(self, target, source="sdk/go/README.md", dest="README.md", mappings=GO):
        return sdk_mirror.rewrite_target(target, source, dest, mappings, PAGES)

    def test_cases(self):
        cases = [
            # (target, want)
            ("https://example.com/x", "https://example.com/x"),
            ("mailto:security@gregale.dev", "mailto:security@gregale.dev"),
            ("#install", "#install"),
            ("/absolute", "/absolute"),
            # Inside the mapped directory: stays relative.
            ("preview/README.md", "preview/README.md"),
            ("./auth/", "auth/"),
            # A published docs page goes to the docs site, keeping the anchor.
            ("../../docs/sdks.md#go", "https://gregale.dev/docs/sdks#go"),
            # Anything else in the repository goes to the upstream repository.
            ("../../docs/adr/1-x.md", "https://github.com/acme/platform/blob/main/docs/adr/1-x.md"),
            ("../service-binding-contracts/README.md",
             "https://github.com/acme/platform/blob/main/sdk/service-binding-contracts/README.md"),
            # Escapes the repository: left alone for the check to report.
            ("../../../outside.md", "../../../outside.md"),
        ]
        for target, want in cases:
            with self.subTest(target=target):
                self.assertEqual(self.rewrite(target), want)

    def test_subdirectory_source(self):
        got = self.rewrite("../README.md", source="sdk/go/preview/README.md", dest="preview/README.md")
        self.assertEqual(got, "../README.md")

    def test_longest_mapping_wins_across_mappings(self):
        got = self.rewrite("LICENSE", source="sdk/node/README.md", dest="packages/sdk-node/README.md", mappings=JS)
        self.assertEqual(got, "../../LICENSE")
        got = self.rewrite("src/index.ts", source="sdk/node/README.md", dest="packages/sdk-node/README.md", mappings=JS)
        self.assertEqual(got, "src/index.ts")


class RewriteMarkdownTest(unittest.TestCase):
    def test_fenced_code_and_reference_links(self):
        text = (
            "See [docs](../../docs/sdks.md) and ![img](../../docs/a.png \"t\").\n"
            "```md\n[kept](../../docs/sdks.md)\n```\n"
            "[ref]: ../../docs/x.md\n"
        )
        got = sdk_mirror.rewrite_markdown(text, "sdk/go/README.md", "README.md", GO, PAGES)
        self.assertEqual(
            got,
            "See [docs](https://gregale.dev/docs/sdks) and "
            "![img](https://github.com/acme/platform/blob/main/docs/a.png \"t\").\n"
            "```md\n[kept](../../docs/sdks.md)\n```\n"
            "[ref]: https://github.com/acme/platform/blob/main/docs/x.md\n",
        )


class CheckTreeTest(unittest.TestCase):
    def test_reports_broken_links_and_missing_root_files(self):
        with tempfile.TemporaryDirectory() as tmp:
            out = Path(tmp)
            (out / "README.md").write_text("[ok](a.md) [gone](missing.md) [up](../x.md)\n")
            (out / "a.md").write_text("fine\n")
            files = {"README.md": "sdk/x/README.md", "a.md": "sdk/x/a.md"}
            problems = sdk_mirror.check_tree("gregale-dev", {"name": "gregale-x"}, out, files)
        self.assertEqual(len(problems), 3, problems)
        self.assertIn("no LICENSE", problems[0])
        self.assertIn("missing.md", problems[1])
        self.assertIn("../x.md", problems[2])


if __name__ == "__main__":
    unittest.main()
