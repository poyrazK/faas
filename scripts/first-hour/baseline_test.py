#!/usr/bin/env python3
"""Scorecard contracts. These fixtures are parser tests, never live evidence."""
import json
import math
import io
import sys
import subprocess
import tempfile
import unittest
from unittest import mock
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import baseline
import acceptance


def completed_events():
    events = [{"Test": "TestFirstHourCLI/" + name, "Action": "pass", "Elapsed": 2.0} for name in baseline.STEPS]
    events.extend({"Test": "TestFirstHourCLI/repair_and_verify", "Action": "output", "Output": f"metric {name}={1 if name == 'first_hour_interruptions' else 1.5}\n"} for name in baseline.METRICS)
    return events


class BaselineTests(unittest.TestCase):
    def test_no_evidence_is_not_a_passing_baseline(self):
        result = baseline.scorecard([], [], {"evidence_kind": "deployed"})
        self.assertEqual(result["coverage"]["scripted_cli"], "not_measured")
        self.assertTrue(all(value is None for value in result["metrics"].values()))
        self.assertEqual(result["coverage"]["agent_tasks"], "not_measured")
        self.assertTrue(all(row["runs"] == 0 for row in result["agents"]))

    def test_live_cli_success_does_not_imply_login_or_agent_acceptance(self):
        result = baseline.scorecard(completed_events(), [], {"evidence_kind": "deployed"})
        self.assertEqual(result["coverage"]["scripted_cli"], "passed")
        self.assertEqual(result["coverage"]["install_login"], "not_measured")
        self.assertEqual(result["coverage"]["interactive_start"], "not_measured")

    def test_fixture_run_cannot_qualify_deployed_acceptance(self):
        result = baseline.scorecard(completed_events(), [], {"evidence_kind": "fixture"})
        self.assertEqual(result["coverage"]["scripted_cli"], "fixture_only")

    def test_old_cli_blocks_before_any_journey(self):
        result = baseline.scorecard([], [], {"evidence_kind": "deployed", "release_verify_available": False})
        self.assertEqual(result["coverage"]["scripted_cli"], "blocked")

    def test_hour_limit_and_missing_interruption_cannot_qualify(self):
        for metric, value in [("first_hour_complete_s", 3601), ("first_hour_interruptions", 0), ("first_hour_first_verified_response_s", 50)]:
            event = {"Test": "TestFirstHourCLI/repair_and_verify", "Action": "output", "Output": f"metric {metric}={value}"}
            self.assertEqual(baseline.scorecard([*completed_events(), event], [], {"evidence_kind": "deployed"})["coverage"]["scripted_cli"], "failed")

    def test_skip_missing_metric_cleanup_and_package_failure_are_not_passes(self):
        mutations = [
            completed_events()[1:],
            completed_events()[:-1],
            [*completed_events(), {"Test": "TestFirstHourCLI/cleanup", "Action": "fail", "Elapsed": 1}],
            [*completed_events(), {"Test": "TestFirstHourCLI/repair_and_verify", "Action": "skip"}],
            [*completed_events(), {"Action": "fail"}],
            [*completed_events(), {"Test": "TestFirstHourCLI", "Action": "fail"}],
        ]
        for events in mutations:
            with self.subTest(events=events[-1]):
                result = baseline.scorecard(events, [], {"evidence_kind": "deployed"})
                self.assertNotEqual(result["coverage"]["scripted_cli"], "passed")

    def test_nonfinite_and_negative_measurements_are_rejected(self):
        for value in (math.nan, math.inf, -1, True):
            with self.subTest(value=value), self.assertRaises(ValueError):
                baseline.scorecard([{"Test": "TestFirstHourCLI/cleanup", "Action": "pass", "Elapsed": value}], [], {})

    def test_agents_group_by_task_agent_and_toolset_and_count_cleanup_failure(self):
        records = [
            {"task": "first-hour-private", "agent": "codex", "toolset": "none", "success": True, "duration_s": 30, "tool_calls": {"shell": 4}},
            {"task": "first-hour-private", "agent": "codex", "toolset": "none", "success": True, "duration_s": 90, "tool_calls": {"shell": 8}, "left_behind": ["test-app"]},
            {"task": "first-hour-private", "agent": "claude", "toolset": "core", "success": False, "duration_s": 20, "follow_ups": 1},
            {"task": "unrelated-task", "success": True},
        ]
        for record in records:
            record.update(target="https://api.example.test", cli_sha256="a" * 64)
        result = baseline.scorecard([], records, {"target": "https://api.example.test", "cli_sha256": "a" * 64})
        codex = next(row for row in result["agents"] if row["agent"] == "codex")
        self.assertEqual((codex["passed"], codex["runs"], codex["median_seconds"], codex["median_tool_calls"]), (1, 2, 60, 6))
        self.assertEqual(next(row for row in result["agents"] if row["agent"] == "claude")["follow_ups"], 1)

    def test_agent_results_from_another_target_or_cli_are_not_a_baseline(self):
        records = [{"task": "first-hour-private", "agent": "codex", "toolset": "none", "success": True,
                    "target": "https://other.example.test", "cli_sha256": "b" * 64}]
        result = baseline.scorecard([], records, {"target": "https://api.example.test", "cli_sha256": "a" * 64})
        self.assertEqual(result["coverage"]["agent_tasks"], "incompatible")
        self.assertEqual(result["excluded_agent_records"], 1)
        self.assertTrue(all(row["runs"] == 0 for row in result["agents"]))

    def test_report_omits_private_logs_answers_and_credentials(self):
        events = completed_events() + [{"Test": "TestFirstHourCLI/cleanup", "Action": "output", "Output": "token=SECRET response=PRIVATE"}]
        result = baseline.scorecard(events, [{"task": "first-hour-private", "agent": "codex", "toolset": "none", "answer": "PRIVATE"}], {"token": "SECRET", "email": "owner@example.test"})
        rendered = baseline.markdown(result) + json.dumps(result)
        for private in ("SECRET", "PRIVATE", "owner@example.test"):
            self.assertNotIn(private, rendered)

    def test_go_build_error_is_reported_as_failed_without_copying_raw_log(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "go-test.json"
            path.write_text("compile error with PRIVATE details\n")
            events = baseline.read_jsonl(path, tolerant=True)
            result = baseline.scorecard(events, [], {"evidence_kind": "deployed"})
            self.assertEqual(result["coverage"]["scripted_cli"], "failed")
            self.assertNotIn("PRIVATE", json.dumps(result))
            with self.assertRaises(ValueError):
                baseline.read_jsonl(path)


class AcceptanceRunnerTests(unittest.TestCase):
    def test_old_cli_is_blocked_before_any_deployment_and_reports_evidence(self):
        with tempfile.TemporaryDirectory() as directory:
            cli = Path(directory) / "gregale"
            cli.write_text("fake CLI fixture")
            cli.chmod(0o700)
            out = Path(directory) / "results"
            calls = []

            def run(command, **kwargs):
                calls.append(command)
                if command[0] == "bash":
                    self.fail("unsupported CLI must not start deployed journeys")
                output = "v-old" if "version" in command else "revision" if "rev-parse" in command else ""
                return subprocess.CompletedProcess(command, 1 if "verify" in command else 0, output, "")

            argv = ["acceptance.py", "--api", "https://api.example.test", "--cli", str(cli), "--cli-login", "--out", str(out)]
            with mock.patch.object(sys, "argv", argv), mock.patch.object(acceptance.subprocess, "run", side_effect=run), mock.patch.object(sys, "stdout", io.StringIO()):
                self.assertEqual(acceptance.main(), 2)
            result = json.loads((out / "baseline.json").read_text())
            self.assertEqual(result["coverage"]["scripted_cli"], "blocked")
            self.assertFalse(result["provenance"]["release_verify_available"])
            self.assertFalse(any(command[0] == "bash" for command in calls))

    def test_reused_evidence_directory_is_rejected_before_commands(self):
        with tempfile.TemporaryDirectory() as directory:
            cli = Path(directory) / "gregale"
            cli.write_text("fake CLI fixture")
            cli.chmod(0o700)
            out = Path(directory) / "results"
            out.mkdir()
            (out / "go-test.json").write_text("old evidence")
            argv = ["acceptance.py", "--api", "https://api.example.test", "--cli", str(cli), "--cli-login", "--out", str(out)]
            with mock.patch.object(sys, "argv", argv), mock.patch.object(acceptance.subprocess, "run") as run, mock.patch.object(sys, "stderr", io.StringIO()):
                with self.assertRaises(SystemExit):
                    acceptance.main()
                run.assert_not_called()

    def test_target_cannot_contain_embedded_credentials(self):
        argv = ["acceptance.py", "--api", "https://user:token@api.example.test", "--cli", "/bin/echo", "--cli-login", "--out", "/tmp/unused"]
        with mock.patch.object(sys, "argv", argv), mock.patch.object(acceptance.subprocess, "run") as run, mock.patch.object(sys, "stderr", io.StringIO()):
            with self.assertRaises(SystemExit):
                acceptance.main()
            run.assert_not_called()


if __name__ == "__main__":
    unittest.main()
