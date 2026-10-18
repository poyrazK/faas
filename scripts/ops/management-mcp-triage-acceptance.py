#!/usr/bin/env python3
"""Isolated pinned Codex acceptance, invoked only by the Go triage fixture."""
import importlib.util
import json
import sys
import tempfile
from pathlib import Path

spec = importlib.util.spec_from_file_location("management_acceptance", Path(__file__).with_name("management-mcp-client-acceptance.py"))
acceptance = importlib.util.module_from_spec(spec)
spec.loader.exec_module(acceptance)


def main():
    test_binary, api_base, codex_binary, state_path, key_path = sys.argv[1:]
    if not api_base.startswith("http://127.0.0.1:"):
        raise RuntimeError("Only the isolated loopback fixture is supported")
    env = {"GREGALE_TEST_TRIAGE_API": api_base, "GREGALE_TEST_TRIAGE_STATE": state_path, "GREGALE_TEST_TRIAGE_KEY": key_path}
    env_toml = ",".join(k + "=" + acceptance.toml_string(v) for k, v in env.items())
    config = "mcp_servers={gregale={command=" + acceptance.toml_string(test_binary) + ',args=["-test.run=^TestRegressionTriageCodexSubprocess$"],env={' + env_toml + "},startup_timeout_sec=20,tool_timeout_sec=20}}"
    with tempfile.TemporaryDirectory(prefix="gregale-codex-triage-") as workdir:
        rpc = None
        def start():
            client = acceptance.CodexRPC(codex_binary, config, workdir)
            client.request("initialize", {"clientInfo": {"name": "gregale-triage-acceptance", "version": "1"}, "capabilities": {"experimentalApi": True}})
            client.send({"method": "initialized"})
            thread = client.request("thread/start", {"cwd": workdir, "ephemeral": True, "approvalPolicy": "never", "sandbox": "read-only"})
            return client, thread["thread"]["id"]

        def invoke(tool, arguments, error=False):
            result = rpc.request("mcpServer/tool/call", {"threadId": thread_id, "server": "gregale", "tool": tool, "arguments": arguments})
            value = result.get("structuredContent")
            if not isinstance(value, dict):
                value = json.loads(next(c["text"] for c in result["content"] if c["type"] == "text"))
            if bool(result.get("isError")) != error:
                raise RuntimeError("Unexpected triage tool result: " + tool)
            return value

        try:
            rpc, thread_id = start()
            selector = {"app": "alpha", "deployment_id": "11111111-1111-4111-8111-111111111111", "route": "GET /orders", "action": "acknowledge", "reason": "Investigate retained latency"}
            plan = invoke("gregale_plan_regression_action", selector)
            # The native fixture commits this first write then drops the response.
            invoke("gregale_apply_regression_action", {"plan_id": plan["plan_id"]}, error=True)
            rpc.close()
            rpc, thread_id = start()
            receipt = invoke("gregale_apply_regression_action", {"plan_id": plan["plan_id"]})
            if receipt["state"] != "acknowledged":
                raise RuntimeError("Lost response did not recover its committed receipt")
            if invoke("gregale_apply_regression_action", {"plan_id": plan["plan_id"]}) != receipt:
                raise RuntimeError("Cached receipt changed")
            for action, state in [("dismiss", "dismissed"), ("resolve", "resolved"), ("reopen", "active")]:
                selector["action"] = action
                plan = invoke("gregale_plan_regression_action", selector)
                result = invoke("gregale_apply_regression_action", {"plan_id": plan["plan_id"]})
                if result["state"] != state or "PRIVATE_SQL" in json.dumps(result):
                    raise RuntimeError("Incorrect or unsafe workflow receipt")
            rpc.close()
            rpc, thread_id = start()
            if invoke("gregale_apply_regression_action", {"plan_id": plan["plan_id"]}) != result:
                raise RuntimeError("Successful receipt did not survive restart")
            print(json.dumps({"client": "codex", "fixture": True, "triage_actions": 4, "lost_response": "passed", "encrypted_restart": "passed", "cached_receipt": "passed", "model_requests": 0, "deployment_writes": 0}))
        finally:
            if rpc:
                rpc.close()


if __name__ == "__main__":
    main()
