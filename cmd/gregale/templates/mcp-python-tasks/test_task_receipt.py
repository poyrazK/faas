"""Exercise lost HTTP replies across fresh Gregale CLI processes and PostgreSQL."""

from concurrent.futures import ThreadPoolExecutor
from contextlib import ExitStack
import hashlib
import hmac
import json
import os
from pathlib import Path
import queue
import socket
import stat
import subprocess
import tempfile
import threading
import time
import unittest
from unittest.mock import Mock, patch
import uuid

import jwt
from cryptography.hazmat.primitives.asymmetric import rsa
from psycopg.conninfo import make_conninfo

from auth import AuthContext, Authenticator
from server import DrainingHTTPServer, MCPHandler
import tasks
from tasks import DurableTaskRuntime, check_task_schema, check_task_submission_index, migrate_task_schema
import test_task_execution


GREGALE_TEST_BINARY = os.environ.get("MCP_TASKS_TEST_GREGALE", "")


@unittest.skipUnless(os.environ.get("MCP_TASKS_TEST_DATABASE_URL"), "requires PostgreSQL")
class TaskSubmissionPostgresTests(unittest.TestCase):
    setUp = test_task_execution.TaskExecutionPostgresTests.setUp

    def test_parallel_submit_and_read_only_lookup(self):
        runtime = self.runtime
        runtime.task_submission_ready = check_task_submission_index(runtime.pool)
        self.assertTrue(runtime.task_submission_ready)
        # UUIDv7's timestamp and version/variant bits, with fixture-local randomness.
        key = f"{time.time_ns() // 1_000_000:012x}" + "7" + uuid.uuid4().hex[13:]
        key = str(uuid.UUID(hex=key))
        identity = AuthContext(subject="alice")
        args = {"report": "parallel", "steps": 1}
        with ThreadPoolExecutor(max_workers=8) as pool:
            handles = list(pool.map(lambda _: runtime.create("build_report", dict(args), identity, submission_key=key),
                                    range(8)))
        self.assertEqual(handles, [handles[0]] * 8)
        with self.assertRaisesRegex(ValueError, "key is unavailable"):
            runtime.create("build_report", dict(args), identity, {"elicitation": {}}, submission_key=key)
        with runtime.pool.connection() as connection:
            self.assertEqual(connection.execute("SELECT COUNT(*) AS count FROM gregale_mcp_tasks").fetchone()["count"], 1)
            connection.execute("DELETE FROM gregale_mcp_task_owners")
        auth = Mock(spec=Authenticator)
        auth.can_access_tool.return_value = True
        with patch.object(runtime, "_decrypt", side_effect=AssertionError("metadata lookup opened a payload")):
            task = runtime.lookup_submission(key, identity, auth)
        auth.can_access_tool.assert_called_once_with("build_report", identity)
        self.assertEqual(task["taskId"], handles[0]["taskId"])
        self.assertEqual(set(task), {"resultType", "taskId", "status", "createdAt", "lastUpdatedAt", "ttlMs", "pollIntervalMs"})
        with runtime.pool.connection() as connection:
            self.assertEqual(connection.execute("SELECT COUNT(*) AS count FROM gregale_mcp_task_owners").fetchone()["count"], 0)

    def test_ordinary_tasks_work_without_the_submission_migration(self):
        runtime = self.runtime
        with runtime.pool.connection() as connection:
            connection.execute("ALTER TABLE gregale_mcp_tasks DROP COLUMN submission_key")
        check_task_schema(runtime.pool)
        self.assertFalse(check_task_submission_index(runtime.pool))
        handle = runtime.create("build_report", {"report": "ordinary", "steps": 1}, AuthContext(subject="alice"))
        migrate_task_schema(runtime.pool)
        migrate_task_schema(runtime.pool)
        self.assertTrue(check_task_submission_index(runtime.pool))
        with runtime.pool.connection() as connection:
            row = connection.execute("SELECT task_id::text AS task_id, submission_key FROM gregale_mcp_tasks").fetchone()
        self.assertEqual(row, {"task_id": handle["taskId"], "submission_key": None})
        with runtime.pool.connection() as connection:
            connection.execute("DROP INDEX gregale_mcp_tasks_submission_key_idx")
            connection.execute("CREATE UNIQUE INDEX gregale_mcp_tasks_submission_key_idx ON gregale_mcp_tasks(namespace,submission_key)")
        self.assertFalse(check_task_submission_index(runtime.pool))


@unittest.skipUnless(GREGALE_TEST_BINARY and os.environ.get("MCP_TASKS_TEST_DATABASE_URL"),
                     "requires the Gregale CLI and PostgreSQL")
class TaskReceiptJourneyTests(unittest.TestCase):
    setUp = test_task_execution.TaskExecutionPostgresTests.setUp

    def test_lost_reply_recovers_one_task_and_one_result(self):
        self.run_receipt_journey()

    def test_reviewed_input_resumes_only_the_original_task(self):
        self.run_receipt_journey(reviewed_verb="wait")

    def test_receipt_watches_approval_and_recovers_lost_replies(self):
        self.run_receipt_journey(reviewed_verb="respond")

    def run_receipt_journey(self, reviewed_verb=None):
        config = {"endpoint": "/mcp", "tasks": {"enabled": True, "poll_interval_ms": 500},
                  "auth": {"mode": "external-oauth", "issuer": "https://issuer.example",
                           "jwks_url": "https://issuer.example/jwks", "resource": "https://mcp.example/mcp",
                           "scopes": ["mcp:tools"], "tool_scopes": {"build_report": ["reports:read"]}}}
        fixture = self.runtime
        environment = {"DATABASE_URL": make_conninfo(fixture.pool.conninfo),
                       "MCP_TASK_OWNER_KEY": test_task_execution.OWNER_KEY.decode(),
                       "MCP_TASK_NAMESPACE": fixture.namespace}
        watch_gates = [threading.Event(), threading.Event()] if reviewed_verb == "respond" else []
        with ExitStack() as startup:
            startup.enter_context(patch.dict(os.environ, environment))
            if watch_gates:
                snapshot = tasks._snapshot_task_handlers
                execution_gates = iter(watch_gates)

                def gated_handlers():
                    handlers = snapshot()
                    entry = handlers["build_report"]
                    execute = entry["executors"][entry["version"]]

                    def gated_report(arguments, context):
                        if arguments["report"] == "receipt journey":
                            gate = next(execution_gates, None)
                            if gate is not None and not gate.wait(15):
                                raise AssertionError("receipt watch did not subscribe before the worker deadline")
                        return execute(arguments, context)

                    entry["executors"][entry["version"]] = gated_report
                    return handlers

                startup.enter_context(patch.object(tasks, "_snapshot_task_handlers", side_effect=gated_handlers))
            runtime = DurableTaskRuntime(config, role="combined")
        self.addCleanup(runtime.close)
        for gate in watch_gates:
            self.addCleanup(gate.set)

        private = rsa.generate_private_key(public_exponent=65537, key_size=2048)
        key = {**jwt.algorithms.RSAAlgorithm.to_jwk(private.public_key(), as_dict=True), "kid": "journey"}
        auth = Authenticator(config)
        jwks = patch.object(auth.jwks_client, "fetch_data", return_value={"keys": [key]})
        jwks.start()
        self.addCleanup(jwks.stop)

        def mint(subject, scope):
            return jwt.encode({"iss": config["auth"]["issuer"], "aud": config["auth"]["resource"],
                               "sub": subject, "scope": scope, "exp": time.time() + 300},
                              private, algorithm="RS256", headers={"kid": "journey"})

        owner = mint("alice", "mcp:tools reports:read")
        foreign = mint("bob", "mcp:tools reports:read")
        reduced = mint("alice", "mcp:tools")
        client_dir = Path(self.enterContext(tempfile.TemporaryDirectory()))
        receipt_path = client_dir / "receipt.json"
        intercepted = threading.Event()
        dropped = queue.Queue(maxsize=1)
        change_prompt = threading.Event()
        changed_reads = []
        updates = []
        requests = []
        dropped_mutations = []
        subscription_gates = iter(watch_gates)

        def assert_requests(*expected):
            self.assertEqual(requests, list(expected))
            requests.clear()

        class ReceiptJourneyHandler(MCPHandler):
            def do_POST(self):
                method = self.headers.get("Mcp-Method")
                if reviewed_verb == "respond":
                    requests.append(method)
                if method == "tasks/update":
                    updates.append(True)
                if method == "subscriptions/listen":
                    gate = next(subscription_gates, None)
                    if gate is not None:
                        # The CLI has already obtained a working snapshot.
                        gate.set()
                super().do_POST()

            def drop_reply(self):
                self.close_connection = True
                self.connection.shutdown(socket.SHUT_RDWR)

            def send_json(self, status, value, extra_headers=None):
                task = value.get("result", {})
                method = self.headers.get("Mcp-Method")
                if (reviewed_verb == "respond" and method in {"tasks/update", "tasks/cancel"}
                        and status == 200 and task.get("resultType") == "complete"):
                    dropped_mutations.append(method)
                    return self.drop_reply()
                if method == "tasks/get" and change_prompt.is_set() and task.get("status") == "input_required":
                    # Change only the served prompt, leaving the durable form pending.
                    task["inputRequests"]["approval"]["params"]["message"] = "Approve a different report?"
                    changed_reads.append(True)
                if (method != "gregale/tasks/submit" or intercepted.is_set()
                        or task.get("resultType") != "task"):
                    return super().send_json(status, value, extra_headers)
                intercepted.set()
                try:
                    # Dispatch already returned. Confirm the Task is committed on an independent connection.
                    info = receipt_path.lstat()
                    if not stat.S_ISREG(info.st_mode) or stat.S_IMODE(info.st_mode) != 0o600:
                        raise AssertionError("receipt was not saved privately before submission")
                    with fixture.pool.connection() as connection:
                        row = connection.execute("SELECT expires_at FROM gregale_mcp_tasks WHERE namespace=%s AND task_id=%s",
                                                 (fixture.namespace, task["taskId"])).fetchone()
                    dropped.put((task["taskId"], row["expires_at"]))
                except Exception as exc:
                    dropped.put(exc)
                self.drop_reply()

        http = DrainingHTTPServer(("127.0.0.1", 0), ReceiptJourneyHandler)
        self.addCleanup(http.server_close)
        http.app_config, http.authenticator, http.task_runtime = config, auth, runtime
        thread = threading.Thread(target=http.serve_forever, daemon=True)
        thread.start()
        self.addCleanup(thread.join, 2)
        self.addCleanup(http.shutdown)
        endpoint = f"http://127.0.0.1:{http.server_port}/mcp"

        def cli(verb, token, code, *args):
            timeout_args = () if verb == "watch" else ("--timeout", "6s")
            result = subprocess.run([GREGALE_TEST_BINARY, "mcp", "client", verb, "--url", endpoint,
                                     "--token-env", "GREGALE_JOURNEY_TOKEN", *timeout_args, "--json", *args],
                                    cwd=client_dir, env={"XDG_CONFIG_HOME": str(client_dir), "GREGALE_JOURNEY_TOKEN": token},
                                    capture_output=True, text=True, timeout=10)
            self.assertTrue(token not in result.stdout and token not in result.stderr, "CLI leaked its bearer token")
            self.assertEqual(result.returncode, code, result.stderr)
            if code == 0:
                if verb == "watch":
                    return [json.loads(line) for line in result.stdout.splitlines()]
                return json.loads(result.stdout)
            self.assertEqual(result.stdout, "")

        original_args = '{"report":"receipt journey","steps":1}'
        form_args = '{"report":"receipt journey","steps":1,"request_approval":true}'

        def submit_args(path):
            args = ("--tool", "build_report", "--tasks", "--task-receipt", str(path), "--arguments")
            if reviewed_verb:
                arguments = form_args
                if watch_gates and path != receipt_path:
                    arguments = arguments.replace("receipt journey", "separate report", 1)
                return (*args, arguments, "--defer-form-input")
            return (*args, original_args)

        cli("call", owner, 3, *submit_args(receipt_path))
        committed = dropped.get(timeout=2)
        if isinstance(committed, Exception):
            raise committed
        task_id, expires_at = committed
        receipt = json.loads(receipt_path.read_text())
        request = json.dumps({"arguments": json.loads(form_args if reviewed_verb else original_args),
                              "deferFormInput": bool(reviewed_verb), "tool": "build_report"},
                             sort_keys=True, separators=(",", ":")).encode()
        endpoint_digest = hashlib.sha256(endpoint.encode()).hexdigest()
        request_digest = hashlib.sha256(request).hexdigest()
        key = receipt["submissionKey"]
        proof = hmac.new(owner.encode(), f"gregale/mcp-task-receipt/v3:{endpoint_digest}{key}{request_digest}".encode(),
                         hashlib.sha256).hexdigest()
        self.assertEqual(receipt, {"version": 3, "endpointSha256": endpoint_digest, "submissionKey": key,
                                   "requestSha256": request_digest, "credentialProof": proof})
        self.assertEqual(key[14], "7")

        if reviewed_verb:
            def rows():
                with fixture.pool.connection() as connection:
                    return connection.execute("""SELECT task_id::text AS task_id,
                        submission_key::text AS submission_key, expires_at, status, attempt_count,
                        result_encrypted IS NOT NULL AS has_result
                        FROM gregale_mcp_tasks WHERE namespace=%s""", (fixture.namespace,)).fetchall()

            def wait_for_status(target, status):
                deadline = time.monotonic() + 8
                while time.monotonic() < deadline:
                    if any(row["task_id"] == target and row["status"] == status for row in rows()):
                        return
                    time.sleep(0.05)
                self.fail(f"Task did not reach {status}")

            selector = ("--task-receipt", str(receipt_path))

            def watch(status, action):
                wait_for_status(task_id, "running")
                snapshots = cli("watch", owner, 0, *selector)
                assert_requests("server/discover", "gregale/tasks/lookup", "subscriptions/listen", "gregale/tasks/lookup")
                self.assertEqual(len(snapshots), 2)
                first, last = snapshots
                self.assertEqual(first, {"taskId": task_id, "status": "working", "lastUpdatedAt": first["lastUpdatedAt"],
                                         "nextAction": "poll_status", "pollIntervalMs": 500})
                self.assertEqual(last, {"taskId": task_id, "status": status, "lastUpdatedAt": last["lastUpdatedAt"],
                                        "nextAction": action})
                return last

            if watch_gates:
                requests.clear()
                for credential in (foreign, reduced):
                    cli("watch", credential, 1, *selector)
                    assert_requests("server/discover", "gregale/tasks/lookup")
                watched_form = watch("input_required", "provide_input")

            wait_for_status(task_id, "input_required")
            template_path = client_dir / "reviewed-input.json"
            snapshot = cli("status", owner, 0, "--task-receipt", str(receipt_path),
                           "--write-input-template", str(template_path))
            self.assertEqual((snapshot["taskId"], snapshot["status"], snapshot["nextAction"]),
                             (task_id, "input_required", "provide_input"))
            if watch_gates:
                self.assertEqual(snapshot, watched_form)
            info = template_path.lstat()
            self.assertTrue(stat.S_ISREG(info.st_mode))
            self.assertEqual(stat.S_IMODE(info.st_mode), 0o600)
            template = json.loads(template_path.read_text())
            self.assertEqual((template["version"], template["endpointSha256"], template["taskId"]),
                             (1, receipt["endpointSha256"], task_id))
            self.assertEqual(set(template["forms"]), {"approval"})
            approval = template["forms"]["approval"]
            self.assertEqual(approval["message"], "Approve generating this report?")
            self.assertIsNone(approval["response"])

            def answer(token, code, path=receipt_path):
                return cli(reviewed_verb, token, code, "--task-receipt", str(path),
                           "--reviewed-input-file", str(template_path))

            answer(owner, 1)
            approval["response"] = {"action": "accept", "content": {"approved": True}}
            template_path.write_text(json.dumps(template))

            other_receipt = client_dir / "other-receipt.json"
            other_id = cli("call", owner, 0, *submit_args(other_receipt))["result"]["taskId"]
            self.assertNotEqual(other_id, task_id)
            wait_for_status(other_id, "input_required")
            other_path = client_dir / "other-input.json"
            cli("status", owner, 0, "--task-receipt", str(other_receipt), "--write-input-template", str(other_path))
            other_template = json.loads(other_path.read_text())
            self.assertEqual(other_template["taskId"], other_id)
            self.assertRegex(approval["formSha256"], r"^[0-9a-f]{64}$")
            self.assertEqual(other_template["forms"]["approval"]["formSha256"], approval["formSha256"])

            answer(owner, 1, other_receipt)
            if reviewed_verb == "respond":
                requests.clear()
                for credential in (foreign, reduced):
                    answer(credential, 1)
                    assert_requests("server/discover", "gregale/tasks/lookup")
            else:
                answer(reduced, 1)
            change_prompt.set()
            answer(owner, 1)
            change_prompt.clear()
            self.assertEqual(len(changed_reads), 1)
            self.assertEqual(updates, [])

            other_status = "input_required"
            if reviewed_verb == "respond":
                requests.clear()
                observed = answer(owner, 0)
                assert_requests("server/discover", "gregale/tasks/lookup", "tasks/get", "tasks/update", "tasks/get")
                self.assertEqual(observed["taskId"], task_id)
                self.assertEqual((observed["status"], observed["nextAction"]), ("working", "poll_status"))
                self.assertEqual(set(observed), {"taskId", "status", "lastUpdatedAt", "nextAction", "pollIntervalMs"})

                watch("completed", "read_result")
                read_args = ("--task-receipt", str(receipt_path), "--read-result")
                for credential in (foreign, reduced):
                    cli("status", credential, 1, *read_args)
                    assert_requests("server/discover", "gregale/tasks/lookup")
                completed = cli("status", owner, 0, *read_args)
                assert_requests("server/discover", "gregale/tasks/lookup", "tasks/get")

                wait_for_status(other_id, "input_required")
                cancelled = cli("cancel", owner, 0, "--task-receipt", str(other_receipt))
                assert_requests("server/discover", "gregale/tasks/lookup", "tasks/cancel", "gregale/tasks/lookup")
                self.assertEqual(cancelled, {"taskId": other_id, "status": "cancelled",
                                             "lastUpdatedAt": cancelled["lastUpdatedAt"], "nextAction": "none"})
                self.assertEqual(dropped_mutations, ["tasks/update", "tasks/cancel"])
                other_status = "cancelled"
            else:
                completed = answer(owner, 0)
            self.assertEqual((completed["taskId"], completed["status"]), (task_id, "completed"))
            self.assertEqual(completed["result"]["content"],
                             [{"type": "text", "text": "Report receipt journey is ready after 1 steps."}])
            self.assertEqual(len(updates), 1)
            by_id = {row["task_id"]: row for row in rows()}
            self.assertEqual(set(by_id), {task_id, other_id})
            self.assertEqual(by_id[task_id], {"task_id": task_id, "submission_key": key, "expires_at": expires_at,
                                             "status": "completed", "attempt_count": 1, "has_result": True})
            other = by_id[other_id]
            self.assertEqual((other["status"], other["attempt_count"], other["has_result"]),
                             (other_status, 1, False))
            return

        def submit(token, code, arguments):
            return cli("call", token, code, "--tool", "build_report", "--tasks", "--task-submission-key", key,
                       "--arguments", arguments)

        def lookup(token, code):
            return cli("status", token, code, "--task-receipt", str(receipt_path), "--metadata-only")

        recovered = lookup(owner, 0)
        self.assertEqual(recovered["taskId"], task_id)
        replay = submit(owner, 0, original_args)["result"]
        self.assertEqual((replay["resultType"], replay["taskId"]), ("task", task_id))
        submit(owner, 1, '{"report":"changed","steps":1}')
        submit(foreign, 1, original_args)
        lookup(foreign, 1)
        lookup(reduced, 1)
        issued_at = int(key[:13].replace("-", ""), 16) / 1000
        time.sleep(max(0, issued_at + 60.1 - time.time()))
        submit(owner, 1, original_args)
        recovered = lookup(owner, 0)
        self.assertEqual(set(recovered), {"taskId", "status", "lastUpdatedAt", "nextAction"})
        self.assertEqual((recovered["taskId"], recovered["status"], recovered["nextAction"]),
                         (task_id, "completed", "read_result"))
        result = cli("status", owner, 0, "--task-id", task_id)
        self.assertIn("Report receipt journey is ready after 1 steps.", json.dumps(result))
        with fixture.pool.connection() as connection:
            rows = connection.execute("""SELECT task_id::text AS task_id, submission_key::text AS submission_key,
                expires_at, status, attempt_count, result_encrypted IS NOT NULL AS has_result
                FROM gregale_mcp_tasks WHERE namespace=%s""", (fixture.namespace,)).fetchall()
            self.assertEqual(rows, [{"task_id": task_id, "submission_key": key, "expires_at": expires_at,
                                     "status": "completed", "attempt_count": 1, "has_result": True}])
            connection.execute("UPDATE gregale_mcp_tasks SET expires_at=clock_timestamp() WHERE namespace=%s AND task_id=%s",
                               (fixture.namespace, task_id))
        lookup(owner, 1)
