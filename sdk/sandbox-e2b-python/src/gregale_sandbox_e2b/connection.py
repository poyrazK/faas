"""One session's connection: its token, re-minted from the API key as it
nears expiry, and its data-plane calls."""

from __future__ import annotations

import json
import re
import time
from datetime import datetime
from typing import Any, Dict, Optional, Tuple

from .api import Api, ApiError, Response, to_sandbox_error
from .errors import SandboxNotFoundException, TimeoutException

LIVE = {"queued", "starting", "ready", "frozen"}
PLACED = {"ready", "frozen"}
MARGIN_S = 60.0

_FRACTION = re.compile(r"(\.\d{6})\d+")


def parse_time(value: str) -> datetime:
    """An RFC 3339 time as the API writes it (nanoseconds and a Z suffix)."""
    return datetime.fromisoformat(_FRACTION.sub(r"\1", value).replace("Z", "+00:00"))


class Connection:
    def __init__(self, api: Api, sandbox_id: str, envs: Optional[Dict[str, str]] = None, ready_timeout: float = 120.0) -> None:
        self.api = api
        self.sandbox_id = sandbox_id
        self.envs = dict(envs or {})
        self.ready_timeout = ready_timeout
        self._token: Optional[Tuple[str, float]] = None

    @property
    def path(self) -> str:
        return f"/v1/sandboxes/{self.sandbox_id}"

    def read(self, **args: Any) -> Dict[str, Any]:
        """Reads the session, keeping the token the read minted."""
        try:
            view = self.api.control("GET", self.path, **args)
        except ApiError as err:
            raise to_sandbox_error(err, f"sandbox {self.sandbox_id}") from err
        token, expires = view.get("token"), view.get("token_expires_at")
        self._token = (token, parse_time(expires).timestamp()) if token and expires else None
        return view

    def wait_ready(self, **args: Any) -> Dict[str, Any]:
        """Waits until the session runs on a node; fails if it has ended."""
        deadline = time.monotonic() + self.ready_timeout
        delay = 0.25
        while True:
            view = self.read(**args)
            closing = view.get("close_requested_at")
            if view.get("state") not in LIVE or closing:
                outcome = view.get("outcome") or {}
                code = f" ({outcome['code']})" if outcome.get("code") else ""
                raise SandboxNotFoundException(f"sandbox {self.sandbox_id} is {'closing' if closing else view.get('state')}{code}")
            if view.get("state") in PLACED and self._token:
                return view
            if time.monotonic() >= deadline:
                raise TimeoutException(f"sandbox {self.sandbox_id} did not start within {self.ready_timeout:g} s")
            time.sleep(delay)
            delay = min(delay * 2, 2.0)

    def token(self) -> str:
        if not self._token or self._token[1] - MARGIN_S <= time.time():
            self.wait_ready()
        assert self._token is not None
        return self._token[0]

    def call(self, method: str, sub: str, **args: Any) -> Response:
        """A data-plane call; a token the data plane no longer takes is re-minted once."""
        path = self.path + sub
        try:
            try:
                return self.api.data(method, path, self.token(), **args)
            except ApiError as err:
                stale = (err.status == 404 and err.code == "") or err.code == "sandbox_not_ready"
                if not stale:
                    raise
                self._token = None
                return self.api.data(method, path, self.token(), **args)
        except ApiError as err:
            raise to_sandbox_error(err, f"{method} {sub}") from err

    def json(self, method: str, sub: str, **args: Any) -> Any:
        res = self.call(method, sub, **args)
        try:
            return json.loads(res.read() or b"null")
        finally:
            res.close()

    def control(self, method: str, sub: str, **args: Any) -> Any:
        try:
            return self.api.control(method, self.path + sub, **args)
        except ApiError as err:
            raise to_sandbox_error(err, f"{method} {self.path + sub}") from err
