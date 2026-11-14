"""Typed calls between functions of one project (ADR-20261111150000).

    from faas_sdk.functions_runtime import functions

    async def handler(event, ctx):
        user = await functions.users.call({"id": "42"})
        sent = await functions.mailer.send({"to": user["email"]})
        return {"body": {"user": user, "invocation": sent.invocation_id}}

A deploy binds each function to its siblings (and its ``calls:`` apps) and
sets ``GREGALE_FUNCTIONS``, a JSON map from each callable name to its app
slug. Calls go over the private binding (``http://<slug>.internal:10081``),
never the public internet; the platform authorizes them by the calling VM,
wakes a parked callee, and the request's deadline and release travel with
them. ``gregale function types`` writes ``gregale_functions.py`` with each
function's input and output ``TypedDict`` and a typed client.
"""

from __future__ import annotations

import json
import os
import re
import time
from collections.abc import Mapping
from typing import Any

import httpx

from .release_context import AsyncGregaleReleaseTransport, GregaleReleaseTransport

GREGALE_INVOKE_HEADER = "X-Gregale-Invoke"
# Marks a typed-client call; the callee checks only these against its input schema.
GREGALE_FUNCTION_CALL_HEADER = "X-Gregale-Function-Call"
GREGALE_FUNCTIONS_ENV = "GREGALE_FUNCTIONS"


class FunctionCallError(Exception):
    """The callee answered with a non-2xx status."""

    def __init__(self, function: str, status: int, problem: dict[str, Any] | None, body: str) -> None:
        self.function = function
        self.status = status
        self.problem = problem
        self.code = str((problem or {}).get("code") or "")
        detail = (problem or {}).get("detail") or body[:200]
        message = f"function {function} answered {status}"
        if self.code:
            message += f" ({self.code})"
        if detail:
            message += f": {detail}"
        super().__init__(message)


GREGALE_ON_COMPLETE_HEADER = "X-Gregale-On-Complete"
# Where a function reads the invocations it sent.
GREGALE_PLATFORM_API_URL = "http://api.svc.gregale:10081"
_MAX_WAIT_SECONDS = 30


class FunctionInvocation:
    """An accepted async invocation; only the function that sent it can read it."""

    def __init__(self, function: str, invocation_id: str, platform_url: str = GREGALE_PLATFORM_API_URL,
                 sync_client: httpx.Client | None = None, async_client: httpx.AsyncClient | None = None) -> None:
        self.function = function
        self.invocation_id = invocation_id
        self._url = platform_url.rstrip("/") + "/v1/invocations/" + invocation_id
        self._sync = sync_client
        self._async = async_client

    def __repr__(self) -> str:
        return f"FunctionInvocation({self.function!r}, {self.invocation_id!r})"

    def __eq__(self, other: object) -> bool:
        return isinstance(other, FunctionInvocation) and other.invocation_id == self.invocation_id

    def __hash__(self) -> int:
        return hash(self.invocation_id)

    def _status_from(self, response: httpx.Response) -> dict[str, Any]:
        _raise_for(self.function, response)
        body = response.json()
        if not isinstance(body, dict):
            raise FunctionCallError(self.function, response.status_code, None, response.text)
        return body

    def _settle(self, status: dict[str, Any], deadline: float) -> tuple[bool, Any]:
        if status.get("done"):
            if status.get("state") == "completed":
                return True, status.get("result")
            raise FunctionCallError(self.function, 500, {"code": "function_invocation_failed",
                                                         "detail": status.get("error") or status.get("state"), "invocation": status},
                                    str(status.get("error") or ""))
        if time.monotonic() >= deadline:
            raise FunctionCallError(self.function, 408, {"code": "function_invocation_pending",
                                                         "detail": f"invocation {self.invocation_id} is still {status.get('state')}",
                                                         "invocation": status}, "")
        return False, None

    @staticmethod
    def _wait_param(deadline: float) -> dict[str, str]:
        left = max(0, min(_MAX_WAIT_SECONDS, int(deadline - time.monotonic() + 0.999)))
        return {"wait": str(left)} if left > 0 else {}

    async def status(self) -> dict[str, Any]:
        """The invocation's current state."""
        if self._async is None:
            raise RuntimeError("this invocation has no client; use Functions.send")
        return self._status_from(await self._async.get(self._url))

    def status_sync(self) -> dict[str, Any]:
        """status for synchronous handlers."""
        if self._sync is None:
            raise RuntimeError("this invocation has no client; use Functions.send_sync")
        return self._status_from(self._sync.get(self._url))

    async def wait(self, timeout: float = 60.0) -> Any:
        """Wait until the invocation finishes and return its result.

        A failed invocation raises FunctionCallError; so does timeout (seconds)
        passing first, with status 408.
        """
        if self._async is None:
            raise RuntimeError("this invocation has no client; use Functions.send")
        deadline = time.monotonic() + timeout
        while True:
            response = await self._async.get(self._url, params=self._wait_param(deadline), timeout=_MAX_WAIT_SECONDS + 5)
            done, result = self._settle(self._status_from(response), deadline)
            if done:
                return result

    def wait_sync(self, timeout: float = 60.0) -> Any:
        """wait for synchronous handlers."""
        if self._sync is None:
            raise RuntimeError("this invocation has no client; use Functions.send_sync")
        deadline = time.monotonic() + timeout
        while True:
            response = self._sync.get(self._url, params=self._wait_param(deadline), timeout=_MAX_WAIT_SECONDS + 5)
            done, result = self._settle(self._status_from(response), deadline)
            if done:
                return result


def functions_from_env(env: Mapping[str, str] | None = None) -> dict[str, str]:
    """Parse GREGALE_FUNCTIONS; a missing or malformed value is an empty map."""
    raw = (env if env is not None else os.environ).get(GREGALE_FUNCTIONS_ENV, "")
    if not raw:
        return {}
    try:
        parsed = json.loads(raw)
    except ValueError:
        return {}
    if not isinstance(parsed, dict):
        return {}
    return {str(name): slug for name, slug in parsed.items() if isinstance(slug, str) and slug}


def function_base_url(slug: str, env: Mapping[str, str] | None = None) -> str:
    """The binding URL for slug: its injected GREGALE_SERVICE_<SLUG>_URL, else the alias."""
    key = "GREGALE_SERVICE_" + re.sub(r"[^A-Z0-9]", "_", slug.upper()) + "_URL"
    url = (env if env is not None else os.environ).get(key) or f"http://{slug}.internal:10081"
    return url.rstrip("/")


def _raise_for(function: str, response: httpx.Response) -> None:
    if response.is_success:
        return
    body = response.text
    problem: dict[str, Any] | None = None
    try:
        parsed = json.loads(body)
        if isinstance(parsed, dict):
            problem = parsed
    except ValueError:
        problem = None
    raise FunctionCallError(function, response.status_code, problem, body)


def _output(response: httpx.Response) -> Any:
    if not response.content:
        return None
    if "json" in response.headers.get("content-type", ""):
        return response.json()
    return response.text


def _invocation(function: str, response: httpx.Response, platform_url: str = GREGALE_PLATFORM_API_URL,
                sync_client: httpx.Client | None = None, async_client: httpx.AsyncClient | None = None) -> FunctionInvocation:
    try:
        body = response.json()
    except ValueError:
        body = {}
    invocation_id = body.get("invocation_id") or body.get("id") if isinstance(body, dict) else None
    if not isinstance(invocation_id, str) or not invocation_id:
        raise FunctionCallError(function, response.status_code, body if isinstance(body, dict) else None, "async invocation id missing")
    return FunctionInvocation(function, invocation_id, platform_url, sync_client, async_client)


class FunctionClient:
    """One callable function."""

    def __init__(self, name: str, url: str, sync_client: httpx.Client, async_client: httpx.AsyncClient,
                 platform_url: str = GREGALE_PLATFORM_API_URL) -> None:
        self._name = name
        self._url = url
        self._sync = sync_client
        self._async = async_client
        self._platform_url = platform_url

    def _request(self, input: Any, path: str, headers: Mapping[str, str] | None, send: bool,
                 on_complete: str | None = None) -> dict[str, Any]:
        all_headers = {"content-type": "application/json", "accept": "application/json", **(headers or {}),
                       GREGALE_FUNCTION_CALL_HEADER: "1"}
        if send:
            all_headers[GREGALE_INVOKE_HEADER] = "async"
            if on_complete:
                all_headers[GREGALE_ON_COMPLETE_HEADER] = on_complete
        return {
            "url": self._url + (path if path.startswith("/") else "/" + path),
            "content": json.dumps(input),
            "headers": all_headers,
        }

    async def call(self, input: Any = None, *, path: str = "/", headers: Mapping[str, str] | None = None) -> Any:
        """POST the input and return the parsed output."""
        response = await self._async.post(**self._request(input, path, headers, False))
        _raise_for(self._name, response)
        return _output(response)

    def call_sync(self, input: Any = None, *, path: str = "/", headers: Mapping[str, str] | None = None) -> Any:
        """call for synchronous handlers."""
        response = self._sync.post(**self._request(input, path, headers, False))
        _raise_for(self._name, response)
        return _output(response)

    async def send(self, input: Any = None, *, path: str = "/", headers: Mapping[str, str] | None = None,
                   on_complete: str | None = None) -> FunctionInvocation:
        """Enqueue a durable async invocation and return at once.

        on_complete names a function that receives the completion when it
        finishes: a name this function may call, or "self".
        """
        response = await self._async.post(**self._request(input, path, headers, True, on_complete))
        _raise_for(self._name, response)
        return _invocation(self._name, response, self._platform_url, self._sync, self._async)

    def send_sync(self, input: Any = None, *, path: str = "/", headers: Mapping[str, str] | None = None,
                  on_complete: str | None = None) -> FunctionInvocation:
        """send for synchronous handlers."""
        response = self._sync.post(**self._request(input, path, headers, True, on_complete))
        _raise_for(self._name, response)
        return _invocation(self._name, response, self._platform_url, self._sync, self._async)


class Functions:
    """The functions this function may call, by attribute or item name."""

    def __init__(
        self,
        functions: Mapping[str, str] | None = None,
        *,
        env: Mapping[str, str] | None = None,
        sync_transport: httpx.BaseTransport | None = None,
        async_transport: httpx.AsyncBaseTransport | None = None,
        timeout: float | None = 30.0,
    ) -> None:
        self._env = env
        self._names = dict(functions) if functions is not None else functions_from_env(env)
        self._sync = httpx.Client(transport=GregaleReleaseTransport(sync_transport), timeout=timeout)
        self._async = httpx.AsyncClient(transport=AsyncGregaleReleaseTransport(async_transport), timeout=timeout)
        self._clients: dict[str, FunctionClient] = {}

    def __getitem__(self, name: str) -> FunctionClient:
        client = self._clients.get(name)
        if client is not None:
            return client
        slug = self._names.get(name)
        if not slug:
            known = ", ".join(sorted(self._names))
            hint = f"callable: {known}" if known else "GREGALE_FUNCTIONS is not set"
            raise KeyError(f"function {name} is not callable from here; {hint} (deploy the functions project, or add the app to calls:)")
        platform_url = (self._env if self._env is not None else os.environ).get("GREGALE_PLATFORM_API_URL") or GREGALE_PLATFORM_API_URL
        client = FunctionClient(name, function_base_url(slug, self._env), self._sync, self._async, platform_url)
        self._clients[name] = client
        return client

    def __getattr__(self, name: str) -> FunctionClient:
        if name.startswith("_"):
            raise AttributeError(name)
        try:
            return self[name]
        except KeyError as err:
            raise AttributeError(str(err)) from None


functions = Functions()

__all__ = [
    "GREGALE_FUNCTIONS_ENV",
    "GREGALE_INVOKE_HEADER",
    "GREGALE_ON_COMPLETE_HEADER",
    "GREGALE_PLATFORM_API_URL",
    "FunctionCallError",
    "FunctionClient",
    "FunctionInvocation",
    "Functions",
    "function_base_url",
    "functions",
    "functions_from_env",
]
