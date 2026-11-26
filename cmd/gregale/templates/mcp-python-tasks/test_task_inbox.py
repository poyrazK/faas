"""Exercise Task inbox and reviewed input through PostgreSQL and the Gregale CLI."""

import json
import os
from pathlib import Path
import queue
import signal
import socket
import stat
import subprocess
import tempfile
import threading
import time
import unittest
from unittest.mock import patch

import jwt
from cryptography.hazmat.primitives.asymmetric import rsa
from psycopg.conninfo import make_conninfo

from auth import AuthContext, Authenticator
from server import DrainingHTTPServer, MCPHandler
from task_inbox_protocol import (
    InvalidTaskInboxRequest, REVIEW_REVISION, TASK_CHECK_REVIEWED_METHOD, TASK_INBOX_METHOD,
    TASK_RESPOND_REVIEWED_METHOD, dump_review_json, load_review_json,
)
from tasks import DurableTaskRuntime, TaskNotFound, check_task_inbox_index, migrate_task_schema
import test_task_execution


GREGALE_TEST_BINARY = os.environ.get("MCP_TASKS_TEST_GREGALE", "")


@unittest.skipUnless(os.environ.get("MCP_TASKS_TEST_DATABASE_URL"), "requires PostgreSQL")
class TaskInboxPostgresTests(unittest.TestCase):
    setUp = test_task_execution.TaskExecutionPostgresTests.setUp

    def test_metadata_pagination_with_expired_anchor(self):
        runtime = self.runtime
        alice, bob = AuthContext(subject="alice"), AuthContext(subject="bob")

        def create(identity):
            return runtime.create("build_report", {"report": "inbox", "steps": 1}, identity)["taskId"]

        earlier, later = create(alice), create(alice)
        create(bob)
        with runtime.pool.connection() as connection:
            connection.execute("""UPDATE gregale_mcp_tasks SET expires_at=
                date_trunc('second', clock_timestamp())+interval '1 minute' + interval '1 microsecond' *
                CASE WHEN task_id=%s THEN 1 ELSE 2 END WHERE task_id=ANY(%s::uuid[])""",
                (earlier, [earlier, later]))
            connection.execute("DELETE FROM gregale_mcp_task_owners")
        options = {"status": "working", "limit": 1}
        with patch.object(runtime, "_decrypt_payload", side_effect=AssertionError("inbox opened a payload")):
            page = runtime.list_inbox(options, alice, ["build_report"])
            self.assertEqual(page["tasks"][0]["taskId"], later)
            self.assertEqual(set(page["tasks"][0]), {
                "resultType", "taskId", "status", "createdAt", "lastUpdatedAt", "ttlMs", "pollIntervalMs",
            })
            cursor = page["nextCursor"]
            self.assertEqual(runtime.list_inbox(options, alice, [])["tasks"], [])
            with self.assertRaises(InvalidTaskInboxRequest):
                runtime.list_inbox(dict(options, cursor=cursor), bob, ["build_report"])
            with self.assertRaises(InvalidTaskInboxRequest):
                runtime.list_inbox(dict(options, status="completed", cursor=cursor), alice, ["build_report"])
            with runtime.pool.connection() as connection:
                connection.execute("UPDATE gregale_mcp_tasks SET expires_at=clock_timestamp() WHERE task_id=%s",
                                   (later,))
            next_page = runtime.list_inbox(dict(options, cursor=cursor), alice, ["build_report"])
        self.assertEqual([task["taskId"] for task in next_page["tasks"]], [earlier])
        self.assertNotIn("nextCursor", next_page)
        with runtime.pool.connection() as connection:
            self.assertEqual(connection.execute("SELECT count(*) AS count FROM gregale_mcp_task_owners").fetchone()["count"], 0)

    def test_migration_requires_the_index_on_the_tasks_table(self):
        runtime = self.runtime
        self.assertTrue(check_task_inbox_index(runtime.pool))
        with runtime.pool.connection() as connection:
            connection.execute("DROP INDEX gregale_mcp_tasks_inbox_idx")
            connection.execute("CREATE INDEX gregale_mcp_tasks_inbox_idx ON gregale_mcp_task_owners(namespace,owner_hash)")
        self.assertFalse(check_task_inbox_index(runtime.pool))
        with runtime.pool.connection() as connection:
            connection.execute("DROP INDEX gregale_mcp_tasks_inbox_idx")
        migrate_task_schema(runtime.pool)
        migrate_task_schema(runtime.pool)
        self.assertTrue(check_task_inbox_index(runtime.pool))

    def test_review_and_confirmation_preserve_exact_numbers(self):
        runtime = self.runtime
        alice = AuthContext(subject="alice")
        task_id = runtime.create("build_report", {"report": "inbox", "steps": 1}, alice,
                                 {"elicitation": {}})["taskId"]
        request = b'{"requests":{"approval":{"method":"elicitation/create","params":{"message":"Confirm value?",' \
                  b'"requestedSchema":{"type":"object","properties":{"x":{"const":0.1000000000000000001}}}}}},"responses":{}}'
        allowed = ["build_report"]

        def stage_form(form):
            ciphertext = runtime._encrypt_payload(form, task_id, "input-state")
            with runtime.pool.connection() as connection:
                connection.execute("""UPDATE gregale_mcp_tasks SET status='input_required',input_state_encrypted=%s
                    WHERE task_id=%s""", (ciphertext, task_id))

        stage_form(request)
        review = runtime.get_input_review(task_id, alice, allowed)
        revision = review["reviewRevision"]
        self.assertIn("0.1000000000000000001", dump_review_json(review))
        exact = load_review_json(b'{"approval":{"action":"accept","content":{"x":0.1000000000000000001}}}')
        different = load_review_json(b'{"approval":{"action":"accept","content":{"x":0.1000000000000000002}}}')
        stage_form(request.replace(b"0001", b"0002"))
        self.assertEqual(runtime.reviewed_input_response(task_id, revision, exact, alice, allowed, apply=True)["outcome"],
                         "stale")
        stage_form(request)
        self.assertEqual(runtime.reviewed_input_response(task_id, revision, exact, alice, allowed, apply=True)["outcome"],
                         "applied")
        claimed = runtime._claim()
        self.assertEqual(claimed["task_id"], task_id)
        follow_up = {"method": "elicitation/create", "params": {"message": "Confirm another value?"}}
        self.assertEqual(runtime._request_inputs(claimed, {"second": follow_up}), ({}, False))
        auth = Authenticator({"endpoint": "/mcp", "auth": {"mode": "open"}})
        runtime.update_inputs(task_id, {"second": {"action": "decline"}}, alice, auth, alice)
        self.assertEqual(runtime.reviewed_input_response(task_id, revision, exact, alice, allowed, apply=False)["outcome"],
                         "applied")
        self.assertEqual(runtime.reviewed_input_response(task_id, revision, different, alice, allowed, apply=False)["outcome"],
                         "unconfirmed")
        with self.assertRaises(TaskNotFound):
            runtime.reviewed_input_response(task_id, revision, exact, alice, [], apply=False)


@unittest.skipUnless(GREGALE_TEST_BINARY and os.environ.get("MCP_TASKS_TEST_DATABASE_URL"),
                     "requires the Gregale CLI and PostgreSQL")
class TaskInboxJourneyTests(unittest.TestCase):
    setUp = test_task_execution.TaskExecutionPostgresTests.setUp

    def test_fresh_inbox_watch_and_reviewed_input_by_id(self):
        config = {"endpoint": "/mcp", "tasks": {"enabled": True, "poll_interval_ms": 500},
                  "auth": {"mode": "external-oauth", "issuer": "https://issuer.example",
                           "jwks_url": "https://issuer.example/jwks", "resource": "https://mcp.example/mcp",
                           "scopes": ["mcp:tools"], "tool_scopes": {"build_report": ["reports:read"]}}}
        with patch.dict(os.environ, {"DATABASE_URL": make_conninfo(self.runtime.pool.conninfo),
                                     "MCP_TASK_OWNER_KEY": test_task_execution.OWNER_KEY.decode(),
                                     "MCP_TASK_NAMESPACE": self.runtime.namespace}):
            runtime = DurableTaskRuntime(config, role="combined")
        self.addCleanup(runtime.close)
        auth = Authenticator(config)
        private = rsa.generate_private_key(public_exponent=65537, key_size=2048)
        key = {**jwt.algorithms.RSAAlgorithm.to_jwk(private.public_key(), as_dict=True), "kid": "inbox"}
        self.enterContext(patch.object(auth.jwks_client, "fetch_data", return_value={"keys": [key]}))
        owner_scopes = "mcp:tools reports:read"

        def mint(subject, scope):
            return jwt.encode({"iss": config["auth"]["issuer"], "aud": config["auth"]["resource"],
                               "sub": subject, "scope": scope, "exp": time.time() + 300},
                              private, algorithm="RS256", headers={"kid": "inbox"})

        owner, foreign, reduced = mint("alice", owner_scopes), mint("bob", owner_scopes), mint("alice", "mcp:tools")
        identity = AuthContext(subject="alice", resource=config["auth"]["resource"], scopes=owner_scopes.split())
        empty_inbox = threading.Event()
        faults = queue.Queue()
        requests = []

        class InboxJourneyHandler(MCPHandler):
            def do_POST(self):
                method = self.headers.get("Mcp-Method")
                if method in {TASK_RESPOND_REVIEWED_METHOD, TASK_CHECK_REVIEWED_METHOD}:
                    requests.append(method)
                if method == TASK_RESPOND_REVIEWED_METHOD:
                    try:
                        fault, task_id = faults.get_nowait()
                    except queue.Empty:
                        fault, task_id = None, None
                    if fault == "before":
                        return self.drop_reply()
                    if fault == "after":
                        self.drop_after = True
                    if fault == "race":
                        runtime.update_inputs(task_id, {"approval": {"action": "decline"}}, identity, auth, identity)
                super().do_POST()

            def drop_reply(self):
                self.close_connection = True
                self.connection.shutdown(socket.SHUT_RDWR)

            def send_json(self, status, value, extra_headers=None):
                result = value.get("result", {})
                if self.headers.get("Mcp-Method") == TASK_INBOX_METHOD and result.get("tasks") == []:
                    empty_inbox.set()
                if getattr(self, "drop_after", False) and result.get("outcome") == "applied":
                    return self.drop_reply()
                return super().send_json(status, value, extra_headers)

        http = DrainingHTTPServer(("127.0.0.1", 0), InboxJourneyHandler)
        self.addCleanup(http.server_close)
        http.app_config, http.authenticator, http.task_runtime = config, auth, runtime
        thread = threading.Thread(target=http.serve_forever, daemon=True)
        thread.start()
        self.addCleanup(thread.join, 2)
        self.addCleanup(http.shutdown)
        url = f"http://127.0.0.1:{http.server_port}/mcp"
        fresh = Path(self.enterContext(tempfile.TemporaryDirectory()))
        submitter = Path(self.enterContext(tempfile.TemporaryDirectory()))

        def command(verb, *args):
            return [GREGALE_TEST_BINARY, "mcp", "client", verb, "--url", url,
                    "--token-env", "GREGALE_JOURNEY_TOKEN", "--json", *args]

        def environment(profile, token):
            return {"XDG_CONFIG_HOME": str(profile), "GREGALE_JOURNEY_TOKEN": token}

        def cli(verb, *args, code=0, token=owner, profile=fresh):
            result = subprocess.run(command(verb, "--timeout", "8s", *args), cwd=profile,
                                    env=environment(profile, token), capture_output=True, text=True, timeout=12)
            self.assertTrue(token not in result.stdout and token not in result.stderr, "CLI leaked its bearer token")
            self.assertEqual(result.returncode, code, result.stderr)
            return json.loads(result.stdout) if result.stdout else None

        def submit(report):
            return cli("call", "--tool", "build_report", "--tasks", "--defer-form-input", "--task-receipt",
                       str(submitter / f"{report}.json"), "--arguments",
                       json.dumps({"report": report, "steps": 1, "request_approval": True}),
                       profile=submitter)["result"]["taskId"]

        checkpoint = Path(self.enterContext(tempfile.TemporaryDirectory())) / "inbox.json"

        def start_watch():
            watch = subprocess.Popen(command("inbox", "--status", "input_required", "--watch", "--interval", "1s",
                                             "--limit", "1", "--checkpoint-file", str(checkpoint)),
                                     cwd=fresh, env=environment(fresh, owner), stdout=subprocess.PIPE, text=True)

            def cleanup():
                if watch.poll() is None:
                    watch.kill()
                    watch.wait(timeout=5)
                watch.stdout.close()

            self.addCleanup(cleanup)
            events = queue.Queue()

            def collect():
                for line in watch.stdout:
                    events.put(line)

            reader = threading.Thread(target=collect, daemon=True)
            reader.start()
            return watch, events, reader

        def expect_task(watch, events, reader, task_id):
            event = json.loads(events.get(timeout=10))
            self.assertEqual(set(event), {"taskId", "status", "nextAction", "createdAt", "lastUpdatedAt"})
            self.assertEqual((event["taskId"], event["status"], event["nextAction"]),
                             (task_id, "input_required", "provide_input"))
            watch.send_signal(signal.SIGINT)
            self.assertEqual(watch.wait(timeout=5), 0)
            reader.join(timeout=2)
            self.assertFalse(reader.is_alive())
            self.assertTrue(events.empty(), "watch replayed another visible Task")

        def wait_paused(task_id):
            deadline = time.monotonic() + 8
            while True:
                with runtime.pool.connection() as connection:
                    row = connection.execute("SELECT status FROM gregale_mcp_tasks WHERE task_id=%s",
                                             (task_id,)).fetchone()
                if row["status"] == "input_required":
                    return
                self.assertLess(time.monotonic(), deadline, "Task did not pause")
                time.sleep(0.025)

        watching = start_watch()
        self.assertTrue(empty_inbox.wait(8), "watch did not start with an empty inbox")
        seen_task_id = submit("already seen")
        expect_task(*watching, seen_task_id)
        self.assertEqual(stat.S_IMODE(checkpoint.stat().st_mode), 0o600)
        self.assertEqual(len(json.loads(checkpoint.read_text())["observed"]), 1)

        # Both Tasks are visible at restart. With a page size of one the
        # resumed watch must scan both pages and print only the new Task.
        task_id = submit("inbox journey")
        wait_paused(task_id)
        expect_task(*start_watch(), task_id)
        saved = checkpoint.read_text()
        self.assertEqual(len(json.loads(saved)["observed"]), 2)
        self.assertNotIn(owner, saved)
        self.assertNotIn(seen_task_id, saved)
        self.assertNotIn(task_id, saved)
        self.assertEqual(list(fresh.iterdir()), [])

        once_checkpoint = checkpoint.with_name("one-shot.json")
        once = ("--watch", "--once", "--status", "input_required", "--limit", "1",
                "--checkpoint-file", str(once_checkpoint), "--interval", "1s")
        pending = {seen_task_id, task_id}
        for count in (1, 2):
            event = cli("inbox", *once, "--wait-timeout", "6s")
            self.assertIn(event["taskId"], pending)
            self.assertEqual((event["status"], event["nextAction"]), ("input_required", "provide_input"))
            pending.remove(event["taskId"])
            self.assertEqual(len(json.loads(once_checkpoint.read_text())["observed"]), count)
        before_timeout = once_checkpoint.read_bytes()
        self.assertIsNone(cli("inbox", *once, "--wait-timeout", "400ms", code=124))
        self.assertEqual(once_checkpoint.read_bytes(), before_timeout)

        def draft(task_id, name):
            wait_paused(task_id)
            path = fresh / f"{name}.json"
            cli("status", "--task-id", task_id, "--write-input-template", str(path))
            self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)
            review = json.loads(path.read_text())
            self.assertEqual((review["version"], review["taskId"]), (2, task_id))
            self.assertRegex(review["reviewRevision"], REVIEW_REVISION)
            review["forms"]["approval"]["response"] = {"action": "accept", "content": {"approved": True}}
            path.write_text(json.dumps(review))
            return path

        def respond(task_id, path, **kwargs):
            return cli("respond", "--task-id", task_id, "--reviewed-input-file", str(path), **kwargs)

        path = draft(task_id, "reviewed")
        respond(task_id, path, token=foreign, code=1)
        respond(task_id, path, token=reduced, code=1)
        self.assertEqual(requests, [])
        faults.put(("before", task_id))
        self.assertEqual(respond(task_id, path, code=3)["outcome"], "unconfirmed")
        faults.put(("after", task_id))
        self.assertEqual(respond(task_id, path)["outcome"], "applied")
        result = cli("wait", "--task-id", task_id)
        self.assertIn("Report inbox journey is ready", json.dumps(result))
        self.assertEqual(respond(task_id, path)["outcome"], "applied")
        self.assertEqual(requests, [TASK_RESPOND_REVIEWED_METHOD, TASK_CHECK_REVIEWED_METHOD,
                                    TASK_RESPOND_REVIEWED_METHOD, TASK_CHECK_REVIEWED_METHOD,
                                    TASK_CHECK_REVIEWED_METHOD])

        racing = submit("racing")
        stale = draft(racing, "stale")
        faults.put(("race", racing))
        respond(racing, stale, code=1)
        self.assertIn("Report was not approved.", json.dumps(cli("wait", "--task-id", racing)))
