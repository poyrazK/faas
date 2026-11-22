"""Same-ID format migration must converge without rewriting current data."""
import os
import unittest

from tasks import _derive_key
import test_task_execution as execution_tests


@unittest.skipUnless(os.environ.get("MCP_TASKS_TEST_DATABASE_URL"), "set MCP_TASKS_TEST_DATABASE_URL")
class TaskRewrapTests(unittest.TestCase):
    setUp = execution_tests.TaskExecutionPostgresTests.setUp
    insert_task = execution_tests.TaskExecutionPostgresTests.insert_task

    def use_explicit_key(self):
        runtime = self.runtime
        runtime._legacy_payload_keys = dict(runtime.payload_keys)
        runtime._main_payload_keys = {"initial": _derive_key(b"new-key-" * 8, "gregale-mcp-task-payload:v2:initial")}
        runtime.payload_keys.update(runtime._main_payload_keys)

    def assert_active_reads(self, payload, task_id, field):
        retained = self.runtime._legacy_payload_keys
        self.runtime._legacy_payload_keys = {}
        try:
            self.runtime._decrypt_payload(payload, task_id, field)
        finally:
            self.runtime._legacy_payload_keys = retained

    def test_same_id_native_fields_are_rewrapped_once(self):
        runtime = self.runtime
        runtime.payload_encryption_version = 2
        native = dict(runtime.payload_keys)
        for field, column in (("arguments", "arguments_encrypted"), ("result", "result_encrypted"), ("error", "error_encrypted"), ("input-state", "input_state_encrypted")):
            with self.subTest(field=field):
                runtime.payload_keys = dict(native)
                runtime._main_payload_keys = {}
                task_id = self.insert_task()
                old = runtime._encrypt({"retained": field}, task_id, field, 1024)
                self.use_explicit_key()
                arguments = runtime._encrypt({"report": "test"}, task_id, "arguments", 1024)
                with runtime.pool.connection() as connection:
                    connection.execute("UPDATE gregale_mcp_tasks SET arguments_encrypted=%s WHERE task_id=%s", (arguments, task_id))
                    connection.execute(f"UPDATE gregale_mcp_tasks SET {column}=%s WHERE task_id=%s", (old, task_id))
                    before = connection.execute("SELECT updated_at FROM gregale_mcp_tasks WHERE task_id=%s", (task_id,)).fetchone()["updated_at"]
                self.assertEqual(runtime._rewrap_batch(), 1)
                with runtime.pool.connection() as connection:
                    row = connection.execute(f"SELECT {column} AS payload,updated_at FROM gregale_mcp_tasks WHERE task_id=%s", (task_id,)).fetchone()
                self.assertNotEqual(row["payload"], old)
                self.assertEqual(row["updated_at"], before)
                self.assert_active_reads(row["payload"], task_id, field)
                self.assertEqual(runtime._rewrap_batch(), 0)
                with runtime.pool.connection() as connection:
                    self.assertEqual(connection.execute(f"SELECT {column} AS payload FROM gregale_mcp_tasks WHERE task_id=%s", (task_id,)).fetchone()["payload"], row["payload"])
                    connection.execute("DELETE FROM gregale_mcp_tasks WHERE task_id=%s", (task_id,))

    def test_scan_progresses_past_current_and_locked_rows(self):
        runtime = self.runtime
        runtime.payload_encryption_version = 2
        ids = sorted(self.insert_task() for _ in range(12))
        self.use_explicit_key()
        with runtime.pool.connection() as connection:
            for task_id in ids[:9]:
                connection.execute("UPDATE gregale_mcp_tasks SET arguments_encrypted=%s WHERE task_id=%s", (runtime._encrypt({"report": "current"}, task_id, "arguments", 1024), task_id))
        changed = 0
        with runtime.pool.connection() as lock:
            lock.execute("SELECT task_id FROM gregale_mcp_tasks WHERE task_id=%s FOR UPDATE", (ids[9],))
            for index in range(9):
                if index == 3:
                    lock.commit()
                count = runtime._rewrap_batch(64)
                if index == 0:
                    self.assertEqual(count, 0)
                changed += count
        self.assertEqual(changed, 3)
        with runtime.pool.connection() as connection:
            for task_id in ids:
                payload = connection.execute("SELECT arguments_encrypted FROM gregale_mcp_tasks WHERE task_id=%s", (task_id,)).fetchone()["arguments_encrypted"]
                self.assert_active_reads(payload, task_id, "arguments")

    def test_failed_batch_preserves_ciphertext_and_cursor(self):
        runtime = self.runtime
        runtime.payload_encryption_version = 2
        ids = sorted(self.insert_task() for _ in range(2))
        with runtime.pool.connection() as connection:
            original = [connection.execute("SELECT arguments_encrypted FROM gregale_mcp_tasks WHERE task_id=%s", (task_id,)).fetchone()["arguments_encrypted"] for task_id in ids]
            corrupt = original[1][:-1] + bytes((original[1][-1] ^ 1,))
            connection.execute("UPDATE gregale_mcp_tasks SET arguments_encrypted=%s WHERE task_id=%s", (corrupt, ids[1]))
        self.use_explicit_key()
        with self.assertRaises(ValueError):
            runtime._rewrap_batch(2)
        with runtime.pool.connection() as connection:
            first = connection.execute("SELECT arguments_encrypted FROM gregale_mcp_tasks WHERE task_id=%s", (ids[0],)).fetchone()["arguments_encrypted"]
            self.assertEqual(first, original[0])
            connection.execute("UPDATE gregale_mcp_tasks SET arguments_encrypted=%s WHERE task_id=%s", (original[1], ids[1]))
        self.assertIsNone(runtime._rewrap_after)
        self.assertIsNone(runtime._rewrap_through)
        self.assertEqual(runtime._rewrap_batch(2), 2)

    def test_distinct_key_ids_keep_fast_path(self):
        runtime = self.runtime
        for version in (1, 2):
            with self.subTest(version=version):
                runtime.active_owner_key_id = "initial"
                runtime.payload_keys = {"initial": runtime.owner_keys[0]["payload_key"]}
                runtime._main_payload_keys = {}
                runtime._legacy_payload_keys = {}
                runtime.payload_encryption_version = version
                task_id = self.insert_task()
                self.use_explicit_key()
                runtime.active_owner_key_id = "next"
                runtime._main_payload_keys["next"] = _derive_key(b"new-key-" * 8, "gregale-mcp-task-payload:v2:next")
                runtime.payload_keys["next"] = runtime._main_payload_keys["next"]
                runtime.payload_encryption_version = 2
                self.assertEqual(runtime._rewrap_batch(2), 1)
                with runtime.pool.connection() as connection:
                    payload = connection.execute("SELECT arguments_encrypted FROM gregale_mcp_tasks WHERE task_id=%s", (task_id,)).fetchone()["arguments_encrypted"]
                    self.assert_active_reads(payload, task_id, "arguments")
