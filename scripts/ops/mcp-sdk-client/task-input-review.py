"""Exercise the Node SDK preview with the materialized Python Task starter."""

from decimal import Decimal
import os
from pathlib import Path
from queue import Queue
import socket
import subprocess
from threading import Thread
import time
import unittest
from unittest.mock import patch

import jwt
from cryptography.hazmat.primitives.asymmetric import rsa

from auth import AuthContext, Authenticator
from server import DrainingHTTPServer, MCPHandler
from task_inbox_protocol import (
    TASK_CHECK_REVIEWED_METHOD, TASK_RESPOND_REVIEWED_METHOD,
)
from tasks import DurableTaskRuntime, TaskExecutionContext
import test_task_execution


class TaskInputReviewJourney(unittest.TestCase):
    setUp = test_task_execution.TaskExecutionPostgresTests.setUp

    def test_sdk_recovers_a_lost_reply_and_retains_exact_numbers(self):
        config = {"endpoint": "/mcp", "tasks": {"enabled": True, "poll_interval_ms": 500},
                  "auth": {"mode": "external-oauth", "issuer": "https://issuer.example",
                           "jwks_url": "https://issuer.example/jwks", "resource": "https://mcp.example/mcp",
                           "scopes": ["mcp:tools"], "tool_scopes": {"build_report": ["reports:read"]}}}
        with patch.dict(os.environ, {"DATABASE_URL": self.runtime.pool.conninfo,
                                     "MCP_TASK_OWNER_KEY": test_task_execution.OWNER_KEY.decode(),
                                     "MCP_TASK_NAMESPACE": self.runtime.namespace}):
            runtime = DurableTaskRuntime(config, role="combined")
        self.addCleanup(runtime.close)
        auth = Authenticator(config)
        private = rsa.generate_private_key(public_exponent=65537, key_size=2048)
        key = {**jwt.algorithms.RSAAlgorithm.to_jwk(private.public_key(), as_dict=True), "kid": "sdk-review"}
        self.enterContext(patch.object(auth.jwks_client, "fetch_data", return_value={"keys": [key]}))
        scope = "mcp:tools reports:read"
        identity = AuthContext(subject="alice", resource=config["auth"]["resource"], scopes=scope.split())
        token = jwt.encode({"iss": config["auth"]["issuer"], "aud": identity.resource,
                            "sub": identity.subject, "scope": scope, "exp": time.time() + 300},
                           private, algorithm="RS256", headers={"kid": "sdk-review"})

        amount = 900719925474099312345
        exact = Decimal("0.1000000000000000001")
        request_input = TaskExecutionContext.request_input

        def numeric_form(context, key, request):
            properties = request["params"]["requestedSchema"]["properties"]
            properties["amount"] = {"type": "integer", "const": amount}
            properties["rate"] = {"type": "number"}
            return request_input(context, key, request)

        # Supply the same form on the initial pause and when the worker resumes.
        self.enterContext(patch.object(TaskExecutionContext, "request_input", numeric_form))

        def pause(report):
            task_id = runtime.create("build_report", {"report": report, "steps": 1, "request_approval": True},
                                     identity, {"elicitation": {}})["taskId"]
            deadline = time.monotonic() + 8
            while True:
                with runtime.pool.connection() as connection:
                    row = connection.execute("SELECT status FROM gregale_mcp_tasks WHERE task_id=%s",
                                             (task_id,)).fetchone()
                if row["status"] == "input_required":
                    return task_id
                self.assertLess(time.monotonic(), deadline, "Task did not pause")
                time.sleep(0.025)

        task_id, racing_id = pause("SDK journey"), pause("stale SDK journey")
        revision = runtime.get_input_review(task_id, identity, ["build_report"])["reviewRevision"]

        faults = Queue()
        for fault in ("before", "after", "stale"):
            faults.put(fault)
        observed = []

        class ReviewJourneyHandler(MCPHandler):
            def do_POST(self):
                method = self.headers.get("Mcp-Method")
                if method in {TASK_RESPOND_REVIEWED_METHOD, TASK_CHECK_REVIEWED_METHOD}:
                    observed.append(method)
                if method == TASK_RESPOND_REVIEWED_METHOD:
                    fault = faults.get_nowait()
                    if fault == "before":
                        return self.drop_reply()
                    if fault == "stale":
                        runtime.update_inputs(racing_id, {"approval": {"action": "decline"}}, identity, auth, identity)
                    self.drop_after = fault == "after"
                return super().do_POST()

            def drop_reply(self):
                self.close_connection = True
                self.connection.shutdown(socket.SHUT_RDWR)

            def send_json(self, status, value, extra_headers=None):
                if getattr(self, "drop_after", False) and value.get("result", {}).get("outcome") == "applied":
                    return self.drop_reply()
                return super().send_json(status, value, extra_headers)

        server = DrainingHTTPServer(("127.0.0.1", 0), ReviewJourneyHandler)
        self.addCleanup(server.server_close)
        server.app_config, server.authenticator, server.task_runtime = config, auth, runtime
        thread = Thread(target=server.serve_forever, daemon=True)
        thread.start()
        self.addCleanup(thread.join, 2)
        self.addCleanup(server.shutdown)
        endpoint = f"http://127.0.0.1:{server.server_port}/mcp"
        result = subprocess.run(["node", str(Path(__file__).with_suffix(".mjs")), endpoint, task_id, racing_id],
                                env={**os.environ, "MCP_TASKS_TEST_TOKEN": token}, capture_output=True,
                                text=True, timeout=25)
        self.assertTrue(token not in result.stdout and token not in result.stderr, "SDK fixture leaked its credential")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(observed, [TASK_RESPOND_REVIEWED_METHOD, TASK_CHECK_REVIEWED_METHOD,
                                    TASK_RESPOND_REVIEWED_METHOD, TASK_CHECK_REVIEWED_METHOD,
                                    TASK_RESPOND_REVIEWED_METHOD])
        self.assertTrue(faults.empty())

        def confirm(rate):
            answers = {"approval": {"action": "accept",
                                    "content": {"approved": True, "amount": amount, "rate": rate}}}
            return runtime.reviewed_input_response(task_id, revision, answers, identity, ["build_report"],
                                                   apply=False)["outcome"]

        self.assertEqual(confirm(exact), "applied")
        self.assertEqual(confirm(Decimal("0.1000000000000000002")), "unconfirmed")


if __name__ == "__main__":
    unittest.main()
