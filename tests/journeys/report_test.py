#!/usr/bin/env python3
"""Tests for report.py: python3 -I tests/journeys/report_test.py"""
import io
import json
import os
import sys
import tempfile
import unittest
from contextlib import redirect_stdout

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import report  # noqa: E402


def stream(*events):
    return "".join(json.dumps(e) + "\n" for e in events)


EVENTS = stream(
    {"Action": "run", "Test": "TestAppLifecycle"},
    {"Action": "run", "Test": "TestAppLifecycle/deploy_v1"},
    {"Action": "output", "Test": "TestAppLifecycle/deploy_v1", "Output": "    harness_test.go:420: deployed e2ej-x\n"},
    {"Action": "output", "Test": "TestAppLifecycle/deploy_v1", "Output": "    harness_test.go:440: warning: deploy e2ej-x aborted for capacity (RAM headroom); retrying once in 60s\n"},
    {"Action": "pass", "Test": "TestAppLifecycle/deploy_v1", "Elapsed": 95.2},
    {"Action": "run", "Test": "TestAppLifecycle/park_and_wake_on_request"},
    {"Action": "output", "Test": "TestAppLifecycle/park_and_wake_on_request", "Output": "    app_test.go:150: metric wake_first_request_ms=812\n"},
    {"Action": "pass", "Test": "TestAppLifecycle/park_and_wake_on_request", "Elapsed": 20.0},
    {"Action": "run", "Test": "TestAppLifecycle/http_request_logs"},
    {"Action": "output", "Test": "TestAppLifecycle/http_request_logs", "Output": "    app_test.go:80: request in logs: not reached within 3m0s; last: Not found (not_found)\n"},
    {"Action": "fail", "Test": "TestAppLifecycle/http_request_logs", "Elapsed": 180.0},
    {"Action": "run", "Test": "TestAppLifecycle/rollback_to_v1"},
    {"Action": "output", "Test": "TestAppLifecycle/rollback_to_v1", "Output": "    harness_test.go:390: blocked: step \"redeploy_v2\" failed\n"},
    {"Action": "skip", "Test": "TestAppLifecycle/rollback_to_v1", "Elapsed": 0},
    {"Action": "fail", "Test": "TestAppLifecycle", "Elapsed": 300.0},
    {"Action": "fail", "Elapsed": 301.0},
)


def run(stdin, *args):
    out = io.StringIO()
    old = sys.stdin
    sys.stdin = io.StringIO(stdin)
    try:
        with redirect_stdout(out):
            code = report.main(list(args))
    finally:
        sys.stdin = old
    return code, out.getvalue()


class ReportTest(unittest.TestCase):
    def test_summary_counts_steps_not_parents(self):
        code, out = run(EVENTS, "--quiet")
        self.assertEqual(code, 1)
        self.assertIn("2 passed, 1 failed, 0 known bugs, 1 skipped", out)
        self.assertNotIn("TestAppLifecycle  FAIL", out)

    def test_failure_log_and_metrics(self):
        _, out = run(EVENTS, "--quiet")
        self.assertIn("--- FAIL TestAppLifecycle/http_request_logs", out)
        self.assertIn("Not found (not_found)", out)
        self.assertIn("wake_first_request_ms=812", out)
        self.assertIn("warnings:\n  deploy e2ej-x aborted for capacity (RAM headroom); retrying once in 60s  (TestAppLifecycle/deploy_v1)", out)

    def test_live_progress_prints_each_step_once(self):
        _, out = run(EVENTS)
        self.assertEqual(out.count("PASS  TestAppLifecycle/deploy_v1"), 1)
        self.assertIn('SKIP  TestAppLifecycle/rollback_to_v1  (0.0s)  blocked: step "redeploy_v2" failed', out)

    def test_markdown(self):
        with tempfile.TemporaryDirectory() as d:
            path = os.path.join(d, "report.md")
            run(EVENTS, "--quiet", "--markdown", path, "--header", "run: abc123")
            with open(path) as f:
                md = f.read()
        self.assertIn("- run: abc123", md)
        self.assertIn("| `TestAppLifecycle/http_request_logs` | FAIL | 180.0 |", md)
        self.assertIn("### TestAppLifecycle/http_request_logs", md)

    def test_all_pass_exits_zero(self):
        code, _ = run(stream(
            {"Action": "run", "Test": "TestPublicSurface"},
            {"Action": "pass", "Test": "TestPublicSurface", "Elapsed": 1.0},
            {"Action": "pass", "Elapsed": 1.1},
        ), "--quiet")
        self.assertEqual(code, 0)

    def test_build_failure_fails(self):
        code, out = run("# github.com/x\n./a_test.go:1: syntax error\n", "--quiet")
        self.assertEqual(code, 1)
        self.assertIn("syntax error", out)

    def test_no_tests_fails(self):
        code, _ = run(stream({"Action": "pass", "Elapsed": 0.1}), "--quiet")
        self.assertEqual(code, 1)


    def test_known_failure_does_not_fail_the_run(self):
        with tempfile.TemporaryDirectory() as d:
            known = os.path.join(d, "known.json")
            with open(known, "w") as f:
                json.dump([{"step": "TestAppLifecycle/http_request_logs", "bug": "logs --source http 404"}], f)
            code, out = run(EVENTS, "--quiet", "--known", known)
        self.assertEqual(code, 0)
        self.assertIn("2 passed, 0 failed, 1 known bugs, 1 skipped", out)
        self.assertIn("known bugs still failing:\n  TestAppLifecycle/http_request_logs: logs --source http 404", out)
        self.assertNotIn("--- FAIL", out)

    def test_known_bug_that_passes_is_reported(self):
        with tempfile.TemporaryDirectory() as d:
            known = os.path.join(d, "known.json")
            with open(known, "w") as f:
                json.dump([{"step": "TestAppLifecycle/deploy_v1", "bug": "old bug"}], f)
            code, out = run(EVENTS, "--quiet", "--known", known)
        self.assertEqual(code, 1)  # the unknown http_request_logs failure still fails
        self.assertIn("known bugs that passed", out)
        self.assertIn("TestAppLifecycle/deploy_v1: old bug", out)

    def test_history_compares_with_the_previous_run(self):
        with tempfile.TemporaryDirectory() as d:
            prev_dir = os.path.join(d, "20261027T100000Z")
            os.makedirs(prev_dir)
            with open(os.path.join(prev_dir, "summary.json"), "w") as f:
                json.dump({"header": ["started: earlier"], "metrics": {"wake_first_request_ms": 400},
                           "steps": {"TestAppLifecycle/deploy_v1": {"result": "pass", "seconds": 40.0},
                                     "TestAppLifecycle/http_request_logs": {"result": "pass", "seconds": 5.0},
                                     "TestAppLifecycle/park_and_wake_on_request": {"result": "fail", "seconds": 9.0}}}, f)
            cur_dir = os.path.join(d, "20261027T120000Z")
            os.makedirs(cur_dir)
            summary = os.path.join(cur_dir, "summary.json")
            _, out = run(EVENTS, "--quiet", "--summary", summary, "--history-dir", d)
            with open(summary) as f:
                written = json.load(f)
        self.assertIn("since started: earlier:", out)
        self.assertIn("newly failing: TestAppLifecycle/http_request_logs", out)
        self.assertIn("now passing: TestAppLifecycle/park_and_wake_on_request", out)
        self.assertIn("slower: TestAppLifecycle/deploy_v1 40s -> 95s", out)
        self.assertIn("wake_first_request_ms: 400 -> 812 (+103%)", out)
        self.assertIn("flaky over the last 5 runs: TestAppLifecycle/http_request_logs, TestAppLifecycle/park_and_wake_on_request", out)
        self.assertEqual(written["steps"]["TestAppLifecycle/deploy_v1"], {"result": "pass", "seconds": 95.2})

    def test_first_run_has_no_comparison(self):
        with tempfile.TemporaryDirectory() as d:
            _, out = run(EVENTS, "--quiet", "--history-dir", d)
        self.assertNotIn("since ", out)


SKIPS = stream(
    {"Action": "run", "Test": "TestBilling"},
    {"Action": "output", "Test": "TestBilling", "Output": "    billing_test.go:20: needs GREGALE_E2E_BILLING_TOKEN: a dedicated fixture account\n"},
    {"Action": "skip", "Test": "TestBilling", "Elapsed": 0},
    {"Action": "run", "Test": "TestNewThing"},
    {"Action": "output", "Test": "TestNewThing", "Output": "    new_test.go:9: feature flag off\n"},
    {"Action": "skip", "Test": "TestNewThing", "Elapsed": 0},
    {"Action": "run", "Test": "TestApp"},
    {"Action": "run", "Test": "TestApp/deploy"},
    {"Action": "pass", "Test": "TestApp/deploy", "Elapsed": 1},
    {"Action": "run", "Test": "TestApp/blocked_step"},
    {"Action": "output", "Test": "TestApp/blocked_step", "Output": "    harness_test.go:390: blocked: step \"x\" failed\n"},
    {"Action": "skip", "Test": "TestApp/blocked_step", "Elapsed": 0},
    {"Action": "pass", "Test": "TestApp", "Elapsed": 2},
    {"Action": "pass", "Elapsed": 3},
)


TIMEOUT = stream(
    {"Action": "run", "Test": "TestA"},
    {"Action": "run", "Test": "TestA/one"},
    {"Action": "pass", "Test": "TestA/one", "Elapsed": 1},
    {"Action": "run", "Test": "TestB"},
    {"Action": "run", "Test": "TestB/step"},
    {"Action": "output", "Test": "TestA", "Output": "panic: test timed out after 2h30m0s\n"},
    {"Action": "output", "Test": "TestA", "Output": "\trunning tests:\n"},
    {"Action": "output", "Test": "TestA", "Output": "\t\tTestB/step (2h29m51s)\n"},
    {"Action": "output", "Test": "TestA", "Output": "\t\tTestC (2h5m43s)\n"},
    {"Action": "output", "Test": "TestA", "Output": "\n"},
    {"Action": "output", "Test": "TestA", "Output": "goroutine 1 [running]:\n"},
    {"Action": "fail", "Test": "TestA", "Elapsed": 9000},
    {"Action": "fail", "Elapsed": 9000},
)


class TimeoutTest(unittest.TestCase):
    def test_timeout_names_unfinished_journeys_and_fails(self):
        with tempfile.TemporaryDirectory() as d:
            path = os.path.join(d, "summary.json")
            code, out = run(TIMEOUT, "--quiet", "--summary", path)
            with open(path) as f:
                summary = json.load(f)
        self.assertEqual(code, 1)
        self.assertIn("TIMED OUT after 2h30m0s: 2 journeys never finished", out)
        self.assertIn("TestB/step (running 2h29m51s)", out)
        self.assertNotIn("goroutine", out.split("TIMED OUT")[1].split("\n\n")[0])
        self.assertEqual(summary["timed_out"], ["TestB", "TestC"])


class SkipAndKnownReviewTest(unittest.TestCase):
    def files(self, d, known=None, allowed=None):
        paths = []
        for name, doc in (("known.json", known), ("allowed.json", allowed)):
            path = os.path.join(d, name)
            with open(path, "w") as f:
                json.dump(doc or [], f)
            paths.append(path)
        return paths

    def test_not_run_lists_reasons_but_not_blocked_steps(self):
        code, out = run(SKIPS, "--quiet")
        self.assertEqual(code, 0)
        self.assertIn("not run:\n", out)
        self.assertIn("1 x feature flag off: TestNewThing", out)
        self.assertNotIn("blocked_step", out.split("not run:")[1])

    def test_unexpected_skip_fails_the_run(self):
        with tempfile.TemporaryDirectory() as d:
            known, allowed = self.files(d, allowed=[{"pattern": "GREGALE_E2E_BILLING_TOKEN"}])
            code, out = run(SKIPS, "--quiet", "--allowed-skips", allowed)
            self.assertEqual(code, 1)
            self.assertIn("unexpected skips", out)
            self.assertIn("TestNewThing: feature flag off", out)
            self.assertNotIn("TestBilling: needs", out.split("unexpected skips")[1])
            _, allowed = self.files(d, allowed=[{"pattern": "BILLING_TOKEN"}, {"pattern": "feature flag off"}])
            code, _ = run(SKIPS, "--quiet", "--allowed-skips", allowed)
            self.assertEqual(code, 0)

    def test_known_failures_to_review(self):
        known = [
            {"step": "TestApp/deploy", "bug": "b", "since": "2026-10-01"},
            {"step": "TestApp/renamed", "bug": "b", "since": "2026-11-14"},
            {"step": "TestBilling/x", "bug": "b", "since": "2026-11-14"},
            {"step": "TestOther/y", "bug": "b", "since": "2026-01-01", "review_by": "2026-12-01"},
        ]
        with tempfile.TemporaryDirectory() as d:
            path, _ = self.files(d, known=known)
            _, out = run(SKIPS, "--quiet", "--known", path, "--now", "2026-11-15")
        review = out.split("known failures to review")[1].split("\n\n")[0]
        self.assertIn("TestApp/deploy: review date 2026-10-22 passed", review)
        self.assertIn("TestApp/renamed: its journey ran without this step", review)
        # A journey skipped whole, or not run at all, says nothing about its steps.
        self.assertNotIn("TestBilling/x", review)
        self.assertNotIn("TestOther/y", review)


if __name__ == "__main__":
    unittest.main()
