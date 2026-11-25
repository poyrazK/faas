"""Exercise the Node SDK preview with the materialized Python Task starter."""

from decimal import Decimal
import json
import os
from pathlib import Path
from queue import Empty, Queue
import signal
import socket
import stat
import subprocess
from tempfile import TemporaryDirectory
from threading import Event, Thread
import time
import unittest
from unittest.mock import patch

import jwt
from cryptography.hazmat.primitives.asymmetric import rsa

from auth import AuthContext, Authenticator
from protocol_headers import TASK_SUBMISSION_LOOKUP_METHOD, TASK_SUBMISSION_METHOD
from server import DrainingHTTPServer, MCPHandler
from task_inbox_protocol import (
    TASK_CHECK_REVIEWED_METHOD, TASK_RESPOND_REVIEWED_METHOD,
)
from tasks import DurableTaskRuntime, TaskExecutionContext
import test_task_execution


class TaskInputReviewJourney(unittest.TestCase):
    setUp = test_task_execution.TaskExecutionPostgresTests.setUp

    def test_sdk_recovers_lost_task_replies(self):
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
        claims = {"iss": config["auth"]["issuer"], "aud": identity.resource, "sub": identity.subject,
                  "scope": scope, "exp": time.time() + 300}

        def mint(**overrides):
            return jwt.encode({**claims, **overrides}, private, algorithm="RS256", headers={"kid": "sdk-review"})

        token = mint()

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

        def wait_for_status(task_id, status):
            deadline = time.monotonic() + 8
            while True:
                with runtime.pool.connection() as connection:
                    row = connection.execute("SELECT status FROM gregale_mcp_tasks WHERE task_id=%s",
                                             (task_id,)).fetchone()
                if row["status"] == status:
                    return
                self.assertLess(time.monotonic(), deadline, f"Task did not reach {status}")
                time.sleep(0.025)

        def pause(report):
            task_id = runtime.create("build_report", {"report": report, "steps": 1, "request_approval": True},
                                     identity, {"elicitation": {}})["taskId"]
            wait_for_status(task_id, "input_required")
            return task_id

        task_id, racing_id = pause("SDK journey"), pause("stale SDK journey")
        lost_cancel_id = pause("lost cancellation reply")
        acknowledged_cancel_id = pause("acknowledged cancellation")
        completed_cancel_id = pause("completion before cancellation")
        revision = runtime.get_input_review(task_id, identity, ["build_report"])["reviewRevision"]

        faults = Queue()
        for fault in ("before", "after", "stale"):
            faults.put(fault)
        cancel_faults = Queue()
        for fault in ("before", "after", "acknowledged", "race"):
            cancel_faults.put(fault)
        observed, submissions, lookups, committed, cancellations = [], [], [], [], []
        receipt_phase = Event()
        receipt_events, receipt_faults = Queue(), Queue()
        receipt_methods = []

        class ReviewJourneyHandler(MCPHandler):
            def do_POST(self):
                method = self.headers.get("Mcp-Method")
                if receipt_phase.is_set() and method != "server/discover":
                    receipt_methods.append(method)
                if method == "tasks/cancel":
                    self.cancel_fault = cancel_faults.get_nowait()
                    cancellations.append(self.cancel_fault)
                    if self.cancel_fault == "before":
                        return self.drop_reply()
                    if self.cancel_fault == "race":
                        runtime.update_inputs(completed_cancel_id, {"approval": {"action": "decline"}},
                                              identity, auth, identity)
                        wait_for_status(completed_cancel_id, "completed")
                if method == TASK_SUBMISSION_METHOD:
                    submissions.append(self.headers.get("Mcp-Name"))
                    if receipt_phase.is_set():
                        try:
                            self.receipt_fault, self.release_receipt_reply = receipt_faults.get_nowait()
                        except Empty:
                            self.receipt_fault = None
                        if self.receipt_fault == "before":
                            receipt_events.put(None)
                            self.release_receipt_reply.wait(10)
                            return self.drop_reply()
                if method == TASK_SUBMISSION_LOOKUP_METHOD:
                    lookups.append(method)
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
                result = value.get("result", {})
                if getattr(self, "drop_after", False) and result.get("outcome") == "applied":
                    return self.drop_reply()
                if status == 200 and result.get("resultType") == "complete" and getattr(
                    self, "cancel_fault", None
                ) in {"after", "race"}:
                    return self.drop_reply()
                if self.headers.get("Mcp-Method") == TASK_SUBMISSION_METHOD and result.get("resultType") == "task":
                    if receipt_phase.is_set():
                        if getattr(self, "receipt_fault", None) == "after":
                            receipt_events.put(result["taskId"])
                            self.release_receipt_reply.wait(10)
                            return self.drop_reply()
                        return super().send_json(status, value, extra_headers)
                    committed.append(result["taskId"])
                    if len(committed) == 1:
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
        result = subprocess.run(["node", str(Path(__file__).with_suffix(".mjs")), endpoint, task_id, racing_id,
                                 lost_cancel_id, acknowledged_cancel_id, completed_cancel_id],
                                env={**os.environ, "MCP_TASKS_TEST_TOKEN": token}, capture_output=True,
                                text=True, timeout=25)
        self.assertTrue(token not in result.stdout and token not in result.stderr, "SDK fixture leaked its credential")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(observed, [TASK_RESPOND_REVIEWED_METHOD, TASK_CHECK_REVIEWED_METHOD,
                                    TASK_RESPOND_REVIEWED_METHOD, TASK_CHECK_REVIEWED_METHOD,
                                    TASK_RESPOND_REVIEWED_METHOD])
        self.assertTrue(faults.empty())
        self.assertEqual(submissions, ["build_report", "build_report", "build_report", None])
        self.assertEqual(lookups, [TASK_SUBMISSION_LOOKUP_METHOD])
        self.assertTrue(cancel_faults.empty())
        self.assertEqual(cancellations, ["before", "after", "acknowledged", "race"])
        with runtime.pool.connection() as connection:
            rows = connection.execute(
                """SELECT task_id::text AS task_id,status,attempt_count,submission_key IS NOT NULL AS keyed,
                          cancel_requested_at IS NOT NULL AS cancel_requested
                   FROM gregale_mcp_tasks WHERE namespace=%s""",
                (runtime.namespace,),
            ).fetchall()
        self.assertEqual(len(committed), 2)
        self.assertEqual(committed[0], committed[1])
        self.assertEqual(len(rows), 6)
        self.assertEqual([row for row in rows if row["keyed"]],
                         [{"task_id": committed[0], "status": "completed", "attempt_count": 1, "keyed": True,
                           "cancel_requested": False}])
        by_id = {row["task_id"]: row for row in rows}
        for cancelled in (lost_cancel_id, acknowledged_cancel_id):
            self.assertEqual((by_id[cancelled]["status"], by_id[cancelled]["cancel_requested"]), ("cancelled", True))
        raced = by_id[completed_cancel_id]
        self.assertEqual((raced["status"], raced["cancel_requested"]), ("completed", False))

        def confirm(rate):
            answers = {"approval": {"action": "accept",
                                    "content": {"approved": True, "amount": amount, "rate": rate}}}
            return runtime.reviewed_input_response(task_id, revision, answers, identity, ["build_report"],
                                                   apply=False)["outcome"]

        self.assertEqual(confirm(exact), "applied")
        self.assertEqual(confirm(Decimal("0.1000000000000000002")), "unconfirmed")

        receipt_phase.set()
        receipt_script = str(Path(__file__).with_name("task-receipt.mjs"))
        renewed = mint(jti="fresh-receipt-recovery")

        def receipt_client(mode, path, credential):
            result = subprocess.run(["node", receipt_script, mode, endpoint, str(path)],
                                    env={**os.environ, "MCP_TASKS_TEST_TOKEN": credential}, capture_output=True,
                                    text=True, timeout=16)
            self.assertNotIn(credential, result.stdout + result.stderr)
            self.assertEqual(result.returncode, 0, result.stderr)
            return json.loads(result.stdout) if result.stdout else None

        def receipt_rows(key):
            with runtime.pool.connection() as connection:
                return connection.execute(
                    """SELECT task_id::text AS task_id,status,attempt_count
                       FROM gregale_mcp_tasks WHERE namespace=%s AND submission_key=%s""",
                    (runtime.namespace, key),
                ).fetchall()

        with TemporaryDirectory() as directory:
            for fault in ("before", "after"):
                with self.subTest(submission_reply=fault):
                    receipt_methods.clear()
                    release_receipt_reply = Event()
                    receipt_faults.put((fault, release_receipt_reply))
                    receipt_path = Path(directory, f"{fault}.json")
                    command = ["node", receipt_script, "submit", endpoint, str(receipt_path)]
                    with subprocess.Popen(command, env={**os.environ, "MCP_TASKS_TEST_TOKEN": token},
                                          stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True) as submitter:
                        try:
                            try:
                                original_id = receipt_events.get(timeout=8)
                            except Empty:
                                self.fail("SDK submission did not reach the fault")
                            submitter.kill()
                            stdout, stderr = submitter.communicate(timeout=5)
                            self.assertEqual(submitter.returncode, -signal.SIGKILL, stderr)
                            self.assertNotIn(token, stdout + stderr)
                        finally:
                            if submitter.poll() is None:
                                submitter.kill()
                                submitter.wait(timeout=5)
                            release_receipt_reply.set()

                    initial = receipt_path.read_text()
                    self.assertEqual(stat.S_IMODE(receipt_path.stat().st_mode), 0o600)
                    receipt = json.loads(initial)
                    self.assertEqual(set(receipt), {"version", "endpointSha256", "submissionKey",
                                                    "requestSha256", "credentialProof"})
                    self.assertEqual(receipt["version"], 2)
                    self.assertNotIn(token, initial)
                    self.assertNotIn(endpoint, initial)
                    self.assertNotIn("crash receipt journey", initial)
                    self.assertEqual([row["task_id"] for row in receipt_rows(receipt["submissionKey"])],
                                     [original_id] if fault == "after" else [])
                    self.assertEqual(receipt_methods, [TASK_SUBMISSION_METHOD])

                    receipt_client("reject-credential", receipt_path, renewed)
                    self.assertEqual(receipt_methods, [TASK_SUBMISSION_METHOD])
                    if fault == "after":
                        self.assertEqual(receipt_client("resume", receipt_path, renewed),
                                         {"taskId": original_id, "status": "completed"})
                        self.assertEqual(receipt_path.read_text(), initial)
                        self.assertEqual(receipt_methods[1:3], [TASK_SUBMISSION_LOOKUP_METHOD, "tasks/get"])

                    before_retry = len(receipt_methods)
                    recovered = receipt_client("retry", receipt_path, token)
                    admitted = recovered["taskId"]
                    if original_id is not None:
                        self.assertEqual(admitted, original_id)
                    self.assertEqual(receipt_methods[before_retry], TASK_SUBMISSION_METHOD)
                    self.assertTrue(all(method == "tasks/get" for method in receipt_methods[before_retry + 1:]))
                    self.assertEqual(receipt_methods.count(TASK_SUBMISSION_METHOD), 2)
                    self.assertEqual(json.loads(receipt_path.read_text()), {**receipt, "taskId": admitted})
                    self.assertEqual(receipt_rows(receipt["submissionKey"]),
                                     [{"task_id": admitted, "status": "completed", "attempt_count": 1}])


if __name__ == "__main__":
    unittest.main()
