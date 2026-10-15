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
    if not resource.startswith("https://127.0.0.1:"):
        raise RuntimeError("Remote acceptance requires the isolated loopback TLS fixture")
    config = "mcp_servers={gregale={url=" + toml_string(resource) + ",startup_timeout_sec=20,tool_timeout_sec=20}}"
    with tempfile.TemporaryDirectory(prefix="gregale-codex-remote-") as workdir:
        phase = 0
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

        def read(rpc):
            thread_id, server = connect(rpc)
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
            if not {"gregale_plan_source_deploy", "gregale_plan_workload_rollout", "gregale_plan_service_abort", "gregale_get_rollout"}.issubset(names):
                raise RuntimeError("Codex remote source tool is missing")
            source_planning_call(rpc, thread_id)
            scoped_telemetry_calls(rpc, thread_id)
            rollout_watch_call(rpc, thread_id)
            context_resource_call(rpc, thread_id)
            preflight_call(rpc, thread_id)
            return len(names)

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
        fixture_control("revoke")
        rpc = start()
        try:
            _thread_id, server = connect(rpc)
            if server and not server.get("toolsError") and server.get("tools"):
                raise RuntimeError("Codex retained remote tools after grant revocation")
        finally:
            rpc.close()
        print(json.dumps({"client": "codex", "fixture": True, "transport": "streamable-http", "tools": tools, "oauth_login": "passed", "discovery": "passed", "read_calls": "passed", "source_planning": "passed", "scoped_telemetry": "passed", "rollout_watch": "passed", "resources": "passed", "restart": "passed", "refresh": "passed", "revocation": "passed", "model_requests": 0, "deployment_writes": 0}))


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
