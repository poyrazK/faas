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
every app and project the run created, and appends one JSON line per run. It
also reports files the agent wrote outside its run directory.

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
import hashlib
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
CHECK_KINDS = ("http", "serving", "file_contains", "answer", "apps_created", "apps_serving", "app_http", "no_traps", "owner_response")
FEATURE_KINDS = ("command", "file", "state")
FEATURE_STATES = ("project_created",)
DEFAULT_API = "https://api.gregale.dev"
# Failure codes that mean the agent walked into a known platform trap.
DEFAULT_TRAPS = ("liveness_exhausted", "app_startup_timeout")
# App statuses that mean parked (the API's and the CLI's spelling).
PARKED_STATUSES = ("evicted_cold", "parked")
# Conditions under which a feature is not needed (see grade_features).
FEATURE_UNLESS = ("first_deploy_live",)
# Ways of working around the CLI that count against a run.
ANTI_PATTERNS = {
    "raw_api_call": re.compile(r"\b(curl|wget|https?|xh)\b[^\n]*(api\.gregale\.dev|/v1/(apps|projects|deployments))"),
    "manual_tarball": re.compile(r"\btar\s+(-\S*c|c\S*\s)"),
    # Reading `app delete --help` is not a deletion (2026-11-15 run).
    "app_delete": re.compile(r"\bgregale\b[^\n|;&]*\b(app|apps|project)\s+(delete|rm)\b(?![^\n|;&]*\s(--help|-h)\b)"),
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
# It may also read the web: a person's agent can read gregale.dev docs.
CLAUDE_ANY_SHELL = ["Bash", "Read", "Edit", "Write", "Glob", "Grep", "WebFetch", "WebSearch", "mcp__gregale"]
# Agents whose session the eval can resume for a task's follow_up reply.
FOLLOW_UP_AGENTS = ("claude", "codex")


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
            if feature.get("unless") is not None and feature["unless"] not in FEATURE_UNLESS:
                raise EvalError(f"{tid}: feature {feature['id']} has unknown unless {feature['unless']!r}")
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


def claude_command(prompt: str, run_dir: Path, gregale: str, toolset: str, budget: float, model: str | None, writes: bool, any_shell: bool = False,
                   resume: str | None = None) -> list[str]:
    """A headless Claude run. A repo task keeps its session so a follow_up
    reply can resume it; `resume` continues that session."""
    allowed = CLAUDE_ANY_SHELL if any_shell else CLAUDE_ALLOWED_TOOLS
    command = ["claude", "-p", prompt, "--output-format", "stream-json", "--verbose",
               "--max-budget-usd", f"{budget:.2f}", "--permission-mode", "dontAsk", "--allowedTools", *allowed, "--strict-mcp-config"]
    if not any_shell:
        command.insert(5, "--no-session-persistence")
    if resume:
        command += ["--resume", resume]
    if toolset != "none":
        config = {"mcpServers": {"gregale": {"command": gregale, "args": mcp_args(toolset, writes)}}}
        path = run_dir / "mcp.json"
        path.write_text(json.dumps(config))
        command += ["--mcp-config", str(path)]
    if model:
        command += ["--model", model]
    return command


def codex_command(prompt: str, project: Path, gregale: str, toolset: str, model: str | None, writes: bool, keep_session: bool = False,
                  resume: str | None = None) -> list[str]:
    """A headless Codex run. A repo task keeps its session (no --ephemeral) so a
    follow_up reply can resume it; `resume` continues that session. `codex exec
    resume` takes no -C or -s: the sandbox comes from config and the working
    directory is the project the run starts in."""
    # --ignore-user-config keeps your own Codex settings and MCP servers out.
    config = ["-c", "sandbox_workspace_write.network_access=true", "-c", "shell_environment_policy.inherit=all"]
    if toolset != "none":
        config += ["-c", f"mcp_servers.gregale.command={json.dumps(gregale)}",
                   "-c", f"mcp_servers.gregale.args={json.dumps(mcp_args(toolset, writes))}",
                   "-c", 'mcp_servers.gregale.env_vars=["FAAS_TOKEN","FAAS_API"]']
    if model:
        config += ["-m", model]
    if resume:
        return ["codex", "exec", "resume", "--json", "--ignore-user-config", "--skip-git-repo-check", "-c", 'sandbox_mode="workspace-write"', *config,
                resume, prompt]
    session = [] if keep_session else ["--ephemeral"]
    return ["codex", "exec", "--json", *session, "--ignore-user-config", "--skip-git-repo-check", "-C", str(project), "-s", "workspace-write", *config,
            prompt]


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
            "cache_read_tokens": 0, "cache_write_tokens": 0, "tool_calls": {}, "commands": [], "written_paths": [], "tool_inputs": [], "answer": "", "error": "", "session_id": ""}


def merge_metrics(first: dict, second: dict) -> dict:
    """One record for a run and its follow-up: sums, concatenations, the last answer."""
    merged = dict(second)
    for key in ("turns", "input_tokens", "output_tokens", "cache_read_tokens", "cache_write_tokens"):
        merged[key] = (first.get(key) or 0) + (second.get(key) or 0)
    if isinstance(first.get("cost_usd"), (int, float)) or isinstance(second.get("cost_usd"), (int, float)):
        merged["cost_usd"] = (first.get("cost_usd") or 0) + (second.get("cost_usd") or 0)
    merged["tool_calls"] = dict(first.get("tool_calls") or {})
    for name, count in (second.get("tool_calls") or {}).items():
        merged["tool_calls"][name] = merged["tool_calls"].get(name, 0) + count
    merged["commands"] = list(first.get("commands") or []) + list(second.get("commands") or [])
    merged["written_paths"] = list(first.get("written_paths") or []) + list(second.get("written_paths") or [])
    merged["tool_inputs"] = list(first.get("tool_inputs") or []) + list(second.get("tool_inputs") or [])
    merged["first_answer"] = first["first_answer"] if "first_answer" in first else first.get("answer", "")
    merged["session_id"] = second.get("session_id") or first.get("session_id")
    return merged


def _count(metrics: dict, name: str) -> None:
    metrics["tool_calls"][name] = metrics["tool_calls"].get(name, 0) + 1


# Claude tools that write a file; their paths are checked against the run directory.
FILE_WRITE_TOOLS = ("Write", "Edit", "MultiEdit", "NotebookEdit")

# Each agent's own config and memory; its writes there are not the task's.
AGENT_OWN_DIRS = {"claude": (Path.home() / ".claude",), "codex": (Path.home() / ".codex",),
                  "gemini": (Path.home() / ".gemini",), "cursor": (Path.home() / ".cursor",)}


def parse_claude(lines) -> dict:
    """Metrics from `claude -p --output-format stream-json` lines."""
    metrics = _metrics(cost=0.0)
    for event in _events(lines):
        kind = event.get("type")
        if event.get("session_id") and not metrics["session_id"]:
            metrics["session_id"] = event["session_id"]
        if kind == "assistant":
            for block in (event.get("message") or {}).get("content") or []:
                if block.get("type") == "tool_use":
                    name = block.get("name", "?")
                    _count(metrics, name)
                    metrics["tool_inputs"].append(json.dumps(block.get("input") or {}))
                    command = (block.get("input") or {}).get("command")
                    if name == "Bash" and command:
                        metrics["commands"].append(command)
                    path = (block.get("input") or {}).get("file_path") or (block.get("input") or {}).get("notebook_path")
                    if name in FILE_WRITE_TOOLS and path:
                        metrics["written_paths"].append(path)
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
        if kind == "thread.started":
            metrics["session_id"] = event.get("thread_id") or ""
        elif kind == "turn.completed":
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

    Reads go through `gregale --json` with the caller's credentials. The account
    may hold other apps, and other sessions may create apps while the run is
    going, so an app is the run's only when it is new or got a release after the
    run started AND the agent named it: in a command, a tool call or a file of
    the project (see attribute). Cleanup deletes only the run's apps and
    projects that did not exist before the run (see deletable)."""

    ROUTES = (
        (re.compile(r"^/v1/apps$"), lambda m: ["app", "list"]),
        (re.compile(r"^/v1/projects$"), lambda m: ["project", "list"]),
        (re.compile(r"^/v1/postgres$"), lambda m: ["postgres", "list"]),
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
                "databases": self.databases(),
                "app_ids": {a["id"]: a["slug"] for a in apps if a.get("id") and a.get("slug")}}

    def databases(self) -> set:
        """Managed Postgres database names. A run that creates one and leaves it
        filled the account's database limit: the 2026-11-24 voting-app rerun
        found 10/10, most of them earlier runs' databases."""
        try:
            return {d["name"] for d in _rows(self.get("/v1/postgres")) if isinstance(d, dict) and d.get("name")}
        except ValueError:
            return set()

    def touched_since(self, before: dict, started_at: str) -> dict:
        after = self.inventory()
        touched = created_since(before, after)
        done = self.cli("release", "list", "--all", "--limit", "200", timeout=120)
        releases = _rows(_json_output(done.stdout)) if done.returncode == 0 else []
        redeployed = {after["app_ids"].get(r.get("app_id")) for r in releases if (r.get("created_at") or "") >= started_at}
        touched["apps"] = sorted(set(touched["apps"]) | {slug for slug in redeployed if slug})
        return touched


# What a run can create in the account, and what cleanup deletes.
RESOURCE_KINDS = ("apps", "projects", "databases")


def created_since(before: dict, after: dict) -> dict:
    return {kind: sorted(after.get(kind, set()) - before.get(kind, set())) for kind in RESOURCE_KINDS}


# Project files read for the names of the apps a run deploys (Compose service
# names, gregale.yaml), bounded so a large checkout stays cheap.
EVIDENCE_FILE_GLOBS = ("*.yml", "*.yaml", "*.toml", "*.json")
EVIDENCE_MAX_FILES, EVIDENCE_MAX_BYTES = 200, 256 * 1024


def run_evidence(metrics: dict, project: Path) -> str:
    """Text the run's own apps are named in: the agent's commands and tool
    inputs, and the project's config files. Tool results are left out: an
    agent that lists the account sees every other app's name there."""
    parts = list(metrics.get("commands") or []) + list(metrics.get("tool_inputs") or [])
    files = 0
    for pattern in EVIDENCE_FILE_GLOBS:
        for path in sorted(project.rglob(pattern)) if project.is_dir() else []:
            if files >= EVIDENCE_MAX_FILES or ".git" in path.parts or "node_modules" in path.parts:
                continue
            try:
                if path.is_file() and path.stat().st_size <= EVIDENCE_MAX_BYTES:
                    parts.append(path.read_text(errors="replace"))
                    files += 1
            except OSError:
                continue
    return "\n".join(parts)


def _named(slug: str, evidence: str) -> bool:
    return re.search(rf"(?<![a-z0-9-]){re.escape(slug)}(?![a-z0-9-])", evidence) is not None


def attribute(gregale: "Gregale", touched: dict, evidence: str, slug: str | None = None) -> tuple[dict, dict]:
    """Split what changed in the account into the run's and the rest. A
    dedicated eval account has only the run in it; the caller's own account
    (--cli-login) is shared with other sessions, so there an app or project is
    the run's only if the agent named it (or it is the harness's own slug)."""
    if not isinstance(gregale, CliGregale):
        return touched, {kind: [] for kind in RESOURCE_KINDS}
    mine: dict = {kind: [] for kind in RESOURCE_KINDS}
    others: dict = {kind: [] for kind in RESOURCE_KINDS}
    prefixes = _slug_prefixes(evidence)
    for kind in RESOURCE_KINDS:
        for name in touched.get(kind, []):
            named = name == slug or _named(name, evidence) or (kind == "apps" and name.startswith(prefixes))
            (mine if named else others)[kind].append(name)
    return mine, others


def _slug_prefixes(evidence: str) -> tuple:
    """App slug prefixes the agent asked for (`--prefix retail-sample-`): a
    Compose deploy names its apps <prefix><service>, a name the agent never
    typed whole (Codex 2026-11-23, retail-store). Prefixes under four
    characters are ignored so a stray `--prefix a` does not claim the account."""
    found = re.findall(r"--prefix[=\s]+['\"]?([a-z0-9][a-z0-9-]*)", evidence)
    return tuple(sorted({p for p in found if len(p) >= 4}))


def deletable(gregale: "Gregale", before: dict | None, created: dict, slug: str | None, evidence: str = "") -> tuple[dict, str | None]:
    """What cleanup may delete. A dedicated eval account loses everything the
    run touched; the caller's own account (--cli-login) loses only the run's
    apps and projects (see attribute) that did not exist before the run, never
    one it redeployed and never another session's."""
    if not isinstance(gregale, CliGregale):
        return created, slug
    if before is None:
        return {kind: [] for kind in RESOURCE_KINDS}, None
    new, _ = attribute(gregale, created_since(before, gregale.inventory()), evidence, slug)
    return new, slug if slug in new["apps"] else None


# Paths under HOME that agents, shells and the CLI write on their own; a write
# there is not the agent's doing. Relative to HOME.
HOME_WRITE_IGNORE = (".claude", ".claude.json", ".claude.json.backup", "Library", ".cache", ".config/gregale", ".gemini", ".codex",
                     ".cursor", ".zsh_history", ".bash_history", ".zsh_sessions", ".python_history", ".lesshst", ".viminfo",
                     ".Trash", ".DS_Store", ".npm", ".gocache-faas", "go", ".local/state")


def home_writes(home: Path, since: float, run_dir: Path, max_depth: int = 2, limit: int = 20000) -> list[str]:
    """Files under HOME modified after since, outside run_dir, at most max_depth
    levels down (where dotfiles and per-tool config live). Bounded at limit
    entries so a large home stays cheap. Other processes' writes can show up,
    so this is a report, not a verdict."""
    found, seen = [], 0
    run_dir = run_dir.resolve()
    stack = [(home, 0)]
    while stack and seen < limit:
        directory, depth = stack.pop()
        try:
            entries = list(os.scandir(directory))
        except OSError:
            continue
        for entry in entries:
            seen += 1
            path = Path(entry.path)
            rel = path.relative_to(home).as_posix()
            if rel in HOME_WRITE_IGNORE or any(rel.startswith(i + "/") for i in HOME_WRITE_IGNORE):
                continue
            try:
                resolved = path.resolve()
                if resolved == run_dir or run_dir in resolved.parents:
                    continue
                if entry.is_dir(follow_symlinks=False):
                    if depth + 1 < max_depth:
                        stack.append((path, depth + 1))
                elif entry.stat(follow_symlinks=False).st_mtime > since:
                    found.append(rel)
            except OSError:
                continue
    return sorted(found)


def writes_outside(paths: list[str], run_dir: Path, project: Path, ignore: tuple = ()) -> list[str]:
    """File-tool writes that landed outside the run directory. Paths under
    ignore (the agent's own config, such as Claude's auto memory in ~/.claude)
    don't count."""
    run_dir = run_dir.resolve()
    ignore = tuple(Path(i).resolve() for i in ignore)
    outside = []
    for raw in paths:
        path = Path(raw)
        path = (path if path.is_absolute() else project / path).resolve()
        inside = [run_dir, *ignore]
        if not any(path == root or root in path.parents for root in inside):
            outside.append(str(path))
    return sorted(set(outside))


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


def private_http_check(gregale, slug: str, check: dict) -> tuple[bool, str]:
    """Apps are private by default (ADR-20261116120000), so a public GET gets
    401. Ask the latest serving deployment as the owner instead."""
    # The serving deployment, not the latest: after a rollback the latest is
    # the bad release.
    health = gregale.get(f"/v1/apps/{slug}/health")
    latest = (health.get("serving_deployment_ids") or [None])[0]
    if not latest:
        return False, "private app has no serving deployment"
    result = gregale.cli("release", "verify", latest, "--app", slug, "--path", check.get("path", "/"), timeout=90)
    if result.returncode != 0:
        return False, f"private: release verify failed: {(result.stderr or result.stdout)[:160]!r}"
    evidence = json.loads(result.stdout)
    status, body = evidence.get("http_status"), str(evidence.get("body") or "")
    ok = status == check.get("status", 200) and check.get("contains", "") in body
    return ok, f"private {status}: {body[:120]!r}"


def _http_or_private(gregale, slug: str, url: str, check: dict, deadline_s: float, fetch) -> tuple[bool, str]:
    ok, detail = http_check(url, check, deadline_s=deadline_s, fetch=fetch)
    if not ok and detail.startswith("401") and fetch is None:
        return private_http_check(gregale, slug, check)
    return ok, detail


def _fetch(url: str) -> tuple[int, str]:
    try:
        with urllib.request.urlopen(url, timeout=30) as response:
            return response.status, response.read(65536).decode("utf-8", "replace")
    except urllib.error.HTTPError as error:
        return error.code, error.read(65536).decode("utf-8", "replace")
    except (urllib.error.URLError, TimeoutError, ConnectionError) as error:
        return 0, str(error)


def _app_is_serving(gregale, slug: str) -> bool:
    """An app serves when health names a serving deployment. Health does not
    assess workers or jobs unless app_health_workers is on (phase
    "unsupported"); a live worker pool serves its queue, not HTTP, so the app
    record's deployment availability decides."""
    health = gregale.get(f"/v1/apps/{slug}/health")
    if health.get("serving_deployment_ids"):
        return True
    if health.get("phase") == "unsupported":
        return gregale.get(f"/v1/apps/{slug}").get("deployment_availability") == "live"
    return False


def _app_url(app: dict) -> str:
    return (app.get("canonical_url") or app.get("url") or "").rstrip("/")


def _app_final_evidence(gregale, slug: str, since: str = "") -> str:
    """The app's state at the end of the run, as text for trap codes: its
    park, if it is still parked from this run, and its latest deployment since
    the run started. A failed release the agent replaced with a working one
    was the agent experimenting, not a trap it was left in (voting-app,
    2026-11-23: a deliberate HEALTHCHECK experiment failed the run)."""
    parts = []
    try:
        app = gregale.get(f"/v1/apps/{slug}")
        parked = app.get("parked_deployment") or {}
        if app.get("status") in PARKED_STATUSES and (parked.get("parked_at") or "") >= since:
            parts.append(json.dumps(parked))
        recent = [d for d in _rows(gregale.get(f"/v1/apps/{slug}/deployments?limit=20")) if (d.get("created_at") or "") >= since]
        if recent:
            parts.append(json.dumps(max(recent, key=lambda d: d.get("created_at") or "")))
    except (OSError, ValueError, AttributeError, urllib.error.URLError) as error:
        parts.append(f"unreadable {slug}: {error}")
    return "\n".join(parts)


def first_deploy_live(gregale, apps: list[str], since: str = "") -> bool:
    """Whether every app the run created went live on its first deployment
    (`live` now, or `superseded` by a later one)."""
    if not apps:
        return False
    try:
        for slug in apps:
            recent = [d for d in _rows(gregale.get(f"/v1/apps/{slug}/deployments?limit=50")) if (d.get("created_at") or "") >= since]
            if not recent or min(recent, key=lambda d: d.get("created_at") or "").get("status") not in ("live", "superseded"):
                return False
    except (OSError, ValueError, AttributeError, urllib.error.URLError):
        return False
    return True


def run_checks(task: dict, slug: str, project: Path, answer: str, gregale, fetch=None, http_deadline_s: float = 120, created: dict | None = None,
               since: str = "", expected_app_id: str | None = None) -> list[dict]:
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
                ok, detail = _http_or_private(gregale, slug, url, check, http_deadline_s, fetch)
            elif kind == "owner_response":
                ok, detail = owner_response_check(gregale, slug, check, expected_app_id)
            elif kind == "serving":
                health = gregale.get(f"/v1/apps/{slug}/health")
                serving, latest = health.get("serving_deployment_ids") or [], health.get("latest_deployment_id", "")
                ok = bool(serving) and (latest not in serving if check["expect"] == "not_latest" else latest in serving)
                detail = f"serving={serving} latest={latest}"
            elif kind == "apps_created":
                ok = len(created_apps) >= check.get("min", 1)
                detail = ",".join(created_apps)
            elif kind == "apps_serving":
                idle = [app for app in created_apps if not _app_is_serving(gregale, app)]
                ok, detail = bool(created_apps) and not idle, f"not serving: {','.join(idle)}" if idle else f"{len(created_apps)} serving"
            elif kind == "app_http":
                matches = [app for app in created_apps if re.search(check["match"], app)]
                pairs = [(app, _app_url(gregale.get(f"/v1/apps/{app}"))) for app in matches]
                pairs = [(app, url) for app, url in pairs if url]
                if not pairs:
                    detail = f"no created public app matches {check['match']!r}"
                else:
                    app, url = pairs[0]
                    ok, detail = _http_or_private(gregale, app, url + check.get("path", "/"), check, http_deadline_s, fetch)
            elif kind == "no_traps":
                codes = check.get("codes") or list(DEFAULT_TRAPS)
                hit = sorted({f"{app}:{code}" for app in created_apps for code in codes if code in _app_final_evidence(gregale, app, since)})
                ok, detail = not hit, ",".join(hit)
        except (OSError, KeyError, ValueError, subprocess.TimeoutExpired, urllib.error.URLError) as error:
            detail = f"check error: {error}"
        results.append({"kind": kind, "ok": ok, "detail": detail})
    return results


def owner_response_check(gregale, slug: str, check: dict, expected_app_id: str | None = None) -> tuple[bool, str]:
    """Grade a private app using the exact latest deployment, never a public URL.

    The grader's own check is evidence of the final outcome, not a timestamp of
    when the agent first verified it. It never records the private response body.
    """
    app = gregale.get(f"/v1/apps/{slug}")
    if expected_app_id is not None and app.get("id") != expected_app_id:
        return False, "the task replaced the original app identity"
    if not app.get("require_authn"):
        return False, "app did not retain required authentication"
    rows = _rows(gregale.get(f"/v1/apps/{slug}/deployments?limit=20"))
    release_ids = {row.get("id") for row in rows if row.get("id") and row.get("app_id") == app.get("id")}
    if len(release_ids) < check.get("min_deployments", 1):
        return False, "not enough accepted deployments for this task"
    health = gregale.get(f"/v1/apps/{slug}/health")
    latest = health.get("latest_deployment_id")
    if latest not in (health.get("serving_deployment_ids") or []):
        return False, "latest accepted deployment is not serving"
    deployment = next((row for row in rows if row.get("id") == latest), None)
    if not deployment or deployment.get("status") != "live" or deployment.get("app_id") != app.get("id"):
        return False, "latest accepted deployment is not live for this app"
    result = gregale.cli("release", "verify", latest, "--app", slug, "--path", check.get("path", "/"), timeout=60)
    if result.returncode != 0:
        return False, "owner-authorized verification failed"
    evidence = json.loads(result.stdout)
    if not isinstance(evidence, dict):
        return False, "owner verification returned an unexpected document"
    instance = str(evidence.get("instance_id", "")).replace("-", "")
    body = evidence.get("body")
    ok = (evidence.get("app_id") == app.get("id") and evidence.get("deployment_id") == latest
          and evidence.get("scope") == "default" and not evidence.get("project_id") and not evidence.get("environment_id")
          and re.fullmatch(r"[0-9a-f]{32}", instance) is not None and instance != "0" * 32
          and evidence.get("verified") is True and evidence.get("public_access_verified") is False
          and type(evidence.get("http_status")) is int and evidence["http_status"] == check.get("status", 200)
          and isinstance(body, str) and len(body.encode("utf-8")) <= 4096 and check.get("contains", "") in body)
    return ok, "private deployment response verified" if ok else "response identity, HTTP status or expected content did not match"


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


def grade_features(task: dict, commands: list[str], project: Path, created: dict, tools: list[str] | None = None,
                   first_live: bool = False) -> dict:
    """Which of the task's applicable Gregale features the run used. A command
    feature also counts when the agent called one of its equivalent MCP
    tools (`tools`, matched on the tool name's suffix). A feature with
    `"unless": "first_deploy_live"` is a fix the agent needs only when
    Gregale's detection falls short, so it counts as used when the first
    deploy went live without it."""
    used, missed = [], []
    joined = "\n".join(commands)
    for feature in task.get("features", []):
        if feature.get("unless") == "first_deploy_live" and first_live:
            hit = True
        elif feature["kind"] == "command":
            hit = re.search(feature["pattern"], joined, re.MULTILINE) is not None or any(
                name.endswith(tool) for name in tools or [] for tool in feature.get("tools", []))
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
        return codex_command(prompt, project, gregale.binary, toolset, model, options.mcp_writes, keep_session=bool(task.get("repo"))), {}
    if agent == "gemini":
        return gemini_command(prompt, run_dir, gregale.binary, toolset, model, options.mcp_writes)
    return cursor_command(prompt, project, gregale.binary, toolset, model, options.mcp_writes), {}


def follow_up_command(agent: str, task: dict, run_dir: Path, project: Path, gregale: Gregale, toolset: str, options, session_id: str) -> list[str]:
    """The task's follow_up reply, resuming the agent's session (FOLLOW_UP_AGENTS)."""
    model = getattr(options, f"{agent}_model", None)
    if agent == "codex":
        return codex_command(task["follow_up"], project, gregale.binary, toolset, model, options.mcp_writes, resume=session_id)
    return claude_command(task["follow_up"], run_dir, gregale.binary, toolset, task["budget_usd"], model, options.mcp_writes, any_shell=True,
                          resume=session_id)


def cleanup(gregale: Gregale, created: dict, slug: str | None) -> list[str]:
    """Delete what the run created; returns what could not be deleted.

    A deleted app keeps its name for its seven-day restore window, so the next
    run's agent found the repository's natural name taken and deployed under a
    suffixed one (express-hello-world-2, flask-hello-world-2, 2026-11-19).
    Purging right after the delete frees the name; `name:<app>` marks a purge
    that failed and left only the name held."""
    left = []
    for project_slug in created.get("projects", []):
        if gregale.cli("project", "delete", "--yes", "--detach-services", project_slug, timeout=300).returncode != 0:
            left.append(f"project:{project_slug}")
    for app in sorted(set(created.get("apps", [])) | ({slug} if slug else set())):
        if gregale.cli("app", "delete", "--yes", "--delete-bindings", app, timeout=300).returncode != 0:
            left.append(f"app:{app}")
        elif gregale.cli("app", "purge", "--yes", app, timeout=300).returncode != 0:
            left.append(f"name:{app}")
    # Recoverable for seven days; a deleted database no longer counts against
    # the account's database limit.
    for database in created.get("databases", []):
        if gregale.cli("postgres", "delete", "--yes", database, timeout=300).returncode != 0:
            left.append(f"database:{database}")
    return left


def invoke(command: list[str], project: Path, env: dict, timeout: float, record: dict) -> list[str]:
    """Run one agent invocation; returns its stdout lines and notes a timeout in record."""
    try:
        done = subprocess.run(command, cwd=project, env=env, stdin=subprocess.DEVNULL, capture_output=True, timeout=timeout)
        record["timed_out"] = False
        record["agent_stderr"] = _text(done.stderr)[-500:]
        return _text(done.stdout).splitlines()
    except subprocess.TimeoutExpired as expired:
        record["timed_out"] = True
        return _text(expired.stdout).splitlines()


# The HTTP wait of the check that decides on a follow-up: long enough for a
# cold wake, short next to the task's own 120 s check.
FOLLOW_UP_HTTP_DEADLINE_S = 30

# The most follow_up replies one run gets. On 2026-11-19 Codex deployed Flask
# and Go privately, answered the first "go ahead" by asking whether to make the
# app public, and failed with one reply.
MAX_FOLLOW_UPS = 2


def needs_follow_up(task: dict, metrics: dict, record: dict, slug: str | None, project: Path, gregale: Gregale, created: dict) -> bool:
    """Answer the task's follow_up when the agent finished its turn but the task
    is not done yet. A quick check decides; an empty app left by a refused
    deploy no longer counts as done (2026-11-14 run)."""
    if not task.get("follow_up") or not metrics["completed"] or record["timed_out"]:
        return False
    checks = run_checks(task, slug, project, metrics["answer"], gregale, http_deadline_s=FOLLOW_UP_HTTP_DEADLINE_S, created=created,
                        since=record["started_at"])
    return not all(check["ok"] for check in checks)


def run_one(task: dict, agent: str, toolset: str, gregale: Gregale, options) -> dict:
    slug = None if task.get("repo") else app_slug(task["id"])
    run_dir = Path(tempfile.mkdtemp(prefix=f"agent-eval-{task['id']}-"))
    project = run_dir / "project"
    record = {"task": task["id"], "agent": agent, "toolset": toolset, "app": slug, "started_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())}
    record["target"] = getattr(gregale, "api", None)
    try:
        with Path(gregale.binary).open("rb") as binary:
            checksum = hashlib.sha256()
            for chunk in iter(lambda: binary.read(1024 * 1024), b""):
                checksum.update(chunk)
            record["cli_sha256"] = checksum.hexdigest()
    except OSError:
        record["cli_sha256"] = None
    created: dict = {kind: [] for kind in RESOURCE_KINDS}
    before, evidence = None, ""
    cleanup_allowed = False
    try:
        before = gregale.inventory()
        if slug and slug in before.get("apps", set()):
            raise EvalError("evaluation app name already exists; refusing to update an existing app")
        cleanup_allowed = True
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
                flags = ("--require-authn", "--rollout", "immediate") if step.get("private") else ()
                result = gregale.cli("deploy", "--path", str(project), "--name", slug, "--app", "--yes", *flags, cwd=project, timeout=1200)
                if (result.returncode == 0) != (step.get("expect", "success") == "success"):
                    raise EvalError(f"setup deploy exit {result.returncode}: {result.stderr[-300:]}")
        if task.get("retain_app_identity"):
            record["initial_app_id"] = gregale.get(f"/v1/apps/{slug}").get("id")
            if not record["initial_app_id"]:
                raise EvalError("could not pin the existing app identity before the edit task")
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
        started, wall_started = time.monotonic(), time.time()
        lines = invoke(command, project, env, task["timeout_s"], record)
        (run_dir / f"{agent}.jsonl").write_text("\n".join(lines))
        metrics = PARSERS[agent](lines)
        evidence = run_evidence(metrics, project)
        created, record["not_attributed"] = attribute(gregale, gregale.touched_since(before, record["started_at"]), evidence, slug)
        # The agent stopped short (often to ask a question); answer as the
        # person would, up to MAX_FOLLOW_UPS times.
        follow_ups = 0
        while follow_ups < MAX_FOLLOW_UPS and needs_follow_up(task, metrics, record, slug, project, gregale, created):
            if agent not in FOLLOW_UP_AGENTS or not metrics.get("session_id"):
                record["follow_ups"] = follow_ups or "unsupported"
                break
            follow_ups += 1
            remaining = max(int(task["timeout_s"] - (time.monotonic() - started)), 60)
            again = follow_up_command(agent, task, run_dir, project, gregale, toolset, options, metrics["session_id"])
            more = invoke(again, project, env, remaining, record)
            suffix = "" if follow_ups == 1 else f"-{follow_ups}"
            (run_dir / f"{agent}-follow-up{suffix}.jsonl").write_text("\n".join(more))
            metrics = merge_metrics(metrics, PARSERS[agent](more))
            record["follow_ups"] = follow_ups
            evidence = run_evidence(metrics, project)
            created, record["not_attributed"] = attribute(gregale, gregale.touched_since(before, record["started_at"]), evidence, slug)
        record["duration_s"] = round(time.monotonic() - started, 1)
        record.update({k: v for k, v in metrics.items() if k != "tool_inputs"})
        record["created"] = created
        record["checks"] = run_checks(task, slug, project, metrics["answer"], gregale, created=created, since=record["started_at"],
                                      expected_app_id=record.get("initial_app_id"))
        record["success"] = all(check["ok"] for check in record["checks"])
        record["first_deploy_live"] = first_deploy_live(gregale, created.get("apps", []), record["started_at"])
        record.update(grade_features(task, metrics["commands"], project, created, tools=list(metrics.get("tool_calls") or {}),
                                     first_live=record["first_deploy_live"]))
        record["anti_patterns"] = grade_anti_patterns(metrics["commands"])
        record["wrote_outside_run"] = writes_outside(metrics.get("written_paths") or [], run_dir, project, AGENT_OWN_DIRS[agent])
        if record["wrote_outside_run"]:
            record["anti_patterns"].append("wrote_outside_run")
        record["home_writes"] = home_writes(Path.home(), wall_started, run_dir)
        record["traps"] = [c["detail"] for c in record["checks"] if c["kind"] == "no_traps" and not c["ok"]]
    except (EvalError, subprocess.TimeoutExpired, OSError, ValueError, urllib.error.URLError) as error:
        record.update(success=False, error=str(error))
    finally:
        if not options.keep_apps and cleanup_allowed:
            try:
                if before is not None and not created.get("apps") and not created.get("projects"):
                    created, _ = attribute(gregale, gregale.touched_since(before, record["started_at"]), evidence, slug)
                doomed, doomed_slug = deletable(gregale, before, created, slug, evidence)
                record["left_behind"] = cleanup(gregale, doomed, doomed_slug)
            except (OSError, ValueError, urllib.error.URLError) as error:
                record["left_behind"] = [f"cleanup: {error}"]
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
    run.add_argument("--cli-login", action="store_true", help="use your own logged-in gregale CLI instead of GREGALE_EVAL_TOKEN; deletes only apps and projects the run created "
                     "(--keep-apps keeps them)")
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
                            "(or pass --cli-login to use your own logged-in CLI, which deletes only what the run created)")
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
