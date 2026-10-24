#!/usr/bin/env python3
"""Exercise Codex's real MCP client without a model request or deployment write.

Use --fixture for isolated protocol acceptance. Omit it to use a configured
Gregale profile. Remote checks can use Codex's existing OAuth login or a bearer
provided only through GREGALE_MCP_ACCESS_TOKEN.
"""
import argparse
import json
import os
from pathlib import Path
import selectors
import signal
import shutil
import subprocess
import tempfile
import threading
import time
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


class FixtureAPI(BaseHTTPRequestHandler):
    calls = 0
    mutations = 0

    def log_message(self, *_args):
        pass

    def do_GET(self):
        FixtureAPI.calls += 1
        if self.headers.get("Authorization") != "Bearer fp_test_codex_acceptance_fixture":
            self.send_error(401)
            return
        values = {
            "/v1/account": {"id": "codex-acceptance-account", "plan": "free", "status": "active"},
            "/v1/capabilities": {"plan": "free", "capabilities": []},
            "/v1/apps": [],
            "/v1/apps/alpha": {"id": "fixture-alpha", "slug": "alpha", "type": "app", "status": "active", "ram_mb": 128},
            "/v1/deployments/release-base": {"id":"release-base","app_id":"fixture-alpha","kind":"image","status":"live","scope":"default","traffic_percent":100,"rollout_state":"complete"},
            "/v1/apps/alpha/health": {"app_id": "fixture-alpha", "scope": "default", "latest_deployment_id": "release-base", "serving_deployment_ids": ["release-base"]},
            "/v1/apps/alpha/release-review": fixture_release_review(),
            "/v1/apps/alpha/route-budgets?scope=default": {"app_id":"fixture-alpha","scope":"default","budget_source":"app_policy","revision":0,"app_revision":0,"config":{"app_id":"fixture-alpha","enabled":False,"revision":0,"routes":[]}},
            "/v1/apps/alpha/routes?scope=default": {"slug":"alpha","app_id":"fixture-alpha","scope":"default","coverage":"deployment_attributed_history","routes":[],"source":"unavailable"},
            "/v1/apps/alpha/operational-summary?scope=default": {"version":1,"app_id":"fixture-alpha","scope":"default","monitoring":{"available":False,"status":"unknown"},"recovery":{"coverage":"explicit_scope_only","rollbacks":[],"restarts":[]}},
        }
        if self.path not in values:
            self.send_error(404)
            return
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(json.dumps(values[self.path]).encode())

    def do_POST(self):
        if self.path == "/v1/apps/alpha/source-review":
            if self.headers.get("Authorization") != "Bearer fp_test_codex_acceptance_fixture":
                self.send_error(401)
                return
            input = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            value = {"app_id": "fixture-alpha", "repo": input["repo"], "commit_sha": "a" * 40, "installation_id": 55, "repository_id": 987, "source_root": "", "release_review": fixture_release_review()}
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(json.dumps(value).encode())
            return
        FixtureAPI.mutations += 1
        self.send_error(405)

    do_PATCH = do_POST
    do_DELETE = do_POST
    do_PUT = do_POST


def fixture_release_review():
    return {"app_id": "fixture-alpha", "scope": "default", "configuration_hash": "a" * 64, "latest_deployment_id": "release-base", "serving_deployment_ids": ["release-base"]}


def source_planning_call(rpc, thread_id):
    result = rpc.request("mcpServer/tool/call", {"threadId": thread_id, "server": "gregale", "tool": "gregale_plan_source_deploy", "arguments": {"app": "alpha", "repo": "team/repository", "ref": "main", "reason": "read-only source planning acceptance"}})
    if result.get("isError"):
        raise RuntimeError("Codex source planning acceptance failed")


def context_resource_call(rpc, thread_id):
    uri = "gregale://default/apps/alpha/health"
    result = rpc.request("mcpServer/resource/read", {"threadId":thread_id,"server":"gregale","uri":uri})
    contents = result.get("contents", [])
    if len(contents)!=1 or contents[0].get("uri")!=uri or json.loads(contents[0]["text"]).get("scope")!="default":
        raise RuntimeError("Codex scoped context resource acceptance failed")


def preflight_call(rpc, thread_id):
    result = rpc.request("mcpServer/tool/call", {"threadId":thread_id,"server":"gregale","tool":"gregale_preflight","arguments":{"app":"alpha"}})
    if result.get("isError"):
        raise RuntimeError("Codex preflight acceptance failed")
    value = result.get("structuredContent")
    if not isinstance(value, dict):
        value = json.loads(next(item["text"] for item in result["content"] if item["type"]=="text"))
    checks = value.get("checks", {})
    if value.get("scope") != "default" or checks.get("api_write_authorization", {}).get("status") != "unknown" or checks.get("write_consent", {}).get("status") != "blocked":
        raise RuntimeError("Codex preflight confused read access with write admission")


def rollout_watch_call(rpc, thread_id):
    result = rpc.request("mcpServer/tool/call", {"threadId":thread_id,"server":"gregale","tool":"gregale_watch_rollout","arguments":{"app":"alpha","deployment_id":"release-base","wait_seconds":1},"_meta":{"progressToken":"fixture-rollout-watch"}})
    if result.get("isError"):
        value = result.get("structuredContent", {})
        code = value.get("code", "unknown") if isinstance(value, dict) else "unknown"
        if code not in {"invalid_argument", "timeout", "request_failed", "cancelled"}:
            code = "unknown"
        raise RuntimeError("Codex rollout watch acceptance failed (" + code + ")")
    value = result.get("structuredContent")
    if not isinstance(value,dict):
        value = json.loads(next(item["text"] for item in result["content"] if item["type"]=="text"))
    if value.get("completed") is not False or value.get("watch_timed_out") is not True or value.get("healthy") is not False or value.get("deployment",{}).get("id")!="release-base":
        raise RuntimeError("Codex rollout watch confused live artifact with completion")


def scoped_telemetry_calls(rpc, thread_id):
    for tool in ["gregale_get_routes", "gregale_get_operational_summary", "gregale_get_route_budgets"]:
        result = rpc.request("mcpServer/tool/call", {"threadId": thread_id, "server": "gregale", "tool": tool, "arguments": {"app": "alpha"}})
        if result.get("isError"):
            raise RuntimeError("Codex scoped telemetry acceptance failed")


class CodexRPC:
    def __init__(self, binary, config, workdir, diagnostics_file=None, client_config_dir=None):
        # Diagnostics stay private and are never included in the report: they
        # may contain upstream failures or application-provided descriptions.
        self.diagnostics = os.fdopen(os.open(diagnostics_file, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), "wb") if diagnostics_file else tempfile.TemporaryFile()
        # A separate client instance gets a disposable profile by default.
        # The running chat's environment and saved configuration stay intact.
        client_environment = dict(os.environ)
        client_environment["CODEX_HOME"] = client_config_dir or str(Path(workdir) / "client-config")
        Path(client_environment["CODEX_HOME"]).mkdir(parents=True, exist_ok=True, mode=0o700)
        self.process = subprocess.Popen(
            [binary, "app-server", "--stdio", "-c", config, "-c", 'mcp_oauth_credentials_store="file"', "-c", "sqlite_home=" + json.dumps(str(Path(workdir) / "state")), "-c", "log_dir=" + json.dumps(str(Path(workdir) / "logs"))],
            stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=self.diagnostics,
            cwd=workdir, env=client_environment, start_new_session=(os.name == "posix"),
        )
        self.selector = selectors.DefaultSelector()
        self.selector.register(self.process.stdout, selectors.EVENT_READ)
        self.pending = b""
        self.sequence = 0
        self.notifications = []

    def send(self, value):
        self.process.stdin.write(json.dumps(value).encode() + b"\n")
        self.process.stdin.flush()

    def request(self, method, params, timeout=45):
        self.sequence += 1
        request_id = self.sequence
        self.send({"id": request_id, "method": method, "params": params})
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            if b"\n" not in self.pending:
                events = self.selector.select(max(0, deadline - time.monotonic()))
                if not events:
                    break
                chunk = os.read(self.process.stdout.fileno(), 65536)
                if not chunk:
                    raise RuntimeError("Codex exited before completing MCP acceptance")
                self.pending += chunk
                continue
            line, self.pending = self.pending.split(b"\n", 1)
            message = json.loads(line)
            if message.get("id") != request_id:
                self.notifications.append(message)
                continue
            if "error" in message:
                raise RuntimeError(f"Codex rejected {method}; inspect the client locally")
            return message["result"]
        raise RuntimeError(f"Codex timed out during {method}")

    def wait_notification(self, method, timeout=45):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            for index, message in enumerate(self.notifications):
                if message.get("method") == method:
                    return self.notifications.pop(index)["params"]
            if b"\n" in self.pending:
                line, self.pending = self.pending.split(b"\n", 1)
                self.notifications.append(json.loads(line))
                continue
            if not self.selector.select(max(0, deadline - time.monotonic())):
                break
            chunk = os.read(self.process.stdout.fileno(), 65536)
            if not chunk:
                break
            self.pending += chunk
        raise RuntimeError(f"Codex timed out waiting for {method}")

    def close(self):
        self.process.stdin.close()
        try:
            self.process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            self.process.terminate()
            try:
                self.process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                self.process.kill()
                self.process.wait()
        if os.name == "posix":
            # Codex can leave background plugin sync children after stdio closes.
            # Stop only this disposable client's session before removing its home.
            try:
                os.killpg(self.process.pid, signal.SIGTERM)
                deadline = time.monotonic() + 2
                while time.monotonic() < deadline:
                    os.killpg(self.process.pid, 0)
                    time.sleep(0.02)
                os.killpg(self.process.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
        self.selector.close()
        self.diagnostics.close()


def toml_string(value):
    return json.dumps(value)


def fixture_control(action, input=None):
    request = urllib.request.Request(
        os.environ["GREGALE_MCP_TEST_CONTROL"] + "/" + action,
        data=json.dumps(input or {}).encode(),
        headers={"Content-Type": "application/json", "Authorization": "Bearer " + os.environ["GREGALE_MCP_TEST_CONTROL_TOKEN"]},
    )
    with urllib.request.urlopen(request, timeout=30) as response:
        return json.load(response)


def expire_fixture_client_credentials(directory):
    """Advance expiry only in this fixture's disposable OAuth credential file."""
    changed = 0

    def expire(value):
        nonlocal changed
        if isinstance(value, dict):
            for key, child in value.items():
                if key.startswith("expires_at") and isinstance(child, (str, int, float)):
                    value[key] = "1970-01-01T00:00:01Z" if isinstance(child, str) else 1
                    changed += 1
                else:
                    expire(child)
        elif isinstance(value, list):
            for child in value:
                expire(child)

    for path in Path(directory).rglob("*.json"):
        try:
            value = json.loads(path.read_text())
        except (ValueError, UnicodeDecodeError):
            continue
        before = changed
        expire(value)
        if changed != before:
            path.write_text(json.dumps(value))
    if not changed:
        raise RuntimeError("Codex did not persist a fixture OAuth expiry")


def remote_fixture_acceptance(args):
    """Called by the Go test's real TLS bridge, never against a live account."""
    resource = os.environ["GREGALE_MCP_TEST_RESOURCE"]
    request_mode = os.environ.get("GREGALE_MCP_TEST_REQUEST_MODE", "")
    if not resource.startswith("https://127.0.0.1:"):
        raise RuntimeError("Remote acceptance requires the isolated loopback TLS fixture")
    config = "mcp_servers={gregale={url=" + toml_string(resource) + ",startup_timeout_sec=20,tool_timeout_sec=20}}"
    with tempfile.TemporaryDirectory(prefix="gregale-codex-remote-") as workdir:
        phase = 0
        history_plan_id = None
        workload_history_plan_id = None
        environment_history_plan_id = None
        budget_history_plan_id = None
        saved_reviews = {}
        saved_recoveries = {}
        saved_discovery_links = {}
        def start():
            nonlocal phase
            phase += 1
            diagnostics = args.diagnostics_file + "." + str(phase) if args.diagnostics_file else None
            rpc = CodexRPC(args.codex_binary, config, workdir, diagnostics)
            rpc.request("initialize", {"clientInfo": {"name": "gregale-remote-acceptance", "version": "1"}, "capabilities": {"experimentalApi": True}})
            rpc.send({"method": "initialized"})
            return rpc

        def connect(rpc):
            thread = rpc.request("thread/start", {"cwd": workdir, "ephemeral": True, "approvalPolicy": "never", "sandbox": "read-only"})
            thread_id = thread["thread"]["id"]
            status = rpc.request("mcpServerStatus/list", {"threadId": thread_id})
            server = next((item for item in status["data"] if item["name"] == "gregale"), None)
            return thread_id, server

        def saved_review_call(rpc, thread_id, tool, selector):
            result, review = invoke(rpc, thread_id, tool, selector)
            plan = review.get("plan", {})
            if result.get("isError") or plan.get("plan_id") != selector["plan_id"] or review.get("review_kind") != "saved_original_proposal" or review.get("apply_eligibility") != "not_checked" or review.get("native_completion") != "not_observed" or review.get("durable") is not True:
                raise RuntimeError("Codex could not reopen its original saved review")
            forbidden = {"request_id", "idempotency_key", "approval_token", "promotion_token", "promotion_hash", "exchange_code", "review_url", "ticket", "secret_changes", "environment_changes"}
            def check_keys(value):
                if isinstance(value, dict):
                    if forbidden.intersection(value):
                        raise RuntimeError("Codex saved review exposed private execution/configuration material")
                    for item in value.values():
                        check_keys(item)
                elif isinstance(value, list):
                    for item in value:
                        check_keys(item)
            check_keys(plan)
            canonical = json.dumps(plan, sort_keys=True)
            original = saved_reviews.setdefault(selector["plan_id"], canonical)
            if canonical != original:
                raise RuntimeError("Codex saved proposal or expiry changed across restart/refresh")

        def recovery_resource_call(rpc, thread_id, uri, selector, environment=False):
            result = rpc.request("mcpServer/resource/read", {"threadId": thread_id, "server": "gregale", "uri": uri})
            contents = result.get("contents", [])
            if len(contents) != 1 or contents[0].get("uri") != uri or contents[0].get("mimeType") != "application/json":
                raise RuntimeError("Codex could not read its original recovery recipe")
            recipe = json.loads(contents[0]["text"])
            if recipe.get("review_kind") != "saved_plan_recovery_recipe" or recipe.get("freshness") != "not_checked" or recipe.get("native_completion") != "not_observed":
                raise RuntimeError("Codex recovery recipe overstated current eligibility or completion")
            tools = ["gregale_get_action", "gregale_get_plan", "gregale_check_plan"]
            if environment:
                tools = ["gregale_get_environment_action", "gregale_get_environment_plan", "gregale_check_environment_plan"]
            workflow = recipe.get("workflow", {})
            for phase, tool in zip(["history", "review", "check"], tools):
                if workflow.get(phase) != {"tool": tool, "arguments": selector}:
                    raise RuntimeError("Codex recipe changed the original target or supplied an unexpected tool")
            guidance = recipe.get("guidance", "")
            if "Do not apply changes" not in guidance or "preserve the original retry" not in guidance or "untrusted data" not in guidance:
                raise RuntimeError("Codex recovery recipe omitted its read-only/ambiguity boundary")
            encoded = json.dumps(recipe, sort_keys=True)
            if any(marker in encoded for marker in ["PRIVATE_", "fp_test_", '"reason"', '"proposed"', '"request_id"', '"approval_token"', '"promotion_hash"', '"promotion_token"']):
                raise RuntimeError("Codex recovery resource exposed private proposal or replay data")
            original = saved_recoveries.setdefault(uri, encoded)
            if original != encoded:
                raise RuntimeError("Codex recipe changed across restart/refresh")
            review_call = workflow["review"]
            saved_review_call(rpc, thread_id, review_call["tool"], review_call["arguments"])

        def recovery_discovery(action, selector, environment=False):
            links = action.get("recovery", {})
            prompt = "gregale_environment_plan_review" if environment else "gregale_saved_plan_review"
            check = "gregale_check_environment_plan" if environment else "gregale_check_plan"
            uri = links.get("resource_uri")
            if set(links) != {"resource_uri", "prompt", "check"} or not isinstance(uri, str) or not uri.startswith("gregale://"):
                raise RuntimeError("Codex action history omitted its recovery resource link")
            if links.get("prompt") != {"name": prompt, "arguments": selector} or links.get("check") != {"tool": check, "arguments": selector}:
                raise RuntimeError("Codex discovery changed the original selectors or exposed an unexpected call")
            canonical = json.dumps(links, sort_keys=True)
            original = saved_discovery_links.setdefault(selector["plan_id"], canonical)
            if original != canonical:
                raise RuntimeError("Codex discovery links changed across restart/refresh")
            return links

        def outcome_discovery(rpc, thread_id):
            seeded = fixture_control("outcome-history")
            selector = {"app": "budget-history", "plan_id": seeded["plan_id"]}
            before = fixture_control("evidence")["outcome_reads"]
            result, detail = invoke(rpc, thread_id, "gregale_get_action", selector)
            action = detail.get("action", {})
            links = action.get("recovery", {})
            call = {"tool": "gregale_get_action_outcome", "arguments": selector}
            if result.get("isError") or action.get("plan_available") is not False or action.get("apply", {}).get("state") != "outcome_unknown" or action.get("retry_tool") or action.get("review") or set(links) != {"resource_uri", "prompt", "outcome"} or links.get("outcome") != call or links.get("prompt") != {"name": "gregale_saved_plan_review", "arguments": selector}:
                raise RuntimeError("Codex expired history lost outcome discovery or restored replay authority")
            result, page = invoke(rpc, thread_id, "gregale_list_actions", {"app": "budget-history"})
            row = next((item for item in page.get("items", []) if item.get("plan_id") == selector["plan_id"]), {})
            if result.get("isError") or row.get("recovery") != links:
                raise RuntimeError("Codex expired history list/detail discovery differs")
            uri = links["resource_uri"]
            result = rpc.request("mcpServer/resource/read", {"threadId": thread_id, "server": "gregale", "uri": uri})
            contents = result.get("contents", [])
            if len(contents) != 1 or contents[0].get("uri") != uri:
                raise RuntimeError("Codex expired outcome recipe is unavailable")
            recipe = json.loads(contents[0]["text"])
            workflow = recipe.get("workflow", {})
            observation = {"tool": "gregale_get_route_budgets", "arguments": {"app": "budget-history"}}
            if set(workflow) != {"history", "outcome", "observation"} or workflow.get("history") != {"tool": "gregale_get_action", "arguments": selector} or workflow.get("outcome") != call or workflow.get("observation") != observation or recipe.get("freshness") != "not_checked" or recipe.get("native_completion") != "not_observed":
                raise RuntimeError("Codex expired recipe offered live-plan calls or lost exact read selectors")
            encoded = json.dumps(recipe, sort_keys=True)
            if any(marker in encoded for marker in ["PRIVATE_", "fp_test_", '"request_id"', '"request_hash"', '"proposed"']):
                raise RuntimeError("Codex outcome discovery exposed private lookup data")
            if saved_recoveries.setdefault(uri, encoded) != encoded:
                raise RuntimeError("Codex expired outcome recipe changed across restart/refresh")
            if fixture_control("evidence")["outcome_reads"] != before:
                raise RuntimeError("Codex recipe retrieval fetched a native receipt")
            result, outcome = invoke(rpc, thread_id, call["tool"], call["arguments"])
            if result.get("isError") or outcome.get("outcome") != "verified_receipt" or outcome.get("receipt", {}).get("budget_revision") != 1 or outcome.get("native_completion") != "not_observed":
                raise RuntimeError("Codex did not follow its discovered historical receipt call")
            result, current = invoke(rpc, thread_id, observation["tool"], observation["arguments"])
            if result.get("isError") or current.get("revision") != 2 or fixture_control("evidence")["outcome_reads"] != before + 1:
                raise RuntimeError("Codex historical receipt observation flow performed unexpected reads")

        def read(rpc):
            nonlocal history_plan_id, workload_history_plan_id, environment_history_plan_id, budget_history_plan_id
            thread_id, server = connect(rpc)
            discoveries = []
            if not server or server.get("toolsError") or not server["tools"]:
                raise RuntimeError("Codex remote MCP discovery failed")
            inventory = server["tools"]
            names = set(inventory) if isinstance(inventory, dict) else {tool["name"] for tool in inventory}
            if any(name.startswith("gregale_apply_") or name == "gregale_promote_deployment" for name in names):
                raise RuntimeError("Remote read fixture exposed write tools")
            for tool in ["gregale_context", "gregale_list_apps"]:
                result = rpc.request("mcpServer/tool/call", {"threadId": thread_id, "server": "gregale", "tool": tool, "arguments": {}})
                if result.get("isError"):
                    raise RuntimeError("Codex remote fixture read failed")
            if not {"gregale_plan_source_deploy", "gregale_plan_workload_rollout", "gregale_plan_service_abort", "gregale_get_rollout", "gregale_get_request_analytics", "gregale_get_request_analytics_timeseries", "gregale_list_error_groups", "gregale_list_error_requests", "gregale_get_wake_history", "gregale_get_wake_timeline", "gregale_get_profiles"}.issubset(names):
                raise RuntimeError("Codex remote source tool is missing")
            source_planning_call(rpc, thread_id)
            if not {"gregale_list_actions", "gregale_get_action"}.issubset(names):
                raise RuntimeError("Codex action history tools are missing")
            result, page = invoke(rpc, thread_id, "gregale_list_actions", {"app": "alpha", "limit": 20})
            if result.get("isError") or not page.get("items") or page.get("durable") is not True:
                raise RuntimeError("Codex did not discover the recorded source plan")
            if history_plan_id is None:
                history_plan_id = page["items"][0]["plan_id"]
            result, detail = invoke(rpc, thread_id, "gregale_get_action", {"app": "alpha", "plan_id": history_plan_id})
            action = detail.get("action", {})
            if result.get("isError") or action.get("plan_id") != history_plan_id or action.get("apply", {}).get("state") != "not_attempted" or action.get("plan_available") is not True or detail.get("native_completion") != "not_observed":
                raise RuntimeError("Codex action history lost its original plan across restart/refresh")
            discoveries.append((recovery_discovery(action, {"app": "alpha", "plan_id": history_plan_id}, environment=False), {"app": "alpha", "plan_id": history_plan_id}, False))
            for value in (page, detail):
                encoded = json.dumps(value)
                if any(marker in encoded for marker in ["read-only source planning acceptance", '"source"', '"configuration_hash"', "fp_test_", '"reason"', '"requested_ref"']):
                    raise RuntimeError("Codex action history exposed excluded plan data")
            if not {"workload_rollout", "service_abort"}.issubset(page.get("supported_actions", [])):
                raise RuntimeError("Codex workload history coverage is missing")
            if workload_history_plan_id is None:
                fixture_control("history-worker")
                try:
                    result, planned = invoke(rpc, thread_id, "gregale_plan_workload_rollout", {"app": "alpha", "image": "registry.example/team/app@sha256:" + "a" * 64, "reason": "PRIVATE_WORKLOAD_HISTORY"})
                    if result.get("isError") or planned.get("action") != "workload_rollout":
                        raise RuntimeError("Codex workload history planning failed")
                    workload_history_plan_id = planned["plan_id"]
                finally:
                    fixture_control("history-request")
            result, detail = invoke(rpc, thread_id, "gregale_get_action", {"app": "alpha", "plan_id": workload_history_plan_id})
            action = detail.get("action", {})
            if result.get("isError") or action.get("plan_id") != workload_history_plan_id or action.get("workload", {}).get("mode") != "worker" or action.get("apply", {}).get("state") != "not_attempted" or action.get("plan_available") is not True or detail.get("native_completion") != "not_observed":
                raise RuntimeError("Codex workload history changed across restart/refresh")
            discoveries.append((recovery_discovery(action, {"app": "alpha", "plan_id": workload_history_plan_id}, environment=False), {"app": "alpha", "plan_id": workload_history_plan_id}, False))
            if any(marker in json.dumps(detail) for marker in ["PRIVATE_WORKLOAD_HISTORY", '"image"', '"rollout_policy"', '"configuration_hash"']):
                raise RuntimeError("Codex workload history exposed excluded plan data")
            if not {"gregale_list_environment_actions", "gregale_get_environment_action"}.issubset(names):
                raise RuntimeError("Codex environment history tools are missing")
            if environment_history_plan_id is None:
                result, planned = invoke(rpc, thread_id, "gregale_plan_environment_promotion", {"project": "shop", "from_environment": "staging", "to_environment": "production", "reason": "PRIVATE_ENV_REASON"})
                if result.get("isError") or planned.get("action") != "environment_promotion":
                    raise RuntimeError("Codex environment history planning failed")
                environment_history_plan_id = planned["plan_id"]
            selector = {"project": "shop", "environment": "production"}
            result, page = invoke(rpc, thread_id, "gregale_list_environment_actions", selector)
            if result.get("isError") or not any(item.get("plan_id") == environment_history_plan_id for item in page.get("items", [])):
                raise RuntimeError("Codex did not discover its original environment plan")
            result, detail = invoke(rpc, thread_id, "gregale_get_environment_action", dict(selector, plan_id=environment_history_plan_id))
            action = detail.get("action", {})
            if result.get("isError") or action.get("plan_id") != environment_history_plan_id or action.get("from_environment") != "staging" or action.get("apply", {}).get("state") != "not_attempted" or action.get("approval_required") is not True or action.get("plan_available") is not True or detail.get("native_completion") != "not_observed" or detail.get("durable") is not True:
                raise RuntimeError("Codex environment history changed across restart/refresh")
            discoveries.append((recovery_discovery(action, {"project": "shop", "environment": "production", "plan_id": environment_history_plan_id}, environment=True), {"project": "shop", "environment": "production", "plan_id": environment_history_plan_id}, True))
            if any(marker in json.dumps(detail) for marker in ["PRIVATE_ENV_REASON", "PRIVATE_ENV_PREVIEW", '"promotion_hash"', '"promotion_token"', '"approval_token"', '"changes"', '"review_url"', '"exchange_code"']):
                raise RuntimeError("Codex environment history exposed excluded plan data")
            if budget_history_plan_id is None:
                result, planned = invoke(rpc, thread_id, "gregale_plan_route_budgets", {"app": "budget-history", "reason": "PRIVATE_BUDGET_HISTORY", "inherit": True, "enabled": False, "routes": []})
                if result.get("isError") or planned.get("action") != "route_budgets":
                    raise RuntimeError("Codex budget history planning failed: " + str(planned.get("code", "unexpected_action")))
                budget_history_plan_id = planned["plan_id"]
            result, page = invoke(rpc, thread_id, "gregale_list_actions", {"app": "budget-history"})
            if result.get("isError") or "route_budgets" not in page.get("supported_actions", []) or not any(item.get("plan_id") == budget_history_plan_id for item in page.get("items", [])):
                raise RuntimeError("Codex did not discover its original budget plan")
            result, detail = invoke(rpc, thread_id, "gregale_get_action", {"app": "budget-history", "plan_id": budget_history_plan_id})
            action = detail.get("action", {})
            if result.get("isError") or action.get("plan_id") != budget_history_plan_id or action.get("budget", {}).get("change") != "inherit_app_policy" or action.get("apply", {}).get("state") != "not_attempted" or action.get("plan_available") is not True or action.get("observation") != {"tool": "gregale_get_route_budgets", "arguments": {"app": "budget-history"}} or detail.get("native_completion") != "not_observed" or detail.get("durable") is not True:
                raise RuntimeError("Codex budget history changed across restart/refresh")
            discoveries.append((recovery_discovery(action, {"app": "budget-history", "plan_id": budget_history_plan_id}, environment=False), {"app": "budget-history", "plan_id": budget_history_plan_id}, False))
            if any(marker in json.dumps(detail) for marker in ["PRIVATE_BUDGET_HISTORY", '"routes"', '"config"', '"request_id"', '"reason"']):
                raise RuntimeError("Codex budget history exposed excluded plan data")
            result, outcome = invoke(rpc, thread_id, "gregale_get_action_outcome", {"app": "budget-history", "plan_id": budget_history_plan_id})
            if result.get("isError") or outcome.get("plan_id") != budget_history_plan_id or outcome.get("outcome") != "unresolved" or outcome.get("reason") != "no_recorded_attempt" or outcome.get("receipt") is not None or outcome.get("native_completion") != "not_observed":
                raise RuntimeError("Codex unattempted budget outcome fabricated receipt evidence")
            outcome_discovery(rpc, thread_id)
            if not {"gregale_get_plan", "gregale_get_environment_plan"}.issubset(names):
                raise RuntimeError("Codex saved review tools are missing")
            for plan_id in (history_plan_id, workload_history_plan_id):
                saved_review_call(rpc, thread_id, "gregale_get_plan", {"app": "alpha", "plan_id": plan_id})
            saved_review_call(rpc, thread_id, "gregale_get_plan", {"app": "budget-history", "plan_id": budget_history_plan_id})
            saved_review_call(rpc, thread_id, "gregale_get_environment_plan", {"project": "shop", "environment": "production", "plan_id": environment_history_plan_id})
            if not {"gregale_check_plan", "gregale_check_environment_plan"}.issubset(names):
                raise RuntimeError("Codex applicability tools are missing")
            for links, selector, _environment in discoveries:
                check_tool = links["check"]["tool"]
                result, checked = invoke(rpc, thread_id, check_tool, links["check"]["arguments"])
                if result.get("isError") or checked.get("plan_id") != selector["plan_id"] or checked.get("apply_eligibility") != "blocked" or "writes_disabled" not in checked.get("blockers", []) or checked.get("native_completion") != "not_observed":
                    raise RuntimeError("Codex applicability check lost read-only authority or original identity")
                original = json.loads(saved_reviews[selector["plan_id"]])
                if checked.get("expires_at") != original.get("expires_at"):
                    raise RuntimeError("Codex applicability check renewed original expiry")
            fixture_control("plan-drift-on")
            try:
                result, checked = invoke(rpc, thread_id, "gregale_check_plan", {"app": "alpha", "plan_id": history_plan_id})
                if result.get("isError") or checked.get("baseline_check") != "changed" or "plan_stale" not in checked.get("blockers", []) or checked.get("drift") != [{"field": "app_settings", "values_omitted": True}]:
                    raise RuntimeError("Codex did not receive a safe explanation of original-plan drift")
                if checked.get("expires_at") != json.loads(saved_reviews[history_plan_id]).get("expires_at"):
                    raise RuntimeError("Codex drift explanation changed original expiry")
            finally:
                fixture_control("plan-drift-off")
            saved_review_call(rpc, thread_id, "gregale_get_plan", {"app": "alpha", "plan_id": history_plan_id})
            scoped_telemetry_calls(rpc, thread_id)
            rollout_watch_call(rpc, thread_id)
            for links, selector, environment in discoveries:
                recovery_resource_call(rpc, thread_id, links["resource_uri"], selector, environment=environment)
            context_resource_call(rpc, thread_id)
            preflight_call(rpc, thread_id)
            if request_mode:
                request_calls(rpc, thread_id, request_mode == "approved")
            return len(names)

        def invoke(rpc, thread_id, tool, arguments):
            result = rpc.request("mcpServer/tool/call", {"threadId": thread_id, "server": "gregale", "tool": tool, "arguments": arguments})
            value = result.get("structuredContent")
            if not isinstance(value, dict):
                value = json.loads(next(item["text"] for item in result["content"] if item["type"] == "text"))
            return result, value

        def profiles_call(rpc, thread_id, approved, app="alpha", empty=False):
            args = {"app":app, "deployment_id":"11111111-1111-4111-8111-111111111111", "runtime":"go", "start":"2026-10-16T10:00:00Z", "end":"2026-10-16T11:00:00Z", "route":"GET /orders", "limit":2}
            result, value = invoke(rpc, thread_id, "gregale_get_profiles", args)
            if bool(result.get("isError")) == approved:
                raise RuntimeError("Codex profile diagnostic consent failed")
            if not approved:
                if value.get("code") != "request_access_denied":
                    raise RuntimeError("Codex profile denial lost its code")
                return
            for key, expected in {"scope":"app_wide", "deployment_id":args["deployment_id"], "runtime":"go", "start":args["start"], "end":args["end"], "route":args["route"], "window_verification":"native_query_echo", "count_basis":"native_nonzero_sample_entries", "source_completeness":"unknown", "freshness":"unknown", "limit":2}.items():
                if value.get(key) != expected:
                    raise RuntimeError("Codex profile selection or uncertainty lost: " + key)
            coverage, attribution = value.get("coverage", {}), value.get("attribution", {})
            if coverage.get("status") != "observed" or attribution.get("status") != "observed" or coverage.get("scope") != "deployment_runtime_window" or attribution.get("scope") != "deployment_runtime_window":
                raise RuntimeError("Codex profile route read lost whole-window quality scope")
            c, a = coverage.get("measurements", {}), attribution.get("measurements", {})
            if c.get("gap_seconds") != 600 or c.get("recorded_capture_seconds") != 3000 or c.get("received_profiles") != 3 or c.get("last_received_at") != "2026-10-16T11:15:00Z" or c.get("recorded_failed_uploads_lower_bound") != 0 or c.get("failures_complete") is not False or a.get("total_cpu_seconds") != 10 or a.get("attributed_percent") != 60 or a.get("native_diagnostics_complete") is not False:
                raise RuntimeError("Codex profile receipt/capture/attribution semantics changed")
            if any(marker in json.dumps(value) for marker in ["excluded-", "fp_test_", '"file"', '"line"', '"source"', '"flamegraph"', '"routes"', '"collector_id"']):
                raise RuntimeError("Codex profile projection leaked excluded payloads")
            if empty:
                if value.get("status") != "no_selected_samples" or "sampled_cpu_seconds" in value or value.get("functions") != [] or value.get("native_nonzero_sample_entries") != 0:
                    raise RuntimeError("Codex empty profile read invented observed zero CPU")
                return
            rows = value.get("functions", [])
            if value.get("status") != "observed" or value.get("sampled_cpu_seconds") != 3 or value.get("native_nonzero_sample_entries") != 4 or value.get("native_function_count") != 3 or value.get("returned_function_count") != 2 or value.get("functions_truncated") is not True or len(rows) != 2 or rows[0].get("self_cpu_seconds") != 2 or rows[0].get("labels_modified") is not True:
                raise RuntimeError("Codex profile CPU, native order or bounded prefix lost")

        def wake_history_call(rpc, thread_id, approved, app, empty):
            args = {"app":app, "since":"2026-10-16T10:00:00Z", "until":"2026-10-16T11:00:00Z", "limit":2}
            result, value = invoke(rpc, thread_id, "gregale_get_wake_history", args)
            if bool(result.get("isError")) == approved:
                raise RuntimeError("Codex wake history consent failed")
            if not approved:
                if value.get("code") != "request_access_denied":
                    raise RuntimeError("Codex wake history denial lost its code")
                return
            for key, expected in {"scope":"app_wide", "source_status":"not_reported", "source_completeness":"unknown", "freshness":"unknown", "window_verification":"not_echoed_by_native", "requested_since":args["since"], "requested_until":args["until"], "native_candidate_limit":100, "native_candidate_truncation":"not_reported", "count_basis":"latest_instance_candidates_filtered_by_requested_range", "at_capacity_denominator":"native_instances_with_meta", "admission_count_basis":"native_admission_concurrency_ledger"}.items():
                if value.get(key) != expected:
                    raise RuntimeError("Codex wake history lost scope, window or native limits: " + key)
            if any(marker in json.dumps(value) for marker in ["excluded-", "fp_test_", '"request_id"', '"node_id"', '"trigger_histogram"']):
                raise RuntimeError("Codex wake history projection leaked private data")
            if empty:
                if value.get("wakes") != [] or value.get("status") != "no_returned_wakes" or value.get("native_returned_row_count") != 0 or value.get("returned_row_count") != 0 or value.get("rows_truncated") is not False or "at_capacity_pct" in value or value.get("at_capacity_pct_status") != "no_meta_rows":
                    raise RuntimeError("Codex empty wake history fabricated measurements or completeness")
                return
            rows = value.get("wakes")
            if not isinstance(rows, list) or len(rows) != 2 or value.get("native_selected_instance_count") != 3 or value.get("native_returned_row_count") != 3 or value.get("returned_row_count") != 2 or value.get("rows_truncated") is not True or value.get("at_capacity_pct") != 50 or value.get("at_capacity_present_count") != 2 or value.get("at_capacity_pct_status") != "observed_on_meta_rows":
                raise RuntimeError("Codex wake history lost counters, denominator or output limits")
            observed, missing = rows
            if observed.get("ready_in_ms") != 112 or observed.get("queued_count") != 2 or observed.get("concurrency_at_admit") != 2 or observed.get("at_capacity") is not True or observed.get("labels_modified") is not True or observed.get("method") != "restore" or observed.get("tier") != "warm" or observed.get("window_membership") != "compatible_at_native_second_precision":
                raise RuntimeError("Codex wake history lost observed duration, admission or label evidence")
            if missing.get("at_capacity") is not False or missing.get("at_capacity_status") != "observed" or missing.get("queued_count_status") != "zero_or_not_reported" or missing.get("concurrency_status") != "zero_or_not_reported" or missing.get("readiness_status") != "not_reported" or any(key in missing for key in ["queued_count", "concurrency_at_admit", "ready_in_ms"]):
                raise RuntimeError("Codex wake history confused observed false with missing scalar measurements")
            return observed["wake_id"]

        def wake_timeline_call(rpc, thread_id, approved, app, empty, wake_id):
            # Explicit client drill-down; the bridge never fetches stage data
            # automatically from history. Denied/empty cases use the known
            # fixture ID; populated cases use the unchanged history identifier.
            result, value = invoke(rpc, thread_id, "gregale_get_wake_timeline", {"app":app, "wake_id":wake_id or "wake-1", "since":"2026-10-16T10:00:00Z", "limit":8})
            if bool(result.get("isError")) == approved:
                raise RuntimeError("Codex wake stage consent failed")
            if not approved:
                if value.get("code") != "request_access_denied":
                    raise RuntimeError("Codex wake stage denial lost its code")
                return
            expected = {"app":app, "wake_id":"wake-1", "scope":"app_wide", "requested_since":"2026-10-16T10:00:00Z", "lower_bound":"exclusive", "since_precision":"native_postgresql_microseconds", "window_verification":"not_echoed_by_native", "limit":8, "pagination_status":"timestamp_only_not_lossless", "event_time_basis":"native_event_recorded_at", "source_completeness":"unknown", "freshness":"unknown"}
            if any(value.get(k) != v for k,v in expected.items()):
                raise RuntimeError("Codex wake stage query lost identity, selection or timing limits")
            if any(marker in json.dumps(value) for marker in ["excluded-", "fp_test_", '"data"', '"actor"', '"request_id"', '"node_id"', '"queue_wait_ms"', '"gateway_phases_ms"']) or "next_arguments" in value:
                raise RuntimeError("Codex wake stage projection leaked data or invented continuation")
            stages = {s["stage"]:s for s in value.get("stage_observations", [])}
            rows = value.get("events")
            if empty:
                if rows != [] or value.get("status") != "no_returned_events" or value.get("native_returned_event_count") != 0 or value.get("returned_event_count") != 0 or value.get("native_more_events_hint") is not False or any(s["status"] != "not_returned" or s["event_count"] != 0 for s in stages.values()) or len(stages) != 6:
                    raise RuntimeError("Codex empty filtered wake stages invented outcomes")
                return
            if not isinstance(rows, list) or len(rows) != 8 or value.get("native_returned_event_count") != 8 or value.get("returned_event_count") != 8 or value.get("native_more_events_hint") is not True or value.get("native_cursor_at") != "2026-10-16T10:59:00.112Z" or value.get("unknown_event_count") != 1 or stages.get("boot", {}).get("event_count") != 3 or stages.get("parking", {}).get("status") != "not_returned":
                raise RuntimeError("Codex wake stages lost bounded counts, gaps or native continuation uncertainty")
            boot, completed, ready, proxy = (rows[i].get("details", {}) for i in [2,3,4,5])
            if boot.get("queued_count") != 0 or boot.get("concurrency_at_admit") != 0 or boot.get("at_capacity") is not False or boot.get("labels_modified") is not True or boot.get("admission_count_basis") != "native_admission_concurrency_ledger" or completed.get("restore_fallback_reason") != "after_restore_failed" or completed.get("restore_fallback_status") != "observed" or ready.get("readiness_elapsed_ms") != 0 or ready.get("probe_count") != 1 or proxy.get("acceptance_to_first_byte_ms") != 112 or proxy.get("post_wake_proxy_ms") != 10:
                raise RuntimeError("Codex wake stages lost observed zero/false, fallback or timing boundaries")
            if rows[6].get("details", {}).get("native_call_basis") != "restore_call" or rows[6].get("details", {}).get("native_call_total_ms") != 90 or rows[7].get("kind") != "unrecognized_event" or rows[7].get("details", {}).get("status") != "not_exposed_for_kind" or rows[5]["at"] != rows[7]["at"]:
                raise RuntimeError("Codex wake stages lost native call boundaries, tied timestamps or unknown event handling")

        def error_triage_calls(rpc, thread_id, approved, app, empty):
            fingerprint = "b" * 64
            group_args = {"app":app, "since":"2026-10-16T10:00:00Z", "until":"2026-10-16T11:00:00Z", "limit":1}
            result, groups = invoke(rpc, thread_id, "gregale_list_error_groups", group_args)
            if bool(result.get("isError")) == approved:
                raise RuntimeError("Codex error-group consent failed")
            if not approved:
                if groups.get("code") != "request_access_denied":
                    raise RuntimeError("Codex error-group denial lost its code")
            else:
                expected = {"scope":"app_wide", "window_start":group_args["since"], "window_end":group_args["until"], "window_basis":"group_last_seen_at", "window_bounds":"inclusive", "counts_basis":"native_group_history_counters", "source_completeness":"unknown", "freshness":"unknown", "order":"count_desc_last_seen_desc_fingerprint_desc", "limit":1}
                if any(groups.get(k) != v for k,v in expected.items()):
                    raise RuntimeError("Codex error groups lost native window/count semantics")
                if any(marker in json.dumps(groups) for marker in ["excluded-", "fp_test_", "sample_message", '"last_node_id"', '"last_commit_sha"']):
                    raise RuntimeError("Codex error groups leaked excluded data")
                if empty:
                    if groups.get("groups") != [] or groups.get("status") != "no_returned_groups" or groups.get("page_complete") is not True or groups.get("next_arguments"):
                        raise RuntimeError("Codex empty error summary invented health or continuation")
                else:
                    rows = groups.get("groups", [])
                    if len(rows) != 1 or rows[0].get("fingerprint") != fingerprint or rows[0].get("count") != 7 or rows[0].get("request_count") != 10 or rows[0].get("first_seen_at") != "2026-10-01T10:00:00Z" or rows[0].get("last_seen_at") != group_args["until"] or rows[0].get("route") != "/orders" or rows[0].get("route_modified") is not True:
                        raise RuntimeError("Codex error summary lost group-history counters or inclusive selection")
                    continuation = groups.get("next_arguments", {})
                    if groups.get("page_complete") is not False or any(continuation.get(k) != v for k,v in group_args.items()) or continuation.get("cursor") != groups.get("next_cursor") or not continuation.get("cursor", "").startswith("e1."):
                        raise RuntimeError("Codex error summary continuation context changed")
                    result, last = invoke(rpc, thread_id, "gregale_list_error_groups", continuation)
                    if result.get("isError") or last.get("groups") != [] or last.get("page_complete") is not True or last.get("next_arguments") or last.get("window_start") != groups.get("window_start") or last.get("window_end") != groups.get("window_end"):
                        raise RuntimeError("Codex error summary empty terminal page failed")
            request_args = {"app":app, "fingerprint":fingerprint, "limit":1}
            result, requests = invoke(rpc, thread_id, "gregale_list_error_requests", request_args)
            if not approved:
                if not result.get("isError") or requests.get("code") != "request_access_denied":
                    raise RuntimeError("Codex fingerprint requests bypassed consent")
                return
            if empty:
                if not result.get("isError") or requests.get("code") != "not_found":
                    raise RuntimeError("Codex purged/missing fingerprint did not preserve its native error")
                return
            if result.get("isError") or requests.get("scope") != "app_wide" or requests.get("selection_basis") != "retained_fingerprint_history_without_summary_window_filter" or requests.get("lookup_basis") != "public_request_id" or requests.get("evidence_availability") != "not_checked" or "window_start" in requests or "window_end" in requests:
                raise RuntimeError("Codex fingerprint requests invented a summary-window cohort or evidence availability")
            if any(marker in json.dumps(requests) for marker in ["excluded-", "fp_test_", "sample_message", '"deployment_id"', '"node_id"', '"commit_sha"']):
                raise RuntimeError("Codex fingerprint requests leaked excluded fields")
            rows = requests.get("requests", [])
            if len(rows) != 1 or rows[0].get("lookup_identifier") != "019a0000-0000-7000-8000-000000000001" or rows[0].get("received_at") != "2026-10-15T08:00:00Z" or rows[0].get("http_status") != 503 or rows[0].get("route_modified") is not True:
                raise RuntimeError("Codex fingerprint requests lost unchanged public lookup or independent history")
            continuation = requests.get("next_arguments", {})
            if requests.get("page_complete") is not False or any(continuation.get(k) != v for k,v in request_args.items()) or continuation.get("cursor") != requests.get("next_cursor") or not continuation.get("cursor", "").startswith("e1."):
                raise RuntimeError("Codex fingerprint request continuation changed")
            result, last = invoke(rpc, thread_id, "gregale_list_error_requests", continuation)
            if result.get("isError") or last.get("requests") != [] or last.get("header_status") != "not_reported" or last.get("page_complete") is not True or last.get("next_arguments") or "http_status" in last:
                raise RuntimeError("Codex fingerprint empty terminal page invented header evidence")
            result, request = invoke(rpc, thread_id, "gregale_get_request", {"app":app, "request_id":rows[0]["lookup_identifier"]})
            if result.get("isError") or request.get("lookup_identifier") != rows[0]["lookup_identifier"] or request.get("evidence_status") != "request_id_only" or "latency_ms" in request or "status" in request:
                raise RuntimeError("Codex public error request lookup invented missing telemetry")

        def timeseries_calls(rpc, thread_id, approved, app, empty):
            for paired in [False, True]:
                arguments = {"app":app, "since":"3h45m", "until":"2026-10-16T13:15:03Z", "limit":4}
                if paired:
                    arguments.update({"route":"GET /orders", "method":"GET"})
                result, value = invoke(rpc, thread_id, "gregale_get_request_analytics_timeseries", arguments)
                if bool(result.get("isError")) == approved:
                    raise RuntimeError("Codex timeseries consent failed")
                if not approved:
                    if value.get("code") != "request_access_denied":
                        raise RuntimeError("Codex timeseries denial lost its code")
                    continue
                expected = {"app":app, "scope":"app_wide", "series_scope":"route_method" if paired else "app_wide", "since":"3h45m", "from":"2026-10-16T09:30:03Z", "until":arguments["until"], "as_of":"2026-10-16T13:16:03Z", "window_clamped":False, "bucket":"1h", "source_completeness":"unknown", "freshness":"unknown", "error_basis":"http_status_gte_400", "basis":"retained_requests_weighted_by_collapse_count", "native_returned_point_count":5, "returned_point_count":4, "points_truncated":True, "order":"oldest_first"}
                if any(value.get(key) != wanted for key, wanted in expected.items()):
                    raise RuntimeError("Codex timeseries lost native window, pair scope or output bounds")
                if (paired and (value.get("route") != "GET /orders" or value.get("method") != "GET")) or (not paired and ("route" in value or "method" in value)):
                    raise RuntimeError("Codex timeseries changed the exact native selector")
                if any(marker in json.dumps(value) for marker in ["excluded-", "fp_test_", '"series"', '"groups"', '"compute_cost"', '"deployment_costs"', '"dependencies"', '"guest_cpu_avg_ms"']):
                    raise RuntimeError("Codex timeseries leaked excluded or invented data")
                rows = value.get("points", [])
                if len(rows) != 4 or rows[0].get("start") != "2026-10-16T09:00:00Z" or rows[0].get("end") != "2026-10-16T10:00:00Z" or rows[0].get("window_from") != value["from"] or rows[0].get("window_until") != "2026-10-16T10:00:00Z" or rows[0].get("partial") is not True or rows[1].get("partial") is not False or rows[-1].get("start") != "2026-10-16T12:00:00Z":
                    raise RuntimeError("Codex timeseries lost partial-hour boundaries or native order")
                if rows[1].get("zero_filled") is not True or rows[1].get("latency_status") != "no_retained_requests" or "p50_ms" in rows[1]:
                    raise RuntimeError("Codex timeseries invented latency in an empty bucket")
                if empty:
                    if value.get("status") != "no_retained_requests" or any(row.get("zero_filled") is not True or row.get("requests") != 0 or "p95_ms" in row for row in rows):
                        raise RuntimeError("Codex empty timeseries invented measured zero or health")
                elif value.get("status") != "observed" or rows[0].get("p50_ms") != 0 or rows[0].get("requests") != 7 or rows[0].get("error_requests") != 7 or rows[0].get("cold_boots") != 7 or rows[2].get("zero_filled") is not False or rows[2].get("requests") != 3 or rows[2].get("p95_ms") != 0:
                    raise RuntimeError("Codex timeseries confused empty and observed zero buckets")

        def analytics_call(rpc, thread_id, approved, app, empty):
            arguments = {"app":app, "since":"1h", "until":"2026-10-16T11:00:03Z", "limit":2}
            result, value = invoke(rpc, thread_id, "gregale_get_request_analytics", arguments)
            if bool(result.get("isError")) == approved:
                raise RuntimeError("Codex analytics consent failed")
            if not approved:
                if value.get("code") != "request_access_denied":
                    raise RuntimeError("Codex analytics denial lost its code")
                return
            expected = {"app":app, "scope":"app_wide", "since":"1h", "from":"2026-10-16T10:00:03Z", "until":arguments["until"], "as_of":"2026-10-16T11:00:04Z", "window_clamped":False, "source_completeness":"unknown", "freshness":"unknown", "error_basis":"http_status_gte_400", "basis":"retained_requests_weighted_by_collapse_count", "native_routes_limit":50}
            if any(value.get(key) != wanted for key, wanted in expected.items()):
                raise RuntimeError("Codex analytics lost native window or measurement basis")
            if any(marker in json.dumps(value) for marker in ["excluded-", "fp_test_", '"groups"', '"compute_cost"', '"deployment_costs"', '"dependencies"', '"deployment_observations"', '"estimated_compute_cost_millicents"']):
                raise RuntimeError("Codex analytics leaked excluded fields")
            summary = value["summary"]
            if empty:
                if value.get("routes") != [] or value.get("status") != "no_retained_requests" or summary.get("latency_status") != "no_retained_requests" or "p50_ms" in summary or value.get("native_returned_route_count") != 0 or value.get("routes_truncated") is not False:
                    raise RuntimeError("Codex empty analytics invented measured zero")
                return
            if value.get("status") != "observed" or summary.get("p50_ms") != 0 or summary.get("requests") != 10 or summary.get("error_requests") != 7 or summary.get("latency_status") != "observed":
                raise RuntimeError("Codex analytics summary lost observed zero or error counts")
            if len(value.get("routes", [])) != 2 or value.get("native_returned_route_count") != 3 or value.get("returned_route_count") != 2 or any(value.get(key) is not True for key in ["native_routes_truncated", "native_groups_truncated", "routes_truncated"]):
                raise RuntimeError("Codex analytics lost independent native and projection limits")
            measured, missing = value["routes"]
            metrics = measured.get("measurements", {})
            if measured.get("route") != "/orders" or measured.get("route_modified") is not True or measured.get("route_filter_usable") is not False or measured.get("timeseries_filter_usable") is not False or metrics.get("measurement_coverage") != "not_reported" or metrics.get("guest_cpu_avg_ms") != 0 or metrics.get("guest_execution_p50_ms") != 0 or metrics.get("guest_peak_rss_max_mb") != 8 or "wake_boot_p95_ms" in metrics:
                raise RuntimeError("Codex analytics lost safe labels or optional zero measurements")
            if missing.get("route_filter_usable") is not True or missing.get("timeseries_filter_usable") is not True or missing.get("method") != "POST" or missing.get("p95_ms") != 0 or missing.get("measurements") != {"measurement_coverage":"not_reported"}:
                raise RuntimeError("Codex analytics confused measured zero and missing telemetry")

        def comparison_call(rpc, thread_id, approved, app, empty):
            arguments = {"app":app, "source_deployment_id":"11111111-1111-4111-8111-111111111111", "target_deployment_id":"22222222-2222-4222-8222-222222222222", "since":"1h", "until":"2026-10-16T11:00:00Z", "limit":2}
            result, value = invoke(rpc, thread_id, "gregale_compare_deployments", arguments)
            if bool(result.get("isError")) == approved:
                raise RuntimeError("Codex comparison consent failed")
            if not approved:
                if value.get("code") != "request_access_denied":
                    raise RuntimeError("Codex comparison denial lost its code")
                return
            if value.get("source_scope") != "staging" or value.get("target_scope") != "production" or value.get("source_completeness") != "unknown" or value.get("window_end") != arguments["until"]:
                raise RuntimeError("Codex comparison lost scope or native window metadata")
            if empty:
                if value.get("routes") != [] or value.get("status") != "no_retained_route_samples":
                    raise RuntimeError("Codex comparison invented empty evidence")
            elif value["routes"][0]["source"].get("p50_ms") != 0 or value["routes"][1]["target"].get("evidence") != "no_retained_samples" or "p95_ms" in value["routes"][1]["target"] or value["routes"][1].get("comparable") is not False:
                raise RuntimeError("Codex comparison misrepresented observed zero or missing samples")

        def regression_resource_call(rpc, thread_id, approved, app, empty):
            uri = "gregale://apps/" + app + "/regressions"
            try:
                result = rpc.request("mcpServer/resource/read", {"threadId": thread_id, "server": "gregale", "uri": uri})
            except RuntimeError as error:
                if approved or "Codex rejected mcpServer/resource/read" not in str(error):
                    raise
                return
            if not approved:
                raise RuntimeError("Codex read an unconsented regression resource")
            contents = result.get("contents", [])
            if len(contents) != 1 or contents[0].get("uri") != uri:
                raise RuntimeError("Codex regression resource lost its envelope")
            value = json.loads(contents[0]["text"])
            if value.get("scope") != "app_wide" or value.get("since") != "1h" or not value.get("snapshot_observed_at") or "does not prove recovery" not in value.get("update_semantics", ""):
                raise RuntimeError("Codex regression resource lost scope, freshness or absence semantics")
            if any(marker in json.dumps(value) for marker in ["excluded-", "fp_test_", '"query"']):
                raise RuntimeError("Codex regression resource leaked excluded data")
            if empty and (value.get("regressions") != [] or value.get("status") != "no_visible_regressions"):
                raise RuntimeError("Codex regression resource invented empty evidence")
            if not empty and (len(value.get("regressions", [])) != 3 or value.get("regressions_truncated") is not False):
                raise RuntimeError("Codex regression resource changed its projection bound")

        def trace_call(rpc, thread_id, approved, app="alpha", empty=False, partial=False):
            arguments = {"apps": [app], "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736", "limit": 10}
            result, value = invoke(rpc, thread_id, "gregale_get_trace", arguments)
            if bool(result.get("isError")) == approved:
                raise RuntimeError("Codex selected trace consent failed")
            if not approved:
                if value.get("code") != "request_access_denied" or "requests" in value:
                    raise RuntimeError("Codex trace denial lost its code or exposed evidence")
                return
            if value.get("trace_id") != arguments["trace_id"] or value.get("scope") != "selected_apps" or value.get("correlation_basis") != "exact_trace_id_only" or value.get("source_completeness") != "unknown" or value.get("partial") != partial or value.get("status") != ("partial" if partial else "ok"):
                raise RuntimeError("Codex trace selection or source uncertainty changed")
            if len(value.get("apps", [])) != 1 or value["apps"][0].get("slug") != app:
                raise RuntimeError("Codex trace widened the explicit app roster")
            if value.get("requests_status") != ("unavailable" if partial else "ok") or value.get("invocations_status") != "ok":
                raise RuntimeError("Codex trace source statuses changed")
            if empty:
                if value.get("requests") != [] or value.get("invocations") != []:
                    raise RuntimeError("Codex trace invented absent evidence")
            elif len(value.get("requests", [])) != 1 or value["requests"][0].get("lookup_identifier") != "33333333-3333-4333-8333-333333333333" or len(value.get("invocations", [])) != 1 or "started_at" in value["invocations"][0]:
                raise RuntimeError("Codex trace lost request lookups or missing lifecycle times")
            if any(marker in json.dumps(value) for marker in ["excluded-", "fp_test_", '"queue_name"', '"traceparent"', '"headers"', '"spans"']):
                raise RuntimeError("Codex trace leaked excluded data")

        def incident_bundle_call(rpc, thread_id, approved, app, empty):
            arguments = {"app": app, "request_id": "row-1", "since": "1h", "source_deployment_id": "11111111-1111-4111-8111-111111111111", "target_deployment_id": "22222222-2222-4222-8222-222222222222"}
            result, value = invoke(rpc, thread_id, "gregale_get_incident_bundle", arguments)
            if bool(result.get("isError")) == approved:
                raise RuntimeError("Codex incident diagnostic consent failed")
            if not approved:
                if value.get("code") != "request_access_denied" or "evidence" in value:
                    raise RuntimeError("Codex incident denial lost its code or exposed evidence")
                return
            if value.get("schema_version") != "gregale.mcp.incident/v1" or value.get("status") != "ok" or value.get("partial") is not False or value.get("requested_since") != "1h" or value.get("source_completeness") != "unknown":
                raise RuntimeError("Codex incident envelope lost provenance or uncertainty")
            for section, source in {"evidence": "gregale_get_request_evidence", "coverage": "gregale_get_debug_coverage", "regressions": "gregale_list_regressions", "comparison": "gregale_compare_deployments"}.items():
                item = value.get(section, {})
                if item.get("status") != "ok" or item.get("source_tool") != source or not item.get("read_started_at") or not item.get("read_finished_at") or not isinstance(item.get("result"), dict) or "error" in item:
                    raise RuntimeError("Codex incident section lost provenance: " + section)
            evidence = value["evidence"]["result"]
            if evidence.get("request", {}).get("id") != "row-1" or "relative" not in value.get("limitations", ""):
                raise RuntimeError("Codex incident evidence or reference semantics changed")
            comparison = value["comparison"]["result"]
            if comparison.get("source_deployment_id") != arguments["source_deployment_id"] or comparison.get("target_deployment_id") != arguments["target_deployment_id"]:
                raise RuntimeError("Codex incident comparison changed explicit deployments")
            if empty and (value["regressions"]["result"].get("regressions") != [] or comparison.get("routes") != []):
                raise RuntimeError("Codex incident invented absent evidence")
            if any(marker in json.dumps(value) for marker in ["excluded-", "fp_test_", '"query"']):
                raise RuntimeError("Codex incident leaked excluded data")

        def fleet_regression_call(rpc, thread_id, approved, app, empty):
            result, value = invoke(rpc, thread_id, "gregale_list_fleet_regressions", {"apps": [app, "beta"], "since": "1h", "limit_per_app": 1})
            if result.get("isError") or value.get("scope") != "selected_apps" or value.get("partial") is not True or value.get("source_completeness") != "unknown":
                raise RuntimeError("Codex fleet regression result lost partial-read semantics")
            apps = value.get("apps", [])
            if len(apps) != 2 or apps[0].get("app") != app or apps[1].get("app") != "beta" or "result" in apps[1] or apps[1].get("error", {}).get("code") != "request_access_denied":
                raise RuntimeError("Codex fleet regression result lost selection order or diagnostic consent")
            if not approved:
                if value.get("status") != "unavailable" or value.get("successful_app_count") != 0 or "result" in apps[0] or apps[0].get("error", {}).get("code") != "request_access_denied":
                    raise RuntimeError("Codex fleet regression result exposed denied or replaced app evidence")
                return
            evidence = apps[0].get("result", {})
            if value.get("status") != "partial" or value.get("successful_app_count") != 1 or value.get("failed_app_count") != 1 or evidence.get("scope") != "app_wide" or evidence.get("since") != "1h":
                raise RuntimeError("Codex fleet regression result discarded a valid app or lost its lookback")
            if any(marker in json.dumps(value) for marker in ["excluded-", "fp_test_", '"query"']):
                raise RuntimeError("Codex fleet regression result leaked excluded data")
            if empty:
                if evidence.get("regressions") != [] or evidence.get("status") != "no_visible_regressions":
                    raise RuntimeError("Codex fleet regression result invented empty evidence")
            elif len(evidence.get("regressions", [])) != 1 or evidence.get("regressions_truncated") is not True:
                raise RuntimeError("Codex fleet regression result exceeded its projection limit")

        def regression_call(rpc, thread_id, approved, app, empty):
            result, value = invoke(rpc, thread_id, "gregale_list_regressions", {"app":app, "since":"1h", "limit":2})
            regression_resource_call(rpc, thread_id, approved, app, empty)
            fleet_regression_call(rpc, thread_id, approved, app, empty)
            incident_bundle_call(rpc, thread_id, approved, app, empty)
            trace_call(rpc, thread_id, approved, app, empty)
            if bool(result.get("isError")) == approved:
                raise RuntimeError("Codex regression inbox consent failed")
            if not approved:
                if value.get("code") != "request_access_denied":
                    raise RuntimeError("Codex regression inbox denial lost its code")
                return
            if value.get("scope") != "app_wide" or value.get("since") != "1h" or value.get("lookback_basis") != "last_detected_at" or value.get("source_completeness") != "unknown" or "window_start" in value or "window_end" in value:
                raise RuntimeError("Codex regression inbox invented window/completeness metadata")
            if any(marker in json.dumps(value) for marker in ["excluded-", "fp_test_", '"query"']):
                raise RuntimeError("Codex regression inbox leaked excluded data")
            if empty:
                if value.get("regressions") != [] or value.get("status") != "no_visible_regressions":
                    raise RuntimeError("Codex regression inbox invented empty evidence")
                return
            rows = value["regressions"]
            if len(rows) != 2 or value.get("regressions_truncated") is not True or value.get("native_returned_regression_count") != 3:
                raise RuntimeError("Codex regression inbox lost projection limits")
            if rows[0].get("kind") != "errors" or rows[0].get("regression_factor") != "1.00" or rows[0].get("route") != "/orders" or rows[0].get("route_modified") is not True or rows[0].get("affected_count") != 7 or rows[0].get("deployment_scope") != "staging" or rows[0].get("suspected_dependency", {}).get("basis") != "hypothesis":
                raise RuntimeError("Codex regression inbox lost detector metrics or hypothesis limits")
            if rows[1].get("state") != "acknowledged" or rows[1].get("deployment_scope") != "production" or not rows[1].get("acknowledged_at"):
                raise RuntimeError("Codex regression inbox lost triage state or scope")

        def check_dependency_impact(value, empty=False):
            if value.get("source_completeness") != "unknown" or value.get("edges_truncated") is not False or value.get("exemplars_truncated") is not False:
                raise RuntimeError("Codex dependency impact lost source/projection uncertainty")
            edges = value.get("edges")
            if empty:
                if edges != [] or value.get("native_returned_edge_count") != 0 or value.get("returned_edge_count") != 0:
                    raise RuntimeError("Codex empty dependency impact invented relationships")
                return
            if not isinstance(edges, list) or len(edges) != 1 or value.get("native_returned_edge_count") != 1 or value.get("returned_edge_count") != 1:
                raise RuntimeError("Codex dependency impact lost native/returned relationship counts")
            edge = edges[0]
            if edge.get("from") != {"type":"application", "name":"request"} or edge.get("to", {}).get("kind") != "postgres" or edge.get("labels_modified") is not True or edge.get("calls") != 20 or edge.get("calls") <= value.get("represented_requests") or edge.get("error_calls") != 14 or edge.get("error_rate_pct") != 70 or edge.get("baseline_p95_ms") != 20 or edge.get("current_p95_ms") != 50 or edge.get("regression_factor") != 2.5 or edge.get("baseline_exclusive_p95_ms") != 10 or edge.get("current_exclusive_p95_ms") != 40:
                raise RuntimeError("Codex dependency relationships lost child sample metrics, display labels or represented weights")
            if edge.get("side_sample_availability") != "not_reported" or "failure_regression" in edge or "baseline_calls" in edge or edge.get("exemplars_truncated") is not False or edge.get("native_returned_exemplar_count") != 2 or edge.get("returned_exemplar_count") != 2:
                raise RuntimeError("Codex dependency edge invented native signals or lost exemplar bounds")
            examples = edge.get("exemplars", [])
            if len(examples) != 2 or examples[0].get("lookup_identifier") != "row-1" or examples[0].get("count") != 7 or examples[0].get("duration_ms") != 50 or examples[0].get("duration_basis") != "child_span_sample" or examples[0].get("error") is not True or examples[1].get("window") != "baseline" or examples[1].get("count") != 3:
                raise RuntimeError("Codex dependency representative lookup, half-window or child duration basis lost")

        def performance_calls(rpc, thread_id, approved, app="alpha", empty=False):
            wake_id = wake_history_call(rpc, thread_id, approved, app, empty)
            wake_timeline_call(rpc, thread_id, approved, app, empty, wake_id)
            regression_call(rpc, thread_id, approved, app, empty)
            comparison_call(rpc, thread_id, approved, app, empty)
            analytics_call(rpc, thread_id, approved, app, empty)
            timeseries_calls(rpc, thread_id, approved, app, empty)
            error_triage_calls(rpc, thread_id, approved, app, empty)
            windows = []
            for tool in ["gregale_get_debug_coverage", "gregale_get_dependency_latency", "gregale_get_critical_paths"]:
                arguments = {"app":app, "since":"1h"}
                if tool == "gregale_get_critical_paths":
                    arguments["limit"] = 1
                result, value = invoke(rpc, thread_id, tool, arguments)
                if bool(result.get("isError")) == approved:
                    raise RuntimeError("Codex performance consent failed")
                if not approved:
                    if value.get("code") != "request_access_denied":
                        raise RuntimeError("Codex performance denial lost its code")
                    continue
                if value.get("scope") != "app_wide" or value.get("since") != "1h" or any(marker in json.dumps(value) for marker in ["excluded-", "fp_test_", '"queries"', '"trace_id"', '"deployment_comparison"']):
                    raise RuntimeError("Codex performance scope/redaction failed")
                windows.append(value.get("window_start"))
                if tool == "gregale_get_debug_coverage":
                    if value.get("collection_completeness") != "unknown" or value.get("coverage_basis") != "retained_telemetry_only":
                        raise RuntimeError("Codex coverage invented a traffic denominator")
                    if empty and (value.get("status") != "no_retained_telemetry" or value.get("represented_requests") != 0):
                        raise RuntimeError("Codex empty coverage was presented as evidence")
                elif tool == "gregale_get_critical_paths":
                    if empty:
                        if value.get("status") != "no_returned_paths" or value.get("critical_paths") != [] or value.get("path_samples") != 0:
                            raise RuntimeError("Codex empty critical-path evidence was lost")
                    else:
                        path = value["critical_paths"][0]
                        if value.get("complete") is not False or value.get("source_truncated") is not True or value.get("path_samples") != 2 or value.get("represented_requests") != 10 or path.get("dominant_segment_exclusive_ms") != 300:
                            raise RuntimeError("Codex critical-path sampling/regression limits lost")
                        if path["segments"][0].get("name") != "handler" or path["exemplars"][0].get("lookup_identifier") != "row-1" or path["exemplars"][0].get("count") != 7 or path["exemplars"][1].get("window") != "baseline" or '"trace_id"' in json.dumps(path):
                            raise RuntimeError("Codex critical-path sequence, representative lookup or trace exclusion failed")
                        lookup = path["exemplars"][0]["lookup_identifier"]
                        detail_result, detail = invoke(rpc, thread_id, "gregale_get_request_evidence", {"app":app, "request_id":lookup})
                        if detail_result.get("isError") or detail.get("request", {}).get("id") != lookup or detail["request"].get("count") != 7:
                            raise RuntimeError("Codex critical-path representative drill-down failed")
                elif tool == "gregale_get_dependency_latency":
                    check_dependency_impact(value, empty)
                    if empty:
                        if value.get("status") != "no_retained_spans" or value.get("dependencies") != []:
                            raise RuntimeError("Codex empty dependency evidence was lost")
                    else:
                        if value.get("complete") is not False or value.get("source_truncated") is not True or not value.get("dependencies"):
                            raise RuntimeError("Codex sampled dependency limits were lost")
                        lookup = value["edges"][0]["exemplars"][0]["lookup_identifier"]
                        detail_result, detail = invoke(rpc, thread_id, "gregale_get_request_evidence", {"app":app, "request_id":lookup})
                        if detail_result.get("isError") or detail.get("request", {}).get("id") != lookup or detail["request"].get("count") != 7 or detail["request"].get("scope") != "staging":
                            raise RuntimeError("Codex dependency representative drill-down failed")
            if approved and len(set(windows)) != 3:
                raise RuntimeError("Codex performance observations lost their independent windows")

        def residency_calls(rpc, thread_id, approved, app="alpha", empty=False):
            result, value = invoke(rpc, thread_id, "gregale_get_running_causes", {"app":app, "since":"1h", "limit":1})
            if bool(result.get("isError")) == approved:
                raise RuntimeError("Codex residency consent failed")
            if not approved:
                if value.get("code") != "request_access_denied":
                    raise RuntimeError("Codex residency denial lost its code")
                return
            if value.get("scope") != "app_wide" or value.get("freshness") != "unknown" or value.get("since") != "1h":
                raise RuntimeError("Codex residency scope/freshness lost")
            if any(marker in json.dumps(value) for marker in ["excluded-", "fp_test_", "192.0.2.123", '"flow_topology"', '"instance_id"', '"trace_id"']):
                raise RuntimeError("Codex residency projection leaked excluded data")
            if empty:
                if value.get("status") != "no_retained_observations" or value.get("history") != [] or value.get("current") != [] or "current_observed_at" in value:
                    raise RuntimeError("Codex empty residency invented current evidence")
            elif value.get("current_observation_age_seconds") != 1800 or value.get("current_degraded") is not True or value.get("history_truncated") is not True or value["current"][0]["request"].get("basis") != "nearest_retained_aggregate_not_exact_causation":
                raise RuntimeError("Codex residency lost observation age, degradation or approximate attribution")

        def check_request_path(value, empty=False):
            path = value.get("critical_path", {})
            if path.get("basis") != "native_retained_span_parent_chain" or path.get("source_completeness") != "unknown":
                raise RuntimeError("Codex request path lost provenance")
            if empty:
                if path.get("status") != "unavailable" or path.get("segments") != [] or path.get("complete") is not False or "duration_ms" in path:
                    raise RuntimeError("Codex empty request path invented timing or segments")
                return
            segments = path.get("segments", [])
            if path.get("status") != "observed" or path.get("complete") is not True or path.get("duration_ms") != 90 or len(segments) != 3 or path.get("slowest_segment_ref") != "/critical_path/segments/2" or path.get("slowest_exclusive_ms") != 50:
                raise RuntimeError("Codex request path lost timing or completeness")
            if segments[1].get("parent_ref") != segments[0].get("ref") or segments[2].get("parent_ref") != segments[1].get("ref") or segments[2].get("dependency_kind") != "http" or segments[2].get("status") != "error":
                raise RuntimeError("Codex request path lost local topology or dependency status")
            if any(marker in json.dumps(path) for marker in ["excluded-", "fp_test_", '"span_id"', '"trace_id"', '"dependency_name"', "2222222222222222", "1111111111111111", "0000000000000001"]):
                raise RuntimeError("Codex request path leaked private span data")

        def check_guest_execution(request, missing=False):
            guest = request.get("guest", {})
            if guest.get("duration_precision") != "native_retained_may_be_bucketed":
                raise RuntimeError("Codex guest duration precision lost")
            if missing:
                if guest.get("status") != "unavailable" or "duration_ms" in guest:
                    raise RuntimeError("Codex missing guest evidence invented timing")
                return
            if guest.get("status") != "observed" or guest.get("runtime") != "node22" or guest.get("outcome") != "timeout" or guest.get("error_class") != "timeout" or guest.get("duration_ms") != 70 or guest.get("represents_multiple_requests") is not True:
                raise RuntimeError("Codex guest runtime/outcome/aggregation evidence lost")

        def check_request_regression(value, status="observed"):
            regression = value.get("regression", {})
            if regression.get("status") != status or regression.get("basis") != "native_route_deployment_observation" or regression.get("source_completeness") != "unknown":
                raise RuntimeError("Codex request regression lost availability or provenance")
            if status != "observed":
                if "observation" in regression:
                    raise RuntimeError("Codex missing request regression invented an observation")
                return
            row = regression.get("observation", {})
            if row.get("deployment_id") != value["request"].get("deployment_id") or row.get("route") != "/orders" or row.get("deployment_scope") != "staging" or row.get("route_modified") is not True or row.get("kind") != "latency_and_errors" or row.get("state") != "active" or row.get("regression_factor") != "2.50" or row.get("p95_ms") != 250 or row.get("p95_base_ms") != 100 or row.get("error_rate_pct") != 20 or row.get("error_rate_base_pct") != 5 or row.get("suspected_dependency", {}).get("basis") != "hypothesis":
                raise RuntimeError("Codex matched regression lost scope, metrics or hypothesis limits")
            if any(marker in json.dumps(regression) for marker in ["excluded-", "private-query", "fp_test_", '"query"', '"baseline_deployment_id"', '"window_start"']):
                raise RuntimeError("Codex matched regression exposed excluded data or invented metadata")

        def check_request_dependency_comparison(value, status="observed"):
            comparison = value.get("dependency_comparison", {})
            if comparison.get("status") != status or comparison.get("basis") != "native_route_deployment_dependency_comparison" or comparison.get("source_completeness") != "unknown":
                raise RuntimeError("Codex request dependency comparison lost availability/provenance")
            if status == "unavailable":
                if comparison.get("dependencies") != [] or "previous_deployment_id" in comparison or "current_deployment_id" in comparison:
                    raise RuntimeError("Codex missing dependency comparison invented deployment/metric evidence")
                return
            if comparison.get("current_deployment_id") != value["request"].get("deployment_id") or comparison.get("previous_deployment_id") != "22222222-2222-4222-8222-222222222222" or comparison.get("current_deployment_scope") != "staging" or comparison.get("previous_deployment_scope") != "production" or comparison.get("route") != "/orders" or comparison.get("route_modified") is not True:
                raise RuntimeError("Codex native dependency pair lost association/verified scopes")
            if comparison.get("native_returned_dependency_count") != 1 or comparison.get("returned_dependency_count") != 1 or comparison.get("native_truncated") != (status == "partial") or comparison.get("dependencies_truncated") is not False:
                raise RuntimeError("Codex dependency comparison lost native/projection bounds")
            rows = comparison.get("dependencies", [])
            if len(rows) != 1 or rows[0].get("calls") != 40 or rows[0].get("error_calls") != 6 or rows[0].get("baseline_p95_ms") != 40 or rows[0].get("current_p95_ms") != 100 or rows[0].get("regression_factor") != 2.5 or rows[0].get("baseline_error_rate_pct") != 0 or rows[0].get("current_error_rate_pct") != 30 or rows[0].get("regression") is not True or rows[0].get("failure_regression") is not True:
                raise RuntimeError("Codex dependency comparison lost latency/error metrics")
            if any(marker in json.dumps(comparison) for marker in ["excluded-", "fp_test_", "private-query", '"queries"', '"pattern"', '"previous_commit_sha"', '"window_start"']):
                raise RuntimeError("Codex dependency comparison leaked excluded data or invented a window")

        def check_request_explanation(value, empty=False):
            check_request_path(value, empty)
            check_request_regression(value, "unknown" if empty else "observed")
            check_request_dependency_comparison(value, "unavailable" if empty else "observed")
            check_guest_execution(value.get("request", {}), missing=empty)
            explanation = value.get("explanation", {})
            if explanation.get("basis") != "native_finding_codes_and_exposed_evidence" or explanation.get("confidence_basis") != "native_rule_label" or explanation.get("source_completeness") != "unknown":
                raise RuntimeError("Codex request explanation lost provenance")
            if empty:
                if explanation.get("status") != "unavailable" or explanation.get("findings") != [] or explanation.get("recommendations") != [] or value.get("request", {}).get("status") != 503:
                    raise RuntimeError("Codex unavailable explanation invented findings or lost request evidence")
                return
            findings = explanation.get("findings", [])
            if explanation.get("status") != "partial" or len(findings) != 8 or explanation.get("omitted_finding_count") != 0 or len(explanation.get("recommendations", [])) != 4:
                raise RuntimeError("Codex request explanation lost unsupported findings/actions")
            if findings[0].get("code") != "request_failure" or findings[0].get("confidence") != "high" or findings[0].get("approximate") is not True or findings[1].get("code") != "evidence_gap":
                raise RuntimeError("Codex request explanation lost native confidence, aggregates or gaps")
            if findings[2].get("code") != "slowest_span" or findings[2].get("evidence_refs") != ["/critical_path/segments/0"] or findings[3].get("code") != "dependency_failure" or findings[3].get("evidence_refs") != ["/critical_path/segments/2"]:
                raise RuntimeError("Codex request explanation lost verified path findings")
            if findings[4].get("code") != "guest_failure" or findings[4].get("evidence_refs") != ["/request/guest"] or explanation["recommendations"][0].get("evidence_refs") != ["/request/guest"]:
                raise RuntimeError("Codex guest finding or investigation reference lost")
            if findings[5].get("code") != "error_regression" or findings[5].get("evidence_refs") != ["/regression/observation"] or findings[5].get("approximate") is not True:
                raise RuntimeError("Codex regression finding citations or aggregation semantics lost")
            if explanation["recommendations"][2].get("action") != "compare_deployments" or explanation["recommendations"][2].get("evidence_refs") != ["/regression/observation"]:
                raise RuntimeError("Codex regression investigation suggestion lost support")
            if findings[6].get("code") != "dependency_regression" or findings[7].get("code") != "dependency_failure_regression" or any(f.get("evidence_refs") != ["/dependency_comparison/dependencies/0"] or f.get("approximate") is not True for f in findings[6:]):
                raise RuntimeError("Codex dependency comparison findings lost local references/aggregate semantics")
            if explanation["recommendations"][3].get("action") != "inspect_dependency_change" or explanation["recommendations"][3].get("evidence_refs") != ["/dependency_comparison/dependencies/0"]:
                raise RuntimeError("Codex dependency change suggestion lost supported comparison citation")
            for item in findings + explanation["recommendations"]:
                for pointer in item.get("evidence_refs", []):
                    if pointer not in ["/request", "/request/guest", "/regression/observation", "/dependency_comparison/dependencies/0", "/correlation/stages/0", "/critical_path/segments/0", "/critical_path/segments/2"]:
                        raise RuntimeError("Codex request explanation exposed unsupported reference")
                    node = value
                    for part in pointer[1:].split("/"):
                        node = node[int(part)] if isinstance(node, list) else node[part]
                    if node is None:
                        raise RuntimeError("Codex request explanation has a dangling reference")

        def check_request_wake(request, status="observed"):
            wake = request.get("wake", {})
            if wake.get("status") != status or wake.get("basis") != "native_retained_request_wake_id" or wake.get("association_scope") != "retained_row" or wake.get("timeline_availability") != "not_checked":
                raise RuntimeError("Codex request wake lost association provenance or availability")
            if status != "observed":
                if any(key in wake for key in ["wake_id", "timeline_tool", "timeline_arguments"]):
                    raise RuntimeError("Codex missing/invalid request wake fabricated a usable selector")
                return
            if wake.get("wake_id") != "wake-1" or wake.get("represents_multiple_requests") is not True or wake.get("timeline_tool") != "gregale_get_wake_timeline" or wake.get("timeline_arguments") != {"app":"alpha", "wake_id":"wake-1", "limit":50}:
                raise RuntimeError("Codex request wake changed the recorded ID, inferred time filters or lost aggregation")

        saved_wake_continuation = None

        def wake_pagination_calls(rpc, thread_id, approved, app):
            nonlocal saved_wake_continuation
            args = {"app":app, "wake_id":"wake-paged-1", "since":"2026-10-16T10:00:00Z", "limit":2}
            kinds = []
            for index in range(3):
                result, value = invoke(rpc, thread_id, "gregale_get_wake_timeline", args)
                if bool(result.get("isError")) == approved:
                    raise RuntimeError("Codex keyset wake consent failed")
                if not approved:
                    if value.get("code") != "request_access_denied":
                        raise RuntimeError("Codex keyset denial lost its code")
                    return
                rows = value.get("events", [])
                if value.get("pagination_status") != "keyset_at_id_v1" or value.get("window_verification") != "native_since_echo" or value.get("source_completeness") != "unknown" or len(rows) != 2 or value.get("returned_event_count") != 2 or any(row.get("at") != "2026-10-16T10:59:00.112Z" for row in rows):
                    raise RuntimeError("Codex keyset read lost native mode, tied timestamps or page bounds")
                if any(marker in json.dumps(value) for marker in ["excluded-", "fp_test_", '"actor"', '"data"', '"event_id"']):
                    raise RuntimeError("Codex keyset continuation exposed excluded event data")
                kinds.extend(row["kind"] for row in rows)
                if index == 2:
                    if value.get("page_complete") is not True or value.get("native_more_events_hint") is not False or "next_cursor" in value or "next_arguments" in value:
                        raise RuntimeError("Codex terminal keyset page invented continuation")
                    break
                next_args = value.get("next_arguments", {})
                if value.get("page_complete") is not False or value.get("native_more_events_hint") is not True or any(next_args.get(key) != args[key] for key in ["app", "wake_id", "since", "limit"]) or next_args.get("cursor") != value.get("next_cursor") or not next_args.get("cursor", "").startswith("wp1."):
                    raise RuntimeError("Codex keyset next_arguments lost exact original selection")
                if index == 0:
                    if saved_wake_continuation is None:
                        saved_wake_continuation = next_args
                    # Reuse the original client cursor after bridge restart and
                    # OAuth refresh; no bridge-local page state is required.
                    args = saved_wake_continuation
                else:
                    args = next_args
            if kinds != ["wake.queue_accepted", "wake.admitted", "wake.boot_started", "wake.readiness_200", "wake.proxy_first_byte", "unrecognized_event"]:
                raise RuntimeError("Codex keyset paging dropped/duplicated equal-time events")

        saved_history_continuation = None

        def history_pagination_calls(rpc, thread_id, approved, app):
            nonlocal saved_history_continuation
            args = {"app": app, "since": "2026-10-15T10:00:00Z", "until": "2026-10-15T11:00:00Z", "limit": 2}
            seen = []
            for index in range(3):
                result, value = invoke(rpc, thread_id, "gregale_get_wake_history", args)
                if bool(result.get("isError")) == approved:
                    raise RuntimeError("Codex paged history consent failed")
                if not approved:
                    if value.get("code") != "request_access_denied":
                        raise RuntimeError("Codex paged history denial lost its code")
                    return
                rows = value.get("wakes", [])
                if value.get("pagination_status") != "keyset_started_at_instance_id_v1" or value.get("window_verification") != "native_window_echo" or value.get("source_status") != "read_succeeded" or value.get("count_basis") != "returned_page_instance_records" or value.get("source_completeness") != "unknown" or len(rows) != 2 or any(row.get("at") != "2026-10-15T10:30:00.123456Z" for row in rows):
                    raise RuntimeError("Codex history page lost mode, source, window or tied timestamps")
                if any(marker in json.dumps(value) for marker in ['"instance_id"', '"node_id"', '"data"', '"boot_data"']):
                    raise RuntimeError("Codex paged history exposed excluded data")
                seen.extend(row["wake_id"] for row in rows)
                if index == 2:
                    if value.get("page_complete") is not True or "next_arguments" in value or "next_cursor" in value:
                        raise RuntimeError("Codex terminal history page invented continuation")
                    break
                next_args = value.get("next_arguments", {})
                if value.get("page_complete") is not False or any(next_args.get(key) != args[key] for key in ["app", "since", "until", "limit"]) or next_args.get("cursor") != value.get("next_cursor") or not next_args.get("cursor", "").startswith("hp1."):
                    raise RuntimeError("Codex history continuation lost original selection")
                if index == 0:
                    if saved_history_continuation is None:
                        saved_history_continuation = next_args
                    args = saved_history_continuation
                else:
                    args = next_args
            if seen != ["history-wake-" + str(i) for i in range(1, 7)]:
                raise RuntimeError("Codex history paging lost or duplicated records")

        def request_calls(rpc, thread_id, approved, app="alpha"):
            profiles_call(rpc, thread_id, approved, app)
            performance_calls(rpc, thread_id, approved, app)
            residency_calls(rpc, thread_id, approved, app)
            wake_pagination_calls(rpc, thread_id, approved, app)
            history_pagination_calls(rpc, thread_id, approved, app)
            before_wake_drill = fixture_control("evidence")["wake_timeline_reads"]

            query = {"app":app,"since":"1h","route":"/orders","deployment_id":"11111111-1111-4111-8111-111111111111","status":503,"min_latency_ms":50,"cold_boot":False,"limit":1}
            result, page = invoke(rpc, thread_id, "gregale_list_requests", query)
            if bool(result.get("isError")) == approved:
                raise RuntimeError("Codex request discovery consent failed")
            lookup = "public-request"
            if not approved:
                if page.get("code") != "request_access_denied":
                    raise RuntimeError("Codex request discovery denial lost its code")
            else:
                if page.get("complete") is not False or len(page.get("requests", [])) != 1 or page.get("since") != "1h":
                    raise RuntimeError("Codex request discovery lost pagination or window metadata")
                continuation = page.get("next_arguments", {})
                if any(continuation.get(k) != v for k, v in query.items()) or continuation.get("cursor") != page.get("next_cursor"):
                    raise RuntimeError("Codex request discovery changed continuation filters")
                row = page["requests"][0]
                lookup = row.get("lookup_identifier")
                if not lookup or row.get("scope") != "staging" or row.get("count") != 7 or any(marker in json.dumps(page) for marker in ["excluded-", "fp_test_", "private-query"]):
                    raise RuntimeError("Codex request discovery attribution/redaction failed")
                check_guest_execution(row)
                check_request_wake(row)
                result, last = invoke(rpc, thread_id, "gregale_list_requests", continuation)
                if result.get("isError") or last.get("complete") is not True or last.get("next_cursor") or last.get("next_arguments") or last.get("window_start") != page.get("window_start") or last.get("window_end") != page.get("window_end"):
                    raise RuntimeError("Codex request discovery continuation failed")
                if last["requests"][0]["lookup_identifier"] == lookup:
                    raise RuntimeError("Codex request discovery repeated the first page")
                check_request_wake(last["requests"][0], "not_reported")
            for tool in ["gregale_get_request", "gregale_get_request_evidence"]:
                result, value = invoke(rpc, thread_id, tool, {"app": app, "request_id": lookup})
                if bool(result.get("isError")) == approved:
                    raise RuntimeError("Codex request diagnostic consent failed")
                if not approved:
                    if value.get("code") != "request_access_denied":
                        raise RuntimeError("Codex request diagnostic denial lost its code")
                    continue
                encoded = json.dumps(value)
                if any(marker in encoded for marker in ["excluded-", "fp_test_", "private-query", '"spans"', '"actor"']):
                    raise RuntimeError("Codex request diagnostic projection leaked excluded data")
                request = value.get("request", value)
                if request.get("status") != 503 or request.get("deployment_id") != query["deployment_id"] or request.get("id") != lookup:
                    raise RuntimeError("Codex request diagnostic identity/status missing")
                check_guest_execution(request)
                check_request_wake(request)
                if tool == "gregale_get_request_evidence" and (value.get("correlation", {}).get("complete") is not False or not value.get("dependency_latency")):
                    raise RuntimeError("Codex request diagnostic evidence missing")
                if tool == "gregale_get_request_evidence":
                    check_request_explanation(value)
            if fixture_control("evidence")["wake_timeline_reads"] != before_wake_drill:
                raise RuntimeError("Request projection automatically fetched wake stage evidence")
            if approved:
                wake = request["wake"]
                result, stages = invoke(rpc, thread_id, wake["timeline_tool"], wake["timeline_arguments"])
                if result.get("isError") or stages.get("wake_id") != wake["wake_id"] or stages.get("app") != app or stages.get("limit") != 50 or "requested_since" in stages or stages.get("returned_event_count") != 8 or stages.get("pagination_status") != "timestamp_only_not_lossless":
                    raise RuntimeError("Codex explicit request-to-wake drill-down lost identity, scope or native timing limits")
                if fixture_control("evidence")["wake_timeline_reads"] != before_wake_drill + 1:
                    raise RuntimeError("Request-to-wake drill-down read more than one bounded native response")

        rpc = start()
        try:
            login = rpc.request("mcpServer/oauth/login", {"name": "gregale", "scopes": ["gregale:read"], "clientRegistration": "dcr", "timeoutSecs": 45})
            fixture_control("approve", {"authorization_url": login["authorizationUrl"]})
            completed = rpc.wait_notification("mcpServer/oauthLogin/completed")
            if not completed["success"]:
                raise RuntimeError("Codex remote OAuth login failed")
            tools = read(rpc)
        finally:
            rpc.close()
        fixture_control("restart")
        rpc = start()
        try:
            read(rpc)
        finally:
            rpc.close()
        fixture_control("expire")
        expire_fixture_client_credentials(Path(workdir) / "client-config")
        rpc = start()
        try:
            read(rpc)
        finally:
            rpc.close()
        if fixture_control("evidence")["refreshes"] < 1:
            raise RuntimeError("Codex did not refresh its remote OAuth token")
        if request_mode:
            evidence = fixture_control("evidence")
            if evidence["native_writes"] != 0 or evidence["request_reads"] != (24 if request_mode == "approved" else 0):
                raise RuntimeError("Request acceptance performed unauthorized reads or writes")
            if evidence["profile_reads"] != (3 if request_mode == "approved" else 0):
                raise RuntimeError("Codex profile read count changed or consent bypassed")
            if evidence["wake_history_reads"] != (3 if request_mode == "approved" else 0):
                raise RuntimeError("Wake acceptance performed unauthorized native reads")
            if evidence["wake_timeline_reads"] != (6 if request_mode == "approved" else 0):
                raise RuntimeError("Wake stage acceptance performed unauthorized native reads")
            if evidence["wake_history_page_reads"] != (9 if request_mode == "approved" else 0):
                raise RuntimeError("Codex history page read count changed")
            if evidence["wake_page_reads"] != (9 if request_mode == "approved" else 0):
                raise RuntimeError("Keyset wake acceptance performed unexpected native reads")
            if evidence["error_groups_reads"] != (6 if request_mode == "approved" else 0) or evidence["error_requests_reads"] != (6 if request_mode == "approved" else 0):
                raise RuntimeError("Error triage acceptance performed unauthorized native reads")
            if evidence["timeseries_reads"] != (6 if request_mode == "approved" else 0):
                raise RuntimeError("Timeseries acceptance performed unauthorized reads")
            if evidence["analytics_reads"] != (3 if request_mode == "approved" else 0):
                raise RuntimeError("Analytics acceptance performed unauthorized reads")
            if evidence["performance_reads"] != (9 if request_mode == "approved" else 0):
                raise RuntimeError("Performance acceptance performed unauthorized reads")
            if evidence["residency_reads"] != (3 if request_mode == "approved" else 0):
                raise RuntimeError("Residency acceptance performed unauthorized reads")
            if evidence["critical_path_reads"] != (3 if request_mode == "approved" else 0):
                raise RuntimeError("Critical-path acceptance performed unauthorized reads")
            if evidence["comparison_reads"] != (6 if request_mode == "approved" else 0):
                raise RuntimeError("Comparison acceptance performed unauthorized reads")
            if evidence["trace_reads"] != (3 if request_mode == "approved" else 0):
                raise RuntimeError("Codex trace read count changed or consent bypassed")
            if evidence["regression_reads"] != (12 if request_mode == "approved" else 0):
                raise RuntimeError("Regression tool/resource acceptance performed unexpected reads")
            if request_mode == "approved":
                fixture_control("performance-empty")
                fixture_control("residency-empty")
                rpc = start()
                try:
                    thread_id, _server = connect(rpc)
                    profiles_call(rpc, thread_id, True, empty=True)
                    performance_calls(rpc, thread_id, True, empty=True)
                    fixture_control("trace-partial")
                    trace_call(rpc, thread_id, True, empty=True, partial=True)
                    residency_calls(rpc, thread_id, True, empty=True)
                    result, value = invoke(rpc, thread_id, "gregale_get_request_evidence", {"app":"alpha", "request_id":"row-1"})
                    if result.get("isError"):
                        raise RuntimeError("Codex empty request explanation read failed")
                    check_request_wake(value["request"], "not_reported")
                    for suffix, status in [("missing", "not_reported"), ("invalid", "invalid_native_wake"), ("cold", "observed")]:
                        result, linked = invoke(rpc, thread_id, "gregale_get_request_evidence", {"app":"alpha", "request_id":"row-1-wake-" + suffix})
                        if result.get("isError") or linked.get("request", {}).get("status") != 503:
                            raise RuntimeError("Codex optional wake metadata discarded other request evidence")
                        check_request_wake(linked["request"], status)
                        if any(marker in json.dumps(linked) for marker in ["private-wake", "fp_test_"]):
                            raise RuntimeError("Codex invalid wake ID leaked or was rewritten into a selector")
                        if suffix == "cold":
                            recs = linked.get("explanation", {}).get("recommendations", [])
                            if len(recs) != 1 or recs[0].get("action") != "inspect_wake_timeline" or recs[0].get("evidence_refs") != ["/request/wake"] or recs[0].get("tool") != linked["request"]["wake"]["timeline_tool"] or recs[0].get("arguments") != linked["request"]["wake"]["timeline_arguments"]:
                                raise RuntimeError("Codex supported wake investigation lost its exposed association/arguments")
                    check_request_explanation(value, empty=True)
                    result, value = invoke(rpc, thread_id, "gregale_get_request_evidence", {"app":"alpha", "request_id":"row-1-comparison-truncated"})
                    if result.get("isError") or value.get("request", {}).get("status") != 503:
                        raise RuntimeError("Codex truncated comparison discarded request evidence")
                    check_request_dependency_comparison(value, "partial")
                    if any(f.get("code") in ["dependency_regression", "dependency_failure_regression"] for f in value.get("explanation", {}).get("findings", [])):
                        raise RuntimeError("Codex collection-truncated comparison invented supported findings")
                    if any(r.get("action") == "inspect_dependency_change" for r in value.get("explanation", {}).get("recommendations", [])):
                        raise RuntimeError("Codex collection-truncated comparison invented an investigation action")
                    for selector, status in [("row-1-no-regression", "no_matching_observation"), ("row-1-regression-unavailable", "unavailable")]:
                        result, value = invoke(rpc, thread_id, "gregale_get_request_evidence", {"app":"alpha", "request_id":selector})
                        if result.get("isError") or value.get("request", {}).get("status") != 503:
                            raise RuntimeError("Codex missing regression discarded request evidence")
                        check_request_regression(value, status)
                        if any(f.get("code") in ["error_regression", "performance_regression"] for f in value.get("explanation", {}).get("findings", [])):
                            raise RuntimeError("Codex missing regression invented a finding")
                finally:
                    rpc.close()
            rpc = start()
            try:
                thread_id, _server = connect(rpc)
                request_calls(rpc, thread_id, False, app="future")
                fixture_control("replace")
                request_calls(rpc, thread_id, False)
            finally:
                rpc.close()
        fixture_control("revoke")
        rpc = start()
        try:
            _thread_id, server = connect(rpc)
            if server and not server.get("toolsError") and server.get("tools"):
                raise RuntimeError("Codex retained remote tools after grant revocation")
            for uri in saved_recoveries:
                try:
                    result = rpc.request("mcpServer/resource/read", {"threadId": _thread_id, "server": "gregale", "uri": uri})
                except RuntimeError as error:
                    if "timed out" in str(error).lower() or "timeout" in str(error).lower():
                        raise
                    continue
                if result.get("contents"):
                    raise RuntimeError("Codex retained recovery resource access after revocation")
        finally:
            rpc.close()
        print(json.dumps({"action_history": "passed", "workload_action_history": "passed", "environment_action_history": "passed", "budget_action_history": "passed", "action_outcomes": "passed", "outcome_discovery": "passed", "saved_plan_review": "passed", "plan_applicability": "passed", "plan_drift": "passed", "recovery_resources": "passed", "recovery_discovery": "passed", "client": "codex", "fixture": True, "transport": "streamable-http", "tools": tools, "oauth_login": "passed", "discovery": "passed", "read_calls": "passed", "source_planning": "passed", "scoped_telemetry": "passed", "rollout_watch": "passed", "resources": "passed", "restart": "passed", "refresh": "passed", "revocation": "passed", "request_diagnostics": request_mode, "request_discovery": "passed" if request_mode else "not_requested", "performance_diagnosis": "passed" if request_mode else "not_requested", "empty_performance": "passed" if request_mode == "approved" else "not_requested", "residency_diagnosis": "passed" if request_mode else "not_requested", "empty_residency": "passed" if request_mode == "approved" else "not_requested", "critical_paths": "passed" if request_mode else "not_requested", "empty_critical_paths": "passed" if request_mode == "approved" else "not_requested", "deployment_comparison": "passed" if request_mode else "not_requested", "empty_comparison": "passed" if request_mode == "approved" else "not_requested", "regression_inbox": "passed" if request_mode else "not_requested", "regression_resource": "passed" if request_mode else "not_requested", "fleet_regressions": "passed" if request_mode else "not_requested", "incident_bundle": "passed" if request_mode else "not_requested", "selected_trace": "passed" if request_mode else "not_requested", "partial_trace": "passed" if request_mode == "approved" else "not_requested", "empty_regressions": "passed" if request_mode == "approved" else "not_requested", "request_explanations": "passed" if request_mode else "not_requested", "empty_request_explanation": "passed" if request_mode == "approved" else "not_requested", "request_critical_path": "passed" if request_mode else "not_requested", "empty_request_critical_path": "passed" if request_mode == "approved" else "not_requested", "guest_execution": "passed" if request_mode else "not_requested", "missing_guest_execution": "passed" if request_mode == "approved" else "not_requested", "request_regression_context": "passed" if request_mode else "not_requested", "missing_request_regression": "passed" if request_mode == "approved" else "not_requested", "request_dependency_comparison": "passed" if request_mode else "not_requested", "missing_request_dependency_comparison": "passed" if request_mode == "approved" else "not_requested", "truncated_request_dependency_comparison": "passed" if request_mode == "approved" else "not_requested", "dependency_impact": "passed" if request_mode else "not_requested", "empty_dependency_impact": "passed" if request_mode == "approved" else "not_requested", "request_analytics": "passed" if request_mode else "not_requested", "empty_request_analytics": "passed" if request_mode == "approved" else "not_requested", "request_analytics_timeseries": "passed" if request_mode else "not_requested", "empty_request_analytics_timeseries": "passed" if request_mode == "approved" else "not_requested", "request_wake": "passed" if request_mode else "not_requested", "missing_request_wake": "passed" if request_mode == "approved" else "not_requested", "invalid_request_wake": "passed" if request_mode == "approved" else "not_requested", "request_wake_recommendation": "passed" if request_mode == "approved" else "not_requested", "wake_pagination": "passed" if request_mode else "not_requested", "wake_history_pagination": "passed" if request_mode else "not_requested", "wake_timeline": "passed" if request_mode else "not_requested", "empty_wake_timeline": "passed" if request_mode == "approved" else "not_requested", "wake_history": "passed" if request_mode else "not_requested", "empty_wake_history": "passed" if request_mode == "approved" else "not_requested", "error_triage": "passed" if request_mode else "not_requested", "empty_error_triage": "passed" if request_mode == "approved" else "not_requested", "profiles": "passed" if request_mode else "not_requested", "empty_profiles": "passed" if request_mode == "approved" else "not_requested", "model_requests": 0, "deployment_writes": 0}))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--codex-binary", default=shutil.which("codex"))
    parser.add_argument("--gregale-binary", default="bin/gregale")
    parser.add_argument("--profile")
    parser.add_argument("--api-base")
    parser.add_argument("--fixture", action="store_true")
    parser.add_argument("--remote-fixture", action="store_true", help="isolated HTTPS/OAuth fixture driven by TestCodexRemoteOAuthAcceptance")
    parser.add_argument("--remote-url")
    parser.add_argument("--codex-config-dir", help="optional writable Codex profile with an existing remote OAuth login")
    parser.add_argument("--diagnostics-file", help="new private file for client diagnostics; never included in the report")
    args = parser.parse_args()
    if not args.codex_binary:
        parser.error("Codex must be installed or supplied with --codex-binary")
    # app-server runs in a disposable directory, so resolve the caller's path.
    args.codex_binary = str(Path(shutil.which(args.codex_binary) or args.codex_binary).resolve())
    if args.remote_fixture:
        if args.fixture or args.profile or args.api_base or args.remote_url or args.codex_config_dir:
            parser.error("--remote-fixture cannot use a live endpoint or profile")
        remote_fixture_acceptance(args)
        return
    if args.fixture and (args.profile or args.api_base or args.remote_url):
        parser.error("--fixture cannot use a live profile, API endpoint or remote URL")
    backend = None
    if args.fixture:
        backend = ThreadingHTTPServer(("127.0.0.1", 0), FixtureAPI)
        threading.Thread(target=backend.serve_forever, daemon=True).start()
        args.api_base = f"http://127.0.0.1:{backend.server_port}"
    fields = ["startup_timeout_sec=30", "tool_timeout_sec=30"]
    if args.remote_url:
        fields.append("url=" + toml_string(args.remote_url))
        if os.environ.get("GREGALE_MCP_ACCESS_TOKEN"):
            fields.append('bearer_token_env_var="GREGALE_MCP_ACCESS_TOKEN"')
    else:
        binary = str(Path(args.gregale_binary).resolve())
        command_args = ["--non-interactive"]
        if args.profile:
            command_args += ["--profile", args.profile]
        command_args += ["mcp", "serve", "--transport", "stdio"]
        fields += ["command=" + toml_string(binary), "args=" + json.dumps(command_args)]
        fields.append('env_vars=["FAAS_TOKEN","FAAS_API","XDG_CONFIG_HOME"]')
        env_fields = []
        if args.api_base:
            env_fields.append("FAAS_API=" + toml_string(args.api_base))
        if args.fixture:
            env_fields.append('FAAS_TOKEN="fp_test_codex_acceptance_fixture"')
        if env_fields:
            fields.append("env={" + ",".join(env_fields) + "}")
    config = "mcp_servers={gregale={" + ",".join(fields) + "}}"
    try:
        with tempfile.TemporaryDirectory(prefix="gregale-codex-acceptance-") as workdir:
            rpc = CodexRPC(args.codex_binary, config, workdir, args.diagnostics_file, args.codex_config_dir)
            try:
                rpc.request("initialize", {"clientInfo": {"name": "gregale-mcp-acceptance", "version": "1"}, "capabilities": {"experimentalApi": True}})
                rpc.send({"method": "initialized"})
                thread = rpc.request("thread/start", {"cwd": workdir, "ephemeral": True, "approvalPolicy": "never", "sandbox": "read-only"})
                thread_id = thread["thread"]["id"]
                status = rpc.request("mcpServerStatus/list", {"threadId": thread_id})
                server = next((item for item in status["data"] if item["name"] == "gregale"), None)
                if not server or server.get("toolsError"):
                    raise RuntimeError("Codex could not discover Gregale MCP tools")
                inventory = server["tools"]
                names = set(inventory) if isinstance(inventory, dict) else {tool["name"] for tool in inventory}
                required = {"gregale_get_route_budgets", "gregale_plan_route_budgets", "gregale_preflight", "gregale_watch_rollout", "gregale_context", "gregale_list_apps", "gregale_plan_deploy", "gregale_plan_source_deploy", "gregale_plan_workload_rollout", "gregale_plan_service_abort", "gregale_get_rollout", "gregale_plan_rollback", "gregale_wait_deployment", "gregale_list_environments", "gregale_get_environment", "gregale_plan_environment_promotion", "gregale_plan_environment_rollback"}
                if not required <= names:
                    raise RuntimeError("Gregale MCP tool inventory is incomplete")
                write_tools = {"gregale_apply_deploy", "gregale_promote_deployment", "gregale_apply_rollback", "gregale_apply_environment_promotion", "gregale_apply_environment_rollback", "gregale_apply_workload_rollout", "gregale_apply_service_abort"}
                if args.fixture and names & write_tools:
                    raise RuntimeError("Fixture exposed write tools without opt-in")
                for tool in ["gregale_context", "gregale_list_apps"]:
                    result = rpc.request("mcpServer/tool/call", {"threadId": thread_id, "server": "gregale", "tool": tool, "arguments": {}})
                    if result.get("isError"):
                        raise RuntimeError(f"Gregale rejected the read-only acceptance call {tool}")
                if args.fixture:
                    source_planning_call(rpc, thread_id)
                    scoped_telemetry_calls(rpc, thread_id)
                    rollout_watch_call(rpc, thread_id)
                    context_resource_call(rpc, thread_id)
                    preflight_call(rpc, thread_id)
                if args.fixture and (FixtureAPI.calls < 3 or FixtureAPI.mutations):
                    raise RuntimeError("Fixture observed missing reads or an unexpected mutation")
                report = {"client": "codex", "fixture": args.fixture, "transport": "streamable-http" if args.remote_url else "stdio", "tools": len(names), "discovery": "passed", "read_calls": "passed", "model_requests": 0, "deployment_writes": 0}
                if args.fixture:
                    report["source_planning"] = "passed"
                    report["scoped_telemetry"] = "passed"
                    report["rollout_watch"] = "passed"
                    report["resources"] = "passed"
                print(json.dumps(report))
            finally:
                rpc.close()
    finally:
        if backend:
            backend.shutdown()
            backend.server_close()


if __name__ == "__main__":
    try:
        main()
    except (OSError, RuntimeError, KeyError, ValueError) as exc:
        raise SystemExit(str(exc)) from None
