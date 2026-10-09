"""Exercise publication refusal boundaries without making network requests."""
import hashlib
import importlib.util
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location(
    "publisher", Path(__file__).with_name("publish-data-api-prerelease.py"))
publisher = importlib.util.module_from_spec(spec)
spec.loader.exec_module(publisher)


class PublicationTests(unittest.TestCase):
    def exercise(self, *, wrong_source=False, published=False,
                 corrupt_download=False, mismatched_asset=False, wrong_tag=False):
        with tempfile.TemporaryDirectory() as work:
            root = Path(work) / "bundle"
            root.mkdir()
            records = []
            for os_name, arch in [("linux", "amd64"), ("linux", "arm64"),
                                  ("darwin", "amd64"), ("darwin", "arm64")]:
                name = f"gregale_{os_name}_{arch}.tar.gz"
                data = name.encode()
                (root / name).write_bytes(data)
                records.append({"file": name, "bytes": len(data),
                                "sha256": hashlib.sha256(data).hexdigest(),
                                "os": os_name, "arch": arch})
            data = b"SDK fixture"
            (root / "sdk.tgz").write_bytes(data)
            manifest = {"source": {"commit": "0" * 40 if wrong_source else publisher.SOURCE},
                        "cli": {"version": publisher.VERSION, "action_sha": publisher.ACTION,
                                "archives": records},
                        "sdk": {"file": "sdk.tgz", "name": "@gregale/data", "version": "0.1.0",
                                "bytes": len(data), "sha256": hashlib.sha256(data).hexdigest()}}
            (root / "data-api-bundle.json").write_text(json.dumps(manifest))
            (root / "DATA-API-SHA256SUMS").write_text("".join(
                hashlib.sha256(p.read_bytes()).hexdigest() + "  " + p.name + "\n"
                for p in sorted(root.iterdir())))
            notes = Path(work) / "notes.md"
            notes.write_text("Prerelease. Live canary pending. Existing license applies.")
            release = {"draft": not published, "prerelease": True, "tag_name": publisher.TAG,
                       "target_commitish": publisher.SOURCE, "assets": []}
            if mismatched_asset:
                release["assets"] = [{"name": "sdk.tgz", "digest": "sha256:wrong", "size": 11}]
            writes = []

            def fake_output(command, **kwargs):
                args = command[1:]
                if args[:2] == ["api", f"repos/{publisher.REPO}/releases/{publisher.RELEASE_ID}"]:
                    return json.dumps(release)
                if args[:2] == ["release", "upload"]:
                    file = Path(args[3])
                    writes.append("upload")
                    release["assets"].append({"name": file.name, "size": file.stat().st_size,
                        "state": "uploaded", "digest": "sha256:" + hashlib.sha256(file.read_bytes()).hexdigest()})
                    return ""
                if args[:2] == ["release", "download"]:
                    dest = Path(args[args.index("--dir") + 1])
                    dest.mkdir()
                    for file in root.iterdir():
                        content = b"corrupt" if corrupt_download and file.name == "sdk.tgz" else file.read_bytes()
                        (dest / file.name).write_bytes(content)
                    return ""
                if args[:3] == ["api", "--method", "PATCH"]:
                    payload = json.loads(Path(args[args.index("--input") + 1]).read_text())
                    self.assertEqual(payload, {"draft": False, "prerelease": True,
                                              "make_latest": "false", "body": notes.read_text()})
                    self.assertEqual(len(release["assets"]), 7)
                    writes.append("publish")
                    release["draft"] = False
                    return json.dumps({**release, "html_url": "https://github.com/poyrazK/faas/releases/tag/data-api/v0.1.0-rc.1"})
                raise AssertionError(command)

            def fake_tag(command, **kwargs):
                return subprocess.CompletedProcess(command, 0,
                    json.dumps({"object": {"type": "commit", "sha": "f" * 40 if wrong_tag else publisher.SOURCE}}), "")

            with patch.object(sys, "argv", ["publisher", "--directory", str(root), "--notes", str(notes)]), \
                 patch.object(publisher.subprocess, "check_output", fake_output), \
                 patch.object(publisher.subprocess, "run", fake_tag):
                try:
                    publisher.main()
                    failure = None
                except RuntimeError as error:
                    failure = str(error)
            return writes, failure

    def test_only_verified_downloads_allow_publication(self):
        writes, failure = self.exercise()
        self.assertIsNone(failure)
        self.assertEqual(writes, ["upload"] * 7 + ["publish"])

    def test_different_source_refuses_all_writes(self):
        writes, failure = self.exercise(wrong_source=True)
        self.assertEqual(writes, [])
        self.assertIn("source", failure)

    def test_published_release_cannot_be_changed(self):
        writes, failure = self.exercise(published=True)
        self.assertEqual(writes, [])
        self.assertIn("published", failure)

    def test_existing_mismatched_asset_is_never_replaced(self):
        writes, failure = self.exercise(mismatched_asset=True)
        self.assertEqual(writes, [])
        self.assertIn("replace", failure)

    def test_corrupt_download_keeps_release_a_draft(self):
        writes, failure = self.exercise(corrupt_download=True)
        self.assertNotIn("publish", writes)
        self.assertIn("Downloaded bytes", failure)

    def test_conflicting_tag_refuses_all_writes(self):
        writes, failure = self.exercise(wrong_tag=True)
        self.assertEqual(writes, [])
        self.assertIn("tag", failure)


if __name__ == "__main__":
    unittest.main()
