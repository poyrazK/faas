#!/usr/bin/env python3
"""Gregale code interpreter driver (ADR-20261027090013 step 14).

guest-init starts this once per session, as the session's user, and talks
to it over a Unix socket: one JSON request line per connection, answered
with JSON event lines. Each code context is its own Jupyter kernel
(ipykernel through jupyter_client), so variables, imports and the working
directory persist between runs of the same context.

Requests (env is the session's environment, sent with every request so a
kernel follows changes to it; max_contexts is the session's context cap):
  {"op": "run", "context": "<id or empty>", "code": "...",
   "timeout_ms": n, "max_output_bytes": n, "env": {...}, "max_contexts": n}
      -> {"type": "stdout"|"stderr", "text": "..."}*
         {"type": "result", "data": {mime: value}, "is_main_result": bool}*
         {"type": "error", "name": "...", "value": "...", "traceback": "..."}?
         {"type": "done", "execution_count": n|null, "timed_out": bool,
          "output_truncated": bool}
  {"op": "create", "language": "python", "cwd": "...", "env": {...},
   "max_contexts": n} -> {"type": "context", ...}
  {"op": "list"} -> {"type": "contexts", "contexts": [...]}
  {"op": "restart"|"remove", "context": "<id>"} -> {"type": "ok"}
Any failure answers {"type": "fail", "code": "...", "message": "..."}.

An empty context names the default one, created on first use in the
workspace. A run's timeout counts from when its context is ready, waiting
for an earlier run included. A run that passes it is interrupted; a kernel
that does not stop within INTERRUPT_GRACE_S is restarted, losing that
context's state. A kernel that dies mid-run ends the run with a KernelDied
error and is started again. Restart and remove cancel a run in progress. A
run whose caller has gone is interrupted, so its timeout still holds.

The error event is not counted against the output budget, so a run that
fills its output still reports its exception; its traceback is cut to
TRACEBACK_MAX_BYTES.

Each kernel's pid is written to kernel-<id>.pid in the runtime directory,
so guest-init can stop kernels the driver leaves behind if it dies.
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
DEFAULT_MAX_CONTEXTS = 8
INTERRUPT_GRACE_S = 5.0
KERNEL_READY_TIMEOUT_S = 60.0
ENV_SYNC_TIMEOUT_S = 10.0
REQUEST_MAX_BYTES = 4 << 20
TRACEBACK_MAX_BYTES = 64 << 10
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


class CallerGone(Exception):
    """The caller's connection closed while a run was answering it."""


def env_of(req: Dict[str, Any]) -> Dict[str, str]:
    env = req.get("env") or {}
    if not isinstance(env, dict):
        raise Fail("invalid_request", "env must be an object")
    return {str(k): str(v) for k, v in env.items()}


class Context:
    def __init__(self, cid: str, language: str, cwd: str, runtime_dir: str, session_env: Dict[str, str]) -> None:
        self.id = cid
        self.language = language
        self.cwd = cwd
        self.runtime_dir = runtime_dir
        self.lock = threading.Lock()
        # Set by restart and remove: the run holding the lock stops at once.
        self.cancel = threading.Event()
        # The session variables this kernel was given, over the driver's own.
        self.session_env: Dict[str, str] = {}
        # IPC transport: the kernel's channels are Unix sockets in the
        # driver's private directory, never listening TCP ports.
        self.km = KernelManager(kernel_name=KERNELS[language], transport="ipc", ip=os.path.join(runtime_dir, f"kernel-{cid}"))
        self.km.connection_file = os.path.join(runtime_dir, f"kernel-{cid}.json")
        try:
            self._start(session_env)
        except BaseException:
            self._stop_kernel()
            raise

    def _pid_file(self) -> str:
        return os.path.join(self.runtime_dir, f"kernel-{self.id}.pid")

    def _write_pid(self) -> None:
        provisioner = getattr(self.km, "provisioner", None)
        pid = getattr(provisioner, "pid", None) or getattr(getattr(self.km, "kernel", None), "pid", None)
        if pid:
            with open(self._pid_file(), "w", encoding="ascii") as f:
                f.write(str(pid))

    def _env(self, session_env: Dict[str, str]) -> Dict[str, str]:
        env = dict(os.environ)
        env.update(session_env)
        env.setdefault("MPLBACKEND", "module://matplotlib_inline.backend_inline")
        return env

    def _start(self, session_env: Dict[str, str]) -> None:
        self.km.start_kernel(cwd=self.cwd, env=self._env(session_env))
        self.session_env = dict(session_env)
        self._connect()

    def _connect(self) -> None:
        self._write_pid()
        self.kc = self.km.blocking_client()
        self.kc.start_channels()
        self.kc.wait_for_ready(timeout=KERNEL_READY_TIMEOUT_S)

    def _stop_kernel(self) -> None:
        try:
            kc = getattr(self, "kc", None)
            if kc is not None:
                kc.stop_channels()
        finally:
            try:
                self.km.shutdown_kernel(now=True)
            finally:
                try:
                    os.unlink(self._pid_file())
                except OSError:
                    pass

    def view(self) -> Dict[str, str]:
        return {"id": self.id, "language": self.language, "cwd": self.cwd}

    def restart(self) -> None:
        """Start the kernel again with the session environment it last had.
        The caller holds the lock. restart_kernel works on a live or a dead
        kernel; stopping the manager and starting it again does not (its
        IPC channels are gone)."""
        self.kc.stop_channels()
        self.km.restart_kernel(now=True, env=self._env(self.session_env))
        self._connect()

    def shutdown(self) -> None:
        self._stop_kernel()

    def sync_env(self, session_env: Dict[str, str]) -> None:
        """Give a running kernel the session's environment as it is now.
        Variables the session no longer sets go back to the driver's own
        value, or away. Silent: nothing is echoed or kept in history."""
        if session_env == self.session_env:
            return
        restore = {k: os.environ.get(k) for k in self.session_env if k not in session_env}
        payload = json.dumps({"set": session_env, "restore": restore})
        code = (
            "def _gregale_env(p):\n"
            "    import json, os\n"
            "    p = json.loads(p)\n"
            "    os.environ.update(p['set'])\n"
            "    for k, v in p['restore'].items():\n"
            "        if v is None:\n"
            "            os.environ.pop(k, None)\n"
            "        else:\n"
            "            os.environ[k] = v\n"
            f"_gregale_env({payload!r})\n"
            "del _gregale_env\n"
        )
        reply = self.kc.execute(code, silent=True, store_history=False, allow_stdin=False, reply=True, timeout=ENV_SYNC_TIMEOUT_S)
        if reply.get("content", {}).get("status") != "ok":
            raise Fail("internal", "the kernel did not take the session's environment")
        self.session_env = dict(session_env)

    def run(self, code: str, timeout_s: float, budget: "Budget", emit: Emit, session_env: Dict[str, str]) -> None:
        deadline = time.monotonic() + timeout_s
        if not self.lock.acquire(timeout=max(timeout_s, 0.001)):
            # An earlier run held the context past this one's whole timeout:
            # this code never ran.
            emit({"type": "done", "execution_count": None, "timed_out": True, "output_truncated": False})
            return
        try:
            if self.cancel.is_set():
                raise Fail("not_found", f"code context {self.id} is being restarted or removed")
            self.sync_env(session_env)
            self._run_locked(code, deadline, budget, emit)
        finally:
            self.lock.release()

    def _run_locked(self, code: str, deadline: float, budget: "Budget", emit: Emit) -> None:
        msg_id = self.kc.execute(code, store_history=True, allow_stdin=False)
        count: Optional[int] = None
        timed_out = interrupted = False

        def send(event: Dict[str, Any], counted: bool = True) -> None:
            try:
                if counted:
                    budget.emit(emit, event)
                else:
                    emit(event)
            except (BrokenPipeError, ConnectionResetError) as err:
                raise CallerGone() from err

        try:
            while True:
                if self.cancel.is_set():
                    self.km.interrupt_kernel()
                    send({"type": "error", "name": "ContextRestarted", "value": "the code context was restarted or removed during the run", "traceback": ""}, counted=False)
                    break
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
                    if not self.km.is_alive():
                        # Killed under the run (out of memory, most often).
                        self.restart()
                        send({"type": "error", "name": "KernelDied", "value": "the kernel died during the run and was started again; its state is lost", "traceback": ""}, counted=False)
                        break
                    continue
                if msg.get("parent_header", {}).get("msg_id") != msg_id:
                    continue
                kind, content = msg["msg_type"], msg["content"]
                if kind == "execute_input":
                    count = content.get("execution_count")
                elif kind == "stream":
                    send({"type": content.get("name", "stdout"), "text": content.get("text", "")})
                elif kind in ("display_data", "execute_result"):
                    data = {k: v for k, v in content.get("data", {}).items() if k in KEPT_MIME}
                    if data:
                        send({"type": "result", "data": data, "is_main_result": kind == "execute_result"})
                elif kind == "error":
                    traceback = "\n".join(content.get("traceback", []))
                    if len(traceback) > TRACEBACK_MAX_BYTES:
                        traceback = traceback[-TRACEBACK_MAX_BYTES:]
                    send({
                        "type": "error",
                        "name": content.get("ename", ""),
                        "value": str(content.get("evalue", ""))[:TRACEBACK_MAX_BYTES],
                        "traceback": traceback,
                    }, counted=False)
                elif kind == "status" and content.get("execution_state") == "idle":
                    break
            send({"type": "done", "execution_count": count, "timed_out": timed_out, "output_truncated": budget.dropped}, counted=False)
        except CallerGone:
            # Nobody reads the run any more: stop it so its timeout holds.
            self._abandon(msg_id)
            raise

    def _abandon(self, msg_id: str) -> None:
        self.km.interrupt_kernel()
        until = time.monotonic() + INTERRUPT_GRACE_S
        while time.monotonic() < until:
            try:
                msg = self.kc.get_iopub_msg(timeout=0.5)
            except queue.Empty:
                continue
            if (msg.get("parent_header", {}).get("msg_id") == msg_id and msg["msg_type"] == "status"
                    and msg["content"].get("execution_state") == "idle"):
                return
        self.restart()


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
        # Contexts being started: a kernel starts outside self.lock, so a
        # slow start never holds up runs in other contexts.
        self.pending: Dict[str, threading.Event] = {}

    def _reserve_locked(self, cid: str, max_contexts: int) -> threading.Event:
        have = len(self.contexts) + len(self.pending)
        if have >= max_contexts:
            raise Fail("limit", f"the session keeps at most {max_contexts} code contexts (it has {have}); remove one first")
        ready = threading.Event()
        self.pending[cid] = ready
        return ready

    def _start(self, cid: str, language: str, cwd: str, env: Dict[str, str], ready: threading.Event) -> Context:
        try:
            ctx = Context(cid, language, cwd, self.runtime_dir, env)
        except BaseException:
            with self.lock:
                self.pending.pop(cid, None)
            ready.set()
            raise
        with self.lock:
            self.pending.pop(cid, None)
            self.contexts[cid] = ctx
        ready.set()
        return ctx

    def _get(self, cid: str, env: Dict[str, str], max_contexts: int) -> Context:
        cid = cid or DEFAULT_CONTEXT
        start = False
        with self.lock:
            ctx = self.contexts.get(cid)
            if ctx is not None:
                return ctx
            if cid != DEFAULT_CONTEXT:
                raise Fail("not_found", f"no code context {cid}")
            ready = self.pending.get(cid)
            if ready is None:
                ready = self._reserve_locked(cid, max_contexts)
                start = True
        if start:
            return self._start(cid, "python", self.workspace, env, ready)
        ready.wait(KERNEL_READY_TIMEOUT_S + 10)
        with self.lock:
            ctx = self.contexts.get(cid)
        if ctx is None:
            raise Fail("internal", "the default code context did not start")
        return ctx

    def handle(self, req: Dict[str, Any], emit: Emit) -> None:
        op = req.get("op")
        max_contexts = int(req.get("max_contexts") or DEFAULT_MAX_CONTEXTS)
        if op == "run":
            code = req.get("code")
            if not isinstance(code, str):
                raise Fail("invalid_request", "code must be text")
            env = env_of(req)
            timeout_s = max(int(req.get("timeout_ms") or 60_000), 1) / 1000
            budget = Budget(int(req.get("max_output_bytes") or (1 << 20)))
            self._get(str(req.get("context") or ""), env, max_contexts).run(code, timeout_s, budget, emit, env)
        elif op == "create":
            language = str(req.get("language") or "python")
            if language not in KERNELS:
                raise Fail("unsupported", f"language {language!r} is not available; use one of {sorted(KERNELS)}")
            cwd = str(req.get("cwd") or self.workspace)
            if not os.path.isdir(cwd):
                raise Fail("not_found", f"{cwd} is not a directory")
            cid = uuid.uuid4().hex[:16]
            with self.lock:
                ready = self._reserve_locked(cid, max_contexts)
            ctx = self._start(cid, language, cwd, env_of(req), ready)
            emit({"type": "context", **ctx.view()})
        elif op == "list":
            with self.lock:
                views = [c.view() for c in self.contexts.values()]
            emit({"type": "contexts", "contexts": views})
        elif op == "restart":
            cid = str(req.get("context") or "")
            with self.lock:
                ctx = self.contexts.get(cid)
            if ctx is None:
                raise Fail("not_found", f"no code context {cid}")
            ctx.cancel.set()
            try:
                with ctx.lock:
                    ctx.restart()
            finally:
                ctx.cancel.clear()
            emit({"type": "ok"})
        elif op == "remove":
            cid = str(req.get("context") or "")
            with self.lock:
                ctx = self.contexts.pop(cid, None)
            if ctx is None:
                raise Fail("not_found", f"no code context {cid}")
            ctx.cancel.set()
            with ctx.lock:
                ctx.shutdown()
            emit({"type": "ok"})
        else:
            raise Fail("invalid_request", f"unknown op {op!r}")


def serve(socket_path: str, workspace: str) -> None:
    # The socket's directory is private to the session's user, and holds the
    # kernels' sockets, connection files and pid files too.
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
            except (CallerGone, BrokenPipeError, ConnectionResetError):
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
