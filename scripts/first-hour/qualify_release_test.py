#!/usr/bin/env python3
"""Release qualification contracts; fixture results never count as live proof."""
import argparse
import contextlib
import hashlib
import io
import json
import subprocess
import sys
import tarfile
import tempfile
import unittest
from pathlib import Path
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parent))
import baseline
import qualify_release as qr

RELEASE = "v0.1.18-rc.349"
SOURCE = "a" * 40
CLI_DIGEST = "b" * 64
API = "https://api.example.test"


def archive_bytes(name="gregale", kind=tarfile.REGTYPE):
    output = io.BytesIO()
    with tarfile.open(fileobj=output, mode="w:gz") as bundle:
        member = tarfile.TarInfo(name)
        member.type = kind
        member.size = 7 if kind == tarfile.REGTYPE else 0
        member.linkname = "/private/target" if kind == tarfile.SYMTYPE else ""
        bundle.addfile(member, io.BytesIO(b"fixture"))
    return output.getvalue()


class ArtifactTests(unittest.TestCase):
    def test_checksum_requires_one_exact_archive_record(self):
        name = "gregale_test.tar.gz"
        self.assertEqual(qr.checksum_for("c" * 64 + " *" + name, name), "c" * 64)
        for text in ("", "c" * 64 + " other.tar.gz", "no-hash " + name,
                     ("c" * 64 + " " + name + "\n") * 2):
            with self.subTest(text=text), self.assertRaises(qr.QualificationError):
                qr.checksum_for(text, name)

    def test_archive_rejects_traversal_and_links(self):
        for name, kind in (("../gregale", tarfile.REGTYPE), ("/gregale", tarfile.REGTYPE),
                           ("gregale", tarfile.SYMTYPE), ("gregale", tarfile.LNKTYPE)):
            with self.subTest(name=name, kind=kind), tempfile.TemporaryDirectory() as temp:
                root = Path(temp)
                archive = root / "cli.tar.gz"
                archive.write_bytes(archive_bytes(name, kind))
                with self.assertRaises(qr.QualificationError):
                    qr.unpack(archive, root)
                self.assertFalse((root / "gregale").exists())

    def test_archive_with_an_extra_file_is_rejected(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            archive = root / "cli.tar.gz"
            with tarfile.open(archive, "w:gz") as bundle:
                for name in ("gregale", "extra"):
                    member = tarfile.TarInfo(name)
                    member.size = 1
                    bundle.addfile(member, io.BytesIO(b"x"))
            with self.assertRaises(qr.QualificationError):
                qr.unpack(archive, root)

    def test_checksum_is_checked_before_cli_can_execute(self):
        self.prepare_case(corrupt=True, error="archive_checksum_mismatch")

    def test_version_and_source_must_match_published_tag(self):
        self.prepare_case(version="v0.0.1", error="cli_version_mismatch")
        self.prepare_case(source="c" * 40, error="cli_source_mismatch")

    def test_unsupported_cli_cannot_start_a_journey(self):
        self.prepare_case(supports_verify=False, error="release_verify_unavailable")

    def test_published_archive_passes_without_rebuilding_or_installing(self):
        self.prepare_case()

    def prepare_case(self, corrupt=False, version=RELEASE, source=SOURCE, supports_verify=True, error=None):
        payload = archive_bytes()
        checksum = hashlib.sha256(payload).hexdigest()
        commands = []

        def github(resource, directory, name):
            if resource.startswith("releases/"):
                return {"tag_name": RELEASE, "draft": False, "published_at": "2026-11-17T12:11:51Z",
                        "assets": [{"name": n, "state": "uploaded"} for n in (f"gregale_{RELEASE[1:]}_linux_amd64.tar.gz", "CLI-SHA256SUMS")]}
            self.assertEqual(resource, f"git/ref/tags/{RELEASE}")
            return {"object": {"type": "commit", "sha": SOURCE}}

        def download(url, path, limit):
            if url.endswith("CLI-SHA256SUMS"):
                path.write_text(f"{checksum}  gregale_{RELEASE[1:]}_linux_amd64.tar.gz\n")
            else:
                path.write_bytes(b"corrupt" if corrupt else payload)

        def run(command, **kwargs):
            commands.append(command)
            self.assertNotIn("go", command)
            if "version" in command:
                return subprocess.CompletedProcess(command, 0, json.dumps({"version": version, "git_sha": source}))
            return subprocess.CompletedProcess(command, 0 if supports_verify else 1)

        with tempfile.TemporaryDirectory() as temp, mock.patch.object(qr, "host_target", return_value=("linux", "amd64")), \
                mock.patch.object(qr, "github_json", side_effect=github), mock.patch.object(qr, "download", side_effect=download), \
                mock.patch.object(qr.subprocess, "run", side_effect=run):
            evidence = {}
            if error:
                with self.assertRaises(qr.QualificationError) as raised:
                    qr.prepare_cli(RELEASE, Path(temp), evidence)
                self.assertEqual(raised.exception.code, error)
            else:
                binary = qr.prepare_cli(RELEASE, Path(temp), evidence)
                self.assertEqual(binary.read_bytes(), b"fixture")
                self.assertEqual(evidence["status"], "passed")
                self.assertEqual(evidence["cli_sha256"], hashlib.sha256(b"fixture").hexdigest())
                self.assertTrue(evidence["checksum_verified"])
            if corrupt:
                self.assertEqual(commands, [])

    def test_draft_and_missing_assets_are_blocked(self):
        for release in ({"tag_name": RELEASE, "draft": True},
                        {"tag_name": RELEASE, "draft": False, "published_at": "now", "assets": []}):
            with tempfile.TemporaryDirectory() as temp, mock.patch.object(qr, "github_json", return_value=release), \
                    mock.patch.object(qr, "host_target", return_value=("linux", "amd64")), \
                    mock.patch.object(qr.subprocess, "run") as run:
                with self.assertRaises(qr.QualificationError) as raised:
                    qr.prepare_cli(RELEASE, Path(temp), {})
                self.assertEqual(raised.exception.status, "blocked")
                run.assert_not_called()

    def test_annotated_tag_resolves_to_source_commit(self):
        with mock.patch.object(qr, "github_json", side_effect=[{"object": {"type": "tag", "sha": "d" * 40}},
                                                             {"object": {"type": "commit", "sha": SOURCE}}]):
            self.assertEqual(qr.source_commit(RELEASE, Path("/unused")), SOURCE)

    def test_invalid_tag_is_rejected_before_network_access(self):
        for tag in ("main", "v1.2.3/../../main", "v1.2.3\n", "v1.2.3;sh"):
            with self.subTest(tag=tag), mock.patch.object(qr, "github_json") as fetch:
                with self.assertRaises(qr.QualificationError):
                    qr.prepare_cli(tag, Path("/unused"), {})
                fetch.assert_not_called()


def events():
    result = [{"Test": "TestFirstHourCLI/" + name, "Action": "pass", "Elapsed": 1} for name in baseline.STEPS]
    result += [{"Test": "TestFirstHourCLI/repair_and_verify", "Action": "output", "Output": f"metric {name}=1\n"} for name in baseline.METRICS]
    return result


class JourneyTests(unittest.TestCase):
    def run_case(self, kind="deployed", checksum=CLI_DIGEST, api=API, code=0, cleanup=True, token_file=False):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            token = root / "acceptance.token" if token_file else None
            if token:
                token.write_text("SECRET")
            args = argparse.Namespace(out=root, cli_login=not token_file, token_file=token, api=API, plan="unknown")
            report = {"journey": {"status": "not_measured"}}

            def run(command, **kwargs):
                if token_file:
                    self.assertNotIn("--cli-login", command)
                    self.assertEqual(command[command.index("--token-file") + 1], str(token.resolve()))
                output = Path(command[command.index("--out") + 1])
                output.mkdir()
                (output / "metadata.json").write_text(json.dumps({"evidence_kind": kind, "cli_sha256": checksum,
                    "target": api, "harness_revision": SOURCE, "harness_worktree_dirty": False}))
                rows = events()
                if not cleanup:
                    rows += [{"Test": "TestFirstHourCLI/cleanup", "Action": "fail"}]
                (output / "go-test.json").write_text("\n".join(json.dumps(row) for row in rows))
                kwargs["stdout"].write("PRIVATE account@example.test token=SECRET\n")
                return subprocess.CompletedProcess(command, code)

            with mock.patch.object(qr.subprocess, "run", side_effect=run):
                try:
                    qr.run_journey(args, Path("/temporary/gregale"), {"cli_sha256": CLI_DIGEST}, report)
                    failure = None
                except qr.QualificationError as error:
                    failure = error.code
            public = "".join(p.read_text() for p in root.glob("baseline.*")) + json.dumps(report)
            for secret in ("PRIVATE", "SECRET", "account@example.test", "/temporary"):
                self.assertNotIn(secret, public)
            return failure, report

    def test_passing_journey_is_bound_to_verified_binary_and_target(self):
        failure, report = self.run_case()
        self.assertIsNone(failure)
        self.assertEqual(report["journey"]["status"], "passed")

    def test_ci_token_file_is_forwarded_without_falling_back_to_login(self):
        self.assertIsNone(self.run_case(token_file=True)[0])

    def test_empty_ci_token_cannot_start_the_journey(self):
        with tempfile.TemporaryDirectory() as temp:
            token = Path(temp) / "empty.token"
            token.write_text("")
            args = argparse.Namespace(api=API, cli_login=False, token_file=token)
            with mock.patch.object(qr.subprocess, "run") as run:
                with self.assertRaises(qr.QualificationError) as raised:
                    qr.run_journey(args, Path("/unused"), {}, {})
                self.assertEqual(raised.exception.status, "blocked")
                run.assert_not_called()

    def test_fixture_cleanup_failure_and_nonzero_exit_cannot_qualify(self):
        for kwargs in ({"kind": "fixture"}, {"cleanup": False}, {"code": 1}):
            with self.subTest(kwargs=kwargs):
                self.assertIsNotNone(self.run_case(**kwargs)[0])

    def test_other_binary_or_target_cannot_qualify(self):
        for kwargs in ({"checksum": "c" * 64}, {"api": "https://other.example.test"}):
            with self.subTest(kwargs=kwargs):
                self.assertEqual(self.run_case(**kwargs)[0], "journey_provenance_mismatch")

    def test_missing_authentication_never_falls_back_to_machine_login(self):
        args = argparse.Namespace(api=API, cli_login=False, token_file=None)
        with mock.patch.object(qr.subprocess, "run") as run:
            with self.assertRaises(qr.QualificationError) as raised:
                qr.run_journey(args, Path("/unused"), {}, {})
            self.assertEqual(raised.exception.status, "blocked")
            run.assert_not_called()

    def test_credential_bearing_target_is_not_recorded(self):
        for value in ("https://owner:SECRET@example.test", "https://example.test?token=SECRET"):
            report = {}
            with self.subTest(value=value), self.assertRaises(qr.QualificationError):
                qr.run_journey(argparse.Namespace(api=value), Path("/unused"), {}, report)
            self.assertNotIn("SECRET", json.dumps(report))


class RunnerTests(unittest.TestCase):
    def test_preflight_failure_still_records_sanitized_nonpassing_result(self):
        with tempfile.TemporaryDirectory() as temp:
            out = Path(temp) / "evidence"
            argv = ["qualify_release.py", "--release", RELEASE, "--out", str(out)]
            with mock.patch.object(sys, "argv", argv), mock.patch.object(qr, "prepare_cli", side_effect=RuntimeError("unexpected")):
                # Even an uncaught interruption/error writes a nonpassing report.
                with contextlib.redirect_stdout(io.StringIO()), self.assertRaises(RuntimeError):
                    qr.main()
            report = json.loads((out / "qualification.json").read_text())
            self.assertEqual(report["status"], "failed")
            self.assertNotIn("unexpected", (out / "qualification.md").read_text())

    def test_missing_configuration_is_blocked_with_artifact_proof_retained(self):
        with tempfile.TemporaryDirectory() as temp:
            out = Path(temp) / "evidence"

            def prepare(tag, directory, artifact):
                artifact.update(status="passed", cli_sha256=CLI_DIGEST)
                return directory / "gregale"

            argv = ["qualify_release.py", "--release", RELEASE, "--out", str(out)]
            with mock.patch.object(sys, "argv", argv), mock.patch.object(qr, "prepare_cli", side_effect=prepare), \
                    contextlib.redirect_stdout(io.StringIO()):
                self.assertEqual(qr.main(), 1)
            report = json.loads((out / "qualification.json").read_text())
            self.assertEqual(report["status"], "blocked")
            self.assertEqual(report["artifact"]["status"], "passed")
            self.assertEqual(report["journey"]["status"], "blocked")

    def test_reused_output_is_rejected_before_downloading(self):
        with tempfile.TemporaryDirectory() as temp, mock.patch.object(qr, "prepare_cli") as prepare:
            with mock.patch.object(sys, "argv", ["qualify_release.py", "--release", RELEASE, "--out", temp]), contextlib.redirect_stderr(io.StringIO()):
                with self.assertRaises(SystemExit):
                    qr.main()
            prepare.assert_not_called()


if __name__ == "__main__":
    unittest.main()
