#!/usr/bin/env python3
"""Run coding agents against Gregale tasks and score them (ADR-20261026152009).

Each run scaffolds a starter into a fresh directory, applies the task's setup
(edits and deploys), starts the agent headless with the Gregale CLI and,
optionally, the management MCP server at one toolset, then checks the result
(HTTP response, serving release, untouched files, the agent's answer). It
records success, turns, tool calls, tokens, cost and time per run, deletes
the run's app, and appends one JSON line per run.

    agent_eval.py list
    agent_eval.py run --agents claude --toolsets core,all,none [--tasks ID,...] [--repeat N]
    agent_eval.py summarize results.jsonl

`run` deploys real apps. It requires GREGALE_EVAL_TOKEN, an API key of a
dedicated evaluation account, and never uses your own login. GREGALE_EVAL_API
selects the API (default https://api.gregale.dev).
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
AGENTS = ("claude", "codex")
TOOLSETS = ("core", "release", "diagnose", "all", "none")
SETUP_KINDS = ("deploy", "replace")
CHECK_KINDS = ("http", "serving", "file_contains", "answer")
DEFAULT_API = "https://api.gregale.dev"
# Shell commands a Claude run may use without asking; everything else is
# denied in headless mode.
CLAUDE_ALLOWED_TOOLS = ["Bash(gregale *)", "Bash(curl *)", "Read", "Edit", "Write", "Glob", "Grep", "mcp__gregale"]


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
        if "{app}" not in task.get("prompt", ""):
            raise EvalError(f"{tid}: prompt must name the app with {{app}}")
        for step in task.get("setup", []):
            if step.get("kind") not in SETUP_KINDS:
                raise EvalError(f"{tid}: unknown setup kind {step.get('kind')!r}")
        if not task.get("checks"):
            raise EvalError(f"{tid}: at least one check is required")
        for check in task["checks"]:
            if check.get("kind") not in CHECK_KINDS:
                raise EvalError(f"{tid}: unknown check kind {check.get('kind')!r}")
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


# --- agent commands ---------------------------------------------------------


def mcp_args(toolset: str, writes: bool) -> list[str]:
    args = ["mcp", "serve", "--transport", "stdio", "--tools", toolset]
    return args + ["--allow-writes"] if writes else args


def claude_command(prompt: str, run_dir: Path, gregale: str, toolset: str, budget: float, model: str | None, writes: bool) -> list[str]:
    command = ["claude", "-p", prompt, "--output-format", "stream-json", "--verbose", "--no-session-persistence",
               "--max-budget-usd", f"{budget:.2f}", "--permission-mode", "dontAsk", "--allowedTools", *CLAUDE_ALLOWED_TOOLS, "--strict-mcp-config"]
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


def parse_claude(lines) -> dict:
    """Metrics from `claude -p --output-format stream-json` lines."""
    metrics = {"completed": False, "turns": 0, "cost_usd": 0.0, "input_tokens": 0, "output_tokens": 0,
               "cache_read_tokens": 0, "cache_write_tokens": 0, "tool_calls": {}, "answer": "", "error": ""}
    for event in _events(lines):
        kind = event.get("type")
        if kind == "assistant":
            for block in (event.get("message") or {}).get("content") or []:
                if block.get("type") == "tool_use":
                    name = block.get("name", "?")
                    metrics["tool_calls"][name] = metrics["tool_calls"].get(name, 0) + 1
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
    metrics = {"completed": False, "turns": 0, "cost_usd": None, "input_tokens": 0, "output_tokens": 0,
               "cache_read_tokens": 0, "cache_write_tokens": 0, "tool_calls": {}, "answer": "", "error": ""}
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
                name = f"mcp__{item.get('server', '?')}__{item.get('tool', '?')}"
                metrics["tool_calls"][name] = metrics["tool_calls"].get(name, 0) + 1
            elif item.get("type") == "command_execution":
                metrics["tool_calls"]["shell"] = metrics["tool_calls"].get("shell", 0) + 1
            elif item.get("type") == "file_change":
                metrics["tool_calls"]["file_change"] = metrics["tool_calls"].get("file_change", 0) + 1
    metrics["completed"] = metrics["turns"] > 0 and not failed
    return metrics


# --- Gregale access ---------------------------------------------------------


class Gregale:
    """The evaluation account, reached only with GREGALE_EVAL_TOKEN."""

    def __init__(self, binary: str, api: str, token: str):
        self.binary, self.api, self.token = binary, api.rstrip("/"), token

    def env(self) -> dict:
        env = {k: v for k, v in os.environ.items() if not k.startswith("FAAS_")}
        env.update(FAAS_TOKEN=self.token, FAAS_API=self.api)
        return env

    def cli(self, *args: str, cwd: Path | None = None, timeout: int = 900) -> subprocess.CompletedProcess:
        return subprocess.run([self.binary, "--non-interactive", "--json", *args], cwd=cwd, env=self.env(),
                              stdin=subprocess.DEVNULL, capture_output=True, text=True, timeout=timeout)

    def get(self, path: str) -> dict:
        request = urllib.request.Request(self.api + path, headers={"Authorization": f"Bearer {self.token}"})
        with urllib.request.urlopen(request, timeout=30) as response:
            return json.loads(response.read())


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


def run_checks(task: dict, slug: str, project: Path, answer: str, gregale: Gregale | None, fetch=None, http_deadline_s: float = 120) -> list[dict]:
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
                app = gregale.get(f"/v1/apps/{slug}")
                url = (app.get("canonical_url") or app.get("url") or "").rstrip("/") + check.get("path", "/")
                ok, detail = http_check(url, check, deadline_s=http_deadline_s, fetch=fetch)
            elif kind == "serving":
                health = gregale.get(f"/v1/apps/{slug}/health")
                serving, latest = health.get("serving_deployment_ids") or [], health.get("latest_deployment_id", "")
                ok = bool(serving) and (latest not in serving if check["expect"] == "not_latest" else latest in serving)
                detail = f"serving={serving} latest={latest}"
        except (OSError, KeyError, ValueError, urllib.error.URLError) as error:
            detail = f"check error: {error}"
        results.append({"kind": kind, "ok": ok, "detail": detail})
    return results


# --- runs -------------------------------------------------------------------


def run_one(task: dict, agent: str, toolset: str, gregale: Gregale, options) -> dict:
    slug = app_slug(task["id"])
    run_dir = Path(tempfile.mkdtemp(prefix=f"agent-eval-{task['id']}-"))
    project = run_dir / "project"
    record = {"task": task["id"], "agent": agent, "toolset": toolset, "app": slug, "started_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())}
    try:
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
        prompt = task["prompt"].format(app=slug) + " The gregale CLI is installed and logged in" + (" and the Gregale MCP server is connected." if toolset != "none" else ".")
        model = options.claude_model if agent == "claude" else options.codex_model
        if agent == "claude":
            command = claude_command(prompt, run_dir, gregale.binary, toolset, task["budget_usd"], model, options.mcp_writes)
        else:
            command = codex_command(prompt, project, gregale.binary, toolset, model, options.mcp_writes)
        env = gregale.env()
        # The agent's shell `gregale` must be the binary under test.
        (run_dir / "bin").mkdir()
        (run_dir / "bin" / "gregale").symlink_to(Path(gregale.binary).resolve())
        env["PATH"] = str(run_dir / "bin") + os.pathsep + env.get("PATH", "")
        if agent == "claude" and env.get("ANTHROPIC_API_KEY"):
            # A fresh config keeps your settings, memory and plugins out of the run.
            env["CLAUDE_CONFIG_DIR"] = str(run_dir / "claude-config")
            record["isolation"] = "fresh-config"
        else:
            record["isolation"] = "user-config"
        started = time.monotonic()
        try:
            done = subprocess.run(command, cwd=project, env=env, stdin=subprocess.DEVNULL, capture_output=True, timeout=task["timeout_s"])
            lines, record["timed_out"] = _text(done.stdout).splitlines(), False
        except subprocess.TimeoutExpired as expired:
            lines, record["timed_out"] = _text(expired.stdout).splitlines(), True
        record["duration_s"] = round(time.monotonic() - started, 1)
        (run_dir / f"{agent}.jsonl").write_text("\n".join(lines))
        metrics = parse_claude(lines) if agent == "claude" else parse_codex(lines)
        record.update(metrics)
        record["checks"] = run_checks(task, slug, project, metrics["answer"], gregale)
        record["success"] = all(check["ok"] for check in record["checks"])
    except (EvalError, subprocess.TimeoutExpired, OSError) as error:
        record.update(success=False, error=str(error))
    finally:
        if not options.keep_apps:
            gregale.cli("apps", "rm", "--yes", slug, timeout=120)
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
    rows = ["| task | agent | toolset | runs | success | turns (median) | tool calls (median) | input tokens (median) | cost USD (mean) | time s (median) |",
            "|---|---|---|---|---|---|---|---|---|---|"]
    for (task, agent, toolset), runs in sorted(groups.items()):
        def median(key):
            values = [r[key] for r in runs if isinstance(r.get(key), (int, float))]
            return round(statistics.median(values), 1) if values else "—"
        costs = [r["cost_usd"] for r in runs if isinstance(r.get("cost_usd"), (int, float))]
        calls = [sum((r.get("tool_calls") or {}).values()) for r in runs]
        tokens = [r.get("input_tokens", 0) + r.get("cache_read_tokens", 0) + r.get("cache_write_tokens", 0) for r in runs]
        success = sum(1 for r in runs if r.get("success"))
        rows.append(f"| {task} | {agent} | {toolset} | {len(runs)} | {success}/{len(runs)} | {median('turns')} | "
                    f"{statistics.median(calls) if calls else '—'} | {statistics.median(tokens) if tokens else '—'} | "
                    f"{round(statistics.mean(costs), 3) if costs else '—'} | {median('duration_s')} |")
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
    run.add_argument("--no-mcp-writes", dest="mcp_writes", action="store_false", help="serve MCP without --allow-writes")
    run.add_argument("--keep-apps", action="store_true", help="do not delete the run's app")
    run.add_argument("--keep-dirs", action="store_true", help="keep run directories and transcripts")
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
        if not token:
            raise EvalError("set GREGALE_EVAL_TOKEN to an API key of a dedicated evaluation account; runs deploy and delete real apps")
        agents = _csv(options.agents, AGENTS, "agents")
        toolsets = _csv(options.toolsets, TOOLSETS, "toolsets")
        selected = [t for t in tasks if not options.tasks or t["id"] in options.tasks.split(",")]
        if not selected or options.repeat < 1:
            raise EvalError("no tasks selected, or --repeat below 1")
        for agent in agents:
            if not shutil.which(agent):
                raise EvalError(f"{agent} is not installed")
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
                                  f"turns={record.get('turns')} cost={record.get('cost_usd')}", file=sys.stderr)
        print(summarize([json.loads(line) for line in Path(options.out).read_text().splitlines() if line.strip()]))
        return 0
    except EvalError as error:
        print(f"agent-eval: {error}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    sys.exit(main())
