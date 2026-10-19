#!/usr/bin/env python3
"""Adversarial evidence and cleanup tests; these grant no native qualification."""
import contextlib
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest import mock

SPEC = importlib.util.spec_from_file_location("repair_native", Path(__file__).with_name("run-data-repair-native.py"))
RUNNER = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(RUNNER)


def events():
    result = [dict(Action="start", Package=RUNNER.PACKAGE)]
    for name in RUNNER.TESTS:
        result.append(dict(Action="run", Package=RUNNER.PACKAGE, Test=name))
        if name == RUNNER.CRASH:
            for phase in RUNNER.PHASES:
                output = f"    === RUN   {name}/{phase}\n    --- PASS: {name}/{phase} (0.01s)\n"
                result.append(dict(Action="output", Package=RUNNER.PACKAGE, Test=name, Output=output))
        result.append(dict(Action="pass", Package=RUNNER.PACKAGE, Test=name))
    result.append(dict(Action="pass", Package=RUNNER.PACKAGE))
    return result


def log_text(items):
    return "make: fixture command\n" + "".join(json.dumps(item) + "\n" for item in items)


class NativeRepairVerdictTest(unittest.TestCase):
    def verify(self, items):
        with tempfile.TemporaryDirectory() as name:
            path = Path(name) / "native.log"
            path.write_text(log_text(items))
            return RUNNER.verify_events(path)

    def test_complete_execution(self):
        result = self.verify(events())
        self.assertEqual(set(result["tests"]), set(RUNNER.TESTS))
        self.assertEqual(set(result["crash_phases"]), set(RUNNER.PHASES))

    def test_missing_filtered_and_skipped_tests_fail(self):
        for name in RUNNER.TESTS:
            with self.subTest(test=name, condition="missing"):
                with self.assertRaises(ValueError):
                    self.verify([item for item in events() if item.get("Test") != name])
            changed = events()
            next(item for item in changed if item.get("Test") == name and item["Action"] == "pass")["Action"] = "skip"
            with self.subTest(test=name, condition="skipped"):
                with self.assertRaises(ValueError):
                    self.verify(changed)

    def test_nested_crash_proof_is_required(self):
        for phase in RUNNER.PHASES:
            for condition in ("missing", "skip", "fail", "pass_without_run", "duplicate"):
                items = events()
                index = next(i for i, item in enumerate(items) if f"/{phase} " in item.get("Output", ""))
                item = items[index]
                if condition == "missing":
                    del items[index]
                elif condition in ("skip", "fail"):
                    item["Output"] = item["Output"].replace("--- PASS:", f"--- {condition.upper()}:")
                elif condition == "pass_without_run":
                    item["Output"] = item["Output"].splitlines(keepends=True)[1]
                else:
                    items.insert(index, dict(item))
                with self.subTest(phase=phase, condition=condition):
                    with self.assertRaises(ValueError):
                        self.verify(items)

    def test_failed_build_and_invalid_sequences_fail(self):
        good = events()
        sequences = [
            [], good[1:], good[:-1], [good[0], *good[2:]],
            [good[0], good[1], *good[1:]],
            [*good, dict(Action="output", Package=RUNNER.PACKAGE, Output="after pass")],
            [dict(Action="build-fail", Package="dependency"), *good],
            [dict(item, Package="different/package") for item in good],
        ]
        for items in sequences:
            with self.subTest(sequence=items[:2]):
                with self.assertRaises(ValueError):
                    self.verify(items)

    def test_textual_pass_is_not_execution(self):
        with tempfile.TemporaryDirectory() as name:
            path = Path(name) / "native.log"
            for text in ("PASS\nok fcvm\n", '{"Action": invalid}\n'):
                path.write_text(text)
                with self.assertRaises(ValueError):
                    RUNNER.verify_events(path)


class NativeRepairRunnerTest(unittest.TestCase):
    def run_fixture(self, *, native_exit=0, before_exit=0, after_exit=0, proof=True,
                    blocked=False, preflight_only=False, changed_source=False, changed_asset=False, interrupt=False, busy=False):
        with tempfile.TemporaryDirectory() as name:
            base = Path(name)
            directory = base / "evidence"
            directory.mkdir()
            binary = base / "pinned"
            binary.write_bytes(b"pinned bytes")
            asset = dict(path=str(binary), sha256=hashlib.sha256(binary.read_bytes()).hexdigest())
            preflight = dict(errors=["KVM absent"] if blocked else [], assets={"kernel": asset},
                             host=dict(go="/pinned/go", firecracker=asset, jailer=asset))
            source = dict(head="a" * 40, tree_sha256="b" * 64, dirty=True, files=[])
            calls = []

            def logged(command, path, env):
                calls.append((command, dict(env)))
                if command[0] == "make":
                    path.write_text(log_text(events() if proof else []))
                    if changed_asset:
                        binary.write_bytes(b"changed runtime bytes")
                    if interrupt:
                        raise KeyboardInterrupt
                    return native_exit
                path.write_text("leakcheck fixture\n")
                return before_exit if path.name == "leakcheck-before.log" else after_exit

            real_open = open
            with contextlib.ExitStack() as stack:
                stack.enter_context(mock.patch.object(RUNNER, "source_identity", side_effect=[source, dict(source, dirty=False)] if changed_source else lambda: source))
                stack.enter_context(mock.patch.object(RUNNER, "inspect_host", return_value=preflight))
                stack.enter_context(mock.patch.object(RUNNER, "run_logged", side_effect=logged))
                stack.enter_context(mock.patch.object(RUNNER, "open", side_effect=lambda *a, **kw: real_open(base / "lock", "a"), create=True))
                stack.enter_context(mock.patch.object(RUNNER.fcntl, "flock", side_effect=BlockingIOError if busy else None))
                stack.enter_context(contextlib.redirect_stdout(io.StringIO()))
                stack.enter_context(contextlib.redirect_stderr(io.StringIO()))
                status = RUNNER.qualify(directory, {"GREGALE_REPAIR_CRASH_CONFIG": "must-clear", "GREGALE_REPAIR_CRASH_ISOLATED": "1"}, preflight_only)
            report = json.loads((directory / "result.json").read_text())
            for filename, digest in report["evidence_sha256"].items():
                self.assertEqual(RUNNER.sha256(directory / filename), digest)
            return status, report, calls

    def test_pass_requires_native_exit_proof_and_both_leak_checks(self):
        status, report, calls = self.run_fixture()
        self.assertEqual(status, 0)
        self.assertEqual(report["status"], "passed")
        self.assertTrue(report["qualified_native_manager"])
        self.assertFalse(report["serving_daemon_and_node_failure_qualified"])
        self.assertEqual(len(calls), 3)
        self.assertEqual(calls[1][1]["FAAS_DATA_REPAIR_NATIVE_TEST"], "1")
        self.assertNotIn("GREGALE_REPAIR_CRASH_CONFIG", calls[1][1])
        self.assertNotIn("GREGALE_REPAIR_CRASH_ISOLATED", calls[1][1])

    def test_failure_and_interruption_still_check_leaks(self):
        for changes in (dict(native_exit=2), dict(after_exit=1), dict(proof=False), dict(interrupt=True), dict(changed_source=True), dict(changed_asset=True)):
            with self.subTest(changes=changes):
                status, report, calls = self.run_fixture(**changes)
                self.assertEqual(status, 1)
                self.assertEqual(report["status"], "failed")
                self.assertFalse(report["qualified_native_manager"])
                self.assertEqual(len(calls), 3)
                self.assertIn("leakcheck.sh", calls[-1][0][1])

    def test_preflight_and_dirty_host_cannot_start_tests(self):
        for changes, expected_calls, expected_status in ((dict(blocked=True), 0, "blocked"),
                                                        (dict(busy=True), 0, "blocked"),
                                                        (dict(before_exit=1), 1, "failed"),
                                                        (dict(preflight_only=True), 0, "preflight_passed")):
            status, report, calls = self.run_fixture(**changes)
            self.assertEqual(len(calls), expected_calls)
            self.assertEqual(report["status"], expected_status)
            self.assertFalse(report["qualified_native_manager"])
            self.assertEqual(status, 0 if changes.get("preflight_only") else 1)

    def test_logs_redact_fixture_connection_and_password(self):
        with tempfile.TemporaryDirectory() as name, contextlib.redirect_stdout(io.StringIO()) as output:
            path = Path(name) / "command.log"
            env = dict(DATABASE_URL="postgres://user:example-password@localhost/fixture")
            command = ["python3", "-c", "print('postgres://user:example-password@localhost/fixture example-password')"]
            self.assertEqual(RUNNER.run_logged(command, path, env), 0)
            self.assertNotIn("example-password", path.read_text())
            self.assertNotIn("example-password", output.getvalue())

    def test_go_compiler_pin_is_independent_of_language_version(self):
        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            (root / "go.mod").write_text("go 1.26.0\ntoolchain go1.26.9\n")
            with mock.patch.object(RUNNER, "ROOT", root), \
                 mock.patch.object(RUNNER.shutil, "which", side_effect=lambda tool: "/fake/go" if tool == "go" else None), \
                 mock.patch.object(RUNNER, "capture", return_value="go1.26.9"):
                report = RUNNER.inspect_host({})
            self.assertEqual(report["host"]["go_version"], "go1.26.9")
            self.assertFalse(any("Go version differs" in error for error in report["errors"]))

    def test_uncommitted_source_changes_change_evidence_identity(self):
        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            (root / "tracked.go").write_text("package fixture\n")
            new = root / "uncommitted.go"
            new.write_text("package fixture\n// before\n")
            def capture(*args):
                return "a" * 40 if args[1] == "rev-parse" else "?? uncommitted.go"
            with mock.patch.object(RUNNER, "ROOT", root), \
                 mock.patch.object(RUNNER, "capture", side_effect=capture), \
                 mock.patch.object(RUNNER.subprocess, "check_output", return_value=b"tracked.go\0uncommitted.go\0"):
                before = RUNNER.source_identity()
                new.write_text("package fixture\n// after\n")
                after = RUNNER.source_identity()
            self.assertEqual(before["head"], after["head"])
            self.assertNotEqual(before["tree_sha256"], after["tree_sha256"])
            self.assertTrue(before["dirty"])

    def test_make_preserves_the_exact_test_selection_and_json_flags(self):
        _, _, calls = self.run_fixture()
        with tempfile.TemporaryDirectory() as name:
            base = Path(name)
            fake = base / "go"
            recorded = base / "arguments.jsonl"
            fake.write_text("#!/usr/bin/env python3\nimport json,os,sys\n"
                            "with open(os.environ['FAKE_REPAIR_ARGUMENTS'],'a') as f: f.write(json.dumps(sys.argv[1:])+'\\n')\n"
                            "if sys.argv[1:]==['env','GOOS']: print('linux')\n"
                            "if sys.argv[1:]==['env','GOARCH']: print('amd64')\n")
            fake.chmod(0o700)
            command = ["GO=" + str(fake) if arg.startswith("GO=") else arg for arg in calls[1][0]]
            env = dict(os.environ, FAKE_REPAIR_ARGUMENTS=str(recorded), RUN_REGEX="^WrongInheritedFilter$", RUN_ARGS="-run=WrongInheritedFilter")
            subprocess.run(command, cwd=RUNNER.ROOT, env=env, check=True, capture_output=True)
            tests = [args for args in map(json.loads, recorded.read_text().splitlines()) if args[0] == "test"]
            self.assertEqual(len(tests), 1)
            args = tests[0]
            self.assertEqual(args[args.index("-run") + 1], "^(" + "|".join(RUNNER.TESTS) + ")$")
            self.assertIn("-json", args)
            self.assertIn("-timeout=20m", args)
            self.assertIn("-race", args)
            self.assertNotIn("-run=WrongInheritedFilter", args)

    def test_existing_evidence_and_source_checkout_are_rejected(self):
        with tempfile.TemporaryDirectory() as name:
            directory = Path(name)
            retained = directory / "retained.json"
            retained.write_text("preserve previous run")
            for destination, message in ((directory, "must be empty"), (RUNNER.ROOT / "scripts", "outside the source checkout")):
                result = subprocess.run(["python3", RUNNER.__file__, "--evidence-dir", str(destination)], capture_output=True, text=True, check=False)
                self.assertEqual(result.returncode, 2)
                self.assertIn(message, result.stderr)
            self.assertEqual(retained.read_text(), "preserve previous run")


if __name__ == "__main__":
    unittest.main()
