#!/usr/bin/env python3
"""Unit tests for mcp_canary.py; a local fake MCP server, no network."""

import json
import os
import sys
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import mcp_canary  # noqa: E402


class FakeMCP(BaseHTTPRequestHandler):
    calls = []
    fail_first = set()
    seen = {}

    def log_message(self, *_args):
        pass

    def reply(self, status, body, content_type="application/json"):
        data = body.encode() if isinstance(body, str) else json.dumps(body).encode()
        self.send_response(status)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_POST(self):
        request = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        method, params, rid = request["method"], request["params"], request["id"]
        name = params.get("name")
        FakeMCP.calls.append((method, name, self.headers.get("Authorization")))
        key = name or method
        FakeMCP.seen[key] = FakeMCP.seen.get(key, 0) + 1
        if key in FakeMCP.fail_first and FakeMCP.seen[key] == 1:
            return self.reply(503, {"error": "busy"})
        if method == "tools/call" and name == "stream_demo":
            events = "".join(
                f"event: message\ndata: {json.dumps(m)}\n\n" for m in (
                    {"jsonrpc": "2.0", "method": "notifications/progress", "params": {"progress": 1}},
                    {"jsonrpc": "2.0", "method": "notifications/progress", "params": {"progress": 2}},
                    {"jsonrpc": "2.0", "id": rid, "result": {"content": []}}))
            return self.reply(200, events, "text/event-stream")
        if method == "tools/call" and name == "add":
            return self.reply(200, {"jsonrpc": "2.0", "id": rid,
                                    "result": {"content": [{"type": "text", "text": "5"}]}})
        if method == "tools/call" and name == "broken":
            return self.reply(200, {"jsonrpc": "2.0", "id": rid,
                                    "result": {"isError": True, "content": [{"type": "text", "text": "x"}]}})
        if method == "tools/list":
            return self.reply(200, {"jsonrpc": "2.0", "id": rid, "result": {"tools": [{"name": "add"}]}})
        return self.reply(200, {"jsonrpc": "2.0", "id": rid, "result": {"capabilities": {"tools": {}}}})


class CanaryTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.server = ThreadingHTTPServer(("127.0.0.1", 0), FakeMCP)
        threading.Thread(target=cls.server.serve_forever, daemon=True).start()
        cls.url = f"http://127.0.0.1:{cls.server.server_port}/mcp"

    @classmethod
    def tearDownClass(cls):
        cls.server.shutdown()

    def setUp(self):
        FakeMCP.calls, FakeMCP.fail_first, FakeMCP.seen = [], set(), {}

    def canary(self):
        canary = mcp_canary.Canary("demo", "secret-key", "gregale", sleep=lambda _s: None)
        canary.endpoint = self.url
        return canary

    def test_sse_progress_and_result_are_parsed_in_order(self):
        _, messages = mcp_canary.MCP(self.url).call("stream_demo", progress_token="t")
        self.assertEqual([m.get("method") for m in messages][:2], ["notifications/progress"] * 2)
        self.assertIn("result", messages[-1])

    def test_modern_requests_carry_per_request_metadata(self):
        client = mcp_canary.MCP(self.url)
        client.call("add", {"a": 2, "b": 3})
        self.assertEqual(FakeMCP.calls[-1][:2], ("tools/call", "add"))

    def test_tool_error_is_a_check_failure(self):
        with self.assertRaises(mcp_canary.CheckFailed):
            mcp_canary.MCP(self.url).call("broken")

    def test_http_error_is_a_check_failure(self):
        FakeMCP.fail_first = {"add"}
        with self.assertRaisesRegex(mcp_canary.CheckFailed, "HTTP 503"):
            mcp_canary.MCP(self.url).call("add", {"a": 2, "b": 3})

    def test_pass_on_retry_is_flaky_not_failed(self):
        FakeMCP.fail_first = {"add"}
        canary = self.canary()
        self.assertTrue(canary.check("call", canary.call))
        self.assertEqual(canary.results[-1]["status"], "flaky")
        self.assertIn("HTTP 503", canary.results[-1]["detail"])

    def test_missing_canary_tools_fail_the_catalog(self):
        canary = self.canary()
        self.assertFalse(canary.check("catalog", canary.catalog))
        self.assertIn("canary_fetch", canary.results[-1]["detail"])

    def test_cross_node_reports_each_unreachable_entry_ip(self):
        canary = self.canary()
        canary.entry_ips = lambda: ["192.0.2.1", "192.0.2.2"]
        canary.fetch = lambda url, connect_ip=None, timeout=30: (
            {"status": 200} if connect_ip == "192.0.2.1" else {"status": 0, "error": "dial timeout"})
        self.assertFalse(canary.check("cross_node", canary.cross_node, retry=False))
        detail = canary.results[-1]["detail"]
        self.assertIn("192.0.2.2", detail)
        self.assertNotIn("192.0.2.1:", detail)

    def test_slow_wake_fails_against_budget(self):
        canary = self.canary()
        ticks = iter([0.0, 100.0, 112.5, 112.5])
        canary.clock = lambda: next(ticks)
        canary.run_cli = lambda *a, **k: ""
        self.assertFalse(canary.check("wake", canary.wake, retry=False))
        self.assertIn("budget", canary.results[-1]["detail"])

    def test_a_canary_bug_is_reported_not_raised(self):
        canary = self.canary()
        self.assertFalse(canary.check("bug", lambda: 1 / 0, retry=False))
        self.assertIn("ZeroDivisionError", canary.results[-1]["detail"])

    def test_summary_counts_failures_and_escapes_pipes(self):
        failed, flaky, table = mcp_canary.summarize([
            {"name": "a", "status": "pass", "ms": 1, "detail": ""},
            {"name": "b", "status": "flaky", "ms": 1, "detail": "x"},
            {"name": "c", "status": "fail", "ms": 1, "detail": "a|b"}])
        self.assertEqual([r["name"] for r in failed], ["c"])
        self.assertEqual([r["name"] for r in flaky], ["b"])
        self.assertIn("a/b", table)

    def test_notify_rejects_non_https_webhooks(self):
        with self.assertRaises(ValueError):
            mcp_canary.notify("http://hooks.example/x", "text")

    def test_missing_key_exits_without_probing(self):
        saved = os.environ.pop("MCP_CANARY_KEY", None)
        try:
            self.assertEqual(mcp_canary.main(["--app", "demo"]), 2)
        finally:
            if saved is not None:
                os.environ["MCP_CANARY_KEY"] = saved


if __name__ == "__main__":
    unittest.main()
