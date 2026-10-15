#!/usr/bin/env python3
"""Run explicit management MCP scenarios on a dedicated nonproduction KVM host."""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import platform
import sys
import urllib.parse
import tempfile
import time

spec = importlib.util.spec_from_file_location("client_acceptance", Path(__file__).with_name("management-mcp-client-acceptance.py"))
client = importlib.util.module_from_spec(spec)
spec.loader.exec_module(client)

PLANS = {
    "gregale_plan_route_budgets": "gregale_apply_route_budgets",
    "gregale_plan_deploy": "gregale_apply_deploy",
    "gregale_plan_source_deploy": "gregale_apply_deploy",
    "gregale_plan_workload_rollout": "gregale_apply_workload_rollout",
    "gregale_plan_service_abort": "gregale_apply_service_abort",
    "gregale_plan_rollback": "gregale_apply_rollback",
    "gregale_plan_environment_promotion": "gregale_apply_environment_promotion",
}
ENV_PLAN = "gregale_plan_environment_promotion"
ACTIONS = {"human_review", "restart_bridge"}
READS = {"gregale_get_route_budgets","gregale_preflight", "gregale_watch_rollout", "gregale_get_environment_promotion", "gregale_wait_deployment", "gregale_get_rollout", "gregale_get_health", "gregale_get_routes", "gregale_get_operational_summary"}
WRITES = set(PLANS.values()) | {"gregale_promote_deployment"}


def field(value, path):
    for part in path.split("."):
        value = value[part]
    return value


def resolve(value, results):
    if isinstance(value, dict):
        if set(value) == {"ref"}:
            step, path = value["ref"].split(".", 1)
            return field(results[step], path)
        return {key: resolve(child, results) for key, child in value.items()}
    if isinstance(value, list):
        return [resolve(child, results) for child in value]
    return value


def validate_references(value, seen):
    if isinstance(value, dict):
        if set(value) == {"ref"}:
            reference = value["ref"]
            if not isinstance(reference, str) or "." not in reference or reference.split(".", 1)[0] not in seen:
                raise ValueError("reference must point to a previous step")
        else:
            for child in value.values():
                validate_references(child, seen)
    elif isinstance(value, list):
        for child in value:
            validate_references(child, seen)


def validate(config):
    if config.get("nonproduction") is not True or not config.get("account_id"):
        raise ValueError("scenario must declare nonproduction and pin account_id")
    target = config.get("target", {})
    if "environment_target" in config:
        target = config["environment_target"]
        for key in ("project", "project_id", "from_environment", "from_environment_id", "environment", "environment_id", "review_origin"):
            if not isinstance(target.get(key), str) or not target[key]:
                raise ValueError("environment promotion identity is incomplete")
        if not isinstance(target.get("changes"), list) or not target["changes"]:
            raise ValueError("pin the exact reviewed workload changes")
        origin = urllib.parse.urlsplit(target["review_origin"])
        if origin.scheme != "https" or not origin.netloc or origin.path or origin.query or origin.fragment or origin.username:
            raise ValueError("review_origin must be an HTTPS origin")
        if target["from_environment"] == target["environment"]:
            raise ValueError("source and target environments must differ")
    else:
        validate_app_target(target)
    seen = {}
    native_source_plans = set()
    for step in config["steps"]:
        name = step["id"]
        if "action" in step:
            action = step["action"]
            if name in seen or action not in ACTIONS or "tool" in step:
                raise ValueError("invalid operator checkpoint")
            if action == "human_review" and (step.get("result_step") not in seen or seen[step["result_step"]] != "gregale_apply_environment_promotion"):
                raise ValueError("human review requires a prior pending apply")
            seen[name] = action
            continue
        tool = step["tool"]
        if name in seen or tool not in set(PLANS) | READS | WRITES:
            raise ValueError("duplicate step or unsupported tool")
        validate_references(step.get("arguments", {}), seen)
        validate_references(step.get("expect", {}), seen)
        args = step.get("arguments", {})
        if tool in WRITES:
            reference = args.get("plan_id", {})
            if set(args) != {"plan_id"} or not isinstance(reference, dict) or set(reference) != {"ref"}:
                raise ValueError("write must reference a previously reviewed plan")
            origin, path = reference["ref"].split(".", 1)
            expected = "gregale_apply_workload_rollout" if origin in native_source_plans else PLANS.get(seen.get(origin))
            if path != "plan_id" or (tool != expected and not (tool == "gregale_promote_deployment" and expected == "gregale_apply_deploy")):
                raise ValueError("write does not match its reviewed plan")
        elif "environment_target" in config:
            if tool not in {ENV_PLAN, "gregale_get_environment_promotion"} or args.get("project") != target["project"]:
                raise ValueError("environment scenario may only use its pinned project")
            if tool == ENV_PLAN:
                if args.get("from_environment") != target["from_environment"] or args.get("to_environment") != target["environment"]:
                    raise ValueError("promotion environments differ from pinned identities")
            elif args.get("environment") != target["environment"]:
                raise ValueError("promotion read differs from pinned environment")
        else:
            if tool == ENV_PLAN or args.get("app") != target["app"]:
                raise ValueError("every read and plan must use the pinned app")
            if tool == "gregale_plan_rollback" and target["scope"] != "default":
                raise ValueError("default rollback cannot target named environments")
            if args.get("environment", "default") != target["scope"] or args.get("project", "") != target.get("project", ""):
                raise ValueError("step scope differs from pinned target")
        timeout = step.get("timeout_seconds", 60)
        if not isinstance(timeout, int) or not 1 <= timeout <= 1800:
            raise ValueError("step timeout must be 1..1800 seconds")
        if not step.get("expect"):
            raise ValueError("every step needs explicit evidence assertions")
        if tool in WRITES and step.get("poll"):
            raise ValueError("writes may only replay explicitly, never poll")
        if tool == "gregale_plan_source_deploy" and args.get("rollout_mode") in {"service", "worker"}:
            native_source_plans.add(name)
        seen[name] = tool
    if not seen:
        raise ValueError("scenario needs steps")


def validate_app_target(target):
    for key in ("app", "app_id", "scope"):
        if not isinstance(target.get(key), str) or not target[key]:
            raise ValueError("target identity is incomplete")
    if target["scope"] != "default" and not all(target.get(k) for k in ("project", "project_id", "environment_id")):
        raise ValueError("named target requires project and immutable environment identities")


def check_plan(value, config):
    if "environment_target" in config:
        target = config["environment_target"]
        expected = {key: target[key] for key in ("project", "project_id", "from_environment", "from_environment_id", "environment", "environment_id", "changes")}
        expected["account_id"] = config["account_id"]
        expected["approval_required"] = True
        if any(value.get(key) != item for key, item in expected.items()):
            raise RuntimeError("protected promotion differs from pinned identities or workload changes")
        return
    target = config["target"]
    expected = {"account_id": config["account_id"], "app_id": target["app_id"], "app": target["app"], "scope": target["scope"]}
    for key in ("project_id", "environment_id"):
        expected[key] = target.get(key, "")
    if any(value.get(key, "") != item for key, item in expected.items()):
        raise RuntimeError("reviewed plan identity differs from pinned target")


def run(config, call, checkpoint=None):
    validate(config)
    results, evidence = {}, []
    for step in config["steps"]:
        if "action" in step:
            if checkpoint is None:
                raise RuntimeError("operator checkpoint requires an interactive remote session")
            value = results.get(step.get("result_step"))
            checkpoint(step["action"], value)
            results[step["id"]] = {}
            evidence.append({"step": step["id"], "action": step["action"], "status": "operator_acknowledged"})
            continue
        args = resolve(step.get("arguments", {}), results)
        expected = resolve(step["expect"], results)
        deadline = time.monotonic() + step.get("timeout_seconds", 60)
        while True:
            value = call(step["tool"], args)
            if step["tool"] in PLANS:
                check_plan(value, config)
            if all(field(value, path) == item for path, item in expected.items()):
                break
            if not step.get("poll") or time.monotonic() >= deadline:
                raise RuntimeError("scenario assertion failed at " + step["id"])
            time.sleep(min(5, max(0, deadline - time.monotonic())))
        if step.get("replay"):
            replay = call(step["tool"], args)
            for path in step["replay"]:
                if field(value, path) != field(replay, path):
                    raise RuntimeError("same-plan replay changed identity at " + step["id"])
        results[step["id"]] = value
        evidence.append({"step": step["id"], "tool": step["tool"], "assertions": list(expected), "status": "passed"})
    return evidence


def checked_review_url(value, origin):
    if not value or value.get("status") != "approval_pending":
        raise RuntimeError("human checkpoint requires a pending approval")
    url = value.get("review_url", "")
    if not isinstance(url, str) or any(ord(char) < 32 or ord(char) == 127 for char in url):
        raise RuntimeError("unsafe review URL")
    parsed = urllib.parse.urlsplit(url)
    query = urllib.parse.parse_qs(parsed.query)
    if parsed.scheme + "://" + parsed.netloc != origin or parsed.path != "/cli-auth" or parsed.username or parsed.fragment or set(query) != {"approval"} or len(query["approval"]) != 1:
        raise RuntimeError("review URL differs from the pinned console origin")
    return url


def decode_result(result):
    if isinstance(result.get("structuredContent"), dict):
        value = result["structuredContent"]
    else:
        value = json.loads(next(item["text"] for item in result["content"] if item["type"] == "text"))
    if result.get("isError"):
        return {"mcp_error_code": value.get("code", "unknown")}
    return value


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("scenario", type=Path)
    parser.add_argument("--validate-only", action="store_true")
    parser.add_argument("--execute-nonproduction", action="store_true")
    parser.add_argument("--gregale-binary", default="bin/gregale")
    parser.add_argument("--codex-binary", default="codex")
    parser.add_argument("--profile")
    parser.add_argument("--remote-url", help="existing HTTPS bridge with encrypted durable state")
    parser.add_argument("--codex-config-dir", help="existing Codex profile with the same OAuth grant")
    args = parser.parse_args()
    config = json.loads(args.scenario.read_text())
    validate(config)
    if args.validate_only:
        print(json.dumps({"scenario": "valid", "live_qualification": "not_run"}))
        return
    if not args.execute_nonproduction:
        parser.error("execution requires --execute-nonproduction")
    if platform.system() != "Linux" or platform.machine() != "x86_64" or not os.access("/dev/kvm", os.R_OK | os.W_OK):
        raise RuntimeError("run on a dedicated native x86_64 Linux KVM host")
    if "REPLACE_" in json.dumps(config):
        raise ValueError("replace all scenario placeholders before execution")
    if any("action" in step for step in config["steps"]) and not args.remote_url:
        raise ValueError("operator checkpoints require the remote bridge")
    if args.remote_url:
        remote = urllib.parse.urlsplit(args.remote_url)
        if remote.scheme != "https" or remote.username or remote.fragment or remote.query or remote.path != "/mcp":
            raise ValueError("remote bridge must use an HTTPS /mcp URL")
        if not args.codex_config_dir:
            raise ValueError("remote recovery needs the existing OAuth client profile")
    command = ["--non-interactive"] + (["--profile", args.profile] if args.profile else []) + ["mcp", "serve", "--allow-writes"]
    settings = "mcp_servers={gregale={command=" + json.dumps(str(Path(args.gregale_binary).resolve())) + ",args=" + json.dumps(command) + ',env_vars=["FAAS_TOKEN","FAAS_API","XDG_CONFIG_HOME"],tool_timeout_sec=60}}'
    if args.remote_url:
        settings = "mcp_servers={gregale={url=" + json.dumps(args.remote_url) + ",tool_timeout_sec=60}}"
    with tempfile.TemporaryDirectory(prefix="gregale-native-mcp-") as directory:
        rpc = None
        thread = None
        def connect():
            nonlocal rpc, thread
            rpc = client.CodexRPC(args.codex_binary, settings, directory, client_config_dir=args.codex_config_dir)
            rpc.request("initialize", {"clientInfo": {"name": "gregale-native-acceptance", "version": "1"}, "capabilities": {"experimentalApi": True}})
            rpc.send({"method": "initialized"})
            thread = rpc.request("thread/start", {"cwd": directory, "ephemeral": True, "approvalPolicy": "never", "sandbox": "read-only"})["thread"]["id"]
        def checkpoint(action, value):
            if not sys.stdin.isatty():
                raise RuntimeError("operator checkpoints require a terminal")
            if action == "human_review":
                url = checked_review_url(value, config["environment_target"]["review_origin"])
                print("Open this review in your signed-in browser: " + url, file=sys.stderr)
                print("Approve this exact nonproduction promotion, or leave it unapproved for an expiry/rejection scenario.", file=sys.stderr)
            else:
                rpc.close()
                print("Restart the dedicated nonproduction bridge using its existing encrypted state/key files. Keep the OAuth grant and return when healthy.", file=sys.stderr)
            print("Type continue when ready; any other input stops:", file=sys.stderr)
            if input() != "continue":
                raise RuntimeError("operator stopped qualification")
            if action == "restart_bridge":
                connect()
        try:
            connect()
            def call(tool, arguments):
                result = rpc.request("mcpServer/tool/call", {"threadId": thread, "server": "gregale", "tool": tool, "arguments": arguments}, timeout=75)
                return decode_result(result)
            evidence = run(config, call, checkpoint)
            print(json.dumps({"transport": "streamable-http" if args.remote_url else "stdio", "model_requests": 0, "steps": evidence, "live_qualification": "scenario_passed"}))
        finally:
            if rpc is not None and rpc.process.poll() is None:
                rpc.close()


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, KeyError, RuntimeError, StopIteration, EOFError):
        raise SystemExit("Native MCP acceptance failed; inspect the scenario and native history. No automatic cleanup or replan was attempted.") from None
