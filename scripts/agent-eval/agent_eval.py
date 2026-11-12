#!/usr/bin/env python3
"""Run coding agents against Gregale tasks and score them (ADR-20261026152009,
ADR-20261112223000).

Each run scaffolds a starter, or checks out a pinned commit of a public
repository, into a fresh directory, applies the task's setup (edits and
deploys), starts the agent headless with the Gregale CLI and, optionally, the
management MCP server at one toolset, then checks the result (HTTP response,
serving release, untouched files, the agent's answer, the apps it created).
It grades how the agent worked (features used, anti-patterns, traps hit),
records success, turns, tool calls, tokens, cost and time per run, deletes
every app and project the run created, and appends one JSON line per run.

    agent_eval.py list
    agent_eval.py run --agents claude,codex,gemini,cursor --toolsets core,all,none [--tasks ID,...] [--repeat N]
    agent_eval.py summarize results.jsonl

`run` deploys real apps. It requires GREGALE_EVAL_TOKEN, an API key of a
dedicated evaluation account, and never uses your own login. GREGALE_EVAL_API
selects the API (default https://api.gregale.dev). Runs are sequential: apps
and projects that appear in the account during a run are attributed to it.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import secrets
import shutil
import statistics
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
from pathlib import Path

HERE = Path(__file__).resolve().parent
TASKS = HERE / "tasks.json"
AGENTS = ("claude", "codex", "gemini", "cursor")
AGENT_BINARIES = {"claude": "claude", "codex": "codex", "gemini": "gemini", "cursor": "cursor-agent"}
TOOLSETS = ("core", "release", "diagnose", "all", "none")
SETUP_KINDS = ("deploy", "replace")
CHECK_KINDS = ("http", "serving", "file_contains", "answer", "apps_created", "apps_serving", "app_http", "no_traps")
FEATURE_KINDS = ("command", "file", "state")
FEATURE_STATES = ("project_created",)
DEFAULT_API = "https://api.gregale.dev"
# Failure codes that mean the agent walked into a known platform trap.
DEFAULT_TRAPS = ("liveness_exhausted", "app_startup_timeout")
# Ways of working around the CLI that count against a run.
ANTI_PATTERNS = {
    "raw_api_call": re.compile(r"\b(curl|wget|https?|xh)\b[^\n]*(api\.gregale\.dev|/v1/(apps|projects|deployments))"),
    "manual_tarball": re.compile(r"\btar\s+(-\S*c|c\S*\s)"),
    "app_delete": re.compile(r"\bgregale\b[^\n|;&]*\b(app|apps|project)\s+(delete|rm)\b"),
    "api_key_minted": re.compile(r"\bgregale\b[^\n|;&]*\bkey\s+create\b"),
}
REPEATED_COMMAND_LIMIT = 3
# Variables an agent sets for the commands it runs. The eval's caller may be an
# agent itself; its markers must not leak into the agent under test.
AGENT_MARKERS = ("CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "GEMINI_CLI", "CODEX_SANDBOX", "CODEX_SANDBOX_NETWORK_DISABLED", "CURSOR_AGENT")
# Shell commands a Claude run may use without asking; everything else is
# denied in headless mode.
CLAUDE_ALLOWED_TOOLS = ["Bash(gregale *)", "Bash(curl *)", "Read", "Edit", "Write", "Glob", "Grep", "mcp__gregale"]
# A foreign-repository task runs as a person's agent would: any shell command.
CLAUDE_ANY_SHELL = ["Bash", "Read", "Edit", "Write", "Glob", "Grep", "mcp__gregale"]


class EvalError(Exception):
    """A task definition or environment problem that stops the run."""


def load_tasks(path: Path = TASKS) -> list[dict]:
    tasks = json.loads(path.read_text())
    seen = set()
    for task in tasks:
        tid = task.get("id", "")
        if not re.fullmatch(r"[a-z][a-z0-9-]{1,30}", tid) or tid in seen:
            raise EvalError(f"task id {tid!r} must be a unique lowercase slug of at most 31 characters")
        seen.add(tid)
        repo = task.get("repo")
        if repo is not None:
            if task.get("template") or not re.fullmatch(r"https://github\.com/[\w.-]+/[\w.-]+", repo.get("url", "")) \
                    or not re.fullmatch(r"[0-9a-f]{40}", repo.get("commit", "")):
                raise EvalError(f"{tid}: repo needs a github.com url and a full commit SHA, and excludes template")
            if task.get("setup"):
                raise EvalError(f"{tid}: repo tasks take no setup")
        elif not task.get("template"):
            raise EvalError(f"{tid}: a task needs a template or a repo")
        elif "{app}" not in task.get("prompt", ""):
            raise EvalError(f"{tid}: prompt must name the app with {{app}}")
        for step in task.get("setup", []):
            if step.get("kind") not in SETUP_KINDS:
                raise EvalError(f"{tid}: unknown setup kind {step.get('kind')!r}")
        if not task.get("checks"):
            raise EvalError(f"{tid}: at least one check is required")
        for check in task["checks"]:
            if check.get("kind") not in CHECK_KINDS:
                raise EvalError(f"{tid}: unknown check kind {check.get('kind')!r}")
        for feature in task.get("features", []):
            if not re.fullmatch(r"[a-z][a-z0-9-]{1,40}", feature.get("id", "")) or feature.get("kind") not in FEATURE_KINDS:
                raise EvalError(f"{tid}: feature {feature.get('id')!r} needs a slug id and a kind in {FEATURE_KINDS}")
            if feature["kind"] == "state" and feature.get("state") not in FEATURE_STATES:
                raise EvalError(f"{tid}: unknown state feature {feature.get('state')!r}")
            if feature["kind"] != "state":
                try:
                    re.compile(feature["pattern"])
                except (KeyError, re.error) as error:
                    raise EvalError(f"{tid}: feature {feature['id']} needs a valid pattern: {error}") from None
        if task.get("budget_usd", 0) <= 0 or task.get("timeout_s", 0) <= 0:
            raise EvalError(f"{tid}: budget_usd and timeout_s must be positive")
    return tasks


def app_slug(task_id: str) -> str:
    return f"eval-{task_id[:20]}-{secrets.token_hex(3)}"


def _text(value) -> str:
    """Captured output as text; a timeout returns bytes even in text mode."""
    if isinstance(value, bytes):
        return value.decode("utf-8", "replace")
    return value or ""


def replace_once(path: Path, old: str, new: str) -> None:
    text = path.read_text()
    if text.count(old) != 1:
        raise EvalError(f"{path.name}: expected exactly one {old[:40]!r}")
    path.write_text(text.replace(old, new))


def checkout(url: str, commit: str, project: Path) -> None:
    """A shallow checkout of exactly one commit, without the caller's git config."""
    project.mkdir(parents=True)
    env = {**os.environ, "GIT_CONFIG_GLOBAL": os.devnull, "GIT_CONFIG_NOSYSTEM": "1", "GIT_TERMINAL_PROMPT": "0"}
    for args in (["init", "-q"], ["fetch", "-q", "--depth", "1", url, commit], ["-c", "advice.detachedHead=false", "checkout", "-q", "FETCH_HEAD"]):
        done = subprocess.run(["git", *args], cwd=project, env=env, capture_output=True, text=True, timeout=600)
        if done.returncode != 0:
            raise EvalError(f"checkout {url}@{commit[:12]}: git {args[0]}: {done.stderr[-300:]}")


def task_prompt(task: dict, slug: str, toolset: str) -> str:
    if task.get("repo"):
        # The person's own words; nothing about Gregale is added.
        return task["prompt"].format(repo_url=task["repo"]["url"])
    return task["prompt"].format(app=slug) + " The gregale CLI is installed and logged in" + (
        " and the Gregale MCP server is connected." if toolset != "none" else ".")


# --- agent commands ---------------------------------------------------------


def mcp_args(toolset: str, writes: bool) -> list[str]:
    args = ["mcp", "serve", "--transport", "stdio", "--tools", toolset]
    return args + ["--allow-writes"] if writes else args


def claude_command(prompt: str, run_dir: Path, gregale: str, toolset: str, budget: float, model: str | None, writes: bool, any_shell: bool = False) -> list[str]:
    allowed = CLAUDE_ANY_SHELL if any_shell else CLAUDE_ALLOWED_TOOLS
    command = ["claude", "-p", prompt, "--output-format", "stream-json", "--verbose", "--no-session-persistence",
               "--max-budget-usd", f"{budget:.2f}", "--permission-mode", "dontAsk", "--allowedTools", *allowed, "--strict-mcp-config"]
    if toolset != "none":
        config = {"mcpServers": {"gregale": {"command": gregale, "args": mcp_args(toolset, writes)}}}
        path = run_dir / "mcp.json"
        path.write_text(json.dumps(config))
        command += ["--mcp-config", str(path)]
    if model:
        command += ["--model", model]
    return command


def codex_command(prompt: str, project: Path, gregale: str, toolset: str, model: str | None, writes: bool) -> list[str]:
    # --ignore-user-config keeps your own Codex settings and MCP servers out.
    command = ["codex", "exec", "--json", "--ephemeral", "--ignore-user-config", "--skip-git-repo-check", "-C", str(project), "-s", "workspace-write",
               "-c", "sandbox_workspace_write.network_access=true", "-c", "shell_environment_policy.inherit=all"]
    if toolset != "none":
        command += ["-c", f"mcp_servers.gregale.command={json.dumps(gregale)}",
                    "-c", f"mcp_servers.gregale.args={json.dumps(mcp_args(toolset, writes))}",
                    "-c", 'mcp_servers.gregale.env_vars=["FAAS_TOKEN","FAAS_API"]']
    if model:
        command += ["-m", model]
    return command + [prompt]


def gemini_command(prompt: str, run_dir: Path, gregale: str, toolset: str, model: str | None, writes: bool) -> tuple[list[str], dict]:
    """Gemini CLI headless, and the environment it needs. MCP servers come from
    a settings file the run points GEMINI_CLI_SYSTEM_SETTINGS_PATH at."""
    command = ["gemini", "-p", prompt, "--output-format", "stream-json", "--approval-mode", "yolo", "--skip-trust"]
    env = {}
    if toolset != "none":
        path = run_dir / "gemini-settings.json"
        path.write_text(json.dumps({"mcpServers": {"gregale": {"command": gregale, "args": mcp_args(toolset, writes), "trust": True}}}))
        env["GEMINI_CLI_SYSTEM_SETTINGS_PATH"] = str(path)
        command += ["--allowed-mcp-server-names", "gregale"]
    if model:
        command += ["-m", model]
    return command, env


def cursor_command(prompt: str, project: Path, gregale: str, toolset: str, model: str | None, writes: bool) -> list[str]:
    """cursor-agent headless; MCP servers come from the workspace's .cursor/mcp.json."""
    command = ["cursor-agent", "-p", prompt, "--output-format", "stream-json", "--force", "--trust", "--workspace", str(project)]
    if toolset != "none":
        config = project / ".cursor" / "mcp.json"
        config.parent.mkdir(parents=True, exist_ok=True)
        config.write_text(json.dumps({"mcpServers": {"gregale": {"command": gregale, "args": mcp_args(toolset, writes)}}}))
        command += ["--approve-mcps"]
    if model:
        command += ["--model", model]
    return command


def agent_ready(agent: str) -> str:
    """Why an installed agent cannot run headless here, or "" when it can."""
    if agent == "gemini" and not any(os.environ.get(k) for k in ("GEMINI_API_KEY", "GOOGLE_API_KEY", "GOOGLE_GENAI_USE_VERTEXAI")):
        return "gemini needs GEMINI_API_KEY (or GOOGLE_API_KEY / Vertex AI); the personal Code Assist login no longer serves this client"
    if agent == "cursor" and not os.environ.get("CURSOR_API_KEY"):
        status = subprocess.run(["cursor-agent", "status"], capture_output=True, text=True, timeout=60, stdin=subprocess.DEVNULL)
        # A stale login still prints "Logged in (unable to fetch user details)"
        # while headless runs answer "Authentication required" (2026-11-12).
        if status.returncode != 0 or re.search(r"not (logged|authenticated)|login required|unable to fetch user details",
                                               status.stdout + status.stderr, re.IGNORECASE):
            return "cursor-agent is not logged in: run `cursor-agent login` or set CURSOR_API_KEY"
    return ""


# --- transcript parsing -----------------------------------------------------


def _events(lines):
    for line in lines:
        line = line.strip()
        if not line.startswith("{"):
            continue
        try:
            yield json.loads(line)
        except json.JSONDecodeError:
            continue


def _metrics(cost) -> dict:
    return {"completed": False, "turns": 0, "cost_usd": cost, "input_tokens": 0, "output_tokens": 0,
            "cache_read_tokens": 0, "cache_write_tokens": 0, "tool_calls": {}, "commands": [], "answer": "", "error": ""}


def _count(metrics: dict, name: str) -> None:
    metrics["tool_calls"][name] = metrics["tool_calls"].get(name, 0) + 1


def parse_claude(lines) -> dict:
    """Metrics from `claude -p --output-format stream-json` lines."""
    metrics = _metrics(cost=0.0)
    for event in _events(lines):
        kind = event.get("type")
        if kind == "assistant":
            for block in (event.get("message") or {}).get("content") or []:
                if block.get("type") == "tool_use":
                    name = block.get("name", "?")
                    _count(metrics, name)
                    command = (block.get("input") or {}).get("command")
                    if name == "Bash" and command:
                        metrics["commands"].append(command)
                elif block.get("type") == "text":
                    metrics["answer"] = block.get("text", "")
        elif kind == "result":
            metrics["completed"] = event.get("subtype") == "success" and not event.get("is_error")
            metrics["turns"] = event.get("num_turns", 0)
            metrics["cost_usd"] = float(event.get("total_cost_usd") or 0)
            if event.get("result"):
                metrics["answer"] = event["result"]
            if not metrics["completed"]:
                metrics["error"] = "; ".join(event.get("errors") or []) or event.get("subtype", "error")
            # Per-model usage is complete even when the run stops early.
            for usage in (event.get("modelUsage") or {}).values():
                metrics["input_tokens"] += usage.get("inputTokens", 0)
                metrics["output_tokens"] += usage.get("outputTokens", 0)
                metrics["cache_read_tokens"] += usage.get("cacheReadInputTokens", 0)
                metrics["cache_write_tokens"] += usage.get("cacheCreationInputTokens", 0)
    return metrics


def parse_codex(lines) -> dict:
    """Metrics from `codex exec --json` lines."""
    metrics = _metrics(cost=None)
    failed = False
    for event in _events(lines):
        kind = event.get("type")
        if kind == "turn.completed":
            metrics["turns"] += 1
            usage = event.get("usage") or {}
            metrics["input_tokens"] += usage.get("input_tokens", 0)
            metrics["output_tokens"] += usage.get("output_tokens", 0)
            metrics["cache_read_tokens"] += usage.get("cached_input_tokens", 0)
        elif kind in ("turn.failed", "error"):
            failed = True
            message = event.get("message") or (event.get("error") or {}).get("message", "")
            metrics["error"] = message[:500]
        elif kind == "item.completed":
            item = event.get("item") or {}
            if item.get("type") == "agent_message":
                metrics["answer"] = item.get("text", "")
            elif item.get("type") == "mcp_tool_call":
                _count(metrics, f"mcp__{item.get('server', '?')}__{item.get('tool', '?')}")
            elif item.get("type") == "command_execution":
                _count(metrics, "shell")
                if item.get("command"):
                    metrics["commands"].append(item["command"])
            elif item.get("type") == "file_change":
                _count(metrics, "file_change")
    metrics["completed"] = metrics["turns"] > 0 and not failed
    return metrics


def parse_gemini(lines) -> dict:
    """Metrics from `gemini -p --output-format stream-json` lines (Gemini CLI 0.45).

    Gemini reports no turn count; turns are tool calls plus the final answer."""
    metrics = _metrics(cost=None)
    answer, saw_result = [], False
    for event in _events(lines):
        kind = event.get("type")
        if kind == "message" and event.get("role") == "assistant":
            answer.append(event.get("content") or "")
        elif kind == "tool_use":
            answer = []
            name = event.get("tool_name", "?")
            _count(metrics, name)
            command = (event.get("parameters") or {}).get("command")
            if name == "run_shell_command" and command:
                metrics["commands"].append(command)
        elif kind == "error":
            metrics["error"] = str(event.get("message") or event.get("error") or "error")[:500]
        elif kind == "result":
            saw_result = True
            metrics["completed"] = event.get("status") == "success"
            if not metrics["completed"]:
                metrics["error"] = str((event.get("error") or {}).get("message") or metrics["error"] or "error")[:500]
            stats = event.get("stats") or {}
            metrics["cache_read_tokens"] = stats.get("cached", 0)
            metrics["input_tokens"] = max(stats.get("input_tokens", 0) - metrics["cache_read_tokens"], 0)
            metrics["output_tokens"] = stats.get("output_tokens", 0)
    metrics["turns"] = sum(metrics["tool_calls"].values()) + 1 if saw_result else 0
    metrics["answer"] = "".join(answer).strip()
    return metrics


def parse_cursor(lines) -> dict:
    """Metrics from `cursor-agent -p --output-format stream-json` lines (2026.04.17)."""
    metrics = _metrics(cost=None)
    model_calls = set()
    for event in _events(lines):
        kind = event.get("type")
        if event.get("model_call_id"):
            model_calls.add(event["model_call_id"])
        if kind == "assistant":
            for block in (event.get("message") or {}).get("content") or []:
                if block.get("type") == "text":
                    metrics["answer"] = block.get("text", "")
        elif kind == "tool_call" and event.get("subtype") == "started":
            for name, body in (event.get("tool_call") or {}).items():
                _count(metrics, name)
                command = ((body or {}).get("args") or {}).get("command") if isinstance(body, dict) else None
                if name == "shellToolCall" and command:
                    metrics["commands"].append(command)
        elif kind == "result":
            metrics["completed"] = event.get("subtype") == "success" and not event.get("is_error")
            if event.get("result"):
                metrics["answer"] = event["result"]
            if not metrics["completed"]:
                metrics["error"] = str(event.get("result") or event.get("subtype") or "error")[:500]
            usage = event.get("usage") or {}
            metrics["input_tokens"] = usage.get("inputTokens", 0)
            metrics["output_tokens"] = usage.get("outputTokens", 0)
            metrics["cache_read_tokens"] = usage.get("cacheReadTokens", 0)
            metrics["cache_write_tokens"] = usage.get("cacheWriteTokens", 0)
    metrics["turns"] = len(model_calls)
    return metrics


PARSERS = {"claude": parse_claude, "codex": parse_codex, "gemini": parse_gemini, "cursor": parse_cursor}


# --- Gregale access ---------------------------------------------------------


class Gregale:
    """The evaluation account, reached only with GREGALE_EVAL_TOKEN."""

    def __init__(self, binary: str, api: str, token: str):
        self.binary, self.api, self.token = binary, api.rstrip("/"), token

    def env(self) -> dict:
        env = {k: v for k, v in os.environ.items() if not k.startswith("FAAS_") and k not in AGENT_MARKERS}
        env.update(FAAS_TOKEN=self.token, FAAS_API=self.api)
        return env

    def cli(self, *args: str, cwd: Path | None = None, timeout: int = 900) -> subprocess.CompletedProcess:
        return subprocess.run([self.binary, "--non-interactive", "--json", *args], cwd=cwd, env=self.env(),
                              stdin=subprocess.DEVNULL, capture_output=True, text=True, timeout=timeout)

    def get(self, path: str):
        request = urllib.request.Request(self.api + path, headers={"Authorization": f"Bearer {self.token}"})
        with urllib.request.urlopen(request, timeout=30) as response:
            return json.loads(response.read())

    def inventory(self) -> dict:
        """Slugs of the account's apps and projects."""
        def slugs(path):
            body = self.get(path)
            items = body if isinstance(body, list) else (body.get("items") or body.get("apps") or body.get("projects") or [])
            return {item["slug"] for item in items if isinstance(item, dict) and item.get("slug")}
        return {"apps": slugs("/v1/apps"), "projects": slugs("/v1/projects")}


    def touched_since(self, before: dict, started_at: str) -> dict:
        """Apps and projects the run created. A dedicated account holds nothing else."""
        return created_since(before, self.inventory())


def _json_output(text: str):
    """One JSON document, or JSON lines (list output) as a list."""
    try:
        return json.loads(text)
    except json.JSONDecodeError:
        return [json.loads(line) for line in text.splitlines() if line.strip().startswith("{")]


def _rows(value) -> list:
    """List output as rows: a JSON array, an {"items": [...]} page, or one object."""
    if isinstance(value, list):
        return value
    if isinstance(value, dict):
        return value["items"] if isinstance(value.get("items"), list) else [value]
    return []


class CliGregale(Gregale):
    """The caller's own logged-in CLI profile (`--cli-login`).

    Reads go through `gregale --json` with the caller's credentials; nothing is
    deleted. The account may hold other apps, so an app is the run's when it is
    new or got a release after the run started. Apps another session deploys at
    the same time are attributed to the run too; run when the account is quiet."""

    ROUTES = (
        (re.compile(r"^/v1/apps$"), lambda m: ["app", "list"]),
        (re.compile(r"^/v1/projects$"), lambda m: ["project", "list"]),
        (re.compile(r"^/v1/apps/([a-z0-9-]+)$"), lambda m: ["app", "get", m[1]]),
        (re.compile(r"^/v1/apps/([a-z0-9-]+)/health$"), lambda m: ["app", "health", m[1]]),
        (re.compile(r"^/v1/apps/([a-z0-9-]+)/deployments(\?.*)?$"), lambda m: ["release", "list", "--app", m[1], "--limit", "20"]),
    )

    def __init__(self, binary: str):
        super().__init__(binary, "", "")

    def env(self) -> dict:
        return {k: v for k, v in os.environ.items() if k not in AGENT_MARKERS}

    def get(self, path: str):
        for pattern, args in self.ROUTES:
            match = pattern.match(path)
            if match:
                done = self.cli(*args(match), timeout=120)
                if done.returncode != 0:
                    raise ValueError(f"gregale {' '.join(args(match))}: {done.stderr.strip()[-300:] or done.stdout.strip()[-300:]}")
                return _json_output(done.stdout)
        raise ValueError(f"no CLI read for {path}")

    def inventory(self) -> dict:
        apps = _rows(self.get("/v1/apps"))
        projects = _rows(self.get("/v1/projects"))
        return {"apps": {a["slug"] for a in apps if a.get("slug")}, "projects": {p["slug"] for p in projects if p.get("slug")},
                "app_ids": {a["id"]: a["slug"] for a in apps if a.get("id") and a.get("slug")}}

    def touched_since(self, before: dict, started_at: str) -> dict:
        after = self.inventory()
        touched = created_since(before, after)
        done = self.cli("release", "list", "--all", "--limit", "200", timeout=120)
        releases = _rows(_json_output(done.stdout)) if done.returncode == 0 else []
        redeployed = {after["app_ids"].get(r.get("app_id")) for r in releases if (r.get("created_at") or "") >= started_at}
        touched["apps"] = sorted(set(touched["apps"]) | {slug for slug in redeployed if slug})
        return touched


def created_since(before: dict, after: dict) -> dict:
    return {kind: sorted(after.get(kind, set()) - before.get(kind, set())) for kind in ("apps", "projects")}


def http_check(url: str, check: dict, deadline_s: float = 120, fetch=None) -> tuple[bool, str]:
    """GET url, retrying through a cold wake until the expected response or the deadline."""
    fetch = fetch or _fetch
    deadline, last = time.monotonic() + deadline_s, ""
    while True:
        status, body = fetch(url)
        if status == check.get("status", 200) and check.get("contains", "") in body:
            return True, f"{status}"
        last = f"{status}: {body[:120]!r}"
        if time.monotonic() >= deadline:
            return False, last
        time.sleep(5)


def _fetch(url: str) -> tuple[int, str]:
    try:
        with urllib.request.urlopen(url, timeout=30) as response:
            return response.status, response.read(65536).decode("utf-8", "replace")
    except urllib.error.HTTPError as error:
        return error.code, error.read(65536).decode("utf-8", "replace")
    except (urllib.error.URLError, TimeoutError, ConnectionError) as error:
        return 0, str(error)


def _app_url(app: dict) -> str:
    return (app.get("canonical_url") or app.get("url") or "").rstrip("/")


def _app_evidence(gregale, slug: str, since: str = "") -> str:
    """The app's park and its deployments since the run started, as text for
    trap codes. Older parks and deployments belong to someone else's run."""
    parts = []
    try:
        parked = gregale.get(f"/v1/apps/{slug}").get("parked_deployment") or {}
        if (parked.get("parked_at") or "") >= since:
            parts.append(json.dumps(parked))
        for deployment in _rows(gregale.get(f"/v1/apps/{slug}/deployments?limit=20")):
            if (deployment.get("created_at") or "") >= since:
                parts.append(json.dumps(deployment))
    except (OSError, ValueError, AttributeError, urllib.error.URLError) as error:
        parts.append(f"unreadable {slug}: {error}")
    return "\n".join(parts)


def run_checks(task: dict, slug: str, project: Path, answer: str, gregale, fetch=None, http_deadline_s: float = 120, created: dict | None = None,
               since: str = "") -> list[dict]:
    created_apps = (created or {}).get("apps", [])
    results = []
    for check in task["checks"]:
        kind, ok, detail = check["kind"], False, ""
        try:
            if kind == "answer":
                ok = re.search(check["pattern"], answer or "", re.IGNORECASE) is not None
                detail = (answer or "")[:200]
            elif kind == "file_contains":
                ok = check["text"] in (project / check["file"]).read_text()
            elif kind == "http":
                url = _app_url(gregale.get(f"/v1/apps/{slug}")) + check.get("path", "/")
                ok, detail = http_check(url, check, deadline_s=http_deadline_s, fetch=fetch)
            elif kind == "serving":
                health = gregale.get(f"/v1/apps/{slug}/health")
                serving, latest = health.get("serving_deployment_ids") or [], health.get("latest_deployment_id", "")
                ok = bool(serving) and (latest not in serving if check["expect"] == "not_latest" else latest in serving)
                detail = f"serving={serving} latest={latest}"
            elif kind == "apps_created":
                ok = len(created_apps) >= check.get("min", 1)
                detail = ",".join(created_apps)
            elif kind == "apps_serving":
                idle = [app for app in created_apps if not gregale.get(f"/v1/apps/{app}/health").get("serving_deployment_ids")]
                ok, detail = bool(created_apps) and not idle, f"not serving: {','.join(idle)}" if idle else f"{len(created_apps)} serving"
            elif kind == "app_http":
                matches = [app for app in created_apps if re.search(check["match"], app)]
                urls = [_app_url(gregale.get(f"/v1/apps/{app}")) for app in matches]
                urls = [url for url in urls if url]
                if not urls:
                    detail = f"no created public app matches {check['match']!r}"
                else:
                    ok, detail = http_check(urls[0] + check.get("path", "/"), check, deadline_s=http_deadline_s, fetch=fetch)
            elif kind == "no_traps":
                codes = check.get("codes") or list(DEFAULT_TRAPS)
                hit = sorted({f"{app}:{code}" for app in created_apps for code in codes if code in _app_evidence(gregale, app, since)})
                ok, detail = not hit, ",".join(hit)
        except (OSError, KeyError, ValueError, urllib.error.URLError) as error:
            detail = f"check error: {error}"
        results.append({"kind": kind, "ok": ok, "detail": detail})
    return results


# --- how the agent worked ---------------------------------------------------


def _project_files_text(project: Path, glob: str, limit: int = 2_000_000) -> str:
    text, total = [], 0
    for path in sorted(project.glob(glob)):
        if path.is_file() and ".git" not in path.parts:
            body = path.read_text(errors="replace")[:200_000]
            total += len(body)
            if total > limit:
                break
            text.append(body)
    return "\n".join(text)


def grade_features(task: dict, commands: list[str], project: Path, created: dict) -> dict:
    """Which of the task's applicable Gregale features the run used."""
    used, missed = [], []
    joined = "\n".join(commands)
    for feature in task.get("features", []):
        if feature["kind"] == "command":
            hit = re.search(feature["pattern"], joined, re.MULTILINE) is not None
        elif feature["kind"] == "file":
            hit = re.search(feature["pattern"], _project_files_text(project, feature.get("glob", "**/*")), re.MULTILINE) is not None
        else:  # state
            hit = bool(created.get("projects"))
        (used if hit else missed).append(feature["id"])
    total = len(used) + len(missed)
    return {"features_used": used, "features_missed": missed, "feature_score": round(len(used) / total, 2) if total else None}


def grade_anti_patterns(commands: list[str]) -> list[str]:
    found = [name for name, pattern in ANTI_PATTERNS.items() if any(pattern.search(command) for command in commands)]
    counts: dict[str, int] = {}
    for command in commands:
        key = " ".join(command.split())
        counts[key] = counts.get(key, 0) + 1
    if any(count > REPEATED_COMMAND_LIMIT for count in counts.values()):
        found.append("repeated_command")
    return found


# --- runs -------------------------------------------------------------------


def agent_command(agent: str, prompt: str, task: dict, run_dir: Path, project: Path, gregale: Gregale, toolset: str, options) -> tuple[list[str], dict]:
    model = getattr(options, f"{agent}_model", None)
    if agent == "claude":
        return claude_command(prompt, run_dir, gregale.binary, toolset, task["budget_usd"], model, options.mcp_writes, any_shell=bool(task.get("repo"))), {}
    if agent == "codex":
        return codex_command(prompt, project, gregale.binary, toolset, model, options.mcp_writes), {}
    if agent == "gemini":
        return gemini_command(prompt, run_dir, gregale.binary, toolset, model, options.mcp_writes)
    return cursor_command(prompt, project, gregale.binary, toolset, model, options.mcp_writes), {}


def cleanup(gregale: Gregale, created: dict, slug: str | None) -> list[str]:
    """Delete what the run created; returns what could not be deleted."""
    left = []
    for project_slug in created.get("projects", []):
        if gregale.cli("project", "delete", "--yes", "--detach-services", project_slug, timeout=300).returncode != 0:
            left.append(f"project:{project_slug}")
    for app in sorted(set(created.get("apps", [])) | ({slug} if slug else set())):
        if gregale.cli("app", "delete", "--yes", "--delete-bindings", app, timeout=300).returncode != 0:
            left.append(f"app:{app}")
    return left


def run_one(task: dict, agent: str, toolset: str, gregale: Gregale, options) -> dict:
    slug = None if task.get("repo") else app_slug(task["id"])
    run_dir = Path(tempfile.mkdtemp(prefix=f"agent-eval-{task['id']}-"))
    project = run_dir / "project"
    record = {"task": task["id"], "agent": agent, "toolset": toolset, "app": slug, "started_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())}
    created: dict = {"apps": [], "projects": []}
    before = None
    try:
        before = gregale.inventory()
        if task.get("repo"):
            checkout(task["repo"]["url"], task["repo"]["commit"], project)
        else:
            init = gregale.cli("init", "--template", task["template"], "--path", str(project), "--name", slug)
            if init.returncode != 0:
                raise EvalError(f"init failed: {init.stderr[-300:]}")
        for step in task.get("setup", []):
            if step["kind"] == "replace":
                replace_once(project / step["file"], step["old"], step["new"])
            elif step["kind"] == "deploy":
                result = gregale.cli("deploy", "--path", str(project), "--name", slug, "--app", "--yes", cwd=project, timeout=1200)
                if (result.returncode == 0) != (step.get("expect", "success") == "success"):
                    raise EvalError(f"setup deploy exit {result.returncode}: {result.stderr[-300:]}")
        command, extra_env = agent_command(agent, task_prompt(task, slug, toolset), task, run_dir, project, gregale, toolset, options)
        env = {**gregale.env(), **extra_env}
        # The agent's shell `gregale` must be the binary under test.
        (run_dir / "bin").mkdir()
        (run_dir / "bin" / "gregale").symlink_to(Path(gregale.binary).resolve())
        env["PATH"] = str(run_dir / "bin") + os.pathsep + env.get("PATH", "")
        record["isolation"] = "user-config"
        if agent == "claude" and env.get("ANTHROPIC_API_KEY"):
            # A fresh config keeps your settings, memory and plugins out of the run.
            env["CLAUDE_CONFIG_DIR"] = str(run_dir / "claude-config")
            record["isolation"] = "fresh-config"
        elif agent == "gemini" and env.get("GEMINI_API_KEY"):
            env["GEMINI_CLI_HOME"] = str(run_dir / "gemini-home")
            record["isolation"] = "fresh-config"
        started = time.monotonic()
        try:
            done = subprocess.run(command, cwd=project, env=env, stdin=subprocess.DEVNULL, capture_output=True, timeout=task["timeout_s"])
            lines, record["timed_out"] = _text(done.stdout).splitlines(), False
            record["agent_stderr"] = _text(done.stderr)[-500:]
        except subprocess.TimeoutExpired as expired:
            lines, record["timed_out"] = _text(expired.stdout).splitlines(), True
        record["duration_s"] = round(time.monotonic() - started, 1)
        (run_dir / f"{agent}.jsonl").write_text("\n".join(lines))
        metrics = PARSERS[agent](lines)
        record.update(metrics)
        created = gregale.touched_since(before, record["started_at"])
        record["created"] = created
        record["checks"] = run_checks(task, slug, project, metrics["answer"], gregale, created=created, since=record["started_at"])
        record["success"] = all(check["ok"] for check in record["checks"])
        record.update(grade_features(task, metrics["commands"], project, created))
        record["anti_patterns"] = grade_anti_patterns(metrics["commands"])
        record["traps"] = [c["detail"] for c in record["checks"] if c["kind"] == "no_traps" and not c["ok"]]
    except (EvalError, subprocess.TimeoutExpired, OSError, ValueError, urllib.error.URLError) as error:
        record.update(success=False, error=str(error))
    finally:
        if not options.keep_apps:
            if before is not None and not created.get("apps") and not created.get("projects"):
                try:
                    created = gregale.touched_since(before, record["started_at"])
                except (OSError, ValueError, urllib.error.URLError):
                    pass
            record["left_behind"] = cleanup(gregale, created, slug)
        if not options.keep_dirs:
            shutil.rmtree(run_dir, ignore_errors=True)
        else:
            record["run_dir"] = str(run_dir)
    record["answer"] = (record.get("answer") or "")[:500]
    return record


def summarize(records: list[dict]) -> str:
    groups: dict[tuple, list[dict]] = {}
    for record in records:
        groups.setdefault((record["task"], record["agent"], record["toolset"]), []).append(record)
    rows = ["| task | agent | toolset | runs | success | turns (median) | tool calls (median) | input tokens (median) | cost USD (mean) | time s (median) | features (mean) | anti-patterns | traps |",
            "|---|---|---|---|---|---|---|---|---|---|---|---|---|"]
    for (task, agent, toolset), runs in sorted(groups.items()):
        def median(key):
            values = [r[key] for r in runs if isinstance(r.get(key), (int, float))]
            return round(statistics.median(values), 1) if values else "—"
        costs = [r["cost_usd"] for r in runs if isinstance(r.get("cost_usd"), (int, float))]
        calls = [sum((r.get("tool_calls") or {}).values()) for r in runs]
        tokens = [r.get("input_tokens", 0) + r.get("cache_read_tokens", 0) + r.get("cache_write_tokens", 0) for r in runs]
        success = sum(1 for r in runs if r.get("success"))
        scores = [r["feature_score"] for r in runs if isinstance(r.get("feature_score"), (int, float))]
        patterns: dict[str, int] = {}
        for r in runs:
            for name in r.get("anti_patterns") or []:
                patterns[name] = patterns.get(name, 0) + 1
        traps = sum(1 for r in runs if r.get("traps"))
        rows.append(f"| {task} | {agent} | {toolset} | {len(runs)} | {success}/{len(runs)} | {median('turns')} | "
                    f"{statistics.median(calls) if calls else '—'} | {statistics.median(tokens) if tokens else '—'} | "
                    f"{round(statistics.mean(costs), 3) if costs else '—'} | {median('duration_s')} | "
                    f"{round(statistics.mean(scores), 2) if scores else '—'} | "
                    f"{', '.join(f'{k}×{v}' for k, v in sorted(patterns.items())) or '—'} | {traps}/{len(runs)} |")
    return "\n".join(rows)


def _csv(value: str, allowed: tuple, name: str) -> list[str]:
    items = [item.strip() for item in value.split(",") if item.strip()]
    unknown = [item for item in items if item not in allowed]
    if not items or unknown:
        raise EvalError(f"--{name} must list {', '.join(allowed)}; got {value!r}")
    return items


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest="command", required=True)
    sub.add_parser("list", help="list tasks")
    run = sub.add_parser("run", help="run agents against tasks (deploys real apps)")
    run.add_argument("--agents", default="claude")
    run.add_argument("--toolsets", default="core,all")
    run.add_argument("--tasks", default="")
    run.add_argument("--repeat", type=int, default=1)
    run.add_argument("--out", default="agent-eval-results.jsonl")
    run.add_argument("--gregale", default=shutil.which("gregale") or "gregale", help="gregale binary under test")
    run.add_argument("--claude-model")
    run.add_argument("--codex-model")
    run.add_argument("--gemini-model")
    run.add_argument("--cursor-model")
    run.add_argument("--no-mcp-writes", dest="mcp_writes", action="store_false", help="serve MCP without --allow-writes")
    run.add_argument("--keep-apps", action="store_true", help="do not delete the apps and projects the run created")
    run.add_argument("--keep-dirs", action="store_true", help="keep run directories and transcripts")
    run.add_argument("--cli-login", action="store_true", help="use your own logged-in gregale CLI instead of GREGALE_EVAL_TOKEN; implies --keep-apps")
    summary = sub.add_parser("summarize", help="print a Markdown summary of a results file")
    summary.add_argument("results")
    options = parser.parse_args(argv)
    try:
        tasks = load_tasks()
        if options.command == "list":
            for task in tasks:
                print(f"{task['id']:22} {task['title']}")
            return 0
        if options.command == "summarize":
            records = [json.loads(line) for line in Path(options.results).read_text().splitlines() if line.strip()]
            print(summarize(records))
            return 0
        token = os.environ.get("GREGALE_EVAL_TOKEN", "")
        if not token and not options.cli_login:
            raise EvalError("set GREGALE_EVAL_TOKEN to an API key of a dedicated evaluation account; runs deploy and delete real apps "
                            "(or pass --cli-login to use your own logged-in CLI, which never deletes)")
        agents = _csv(options.agents, AGENTS, "agents")
        toolsets = _csv(options.toolsets, TOOLSETS, "toolsets")
        selected = [t for t in tasks if not options.tasks or t["id"] in options.tasks.split(",")]
        if not selected or options.repeat < 1:
            raise EvalError("no tasks selected, or --repeat below 1")
        for agent in agents:
            if not shutil.which(AGENT_BINARIES[agent]):
                raise EvalError(f"{agent} is not installed ({AGENT_BINARIES[agent]} not on PATH)")
            problem = agent_ready(agent)
            if problem:
                raise EvalError(problem)
        if options.cli_login:
            gregale: Gregale = CliGregale(options.gregale)
            options.keep_apps = True
        else:
            gregale = Gregale(options.gregale, os.environ.get("GREGALE_EVAL_API", DEFAULT_API), token)
        with open(options.out, "a") as out:
            for task in selected:
                for agent in agents:
                    for toolset in toolsets:
                        for _ in range(options.repeat):
                            record = run_one(task, agent, toolset, gregale, options)
                            out.write(json.dumps(record) + "\n")
                            out.flush()
                            print(f"{task['id']:22} {agent:6} {toolset:8} success={record.get('success')} "
                                  f"turns={record.get('turns')} cost={record.get('cost_usd')} features={record.get('feature_score')} "
                                  f"anti={record.get('anti_patterns')}", file=sys.stderr)
        print(summarize([json.loads(line) for line in Path(options.out).read_text().splitlines() if line.strip()]))
        return 0
    except EvalError as error:
        print(f"agent-eval: {error}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    sys.exit(main())
