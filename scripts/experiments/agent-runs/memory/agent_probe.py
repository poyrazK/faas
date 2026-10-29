#!/usr/bin/env python3
"""Layer entrypoint for the keyless agent memory experiment.

One process serves two ports. Port 8080 (all interfaces) is the metal test's
control API. Port 8787 (loopback only) is a fake model provider that the
harness uses as its base URL. The provider speaks Anthropic Messages (Claude
Code, OpenCode) and OpenAI Responses or Chat Completions (Codex). It follows
a fixed script of turns, and it holds each response until the test releases
it or the hold ends. So the harness waits on a model call the way it does in
production, with no model and no key.

Control API (JSON):
  GET  /            readiness
  POST /run         {"preset", "script", "hold_s", "manual": [turn numbers], "max_hold_s"}
  POST /warm        {"preset"}: one short run, so the snapshot holds the harness's pages
  POST /release     release every held response
  GET  /status      phase, call count, held flag, exit code, timeline, guest memory

Fidelity mode, for the layer build (no VM):
  agent_probe.py --fidelity PRESET SCRIPT [--port N]
"""

from __future__ import annotations

import argparse
import json
import os
import shutil
import subprocess
import sys
import threading
import time
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

HERE = Path(__file__).resolve().parent
SCRIPTS = Path(os.environ.get("AGENT_PROBE_SCRIPTS", HERE / "scripts"))
INPUT = Path(os.environ.get("AGENT_PROBE_INPUT", "/input"))
KEY = "relay-placeholder-key"
MODEL_ANTHROPIC = "claude-sonnet-5-5"
DONE_MARK = "AGENT-MEM-DONE"

# Tool names each harness exposes for running a shell command, in order of
# preference. The provider picks the first one present in the request's tools.
ANTHROPIC_SHELL = ("Bash", "bash")
OPENAI_SHELL = ("shell", "shell_command", "exec_command", "container.exec", "bash")


def now() -> float:
    return time.time()


# --------------------------------------------------------------------------
# Script and turn logic


def load_script(name: str) -> list[dict]:
    with open(SCRIPTS / f"{name}.json", encoding="utf-8") as f:
        return json.load(f)["turns"]


def count_tool_results(body: dict) -> int:
    """Completed tool calls in the conversation so far, in any wire format."""
    n = 0
    for msg in body.get("messages") or []:
        if msg.get("role") == "tool":
            n += 1
        content = msg.get("content")
        if isinstance(content, list):
            n += sum(1 for b in content if isinstance(b, dict) and b.get("type") == "tool_result")
    for item in body.get("input") or []:
        if isinstance(item, dict) and item.get("type") in (
            "function_call_output", "local_shell_call_output", "custom_tool_call_output"):
            n += 1
    return n


def tool_names(body: dict) -> list[str]:
    names = []
    for tool in body.get("tools") or []:
        if not isinstance(tool, dict):
            continue
        if tool.get("type") == "local_shell":
            names.append("local_shell")
        name = tool.get("name") or (tool.get("function") or {}).get("name")
        if name:
            names.append(name)
    return names


def tool_schema(body: dict, name: str) -> dict:
    for tool in body.get("tools") or []:
        if not isinstance(tool, dict):
            continue
        fn = tool.get("function") or {}
        if tool.get("name") == name or fn.get("name") == name:
            return tool.get("input_schema") or tool.get("parameters") or fn.get("parameters") or {}
    return {}


def shell_args(schema: dict, command: str) -> dict:
    """Arguments for a shell tool, shaped by its JSON schema."""
    props = schema.get("properties") or {}
    args: dict = {}
    if "command" in props:
        kind = props["command"].get("type")
        args["command"] = ["bash", "-lc", command] if kind == "array" else command
    elif "cmd" in props:
        args["cmd"] = command
    else:
        args["command"] = command
    if "description" in props:
        args["description"] = "experiment step"
    return args


# --------------------------------------------------------------------------
# Fake provider


class Provider:
    """Scripted, response-holding model provider."""

    def __init__(self) -> None:
        self.lock = threading.Lock()
        self.turns: list[dict] = []
        self.hold_s = 0.0
        self.manual: set[int] = set()
        self.max_hold_s = 600.0
        self.release = threading.Event()
        self.calls = 0
        self.held = 0
        self.timeline: list[dict] = []
        self.side_requests = 0
        self.paths: dict[str, int] = {}
        self.tools_seen: list[str] = []

    def configure(self, turns: list[dict], hold_s: float, manual: list[int], max_hold_s: float) -> None:
        with self.lock:
            self.turns, self.hold_s = turns, hold_s
            self.manual, self.max_hold_s = set(manual), max_hold_s
            self.calls, self.held, self.side_requests = 0, 0, 0
            self.timeline, self.paths, self.tools_seen = [], {}, []
            self.release = threading.Event()

    def do_release(self) -> float:
        t = now()
        with self.lock:
            for entry in self.timeline:
                if entry.get("released") is None and entry.get("hold_start") is not None:
                    entry["released"] = t
            event = self.release
            self.release = threading.Event()
        event.set()
        return t

    def note_path(self, path: str) -> None:
        with self.lock:
            self.paths[path] = self.paths.get(path, 0) + 1

    def plan(self, body: dict, raw: str, shell_names: tuple[str, ...]) -> tuple[dict, dict] | None:
        """The turn to answer and its timeline entry, or None for a side request."""
        names = tool_names(body)
        shell = next((n for n in shell_names if n in names), None)
        if shell is None and "local_shell" in names:
            shell = "local_shell"
        if shell is None or not self.turns:
            with self.lock:
                self.side_requests += 1
            return None
        done = count_tool_results(body)
        idx = min(done, len(self.turns) - 1)
        turn = dict(self.turns[idx])
        with self.lock:
            self.calls += 1
            call = self.calls
            if not self.tools_seen:
                self.tools_seen = names
            prev = self.turns[idx - 1].get("mark") if idx > 0 else None
            entry = {"call": call, "turn": idx + 1, "arrived": now(), "hold_start": None,
                     "released": None, "sent": None, "shell_tool": shell,
                     "prev_tool_ok": (prev in raw) if prev else None,
                     "request_bytes": len(raw)}
            self.timeline.append(entry)
        turn["_shell"], turn["_schema"] = shell, tool_schema(body, shell)
        return turn, entry

    def hold(self, entry: dict) -> None:
        """Hold a response: until /release for a manual turn, else for hold_s."""
        with self.lock:
            manual = entry["turn"] in self.manual
            hold_s = self.max_hold_s if manual else self.hold_s
            event = self.release
            entry["hold_start"] = now()
            entry["manual"] = manual
            self.held += 1
        try:
            if hold_s > 0:
                event.wait(hold_s)
        finally:
            with self.lock:
                self.held -= 1
                if entry.get("released") is None:
                    entry["released"] = now()

    def sent(self, entry: dict) -> None:
        with self.lock:
            entry["sent"] = now()

    def snapshot(self) -> dict:
        with self.lock:
            return {"calls": self.calls, "held": self.held, "side_requests": self.side_requests,
                    "timeline": [dict(e) for e in self.timeline], "paths": dict(self.paths),
                    "tools_seen": list(self.tools_seen)}


class Chunked:
    """Chunked HTTP/1.1 body writer."""

    def __init__(self, handler: BaseHTTPRequestHandler) -> None:
        self.w = handler.wfile

    def write(self, data: bytes) -> None:
        if data:
            self.w.write(f"{len(data):x}\r\n".encode() + data + b"\r\n")
            self.w.flush()

    def close(self) -> None:
        self.w.write(b"0\r\n\r\n")
        self.w.flush()


def sse(event: str | None, data: dict) -> bytes:
    head = f"event: {event}\n" if event else ""
    return (head + "data: " + json.dumps(data, separators=(",", ":")) + "\n\n").encode()


def provider_handler(p: Provider):
    class Handler(BaseHTTPRequestHandler):
        protocol_version = "HTTP/1.1"

        def log_message(self, *_args) -> None:
            return

        def _body(self) -> tuple[dict, str]:
            n = int(self.headers.get("content-length") or 0)
            raw = self.rfile.read(n).decode("utf-8", "replace") if n else ""
            try:
                return (json.loads(raw) if raw else {}), raw
            except json.JSONDecodeError:
                return {}, raw

        def _json(self, code: int, obj: dict) -> None:
            data = json.dumps(obj).encode()
            self.send_response(code)
            self.send_header("content-type", "application/json")
            self.send_header("content-length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)

        def _stream_start(self) -> Chunked:
            self.send_response(200)
            self.send_header("content-type", "text/event-stream")
            self.send_header("cache-control", "no-cache")
            self.send_header("transfer-encoding", "chunked")
            self.send_header("connection", "close")
            self.end_headers()
            self.close_connection = True
            return Chunked(self)

        def do_GET(self) -> None:
            p.note_path("GET " + self.path)
            if self.path.rstrip("/").endswith("/models"):
                self._json(200, {"object": "list", "data": [
                    {"id": "gpt-5.5", "object": "model"}, {"id": MODEL_ANTHROPIC, "type": "model"}]})
                return
            self._json(404, {"error": {"type": "not_found", "message": self.path}})

        def do_POST(self) -> None:
            path = self.path.split("?", 1)[0]
            p.note_path("POST " + path)
            body, raw = self._body()
            if path.endswith("/messages/count_tokens"):
                self._json(200, {"input_tokens": max(1, len(raw) // 4)})
            elif path.endswith("/messages"):
                self._anthropic(body, raw)
            elif path.endswith("/responses"):
                self._responses(body, raw)
            elif path.endswith("/chat/completions"):
                self._chat(body, raw)
            else:
                self._json(404, {"error": {"type": "not_found", "message": path}})

        # Anthropic Messages -------------------------------------------------
        def _anthropic(self, body: dict, raw: str) -> None:
            planned = p.plan(body, raw, ANTHROPIC_SHELL)
            msg_id = "msg_" + uuid.uuid4().hex[:20]
            model = body.get("model") or MODEL_ANTHROPIC
            usage = {"input_tokens": max(1, len(raw) // 4), "output_tokens": 1}
            if planned is None:
                turn, entry = {"text": "ok"}, None
            else:
                turn, entry = planned
            if "command" in turn:
                block = {"type": "tool_use", "id": "toolu_" + uuid.uuid4().hex[:20],
                         "name": turn["_shell"], "input": shell_args(turn["_schema"], turn["command"])}
                stop = "tool_use"
            else:
                block = {"type": "text", "text": turn.get("text", "ok")}
                stop = "end_turn"
            if not body.get("stream"):
                if entry:
                    p.hold(entry)
                self._json(200, {"id": msg_id, "type": "message", "role": "assistant", "model": model,
                                 "content": [block], "stop_reason": stop, "stop_sequence": None,
                                 "usage": usage})
                if entry:
                    p.sent(entry)
                return
            out = self._stream_start()
            try:
                out.write(sse("message_start", {"type": "message_start", "message": {
                    "id": msg_id, "type": "message", "role": "assistant", "model": model, "content": [],
                    "stop_reason": None, "stop_sequence": None, "usage": usage}}))
                if entry:
                    p.hold(entry)
                if block["type"] == "tool_use":
                    start = dict(block, input={})
                    out.write(sse("content_block_start", {"type": "content_block_start", "index": 0,
                                                          "content_block": start}))
                    out.write(sse("content_block_delta", {"type": "content_block_delta", "index": 0, "delta": {
                        "type": "input_json_delta", "partial_json": json.dumps(block["input"])}}))
                else:
                    out.write(sse("content_block_start", {"type": "content_block_start", "index": 0,
                                                          "content_block": {"type": "text", "text": ""}}))
                    out.write(sse("content_block_delta", {"type": "content_block_delta", "index": 0,
                                                          "delta": {"type": "text_delta", "text": block["text"]}}))
                out.write(sse("content_block_stop", {"type": "content_block_stop", "index": 0}))
                out.write(sse("message_delta", {"type": "message_delta",
                                                "delta": {"stop_reason": stop, "stop_sequence": None},
                                                "usage": {"output_tokens": 16}}))
                out.write(sse("message_stop", {"type": "message_stop"}))
                out.close()
            except (BrokenPipeError, ConnectionResetError):
                return
            if entry:
                p.sent(entry)

        # OpenAI Responses ---------------------------------------------------
        def _responses(self, body: dict, raw: str) -> None:
            planned = p.plan(body, raw, OPENAI_SHELL)
            resp_id = "resp_" + uuid.uuid4().hex[:20]
            turn, entry = (planned if planned else ({"text": "ok"}, None))
            if "command" in turn and turn["_shell"] == "local_shell":
                item = {"type": "local_shell_call", "id": "lsh_" + uuid.uuid4().hex[:12],
                        "call_id": "call_" + uuid.uuid4().hex[:12], "status": "completed",
                        "action": {"type": "exec", "command": ["bash", "-lc", turn["command"]]}}
            elif "command" in turn:
                item = {"type": "function_call", "id": "fc_" + uuid.uuid4().hex[:12],
                        "call_id": "call_" + uuid.uuid4().hex[:12], "name": turn["_shell"],
                        "arguments": json.dumps(shell_args(turn["_schema"], turn["command"]))}
            else:
                item = {"type": "message", "id": "msg_" + uuid.uuid4().hex[:12], "role": "assistant",
                        "status": "completed",
                        "content": [{"type": "output_text", "text": turn.get("text", "ok"), "annotations": []}]}
            usage = {"input_tokens": max(1, len(raw) // 4), "input_tokens_details": {"cached_tokens": 0},
                     "output_tokens": 16, "output_tokens_details": {"reasoning_tokens": 0},
                     "total_tokens": max(1, len(raw) // 4) + 16}
            out = self._stream_start()
            try:
                out.write(sse("response.created", {"type": "response.created",
                                                   "response": {"id": resp_id, "status": "in_progress"}}))
                if entry:
                    p.hold(entry)
                out.write(sse("response.output_item.added", {"type": "response.output_item.added",
                                                             "output_index": 0, "item": item}))
                if item["type"] == "message":
                    out.write(sse("response.output_text.delta", {
                        "type": "response.output_text.delta", "item_id": item["id"], "output_index": 0,
                        "content_index": 0, "delta": item["content"][0]["text"]}))
                out.write(sse("response.output_item.done", {"type": "response.output_item.done",
                                                            "output_index": 0, "item": item}))
                out.write(sse("response.completed", {"type": "response.completed", "response": {
                    "id": resp_id, "status": "completed", "output": [item], "usage": usage}}))
                out.close()
            except (BrokenPipeError, ConnectionResetError):
                return
            if entry:
                p.sent(entry)

        # OpenAI Chat Completions (fallback) ---------------------------------
        def _chat(self, body: dict, raw: str) -> None:
            planned = p.plan(body, raw, OPENAI_SHELL)
            turn, entry = (planned if planned else ({"text": "ok"}, None))
            if "command" in turn:
                delta = {"role": "assistant", "tool_calls": [{
                    "index": 0, "id": "call_" + uuid.uuid4().hex[:12], "type": "function",
                    "function": {"name": turn["_shell"],
                                 "arguments": json.dumps(shell_args(turn["_schema"], turn["command"]))}}]}
                finish = "tool_calls"
            else:
                delta, finish = {"role": "assistant", "content": turn.get("text", "ok")}, "stop"
            base = {"id": "chatcmpl-" + uuid.uuid4().hex[:12], "object": "chat.completion.chunk",
                    "created": int(now()), "model": body.get("model") or "fake"}
            out = self._stream_start()
            try:
                if entry:
                    p.hold(entry)
                out.write(sse(None, dict(base, choices=[{"index": 0, "delta": delta, "finish_reason": None}])))
                out.write(sse(None, dict(base, choices=[{"index": 0, "delta": {}, "finish_reason": finish}])))
                out.write(b"data: [DONE]\n\n")
                out.close()
            except (BrokenPipeError, ConnectionResetError):
                return
            if entry:
                p.sent(entry)

    return Handler


# --------------------------------------------------------------------------
# Harness launch


def harness_env(preset: str, port: int, home: Path) -> dict:
    env = {
        "PATH": "/usr/local/bin:/usr/bin:/bin:/usr/local/sbin:/usr/sbin:/sbin",
        "HOME": str(home), "TMPDIR": str(home / "tmp"), "LANG": "C.UTF-8", "TERM": "dumb",
        # Telemetry and self-update off: the guest makes no third-party calls.
        "DISABLE_TELEMETRY": "1", "DISABLE_ERROR_REPORTING": "1", "DISABLE_AUTOUPDATER": "1",
        "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "USE_BUILTIN_RIPGREP": "0",
        "NO_UPDATE_NOTIFIER": "1", "OPENCODE_DISABLE_AUTOUPDATE": "1",
        "OPENCODE_DISABLE_MODELS_FETCH": "1", "OPENCODE_DISABLE_LSP_DOWNLOAD": "1",
        "OPENCODE_DISABLE_DEFAULT_PLUGINS": "1", "OPENCODE_DISABLE_SHARE": "1",
        "RUST_LOG": "error",
    }
    base = f"http://127.0.0.1:{port}"
    if preset == "codex":
        # Codex 0.163 ignores OPENAI_BASE_URL; its provider comes from config.toml.
        env.update(CODEX_HOME=str(home / ".codex"), OPENAI_API_KEY=KEY)
    else:
        env.update(ANTHROPIC_BASE_URL=base, ANTHROPIC_API_KEY=KEY)
    return env


def harness_argv(preset: str, prompt: str) -> list[str]:
    if preset == "codex":
        return ["codex", "exec", "--skip-git-repo-check", "--dangerously-bypass-approvals-and-sandbox",
                "--color", "never", prompt]
    if preset == "claude-code":
        return ["claude", "-p", prompt, "--output-format", "json", "--model", MODEL_ANTHROPIC,
                "--allowedTools", "Bash,Read", "--permission-mode", "acceptEdits"]
    if preset == "opencode":
        return ["opencode", "run", "-m", f"anthropic/{MODEL_ANTHROPIC}", prompt]
    raise ValueError(f"unknown preset {preset!r}")


def write_opencode_config(home: Path, port: int) -> None:
    cfg = home / ".config" / "opencode"
    cfg.mkdir(parents=True, exist_ok=True)
    (cfg / "opencode.json").write_text(json.dumps({
        "$schema": "https://opencode.ai/config.json",
        "autoupdate": False, "share": "disabled",
        "permission": {"bash": "allow", "edit": "allow", "webfetch": "deny"},
        "provider": {"anthropic": {"options": {"baseURL": f"http://127.0.0.1:{port}/v1", "apiKey": KEY}}},
    }))


def write_codex_config(home: Path, port: int) -> None:
    cfg = home / ".codex"
    cfg.mkdir(parents=True, exist_ok=True)
    (cfg / "config.toml").write_text(
        'model = "gpt-5.5"\n'
        'model_provider = "relay"\n'
        'check_for_update_on_startup = false\n'
        '[analytics]\nenabled = false\n'
        '[model_providers.relay]\n'
        'name = "relay"\n'
        f'base_url = "http://127.0.0.1:{port}/v1"\n'
        'env_key = "OPENAI_API_KEY"\n'
        'wire_api = "responses"\n'
        'supports_websockets = false\n')


def prepare_workdir(work: Path) -> None:
    if work.exists():
        shutil.rmtree(work)
    repo = INPUT / "repo"
    if repo.is_dir():
        shutil.copytree(repo, work, symlinks=True)
    else:
        work.mkdir(parents=True)
    for name in ("PR.diff", "big.txt"):
        if (INPUT / name).exists():
            shutil.copy2(INPUT / name, work / name)


CLK_TCK = os.sysconf("SC_CLK_TCK")


def process_tree_usage(root_pid: int) -> tuple[int, float]:
    """Guest-side RSS (kB) and CPU time (ms) of a process and its descendants."""
    children: dict[int, list[int]] = {}
    rss: dict[int, int] = {}
    cpu: dict[int, int] = {}
    for d in os.listdir("/proc"):
        if not d.isdigit():
            continue
        try:
            with open(f"/proc/{d}/stat", encoding="utf-8") as f:
                fields = f.read().rsplit(")", 1)[1].split()
            with open(f"/proc/{d}/status", encoding="utf-8") as f:
                kb = next((int(line.split()[1]) for line in f if line.startswith("VmRSS:")), 0)
        except (OSError, ValueError, IndexError):
            continue
        children.setdefault(int(fields[1]), []).append(int(d))
        rss[int(d)] = kb
        cpu[int(d)] = int(fields[11]) + int(fields[12])  # utime + stime
    total_kb, ticks, stack = 0, 0, [root_pid]
    while stack:
        pid = stack.pop()
        total_kb += rss.get(pid, 0)
        ticks += cpu.get(pid, 0)
        stack.extend(children.get(pid, []))
    return total_kb, ticks * 1000 / CLK_TCK


def meminfo_kb() -> dict:
    out = {}
    try:
        with open("/proc/meminfo", encoding="utf-8") as f:
            for line in f:
                key, val = line.split(":", 1)
                if key in ("MemTotal", "MemFree", "MemAvailable", "Cached", "AnonPages", "Shmem", "Buffers"):
                    out[key] = int(val.split()[0])
    except OSError:
        pass
    return out


class Runner:
    """One harness run at a time against the provider."""

    def __init__(self, provider: Provider, port: int, root: Path) -> None:
        self.p, self.port, self.root = provider, port, root
        self.lock = threading.Lock()
        self.proc: subprocess.Popen | None = None
        self.state: dict = {"phase": "idle"}
        self.seq = 0

    def start(self, preset: str, script: str, hold_s: float, manual: list[int], max_hold_s: float) -> dict:
        with self.lock:
            if self.proc and self.proc.poll() is None:
                raise RuntimeError("a run is in progress")
            self.seq += 1
            run_dir = self.root / f"run-{self.seq}"
            home, work = run_dir / "home", run_dir / "work"
            (home / "tmp").mkdir(parents=True, exist_ok=True)
            prepare_workdir(work)
            if preset == "opencode":
                write_opencode_config(home, self.port)
            elif preset == "codex":
                write_codex_config(home, self.port)
            self.p.configure(load_script(script), hold_s, manual, max_hold_s)
            prompt = ("Experiment run. Follow the tool calls you are given and report "
                      f"{DONE_MARK} when finished.")
            stdout = open(run_dir / "stdout", "wb")
            stderr = open(run_dir / "stderr", "wb")
            started = now()
            self.proc = subprocess.Popen(harness_argv(preset, prompt), cwd=work, env=harness_env(
                preset, self.port, home), stdout=stdout, stderr=stderr, stdin=subprocess.DEVNULL)
            self.state = {"phase": "running", "preset": preset, "script": script, "started": started,
                          "pid": self.proc.pid, "run_dir": str(run_dir), "exit_code": None, "ended": None,
                          "peak_tree_rss_kb": 0}
            threading.Thread(target=self._watch, args=(self.proc, stdout, stderr), daemon=True).start()
            return dict(self.state)

    def _watch(self, proc: subprocess.Popen, stdout, stderr) -> None:
        while proc.poll() is None:
            rss, cpu_ms = process_tree_usage(proc.pid)
            with self.lock:
                self.state["peak_tree_rss_kb"] = max(self.state["peak_tree_rss_kb"], rss)
                self.state["tree_rss_kb"] = rss
                self.state["tree_cpu_ms"] = cpu_ms
            time.sleep(0.5)
        stdout.close()
        stderr.close()
        with self.lock:
            self.state.update(phase="done", exit_code=proc.returncode, ended=now(), tree_rss_kb=0)

    def status(self) -> dict:
        with self.lock:
            st = dict(self.state)
        st.update(self.p.snapshot())
        st["guest_meminfo_kb"] = meminfo_kb()
        t = os.times()
        st["probe_cpu_ms"] = (t.user + t.system) * 1000
        run_dir = st.get("run_dir")
        if run_dir and st.get("phase") == "done":
            out = Path(run_dir, "stdout").read_bytes()[-4000:].decode("utf-8", "replace")
            err = Path(run_dir, "stderr").read_bytes()[-4000:].decode("utf-8", "replace")
            st.update(stdout_tail=out, stderr_tail=err, done_mark=DONE_MARK in out)
        return st

    def wait(self, timeout_s: float) -> dict:
        deadline = now() + timeout_s
        while now() < deadline:
            st = self.status()
            if st.get("phase") == "done":
                return st
            time.sleep(0.2)
        raise TimeoutError("harness did not finish")


def control_handler(runner: Runner):
    class Handler(BaseHTTPRequestHandler):
        protocol_version = "HTTP/1.1"

        def log_message(self, *_args) -> None:
            return

        def _json(self, code: int, obj: dict) -> None:
            data = json.dumps(obj).encode()
            self.send_response(code)
            self.send_header("content-type", "application/json")
            self.send_header("content-length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)

        def _req(self) -> dict:
            n = int(self.headers.get("content-length") or 0)
            return json.loads(self.rfile.read(n) or b"{}")

        def do_GET(self) -> None:
            if self.path.startswith("/status"):
                self._json(200, runner.status())
            else:
                self._json(200, {"ok": True})

        def do_POST(self) -> None:
            try:
                req = self._req()
                if self.path.startswith("/run"):
                    st = runner.start(req["preset"], req.get("script", "s1"), float(req.get("hold_s", 3)),
                                      [int(x) for x in req.get("manual", [])],
                                      float(req.get("max_hold_s", 600)))
                    self._json(200, st)
                elif self.path.startswith("/warm"):
                    t0 = now()
                    runner.start(req["preset"], "warm", 0, [], 60)
                    st = runner.wait(float(req.get("timeout_s", 180)))
                    st["warm_s"] = now() - t0
                    self._json(200, st)
                elif self.path.startswith("/release"):
                    self._json(200, {"released": runner.p.do_release()})
                else:
                    self._json(404, {"error": self.path})
            except Exception as exc:  # the test reads the error text
                self._json(500, {"error": f"{type(exc).__name__}: {exc}"})

    return Handler


def serve(port: int, provider_port: int, root: Path) -> None:
    provider = Provider()
    runner = Runner(provider, provider_port, root)
    # Bind the app port first: guest-init's port normalisation (ADR-051)
    # treats the first port an app binds as its port and forwards :8080 to it.
    cs = ThreadingHTTPServer(("0.0.0.0", port), control_handler(runner))
    cs.daemon_threads = True
    ps = ThreadingHTTPServer(("127.0.0.1", provider_port), provider_handler(provider))
    ps.daemon_threads = True
    threading.Thread(target=ps.serve_forever, daemon=True).start()
    cs.serve_forever()


def fidelity(preset: str, script: str, provider_port: int, root: Path) -> int:
    """Run one harness to completion against the provider; exit 0 when it followed the script."""
    provider = Provider()
    ps = ThreadingHTTPServer(("127.0.0.1", provider_port), provider_handler(provider))
    ps.daemon_threads = True
    threading.Thread(target=ps.serve_forever, daemon=True).start()
    runner = Runner(provider, provider_port, root)
    runner.start(preset, script, 0.2, [], 60)
    st = runner.wait(300)
    ps.shutdown()
    turns = load_script(script)
    calls = st["timeline"]
    tool_ok = all(e.get("prev_tool_ok") in (True, None) for e in calls)
    ok = st["exit_code"] == 0 and st.get("done_mark") and len(calls) >= len(turns) and tool_ok
    print(json.dumps({"preset": preset, "script": script, "ok": bool(ok), "exit_code": st["exit_code"],
                      "calls": len(calls), "turns": len(turns), "tool_ok": tool_ok,
                      "side_requests": st["side_requests"], "paths": st["paths"],
                      "tools_seen": st["tools_seen"], "peak_tree_rss_kb": st["peak_tree_rss_kb"],
                      "elapsed_s": round(st["ended"] - st["started"], 2),
                      "stdout_tail": st.get("stdout_tail", "")[-600:],
                      "stderr_tail": st.get("stderr_tail", "")[-1500:]}, indent=1))
    return 0 if ok else 1


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--port", type=int, default=8080)
    ap.add_argument("--provider-port", type=int, default=8787)
    ap.add_argument("--root", default=os.environ.get("AGENT_PROBE_ROOT", "/tmp/agent-probe"))
    ap.add_argument("--fidelity", nargs=2, metavar=("PRESET", "SCRIPT"))
    args = ap.parse_args()
    root = Path(args.root)
    root.mkdir(parents=True, exist_ok=True)
    if args.fidelity:
        return fidelity(args.fidelity[0], args.fidelity[1], args.provider_port, root)
    serve(args.port, args.provider_port, root)
    return 0


if __name__ == "__main__":
    sys.exit(main())
