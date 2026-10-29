#!/usr/bin/env python3
"""Tests for the compute-node serving probe (ADR-20261023124955 §5)."""

import http.server
import importlib.machinery
import importlib.util
import json
from pathlib import Path
import sys
import tempfile
import threading
import unittest
import urllib.request


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

    def do_GET(self):
        statuses = self.plan.setdefault(self.path, [200])
        status = statuses.pop(0) if len(statuses) > 1 else statuses[0]
        self.send_response(status)
        if 300 <= status < 400:
            self.send_header("Location", "/elsewhere")
        self.end_headers()
        self.wfile.write(b"ok")

    def log_message(self, *args):
        pass


class _HTTPOpener:
    """Rewrites the probe's https URLs to the local plain-HTTP server."""

    def __init__(self, port):
        self.port = port
        self.inner = MODULE.default_opener()

    def open(self, req, timeout):
        path = "/" + req.full_url.split("/", 3)[3] if req.full_url.count("/") >= 3 else "/"
        local = urllib.request.Request(f"http://127.0.0.1:{self.port}{path}", headers=dict(req.header_items()))
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

    def run_probe(self, env, advance=0):
        self.clock += advance
        return MODULE.run(
            env=env,
            opener=_HTTPOpener(self.server.server_address[1]),
            sleep=self.sleeps.append,
            now=lambda: self.clock,
            post=lambda url, payload: self.posts.append((url, payload)),
            beat=self.beats.append,
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

    def test_failure_notifies_once_then_hourly_then_recovers(self):
        env = self.env("/down")
        _Handler.plan = {"/down": [502]}
        self.assertEqual(self.run_probe(env), 1)
        self.assertEqual(len(self.posts), 1)
        url, payload = self.posts[0]
        self.assertEqual(url, "https://hooks.example/abc")
        self.assertIn("fsn-2", payload["text"])
        self.assertIn("FAILING https://probe.example/down: HTTP 502 after 2 attempts", payload["text"])
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
        self.assertIn("RECOVERED https://probe.example/down", self.posts[2][1]["text"])
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


if __name__ == "__main__":
    unittest.main()
