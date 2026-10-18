#!/usr/bin/env python3
"""Isolated pinned Codex acceptance, invoked only by the Go triage fixture."""
import importlib.util
import json
import os
import sys
import tempfile
from pathlib import Path

spec = importlib.util.spec_from_file_location("management_acceptance", Path(__file__).with_name("management-mcp-client-acceptance.py"))
acceptance = importlib.util.module_from_spec(spec)
spec.loader.exec_module(acceptance)


def main():
    phase, mcp_base, codex_binary, phase_path = sys.argv[1:]
    if not mcp_base.startswith("http://127.0.0.1:"):
        raise RuntimeError("Only the isolated loopback fixture is supported")
    if phase not in {"lost-response", "recover", "replay"}:
        raise RuntimeError("Unknown triage acceptance phase")
    config = "mcp_servers={gregale={url=" + acceptance.toml_string(mcp_base) + ",startup_timeout_sec=20,tool_timeout_sec=20}}"
    with tempfile.TemporaryDirectory(prefix="gregale-codex-triage-") as workdir:
        rpc = acceptance.CodexRPC(codex_binary, config, workdir)
        try:
            rpc.request("initialize", {"clientInfo": {"name": "gregale-triage-acceptance", "version": "1"}, "capabilities": {"experimentalApi": True}})
            rpc.send({"method": "initialized"})
            thread = rpc.request("thread/start", {"cwd": workdir, "ephemeral": True, "approvalPolicy": "never", "sandbox": "read-only"})
            thread_id = thread["thread"]["id"]
            status = rpc.request("mcpServerStatus/list", {"threadId": thread_id})
            server = next((item for item in status["data"] if item["name"] == "gregale"), None)
            if not server or server.get("toolsError"):
                raise RuntimeError("Codex could not discover Gregale triage tools")
            inventory = server["tools"]
            names = set(inventory) if isinstance(inventory, dict) else {tool["name"] for tool in inventory}
            if not {"gregale_plan_regression_action", "gregale_apply_regression_action"} <= names:
                raise RuntimeError("Gregale triage tool inventory is incomplete")

            def invoke(tool, arguments, error=False):
                result = rpc.request("mcpServer/tool/call", {"threadId": thread_id, "server": "gregale", "tool": tool, "arguments": arguments})
                value = result.get("structuredContent")
                if not isinstance(value, dict):
                    value = json.loads(next(c["text"] for c in result["content"] if c["type"] == "text"))
                if bool(result.get("isError")) != error:
                    raise RuntimeError("Unexpected triage tool result: " + tool)
                return value

            def save(value):
                fd = os.open(phase_path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
                with os.fdopen(fd, "w") as stream:
                    json.dump(value, stream)

            selector = {"app": "alpha", "deployment_id": "11111111-1111-4111-8111-111111111111", "route": "GET /orders", "action": "acknowledge", "reason": "Investigate retained latency"}
            if phase == "lost-response":
                plan = invoke("gregale_plan_regression_action", selector)
                # The native fixture commits this write, then drops its response.
                invoke("gregale_apply_regression_action", {"plan_id": plan["plan_id"]}, error=True)
                save({"plan_id": plan["plan_id"]})
            elif phase == "recover":
                saved = json.loads(Path(phase_path).read_text())
                receipt = invoke("gregale_apply_regression_action", {"plan_id": saved["plan_id"]})
                if receipt["state"] != "acknowledged":
                    raise RuntimeError("Lost response did not recover its committed receipt")
                if invoke("gregale_apply_regression_action", {"plan_id": saved["plan_id"]}) != receipt:
                    raise RuntimeError("Cached receipt changed")
                for action, state in [("dismiss", "dismissed"), ("resolve", "resolved"), ("reopen", "active")]:
                    selector["action"] = action
                    plan = invoke("gregale_plan_regression_action", selector)
                    receipt = invoke("gregale_apply_regression_action", {"plan_id": plan["plan_id"]})
                    if receipt["state"] != state or "PRIVATE_SQL" in json.dumps(receipt):
                        raise RuntimeError("Incorrect or unsafe workflow receipt")
                save({"plan_id": plan["plan_id"], "receipt": receipt})
            else:
                saved = json.loads(Path(phase_path).read_text())
                if invoke("gregale_apply_regression_action", {"plan_id": saved["plan_id"]}) != saved["receipt"]:
                    raise RuntimeError("Successful receipt did not survive bridge restart")
            print(json.dumps({"client": "codex", "fixture": True, "phase": phase, "result": "passed", "model_requests": 0, "deployment_writes": 0}))
        finally:
            rpc.close()


if __name__ == "__main__":
    main()
