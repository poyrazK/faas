import copy
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


SCRIPT = Path(__file__).resolve().with_name("commit_native_evidence.py")
spec = importlib.util.spec_from_file_location("commit_native_evidence", SCRIPT)
evidence = importlib.util.module_from_spec(spec)
spec.loader.exec_module(evidence)
REVISION = "a" * 40


def fixture(directory):
    private = directory / "private"
    private.mkdir(parents=True)
    identities = {key: "b" * 64 for key in evidence.IDENTITIES}
    identities["binaries"] = {name: "c" * 64 for name in evidence.BINARIES}
    fingerprint = {"source_revision": REVISION, "source_archive_sha256": "d" * 64, "identities": identities}
    for name in ("identities-before.json", "identities-after.json"):
        evidence.write_json(private / name, fingerprint)
    state = {"reason": "completed", "native_started": True, "host_class": "bare-metal",
             "leak_before": "pass", "leak_after": "pass", "postgres_cleanup": "pass",
             "process_cleanup": "pass", "binaries_cleanup": "pass", "work_cleanup": "pass", "gate_exit": 0}
    evidence.write_json(private / "state.json", state)
    events = []
    for name in evidence.REQUIRED:
        events.extend([{"Action": "run", "Package": evidence.PACKAGE, "Test": name},
                       {"Action": "pass", "Package": evidence.PACKAGE, "Test": name}])
    events.append({"Action": "output", "Output": "postgres://private:password@host/private"})
    (private / "test-events.jsonl").write_text("".join(json.dumps(item) + "\n" for item in events))
    return state, fingerprint, events


class CommitNativeEvidenceTest(unittest.TestCase):
    def test_host_configuration_is_data_with_only_expected_fields(self):
        valid = ("# local fixtures\nFAAS_TEST_KERNEL=/srv/kernel\nFAAS_TEST_FC_VERSION=1.17.0\n"
                 "FAAS_BUILDER_BASE_PATH=/srv/base\nFAAS_COMMIT_TLS_PG_BIN_DIR=/usr/lib/pg/bin\n"
                 "FAAS_COMMIT_TLS_PG_USER=postgres\nFAAS_PUBLIC_IFACE=eth0\n")
        self.assertEqual(evidence.parse_config(valid)["FAAS_TEST_KERNEL"], "/srv/kernel")
        for invalid in (
            valid + "DATABASE_URL=postgres://production\n",
            valid + "FAAS_TEST_KERNEL=/other\n",
            valid.replace("FAAS_COMMIT_TLS_PG_USER=postgres\n", ""),
            valid.replace("/srv/kernel", "$(touch_marker)"),
            valid.replace("/srv/kernel", "/srv/kernel;touch_marker"),
            valid.replace("/srv/kernel", "'/srv/kernel'"),
        ):
            with self.subTest(invalid=invalid), self.assertRaises(ValueError):
                evidence.parse_config(invalid)

    def test_valid_evidence_is_sanitized_and_scope_is_explicit(self):
        with tempfile.TemporaryDirectory() as temp:
            directory = Path(temp)
            fixture(directory)
            summary = evidence.verify(directory, REVISION, "success")
            self.assertEqual(summary["result"], "passed")
            self.assertTrue(summary["native_kvm_passed"])
            self.assertFalse(summary["production_qualified"])
            self.assertEqual(summary["host_class"], "bare-metal")
            self.assertEqual(set(summary["tests"]), set(evidence.REQUIRED))
            self.assertEqual(len(summary["test_events_sha256"]), 64)
            self.assertNotIn("password", json.dumps(summary))
            self.assertNotIn("postgres://", json.dumps(summary))

    def test_nested_kvm_is_reported_without_claiming_production(self):
        with tempfile.TemporaryDirectory() as temp:
            directory = Path(temp)
            state, _, _ = fixture(directory)
            state["host_class"] = "nested-kvm"
            evidence.write_json(directory / "private" / "state.json", state)
            summary = evidence.verify(directory, REVISION, "success")
            self.assertTrue(summary["native_kvm_passed"])
            self.assertEqual(summary["host_class"], "nested-kvm")
            self.assertFalse(summary["production_qualified"])

    def test_incomplete_or_changed_evidence_never_passes(self):
        for change in ("source", "kernel", "binary", "leak_before", "leak_after", "postgres_cleanup", "process_cleanup", "binaries_cleanup", "work_cleanup", "gate_exit", "runner_outcome"):
            with self.subTest(change=change), tempfile.TemporaryDirectory() as temp:
                directory = Path(temp)
                state, fingerprint, _ = fixture(directory)
                private = directory / "private"
                outcome = "success"
                if change in ("source", "kernel", "binary"):
                    after = copy.deepcopy(fingerprint)
                    if change == "source":
                        after["source_revision"] = "e" * 40
                    elif change == "kernel":
                        after["identities"]["kernel"] = "e" * 64
                    else:
                        after["identities"]["binaries"]["vmmd"] = "e" * 64
                    evidence.write_json(private / "identities-after.json", after)
                elif change == "runner_outcome":
                    outcome = "failure"
                else:
                    state[change] = 1 if change == "gate_exit" else "fail"
                    evidence.write_json(private / "state.json", state)
                self.assertFalse(evidence.verify(directory, REVISION, outcome)["native_kvm_passed"])

    def test_skips_failures_missing_and_duplicate_tests_are_rejected(self):
        for change in ("skip", "fail", "missing", "duplicate", "wrong_package", "malformed"):
            with self.subTest(change=change), tempfile.TemporaryDirectory() as temp:
                directory = Path(temp)
                _, _, events = fixture(directory)
                if change in ("skip", "fail"):
                    events.append({"Action": change, "Test": "private-name", "Package": evidence.PACKAGE})
                elif change == "missing":
                    events = [e for e in events if e.get("Test") != evidence.REQUIRED[1]]
                elif change == "duplicate":
                    events.append({"Action": "pass", "Test": evidence.REQUIRED[1], "Package": evidence.PACKAGE})
                elif change == "wrong_package":
                    events[0]["Package"] = "unrelated"
                path = directory / "private" / "test-events.jsonl"
                path.write_text("not-json\n" if change == "malformed" else "".join(json.dumps(e) + "\n" for e in events))
                result = evidence.verify(directory, REVISION, "success")
                self.assertFalse(result["native_kvm_passed"])
                self.assertNotIn("private-name", json.dumps(result))

    def test_process_inventory_rejects_detached_test_daemons(self):
        with tempfile.TemporaryDirectory() as temp:
            base = Path(temp)
            binaries, proc = base / "bin", base / "proc"
            binaries.mkdir()
            proc.mkdir()
            (proc / "123").mkdir()
            (proc / "124").mkdir()
            (proc / "123" / "exe").symlink_to("/usr/bin/unrelated")
            evidence.check_processes(proc, binaries)
            (proc / "124" / "exe").symlink_to(str(binaries.resolve() / "apid") + " (deleted)")
            with self.assertRaises(ValueError):
                evidence.check_processes(proc, binaries)
            with self.assertRaises(OSError):
                evidence.check_processes(base / "missing-proc", binaries)

    def test_fingerprint_requires_clean_source_and_records_all_inputs(self):
        with tempfile.TemporaryDirectory() as temp:
            base = Path(temp)
            repo, binaries = base / "repo", base / "bin"
            repo.mkdir()
            binaries.mkdir()
            subprocess.run(["git", "init", "-q", str(repo)], check=True)
            subprocess.run(["git", "-C", str(repo), "config", "user.email", "test@example.invalid"], check=True)
            subprocess.run(["git", "-C", str(repo), "config", "user.name", "Test"], check=True)
            (repo / "source").write_text("source")
            (repo / ".gitignore").write_text("ignored\n")
            subprocess.run(["git", "-C", str(repo), "add", "source", ".gitignore"], check=True)
            subprocess.run(["git", "-C", str(repo), "-c", "core.hooksPath=/dev/null", "commit", "-qm", "fixture"], check=True)
            revision = subprocess.check_output(["git", "-C", str(repo), "rev-parse", "HEAD"], text=True).strip()
            for name in evidence.BINARIES:
                (binaries / name).write_text(name)
            paths = {}
            for name in evidence.IDENTITIES:
                paths[name] = base / name
                paths[name].write_text(name)
            before = evidence.fingerprint(repo, revision, binaries, paths)
            self.assertTrue(evidence.valid_fingerprint(before, revision))
            paths["kernel"].write_text("changed kernel")
            after = evidence.fingerprint(repo, revision, binaries, paths)
            self.assertNotEqual(before["identities"]["kernel"], after["identities"]["kernel"])
            (repo / "source").write_text("changed")
            with self.assertRaises(ValueError):
                evidence.fingerprint(repo, revision, binaries, paths)
            (repo / "source").write_text("source")
            (repo / "ignored").write_text("not part of source")
            with self.assertRaises(ValueError):
                evidence.fingerprint(repo, revision, binaries, paths)

    def test_preflight_rejects_unprivileged_host_without_starting(self):
        runner = SCRIPT.with_name("run-commit-native-acceptance.sh")
        with tempfile.TemporaryDirectory() as temp:
            base = Path(temp)
            fake_id = base / "id"
            fake_id.write_text("#!/bin/sh\necho 1000\n")
            fake_id.chmod(0o755)
            env = dict(os.environ, PATH=str(base) + os.pathsep + os.environ["PATH"])
            directory = base / "evidence"
            result = subprocess.run(["bash", str(runner), REVISION, str(directory)], env=env, capture_output=True, text=True)
            self.assertNotEqual(result.returncode, 0)
            state = json.loads((directory / "private" / "state.json").read_text())
            self.assertFalse(state["native_started"])
            self.assertEqual(state["reason"], "unsupported_host")
            self.assertEqual(state["leak_before"], "not_run")
            self.assertEqual(evidence.verify(directory, REVISION, "failure")["result"], "blocked")

    def test_cli_publishes_blocked_report_when_evidence_is_missing(self):
        with tempfile.TemporaryDirectory() as temp:
            directory = Path(temp) / "evidence"
            result = subprocess.run([sys.executable, str(SCRIPT), "verdict", "--directory", str(directory),
                                     "--expected", REVISION, "--outcome", "failure"], capture_output=True, text=True)
            self.assertNotEqual(result.returncode, 0)
            report = json.loads((directory / "public" / "summary.json").read_text())
            self.assertEqual(report["result"], "blocked")
            self.assertFalse(report["native_kvm_passed"])

    def test_cli_publishes_only_sanitized_success_evidence(self):
        with tempfile.TemporaryDirectory() as temp:
            directory = Path(temp) / "evidence"
            fixture(directory)
            (directory / "private").chmod(0o700)
            result = subprocess.run([sys.executable, str(SCRIPT), "verdict", "--directory", str(directory),
                                     "--expected", REVISION, "--outcome", "success"], capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            raw = (directory / "public" / "summary.json").read_text()
            self.assertEqual(json.loads(raw)["result"], "passed")
            self.assertNotIn("password", raw)
            self.assertEqual((directory / "private").stat().st_mode & 0o777, 0o700)
            self.assertEqual((directory / "public" / "summary.json").stat().st_mode & 0o777, 0o644)

    def test_symlinked_evidence_and_public_output_are_not_followed(self):
        with tempfile.TemporaryDirectory() as temp:
            directory = Path(temp) / "evidence"
            fixture(directory)
            private = directory / "private"
            original = private / "identities-before.json"
            target = Path(temp) / "outside.json"
            target.write_text(original.read_text())
            original.unlink()
            original.symlink_to(target)
            self.assertFalse(evidence.verify(directory, REVISION, "success")["native_kvm_passed"])
            public = directory / "public"
            public.mkdir()
            (public / "summary.json").symlink_to(target)
            before = target.read_text()
            result = subprocess.run([sys.executable, str(SCRIPT), "verdict", "--directory", str(directory),
                                     "--expected", REVISION, "--outcome", "failure"], capture_output=True, text=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertEqual(target.read_text(), before)


if __name__ == "__main__":
    unittest.main()
