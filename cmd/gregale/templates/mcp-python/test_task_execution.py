"""Cross-replica Task limits, durable retries and retained owner identities."""

from concurrent.futures import ThreadPoolExecutor
import os
import threading
import unittest
import uuid

from auth import AuthContext
from task_owners import associate_owners, cleanup_expired_tasks
from task_execution import RetryableTaskError, TaskExecutionPolicy
from tasks import DurableTaskRuntime, TaskNotFound, _derive_key, _snapshot_task_handlers, check_task_schema, migrate_task_schema


OWNER_KEY = b"01234567890123456789012345678901"
CANONICAL_OWNER = "4fbc7a60b90569b9ac1b0d8b0c1f59ce770120370a5da54c662ce779b04edcab"
LEGACY_GO_OWNER = "803002df4e65c74a4f74c141874d907b0d661cb39332bc05c3424af3145a2928"


class TaskExecutionPolicyTests(unittest.TestCase):
    def test_invalid_settings_fail_closed(self):
        for settings in (
            {"max_attempts": -1}, {"max_attempts": 11}, {"max_running": False}, {"max_running": 0.0},
            {"max_running": 2, "max_running_per_owner": 3}, {"retry_base_delay_ms": 99},
            {"retry_base_delay_ms": 200, "retry_max_delay_ms": 100},
            {"retry_max_delay_ms": 86400001},
        ):
            with self.subTest(settings=settings), self.assertRaises(ValueError):
                TaskExecutionPolicy(settings)

    def test_owner_identity_matches_node_and_preserves_legacy_go(self):
        runtime = object.__new__(DurableTaskRuntime)
        runtime.owner_keys = [{"owner_hash_key": _derive_key(OWNER_KEY, "gregale-mcp-task-owner:v1")}]
        identity = AuthContext(subject="alice", client_id="test-client", resource="https://example.com/mcp")
        self.assertEqual([value.hex() for value in runtime._owner_hashes(identity)],
                         [CANONICAL_OWNER, LEGACY_GO_OWNER])


if os.environ.get("MCP_TASKS_REQUIRE_POSTGRES") == "1" and not os.environ.get("MCP_TASKS_TEST_DATABASE_URL"):
    raise RuntimeError("MCP_TASKS_TEST_DATABASE_URL is required")


@unittest.skipUnless(os.environ.get("MCP_TASKS_TEST_DATABASE_URL"), "set MCP_TASKS_TEST_DATABASE_URL")
class TaskExecutionPostgresTests(unittest.TestCase):
    def setUp(self):
        import psycopg
        from psycopg import sql
        from psycopg.conninfo import make_conninfo
        from psycopg.rows import dict_row
        from psycopg_pool import ConnectionPool
        from cryptography.hazmat.primitives.ciphers.aead import AESGCM

        database_url = os.environ["MCP_TASKS_TEST_DATABASE_URL"]
        admin = psycopg.connect(database_url, autocommit=True)
        self.addCleanup(admin.close)
        schema = "mcp_execution_" + uuid.uuid4().hex
        admin.execute(sql.SQL("CREATE SCHEMA {}").format(sql.Identifier(schema)))
        self.addCleanup(lambda: admin.execute(sql.SQL("DROP SCHEMA {} CASCADE").format(sql.Identifier(schema))))
        pool = ConnectionPool(make_conninfo(database_url, options="-c search_path=" + schema + ",pg_catalog"),
                              min_size=1, max_size=8, kwargs={"row_factory": dict_row})
        self.addCleanup(pool.close)
        migrate_task_schema(pool)
        runtime = object.__new__(DurableTaskRuntime)
        runtime.pool, runtime.namespace, runtime.role = pool, "execution-test", "web"
        runtime.execution = TaskExecutionPolicy({})
        runtime._stopping, runtime._drain_expired = threading.Event(), threading.Event()
        runtime._task_handlers = _snapshot_task_handlers()
        runtime._task_executors = {(name, version): execute
                                   for name, handler in runtime._task_handlers.items()
                                   for version, execute in handler["executors"].items()}
        payload_key = _derive_key(OWNER_KEY, "gregale-mcp-task-payload:v1")
        runtime.owner_keys = [{"owner_hash_key": _derive_key(OWNER_KEY, "gregale-mcp-task-owner:v1"),
                               "payload_key": payload_key}]
        runtime.payload_keys, runtime._main_payload_keys = {"initial": payload_key}, {}
        runtime.active_owner_key_id, runtime.payload_encryption_version = "initial", 1
        runtime._aesgcm, runtime.ttl_ms, runtime.poll_interval_ms = AESGCM, 3600000, 2000
        runtime.max_outstanding, runtime.max_per_owner = 1000, 100
        self.runtime = runtime

    def insert_task(self, owner=b"alice"):
        task_id = str(uuid.uuid4())
        encrypted = self.runtime._encrypt({"report": "test", "steps": 1}, task_id, "arguments", 256 * 1024)
        with self.runtime.pool.connection() as connection:
            if len(owner)!=32:
                associate_owners(connection, self.runtime.namespace, [owner])
            connection.execute("""INSERT INTO gregale_mcp_tasks
                (namespace,task_id,owner_hash,tool_name,handler_version,arguments_encrypted,status,expires_at)
                VALUES(%s,%s,%s,'build_report','2',%s,'queued',clock_timestamp()+interval '1 hour')""",
                (self.runtime.namespace, task_id, owner, encrypted))
        return task_id

    def test_aliases_share_capacity_before_and_after_authentication(self):
        self.runtime.execution = TaskExecutionPolicy({"max_running": 3, "max_running_per_owner": 1})
        identity = AuthContext(subject="alice", client_id="test-client", resource="https://example.com/mcp")
        owners = self.runtime._owner_hashes(identity)
        for owner in owners:
            self.insert_task(owner)
        self.assertIsNotNone(self.runtime._claim())
        self.assertIsNone(self.runtime._claim())
        with self.runtime.pool.connection() as connection:
            associate_owners(connection, self.runtime.namespace, owners)
        self.assertIsNone(self.runtime._claim())
        bob = AuthContext(subject="bob", client_id="test-client", resource="https://example.com/mcp")
        bob_owners = self.runtime._owner_hashes(bob)
        with self.runtime.pool.connection() as connection:
            associate_owners(connection, self.runtime.namespace, bob_owners)
        task_id = self.insert_task(bob_owners[1])
        self.assertEqual(self.runtime._claim()["task_id"], task_id)
        with self.runtime.pool.connection() as connection:
            associate_owners(connection, self.runtime.namespace, [b"old-generation", owners[0]])
            associate_owners(connection, self.runtime.namespace, [b"new-generation", b"old-generation"])
            row = connection.execute("SELECT COUNT(DISTINCT owner_group) AS groups FROM gregale_mcp_task_owners WHERE namespace=%s AND owner_hash=ANY(%s::bytea[])",
                (self.runtime.namespace, owners + [b"old-generation", b"new-generation"])).fetchone()
            self.assertEqual(row["groups"], 1)

    def test_alias_cleanup_retains_whole_group_until_last_task_expires(self):
        owners = self.runtime._owner_hashes(AuthContext(subject="alice"))
        with self.runtime.pool.connection() as connection:
            associate_owners(connection, self.runtime.namespace, owners)
            associate_owners(connection, self.runtime.namespace, [b"unused-owner"])
        task_id = self.insert_task(owners[1])
        with self.runtime.pool.connection() as connection:
            cleanup_expired_tasks(connection, self.runtime.namespace)
            row = connection.execute("SELECT COUNT(*) AS count FROM gregale_mcp_task_owners WHERE namespace=%s", (self.runtime.namespace,)).fetchone()
            self.assertEqual(row["count"], 2)
            connection.execute("UPDATE gregale_mcp_tasks SET created_at=clock_timestamp()-interval '1 hour',expires_at=clock_timestamp()-interval '1 second' WHERE task_id=%s", (task_id,))
        with self.runtime.pool.connection() as connection:
            cleanup_expired_tasks(connection, self.runtime.namespace)
            row = connection.execute("SELECT COUNT(*) AS count FROM gregale_mcp_task_owners WHERE namespace=%s", (self.runtime.namespace,)).fetchone()
            self.assertEqual(row["count"], 0)

    def test_capacity_across_concurrent_replicas(self):
        self.runtime.execution = TaskExecutionPolicy({"max_running": 3, "max_running_per_owner": 2})
        for _ in range(10):
            self.insert_task()
        with ThreadPoolExecutor(max_workers=10) as executor:
            claimed = list(executor.map(lambda _: self.runtime._claim(), range(10)))
        self.assertEqual(sum(task is not None for task in claimed), 2)
        self.insert_task(b"bob")
        self.insert_task(b"bob")
        with ThreadPoolExecutor(max_workers=10) as executor:
            claimed = list(executor.map(lambda _: self.runtime._claim(), range(10)))
        self.assertEqual(sum(task is not None for task in claimed), 1)

    def test_retry_schedule_and_final_attempt_input_resume(self):
        self.runtime.execution = TaskExecutionPolicy({"max_attempts": 5})
        task_id = self.insert_task()
        with self.runtime.pool.connection() as connection:
            connection.execute("UPDATE gregale_mcp_tasks SET attempt_count=3,next_attempt_at=clock_timestamp()+interval '1 day' WHERE task_id=%s", (task_id,))
        self.assertIsNone(self.runtime._claim())
        with self.runtime.pool.connection() as connection:
            connection.execute("UPDATE gregale_mcp_tasks SET next_attempt_at=clock_timestamp()-interval '1 second' WHERE task_id=%s", (task_id,))
        self.assertEqual(self.runtime._claim()["attempt_count"], 4)
        with self.runtime.pool.connection() as connection:
            connection.execute("UPDATE gregale_mcp_tasks SET status='queued',attempt_count=5,resume_pending=true,lease_token=NULL,lease_expires_at=NULL WHERE task_id=%s", (task_id,))
        self.assertEqual(self.runtime._claim()["attempt_count"], 5)
        with self.runtime.pool.connection() as connection:
            connection.execute("UPDATE gregale_mcp_tasks SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE task_id=%s", (task_id,))
        self.assertIsNone(self.runtime._claim())

    def test_lowered_budget_settles_queued_retries(self):
        self.runtime.execution = TaskExecutionPolicy({"max_attempts": 5})
        task_id = self.insert_task()
        for attempt in range(1, 4):
            task = self.runtime._claim()
            self.assertEqual(task["attempt_count"], attempt)
            self.runtime._finish(task, "failed", retryable=True)
            if attempt < 3:
                with self.runtime.pool.connection() as connection:
                    connection.execute("UPDATE gregale_mcp_tasks SET next_attempt_at=clock_timestamp()-interval '1 second' WHERE task_id=%s", (task_id,))
        self.runtime.execution = TaskExecutionPolicy({"max_attempts": 3})
        self.assertIsNone(self.runtime._claim())
        with self.runtime.pool.connection() as connection:
            task = connection.execute("SELECT status,next_attempt_at,resume_pending FROM gregale_mcp_tasks WHERE task_id=%s", (task_id,)).fetchone()
        self.assertEqual(task, {"status": "failed", "next_attempt_at": None, "resume_pending": False})
        self.assertEqual(self.runtime.queue_metrics()["outstanding_tasks"], 0)

    def test_lowered_budget_preserves_active_attempt_and_drain_resume(self):
        self.runtime.execution = TaskExecutionPolicy({"max_attempts": 5})
        task_id = self.insert_task()
        with self.runtime.pool.connection() as connection:
            connection.execute("UPDATE gregale_mcp_tasks SET attempt_count=3 WHERE task_id=%s", (task_id,))
        active = self.runtime._claim()
        self.assertEqual(active["attempt_count"], 4)
        self.runtime.execution = TaskExecutionPolicy({"max_attempts": 3})
        self.assertIsNone(self.runtime._claim())
        self.runtime._release_drained_task(active)
        self.assertEqual(self.runtime.queue_metrics()["without_compatible_worker_tasks"], 1)
        resumed = self.runtime._claim()
        self.assertEqual((resumed["task_id"], resumed["attempt_count"]), (task_id, 4))
        self.runtime._finish(resumed, "failed", retryable=True)
        self.assertIsNone(self.runtime._claim())

    def test_expired_lease_cannot_be_revived_or_finish_replacement(self):
        self.runtime.execution = TaskExecutionPolicy({"max_running": 1, "max_running_per_owner": 1})
        task_id = self.insert_task()
        old = self.runtime._claim()
        with self.runtime.pool.connection() as connection:
            connection.execute("UPDATE gregale_mcp_tasks SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE task_id=%s", (task_id,))
        self.assertIsNone(self.runtime._heartbeat(task_id, old["lease_token"]))
        replacement = self.runtime._claim()
        self.assertIsNotNone(replacement)
        self.assertNotEqual(old["lease_token"], replacement["lease_token"])
        self.runtime._finish(old, "failed", retryable=True)
        with self.runtime.pool.connection() as connection:
            self.assertEqual(connection.execute("SELECT status FROM gregale_mcp_tasks WHERE task_id=%s", (task_id,)).fetchone()["status"], "running")

    def test_only_explicit_handler_failures_retry(self):
        self.runtime.execution = TaskExecutionPolicy({"retry_base_delay_ms": 1000, "retry_max_delay_ms": 1000})
        for scenario, expected in (("retryable", "queued"), ("terminal", "failed"),
                                   ("cancelled", "cancelled"), ("ttl", "failed"), ("exhausted", "failed")):
            with self.subTest(scenario=scenario):
                task_id = self.insert_task()
                task = self.runtime._claim()
                patches = {"cancelled": "cancel_requested_at=clock_timestamp()",
                           "ttl": "expires_at=clock_timestamp()+interval '100 milliseconds'",
                           "exhausted": "attempt_count=3"}
                if scenario in patches:
                    with self.runtime.pool.connection() as connection:
                        connection.execute("UPDATE gregale_mcp_tasks SET " + patches[scenario] + " WHERE task_id=%s", (task_id,))

                def handler(_arguments, _context):
                    if scenario == "terminal":
                        raise ValueError("private provider details")
                    raise RetryableTaskError("private provider details")

                self.runtime._task_executors[("build_report", "2")] = handler
                self.runtime._run_task(task, threading.Event())
                with self.runtime.pool.connection() as connection:
                    row = connection.execute("SELECT status,next_attempt_at FROM gregale_mcp_tasks WHERE task_id=%s", (task_id,)).fetchone()
                    self.assertEqual(row["status"], expected)
                    self.assertEqual(row["next_attempt_at"] is not None, scenario == "retryable")
                    # Keep one subcase's queued retry out of subsequent claims.
                    connection.execute("DELETE FROM gregale_mcp_tasks WHERE task_id=%s", (task_id,))

    def test_legacy_go_ownership_is_readable_but_remains_isolated(self):
        identity = AuthContext(subject="alice", client_id="test-client", resource="https://example.com/mcp")
        task_id = self.insert_task(bytes.fromhex(LEGACY_GO_OWNER))
        self.assertEqual(str(self.runtime._read_task(task_id, identity)["task_id"]), task_id)
        foreign = AuthContext(subject="bob", client_id="test-client", resource="https://example.com/mcp")
        with self.assertRaises(TaskNotFound):
            self.runtime._read_task(task_id, foreign)
        self.runtime.max_per_owner = 1
        with self.assertRaises(ValueError):
            self.runtime.create("build_report", {"report": "new", "steps": 1}, identity)

    def test_migration_adds_retry_state_without_replacing_tasks(self):
        task_id = self.insert_task()
        with self.runtime.pool.connection() as connection:
            connection.execute("ALTER TABLE gregale_mcp_tasks DROP COLUMN next_attempt_at")
        with self.assertRaises(Exception):
            check_task_schema(self.runtime.pool)
        migrate_task_schema(self.runtime.pool)
        check_task_schema(self.runtime.pool)
        self.assertEqual(self.runtime._claim()["task_id"], task_id)
