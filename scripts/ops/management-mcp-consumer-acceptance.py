#!/usr/bin/env python3
"""Model-free real Codex consumer lifecycle acceptance; isolated fixture only."""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import shutil
import sys
import tempfile

sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location(
    "gregale_schedule_acceptance", Path(__file__).with_name("management-mcp-schedule-acceptance.py")
)
schedule = importlib.util.module_from_spec(spec)
spec.loader.exec_module(schedule)
harness, require = schedule.harness, schedule.require


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--codex-binary", required=True)
    args = parser.parse_args()
    binary = str(Path(shutil.which(args.codex_binary) or args.codex_binary).resolve())
    resource = schedule.fixture_url("GREGALE_MCP_TEST_RESOURCE", "/mcp")
    schedule.fixture_url("GREGALE_MCP_TEST_CONTROL", "")
    require(bool(os.environ.get("GREGALE_MCP_ACCESS_TOKEN")), "Missing fixture credential")
    config = 'mcp_servers={gregale={url=' + json.dumps(resource) + ',bearer_token_env_var="GREGALE_MCP_ACCESS_TOKEN",startup_timeout_sec=20,tool_timeout_sec=20}}'
    scope = {"app": "alpha", "project": "shop", "environment": "staging"}
    get_tool = "gregale_get_environment_cache_activity_consumer"
    list_tool = "gregale_list_environment_cache_activity_consumers"
    ack_tool = "gregale_ack_environment_cache_activity_consumer"
    retire_tool = "gregale_retire_environment_cache_activity_consumer"
    recovery_tool = "gregale_recover_environment_cache_schedule_activity"
    consumer_recovery_tool = "gregale_recover_environment_cache_activity_consumer"
    watch_tool = "gregale_watch_environment_cache_schedule_activity"
    consumer_watch_tool = "gregale_watch_environment_cache_activity_consumer"
    health_tool = "gregale_get_environment_cache_activity_consumer_health"
    mutation_calls = 0

    with tempfile.TemporaryDirectory(prefix="gregale-codex-consumers-") as workdir:
        def connect():
            rpc = harness.CodexRPC(binary, config, workdir)
            try:
                rpc.request("initialize", {"clientInfo": {"name": "gregale-consumer-acceptance", "version": "1"}, "capabilities": {"experimentalApi": True}})
                rpc.send({"method": "initialized"})
                thread = rpc.request("thread/start", {"cwd": workdir, "ephemeral": True, "approvalPolicy": "never", "sandbox": "read-only"})
                thread_id = thread["thread"]["id"]
                inventory = rpc.request("mcpServerStatus/list", {"threadId": thread_id})
                server = next((item for item in inventory["data"] if item["name"] == "gregale"), None)
                require(server is not None and not server.get("toolsError"), "Consumer client discovery failed")
                tools = server["tools"]
                names = set(tools) if isinstance(tools, dict) else {tool["name"] for tool in tools}
                require({get_tool, list_tool, ack_tool, retire_tool, recovery_tool, watch_tool, consumer_watch_tool, consumer_recovery_tool, health_tool} <= names, "Missing consumer lifecycle tools")
                require(not any(name.startswith("gregale_apply_") or name == "gregale_cancel_environment_cache_schedule" for name in names), "Fixture exposed native mutation tools")
                return rpc, thread_id
            except BaseException:
                rpc.close()
                raise

        rpc, thread_id = connect()
        try:
            def call(tool, extra, error=None):
                nonlocal mutation_calls
                if tool in (ack_tool, retire_tool):
                    mutation_calls += 1
                result = rpc.request("mcpServer/tool/call", {"threadId": thread_id, "server": "gregale", "tool": tool, "arguments": {**scope, **extra}})
                value = result.get("structuredContent")
                if not isinstance(value, dict):
                    value = json.loads(next(item["text"] for item in result["content"] if item["type"] == "text"))
                require(bool(result.get("isError")) == (error is not None), "Unexpected consumer lifecycle result")
                if error is not None:
                    require(value.get("code") == error, "Unexpected consumer lifecycle error code")
                    require(not any(key in value for key in ("cursor", "entries", "generation", "page", "acknowledgment", "recovery_token", "activity_cursor", "summary", "next_cursor", "observed_at")), "Failed consumer operation returned successful progress")
                return value

            def read_consumer(name):
                uri = "gregale://projects/shop/environments/staging/apps/alpha/activity-consumers/" + name
                result = rpc.request("mcpServer/resource/read", {"threadId": thread_id, "server": "gregale", "uri": uri})
                contents = result.get("contents", [])
                require(len(contents) == 1 and contents[0].get("uri") == uri, "Consumer resource missing")
                state = json.loads(contents[0]["text"])
                require(state["consumer"] == name and state["scope"] == "staging" and "lag" in state, "Consumer resource scope missing")
                require(not any(key in state for key in ("cursor", "activity_cursor", "activity_recovery", "acknowledgment", "hint", "binding")), "Consumer resource returned actionable progress")
                return state

            missing = call(get_tool, {"consumer": "alpha"})
            empty_health = call(health_tool, {"limit": 1})
            require(empty_health["summary"]["total"] == 0 and empty_health["entries"] == [], "Health overview invented a consumer")
            require(read_consumer("alpha")["status"] == "uninitialized", "Consumer resource implicitly initialized progress")
            require(missing["status"] == "uninitialized" and missing["revision"] == 0, "New consumer was initialized implicitly")
            require(not any(key in missing["lag"] for key in ("retained_pending_events", "expired_unacknowledged_events", "seconds_since_ack")), "Uninitialized consumer invented lag")
            needs_baseline = call(consumer_watch_tool, {"consumer": "alpha", "wait_seconds": 1})
            require(needs_baseline["status"] == "uninitialized" and needs_baseline["watch_status"] == "recovery_required" and "cursor" not in needs_baseline and "page" not in needs_baseline and "acknowledgment" not in needs_baseline, "Watch implicitly initialized a consumer")
            baseline = call(consumer_recovery_tool, {"consumer": "alpha", "limit": 50})
            require(baseline["complete"] is True, "Fixture baseline incomplete")
            cursor = baseline["activity_cursor"]
            initial_ack = baseline["acknowledgment"]
            require(initial_ack["expected_revision"] == 0 and initial_ack.get("expected_generation", "") == missing["generation"] and not initial_ack.get("baseline_rebuilt", False), "Initial recovery ACK guards were incorrect")
            first = call(ack_tool, initial_ack)
            require(read_consumer("alpha")["revision"] == first["revision"], "Consumer resource lost acknowledged revision")
            require(first["revision"] == 1 and first["cursor"] == cursor, "Initial acknowledgment failed")
            retry = call(ack_tool, initial_ack)
            require(retry["revision"] == 1 and retry["updated_at"] == first["updated_at"], "Acknowledgment retry changed progress")
            for name in ("charlie", "bravo"):
                call(ack_tool, {**initial_ack, "consumer": name})
            health_page = call(health_tool, {"limit": 1})
            require(health_page["summary"] == {"total": 3, "ready": 3, "cursor_expired": 0, "with_retained_backlog": 0, "needs_attention": 0} and [item["consumer"] for item in health_page["entries"]] == ["alpha"], "Missing full-scope health summary")
            health_tail = call(health_tool, {"limit": 2, "after": health_page["next_cursor"]})
            require(health_tail["summary"] == health_page["summary"] and [item["consumer"] for item in health_tail["entries"]] == ["bravo", "charlie"] and not health_tail.get("next_cursor"), "Consumer health pagination failed")
            require(all(not any(key in item for key in ("cursor", "activity_recovery", "acknowledgment")) for item in health_page["entries"] + health_tail["entries"]), "Consumer health returned processing metadata")
            page = call(list_tool, {"limit": 1})
            require([item["consumer"] for item in page["entries"]] == ["alpha"] and page.get("next_cursor"), "Missing sorted bounded consumer page")
            tail = call(list_tool, {"limit": 2, "after": page["next_cursor"]})
            require([item["consumer"] for item in tail["entries"]] == ["bravo", "charlie"] and not tail.get("next_cursor"), "Consumer continuation failed")
            require(all("cursor" not in item for item in page["entries"] + tail["entries"]), "Listing exposed activity cursors")

            retirement = {"consumer": "alpha", "expected_revision": 1, "expected_generation": first["generation"]}
            retired = call(retire_tool, retirement)
            generation = retired["generation"]
            require(retired["status"] == "retired" and generation != first["generation"], "Retirement did not rotate generation")
            require(call(retire_tool, retirement)["generation"] == generation, "Retirement retry rotated twice")
            call(ack_tool, initial_ack, "consumer_generation_conflict")
            call(list_tool, {"after": page["next_cursor"], "limit": 1}, "consumer_generation_conflict")
            call(health_tool, {"after": health_page["next_cursor"], "limit": 1}, "consumer_generation_conflict")
            restart_health = call(health_tool, {"limit": 1})

            rpc.close()
            rpc = None
            harness.fixture_control("restart")
            rpc, thread_id = connect()
            restarted_health = call(health_tool, {"after": restart_health["next_cursor"], "limit": 2})
            require(restarted_health["summary"]["total"] == 2 and [item["consumer"] for item in restarted_health["entries"]] == ["charlie"], "Health continuation lost across bridge reopen")
            idle = call(consumer_watch_tool, {"consumer": "bravo", "wait_seconds": 1})
            require(idle["watch_timed_out"] is True and idle["page"]["entries"] == [] and idle["cursor"] == cursor and idle["page"]["cursor"] == cursor and idle["revision"] == 1 and idle["generation"] == generation and "acknowledgment" not in idle, "Reconnected consumer did not resume saved progress")
            gone = call(get_tool, {"consumer": "alpha"})
            require(gone["status"] == "uninitialized" and gone["generation"] == generation, "Restart lost retirement fence")
            call(ack_tool, initial_ack, "consumer_generation_conflict")
            recreated = call(ack_tool, {**initial_ack, "expected_generation": generation})
            require(recreated["revision"] == 1 and recreated["generation"] == generation, "Consumer recreation failed")
            call(retire_tool, retirement, "consumer_generation_conflict")
            call(ack_tool, initial_ack, "consumer_generation_conflict")

            harness.fixture_control("append")
            observed = call(consumer_watch_tool, {"consumer": "alpha", "wait_seconds": 2, "limit": 1})
            require([item["sequence"] for item in observed["page"]["entries"]] == [2] and observed["cursor"] == cursor, "Consumer watch did not resume saved progress")
            require(observed["acknowledgment"]["expected_revision"] == 1 and observed["acknowledgment"]["expected_generation"] == generation and observed["acknowledgment"]["cursor"] == observed["page"]["cursor"], "Watch returned mismatched acknowledgment arguments")
            replay = call(consumer_watch_tool, {"consumer": "alpha", "wait_seconds": 1})
            require(replay["page"]["cursor"] == observed["page"]["cursor"] and replay["revision"] == 1, "Watch acknowledged events implicitly")
            unchanged = call(get_tool, {"consumer": "alpha"})
            require(unchanged["cursor"] == cursor and unchanged["revision"] == 1, "Watch auto-acknowledged activity")
            require(unchanged["lag"]["retained_pending_events"] == 1 and unchanged["lag"]["expired_unacknowledged_events"] == 0 and unchanged["lag"]["seconds_since_ack"] >= 0, "Missing retained lag diagnostics")
            lag_health = call(health_tool, {"limit": 1})
            require(lag_health["summary"]["with_retained_backlog"] == 3 and lag_health["summary"]["needs_attention"] == 3 and lag_health["entries"][0]["lag"]["retained_pending_events"] == 1, "Missing consumer health backlog")
            ack = call(ack_tool, observed["acknowledgment"])
            call(health_tool, {"after": lag_health["next_cursor"], "limit": 1}, "checkpoint_conflict")
            require(ack["revision"] == 2, "Subsequent acknowledgment failed")
            call(ack_tool, {**initial_ack, "expected_generation": generation}, "checkpoint_conflict")

            harness.fixture_control("append-on-poll")
            arrival = call(consumer_watch_tool, {"consumer": "alpha", "wait_seconds": 3, "limit": 1})
            require(arrival["watch_status"] == "events" and [item["sequence"] for item in arrival["page"]["entries"]] == [3] and arrival["revision"] == 2, "Consumer watch did not observe arrival while waiting")
            require(call(ack_tool, arrival["acknowledgment"])["revision"] == 3, "Watch acknowledgment arguments were unusable")

            harness.fixture_control("expire")
            expired = call(get_tool, {"consumer": "alpha"})
            expired_resource = read_consumer("alpha")
            require(expired_resource["status"] == "cursor_expired" and expired_resource["revision"] == expired["revision"] and expired_resource["lag"]["expired_unacknowledged_events"] == 1, "Consumer resource lost expiry/gap diagnostics")
            require(expired["status"] == "cursor_expired" and expired["revision"] == 3, "Missing consumer expiry state")
            require(expired["lag"]["retained_pending_events"] == 0 and expired["lag"]["expired_unacknowledged_events"] == 1, "Expired gap conflated with retained backlog")
            expired_health = call(health_tool, {"limit": 1})
            require(expired_health["summary"]["cursor_expired"] == 3 and expired_health["summary"]["with_retained_backlog"] == 0 and expired_health["summary"]["needs_attention"] == 3, "Health overview lost expired consumers")
            needs_recovery = call(consumer_watch_tool, {"consumer": "alpha", "wait_seconds": 1})
            require(needs_recovery["status"] == "cursor_expired" and needs_recovery["watch_status"] == "recovery_required" and needs_recovery["revision"] == 3 and "activity_recovery" in needs_recovery and "page" not in needs_recovery and "acknowledgment" not in needs_recovery, "Expired consumer watch skipped baseline recovery")
            partial = call(consumer_recovery_tool, {"consumer": "bravo", "limit": 1})
            require(partial["complete"] is False and len(partial["entries"]) == 1 and partial["revision"] == 1 and "acknowledgment" not in partial and "activity_cursor" not in partial, "Partial recovery returned progress to acknowledge")
            harness.fixture_control("expire")
            call(consumer_recovery_tool, {"consumer": "bravo", "recovery_token": partial["recovery_token"], "limit": 1}, "recovery_restart_required")
            partial = call(consumer_recovery_tool, {"consumer": "bravo", "limit": 1})
            token = partial["recovery_token"]
            rpc.close()
            rpc = None
            harness.fixture_control("restart")
            rpc, thread_id = connect()
            recovered = partial
            pages = 1
            while not recovered["complete"]:
                recovered = call(consumer_recovery_tool, {"consumer": "bravo", "recovery_token": recovered["recovery_token"], "limit": 2})
                pages += 1
                require(pages <= 5, "Consumer recovery exceeded bounded fixture pages")
            require(recovered["revision"] == 1 and recovered["generation"] == generation and recovered["acknowledgment"]["baseline_rebuilt"] is True and recovered["acknowledgment"]["cursor"] == recovered["activity_cursor"], "Resumed recovery lost ACK guards")
            unchanged_recovery = call(get_tool, {"consumer": "bravo"})
            require(read_consumer("bravo")["status"] == "cursor_expired", "Resource lost saved consumer after bridge reopen")
            require(unchanged_recovery["revision"] == 1 and unchanged_recovery["status"] == "cursor_expired", "Recovery automatically saved progress")
            require(call(ack_tool, recovered["acknowledgment"])["revision"] == 2, "Completed consumer recovery could not be acknowledged")
            call(consumer_recovery_tool, {"consumer": "bravo", "recovery_token": token, "limit": 1}, "checkpoint_conflict")
            retired = call(retire_tool, {"consumer": "alpha", "expected_revision": 3, "expected_generation": generation})
            call(consumer_recovery_tool, {"consumer": "bravo", "recovery_token": token, "limit": 1}, "consumer_generation_conflict")
            fresh = call(consumer_recovery_tool, {"consumer": "alpha", "limit": 50})
            require(fresh["complete"] is True, "Fresh consumer baseline incomplete")
            call(ack_tool, fresh["acknowledgment"])
            recovered_health = call(health_tool, {"limit": 50})
            require(recovered_health["summary"] == {"total": 3, "ready": 2, "cursor_expired": 1, "with_retained_backlog": 0, "needs_attention": 1}, "Health overview did not reflect recovery")

            harness.fixture_control("deny")
            call(get_tool, {"consumer": "alpha"}, "insufficient_scope")
            call(consumer_watch_tool, {"consumer": "alpha", "wait_seconds": 1}, "insufficient_scope")
            call(consumer_recovery_tool, {"consumer": "alpha", "limit": 1}, "insufficient_scope")
            call(list_tool, {"limit": 1}, "insufficient_scope")
            call(health_tool, {"limit": 1}, "insufficient_scope")
            call(retire_tool, {"consumer": "alpha", "expected_revision": 1, "expected_generation": retired["generation"]}, "insufficient_scope")
            evidence = harness.fixture_control("evidence")
            require(evidence["native_writes"] == 0, "Consumer lifecycle issued a native mutation")
        finally:
            if rpc is not None:
                rpc.close()

    print(json.dumps({"client": "codex", "fixture": True, "transport": "streamable-http", "scenario": "activity-consumers", "listing": "passed", "acknowledgment": "passed", "retirement": "passed", "stale_requests": "passed", "restart": "passed", "recreation": "passed", "expiry_retirement": "passed", "lag_diagnostics": "passed", "consumer_watch": "passed", "consumer_recovery": "passed", "consumer_resources": "passed", "consumer_health": "passed", "access_revocation": "passed", "model_requests": 0, "native_writes": 0, "bridge_mutation_calls": mutation_calls}))


if __name__ == "__main__":
    try:
        main()
    except (OSError, RuntimeError, KeyError, ValueError) as exc:
        raise SystemExit(str(exc)) from None
