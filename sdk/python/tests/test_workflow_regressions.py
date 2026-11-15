"""Regression coverage for durable workflow invocation boundaries."""
import asyncio
import importlib.util
import json
from pathlib import Path
import sys
import unittest

# Load the self-contained runtime without importing generated API clients.
spec = importlib.util.spec_from_file_location("workflow_regression_runtime", Path(__file__).parents[1] / "faas_sdk/workflows.py")
w = importlib.util.module_from_spec(spec)
sys.modules[spec.name] = w
spec.loader.exec_module(w)


async def invoke(run, journal=None):
    return await w.run_workflow_invocation(w.WorkflowDefinition("regression", run), {
        "protocol": w.WORKFLOW_CODE_PROTOCOL, "run_id": "run", "workflow": "regression",
        "input": {}, "invocation": 1, "journal": journal or [],
    })


class WorkflowRegressions(unittest.IsolatedAsyncioTestCase):
    async def test_nested_parallel_branches_reach_their_steps(self):
        async def workflow(input, wf):
            async def nested(depth, name):
                if depth:
                    return await asyncio.gather(nested(depth - 1, name))
                return await wf.step(name, lambda ctx: name)
            return await asyncio.gather(nested(1, "shallow"), nested(30, "deep"))

        response = await invoke(workflow)
        self.assertEqual([op["name"] for op in response["ops"]], ["shallow", "deep"])
        self.assertEqual(response["result"]["status"], "running")

    async def test_nested_gather_return_value_is_not_cancelled(self):
        async def workflow(input, wf):
            async def nested(depth):
                return await asyncio.gather(nested(depth - 1)) if depth else 42
            return await nested(30)

        response = await invoke(workflow)
        self.assertEqual(response["result"]["status"], "succeeded")

    async def test_large_errors_fit_in_response(self):
        for message in ("x" * (2 * 1024 * 1024), "😀" * 5000):
            async def workflow(input, wf):
                def fail(context):
                    raise w.NonRetryableError(message)
                await wf.step("work", fail)

            response = await invoke(workflow)
            self.assertTrue(response["ops"][0]["non_retryable"])
            self.assertLessEqual(len(json.dumps(response).encode()), w.WORKFLOW_RESPONSE_MAX_BYTES)

    async def test_nonfinite_step_result_is_a_nonretryable_failure(self):
        for value in (float("nan"), float("inf"), float("-inf")):
            async def workflow(input, wf):
                return await wf.step("work", lambda ctx: value)
            response = await invoke(workflow)
            self.assertEqual(response["ops"][0]["status"], "failed")
            self.assertTrue(response["ops"][0]["non_retryable"])
            json.dumps(response, allow_nan=False)

    async def test_large_terminal_error_fits_in_response(self):
        async def workflow(input, wf):
            raise ValueError("failure" * 300000)
        response = await invoke(workflow)
        self.assertEqual(response["result"]["status"], "failed")
        self.assertLessEqual(len(json.dumps(response).encode()), w.WORKFLOW_RESPONSE_MAX_BYTES)

    async def test_parallel_errors_share_one_response_budget(self):
        async def workflow(input, wf):
            def fail(context):
                raise w.NonRetryableError("failure" * 1000)
            return await asyncio.gather(*(wf.step(f"work-{i}", fail) for i in range(1000)))
        response = await invoke(workflow)
        self.assertEqual(len(response["ops"]), 1000)
        self.assertLessEqual(len(json.dumps(response).encode()), w.WORKFLOW_RESPONSE_MAX_BYTES)

    async def test_replayed_cursor_detects_removed_history(self):
        async def workflow(input, wf):
            return "done"
        response = await invoke(workflow, [{"seq": 0, "name": "approval", "kind": "event", "status": "waiting"}])
        self.assertEqual(response["replayed_ops"], 0)

    async def test_repeated_maximum_base_name_is_canonical(self):
        name = "a" * 128
        async def workflow(input, wf):
            for _ in range(2):
                await wf.step(name, lambda ctx: 1)
        response = await invoke(workflow, [{"seq": 0, "name": name, "kind": "step", "status": "succeeded", "attempt": 1, "output": 1}])
        self.assertEqual(response["ops"][0]["name"], name + "#2")
        self.assertEqual(response["replayed_ops"], 1)
