#!/usr/bin/env python3
"""Tests for the compute-node serving probe (ADR-20261023124955 §5)."""

import contextlib
import errno
from functools import partial
import http.server
import io
import importlib.machinery
import importlib.util
import json
import os
from multiprocessing.connection import Connection
import socket
import ssl
from pathlib import Path
import sys
import tempfile
import threading
import time
import unittest
import urllib.request
from unittest.mock import Mock, patch


ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "deploy/ansible/roles/serving_probe/files/faas-serving-probe"
LOADER = importlib.machinery.SourceFileLoader("faas_serving_probe", str(SCRIPT))
SPEC = importlib.util.spec_from_loader(LOADER.name, LOADER)
MODULE = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = MODULE
LOADER.exec_module(MODULE)


class _Handler(http.server.BaseHTTPRequestHandler):
    # Path -> list of statuses served in order; the last one repeats.
    plan = {}
    bodies = {}
    delays = {}
    started = []
    disconnected = []
    requests = []
    lengths = {}

    def do_GET(self):
        request_body = self.rfile.read(int(self.headers.get("Content-Length", "0")))
        self.requests.append((self.command, self.path, request_body))
        self.started.append(self.path)
        statuses = self.plan.setdefault(self.path, [200])
        status = statuses.pop(0) if len(statuses) > 1 else statuses[0]
        body = self.bodies.get(self.path, b"ok")
        if isinstance(body, list):
            body = body.pop(0) if len(body) > 1 else body[0]
        delays = self.delays.get(self.path, (0, 0, 0))
        if isinstance(delays, list):
            delays = delays.pop(0) if len(delays) > 1 else delays[0]
        headers_delay, first_delay, byte_delay = delays
        try:
            time.sleep(headers_delay)
            self.send_response(status)
            if 300 <= status < 400:
                self.send_header("Location", "/elsewhere")
            self.send_header("Content-Length", str(self.lengths.get(self.path, len(body))))
            self.end_headers()
            time.sleep(first_delay)
            if byte_delay:
                for byte in body:
                    self.wfile.write(bytes([byte]))
                    self.wfile.flush()
                    time.sleep(byte_delay)
            else:
                self.wfile.write(body)
        except (BrokenPipeError, ConnectionResetError):
            self.disconnected.append(self.path)

    do_POST = do_GET

    def log_message(self, *args):
        pass


class _HTTPOpener:
    """Rewrites the probe's https URLs to the local plain-HTTP server."""

    def __init__(self, port):
        self.port = port
        self.inner = MODULE.default_opener()

    def open(self, req, timeout):
        path = "/" + req.full_url.split("/", 3)[3] if req.full_url.count("/") >= 3 else "/"
        local = urllib.request.Request(f"http://127.0.0.1:{self.port}{path}", data=req.data,
                                       headers=dict(req.header_items()), method=req.get_method())
        return self.inner.open(local, timeout=timeout)


class ServingProbeTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), _Handler)
        cls.thread = threading.Thread(target=cls.server.serve_forever, daemon=True)
        cls.thread.start()

    @classmethod
    def tearDownClass(cls):
        cls.server.shutdown()
        cls.server.server_close()

    def setUp(self):
        _Handler.plan = {}
        _Handler.bodies = {}
        _Handler.delays = {}
        _Handler.started = []
        _Handler.disconnected = []
        _Handler.requests = []
        _Handler.lengths = {}
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        (self.root / "creds").mkdir()
        (self.root / "creds" / "webhook_url").write_text("https://hooks.example/abc\n")
        (self.root / "state").mkdir()
        self.posts, self.beats, self.sleeps = [], [], []
        self.clock = 1_000_000.0

    def tearDown(self):
        self.temp.cleanup()

    def env(self, *paths, heartbeat=True):
        if heartbeat:
            (self.root / "creds" / "heartbeat_url").write_text("https://hc.example/ping/1\n")
        return {
            "FAAS_SERVING_PROBE_URLS": " ".join(f"https://probe.example{p}" for p in paths),
            "FAAS_SERVING_PROBE_TIMEOUT_SECONDS": "5",
            "FAAS_SERVING_PROBE_RETRY_DELAY_SECONDS": "30",
            "FAAS_SERVING_PROBE_NODE": "fsn-2",
            "FAAS_SERVING_PROBE_TEXTFILE": str(self.root / "metrics" / "serving-probe.prom"),
            "STATE_DIRECTORY": str(self.root / "state"),
            "CREDENTIALS_DIRECTORY": str(self.root / "creds"),
        }

    def run_probe(self, env, advance=0, monotonic=time.monotonic):
        self.clock += advance
        return MODULE.run(
            env=env,
            opener=_HTTPOpener(self.server.server_address[1]),
            sleep=self.sleeps.append,
            now=lambda: self.clock,
            post=lambda url, payload: self.posts.append((url, payload)),
            beat=self.beats.append,
            monotonic=monotonic,
        )

    def metrics(self):
        return (self.root / "metrics" / "serving-probe.prom").read_text()

    def test_healthy_run_pings_heartbeat_and_stays_quiet(self):
        _Handler.plan = {"/ok": [200]}
        self.assertEqual(self.run_probe(self.env("/ok")), 0)
        self.assertEqual(self.posts, [])
        self.assertEqual(self.beats, ["https://hc.example/ping/1"])
        self.assertEqual(self.sleeps, [])
        self.assertIn('faas_serving_probe_success{url="https://probe.example/ok"} 1', self.metrics())
        self.assertIn('faas_serving_probe_http_status{url="https://probe.example/ok"} 200', self.metrics())

    def test_one_failed_attempt_is_retried_not_reported(self):
        _Handler.plan = {"/flaky": [503, 200]}
        self.assertEqual(self.run_probe(self.env("/flaky")), 0)
        self.assertEqual(self.sleeps, [30.0])
        self.assertEqual(self.posts, [])
        self.assertEqual(len(self.beats), 1)

    def test_exact_response_body_passes_and_reports_validation_enabled(self):
        env = self.env("/ok")
        env["FAAS_SERVING_PROBE_EXPECTED_BODY"] = "ok"
        self.assertEqual(self.run_probe(env), 0)
        self.assertEqual(len(self.beats), 1)
        self.assertIn("faas_serving_probe_content_check_enabled 1", self.metrics())
        self.assertIn("faas_serving_probe_first_byte_budget_seconds 5", self.metrics())

    def test_status_only_configuration_remains_visible(self):
        self.assertEqual(self.run_probe(self.env("/ok")), 0)
        self.assertIn("faas_serving_probe_content_check_enabled 0", self.metrics())

    def test_wrong_empty_and_prefix_only_bodies_fail_despite_http_200(self):
        for body in (b"parking page", b"", b"ok extra", b"ok" + b"x" * 10000):
            with self.subTest(body_length=len(body)):
                self.posts.clear()
                _Handler.bodies = {"/wrong": body}
                env = self.env("/wrong")
                env["FAAS_SERVING_PROBE_EXPECTED_BODY"] = "ok"
                # Each case starts a new incident.
                (self.root / "state" / "state.json").unlink(missing_ok=True)
                self.assertEqual(self.run_probe(env), 1)
                self.assertIn("response body mismatch", self.posts[0][1]["text"])
                self.assertIn('faas_serving_probe_http_status{url="https://probe.example/wrong"} 200', self.metrics())
                self.assertIn('faas_serving_probe_success{url="https://probe.example/wrong"} 0', self.metrics())
                self.assertIn('faas_serving_probe_failure_code{url="https://probe.example/wrong"} 7', self.metrics())
                self.assertEqual(self.beats, [])

    def test_body_mismatch_can_recover_on_the_second_attempt(self):
        _Handler.bodies = {"/flaky": [b"wrong", b"ok"]}
        env = self.env("/flaky")
        env["FAAS_SERVING_PROBE_EXPECTED_BODY"] = "ok"
        self.assertEqual(self.run_probe(env), 0)
        self.assertEqual(self.sleeps, [30.0])
        self.assertEqual(self.posts, [])
        self.assertEqual(len(self.beats), 1)

    def test_utf8_expected_body(self):
        _Handler.bodies = {"/utf8": "sağlıklı".encode()}
        env = self.env("/utf8")
        env["FAAS_SERVING_PROBE_EXPECTED_BODY"] = "sağlıklı"
        self.assertEqual(self.run_probe(env), 0)

    def test_slow_200_is_retried_and_does_not_feed_the_heartbeat(self):
        env = self.env("/slow")
        env["FAAS_SERVING_PROBE_MAX_FIRST_BYTE_SECONDS"] = "2"
        ticks = iter((0.0, 3.0, 4.0, 7.0))
        # Unit-test measurement boundaries with a synthetic clock; actual
        # deadline cancellation is exercised against real I/O below.
        with patch.object(MODULE, "probe_once", MODULE._probe_request):
            self.assertEqual(self.run_probe(env, monotonic=lambda: next(ticks)), 1)
        self.assertEqual(self.sleeps, [30.0])
        self.assertIn("first byte exceeded 2s budget", self.posts[0][1]["text"])
        self.assertEqual(self.beats, [])
        self.assertIn('faas_serving_probe_first_byte_seconds{url="https://probe.example/slow"} 3.000', self.metrics())
        self.assertIn('faas_serving_probe_failure_code{url="https://probe.example/slow"} 6', self.metrics())

    def test_latency_budget_boundary_and_retry_recovery(self):
        for elapsed in (2.0, 3.0):
            with self.subTest(elapsed=elapsed):
                env = self.env("/slow")
                env["FAAS_SERVING_PROBE_MAX_FIRST_BYTE_SECONDS"] = "2"
                ticks = iter((0.0, elapsed, 4.0, 6.0))
                with patch.object(MODULE, "probe_once", MODULE._probe_request):
                    self.assertEqual(self.run_probe(env, monotonic=lambda: next(ticks)), 0)
                self.assertEqual(self.posts, [])

    def test_body_validation_is_bounded_and_preserves_first_byte_timing(self):
        assert_bounded = lambda size: self.assertLessEqual(size, MODULE.MAX_EXPECTED_BODY_BYTES)

        class Response(io.BytesIO):
            status = 200

            def read(self, size=-1):
                self.assert_bounded(size)
                return super().read(size)

        class Opener:
            def open(self, req, timeout):
                response = Response(b"ok")
                response.assert_bounded = assert_bounded
                return response

        env = self.env("/slow-body")
        env["FAAS_SERVING_PROBE_EXPECTED_BODY"] = "ok"
        cfg = MODULE.Config(env)
        for finished, error in ((4.0, ""), (6.0, "response body exceeded attempt timeout")):
            with self.subTest(finished=finished):
                ticks = iter((0.0, 1.0, finished))
                status, seconds, observed_error, _ = MODULE._probe_request(cfg.urls[0], cfg, Opener(), lambda: next(ticks))
                self.assertEqual((status, seconds, observed_error), (200, 1.0, error))

    def test_invalid_check_configuration_is_rejected(self):
        cases = {
            "FAAS_SERVING_PROBE_TIMEOUT_SECONDS": ("0", "nan", "inf"),
            "FAAS_SERVING_PROBE_RETRY_DELAY_SECONDS": ("-1", "nan", "inf"),
            "FAAS_SERVING_PROBE_MAX_FIRST_BYTE_SECONDS": ("0", "-1", "6", "nan", "inf"),
            "FAAS_SERVING_PROBE_EXPECTED_BODY": ("x" * 4097, "é" * 2049, "ok\n", "ok\x00"),
        }
        for field, values in cases.items():
            for value in values:
                with self.subTest(field=field, value_length=len(value)):
                    env = self.env("/ok")
                    env[field] = value
                    with self.assertRaises(ValueError):
                        MODULE.Config(env)

    def test_failure_notifies_once_then_hourly_then_recovers(self):
        env = self.env("/down")
        _Handler.plan = {"/down": [502]}
        self.assertEqual(self.run_probe(env), 1)
        self.assertEqual(len(self.posts), 1)
        url, payload = self.posts[0]
        self.assertEqual(url, "https://hooks.example/abc")
        self.assertIn("fsn-2", payload["text"])
        self.assertIn("FAILING url_id=" + MODULE.url_id("https://probe.example/down") + ": HTTP 502 after 2 attempts", payload["text"])
        self.assertEqual(self.beats, [], "a failing run must not feed the heartbeat")

        # Five minutes later: still failing, no repeat.
        self.assertEqual(self.run_probe(env, advance=300), 1)
        self.assertEqual(len(self.posts), 1)

        # An hour after the first notice: one reminder with the duration.
        self.assertEqual(self.run_probe(env, advance=3300), 1)
        self.assertEqual(len(self.posts), 2)
        self.assertIn("failing for 60 min", self.posts[1][1]["text"])

        _Handler.plan = {"/down": [200]}
        self.assertEqual(self.run_probe(env, advance=300), 0)
        self.assertEqual(len(self.posts), 3)
        self.assertIn("RECOVERED url_id=" + MODULE.url_id("https://probe.example/down"), self.posts[2][1]["text"])
        self.assertEqual(len(self.beats), 1)

    def test_redirect_is_a_failure(self):
        _Handler.plan = {"/moved": [301]}
        self.assertEqual(self.run_probe(self.env("/moved")), 1)
        self.assertIn("HTTP 301", self.posts[0][1]["text"])

    def test_unreachable_host_is_a_failure(self):
        env = self.env("/x")
        port = self.server.server_address[1]

        class Refused(_HTTPOpener):
            def open(self, req, timeout):
                local = urllib.request.Request("http://127.0.0.1:1/x")
                return self.inner.open(local, timeout=timeout)

        self.clock += 1
        code = MODULE.run(
            env=env, opener=Refused(port), sleep=self.sleeps.append, now=lambda: self.clock,
            post=lambda u, p: self.posts.append((u, p)), beat=self.beats.append,
        )
        self.assertEqual(code, 1)
        self.assertIn('faas_serving_probe_http_status{url="https://probe.example/x"} 0', self.metrics())

    def test_undelivered_notice_is_retried_next_run(self):
        env = self.env("/down")
        _Handler.plan = {"/down": [500]}

        def broken(url, payload):
            raise OSError("webhook unreachable")

        self.clock += 1
        code = MODULE.run(
            env=env, opener=_HTTPOpener(self.server.server_address[1]), sleep=self.sleeps.append,
            now=lambda: self.clock, post=broken, beat=self.beats.append,
        )
        self.assertEqual(code, 1)
        self.assertEqual(self.run_probe(env, advance=300), 1)
        self.assertEqual(len(self.posts), 1, "the notice that failed to send must go out on the next run")

    def test_rejects_non_https_and_empty_url_lists(self):
        for urls in ("", "http://plain.example/", "https://ok.example/ ftp://x"):
            with self.subTest(urls=urls):
                env = self.env("/ok")
                env["FAAS_SERVING_PROBE_URLS"] = urls
                with self.assertRaises(ValueError):
                    MODULE.Config(env)

    def test_metric_labels_are_escaped(self):
        self.assertEqual(MODULE._label('a"b\\c\nd'), 'a\\"b\\\\c\\nd')

    def test_state_file_is_json(self):
        _Handler.plan = {"/down": [500]}
        self.run_probe(self.env("/down"))
        state = json.loads((self.root / "state" / "state.json").read_text())
        self.assertFalse(state["https://probe.example/down"]["ok"])

    def test_structured_transport_failures_do_not_parse_or_expose_exception_text(self):
        secret = "private-error-with-token"
        cases = [
            (urllib.error.URLError(socket.gaierror(socket.EAI_NONAME, secret)), "dns", 1),
            (ConnectionRefusedError(errno.ECONNREFUSED, secret), "connection", 2),
            (urllib.error.URLError(OSError(errno.ENETUNREACH, secret)), "connection", 2),
            (urllib.error.URLError(ssl.SSLCertVerificationError(1, secret)), "tls", 3),
            (urllib.error.URLError(TimeoutError(secret)), "timeout", 4),
            (RuntimeError(secret), "unknown", 8),
            (urllib.error.URLError("DNS TLS timeout " + secret), "unknown", 8),
        ]
        cfg = MODULE.Config(self.env("/down"))
        for error, code, metric_code in cases:
            with self.subTest(code=code, error=type(error).__name__):
                opener = Mock()
                opener.open.side_effect = error
                result = MODULE.probe(cfg.urls[0], cfg, opener, self.sleeps.append)
                self.assertFalse(result["ok"])
                self.assertEqual((result["failure_code"], result["first_failure_code"], result["attempts"]), (code, code, 2))
                self.assertEqual(result["status"], 0)
                self.assertNotIn(secret, result["error"])
                MODULE.write_metrics(cfg.textfile, {cfg.urls[0]: result}, self.clock, cfg)
                self.assertIn(f'faas_serving_probe_failure_code{{url="https://probe.example/down"}} {metric_code}', self.metrics())

    def test_retry_diagnostics_retain_the_first_failure_without_changing_success(self):
        _Handler.plan = {"/flaky": [503, 200]}
        self.assertEqual(self.run_probe(self.env("/flaky")), 0)
        self.assertIn('faas_serving_probe_failure_code{url="https://probe.example/flaky"} 0', self.metrics())
        self.assertIn('faas_serving_probe_first_failure_code{url="https://probe.example/flaky"} 5', self.metrics())
        self.assertIn('faas_serving_probe_attempts{url="https://probe.example/flaky"} 2', self.metrics())
        self.assertEqual(self.posts, [])
        self.assertEqual(len(self.beats), 1)

    def test_timeout_after_headers_retains_observed_http_status(self):
        class Response(io.BytesIO):
            status = 200

            def read(self, size=-1):
                raise TimeoutError("private-error-with-token")

        opener = Mock()
        opener.open.return_value = Response()
        cfg = MODULE.Config(self.env("/down"))
        status, _, error, code = MODULE.probe_once(cfg.urls[0], cfg, opener)
        self.assertEqual((status, code), (200, "timeout"))
        self.assertNotIn("private-error-with-token", error)

    def test_body_read_error_preserves_first_byte_timing(self):
        class Response(io.BytesIO):
            status = 200

            def read(self, size=-1):
                if self.tell():
                    raise TimeoutError("private-error-with-token")
                return super().read(size)

        opener = Mock()
        opener.open.return_value = Response(b"ok")
        cfg = MODULE.Config(self.env("/body-timeout"))
        cfg.expected_body = b"ok"
        ticks = iter((0.0, 1.0, 4.0))
        status, seconds, _, code = MODULE._probe_request(cfg.urls[0], cfg, opener, lambda: next(ticks))
        self.assertEqual((status, seconds, code), (200, 1.0, "timeout"))

    def test_logs_and_notifications_use_url_ids_and_fixed_errors(self):
        env = self.env("/private?token=private-query-token")
        opener = Mock()
        opener.open.side_effect = urllib.error.URLError("private-provider-error")
        output = io.StringIO()
        with contextlib.redirect_stdout(output), contextlib.redirect_stderr(output):
            code = MODULE.run(env=env, opener=opener, sleep=self.sleeps.append, now=lambda: self.clock,
                              post=lambda url, payload: self.posts.append((url, payload)))
        self.assertEqual(code, 1)
        text = output.getvalue() + json.dumps([payload for _, payload in self.posts])
        self.assertNotIn("private-query-token", text)
        self.assertNotIn("private-provider-error", text)
        self.assertIn(MODULE.url_id(env["FAAS_SERVING_PROBE_URLS"]), text)
        self.assertIn("failure_code=unknown", output.getvalue())

    def test_notification_transport_errors_are_redacted(self):
        env = self.env("/down")
        _Handler.plan = {"/down": [500]}
        output = io.StringIO()
        with contextlib.redirect_stdout(output), contextlib.redirect_stderr(output):
            code = MODULE.run(env=env, opener=_HTTPOpener(self.server.server_address[1]),
                              sleep=self.sleeps.append, now=lambda: self.clock,
                              post=Mock(side_effect=OSError("https://webhook.invalid/private-secret")))
        self.assertEqual(code, 1)
        self.assertIn("notification_submission_failed", output.getvalue())
        self.assertNotIn("private-secret", output.getvalue())

    def assert_worker_reaped(self, pid_path):
        pid = int(pid_path.read_text())
        with self.assertRaises(ChildProcessError):
            os.waitpid(pid, os.WNOHANG)
        with self.assertRaises(ProcessLookupError):
            os.kill(pid, 0)

    def test_total_deadline_cancels_a_blocked_resolver_and_reaps_worker(self):
        pid_path = self.root / "worker.pid"

        def blocked_resolver(*args, **kwargs):
            pid_path.write_text(str(os.getpid()))
            time.sleep(30)
            raise AssertionError("resolver must have been cancelled")

        cfg = MODULE.Config(self.env("/blocked-dns"))
        cfg.timeout = 1
        started = time.monotonic()
        with patch.object(socket, "getaddrinfo", blocked_resolver):
            status, seconds, error, code = MODULE.probe_once(cfg.urls[0], cfg, MODULE.default_opener())
        elapsed = time.monotonic() - started
        self.assertEqual((status, code), (0, "timeout"))
        self.assertIn("timeout", error)
        self.assertGreaterEqual(seconds, cfg.timeout)
        self.assertLess(elapsed, 3, "the resolver must not outlive the attempt")
        self.assert_worker_reaped(pid_path)

    def test_total_deadline_bounds_stalled_headers_first_byte_and_slow_body(self):
        cases = (("headers", (30, 0, 0), 0),
                 ("first-byte", (0, 30, 0), 200),
                 # Every byte arrives within the socket timeout, but the
                 # complete body arrives after the total attempt deadline.
                 ("slow-body", (0, 0, 0.35), 200),
                 ("shared-phase-budget", (0.4, 0.3, 0.15), 200))
        for name, delays, expected_status in cases:
            with self.subTest(phase=name):
                path = "/deadline-" + name
                _Handler.delays[path] = delays
                _Handler.bodies[path] = b"abcdefgh"
                cfg = MODULE.Config(self.env(path))
                cfg.timeout = 1
                cfg.expected_body = b"abcdefgh"
                started = time.monotonic()
                status, seconds, _, code = MODULE.probe_once(
                    cfg.urls[0], cfg, _HTTPOpener(self.server.server_address[1]))
                elapsed = time.monotonic() - started
                self.assertEqual((status, code), (expected_status, "timeout"))
                self.assertGreaterEqual(elapsed, cfg.timeout)
                self.assertLess(elapsed, 2.5)
                if name in ("slow-body", "shared-phase-budget"):
                    self.assertLess(seconds, cfg.timeout, "retain observed first-byte time when the body stalls")

        # A killed worker closes its real response socket instead of leaving
        # a blocked background thread alive for the rest of the run.
        until = time.monotonic() + 2
        while "/deadline-slow-body" not in _Handler.disconnected and time.monotonic() < until:
            time.sleep(0.02)
        self.assertIn("/deadline-slow-body", _Handler.disconnected)

    def test_total_deadline_stops_stalled_tls_and_closes_its_socket(self):
        received = bytearray()
        closed = threading.Event()
        with socket.socket() as listener:
            listener.bind(("127.0.0.1", 0))
            listener.listen()
            listener.settimeout(3)

            def stall_tls():
                with listener.accept()[0] as connection:
                    connection.settimeout(3)
                    while data := connection.recv(4096):
                        received.extend(data)
                    closed.set()  # EOF after kill

            thread = threading.Thread(target=stall_tls, daemon=True)
            thread.start()
            cfg = MODULE.Config(self.env("/tls"))
            cfg.timeout = 1
            opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), MODULE._NoRedirect())
            status, _, _, code = MODULE.probe_once(
                f"https://127.0.0.1:{listener.getsockname()[1]}/tls", cfg, opener)
            self.assertEqual((status, code), (0, "timeout"))
            thread.join(timeout=3)
            self.assertFalse(thread.is_alive(), "the TLS worker must release its socket")
            self.assertTrue(received.startswith(b"\x16\x03"))  # ClientHello
            self.assertTrue(closed.is_set())

    def test_late_valid_response_cannot_pass_and_worker_is_reaped(self):
        pid_path = self.root / "late-worker.pid"
        finished_path = self.root / "late-worker-finished"

        class LateOpener:
            def open(self, req, timeout):
                pid_path.write_text(str(os.getpid()))
                time.sleep(1.6)
                finished_path.write_text("late success")
                response = io.BytesIO(b"ok")
                response.status = 200
                return response

        cfg = MODULE.Config(self.env("/late"))
        cfg.timeout = 1
        _, _, _, code = MODULE.probe_once(cfg.urls[0], cfg, LateOpener())
        self.assertEqual(code, "timeout")
        self.assert_worker_reaped(pid_path)
        self.assertFalse(finished_path.exists())

    def test_expired_attempts_record_timeout_and_continue_to_later_urls(self):
        _Handler.delays["/stuck"] = (0, 0, 0.35)
        _Handler.bodies["/stuck"] = b"abcdefgh"
        _Handler.bodies["/later"] = b"abcdefgh"
        env = self.env("/stuck", "/later")
        env["FAAS_SERVING_PROBE_TIMEOUT_SECONDS"] = "1"
        env["FAAS_SERVING_PROBE_EXPECTED_BODY"] = "abcdefgh"
        started = time.monotonic()
        self.assertEqual(self.run_probe(env), 1)
        self.assertLess(time.monotonic() - started, 4)
        self.assertEqual(_Handler.started.count("/stuck"), 2)
        self.assertEqual(_Handler.started.count("/later"), 1)
        self.assertEqual(self.sleeps, [30.0])
        self.assertEqual(self.beats, [])
        self.assertEqual(len(self.posts), 1)
        self.assertIn("timeout", self.posts[0][1]["text"])
        for field, value in (("failure_code", 4), ("first_failure_code", 4), ("attempts", 2),
                             ("http_status", 200), ("success", 0)):
            self.assertIn(f'faas_serving_probe_{field}{{url="https://probe.example/stuck"}} {value}', self.metrics())
        self.assertIn('faas_serving_probe_success{url="https://probe.example/later"} 1', self.metrics())
        self.assertIn("faas_serving_probe_last_run_timestamp_seconds 1000000", self.metrics())

    def test_timeout_can_recover_on_retry_with_first_failure_retained(self):
        _Handler.delays["/retry-timeout"] = [(0, 0, 0.35), (0, 0, 0)]
        _Handler.bodies["/retry-timeout"] = b"abcdefgh"
        env = self.env("/retry-timeout")
        env["FAAS_SERVING_PROBE_TIMEOUT_SECONDS"] = "1"
        env["FAAS_SERVING_PROBE_EXPECTED_BODY"] = "abcdefgh"
        self.assertEqual(self.run_probe(env), 0)
        self.assertEqual(self.sleeps, [30.0])
        self.assertEqual(self.posts, [])
        self.assertEqual(len(self.beats), 1)
        for field, value in (("failure_code", 0), ("first_failure_code", 4), ("attempts", 2), ("success", 1)):
            self.assertIn(f'faas_serving_probe_{field}{{url="https://probe.example/retry-timeout"}} {value}', self.metrics())

    def test_worker_exit_and_parent_cancellation_always_release_resources(self):
        for cancel in (False, True):
            with self.subTest(parent_cancelled=cancel):
                pid_path = self.root / "exited-worker.pid"
                cfg = MODULE.Config(self.env("/worker-exit"))
                cfg.timeout = 1

                class ExitingOpener:
                    def open(self, req, timeout):
                        pid_path.write_text(str(os.getpid()))
                        if cancel:
                            class Response(io.BytesIO):
                                status = 200

                                def read(self, size=-1):
                                    time.sleep(30)

                            return Response()
                        os._exit(23)

                if cancel:
                    original_poll = Connection.poll

                    def cancelled_poll(connection, timeout):
                        self.assertTrue(original_poll(connection, timeout))
                        raise KeyboardInterrupt

                    with patch.object(Connection, "poll", cancelled_poll), self.assertRaises(KeyboardInterrupt):
                        MODULE.probe_once(cfg.urls[0], cfg, ExitingOpener())
                else:
                    self.assertEqual(MODULE.probe_once(cfg.urls[0], cfg, ExitingOpener())[3], "unknown")
                self.assert_worker_reaped(pid_path)

    def test_successful_workers_are_reaped_without_descriptor_leaks(self):
        pid_path = self.root / "successful-worker.pid"

        class Opener:
            def open(self, req, timeout):
                pid_path.write_text(str(os.getpid()))
                response = io.BytesIO(b"ok")
                response.status = 200
                return response

        cfg = MODULE.Config(self.env("/ok"))
        descriptors_before = len(list(Path("/proc/self/fd").iterdir()))
        for _ in range(10):
            self.assertEqual(MODULE.probe_once(cfg.urls[0], cfg, Opener())[3], "none")
            self.assert_worker_reaped(pid_path)
        self.assertEqual(len(list(Path("/proc/self/fd").iterdir())), descriptors_before)

    def notification_submit(self, channel, path, timeout=1):
        opener = _HTTPOpener(self.server.server_address[1])
        url = "https://provider.example" + path
        if channel == "webhook":
            return MODULE.post_json(url, {"text": "test notification"}, timeout=timeout, opener=opener)
        return MODULE.ping(url, timeout=timeout, opener=opener)

    def assert_notification_metric(self, field, channel, value):
        self.assertIn(f'faas_serving_probe_notification_{field}{{channel="{channel}"}} {value:.6f}\n', self.metrics())

    def test_notification_requests_accept_complete_2xx_and_preserve_post_payload(self):
        _Handler.plan = {"/post": [202], "/ping": [204]}
        _Handler.bodies["/ping"] = b""
        self.notification_submit("webhook", "/post")
        self.notification_submit("heartbeat", "/ping")
        self.assertEqual(_Handler.requests, [
            ("POST", "/post", b'{"text": "test notification"}'), ("GET", "/ping", b"")])

    def test_notification_credentials_require_https_before_network_io(self):
        for url in ("http://provider.example/private-token", "ftp://provider.example/private-token", "file:///private-token"):
            with self.subTest(scheme=url.split(":")[0]):
                opener = Mock()
                _, _, error, code = MODULE._notification_request(url, {"text": "test"}, 1, opener)
                opener.open.assert_not_called()
                self.assertEqual(code, "unknown")
                self.assertNotIn("private-token", error)

    def test_notification_requests_reject_redirects_and_non_2xx(self):
        for channel in ("webhook", "heartbeat"):
            for status in (301, 302, 303, 307, 308, 400, 429, 503):
                with self.subTest(channel=channel, status=status):
                    path = f"/{channel}-{status}?private-credential-token"
                    _Handler.plan[path] = [status]
                    with self.assertRaises(MODULE.NotificationError) as caught:
                        self.notification_submit(channel, path)
                    self.assertEqual(caught.exception.failure_code, "http_status")
                    self.assertNotIn("private-credential-token", str(caught.exception))
        self.assertNotIn("/elsewhere", _Handler.started, "credential requests must never follow a redirect")

    def test_notification_response_reads_are_bounded_and_limits_are_exact(self):
        for length in (0, MODULE.MAX_NOTIFICATION_RESPONSE_BYTES, MODULE.MAX_NOTIFICATION_RESPONSE_BYTES + 1, 10000):
            with self.subTest(length=length):
                reads = []

                class Response(io.BytesIO):
                    status = 200

                    def read(self, size=-1):
                        reads.append(size)
                        return super().read(size)

                opener = Mock()
                opener.open.return_value = Response(b"x" * length)
                _, _, error, code = MODULE._notification_request("https://provider.example/private", None, 1, opener)
                self.assertEqual(reads, [MODULE.MAX_NOTIFICATION_RESPONSE_BYTES + 1])
                self.assertEqual(code, "none" if length <= MODULE.MAX_NOTIFICATION_RESPONSE_BYTES else "unknown")
                self.assertEqual(bool(error), length > MODULE.MAX_NOTIFICATION_RESPONSE_BYTES)
                self.assertTrue(opener.open.return_value.closed)

        _Handler.bodies["/oversized"] = b"x" * (MODULE.MAX_NOTIFICATION_RESPONSE_BYTES + 1)
        _Handler.lengths["/truncated"] = 10  # The fixture sends only two bytes.
        for path in ("/oversized", "/truncated"):
            with self.subTest(path=path), self.assertRaises(MODULE.NotificationError) as caught:
                self.notification_submit("webhook", path)
            self.assertEqual(caught.exception.failure_code, "unknown")

    def test_notification_deadlines_stop_stalled_and_streaming_providers(self):
        for channel in ("webhook", "heartbeat"):
            for phase, delays in (("headers", (30, 0, 0)), ("body", (0, 30, 0)),
                                  ("stream", (0, 0, 0.35)), ("shared", (0.4, 0.3, 0.15))):
                with self.subTest(channel=channel, phase=phase):
                    path = f"/{channel}-{phase}"
                    _Handler.delays[path] = delays
                    _Handler.bodies[path] = b"abcdefgh"
                    started = time.monotonic()
                    with self.assertRaises(MODULE.NotificationError) as caught:
                        self.notification_submit(channel, path)
                    self.assertEqual(caught.exception.failure_code, "timeout")
                    self.assertLess(time.monotonic() - started, 2.5)

    def test_notification_deadlines_cancel_blocked_dns_and_reap_workers(self):
        pid_path = self.root / "notification-worker.pid"

        def blocked_resolver(*args, **kwargs):
            pid_path.write_text(str(os.getpid()))
            time.sleep(30)

        for submit, args in ((MODULE.post_json, ({"text": "test"},)), (MODULE.ping, ())):
            with self.subTest(submit=submit.__name__):
                with patch.object(socket, "getaddrinfo", blocked_resolver), self.assertRaises(MODULE.NotificationError) as caught:
                    submit("https://provider.example/private-token", *args, timeout=1)
                self.assertEqual(caught.exception.failure_code, "timeout")
                self.assertNotIn("private-token", str(caught.exception))
                self.assert_worker_reaped(pid_path)

    def test_notification_metrics_distinguish_disabled_skipped_and_unknown(self):
        self.assertEqual(self.run_probe(self.env("/ok", heartbeat=False)), 0)
        for channel, enabled in (("webhook", 1), ("heartbeat", 0)):
            self.assert_notification_metric("enabled", channel, enabled)
            self.assert_notification_metric("attempted", channel, 0)
            self.assertNotIn(f'notification_last_attempt_success{{channel="{channel}"}}', self.metrics())
            self.assertNotIn(f'notification_last_success_timestamp_seconds{{channel="{channel}"}}', self.metrics())

    def test_notification_history_survives_quiet_runs_without_inventing_attempts(self):
        _Handler.plan["/down"] = [503]
        env = self.env("/down")
        self.assertEqual(self.run_probe(env), 1)
        self.assert_notification_metric("last_success_timestamp_seconds", "webhook", self.clock)
        self.assertEqual(self.run_probe(env, advance=300), 1)
        self.assertEqual(len(self.posts), 1)
        self.assert_notification_metric("attempted", "webhook", 0)
        self.assert_notification_metric("last_attempt_timestamp_seconds", "webhook", self.clock - 300)
        self.assert_notification_metric("last_success_timestamp_seconds", "webhook", self.clock - 300)
        self.assert_notification_metric("last_attempt_success", "webhook", 1)
        self.assert_notification_metric("attempted", "heartbeat", 0)
        self.assertNotIn('notification_last_attempt_success{channel="heartbeat"}', self.metrics())

    def test_notification_publication_does_not_advance_the_serving_run_clock(self):
        probe_finished = self.clock

        def delayed_heartbeat(url):
            self.clock += 5

        env = self.env("/ok")
        self.assertEqual(MODULE.run(env=env, opener=_HTTPOpener(self.server.server_address[1]),
                                   now=lambda: self.clock, beat=delayed_heartbeat), 0)
        self.assertIn(f"faas_serving_probe_last_run_timestamp_seconds {probe_finished:.0f}\n", self.metrics())
        self.assert_notification_metric("last_success_timestamp_seconds", "heartbeat", probe_finished + 5)

    def test_heartbeat_failure_preserves_serving_result_and_previous_success(self):
        env = self.env("/ok")
        self.assertEqual(self.run_probe(env), 0)
        first_success = self.clock
        self.clock += 300
        output = io.StringIO()
        with contextlib.redirect_stderr(output):
            code = MODULE.run(env=env, opener=_HTTPOpener(self.server.server_address[1]),
                              now=lambda: self.clock, beat=Mock(side_effect=TimeoutError("private-provider-error")))
        self.assertEqual(code, 0, "serving exit policy stays independent of heartbeat transport")
        self.assertIn('faas_serving_probe_success{url="https://probe.example/ok"} 1', self.metrics())
        self.assert_notification_metric("attempted", "heartbeat", 1)
        self.assert_notification_metric("last_attempt_success", "heartbeat", 0)
        self.assert_notification_metric("last_attempt_failure_code", "heartbeat", 4)
        self.assert_notification_metric("last_attempt_timestamp_seconds", "heartbeat", self.clock)
        self.assert_notification_metric("last_success_timestamp_seconds", "heartbeat", first_success)
        self.assertNotIn("private-provider-error", output.getvalue())

        _Handler.plan["/ok"] = [503]
        self.assertEqual(self.run_probe(env, advance=300), 1)
        self.assert_notification_metric("attempted", "heartbeat", 0)
        self.assert_notification_metric("last_attempt_success", "heartbeat", 0)
        self.assert_notification_metric("last_success_timestamp_seconds", "heartbeat", first_success)

    def test_failed_webhook_is_recorded_and_retried_until_a_successful_submission(self):
        _Handler.plan = {"/down": [503], "/notify": [503, 200]}
        env = self.env("/down")
        post = partial(MODULE.post_json, timeout=1, opener=_HTTPOpener(self.server.server_address[1]))
        (self.root / "creds" / "webhook_url").write_text("https://provider.example/notify")

        def run():
            return MODULE.run(env=env, opener=_HTTPOpener(self.server.server_address[1]),
                              sleep=self.sleeps.append, now=lambda: self.clock, post=post, beat=self.beats.append)

        self.assertEqual(run(), 1)
        self.assert_notification_metric("last_attempt_success", "webhook", 0)
        self.assert_notification_metric("last_attempt_failure_code", "webhook", 5)
        self.assertNotIn('notification_last_success_timestamp_seconds{channel="webhook"}', self.metrics())
        self.assertFalse((self.root / "state" / "state.json").exists(), "an undelivered incident must stay due")
        self.assertTrue((self.root / "state" / "notifications.json").exists(), "failed transport evidence is persisted")
        self.clock += 300
        self.assertEqual(run(), 1)
        self.assertEqual(_Handler.started.count("/notify"), 2)
        self.assert_notification_metric("last_attempt_success", "webhook", 1)
        self.assert_notification_metric("last_attempt_failure_code", "webhook", 0)
        self.assert_notification_metric("last_success_timestamp_seconds", "webhook", self.clock)
        self.assertTrue((self.root / "state" / "state.json").exists())
        self.assertEqual(self.beats, [])

    def test_recovery_webhook_timeout_does_not_block_heartbeat_or_final_metrics(self):
        _Handler.plan["/app"] = [503]
        env = self.env("/app")
        self.assertEqual(self.run_probe(env), 1)
        _Handler.plan["/app"] = [200]
        _Handler.delays["/notify"] = (0, 30, 0)
        (self.root / "creds" / "webhook_url").write_text("https://provider.example/notify")
        (self.root / "creds" / "heartbeat_url").write_text("https://provider.example/beat")
        opener = _HTTPOpener(self.server.server_address[1])

        def submit(url, payload):
            self.assertIn('faas_serving_probe_success{url="https://probe.example/app"} 1', self.metrics(),
                          "serving results must be published before waiting for providers")
            MODULE.post_json(url, payload, timeout=1, opener=opener)

        started = time.monotonic()
        code = MODULE.run(env=env, opener=opener, now=lambda: self.clock, post=submit,
                          beat=partial(MODULE.ping, timeout=1, opener=opener))
        self.assertLess(time.monotonic() - started, 3)
        self.assertEqual(code, 1)
        self.assert_notification_metric("last_attempt_failure_code", "webhook", 4)
        self.assert_notification_metric("last_attempt_success", "heartbeat", 1)
        self.assertIn("/beat", _Handler.started)
        self.assertFalse(json.loads((self.root / "state" / "state.json").read_text())["https://probe.example/app"]["ok"])

    def test_notification_credentials_never_appear_in_metrics_logs_or_history(self):
        env = self.env("/down")
        _Handler.plan["/down"] = [503]
        credential = "https://provider.example/private-credential-token"
        (self.root / "creds" / "webhook_url").write_text(credential)
        output = io.StringIO()
        with contextlib.redirect_stderr(output):
            self.assertEqual(MODULE.run(env=env, opener=_HTTPOpener(self.server.server_address[1]),
                                       sleep=self.sleeps.append, now=lambda: self.clock,
                                       post=Mock(side_effect=OSError(credential))), 1)
        state_path = self.root / "state" / "notifications.json"
        text = self.metrics() + output.getvalue() + state_path.read_text()
        self.assertNotIn("private-credential-token", text)
        self.assertNotIn("provider.example", text)
        self.assertEqual(state_path.stat().st_mode & 0o777, 0o600)

    def test_credential_rotation_and_disable_remove_previous_submission_history(self):
        env = self.env("/app")
        self.assertEqual(self.run_probe(env), 0)  # Establish heartbeat success.
        _Handler.plan["/app"] = [503]
        self.assertEqual(self.run_probe(env, advance=300), 1)  # Establish webhook success.
        for channel in ("webhook", "heartbeat"):
            self.assert_notification_metric("last_attempt_success", channel, 1)
        (self.root / "creds" / "webhook_url").write_text("https://new-provider.example/webhook-token")
        (self.root / "creds" / "heartbeat_url").write_text("https://new-provider.example/heartbeat-token")
        self.assertEqual(self.run_probe(env, advance=300), 1)  # No reminder due, heartbeat suppressed.
        for channel in ("webhook", "heartbeat"):
            self.assert_notification_metric("enabled", channel, 1)
            self.assert_notification_metric("attempted", channel, 0)
            self.assertNotIn(f'notification_last_attempt_success{{channel="{channel}"}}', self.metrics())
        (self.root / "creds" / "heartbeat_url").unlink()
        self.assertEqual(self.run_probe(env, advance=300), 1)
        self.assert_notification_metric("enabled", "heartbeat", 0)
        saved = json.loads((self.root / "state" / "notifications.json").read_text())
        self.assertNotIn("heartbeat", saved["channels"])

    def test_malformed_notification_history_remains_unknown(self):
        cfg = MODULE.Config(self.env("/ok"))
        valid = {"destination_sha256": MODULE.url_id(cfg.webhook), "last_attempt_at": self.clock,
                 "last_attempt_success": True, "last_attempt_failure_code": 0, "last_success_at": self.clock}
        cases = [{}, {"last_attempt_at": float("nan")}, {"last_attempt_at": float("inf")},
                 {"last_attempt_at": self.clock + 1}, {"last_attempt_at": 10 ** 1000},
                 {"last_attempt_at": True}, {"last_attempt_success": 1}, {"last_attempt_failure_code": 4},
                 {"last_success_at": None}, {"last_success_at": self.clock + 1},
                 {"destination_sha256": "wrong-destination"}]
        for changed in cases:
            with self.subTest(changed=list(changed)):
                row = dict(valid, **changed) if changed else {}
                cfg.notification_state.write_text(json.dumps({"version": 1, "channels": {"webhook": row}}))
                observed = MODULE.notification_channels(cfg, self.clock)
                self.assertNotIn("last_attempt_success", observed["webhook"])
                self.assertNotIn("last_success_at", observed["webhook"])
        for raw in ("broken-json", "[]", "null", '{"version":1,"channels":[]}',
                    json.dumps({"version": True, "channels": {"webhook": valid}}),
                    json.dumps({"version": 2, "channels": {"webhook": valid}})):
            cfg.notification_state.write_text(raw)
            self.assertNotIn("last_success_at", MODULE.notification_channels(cfg, self.clock)["webhook"])

        output = io.StringIO()
        with patch.object(MODULE, "load_state", side_effect=OSError("private-storage-error")), contextlib.redirect_stderr(output):
            observed = MODULE.notification_channels(cfg, self.clock)
        self.assertNotIn("last_success_at", observed["webhook"])
        self.assertIn("notification_state_read_failed", output.getvalue())
        self.assertNotIn("private-storage-error", output.getvalue())

    def test_notification_history_write_failure_still_publishes_current_result(self):
        env = self.env("/ok")
        save = MODULE.save_state

        def failing_save(path, state, mode=None):
            if path.name == "notifications.json":
                raise OSError("private-storage-error")
            save(path, state, mode)

        output = io.StringIO()
        with patch.object(MODULE, "save_state", failing_save), contextlib.redirect_stderr(output):
            self.assertEqual(self.run_probe(env), 0)
        self.assertIn("notification_state_write_failed", output.getvalue())
        self.assertNotIn("private-storage-error", output.getvalue())
        self.assert_notification_metric("last_attempt_success", "heartbeat", 1)


if __name__ == "__main__":
    unittest.main()
