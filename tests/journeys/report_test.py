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
        self.assertIn("2 passed, 1 failed, 1 skipped", out)
        self.assertNotIn("TestAppLifecycle  FAIL", out)

    def test_failure_log_and_metrics(self):
        _, out = run(EVENTS, "--quiet")
        self.assertIn("--- FAIL TestAppLifecycle/http_request_logs", out)
        self.assertIn("Not found (not_found)", out)
        self.assertIn("wake_first_request_ms=812", out)

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


if __name__ == "__main__":
    unittest.main()
