#!/usr/bin/env python3
"""Synthetic event/metadata contract tests; these do not execute native VMs."""
import copy
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import gitops_native_report as report


def event(action, test=None, **extra):
    value = {"Package": report.PACKAGE, "Action": action, **extra}
    if test:
        value["Test"] = test
    return value


def successful_events():
    events = [event("start")]
    expected = report.required_tests()
    def visit(name):
        events.append(event("run", name))
        for child in expected:
            if "/" in child and child.rsplit("/", 1)[0] == name:
                visit(child)
        events.append(event("pass", name))
    for root in (report.ROOT_TEST, report.SOURCE_JOB_TEST):
        visit(root)
    events.append(event("pass"))
    return events


def metadata():
    return {"source": {"commit": "a" * 40, "clean": True},
            "host": {"system": "Linux", "architecture": "x86_64", "virtualization_probe": "none"},
            "base_images": {key: "registry.example/"+key+"@sha256:"+"c"*64 for key in report.BASE_IMAGES},
            "fixtures": {key: {"sha256": "b" * 64, "bytes": 128} for key in report.FIXTURES},
            "versions": {"go": "go1.26.9", "go_required": "go1.26.9", "firecracker": "v1.15.0",
                         "jailer": "v1.15.0", "postgres_client": "PostgreSQL 16"}}


class NativeReportTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.directory = Path(self.tmp.name)
        self.events = self.directory / "test-events.jsonl"
        self.write_events(successful_events())

    def write_events(self, events):
        self.events.write_text("".join(json.dumps(e) + "\n" for e in events))

    def test_requires_both_providers_and_every_scenario(self):
        self.assertTrue(report.read_events(self.events)["complete"])
        for missing in report.required_tests():
            with self.subTest(missing=missing):
                self.write_events([e for e in successful_events() if e.get("Test") != missing])
                self.assertFalse(report.read_events(self.events)["complete"])

    def test_image_only_evidence_cannot_substitute_for_source_job_matrix(self):
        expected = report.required_tests()
        for provider in ("github", "gitlab"):
            for kind in ("source", "dockerfile"):
                for scenario in ("qualified_schedule", "artifact_reuse", "scheduler_restart", "held_build_supersession", "reviewed_rollback"):
                    self.assertIn(f"{report.SOURCE_JOB_TEST}/{provider}/{kind}/{scenario}", expected)
        self.write_events([e for e in successful_events() if not e.get("Test", "").startswith(report.SOURCE_JOB_TEST)])
        self.assertFalse(report.read_events(self.events)["complete"])
        self.assertFalse(self.finish()[0])

    def test_source_scenarios_cannot_skip_or_fail(self):
        for name in report.required_tests():
            if not name.startswith(report.SOURCE_JOB_TEST + "/"):
                continue
            for action in ("skip", "fail"):
                events = successful_events()
                for e in events:
                    if e.get("Test") == name and e["Action"] == "pass":
                        e["Action"] = action
                self.write_events(events)
                self.assertFalse(report.read_events(self.events)["complete"])

    def test_skips_failures_duplicates_and_out_of_order_results_fail_closed(self):
        key = report.ROOT_TEST + "/gitlab/reviewed_rollback"
        for action in ("skip", "fail", "run"):
            with self.subTest(action=action):
                events = successful_events()
                for e in events:
                    if e.get("Test") == key and e["Action"] == "pass":
                        e["Action"] = action
                self.write_events(events)
                self.assertFalse(report.read_events(self.events)["complete"])
        for injected in (event("pass", key), event("fail", "UnexpectedTest"), event("fail", Package="another/package")):
            self.write_events(successful_events() + [injected])
            self.assertFalse(report.read_events(self.events)["complete"])
        events = successful_events()
        events.insert(1, events.pop())  # A premature package pass is incomplete.
        self.write_events(events)
        self.assertFalse(report.read_events(self.events)["complete"])

    def test_human_pass_output_and_malformed_or_truncated_stream_never_qualify(self):
        for text in ("--- PASS: " + report.ROOT_TEST + "\n", "{}\n", "[]\n", "{", "x" * ((1 << 20) + 1)):
            with self.subTest(text=text[:30]):
                self.events.write_text(text)
                self.assertFalse(report.read_events(self.events)["complete"])
        self.write_events([event("start"), event("output", Output="\n--- PASS: " + report.ROOT_TEST)])
        self.assertFalse(report.read_events(self.events)["complete"])

    def finish(self, initial=None, final=None, host_class="native", stages=None, exit_code=0):
        initial = initial or metadata()
        (self.directory / "report.json").write_text(json.dumps({"schema": report.SCHEMA, "coverage": report.required_coverage(), "initial": initial, "host_class": host_class,
            "production_enforcement_enabled": False, "native_acceptance_passed": False}))
        stages = stages or {key: "pass" for key in ("preflight", "leak_before", "journey", "leak_after", "source")}
        with patch.object(report, "metadata", return_value=final or initial):
            passed = report.finish(self.directory, self.directory, stages, "", exit_code)
        value = json.loads((self.directory / "report.json").read_text())
        self.assertFalse(value["production_enforcement_enabled"])
        self.assertEqual((self.directory / "report.json").stat().st_mode & 0o777, 0o600)
        return passed, value

    def test_complete_native_and_nested_results_are_distinct(self):
        passed, value = self.finish()
        self.assertTrue(passed)
        self.assertTrue(value["native_acceptance_passed"])
        self.assertTrue(value["source_scheduled_jobs_passed"])
        self.assertEqual(value["result"], "passed")
        passed, value = self.finish(host_class="nested-diagnostic")
        self.assertTrue(passed)
        self.assertFalse(value["native_acceptance_passed"])
        self.assertEqual(value["result"], "diagnostic_passed")
        self.assertFalse(value["source_scheduled_jobs_passed"])

    def test_old_or_changed_report_contract_cannot_qualify(self):
        for field, value in (("schema", "gregale.dev/gitops-native-acceptance/v1"), ("coverage", {})):
            self.finish()
            path = self.directory / "report.json"
            stored = json.loads(path.read_text())
            stored[field] = value
            path.write_text(json.dumps(stored))
            with patch.object(report, "metadata", return_value=metadata()):
                self.assertFalse(report.finish(self.directory, self.directory,
                    {key: "pass" for key in ("preflight", "leak_before", "journey", "leak_after", "source")}, "", 0))
            final = json.loads(path.read_text())
            self.assertFalse(final["native_acceptance_passed"])
            self.assertIn("report_contract_mismatch", final["failure_reasons"])

    def test_unverified_host_missing_versions_and_changed_inputs_fail(self):
        mutations = (
            lambda x: x["source"].update(clean=False),
            lambda x: x["source"].update(commit=""),
            lambda x: x["host"].update(system="Darwin"),
            lambda x: x["host"].update(virtualization_probe="kvm"),
            lambda x: x["host"].update(virtualization_probe=None),
            lambda x: x["fixtures"].update(guest_init=None),
            lambda x: x["base_images"].update(builder=None),
            lambda x: x["base_images"].pop("source_runtime"),
            lambda x: x["versions"].update(firecracker=None),
            lambda x: x["versions"].update(go="go1.27.0"),
        )
        for change in mutations:
            with self.subTest(change=change):
                initial = metadata()
                change(initial)
                passed, value = self.finish(initial)
                self.assertFalse(passed)
                self.assertFalse(value["native_acceptance_passed"])
        for field in ("source", "fixtures", "versions", "base_images"):
            final = copy.deepcopy(metadata())
            final[field][next(iter(final[field]))] = "changed"
            passed, _ = self.finish(final=final)
            self.assertFalse(passed)

    def test_failed_leak_source_preflight_or_exit_cannot_pass(self):
        for key in ("preflight", "leak_before", "journey", "leak_after", "source"):
            stages = {k: "pass" for k in ("preflight", "leak_before", "journey", "leak_after", "source")}
            stages[key] = "fail"
            passed, value = self.finish(stages=stages)
            self.assertFalse(passed)
            self.assertFalse(value["native_acceptance_passed"])
        self.assertFalse(self.finish(exit_code=143)[0])

    def test_new_private_directory_cannot_overwrite_an_existing_run(self):
        target = self.directory / "new"
        with patch.object(report, "metadata", return_value=metadata()):
            report.initialize(self.directory / "source", str(target), "native")
            with self.assertRaises(FileExistsError):
                report.initialize(self.directory / "source", str(target), "native")
            with self.assertRaises(ValueError):
                report.initialize(self.directory, str(self.directory / "inside-source"), "native")
        self.assertEqual(target.stat().st_mode & 0o777, 0o700)

    def test_image_configuration_is_pinned_and_redacted(self):
        for value in (None, "registry.example/base:latest", "https://registry.example/base@sha256:"+"a"*64, "https://user:must-not-leak@registry.example/base@sha256:"+"a"*64):
            self.assertIsNone(report.pinned_image(value))
        self.assertEqual(report.pinned_image("registry.example/base@sha256:"+"a"*64), "registry.example/base@sha256:"+"a"*64)

    def test_fingerprints_do_not_block_on_devices_or_pipes(self):
        path = self.directory / "fixture"
        path.write_bytes(b"reviewed fixture")
        self.assertEqual(report.fingerprint(path)["bytes"], 16)
        self.assertIsNone(report.fingerprint(self.directory / "missing"))
        self.assertIsNone(report.fingerprint(self.directory))
        self.assertIsNone(report.fingerprint("/dev/null"))
        fifo = self.directory / "pipe"
        os.mkfifo(fifo)
        self.assertIsNone(report.fingerprint(fifo))

    def test_preflight_failure_still_writes_a_redacted_blocked_report(self):
        repo = Path(__file__).resolve().parent.parent
        target = self.directory / "blocked-run"
        env = os.environ.copy()
        for name in ("FAAS_TEST_KERNEL", "FAAS_GUEST_INIT", *report.BASE_IMAGES.values()):
            env.pop(name, None)
        secret = "must-not-appear-in-report"
        env.update(GREGALE_GITOPS_NATIVE_REPORT_DIR=str(target), GREGALE_GITOPS_NATIVE_HOST_CLASS="native", DATABASE_URL=secret)
        result = subprocess.run(["bash", str(repo / "scripts/test-environment-gitops-native.sh")],
                                cwd=repo, env=env, capture_output=True, timeout=60, check=False)
        self.assertNotEqual(result.returncode, 0)
        value = (target / "report.json").read_text()
        self.assertNotIn(secret, value + result.stdout.decode() + result.stderr.decode())
        data = json.loads(value)
        self.assertEqual(data["result"], "blocked")
        self.assertFalse(data["native_acceptance_passed"])
        self.assertEqual(data["stages"]["journey"], "not_run")


if __name__ == "__main__":
    unittest.main()
