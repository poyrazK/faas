#!/usr/bin/env python3
"""Synthetic canary for customer MCP hosting on production.

Answers one question from the outside: can a customer host an MCP server on
Gregale right now, and can an agent reach Gregale's own hosted MCP? It uses
only public endpoints and the released CLI, never control-plane SSH, so it
keeps working when operator access is broken (see ADR-20261123100000).

Checks (each retried once; a pass on retry is reported as flaky, not failed):
  deploy      fresh Go starter from the released CLI + canary tools, promoted
              (only with --deploy, or when the canary app does not exist)
  discover    2026-07-28 server/discover
  legacy      2025-06-18 initialize is negotiated, never refused
  catalog     tools/list includes the starter and canary tools
  call        tools/call add
  stream      stream_demo sends progress over SSE
  long_call   a 20 s silent call (starters once cut responses at 15 s)
  egress      the app reaches an external HTTPS host
  cross_node  the app reaches its own public URL through every entry IP
  wake        park, then a call through the cold path within the wake budget
  wake_egress the first outbound call after a wake does not hang
  mgmt_oauth  hosted management MCP OAuth discovery metadata
  mgmt_tools  hosted management MCP tools/list with the canary agent key
  mgmt_logs   hosted gregale_get_logs on the canary app

Credentials come only from the environment: MCP_CANARY_KEY (an org agent key
with agent,deploy:write and approvals none) is used as FAAS_TOKEN for the CLI
and as the bearer for the hosted management MCP. It is never logged.
"""

import argparse
import json
import os
import shutil
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

PROTOCOL = "2026-07-28"
LEGACY_PROTOCOL = "2025-06-18"
CANARY_TOOLS = ("canary_sleep", "canary_fetch", "canary_whoami")
STARTER_TOOLS = ("add", "greet", "stream_demo")
HERE = Path(__file__).resolve().parent
TOOLS_SOURCE = HERE.parent.parent / "deploy" / "canary" / "mcp" / "_starter" / "canary_tools.go"


class CheckFailed(Exception):
    pass


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


OPENER = urllib.request.build_opener(NoRedirect())


def http(method, url, body=None, headers=None, timeout=30):
    """Return (status, headers, body bytes). HTTP errors are results, not raises."""
    data = None if body is None else json.dumps(body).encode()
    request = urllib.request.Request(url, data=data, method=method, headers=headers or {})
    try:
        with OPENER.open(request, timeout=timeout) as response:
            return response.status, dict(response.headers), response.read()
    except urllib.error.HTTPError as err:
        return err.code, dict(err.headers or {}), err.read() if err.fp else b""
    except (urllib.error.URLError, OSError) as err:
        raise CheckFailed(f"{type(err).__name__}: {getattr(err, 'reason', err)}") from None


def parse_messages(content_type, payload):
    """JSON-RPC messages from a JSON or SSE body, in order."""
    text = payload.decode("utf-8", "replace")
    if (content_type or "").split(";")[0].strip() == "text/event-stream":
        messages, data = [], []
        for line in text.splitlines() + [""]:
            if line.startswith("data:"):
                data.append(line[5:].lstrip())
            elif line == "" and data:
                messages.append(json.loads("\n".join(data)))
                data = []
        return messages
    return [json.loads(text)] if text.strip() else []


class MCP:
    def __init__(self, url, token=None, protocol=PROTOCOL):
        self.url, self.token, self.protocol = url, token, protocol
        self.next_id = 0

    def rpc(self, method, params=None, name=None, timeout=60, progress_token=None):
        self.next_id += 1
        params = dict(params or {})
        if self.protocol == PROTOCOL:
            meta = {
                "io.modelcontextprotocol/protocolVersion": PROTOCOL,
                "io.modelcontextprotocol/clientInfo": {"name": "gregale-mcp-canary", "version": "1"},
                "io.modelcontextprotocol/clientCapabilities": {},
            }
            if progress_token is not None:
                meta["progressToken"] = progress_token
            params["_meta"] = {**params.get("_meta", {}), **meta}
        elif progress_token is not None:
            params["_meta"] = {"progressToken": progress_token}
        headers = {
            "Content-Type": "application/json",
            "Accept": "application/json, text/event-stream",
            "MCP-Protocol-Version": self.protocol,
            "Mcp-Method": method,
        }
        if name:
            headers["Mcp-Name"] = name
        if self.token:
            headers["Authorization"] = "Bearer " + self.token
        body = {"jsonrpc": "2.0", "id": self.next_id, "method": method, "params": params}
        status, response_headers, payload = http("POST", self.url, body, headers, timeout)
        content_type = next((v for k, v in response_headers.items() if k.lower() == "content-type"), "")
        if status != 200:
            raise CheckFailed(f"{method}: HTTP {status} {payload[:160].decode('utf-8', 'replace')}")
        messages = parse_messages(content_type, payload)
        result = next((m for m in messages if m.get("id") == self.next_id), None)
        if result is None:
            raise CheckFailed(f"{method}: no response for request {self.next_id}")
        if "error" in result:
            raise CheckFailed(f"{method}: JSON-RPC error {result['error'].get('code')}")
        return result["result"], messages

    def call(self, tool, arguments=None, timeout=60, progress_token=None):
        result, messages = self.rpc("tools/call", {"name": tool, "arguments": arguments or {}},
                                    name=tool, timeout=timeout, progress_token=progress_token)
        if result.get("isError"):
            raise CheckFailed(f"{tool}: tool error {json.dumps(result.get('content'))[:160]}")
        return result, messages


def text_of(result):
    return "".join(c.get("text", "") for c in result.get("content", []) if c.get("type") == "text")


class Canary:
    def __init__(self, app, key, cli, wake_budget_s=10.0, mgmt_url="https://api.gregale.dev/mcp",
                 app_domain="gregale.app", sleep=time.sleep, clock=time.monotonic):
        self.app, self.key, self.cli = app, key, cli
        self.endpoint = f"https://{app}.{app_domain}/mcp"
        self.host = f"{app}.{app_domain}"
        self.mgmt_url = mgmt_url
        self.wake_budget_s = wake_budget_s
        self.sleep, self.clock = sleep, clock
        self.results = []

    # -- plumbing ---------------------------------------------------------
    def check(self, name, fn, retry=True):
        start = self.clock()
        attempts = 2 if retry else 1
        errors = []
        for attempt in range(attempts):
            try:
                detail = fn() or ""
                status = "pass" if attempt == 0 else "flaky"
                if errors:
                    detail = f"{detail} (first attempt: {errors[0]})".strip()
                self.results.append({"name": name, "status": status,
                                     "ms": int((self.clock() - start) * 1000), "detail": detail})
                return True
            except CheckFailed as err:
                errors.append(str(err))
            except Exception as err:  # a canary bug must still report, not crash
                errors.append(f"{type(err).__name__}: {err}")
        self.results.append({"name": name, "status": "fail",
                             "ms": int((self.clock() - start) * 1000), "detail": " | ".join(errors)})
        return False

    def run_cli(self, *args, timeout=900):
        env = {**os.environ, "FAAS_TOKEN": self.key, "NO_COLOR": "1"}
        proc = subprocess.run([self.cli, *args], env=env, stdin=subprocess.DEVNULL,
                              capture_output=True, text=True, timeout=timeout)
        if proc.returncode != 0:
            tail = (proc.stdout + proc.stderr).strip().splitlines()[-6:]
            raise CheckFailed(f"gregale {' '.join(args[:3])} exit {proc.returncode}: {' / '.join(tail)}")
        return proc.stdout

    def app_mcp(self, protocol=PROTOCOL):
        return MCP(self.endpoint, protocol=protocol)

    # -- checks -----------------------------------------------------------
    def deploy(self):
        work = Path(tempfile.mkdtemp(prefix="mcp-canary-"))
        try:
            starter = work / "server"
            self.run_cli("mcp", "server", "init", "--path", str(starter), "--runtime", "go", timeout=120)
            shutil.copy(TOOLS_SOURCE, starter / "canary_tools.go")
            manifest_path = starter / "gregale-mcp.json"
            manifest = json.loads(manifest_path.read_text())
            for tool in CANARY_TOOLS:
                manifest["auth"]["tool_scopes"][tool] = []
            manifest_path.write_text(json.dumps(manifest, indent=2) + "\n")
            # Tie each deploy to this run so it is never a no-op redeploy.
            (starter / "canary_revision.go").write_text(
                f'package main\n\nconst canaryRevision = "{os.environ.get("GITHUB_RUN_ID", int(time.time()))}"\n')
            self.run_cli("mcp", "server", "deploy", "--path", str(starter), "--name", self.app)
            return "deployed and promoted"
        finally:
            shutil.rmtree(work, ignore_errors=True)

    def app_exists(self):
        try:
            self.app_mcp().rpc("server/discover", timeout=30)
            return True
        except CheckFailed:
            return False

    def discover(self):
        result, _ = self.app_mcp().rpc("server/discover", timeout=30)
        if "tools" not in result.get("capabilities", {}):
            raise CheckFailed("server/discover did not advertise tools")

    def legacy(self):
        client = self.app_mcp(LEGACY_PROTOCOL)
        result, _ = client.rpc("initialize", {
            "protocolVersion": LEGACY_PROTOCOL, "capabilities": {},
            "clientInfo": {"name": "gregale-mcp-canary", "version": "1"}}, timeout=30)
        if not result.get("protocolVersion"):
            raise CheckFailed("initialize answered without a protocolVersion")
        return f"negotiated {result['protocolVersion']}"

    def catalog(self):
        result, _ = self.app_mcp().rpc("tools/list", timeout=30)
        names = {tool.get("name") for tool in result.get("tools", [])}
        missing = sorted(set(STARTER_TOOLS + CANARY_TOOLS) - names)
        if missing:
            raise CheckFailed(f"tools/list is missing {', '.join(missing)}")

    def call(self):
        result, _ = self.app_mcp().call("add", {"a": 2, "b": 3}, timeout=30)
        if text_of(result).strip() != "5":
            raise CheckFailed(f"add returned {text_of(result)[:40]!r}")

    def stream(self):
        _, messages = self.app_mcp().call("stream_demo", {}, timeout=30, progress_token="canary")
        progress = [m for m in messages if m.get("method") == "notifications/progress"]
        if not progress:
            raise CheckFailed("stream_demo sent no progress notifications")
        return f"{len(progress)} progress events"

    def long_call(self):
        result, _ = self.app_mcp().call("canary_sleep", {"seconds": 20}, timeout=60)
        if result.get("structuredContent", {}).get("slept_ms", 0) < 19000:
            raise CheckFailed("canary_sleep returned early")

    def fetch(self, url, connect_ip=None, timeout=30):
        args = {"url": url}
        if connect_ip:
            args["connect_ip"] = connect_ip
        result, _ = self.app_mcp().call("canary_fetch", args, timeout=timeout)
        return result.get("structuredContent", {})

    def egress(self):
        out = self.fetch("https://example.com/")
        if out.get("status") != 200:
            raise CheckFailed(f"example.com: status {out.get('status')} {out.get('error', '')[:120]}")
        return f"{out.get('ms')} ms"

    def entry_ips(self):
        infos = socket.getaddrinfo(self.host, 443, socket.AF_INET, socket.SOCK_STREAM)
        return sorted({info[4][0] for info in infos})

    def cross_node(self):
        ips = self.entry_ips()
        if not ips:
            raise CheckFailed(f"{self.host} has no A records")
        bad = []
        for ip in ips:
            out = self.fetch(f"https://{self.host}/healthz", connect_ip=ip)
            if out.get("status") != 200:
                bad.append(f"{ip}: status {out.get('status')} {out.get('error', '')[:80]}")
        if bad:
            raise CheckFailed("app cannot reach its own URL via " + "; ".join(bad))
        return f"{len(ips)} entry IPs"

    def wake(self):
        self.run_cli("app", "park", self.app, timeout=120)
        self.sleep(20)
        start = self.clock()
        self.app_mcp().call("canary_whoami", {}, timeout=60)
        elapsed = self.clock() - start
        if elapsed > self.wake_budget_s:
            raise CheckFailed(f"cold call took {elapsed:.1f} s (budget {self.wake_budget_s:.0f} s)")
        return f"cold call {elapsed * 1000:.0f} ms"

    def wake_egress(self):
        out = self.fetch("https://example.com/")
        if out.get("status") != 200 or out.get("ms", 0) > 5000:
            raise CheckFailed(f"post-wake fetch: status {out.get('status')} in {out.get('ms')} ms "
                              f"{out.get('error', '')[:100]}")
        return f"{out.get('ms')} ms"

    def mgmt_oauth(self):
        parsed = urllib.parse.urlsplit(self.mgmt_url)
        origin = f"{parsed.scheme}://{parsed.netloc}"
        status, _, body = http("GET", f"{origin}/.well-known/oauth-protected-resource{parsed.path}")
        if status != 200:
            raise CheckFailed(f"protected resource metadata: HTTP {status}")
        resource = json.loads(body)
        if resource.get("resource") != self.mgmt_url or not resource.get("authorization_servers"):
            raise CheckFailed("protected resource metadata does not name this resource")
        issuer = urllib.parse.urlsplit(resource["authorization_servers"][0])
        status, _, body = http("GET", f"{issuer.scheme}://{issuer.netloc}"
                                      f"/.well-known/oauth-authorization-server{issuer.path}")
        if status != 200:
            raise CheckFailed(f"authorization server metadata: HTTP {status}")
        server = json.loads(body)
        for field in ("authorization_endpoint", "token_endpoint", "registration_endpoint"):
            if not server.get(field):
                raise CheckFailed(f"authorization server metadata lacks {field}")
        if "S256" not in server.get("code_challenge_methods_supported", []):
            raise CheckFailed("authorization server does not offer PKCE S256")

    def mgmt_client(self):
        client = MCP(self.mgmt_url, token=self.key, protocol=LEGACY_PROTOCOL)
        client.rpc("initialize", {"protocolVersion": LEGACY_PROTOCOL, "capabilities": {},
                                  "clientInfo": {"name": "gregale-mcp-canary", "version": "1"}})
        return client

    def mgmt_tools(self):
        result, _ = self.mgmt_client().rpc("tools/list")
        names = {tool.get("name") for tool in result.get("tools", [])}
        if "gregale_get_logs" not in names:
            raise CheckFailed("hosted tools/list lacks gregale_get_logs")
        return f"{len(names)} tools"

    def mgmt_logs(self):
        result, _ = self.mgmt_client().call("gregale_get_logs", {"app": self.app})
        text = text_of(result)
        if '"items"' not in text:
            raise CheckFailed("gregale_get_logs returned no items field")

    # -- run --------------------------------------------------------------
    def run(self, deploy=False):
        if deploy or not self.app_exists():
            if not self.check("deploy", self.deploy, retry=False):
                return self.results
        for name in ("discover", "legacy", "catalog", "call", "stream", "long_call",
                     "egress", "cross_node", "wake", "wake_egress"):
            self.check(name, getattr(self, name), retry=name not in ("wake", "wake_egress"))
        for name in ("mgmt_oauth", "mgmt_tools", "mgmt_logs"):
            self.check(name, getattr(self, name))
        return self.results


def summarize(results):
    failed = [r for r in results if r["status"] == "fail"]
    flaky = [r for r in results if r["status"] == "flaky"]
    lines = ["| check | result | ms | detail |", "|---|---|---|---|"]
    for r in results:
        detail = r["detail"].replace("|", "/").replace("\n", " ")[:300]
        lines.append(f"| {r['name']} | {r['status']} | {r['ms']} | {detail} |")
    return failed, flaky, "\n".join(lines)


def notify(webhook, text, opener=None):
    """Post a failure text to the fallback webhook. The URL is a credential."""
    url = urllib.parse.urlsplit(webhook)
    if url.scheme != "https" or not url.hostname or any(c.isspace() for c in webhook):
        raise ValueError("fallback_url_invalid")
    request = urllib.request.Request(webhook, data=json.dumps({"text": text}).encode(),
                                     headers={"Content-Type": "application/json"}, method="POST")
    with (opener or OPENER).open(request, timeout=15) as response:
        if not 200 <= response.status < 300:
            raise ValueError("fallback_submission_failed")


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--app", default=os.environ.get("MCP_CANARY_APP", "mcp-canary"))
    parser.add_argument("--cli", default=os.environ.get("GREGALE_CLI", "gregale"))
    parser.add_argument("--deploy", action="store_true", help="redeploy a fresh starter first")
    parser.add_argument("--wake-budget", type=float, default=10.0)
    parser.add_argument("--page", action="store_true", help="post failures to the fallback webhook")
    parser.add_argument("--json", help="write results here")
    args = parser.parse_args(argv)

    key = os.environ.get("MCP_CANARY_KEY", "")
    if not key:
        print("::error::MCP_CANARY_KEY is required", file=sys.stderr)
        return 2
    canary = Canary(args.app, key, args.cli, wake_budget_s=args.wake_budget)
    results = canary.run(deploy=args.deploy)
    failed, flaky, table = summarize(results)
    print(table)
    if args.json:
        Path(args.json).write_text(json.dumps(results, indent=2))
    summary_path = os.environ.get("GITHUB_STEP_SUMMARY")
    if summary_path:
        with open(summary_path, "a") as handle:
            handle.write(f"## MCP hosting canary ({args.app})\n\n{table}\n")
    for r in flaky:
        print(f"::warning::mcp-canary {r['name']} passed only on retry: {r['detail'][:200]}")
    if not failed:
        return 0
    for r in failed:
        print(f"::error::mcp-canary {r['name']} failed: {r['detail'][:300]}")
    if args.page:
        webhook = os.environ.get("CANARY_FALLBACK_WEBHOOK_URL", "")
        run = ""
        if os.environ.get("GITHUB_RUN_ID"):
            run = (f" Run: {os.environ.get('GITHUB_SERVER_URL', 'https://github.com')}/"
                   f"{os.environ.get('GITHUB_REPOSITORY', '')}/actions/runs/{os.environ['GITHUB_RUN_ID']}")
        text = ("Gregale MCP hosting canary failed: "
                + "; ".join(f"{r['name']}: {r['detail'][:160]}" for r in failed) + "." + run)
        if not webhook:
            print("::error::canary-notify: fallback_unconfigured", file=sys.stderr)
        else:
            try:
                notify(webhook, text)
                print("::notice::canary-notify: failure_submitted_fallback")
            except Exception:  # never log the URL or provider response
                print("::error::canary-notify: fallback_submission_failed", file=sys.stderr)
    return 1


if __name__ == "__main__":
    sys.exit(main())
