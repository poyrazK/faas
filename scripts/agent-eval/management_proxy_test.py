import http.client
import json
import sys
import tempfile
import threading
import unittest
import urllib.error
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import agent_eval as ev
from management_proxy import ManagementProxy


class ManagementTests(unittest.TestCase):
    def test_lost_reply_occurs_after_commit_and_keeps_attempt_identity(self):
        calls = []

        class Native(BaseHTTPRequestHandler):
            def log_message(self, *_args):
                pass

            def do_POST(self):
                calls.append(json.loads(self.rfile.read(int(self.headers["Content-Length"]))))
                self.send_response(200)
                self.end_headers()
                self.wfile.write(b'{"status":"accepted"}')

        server = ThreadingHTTPServer(("127.0.0.1", 0), Native)
        worker = threading.Thread(target=server.serve_forever, daemon=True)
        worker.start()
        proxy = ManagementProxy(f"http://127.0.0.1:{server.server_port}", "private-key", "alpha", True)
        base = proxy.start()
        try:
            def call(attempt):
                request = urllib.request.Request(base + "/v1/apps/alpha/cron-deletions", data=json.dumps({"request_id": attempt, "receipt_proof": "private-proof"}).encode(),
                                                 headers={"Authorization": "Bearer private-key", "Content-Type": "application/json"})
                return urllib.request.urlopen(request, timeout=5)

            with self.assertRaises(http.client.RemoteDisconnected):
                call("original")
            self.assertEqual(len(calls), 1, "the fault must occur after the native write")
            with call("original") as response:
                self.assertEqual(response.status, 200)
            self.assertEqual(proxy.report(), {"mutation_requests": 2, "lost_replies": 1, "cron_attempts": 1, "changed_input_retries": 0, "unexpected_mutations":0})
            with call("replacement"):
                pass
            self.assertEqual(proxy.report()["cron_attempts"], 2)
            report = json.dumps(proxy.report())
            self.assertNotIn("private-key", report)
            self.assertNotIn("private-proof", report)
        finally:
            proxy.close()
            server.shutdown()
            server.server_close()
            worker.join(timeout=5)

    def test_grader_rejects_claimed_success_without_recovery(self):
        with tempfile.TemporaryDirectory() as directory:
            project = Path(directory)
            metrics = {"answer": "Deleted successfully", "commands": [], "tool_calls": {"gregale_get_app": 1}}
            checks = ev.management_checks({"lost_cron_reply": True}, {"lost_replies": 0, "cron_attempts": 0, "changed_input_retries": 0}, metrics, project, None, None, {})
            self.assertFalse(any(check["ok"] for check in checks))

    def test_read_only_grader_checks_requests_and_all_files(self):
        with tempfile.TemporaryDirectory() as directory:
            project = Path(directory)
            (project / "handler.js").write_text("original")
            before = ev.project_fingerprints(project)
            (project / "other.js").write_text("new file the old grader would miss")
            metrics = {"commands": ["gregale deploy"], "tool_calls": {"gregale_get_logs": 1}}
            checks = ev.management_checks({"read_only": True, "mcp_only": True}, {"mutation_requests": 1}, metrics, project, before, None, {})
            self.assertTrue(all(not check["ok"] for check in checks))


if __name__ == "__main__":
    unittest.main()
