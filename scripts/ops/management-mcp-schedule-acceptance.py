#!/usr/bin/env python3
"""Model-free real Codex schedule recovery/watch acceptance; isolated fixture only."""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import shutil
import sys
import tempfile
from urllib.parse import urlsplit

sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location(
    "gregale_codex_acceptance", Path(__file__).with_name("management-mcp-client-acceptance.py")
)
harness = importlib.util.module_from_spec(spec)
spec.loader.exec_module(harness)


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def fixture_url(name, path=None):
    value = os.environ[name]
    parsed = urlsplit(value)
    require(parsed.scheme == "http" and parsed.hostname == "127.0.0.1"
            and parsed.port is not None and not parsed.username and not parsed.password
            and not parsed.query and not parsed.fragment
            and (path is None or parsed.path == path), "Schedule acceptance requires loopback fixtures")
    return value


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--codex-binary", required=True)
    args = parser.parse_args()
    binary = str(Path(shutil.which(args.codex_binary) or args.codex_binary).resolve())
    resource = fixture_url("GREGALE_MCP_TEST_RESOURCE", "/mcp")
    fixture_url("GREGALE_MCP_TEST_CONTROL", "")
    require(bool(os.environ.get("GREGALE_MCP_ACCESS_TOKEN")), "Missing fixture credential")
    config = 'mcp_servers={gregale={url=' + json.dumps(resource) + ',bearer_token_env_var="GREGALE_MCP_ACCESS_TOKEN",startup_timeout_sec=20,tool_timeout_sec=20}}'
    scope = {"app": "alpha", "project": "shop", "environment": "staging"}
    recovery_tool = "gregale_recover_environment_cache_schedule_activity"
    watch_tool = "gregale_watch_environment_cache_schedule_activity"

    with tempfile.TemporaryDirectory(prefix="gregale-codex-schedules-") as workdir:
        def connect():
            rpc = harness.CodexRPC(binary, config, workdir)
            try:
                rpc.request("initialize", {"clientInfo": {"name": "gregale-schedule-acceptance", "version": "1"}, "capabilities": {"experimentalApi": True}})
                rpc.send({"method": "initialized"})
                thread = rpc.request("thread/start", {"cwd": workdir, "ephemeral": True, "approvalPolicy": "never", "sandbox": "read-only"})
                thread_id = thread["thread"]["id"]
                inventory = rpc.request("mcpServerStatus/list", {"threadId": thread_id})
                server = next((item for item in inventory["data"] if item["name"] == "gregale"), None)
                require(server is not None and not server.get("toolsError"), "Schedule client discovery failed")
                tools = server["tools"]
                names = set(tools) if isinstance(tools, dict) else {tool["name"] for tool in tools}
                require({recovery_tool, watch_tool} <= names, "Missing schedule workflow tools")
                require(not any(name.startswith("gregale_apply_") or name == "gregale_cancel_environment_cache_schedule" for name in names), "Fixture exposed mutations")
                return rpc, thread_id
            except BaseException:
                rpc.close()
                raise

        rpc, thread_id = connect()
        try:
            def call(tool, extra, error=None):
                result = rpc.request("mcpServer/tool/call", {"threadId": thread_id, "server": "gregale", "tool": tool, "arguments": {**scope, **extra}})
                value = result.get("structuredContent")
                if not isinstance(value, dict):
                    value = json.loads(next(item["text"] for item in result["content"] if item["type"] == "text"))
                require(bool(result.get("isError")) == (error is not None), "Unexpected schedule workflow result")
                if error is not None:
                    require(value.get("code") == error, "Unexpected schedule workflow error code")
                    require(not any(key in value for key in ("entries", "activity_cursor", "recovery_token", "watch_status")), "Failed workflow leaked a successful page")
                return value

            first = call(recovery_tool, {"limit": 1})
            require(first.get("complete") is False and len(first["entries"]) == 1
                    and first.get("recovery_token") and not first.get("activity_cursor"), "Missing bounded initial baseline")
            changed = harness.fixture_control("during-recovery")
            baseline = list(first["entries"])
            page = first
            for _ in range(20):
                page = call(recovery_tool, {"limit": 1, "recovery_token": page["recovery_token"]})
                baseline.extend(page["entries"])
                if page.get("complete"):
                    break
            require(page.get("complete") is True and page.get("activity_cursor"), "Recovery never completed")
            require(changed["created_id"] not in {item["schedule_id"] for item in baseline}, "Earlier-sorting fixture did not exercise catch-up")
            original = next(item for item in baseline if item["schedule_id"] == changed["original_id"])
            require(original["status"] == "cancelled" and any(item["schedule_id"] == changed["replacement_id"] and item["status"] == "scheduled" for item in baseline), "Baseline missed rescheduling")
            cursor = page["activity_cursor"]
            events = []
            for _ in range(10):
                page = call(watch_tool, {"after": cursor, "limit": 2, "wait_seconds": 3})
                require(page.get("watch_status") == "events", "Catch-up unexpectedly timed out")
                events.extend(page["entries"])
                cursor = page["cursor"]
                if not page["has_more"]:
                    break
            require([item["sequence"] for item in events] == [3, 4, 5]
                    and [item["kind"] for item in events] == ["accepted", "rescheduled", "accepted"], "Concurrent events were lost or duplicated")
            require(events[0]["schedule_id"] == changed["created_id"]
                    and events[1]["schedule_id"] == changed["original_id"]
                    and events[1]["related_schedule_id"] == changed["replacement_id"]
                    and events[2]["schedule_id"] == changed["replacement_id"]
                    and events[2]["related_schedule_id"] == changed["original_id"], "Reschedule event links were lost")

            harness.fixture_control("arm-arrival")
            page = call(watch_tool, {"after": cursor, "limit": 2, "wait_seconds": 4})
            require(page.get("watch_status") == "events" and [e["sequence"] for e in page["entries"]] == [6], "Watch did not observe new activity")
            cursor = page["cursor"]
            empty = call(watch_tool, {"after": cursor, "wait_seconds": 1})
            require(empty.get("watch_status") == "timed_out" and empty.get("watch_timed_out") is True and empty["entries"] == [] and empty["cursor"] == cursor, "Timeout advanced the checkpoint")

            rpc.close()
            rpc = None
            harness.fixture_control("offline-create")
            rpc, thread_id = connect()
            page = call(watch_tool, {"after": cursor, "wait_seconds": 3})
            require([e["sequence"] for e in page["entries"]] == [7], "Reconnect lost offline activity")
            cursor = page["cursor"]

            harness.fixture_control("expire-checkpoint")
            expired = call(watch_tool, {"after": cursor, "wait_seconds": 3}, "cursor_expired")
            require(expired.get("status") == 410 and expired.get("activity_recovery", {}).get("restart_cursor"), "Missing expiry recovery metadata")
            partial = call(recovery_tool, {"limit": 1})
            require(partial.get("complete") is False and partial.get("recovery_token"), "Missing restart baseline continuation")
            harness.fixture_control("expire-checkpoint")
            call(recovery_tool, {"limit": 1, "recovery_token": partial["recovery_token"]}, "recovery_restart_required")
            # Discard the partial baseline and begin again with no token.
            page = call(recovery_tool, {"limit": 2})
            for _ in range(20):
                if page.get("complete"):
                    break
                page = call(recovery_tool, {"limit": 2, "recovery_token": page["recovery_token"]})
            require(page.get("complete") is True and page.get("activity_cursor"), "Recovery restart failed")
            harness.fixture_control("arm-revoke")
            denied = call(watch_tool, {"after": page["activity_cursor"], "wait_seconds": 4}, "insufficient_scope")
            require(denied.get("status") == 403, "Watch did not stop on revoked access")
            call(recovery_tool, {"limit": 1}, "insufficient_scope")
            evidence = harness.fixture_control("evidence")
            require(evidence["native_writes"] == 0 and evidence["arrival_polls"] >= 2 and evidence["revocation_polls"] >= 2, "Missing bounded wait or read-only evidence")
        finally:
            if rpc is not None:
                rpc.close()

    print(json.dumps({"client": "codex", "fixture": True, "transport": "streamable-http", "scenario": "schedule-activity", "recovery": "passed", "concurrent_changes": "passed", "watch_arrival": "passed", "timeout_checkpoint": "passed", "reconnect": "passed", "cursor_expiry": "passed", "recovery_restart": "passed", "access_revocation": "passed", "model_requests": 0, "mcp_writes": 0}))


if __name__ == "__main__":
    try:
        main()
    except (OSError, RuntimeError, KeyError, ValueError) as exc:
        raise SystemExit(str(exc)) from None
