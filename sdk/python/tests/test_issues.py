import json
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

from faas_sdk.issues import IssueReporter


def test_issue_reporter_captures_context_without_locals():
    received = []

    class Handler(BaseHTTPRequestHandler):
        def do_POST(self):
            assert self.path == "/v1/apps/exports/issue-events"
            assert self.headers["Authorization"] == "Bearer g_issue_test"
            received.append(json.loads(self.rfile.read(int(self.headers["Content-Length"]))))
            self.send_response(202)
            self.end_headers()

        def log_message(self, *args):
            pass

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    reporter = IssueReporter(f"http://127.0.0.1:{server.server_port}", "exports", "g_issue_test")
    try:
        try:
            raise ValueError("invalid format")
        except ValueError as error:
            event_id = reporter.capture_exception(error, route="/exports", trace_id="a" * 32, tenant_id="forged")
        assert reporter.flush()
        assert len(received) == 1
        assert received[0]["event_id"] == event_id
        assert received[0]["exception_type"] == "builtins.ValueError"
        assert received[0]["route"] == "/exports"
        assert received[0]["frames"]
        assert "tenant_id" not in received[0]
        assert "locals" not in received[0]
    finally:
        reporter.close()
        server.shutdown()
        server.server_close()


def test_issue_reporter_retries_exact_payload_and_preserves_exception():
    import asyncio

    received = []

    class Handler(BaseHTTPRequestHandler):
        def do_POST(self):
            received.append(json.loads(self.rfile.read(int(self.headers["Content-Length"]))))
            self.send_response(503 if len(received) == 1 else 202)
            self.end_headers()

        def log_message(self, *args):
            pass

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    reporter = IssueReporter(f"http://127.0.0.1:{server.server_port}", "exports", "g_issue_test")
    original = ValueError("failed")

    async def work():
        raise original

    try:
        try:
            asyncio.run(reporter.wrap(work)())
        except ValueError as error:
            assert error is original
        else:
            raise AssertionError("reporter swallowed application failure")
        assert reporter.flush()
        assert len(received) == 2
        assert received[0] == received[1]
        assert reporter.stats()["accepted"] == 1
    finally:
        reporter.close()
        server.shutdown()
        server.server_close()
