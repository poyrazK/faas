"""Retained-key startup checks, rollback-safe claims and lease fencing."""
import os
import threading
import time
import unittest
from unittest.mock import Mock, patch

import test_task_execution as execution_tests
from task_keys import check_task_payload_keys
from tasks import DurableTaskRuntime, TaskInputLeaseLost, _derive_key


@unittest.skipUnless(os.environ.get("MCP_TASKS_TEST_DATABASE_URL"), "set MCP_TASKS_TEST_DATABASE_URL")
class TaskRecoveryTests(unittest.TestCase):
    setUp = execution_tests.TaskExecutionPostgresTests.setUp
    insert_task = execution_tests.TaskExecutionPostgresTests.insert_task

    def test_heartbeat_rechecks_expiry_after_row_lock(self):
        for field in ("lease_expires_at", "expires_at"):
            with self.subTest(field=field):
                task_id = self.insert_task()
                task = self.runtime._claim()
                with self.runtime.pool.connection() as connection:
                    connection.execute(f"UPDATE gregale_mcp_tasks SET {field}=clock_timestamp()+interval '1 second' WHERE task_id=%s", (task_id,))
                results = []
                def renew():
                    try:
                        results.append(self.runtime._heartbeat(task_id, task["lease_token"]))
                    except Exception as error:
                        results.append(error)
                worker = threading.Thread(target=renew, daemon=True)
                deadline = time.monotonic() + 5
                with self.runtime.pool.connection() as lock:
                    lock.execute("SELECT task_id FROM gregale_mcp_tasks WHERE task_id=%s FOR UPDATE", (task_id,))
                    worker.start()
                    while True:
                        with self.runtime.pool.connection() as connection:
                            waiting = connection.execute("SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '%%UPDATE gregale_mcp_tasks%%lease_expires_at%%') AS waiting").fetchone()["waiting"]
                        if waiting:
                            break
                        self.assertLess(time.monotonic(), deadline)
                        time.sleep(.005)
                    while not lock.execute(f"SELECT {field}<=clock_timestamp() AS expired FROM gregale_mcp_tasks WHERE task_id=%s", (task_id,)).fetchone()["expired"]:
                        self.assertLess(time.monotonic(), deadline)
                        time.sleep(.005)
                worker.join(timeout=5)
                self.assertFalse(worker.is_alive())
                self.assertEqual(results, [None])
                if field == "lease_expires_at":
                    replacement = self.runtime._claim()
                    self.assertNotEqual(replacement["lease_token"], task["lease_token"])
                    self.runtime._finish(replacement, "completed", {"content": []})

    def test_uuid_case_preserves_payload_reads_and_input_updates(self):
        runtime = self.runtime
        identity = execution_tests.AuthContext(subject="alice", client_id="test-client", resource="https://example.com/mcp")
        authenticator = Mock()
        authenticator.can_access_tool.return_value = True
        for version in (1, 2):
            with self.subTest(version=version):
                runtime.payload_encryption_version = version
                task_id = self.insert_task(runtime._owner_hashes(identity)[0])
                state = {"requests":{"approval":{"method":"elicitation/create", "params":{"mode":"form", "message":"Approve?"}}}, "responses":{}}
                payloads = [runtime._encrypt(value, task_id, field, 1024) for field, value in (
                    ("result", {"content":[]}), ("error", {"code":-32603, "message":"Previous failure"}), ("input-state", state))]
                with runtime.pool.connection() as connection:
                    connection.execute("UPDATE gregale_mcp_tasks SET status='input_required',attempt_count=1,result_encrypted=%s,error_encrypted=%s,input_state_encrypted=%s WHERE task_id=%s", (*payloads, task_id))
                wire, task = runtime.get(task_id.upper(), identity, authenticator, identity)
                self.assertEqual((task["task_id"], wire["status"]), (task_id, "input_required"))
                self.assertEqual(task["result"], {"content":[]})
                self.assertEqual(task["error"]["code"], -32603)
                self.assertEqual(len(task["input_requests"]), 1)
                with self.assertRaises(execution_tests.TaskNotFound):
                    runtime.get(task_id.upper(), execution_tests.AuthContext(subject="bob", client_id="test-client", resource=identity.resource), authenticator, identity)
                runtime.update_inputs(task_id.upper(), {"approval":{"action":"accept", "content":{}}}, identity, authenticator, identity)
                wire, task = runtime.get(task_id, identity, authenticator, identity)
                self.assertEqual((task["status"], task["input_requests"]), ("queued", {}))
                runtime.cancel(task_id.upper(), identity, authenticator, identity)
                self.assertEqual(runtime.get(task_id.upper(), identity, authenticator, identity)[0]["status"], "cancelled")

    def test_preflight_checks_all_retained_fields_and_versions(self):
        from cryptography.hazmat.primitives.ciphers.aead import AESGCM
        runtime = self.runtime
        self.insert_task()  # A readable v1 sample cannot establish key coverage.
        old_key = _derive_key(b"old-" * 8, "gregale-mcp-task-payload:v1")
        for field, column, version in (("arguments", "arguments_encrypted", 1), ("arguments", "arguments_encrypted", 2),
                ("result", "result_encrypted", 2), ("error", "error_encrypted", 2), ("input-state", "input_state_encrypted", 2)):
            with self.subTest(field=field, version=version):
                task_id = self.insert_task()
                nonce = os.urandom(12)
                sealed = AESGCM(old_key).encrypt(nonce, b"{}", runtime._aad(task_id, field))
                payload = (b"\x01" if version == 1 else b"\x02\x03old") + nonce + sealed[-16:] + sealed[:-16]
                with runtime.pool.connection() as connection:
                    connection.execute("UPDATE gregale_mcp_tasks SET " + column + "=%s WHERE task_id=%s", (payload, task_id))
                with self.assertRaises(ValueError):
                    check_task_payload_keys(runtime)
                runtime.owner_keys.append({"id":"old", "payload_key":old_key})
                runtime.payload_keys["old"] = old_key
                check_task_payload_keys(runtime)
                runtime.owner_keys.pop()
                del runtime.payload_keys["old"]
                with runtime.pool.connection() as connection:
                    connection.execute("UPDATE gregale_mcp_tasks SET namespace='other-namespace' WHERE task_id=%s", (task_id,))
                check_task_payload_keys(runtime)
                with runtime.pool.connection() as connection:
                    connection.execute("UPDATE gregale_mcp_tasks SET namespace=%s,created_at=clock_timestamp()-interval '1 hour',expires_at=clock_timestamp()-interval '1 second' WHERE task_id=%s", (runtime.namespace, task_id))
                check_task_payload_keys(runtime)

    def test_startup_defaults_zero_settings(self):
        with patch.dict(os.environ, {"DATABASE_URL":self.runtime.pool.conninfo,
                "MCP_TASK_NAMESPACE":self.runtime.namespace, "MCP_TASK_OWNER_KEY":execution_tests.OWNER_KEY.decode()}):
            runtime = DurableTaskRuntime({"tasks":{"enabled":True, "owner_key_id":"initial",
                "ttl_seconds":0, "poll_interval_ms":0, "worker_concurrency":0,
                "max_attempts":0, "retry_base_delay_ms":0, "retry_max_delay_ms":0}}, role="web")
            try:
                self.assertEqual((runtime.ttl_ms, runtime.poll_interval_ms, runtime.concurrency), (86400000, 2000, 1))
                self.assertEqual((runtime.execution.max_attempts, runtime.execution.retry_base_delay_ms,
                                  runtime.execution.retry_max_delay_ms), (3, 1000, 60000))
            finally:
                runtime.close()

    def test_startup_rejects_wrong_secret_before_starting_writers(self):
        task_id = self.insert_task()
        for secret, allowed in (("wrong" * 8, False), (execution_tests.OWNER_KEY.decode(), True)):
            with self.subTest(allowed=allowed), patch.dict(os.environ, {"DATABASE_URL":self.runtime.pool.conninfo,
                    "MCP_TASK_NAMESPACE":self.runtime.namespace, "MCP_TASK_OWNER_KEY":secret}):
                if allowed:
                    runtime = DurableTaskRuntime({"tasks":{"enabled":True, "owner_key_id":"initial"}}, role="web")
                    runtime.close()
                else:
                    with self.assertRaises(ValueError):
                        DurableTaskRuntime({"tasks":{"enabled":True, "owner_key_id":"initial"}}, role="web")
        with self.runtime.pool.connection() as connection:
            row = connection.execute("SELECT attempt_count FROM gregale_mcp_tasks WHERE task_id=%s", (task_id,)).fetchone()
        self.assertEqual(row["attempt_count"], 0)

    def test_claim_rolls_back_unreadable_payloads_without_consuming_attempts(self):
        for column in ("arguments_encrypted", "result_encrypted", "error_encrypted", "input_state_encrypted"):
            with self.subTest(column=column):
                task_id = self.insert_task()
                check_task_payload_keys(self.runtime)
                with self.runtime.pool.connection() as connection:
                    connection.execute("UPDATE gregale_mcp_tasks SET " + column + "=%s WHERE task_id=%s", (b"corrupted payload", task_id))
                for _ in range(3):
                    with self.assertRaises(ValueError):
                        self.runtime._claim()
                with self.runtime.pool.connection() as connection:
                    row = connection.execute("SELECT status,attempt_count,lease_token FROM gregale_mcp_tasks WHERE task_id=%s", (task_id,)).fetchone()
                    self.assertEqual(row, {"status":"queued", "attempt_count":0, "lease_token":None})
                    connection.execute("DELETE FROM gregale_mcp_tasks WHERE task_id=%s", (task_id,))

    def test_worker_writes_require_live_lease_and_ttl(self):
        runtime = self.runtime
        for operation in ("completed", "cancelled", "input", "failed"):
            for condition in ("live", "lease-expired", "ttl-expired", "replaced"):
                with self.subTest(operation=operation, condition=condition):
                    task_id = self.insert_task()
                    with runtime.pool.connection() as connection:
                        connection.execute("UPDATE gregale_mcp_tasks SET input_methods=ARRAY['elicitation/create:form'] WHERE task_id=%s", (task_id,))
                    task = runtime._claim()
                    patch_sql = {"lease-expired":"lease_expires_at=clock_timestamp()-interval '1 second'",
                        "ttl-expired":"created_at=clock_timestamp()-interval '1 hour',expires_at=clock_timestamp()-interval '1 second'",
                        "replaced":"lease_token=gen_random_uuid()"}.get(condition)
                    if patch_sql:
                        with runtime.pool.connection() as connection:
                            connection.execute("UPDATE gregale_mcp_tasks SET " + patch_sql + " WHERE task_id=%s", (task_id,))
                    if operation == "input":
                        request = {"approve":{"method":"elicitation/create", "params":{"mode":"form", "message":"Approve?"}}}
                        if condition == "live":
                            runtime._request_inputs(task, request)
                        else:
                            with self.assertRaises(TaskInputLeaseLost):runtime._request_inputs(task, request)
                    else:
                        runtime._finish(task, operation, {"content":[]}, retryable=True)
                    with runtime.pool.connection() as connection:
                        row = connection.execute("SELECT status,result_encrypted,input_state_encrypted FROM gregale_mcp_tasks WHERE task_id=%s", (task_id,)).fetchone()
                        expected = {"completed":"completed", "cancelled":"cancelled", "input":"input_required", "failed":"queued"}[operation] if condition == "live" else "running"
                        self.assertEqual(row["status"], expected)
                        if condition != "live":
                            self.assertIsNone(row["result_encrypted"])
                            self.assertIsNone(row["input_state_encrypted"])
                        connection.execute("UPDATE gregale_mcp_tasks SET status='failed',lease_token=NULL,lease_expires_at=NULL WHERE task_id=%s", (task_id,))

    def test_completion_rechecks_lease_after_waiting_for_row_lock(self):
        runtime = self.runtime
        task_id = self.insert_task()
        task = runtime._claim()
        with runtime.pool.connection() as connection:
            connection.execute("UPDATE gregale_mcp_tasks SET lease_expires_at=clock_timestamp()+interval '1 second' WHERE task_id=%s", (task_id,))
        errors = []
        def finish():
            try:runtime._finish(task, "completed", {"content":[]})
            except Exception as error:errors.append(error)
        worker = threading.Thread(target=finish, daemon=True)
        deadline = time.monotonic() + 5
        with runtime.pool.connection() as lock:
            lock.execute("SELECT 1 FROM gregale_mcp_tasks WHERE task_id=%s FOR UPDATE", (task_id,))
            worker.start()
            try:
                while True:
                    with runtime.pool.connection() as connection:
                        waiting = connection.execute("SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE 'WITH lease AS MATERIALIZED%%') AS waiting").fetchone()["waiting"]
                    if waiting:break
                    self.assertLess(time.monotonic(), deadline)
                    time.sleep(.005)
                while True:
                    with runtime.pool.connection() as connection:
                        expired = connection.execute("SELECT lease_expires_at<=clock_timestamp() AS expired FROM gregale_mcp_tasks WHERE task_id=%s", (task_id,)).fetchone()["expired"]
                    if expired:break
                    self.assertLess(time.monotonic(), deadline)
                    time.sleep(.005)
                lock.commit()
            finally:lock.rollback()
        worker.join(timeout=5)
        self.assertFalse(worker.is_alive())
        self.assertEqual(errors, [])
        with runtime.pool.connection() as connection:
            row = connection.execute("SELECT status FROM gregale_mcp_tasks WHERE task_id=%s", (task_id,)).fetchone()
        self.assertEqual(row["status"], "running")
