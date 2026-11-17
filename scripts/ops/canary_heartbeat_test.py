#!/usr/bin/env python3
"""A missed-run monitor must never be refreshed by an unsuccessful canary."""

import contextlib
import http.client
import http.server
import importlib.util
import io
import os
from pathlib import Path
import ssl
import subprocess
import tempfile
import threading
import unittest
from unittest.mock import Mock, patch
import urllib.error

ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("canary_heartbeat", ROOT / "scripts/ops/canary_heartbeat.py")
HEARTBEAT = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(HEARTBEAT)
SECRET_URL = "https://monitor.example.invalid/private-heartbeat-token"


def scheduled(**overrides):
    return dict({"GITHUB_EVENT_NAME": "schedule", "GITHUB_RUN_ATTEMPT": "1",
                 "CANARY_JOB_STATUS": "success", "CANARY_PROBE_OUTCOME": "success",
                 "CANARY_HEARTBEAT_URL": SECRET_URL}, **overrides)


def invoke(environ, reporter):
    output = io.StringIO()
    with contextlib.redirect_stdout(output), contextlib.redirect_stderr(output):
        code = HEARTBEAT.run(environ, reporter=reporter)
    return code, output.getvalue()


class EligibilityTest(unittest.TestCase):
    def test_only_first_successful_scheduled_run_reports(self):
        reporter = Mock(return_value=True)
        code, output = invoke(scheduled(), reporter)
        self.assertEqual(code, 0)
        reporter.assert_called_once_with(SECRET_URL)
        self.assertIn("scheduled_success_reported", output)
        self.assertNotIn("private-heartbeat-token", output)

    def test_manual_failed_cancelled_incomplete_and_rerun_jobs_do_not_read_the_secret(self):
        class GuardedEnvironment(dict):
            def get(self, key, default=None):
                if key == "CANARY_HEARTBEAT_URL":
                    raise AssertionError("ineligible run read the heartbeat secret")
                return super().get(key, default)

        cases = [{"GITHUB_EVENT_NAME": event} for event in ("workflow_dispatch", "push", "pull_request", "")]
        cases += [{"CANARY_JOB_STATUS": status} for status in ("failure", "cancelled", "skipped", "timed_out", "")]
        cases += [{"CANARY_PROBE_OUTCOME": status} for status in ("failure", "cancelled", "skipped", "")]
        cases += [{"GITHUB_RUN_ATTEMPT": attempt} for attempt in ("2", "3", "", "unknown")]
        for settings in cases:
            with self.subTest(settings=settings):
                reporter = Mock()
                code, output = invoke(GuardedEnvironment(scheduled(**settings)), reporter)
                self.assertEqual(code, 0)
                reporter.assert_not_called()
                self.assertIn("skipped_ineligible_run", output)

    def test_unconfigured_monitor_is_an_error_for_a_qualifying_run(self):
        reporter = Mock()
        code, output = invoke(scheduled(CANARY_HEARTBEAT_URL=""), reporter)
        self.assertEqual(code, 1)
        self.assertIn("heartbeat_url_unconfigured", output)
        reporter.assert_not_called()

    def test_invalid_secret_urls_never_reach_a_transport_or_log(self):
        urls = ["http://monitor.example.invalid/private-token", "file:///private-token",
                "https:///private-token", "https://user:private-token@example.invalid/",
                "https://example.invalid/#private-token", "https://example.invalid:bad/private-token",
                "https://example.invalid:0/private-token", "https://example.invalid:65536/private-token",
                "https://example.invalid/private-token\n", " https://example.invalid/private-token",
                "https://example.invalid/" + "private-token" * 200, "https://example.invalid/\\private-token"]
        for url in urls:
            with self.subTest(length=len(url)):
                reporter = Mock()
                code, output = invoke(scheduled(CANARY_HEARTBEAT_URL=url), reporter)
                self.assertEqual(code, 1)
                self.assertIn("invalid_heartbeat_url", output)
                self.assertNotIn("private-token", output)
                reporter.assert_not_called()

    def test_provider_failure_and_exceptions_are_errors_without_credentials(self):
        for reporter in (Mock(return_value=False), Mock(side_effect=OSError(SECRET_URL)),
                         Mock(side_effect=http.client.HTTPException(SECRET_URL))):
            with self.subTest(reporter=type(reporter).__name__):
                code, output = invoke(scheduled(), reporter)
                self.assertEqual(code, 1)
                self.assertIn("heartbeat_delivery_failed", output)
                self.assertNotIn("private-heartbeat-token", output)


class TransportTest(unittest.TestCase):
    def test_network_and_http_failures_have_bounded_retries(self):
        for error in (TimeoutError(SECRET_URL), urllib.error.URLError(SECRET_URL),
                      http.client.BadStatusLine(SECRET_URL)):
            with self.subTest(error=type(error).__name__):
                opener, sleep = Mock(), Mock()
                opener.open.side_effect = error
                self.assertFalse(HEARTBEAT.report(SECRET_URL, opener=opener, sleep=sleep))
                self.assertEqual(opener.open.call_count, 3)
                self.assertEqual(sleep.call_count, 2)
                request = opener.open.call_args.args[0]
                self.assertEqual(request.get_method(), "POST")
                self.assertEqual(request.data, b"")
                self.assertEqual(opener.open.call_args.kwargs["timeout"], 5)

    def test_nonretryable_http_errors_do_not_redirect_or_retry(self):
        for status in (301, 302, 307, 308, 400, 401, 403, 404):
            with self.subTest(status=status):
                opener, sleep = Mock(), Mock()
                body = io.BytesIO(b"provider response containing credentials")
                opener.open.side_effect = urllib.error.HTTPError(SECRET_URL, status, "private error", {}, body)
                self.assertFalse(HEARTBEAT.report(SECRET_URL, opener=opener, sleep=sleep))
                opener.open.assert_called_once()
                sleep.assert_not_called()
                self.assertTrue(body.closed)


class LocalTLSTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.directory = tempfile.TemporaryDirectory(prefix="gregale-canary-heartbeat-")
        cls.addClassCleanup(cls.directory.cleanup)
        root = Path(cls.directory.name)
        cls.cert = str(root / "cert.pem")
        key = str(root / "key.pem")
        subprocess.run(["openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1",
                        "-subj", "/CN=localhost", "-addext", "subjectAltName=DNS:localhost",
                        "-keyout", key, "-out", cls.cert], check=True, capture_output=True)
        cls.paths = []

        class Handler(http.server.BaseHTTPRequestHandler):
            def do_POST(self):
                cls.paths.append(self.path)
                if self.path.endswith("redirect"):
                    self.send_response(302)
                    self.send_header("Location", "/must-not-receive-a-heartbeat")
                elif self.path.endswith("retry") and cls.paths.count(self.path) == 1:
                    self.send_response(503)
                elif self.path.endswith("fail"):
                    self.send_response(503)
                else:
                    self.send_response(204)
                self.end_headers()

            def log_message(self, *args):
                pass

        cls.server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.load_cert_chain(cls.cert, key)
        cls.server.socket = context.wrap_socket(cls.server.socket, server_side=True)
        cls.addClassCleanup(cls.server.server_close)
        thread = threading.Thread(target=cls.server.serve_forever, daemon=True)
        thread.start()
        cls.addClassCleanup(cls.server.shutdown)
        cls.base = f"https://localhost:{cls.server.server_port}/private-token/"

    def setUp(self):
        self.paths.clear()

    def send(self, path, trusted=True):
        # Trust only this disposable test certificate. Production uses the
        # platform's normal CA verification and never an insecure TLS option.
        with patch.dict(os.environ, {"SSL_CERT_FILE": self.cert if trusted else "/dev/null",
                                     "HTTPS_PROXY": "http://127.0.0.1:1", "NO_PROXY": ""}):
            return HEARTBEAT.report(self.base + path, sleep=lambda _: None)

    def test_success_uses_verified_tls_and_does_not_use_a_proxy(self):
        self.assertTrue(self.send("success"))
        self.assertEqual(self.paths, ["/private-token/success"])

    def test_transient_failure_can_recover(self):
        self.assertTrue(self.send("retry"))
        self.assertEqual(self.paths, ["/private-token/retry"] * 2)

    def test_persistent_failure_is_not_success(self):
        self.assertFalse(self.send("fail"))
        self.assertEqual(self.paths, ["/private-token/fail"] * 3)

    def test_redirect_does_not_reach_another_endpoint(self):
        self.assertFalse(self.send("redirect"))
        self.assertEqual(self.paths, ["/private-token/redirect"])

    def test_untrusted_certificate_is_rejected_before_posting(self):
        self.assertFalse(self.send("success", trusted=False))
        self.assertEqual(self.paths, [])


if __name__ == "__main__":
    unittest.main()
