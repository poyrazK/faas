#!/usr/bin/env python3
from __future__ import annotations

import importlib.util
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path


MODULE_PATH = Path(__file__).with_name("check_live_docs.py")
SPEC = importlib.util.spec_from_file_location("check_live_docs", MODULE_PATH)
assert SPEC and SPEC.loader
CHECK = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(CHECK)


class Handler(BaseHTTPRequestHandler):
    homepage_fallback = False

    def do_GET(self) -> None:
        if self.path == "/docs/security":
            body = "<html><h1>Security</h1></html>"
            self.send_response(200)
        elif self.path == "/docs/object-storage":
            body = "<html><h1>Wrong page</h1></html>"
            self.send_response(200)
        elif self.homepage_fallback:
            body = "<html><h1>Serverless on real microVMs</h1></html>"
            self.send_response(200)
        else:
            body = "missing"
            self.send_response(404)
        self.end_headers()
        self.wfile.write(body.encode())

    def log_message(self, format: str, *args: object) -> None:
        pass


class LiveDocsTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        cls.thread = threading.Thread(target=cls.server.serve_forever, daemon=True)
        cls.thread.start()
        cls.base = f"http://127.0.0.1:{cls.server.server_port}/docs"

    @classmethod
    def tearDownClass(cls) -> None:
        cls.server.shutdown()
        cls.server.server_close()

    def test_matching_heading_and_real_404_pass(self) -> None:
        Handler.homepage_fallback = False
        self.assertEqual(CHECK.check(self.base, {"security"}), [])

    def test_soft_404_homepage_fallback_fails(self) -> None:
        Handler.homepage_fallback = True
        failures = CHECK.check(self.base, {"security"})
        self.assertTrue(any("unknown docs route: HTTP 200" in item for item in failures), failures)

    def test_wrong_heading_fails_even_with_200(self) -> None:
        Handler.homepage_fallback = False
        failures = CHECK.check(self.base, {"object-storage"})
        self.assertTrue(any("first h1" in item for item in failures), failures)


if __name__ == "__main__":
    unittest.main()
