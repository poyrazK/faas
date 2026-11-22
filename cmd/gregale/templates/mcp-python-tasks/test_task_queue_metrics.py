"""PostgreSQL coverage for native worker registration and queue-state gauges."""
import os
import threading
import unittest
import uuid

import test_task_execution as execution
from task_execution import TaskExecutionPolicy


@unittest.skipUnless(os.environ.get("MCP_TASKS_TEST_DATABASE_URL"), "set MCP_TASKS_TEST_DATABASE_URL")
class TaskQueueMetricsPostgresTests(unittest.TestCase):
    setUp = execution.TaskExecutionPostgresTests.setUp
    insert_task = execution.TaskExecutionPostgresTests.insert_task

    def test_queue_states_and_owner_alias_capacity(self):
        runtime = self.runtime
        runtime.execution = TaskExecutionPolicy({"max_running": 2, "max_running_per_owner": 1})
        runtime._worker_registry_lock = threading.Lock()
        runtime._worker_id = uuid.uuid4()
        runtime._deployment_id = "deployment-test"
        runtime.drain_timeout = 10
        failed = self.insert_task()
        with runtime.pool.connection() as connection:
            connection.execute("UPDATE gregale_mcp_tasks SET status='failed' WHERE task_id=%s", (failed,))
        metrics = runtime.queue_metrics()
        self.assertEqual((metrics["outstanding_tasks"], metrics["failed_tasks"], metrics["oldest_age_seconds"]), (0, 1, 0))
        ids = [self.insert_task(owner) for owner in (b"alice", b"alias", b"bob", b"carol", b"dave", b"expired")]
        with runtime.pool.connection() as connection:
            execution.associate_owners(connection, runtime.namespace, [b"alice", b"alias"])
            connection.execute("UPDATE gregale_mcp_tasks SET status='running',lease_token=gen_random_uuid(),lease_expires_at=clock_timestamp()+interval '1 minute',attempt_count=1 WHERE task_id=%s", (ids[0],))
            connection.execute("UPDATE gregale_mcp_tasks SET next_attempt_at=clock_timestamp()+interval '1 hour' WHERE task_id=%s", (ids[2],))
            connection.execute("UPDATE gregale_mcp_tasks SET handler_version='unsupported' WHERE task_id=%s", (ids[3],))
            connection.execute("UPDATE gregale_mcp_tasks SET status='running',lease_token=gen_random_uuid(),lease_expires_at=clock_timestamp()-interval '1 second',attempt_count=1 WHERE task_id=%s", (ids[4],))
            connection.execute("UPDATE gregale_mcp_tasks SET created_at=clock_timestamp()-interval '2 hours',expires_at=clock_timestamp()-interval '1 hour' WHERE task_id=%s", (ids[5],))
            connection.execute("INSERT INTO gregale_mcp_task_workers(namespace,worker_id,handlers,draining,heartbeat_at,expires_at) VALUES(%s,gen_random_uuid(),'[]',true,clock_timestamp(),clock_timestamp()+interval '1 minute')", (runtime.namespace,))
        runtime._heartbeat_worker_capabilities()
        metrics = runtime.queue_metrics()
        expected = {"outstanding_tasks": 5, "running_tasks": 1, "capacity_waiting_tasks": 1,
                    "failed_tasks": 1, "retry_waiting_tasks": 1, "active_workers": 1,
                    "draining_workers": 1, "unsupported_handler_tasks": 1, "without_compatible_worker_tasks": 1}
        self.assertEqual({key: metrics[key] for key in expected}, expected)
        runtime.execution.max_running = 1
        self.assertEqual(runtime.queue_metrics()["capacity_waiting_tasks"], 3)
        runtime._stopping.set()
        runtime._mark_worker_draining()
        runtime._heartbeat_worker_capabilities()  # A late heartbeat must not reactivate a draining worker.
        metrics = runtime.queue_metrics()
        self.assertEqual((metrics["active_workers"], metrics["draining_workers"], metrics["unsupported_handler_tasks"], metrics["without_compatible_worker_tasks"]), (0, 2, 0, 3))
