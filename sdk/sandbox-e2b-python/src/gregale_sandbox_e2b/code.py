"""The code interpreter (ADR-20261027090013 step 14): run_code and code contexts.

Names follow E2B Code Interpreter. A session created with template
"code-interpreter" runs code in Jupyter kernels; each context is its own
kernel, so variables and imports persist between runs.
"""

from __future__ import annotations

import json
import time
from dataclasses import dataclass, field
from typing import Any, Callable, Dict, List, Optional, Union

from .connection import Connection
from .errors import SandboxException, UnsupportedException

_FIELDS = {
    "text/plain": "text",
    "text/html": "html",
    "text/markdown": "markdown",
    "image/svg+xml": "svg",
    "image/png": "png",
    "image/jpeg": "jpeg",
    "application/pdf": "pdf",
    "text/latex": "latex",
    "application/json": "json",
    "application/javascript": "javascript",
}


class Result:
    """A rich output of a run: a display, or the run's value (is_main_result).

    png, jpeg and pdf are base64 text.
    """

    def __init__(self, data: Dict[str, Any], is_main_result: bool = False) -> None:
        self.raw = data
        self.is_main_result = is_main_result
        for mime, name in _FIELDS.items():
            setattr(self, name, data.get(mime))

    text: Optional[str]
    html: Optional[str]
    markdown: Optional[str]
    svg: Optional[str]
    png: Optional[str]
    jpeg: Optional[str]
    pdf: Optional[str]
    latex: Optional[str]
    json: Any
    javascript: Optional[str]

    def formats(self) -> List[str]:
        """The formats this result is available in."""
        return [name for name in _FIELDS.values() if getattr(self, name) is not None]

    def __repr__(self) -> str:
        return f"Result(formats={self.formats()}, is_main_result={self.is_main_result})"


@dataclass
class ExecutionError:
    name: str
    value: str
    traceback: str


@dataclass
class Logs:
    stdout: List[str] = field(default_factory=list)
    stderr: List[str] = field(default_factory=list)


@dataclass
class OutputMessage:
    """A chunk of a run's stdout or stderr, as on_stdout and on_stderr receive it."""

    line: str
    timestamp: int
    error: bool = False


@dataclass
class Execution:
    results: List[Result] = field(default_factory=list)
    logs: Logs = field(default_factory=Logs)
    error: Optional[ExecutionError] = None
    execution_count: Optional[int] = None

    @property
    def text(self) -> Optional[str]:
        """The text of the run's value, when it had one."""
        for r in self.results:
            if r.is_main_result:
                return r.text
        return None


@dataclass
class Context:
    """A code context: its own kernel, language and working directory."""

    id: str
    language: str
    cwd: str


def _context_id(ctx: Union[Context, str]) -> str:
    return ctx.id if isinstance(ctx, Context) else ctx


def _execution(wire: Dict[str, Any], logs: Optional[Logs] = None) -> Execution:
    err = wire.get("error")
    wl = wire.get("logs") or {}
    return Execution(
        results=[Result(r.get("data") or {}, bool(r.get("is_main_result"))) for r in wire.get("results") or []],
        logs=logs if logs is not None else Logs(list(wl.get("stdout") or []), list(wl.get("stderr") or [])),
        error=ExecutionError(err["name"], err["value"], err["traceback"]) if err else None,
        execution_count=wire.get("execution_count"),
    )


class CodeInterpreter:
    def __init__(self, conn: Connection) -> None:
        self._conn = conn

    def run_code(
        self,
        code: str,
        language: Optional[str] = None,
        context: Optional[Union[Context, str]] = None,
        on_stdout: Optional[Callable[[OutputMessage], None]] = None,
        on_stderr: Optional[Callable[[OutputMessage], None]] = None,
        on_result: Optional[Callable[[Result], None]] = None,
        on_error: Optional[Callable[[ExecutionError], None]] = None,
        envs: Optional[Dict[str, str]] = None,
        timeout: Optional[float] = None,
        request_timeout: Optional[float] = None,
    ) -> Execution:
        if envs:
            raise UnsupportedException("run_code(envs=...)", "set the session environment with sandbox.set_envs before the first run")
        body = {
            "code": code,
            "context": _context_id(context) if context is not None else None,
            "language": language,
            "timeout_ms": int(timeout * 1000) if timeout else None,
        }
        if on_stdout or on_stderr:
            execution = self._stream(body, on_stdout, on_stderr, request_timeout)
        else:
            execution = _execution(self._conn.json("POST", "/code", json_body=body, timeout=request_timeout))
        for r in execution.results:
            if on_result:
                on_result(r)
        if execution.error and on_error:
            on_error(execution.error)
        return execution

    def _stream(self, body: Dict[str, Any], on_stdout: Any, on_stderr: Any, request_timeout: Optional[float]) -> Execution:
        res = self._conn.call("POST", "/code", json_body=body, accept="text/event-stream", timeout=request_timeout)
        logs = Logs()
        event: Optional[str] = None
        data: Optional[str] = None
        try:
            for raw in res:
                line = raw.decode("utf-8").rstrip("\r\n")
                if line.startswith("event: "):
                    event = line[len("event: "):]
                elif line.startswith("data: "):
                    data = line[len("data: "):]
                elif line == "":
                    if event and data is not None:
                        value = json.loads(data)
                        if event in ("stdout", "stderr"):
                            getattr(logs, event).append(value)
                            handler = on_stdout if event == "stdout" else on_stderr
                            if handler:
                                handler(OutputMessage(value, time.time_ns() // 1000, event == "stderr"))
                        elif event == "result":
                            return _execution(value, logs)
                        elif event == "error":
                            raise SandboxException(value.get("detail") or "the run failed")
                    event, data = None, None
        finally:
            res.close()
        raise SandboxException("the run ended without a result")

    def create_code_context(self, cwd: Optional[str] = None, language: Optional[str] = None, request_timeout: Optional[float] = None) -> Context:
        v = self._conn.json("POST", "/code/contexts", json_body={"cwd": cwd, "language": language}, timeout=request_timeout)
        return Context(v["id"], v["language"], v["cwd"])

    def list_code_contexts(self, request_timeout: Optional[float] = None) -> List[Context]:
        res = self._conn.json("GET", "/code/contexts", timeout=request_timeout)
        return [Context(c["id"], c["language"], c["cwd"]) for c in res.get("contexts") or []]

    def restart_code_context(self, context: Union[Context, str], request_timeout: Optional[float] = None) -> None:
        """Restarts a context's kernel; its variables and imports are lost."""
        self._conn.call("POST", f"/code/contexts/{_context_id(context)}/restart", timeout=request_timeout).close()

    def remove_code_context(self, context: Union[Context, str], request_timeout: Optional[float] = None) -> None:
        self._conn.call("DELETE", f"/code/contexts/{_context_id(context)}", timeout=request_timeout).close()
