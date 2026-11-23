"""Sandbox: an E2B-style handle on a Gregale sandbox session
(ADR-20261027090013 §12).

Written from E2B's public documentation; code moves by changing the import
and the API key.
"""

from __future__ import annotations

import math
import re
from dataclasses import dataclass, field
from datetime import datetime
from enum import Enum
from typing import Any, Callable, Dict, List, Optional
from urllib.parse import urlsplit

from .api import Api, ApiError, Transport, to_sandbox_error
from .code import CodeInterpreter, Context, Execution
from .commands import Commands, Pty
import time

from .connection import PAUSED, Connection, parse_time
from .errors import InvalidArgumentException, NotFoundException, SandboxNotFoundException, TimeoutException, UnsupportedException
from .filesystem import Filesystem

#: The harness presets a template name may be.
PRESETS = {"codex", "opencode", "claude-code", "code-interpreter"}
#: Template names E2B code commonly passes for the stock image.
DEFAULT_TEMPLATES = {"base", "default"}

_SANDBOX_ID = re.compile(r"^[0-9a-f-]{36}$")
_LIVE = {"queued", "starting", "ready", "frozen"}
#: Sessions Sandbox.list shows: running ones and paused persistent ones.
_LISTED = _LIVE | PAUSED


class SandboxState(str, Enum):
    RUNNING = "running"
    PAUSED = "paused"


@dataclass
class SandboxInfo:
    sandbox_id: str
    template_id: str
    metadata: Dict[str, str]
    started_at: datetime
    end_at: datetime
    state: SandboxState
    cpu_count: int
    memory_mb: int
    name: Optional[str] = None


@dataclass
class SandboxQuery:
    metadata: Optional[Dict[str, str]] = None
    state: Optional[List[SandboxState]] = None


@dataclass
class _ApiOpts:
    api_key: Optional[str] = None
    api_base: Optional[str] = None
    request_timeout: Optional[float] = None
    transport: Optional[Transport] = None

    def api(self) -> Api:
        return Api(self.api_key, self.api_base, self.request_timeout, self.transport)


def _info(view: Dict[str, Any]) -> SandboxInfo:
    ram = int(view.get("ram_mb", 0))
    return SandboxInfo(
        sandbox_id=view["id"],
        template_id=view.get("template") or view.get("preset", ""),
        metadata=dict(view.get("labels") or {}),
        started_at=parse_time(view["created_at"]),
        end_at=parse_time(view["expires_at"]),
        state=SandboxState.PAUSED if view.get("state") == "frozen" or view.get("state") in PAUSED else SandboxState.RUNNING,
        cpu_count=int(view.get("vcpu") or 0) or max(1, round(ram / 1024)),
        memory_mb=ram,
    )


def _is_live(view: Dict[str, Any]) -> bool:
    return view.get("state") in _LIVE and not view.get("close_requested_at")


def _resume_if_paused(conn: Connection) -> None:
    """Asks for a paused persistent session's resume; wait_ready then waits for it."""
    if conn.read().get("state") in ("paused", "pausing"):
        conn.control("POST", "/resume")


def _check_id(sandbox_id: str) -> None:
    if not _SANDBOX_ID.match(sandbox_id):
        raise InvalidArgumentException(f'"{sandbox_id}" is not a Gregale sandbox ID')


class SandboxPaginator:
    """Pages of the account's live sessions, newest first."""

    def __init__(self, opts: _ApiOpts, query: Optional[SandboxQuery], limit: Optional[int]) -> None:
        self._api = opts.api()
        self._query = query or SandboxQuery()
        self._limit = limit or 100
        self._before: Optional[str] = None
        self._done = False

    @property
    def has_next(self) -> bool:
        return not self._done

    def next_items(self) -> List[SandboxInfo]:
        if self._done:
            return []
        params: Dict[str, Any] = {"limit": self._limit, "before": self._before}
        states = self._query.state
        # A paused session is frozen or paused (persistent): filtered here.
        if states and len(states) == 1 and states[0] == SandboxState.RUNNING:
            params["state"] = "ready"
        if self._query.metadata:
            params["selector"] = ",".join(f"{k}={v}" for k, v in self._query.metadata.items())
        try:
            page = self._api.control("GET", "/v1/sandboxes", query=params)
        except ApiError as err:
            raise to_sandbox_error(err, "Sandbox.list") from err
        self._before = page.get("next_before") or None
        self._done = not self._before
        infos = [_info(v) for v in page.get("items", []) if v.get("state") in _LISTED and not v.get("close_requested_at")]
        return [i for i in infos if not states or i.state in states]


class _ClassOrInstance:
    """A method that works on the class (with a sandbox ID) and on an instance."""

    def __init__(self, on_instance: Callable[..., Any], on_class: Callable[..., Any]) -> None:
        self._on_instance = on_instance
        self._on_class = on_class

    def __get__(self, obj: Any, objtype: Any = None) -> Callable[..., Any]:
        if obj is None:
            return lambda *a, **k: self._on_class(objtype, *a, **k)
        return lambda *a, **k: self._on_instance(obj, *a, **k)


class Sandbox:
    """A Gregale sandbox session behind E2B's names.

    Sandbox(...) and Sandbox.create(...) both create a session and wait
    until it runs. Use it as a context manager to close it on exit.
    """

    def __init__(
        self,
        template: Optional[str] = None,
        timeout: Optional[float] = None,
        metadata: Optional[Dict[str, str]] = None,
        envs: Optional[Dict[str, str]] = None,
        allow_internet_access: Optional[bool] = None,
        api_key: Optional[str] = None,
        api_base: Optional[str] = None,
        request_timeout: Optional[float] = None,
        *,
        network: Optional[str] = None,
        ram_mb: Optional[int] = None,
        lifetime: Optional[float] = None,
        ports: Optional[List[int]] = None,
        port_visibility: str = "private",
        snapshot_id: Optional[str] = None,
        kind: Optional[str] = None,
        volume_gb: Optional[int] = None,
        vcpu: Optional[int] = None,
        ready_timeout: float = 120.0,
        transport: Optional[Transport] = None,
        _conn: Optional[Connection] = None,
    ) -> None:
        self._port_visibility = port_visibility
        self._ports: Dict[int, Dict[str, Any]] = {}
        if _conn is not None:
            self._attach(_conn)
            return
        name = template or "base"
        # A name that is not a preset is one of the account's templates.
        source = {"preset": name} if name in PRESETS else {"preset": "codex"} if name in DEFAULT_TEMPLATES else {"template": name}
        if network is None and allow_internet_access is not None:
            network = "web" if allow_internet_access else "none"
        api = _ApiOpts(api_key, api_base, request_timeout, transport).api()
        try:
            view = api.control(
                "POST",
                "/v1/sandboxes",
                json_body={
                    **source,
                    "ram_mb": ram_mb,
                    "network": {"mode": network} if network else None,
                    "idle_timeout_s": math.ceil(timeout) if timeout else None,
                    "lifetime_s": math.ceil(lifetime) if lifetime else None,
                    "labels": metadata,
                    "snapshot_id": snapshot_id,
                    "kind": kind,
                    "volume_gb": volume_gb,
                    "vcpu": vcpu,
                },
            )
        except ApiError as err:
            raise to_sandbox_error(err, "Sandbox.create") from err
        self._attach(Connection(api, view["id"], None, ready_timeout))
        try:
            self._conn.wait_ready()
            # The envs are set in the session, so a later connect sees them.
            if envs:
                self.set_envs(envs)
            for port in ports or []:
                self.expose_port(port)
        except BaseException:
            try:
                self.kill()
            except Exception:  # noqa: BLE001 - the first error is the one to raise
                pass
            raise

    def _attach(self, conn: Connection) -> None:
        self._conn = conn
        self.sandbox_id = conn.sandbox_id
        self.files = Filesystem(conn)
        self.commands = Commands(conn)
        self.pty = Pty(self.commands)
        self._code = CodeInterpreter(conn)

    @classmethod
    def create(cls, template: Optional[str] = None, **opts: Any) -> "Sandbox":
        """Creates a session and waits until it runs.

        template is a preset (codex, opencode, claude-code), "base" for the
        default preset, or the name of a template built with
        `gregale sandbox template publish`. timeout is the idle time in
        seconds before the session freezes. kind="persistent" keeps the
        session's memory and /workspace volume (volume_gb) across pauses:
        pause() stops it and connect() resumes it; it has no lifetime.
        """
        return cls(template, **opts)

    @classmethod
    def connect(
        cls,
        sandbox_id: str,
        api_key: Optional[str] = None,
        api_base: Optional[str] = None,
        request_timeout: Optional[float] = None,
        *,
        envs: Optional[Dict[str, str]] = None,
        port_visibility: str = "private",
        ready_timeout: float = 120.0,
        transport: Optional[Transport] = None,
    ) -> "Sandbox":
        """Connects to a live session; its token is re-minted with your API key.

        A paused persistent session is resumed first, as E2B's connect does.
        """
        _check_id(sandbox_id)
        conn = Connection(_ApiOpts(api_key, api_base, request_timeout, transport).api(), sandbox_id, envs, ready_timeout)
        _resume_if_paused(conn)
        conn.wait_ready()
        sandbox = cls(_conn=conn, port_visibility=port_visibility)
        for p in sandbox._list_ports():
            sandbox._ports[int(p["port"])] = p
        return sandbox

    @staticmethod
    def list(
        query: Optional[SandboxQuery] = None,
        limit: Optional[int] = None,
        api_key: Optional[str] = None,
        api_base: Optional[str] = None,
        request_timeout: Optional[float] = None,
        transport: Optional[Transport] = None,
    ) -> SandboxPaginator:
        return SandboxPaginator(_ApiOpts(api_key, api_base, request_timeout, transport), query, limit)

    def __enter__(self) -> "Sandbox":
        return self

    def __exit__(self, *exc: Any) -> None:
        self.kill()

    # kill, get_info and set_timeout work on the class with a sandbox ID, as
    # in Sandbox.kill(sandbox_id), and on an instance.

    def _kill(self) -> bool:
        """Closes the session; its VM and workspace go with it. False when it was gone."""
        try:
            self._conn.control("DELETE", "")
            return True
        except NotFoundException:
            return False

    @staticmethod
    def _kill_by_id(cls: Any, sandbox_id: str, **opts: Any) -> bool:
        _check_id(sandbox_id)
        return cls(_conn=Connection(_ApiOpts(**opts).api(), sandbox_id))._kill()

    kill = _ClassOrInstance(_kill, _kill_by_id)

    def _get_info(self) -> SandboxInfo:
        return _info(self._conn.read())

    @staticmethod
    def _get_info_by_id(cls: Any, sandbox_id: str, **opts: Any) -> SandboxInfo:
        _check_id(sandbox_id)
        return _info(Connection(_ApiOpts(**opts).api(), sandbox_id).read())

    get_info = _ClassOrInstance(_get_info, _get_info_by_id)

    def _set_timeout(self, timeout: float) -> None:
        """Changes the idle time, in seconds, before the session freezes."""
        self._conn.control("PATCH", "", json_body={"idle_timeout_s": math.ceil(timeout)})

    @staticmethod
    def _set_timeout_by_id(cls: Any, sandbox_id: str, timeout: float, **opts: Any) -> None:
        _check_id(sandbox_id)
        cls(_conn=Connection(_ApiOpts(**opts).api(), sandbox_id))._set_timeout(timeout)

    set_timeout = _ClassOrInstance(_set_timeout, _set_timeout_by_id)

    def run_code(self, code: str, **opts: Any) -> Execution:
        """Runs code in a code context's Jupyter kernel.

        The session must come from template "code-interpreter". Variables and
        imports persist between runs of one context. Takes E2B's language,
        context, on_stdout, on_stderr, on_result, on_error, timeout and
        request_timeout.
        """
        return self._code.run_code(code, **opts)

    def create_code_context(self, cwd: Optional[str] = None, language: Optional[str] = None, request_timeout: Optional[float] = None) -> Context:
        """Creates a code context: its own kernel, in cwd (default /workspace)."""
        return self._code.create_code_context(cwd, language, request_timeout)

    def list_code_contexts(self, request_timeout: Optional[float] = None) -> List[Context]:
        return self._code.list_code_contexts(request_timeout)

    def restart_code_context(self, context: Any, request_timeout: Optional[float] = None) -> None:
        self._code.restart_code_context(context, request_timeout)

    def remove_code_context(self, context: Any, request_timeout: Optional[float] = None) -> None:
        self._code.remove_code_context(context, request_timeout)

    def set_envs(self, envs: Dict[str, str], request_timeout: Optional[float] = None) -> List[str]:
        """Gregale only: replaces the session's environment.

        Every later command gets it under its own envs. It is kept in the
        session's memory, not stored; an empty dict clears it. Returns the
        names now set.
        """
        return list(self._conn.json("PUT", "/env", json_body={"env": envs}, timeout=request_timeout)["names"])

    def is_running(self) -> bool:
        try:
            return _is_live(self._conn.read())
        except NotFoundException:
            return False

    def _pause(self) -> bool:
        """Pauses a persistent session: its memory and /workspace are kept and
        Sandbox.connect resumes it (ADR-20261122132055). Returns once it is
        paused; False when it already was. An ephemeral session cannot pause:
        it freezes on its own when idle.
        """
        view = self._conn.read()
        if view.get("kind") != "persistent":
            raise UnsupportedException(
                "pause",
                'idle sessions freeze on their own and resume on the next call; create the session with kind="persistent" to pause it and keep its memory',
            )
        if view.get("state") == "paused":
            return False
        self._conn.control("POST", "/pause")
        deadline = time.monotonic() + self._conn.ready_timeout
        delay = 0.25
        while True:
            now = self._conn.read()
            state = now.get("state")
            if state == "paused":
                return True
            if state not in PAUSED and state not in ("ready", "frozen"):
                outcome = now.get("outcome") or {}
                code = f" ({outcome['code']})" if outcome.get("code") else ""
                raise SandboxNotFoundException(f"sandbox {self.sandbox_id} is {state}{code}")
            if time.monotonic() >= deadline:
                raise TimeoutException(f"sandbox {self.sandbox_id} did not pause within {self._conn.ready_timeout:g} s")
            time.sleep(delay)
            delay = min(delay * 2, 2.0)

    @staticmethod
    def _pause_by_id(cls: Any, sandbox_id: str, **opts: Any) -> bool:
        _check_id(sandbox_id)
        return cls(_conn=Connection(_ApiOpts(**opts).api(), sandbox_id))._pause()

    pause = _ClassOrInstance(_pause, _pause_by_id)
    beta_pause = _ClassOrInstance(_pause, _pause_by_id)

    def fork(
        self,
        count: int = 1,
        *,
        kind: Optional[str] = None,
        metadata: Optional[Dict[str, str]] = None,
        ready_timeout: Optional[float] = None,
    ) -> List["Sandbox"]:
        """Gregale only: starts count new sessions from this one's memory and
        /workspace as they are now, and waits until they run.

        A running session is captured without stopping; a paused one forks
        from its pause. Forks are ephemeral unless kind="persistent"; their
        labels are the source's unless metadata is given.
        """
        res = self._conn.control("POST", "/fork", json_body={"count": count, "kind": kind, "labels": metadata})
        forks = [
            type(self)(
                _conn=Connection(self._conn.api, v["id"], None, ready_timeout or self._conn.ready_timeout),
                port_visibility=self._port_visibility,
            )
            for v in res["forks"]
        ]
        for f in forks:
            f._conn.wait_ready()
        return forks

    def expose_port(self, port: int, visibility: Optional[str] = None) -> str:
        """Exposes a port at its preview URL and answers its host.

        A private port's preview token is what preview_token(port) returns.
        """
        view = self._conn.control("POST", "/ports", json_body={"port": port, "visibility": visibility or self._port_visibility})
        self._ports[port] = view
        return urlsplit(view["url"]).netloc

    def get_host(self, port: int) -> str:
        """The preview host of an exposed port.

        Gregale exposes ports with an API call, so a port must be exposed
        first: pass ports= at create, or call expose_port(port).
        """
        view = self._ports.get(port)
        if view is None:
            raise UnsupportedException(
                f"get_host({port}) before the port is exposed",
                f"call sandbox.expose_port({port}), or pass ports=[{port}] to Sandbox.create",
            )
        return urlsplit(view["url"]).netloc

    def preview_token(self, port: int) -> Optional[str]:
        """The preview token a private port's requests need (bearer or ?gregale_preview_token=)."""
        view = self._ports.get(port)
        return view.get("preview_token") if view else None

    def _list_ports(self) -> List[Dict[str, Any]]:
        try:
            return list(self._conn.control("GET", "/ports").get("items", []))
        except Exception:  # noqa: BLE001 - previews may be off on this installation
            return []

    def get_mcp_url(self) -> None:
        raise UnsupportedException("get_mcp_url", "sandbox MCP gateways are not available on Gregale")

    def create_snapshot(self) -> "SnapshotInfo":
        res = self._conn.json("POST", "/workspace:persist", json_body={})
        return SnapshotInfo(snapshot_id=res["snapshot_id"])


@dataclass
class SnapshotInfo:
    snapshot_id: str
    extra: Dict[str, Any] = field(default_factory=dict)
