"""Output contracts follow durable tasks through retries and worker handoff."""
import copy
import json
import os
import threading
import unittest

from task_execution import RetryableTaskError
import test_task_execution as fixtures


def output_schema():
    return {"type": "object", "required": ["amount"], "additionalProperties": False,
            "$defs": {"amount": {"type": "integer", "minimum": 1}},
            "properties": {"amount": {"$ref": "#/$defs/amount"}}}


@unittest.skipUnless(os.environ.get("MCP_TASKS_TEST_DATABASE_URL"), "set MCP_TASKS_TEST_DATABASE_URL")
class TaskOutputTests(unittest.TestCase):
    setUp = fixtures.TaskExecutionPostgresTests.setUp

    def create(self, schema, capabilities=None):
        return self.runtime.create("build_report", {"report": "review", "steps": 1}, None, capabilities,
                                   output_schema=schema)["taskId"]

    def status(self, task_id):
        with self.runtime.pool.connection() as connection:
            return connection.execute("SELECT status,result_encrypted,error_encrypted,output_schema FROM gregale_mcp_tasks WHERE task_id=%s", (task_id,)).fetchone()

    def test_tool_call_accepts_empty_form_capability_and_parks_for_approval(self):
        from auth import Authenticator
        from server import call_tool
        auth = Authenticator({"endpoint": "/mcp", "auth": {"mode": "open"}})
        accepted = call_tool(1, {"name": "build_report", "arguments": {
            "report": "review", "steps": 1, "request_approval": True}, "_meta": {
                "io.modelcontextprotocol/clientCapabilities": {
                    "extensions": {"io.modelcontextprotocol/tasks": {}}, "elicitation": {"form": {}}}}},
            auth, set(), task_runtime=self.runtime)["result"]
        self.assertEqual(accepted.get("resultType"), "task", accepted)
        task = self.runtime._claim()
        self.assertIn("elicitation/create:form", task["input_methods"])
        self.runtime._run_task(task, threading.Event())
        self.assertEqual(self.status(accepted["taskId"])["status"], "input_required")

    def test_workers_validate_saved_contracts_and_redact_invalid_results(self):
        cases = [
            ({"content": [], "structuredContent": {"amount": "secret-output"}}, "failed"),
            ({"content": []}, "failed"),
            ({"content": [], "structuredContent": {}}, "failed"),
            ({"content": [], "structuredContent": {"amount": 0}}, "failed"),
            ({"structuredContent": {"amount": 2}}, "failed"),
            ({"content": [], "resultType": "input_required"}, "failed"),
            ({"content": [], "structuredContent": {"amount": 2}}, "completed"),
            ({"isError": True, "content": [{"type": "text", "text": "business rejection"}]}, "completed"),
        ]
        for result, expected in cases:
            with self.subTest(result=result):
                schema = output_schema()
                task_id = self.create(schema)
                schema["required"].clear()
                task = self.runtime._claim()
                self.assertIsNotNone(task.get("output_schema"))
                self.runtime._task_executors[("build_report", "2")] = lambda *_: copy.deepcopy(result)
                self.runtime._run_task(task, threading.Event())
                row = self.status(task_id)
                self.assertEqual(row["status"], expected)
                if expected == "failed":
                    self.assertIsNone(row["result_encrypted"])
                    error = self.runtime._decrypt(row["error_encrypted"], task_id, "error")
                    self.assertNotIn("secret-output", json.dumps(error))

    def test_contract_survives_retry_and_input_resume_without_the_original_catalog(self):
        for pause in (False, True):
            with self.subTest(input_pause=pause):
                runtime = self.runtime
                schema = output_schema()
                request = {"method": "elicitation/create", "params": {
                    "message": "Approve?", "requestedSchema": {"type": "object", "properties": {}}}}
                task_id = self.create(schema, {"elicitation": {"form": {}}})
                def first(_arguments, execution):
                    if pause:
                        execution.request_input("approval", request)
                        raise AssertionError("input was not parked")
                    raise RetryableTaskError()
                runtime._task_executors[("build_report", "2")] = first
                task = runtime._claim()
                runtime._run_task(task, threading.Event())
                if pause:
                    self.assertEqual(self.status(task_id)["status"], "input_required")
                    auth = type("Auth", (), {"can_access_tool": lambda *_: True})()
                    runtime.update_inputs(task_id, {"approval": {"action": "accept", "content": {}}}, None, auth, set())
                else:
                    with runtime.pool.connection() as connection:
                        connection.execute("UPDATE gregale_mcp_tasks SET next_attempt_at=clock_timestamp() WHERE task_id=%s", (task_id,))
                schema["$defs"]["amount"]["type"] = "string"
                def replacement(_arguments, execution):
                    if pause:
                        execution.request_input("approval", request)
                    return {"content": [], "structuredContent": {"amount": 2}}
                # Swap the executor as a restarted/retained worker would; the
                # validation contract is read from PostgreSQL, not that process.
                runtime._task_executors = {("build_report", "2"): replacement}
                task = runtime._claim()
                self.assertIsNotNone(task)
                runtime._run_task(task, threading.Event())
                self.assertEqual(self.status(task_id)["status"], "completed")

    def test_invalid_schema_is_not_admitted_and_explicit_migration_upgrades_existing_tables(self):
        from tasks import check_task_schema, migrate_task_schema
        with self.assertRaises(Exception):
            self.create({"type": 42})
        with self.runtime.pool.connection() as connection:
            self.assertEqual(connection.execute("SELECT count(*) AS count FROM gregale_mcp_tasks").fetchone()["count"], 0)
            connection.execute("ALTER TABLE gregale_mcp_tasks DROP COLUMN output_schema")
        with self.assertRaises(Exception):
            check_task_schema(self.runtime.pool)
        migrate_task_schema(self.runtime.pool)
        check_task_schema(self.runtime.pool)
        task_id = self.create(None)
        self.runtime._task_executors[("build_report", "2")] = lambda *_: {"content": []}
        self.runtime._run_task(self.runtime._claim(), threading.Event())
        self.assertEqual(self.status(task_id)["status"], "completed")
