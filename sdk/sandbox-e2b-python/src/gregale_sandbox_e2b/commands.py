"""sandbox.commands and sandbox.pty: commands in the session, and terminals.

Gregale names a background command by a handle; this module gives each a
number, which is what callers know as its pid. A terminal is a background
command on a pseudo-terminal: its output is polled like any background
command's, and its input, end of input and resizes go to
POST .../exec/{handle}/input.
"""

from __future__ import annotations

import base64
import json
import time
from dataclasses import dataclass, field
from typing import Any, Callable, Dict, Iterator, List, Optional, Tuple

from .connection import Connection
from .errors import CommandExitException, CommandResult, NotFoundException, UnsupportedException

POLL_S = 0.2

OutputHandler = Callable[[str], None]
PtyHandler = Callable[[bytes], None]


@dataclass
class PtySize:
    """A terminal's size."""

    rows: int
    cols: int


@dataclass
class ProcessInfo:
    pid: int
    cmd: str
    args: List[str]
    envs: Dict[str, str] = field(default_factory=dict)
    tag: Optional[str] = None
    cwd: Optional[str] = None


def _run_as(user: Optional[str]) -> Optional[str]:
    if user is None or user in ("user", "app"):
        return None
    if user == "root":
        return "root"
    raise UnsupportedException(f'user "{user}"', 'Gregale sandboxes run commands as "user" (unprivileged) or "root"')


def _result_of(res: Dict[str, Any], stdout: Optional[str] = None, stderr: Optional[str] = None) -> CommandResult:
    error = "the command timed out" if res.get("timed_out") else "the command was killed" if res.get("killed") else None
    return CommandResult(
        stdout=res.get("stdout", "") if stdout is None else stdout,
        stderr=res.get("stderr", "") if stderr is None else stderr,
        exit_code=int(res.get("exit_code", 0)),
        error=error,
    )


def _decode(b64: Optional[str]) -> str:
    return base64.b64decode(b64).decode("utf-8", "replace") if b64 else ""


class CommandHandle:
    """A background command, polled for its output.

    Output handlers given to run() or wait() are called while wait() or
    iteration follows the command.
    """

    def __init__(
        self,
        pid: int,
        conn: Connection,
        handle: str,
        on_stdout: Optional[OutputHandler] = None,
        on_stderr: Optional[OutputHandler] = None,
        tty: bool = False,
    ) -> None:
        self.pid = pid
        self._conn = conn
        self._handle = handle
        self._tty = tty
        self._on_stdout = on_stdout
        self._on_stderr = on_stderr
        self._stdout = ""
        self._stderr = ""
        self._offsets = (0, 0)
        self._exit_code: Optional[int] = None
        self._disconnected = False

    @property
    def stdout(self) -> str:
        return self._stdout

    @property
    def stderr(self) -> str:
        return self._stderr

    @property
    def exit_code(self) -> Optional[int]:
        return self._exit_code

    @property
    def error(self) -> Optional[str]:
        return None

    def __iter__(self) -> Iterator[Tuple[Optional[str], Optional[str], Optional[bytes]]]:
        """Yields (stdout, stderr, pty) chunks until the command ends.

        A terminal's output comes as pty bytes; other commands' as text.
        """
        while True:
            out = self._conn.json("GET", f"/exec/{self._handle}", query={"stdout_offset": self._offsets[0], "stderr_offset": self._offsets[1]})
            raw = base64.b64decode(out["stdout"]) if out.get("stdout") else b""
            stdout, stderr = raw.decode("utf-8", "replace"), _decode(out.get("stderr"))
            offsets = (int(out.get("stdout_offset", 0)), int(out.get("stderr_offset", 0)))
            progressed = offsets != self._offsets
            self._offsets = offsets
            if raw and self._tty:
                self._stdout += stdout
                yield None, None, raw
            elif stdout:
                self._stdout += stdout
                yield stdout, None, None
            if stderr:
                self._stderr += stderr
                yield None, stderr, None
            if not out.get("running") and not progressed:
                self._exit_code = int(out.get("exit_code") or 0)
                return
            if self._disconnected:
                return
            if out.get("running"):
                time.sleep(POLL_S)

    def wait(
        self,
        on_stdout: Optional[OutputHandler] = None,
        on_stderr: Optional[OutputHandler] = None,
        on_pty: Optional[PtyHandler] = None,
    ) -> CommandResult:
        """Waits for the command to end. A non-zero exit raises CommandExitException."""
        on_stdout = on_stdout or self._on_stdout
        on_stderr = on_stderr or self._on_stderr
        for stdout, stderr, pty in self:
            if pty and on_pty:
                on_pty(pty)
            if stdout and on_stdout:
                on_stdout(stdout)
            if stderr and on_stderr:
                on_stderr(stderr)
        if self._exit_code is None:
            return CommandResult(self._stdout, self._stderr, -1, "disconnected")
        result = CommandResult(self._stdout, self._stderr, self._exit_code)
        if result.exit_code != 0:
            raise CommandExitException(result)
        return result

    def disconnect(self) -> None:
        """Stops following the output; the command keeps running."""
        self._disconnected = True

    def kill(self) -> bool:
        try:
            self._conn.call("DELETE", f"/exec/{self._handle}").close()
            return True
        except NotFoundException:
            return False

    def send_stdin(self, data: str, request_timeout: Optional[float] = None) -> None:
        """Writes to the command's stdin."""
        _send_input(self._conn, self._handle, {"data": _b64(data)}, request_timeout)


def _b64(data: Any) -> str:
    return base64.b64encode(data.encode() if isinstance(data, str) else bytes(data)).decode()


def _send_input(conn: Connection, handle: str, body: Dict[str, Any], request_timeout: Optional[float]) -> None:
    conn.call("POST", f"/exec/{handle}/input", json_body=body, timeout=request_timeout).close()


class Commands:
    def __init__(self, conn: Connection) -> None:
        self._conn = conn
        self._pids: Dict[str, int] = {}
        self._handles: Dict[int, str] = {}
        self._next_pid = 1

    def _pid(self, handle: str) -> int:
        if handle not in self._pids:
            self._pids[handle] = self._next_pid
            self._handles[self._next_pid] = handle
            self._next_pid += 1
        return self._pids[handle]

    def _handle_of(self, pid: int) -> str:
        if pid not in self._handles:
            raise NotFoundException(f"no command with pid {pid} in this client; commands.list() names the running ones")
        return self._handles[pid]

    def run(
        self,
        cmd: str,
        background: bool = False,
        envs: Optional[Dict[str, str]] = None,
        user: Optional[str] = None,
        cwd: Optional[str] = None,
        on_stdout: Optional[OutputHandler] = None,
        on_stderr: Optional[OutputHandler] = None,
        stdin: bool = False,
        timeout: Optional[float] = None,
        request_timeout: Optional[float] = None,
    ) -> Any:
        """Runs a shell command.

        In the foreground it answers a CommandResult, and a non-zero exit
        raises CommandExitException; with background=True it answers a
        CommandHandle at once. timeout is how long the command may run, in
        seconds (60 by default, within the plan's cap).
        """
        if stdin and not background:
            raise UnsupportedException("commands.run(stdin=True)", "only background commands take input; run it with background=True and use send_stdin")
        merged = {**self._conn.envs, **(envs or {})}
        body = {
            "cmd": ["/bin/bash", "-l", "-c", cmd],
            "workdir": cwd,
            "env": merged or None,
            "run_as": _run_as(user),
            "timeout_ms": int(timeout * 1000) if timeout else None,
            "background": True if background else None,
        }
        if background:
            res = self._conn.json("POST", "/exec", json_body=body, timeout=request_timeout)
            handle = res["handle"]
            return CommandHandle(self._pid(handle), self._conn, handle, on_stdout, on_stderr)
        if on_stdout or on_stderr:
            result = self._stream(body, on_stdout, on_stderr, request_timeout)
        else:
            result = _result_of(self._conn.json("POST", "/exec", json_body=body, timeout=request_timeout))
        if result.exit_code != 0:
            raise CommandExitException(result)
        return result

    def _stream(self, body: Dict[str, Any], on_stdout: Optional[OutputHandler], on_stderr: Optional[OutputHandler], request_timeout: Optional[float]) -> CommandResult:
        """A foreground command over server-sent events, calling back per chunk."""
        res = self._conn.call("POST", "/exec", json_body=body, accept="text/event-stream", timeout=request_timeout)
        stdout, stderr = "", ""
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
                        if event == "stdout":
                            stdout += value
                            if on_stdout:
                                on_stdout(value)
                        elif event == "stderr":
                            stderr += value
                            if on_stderr:
                                on_stderr(value)
                        elif event == "result":
                            return _result_of(value, stdout, stderr)
                        elif event == "error":
                            raise CommandExitException(CommandResult(stdout, stderr, -1, value.get("detail") or "the command failed"))
                    event, data = None, None
        finally:
            res.close()
        return CommandResult(stdout, stderr, -1, "the stream ended without a result")

    def list(self, request_timeout: Optional[float] = None) -> List[ProcessInfo]:
        """The session's running background commands."""
        res = self._conn.json("GET", "/exec", timeout=request_timeout)
        return [
            ProcessInfo(pid=self._pid(c["handle"]), tag=c["handle"], cmd=(c.get("argv") or [""])[0], args=(c.get("argv") or [])[1:])
            for c in res.get("commands", [])
            if c.get("running")
        ]

    def kill(self, pid: int, request_timeout: Optional[float] = None) -> bool:
        """Kills a background command; False when there is none."""
        try:
            self._conn.call("DELETE", f"/exec/{self._handle_of(pid)}", timeout=request_timeout).close()
            return True
        except NotFoundException:
            return False

    def connect(self, pid: int, on_stdout: Optional[OutputHandler] = None, on_stderr: Optional[OutputHandler] = None) -> CommandHandle:
        """Follows a background command again, from the start of its kept output."""
        return CommandHandle(pid, self._conn, self._handle_of(pid), on_stdout, on_stderr)

    def send_stdin(self, pid: int, data: str, request_timeout: Optional[float] = None) -> None:
        """Writes to a background command's stdin."""
        _send_input(self._conn, self._handle_of(pid), {"data": _b64(data)}, request_timeout)

    def close_stdin(self, pid: int, request_timeout: Optional[float] = None) -> None:
        """Ends a background command's input: it reads end of file."""
        _send_input(self._conn, self._handle_of(pid), {"eof": True}, request_timeout)

    def _input(self, pid: int, body: Dict[str, Any], request_timeout: Optional[float]) -> None:
        _send_input(self._conn, self._handle_of(pid), body, request_timeout)

    def _start_terminal(
        self,
        size: PtySize,
        user: Optional[str],
        cwd: Optional[str],
        envs: Optional[Dict[str, str]],
        timeout: Optional[float],
        request_timeout: Optional[float],
    ) -> CommandHandle:
        merged = {**self._conn.envs, **(envs or {})}
        body = {
            "cmd": ["/bin/bash", "-i", "-l"],
            "workdir": cwd,
            "env": merged or None,
            "run_as": _run_as(user),
            "timeout_ms": int(timeout * 1000) if timeout else None,
            "background": True,
            "tty": True,
            "rows": size.rows,
            "cols": size.cols,
        }
        handle = self._conn.json("POST", "/exec", json_body=body, timeout=request_timeout)["handle"]
        return CommandHandle(self._pid(handle), self._conn, handle, tty=True)


class Pty:
    """sandbox.pty: interactive terminals, as background commands on a pseudo-terminal.

    Read a terminal's output by iterating its handle (the third item of each
    chunk) or with handle.wait(on_pty=...).
    """

    def __init__(self, commands: Commands) -> None:
        self._commands = commands

    def create(
        self,
        size: PtySize,
        user: Optional[str] = None,
        cwd: Optional[str] = None,
        envs: Optional[Dict[str, str]] = None,
        timeout: Optional[float] = 60,
        request_timeout: Optional[float] = None,
    ) -> CommandHandle:
        """Starts a login shell on a terminal of the given size.

        timeout is how long it may run, in seconds, within the plan's cap.
        """
        return self._commands._start_terminal(size, user, cwd, envs, timeout, request_timeout)

    def send_stdin(self, pid: int, data: bytes, request_timeout: Optional[float] = None) -> None:
        """Types into the terminal."""
        self._commands._input(pid, {"data": _b64(data)}, request_timeout)

    def resize(self, pid: int, size: PtySize, request_timeout: Optional[float] = None) -> None:
        self._commands._input(pid, {"rows": size.rows, "cols": size.cols}, request_timeout)

    def kill(self, pid: int, request_timeout: Optional[float] = None) -> bool:
        """Kills the terminal's shell; False when there is none."""
        return bool(self._commands.kill(pid, request_timeout=request_timeout))
