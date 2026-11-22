#!/usr/bin/env python3
"""Gregale code interpreter driver (ADR-20261027090013 step 14).

guest-init starts this once per session, as the session's user, and talks
to it over a Unix socket: one JSON request line per connection, answered
with JSON event lines. Each code context is its own Jupyter kernel
(ipykernel through jupyter_client), so variables, imports and the working
directory persist between runs of the same context.

Requests:
  {"op": "run", "context": "<id or empty>", "code": "...",
   "timeout_ms": n, "max_output_bytes": n}
      -> {"type": "stdout"|"stderr", "text": "..."}*
         {"type": "result", "data": {mime: value}, "is_main_result": bool}*
         {"type": "error", "name": "...", "value": "...", "traceback": "..."}?
         {"type": "done", "execution_count": n|null, "timed_out": bool,
          "output_truncated": bool}
  {"op": "create", "language": "python", "cwd": "..."} -> {"type": "context", ...}
  {"op": "list"} -> {"type": "contexts", "contexts": [...]}
  {"op": "restart"|"remove", "context": "<id>"} -> {"type": "ok"}
Any failure answers {"type": "fail", "code": "...", "message": "..."}.

An empty context names the default one, created on first use in the
workspace. A run that passes its timeout is interrupted; a kernel that does
not stop within INTERRUPT_GRACE_S is restarted, losing that context's state.
"""

from __future__ import annotations

import argparse
import json
import os
import queue
import socketserver
import threading
import time
import uuid
from typing import Any, Callable, Dict, Optional

from jupyter_client import KernelManager

DEFAULT_CONTEXT = "default"
MAX_CONTEXTS = 8
INTERRUPT_GRACE_S = 5.0
KERNEL_READY_TIMEOUT_S = 60.0
REQUEST_MAX_BYTES = 4 << 20
KERNELS = {"python": "python3"}
# Rich outputs a client can use; others (widgets, application/vnd.*) are dropped.
KEPT_MIME = (
    "text/plain",
    "text/html",
    "text/markdown",
    "text/latex",
    "image/png",
    "image/jpeg",
    "image/svg+xml",
    "application/json",
    "application/javascript",
    "application/pdf",
)

Emit = Callable[[Dict[str, Any]], None]


class Fail(Exception):
    def __init__(self, code: str, message: str) -> None:
        super().__init__(message)
        self.code = code
        self.message = message


class Context:
    def __init__(self, cid: str, language: str, cwd: str, runtime_dir: str) -> None:
        self.id = cid
        self.language = language
        self.cwd = cwd
        self.lock = threading.Lock()
        # IPC transport: the kernel's channels are Unix sockets in the
        # driver's private directory, never listening TCP ports.
        self.km = KernelManager(kernel_name=KERNELS[language], transport="ipc", ip=os.path.join(runtime_dir, f"kernel-{cid}"))
        self.km.connection_file = os.path.join(runtime_dir, f"kernel-{cid}.json")
        self._start()

    def _start(self) -> None:
        env = dict(os.environ)
        env.setdefault("MPLBACKEND", "module://matplotlib_inline.backend_inline")
        self.km.start_kernel(cwd=self.cwd, env=env)
        self.kc = self.km.blocking_client()
        self.kc.start_channels()
        self.kc.wait_for_ready(timeout=KERNEL_READY_TIMEOUT_S)

    def view(self) -> Dict[str, str]:
        return {"id": self.id, "language": self.language, "cwd": self.cwd}

    def restart(self) -> None:
        self.kc.stop_channels()
        self.km.restart_kernel(now=True)
        self.kc = self.km.blocking_client()
        self.kc.start_channels()
        self.kc.wait_for_ready(timeout=KERNEL_READY_TIMEOUT_S)

    def shutdown(self) -> None:
        try:
            self.kc.stop_channels()
        finally:
            self.km.shutdown_kernel(now=True)

    def run(self, code: str, timeout_s: float, budget: "Budget", emit: Emit) -> None:
        with self.lock:
            msg_id = self.kc.execute(code, store_history=True, allow_stdin=False)
            deadline = time.monotonic() + timeout_s
            count: Optional[int] = None
            timed_out = interrupted = False
            while True:
                left = deadline - time.monotonic()
                if left <= 0:
                    if interrupted:
                        # The kernel ignored the interrupt: its state is lost.
                        self.restart()
                        break
                    timed_out = interrupted = True
                    self.km.interrupt_kernel()
                    deadline = time.monotonic() + INTERRUPT_GRACE_S
                    continue
                try:
                    msg = self.kc.get_iopub_msg(timeout=min(left, 1.0))
                except queue.Empty:
                    continue
                if msg.get("parent_header", {}).get("msg_id") != msg_id:
                    continue
                kind, content = msg["msg_type"], msg["content"]
                if kind == "execute_input":
                    count = content.get("execution_count")
                elif kind == "stream":
                    budget.emit(emit, {"type": content.get("name", "stdout"), "text": content.get("text", "")})
                elif kind in ("display_data", "execute_result"):
                    data = {k: v for k, v in content.get("data", {}).items() if k in KEPT_MIME}
                    if data:
                        budget.emit(emit, {"type": "result", "data": data, "is_main_result": kind == "execute_result"})
                elif kind == "error":
                    budget.emit(emit, {
                        "type": "error",
                        "name": content.get("ename", ""),
                        "value": content.get("evalue", ""),
                        "traceback": "\n".join(content.get("traceback", [])),
                    })
                elif kind == "status" and content.get("execution_state") == "idle":
                    break
            emit({"type": "done", "execution_count": count, "timed_out": timed_out, "output_truncated": budget.dropped})


class Budget:
    """A run's output allowance, in encoded bytes; events past it are dropped."""

    def __init__(self, limit: int) -> None:
        self.left = limit
        self.dropped = False

    def emit(self, emit: Emit, event: Dict[str, Any]) -> None:
        size = len(json.dumps(event))
        if size > self.left:
            self.dropped = True
            return
        self.left -= size
        emit(event)


class Driver:
    def __init__(self, workspace: str, runtime_dir: str) -> None:
        self.workspace = workspace
        self.runtime_dir = runtime_dir
        self.lock = threading.Lock()
        self.contexts: Dict[str, Context] = {}

    def _get(self, cid: str) -> Context:
        cid = cid or DEFAULT_CONTEXT
        with self.lock:
            ctx = self.contexts.get(cid)
            if ctx is None and cid == DEFAULT_CONTEXT:
                ctx = self._create_locked(DEFAULT_CONTEXT, "python", self.workspace)
        if ctx is None:
            raise Fail("not_found", f"no code context {cid}")
        return ctx

    def _create_locked(self, cid: str, language: str, cwd: str) -> Context:
        if language not in KERNELS:
            raise Fail("unsupported", f"language {language!r} is not available; use one of {sorted(KERNELS)}")
        if len(self.contexts) >= MAX_CONTEXTS:
            raise Fail("limit", f"the session keeps at most {MAX_CONTEXTS} code contexts; remove one first")
        ctx = Context(cid, language, cwd, self.runtime_dir)
        self.contexts[cid] = ctx
        return ctx

    def handle(self, req: Dict[str, Any], emit: Emit) -> None:
        op = req.get("op")
        if op == "run":
            code = req.get("code")
            if not isinstance(code, str):
                raise Fail("invalid_request", "code must be text")
            timeout_s = max(int(req.get("timeout_ms") or 60_000), 1) / 1000
            budget = Budget(int(req.get("max_output_bytes") or (1 << 20)))
            self._get(str(req.get("context") or "")).run(code, timeout_s, budget, emit)
        elif op == "create":
            cwd = str(req.get("cwd") or self.workspace)
            if not os.path.isdir(cwd):
                raise Fail("not_found", f"{cwd} is not a directory")
            with self.lock:
                ctx = self._create_locked(uuid.uuid4().hex[:16], str(req.get("language") or "python"), cwd)
            emit({"type": "context", **ctx.view()})
        elif op == "list":
            with self.lock:
                views = [c.view() for c in self.contexts.values()]
            emit({"type": "contexts", "contexts": views})
        elif op == "restart":
            ctx = self._get(str(req.get("context") or ""))
            with ctx.lock:
                ctx.restart()
            emit({"type": "ok"})
        elif op == "remove":
            cid = str(req.get("context") or "")
            with self.lock:
                ctx = self.contexts.pop(cid, None)
            if ctx is None:
                raise Fail("not_found", f"no code context {cid}")
            with ctx.lock:
                ctx.shutdown()
            emit({"type": "ok"})
        else:
            raise Fail("invalid_request", f"unknown op {op!r}")


def serve(socket_path: str, workspace: str) -> None:
    # The socket's directory is private to the session's user, and holds the
    # kernels' sockets and connection files too.
    runtime_dir = os.path.dirname(os.path.abspath(socket_path))
    os.makedirs(runtime_dir, mode=0o700, exist_ok=True)
    os.chmod(runtime_dir, 0o700)
    driver = Driver(workspace, runtime_dir)

    class Handler(socketserver.StreamRequestHandler):
        def handle(self) -> None:
            def emit(event: Dict[str, Any]) -> None:
                self.wfile.write(json.dumps(event).encode() + b"\n")
                self.wfile.flush()

            line = self.rfile.readline(REQUEST_MAX_BYTES + 1)
            try:
                if len(line) > REQUEST_MAX_BYTES:
                    raise Fail("too_large", "the request is too large")
                req = json.loads(line)
                if not isinstance(req, dict):
                    raise Fail("invalid_request", "the request must be a JSON object")
                driver.handle(req, emit)
            except Fail as err:
                emit({"type": "fail", "code": err.code, "message": err.message})
            except (BrokenPipeError, ConnectionResetError):
                pass
            except Exception as err:  # noqa: BLE001 - every failure is answered, never crashes the driver
                emit({"type": "fail", "code": "internal", "message": f"{type(err).__name__}: {err}"})

    class Server(socketserver.ThreadingUnixStreamServer):
        daemon_threads = True

    if os.path.exists(socket_path):
        os.unlink(socket_path)
    with Server(socket_path, Handler) as server:
        os.chmod(socket_path, 0o600)
        server.serve_forever()


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--socket", required=True)
    parser.add_argument("--workspace", default="/workspace")
    args = parser.parse_args()
    serve(args.socket, args.workspace)


if __name__ == "__main__":
    main()
