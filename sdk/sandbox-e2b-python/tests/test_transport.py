# The default transport: the standard library's HTTP client, which turns
# refusals into problem documents and never follows a redirect with the
# bearer.

from __future__ import annotations

import json
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

from gregale_sandbox_e2b import AuthenticationException, RateLimitException, SandboxException
from gregale_sandbox_e2b.api import Api, ApiError


class Handler(BaseHTTPRequestHandler):
    seen: list = []

    def log_message(self, *args) -> None:
        pass

    def do_GET(self) -> None:
        Handler.seen.append((self.path, self.headers.get("Authorization")))
        if self.path.startswith("/ok"):
            self._send(200, {"ok": True, "query": self.path.partition("?")[2]})
        elif self.path == "/limited":
            self._send(429, {"code": "agent_limit", "status": 429, "detail": "SandboxesConcurrent is 2"})
        elif self.path == "/redirect":
            self.send_response(302)
            self.send_header("Location", "/ok")
            self.send_header("Content-Length", "0")
            self.end_headers()
        else:
            self.send_response(401)
            self.send_header("Content-Type", "text/plain")
            self.end_headers()
            self.wfile.write(b"nope")

    def _send(self, status: int, body: dict) -> None:
        raw = json.dumps(body).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)


class TransportTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        threading.Thread(target=cls.server.serve_forever, daemon=True).start()
        cls.api = Api(api_key="key-1", api_base=f"http://127.0.0.1:{cls.server.server_port}/")

    @classmethod
    def tearDownClass(cls) -> None:
        cls.server.shutdown()
        cls.server.server_close()

    def test_answers_json_with_the_bearer_and_query(self) -> None:
        self.assertEqual(self.api.control("GET", "/ok", query={"a": True, "b": None, "c": 2}), {"ok": True, "query": "a=true&c=2"})
        self.assertEqual(Handler.seen[-1][1], "Bearer key-1")

    def test_refusals_become_problems(self) -> None:
        from gregale_sandbox_e2b.api import to_sandbox_error

        with self.assertRaises(ApiError) as limited:
            self.api.control("GET", "/limited")
        self.assertIsInstance(to_sandbox_error(limited.exception, "create"), RateLimitException)
        with self.assertRaises(ApiError) as denied:
            self.api.control("GET", "/denied")
        self.assertEqual(denied.exception.status, 401)
        self.assertIsInstance(to_sandbox_error(denied.exception, "create"), AuthenticationException)

    def test_never_follows_a_redirect(self) -> None:
        before = len(Handler.seen)
        with self.assertRaises(ApiError) as moved:
            self.api.control("GET", "/redirect")
        self.assertEqual(moved.exception.status, 302)
        self.assertIsInstance(moved.exception, Exception)
        self.assertNotIsInstance(moved.exception, SandboxException)
        self.assertEqual(len(Handler.seen), before + 1)


if __name__ == "__main__":
    unittest.main()
