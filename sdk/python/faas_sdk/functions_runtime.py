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
from collections.abc import Mapping
from dataclasses import dataclass
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


@dataclass(frozen=True)
class FunctionInvocation:
    """An accepted async invocation."""

    invocation_id: str


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


def _invocation(function: str, response: httpx.Response) -> FunctionInvocation:
    try:
        body = response.json()
    except ValueError:
        body = {}
    invocation_id = body.get("invocation_id") or body.get("id") if isinstance(body, dict) else None
    if not isinstance(invocation_id, str) or not invocation_id:
        raise FunctionCallError(function, response.status_code, body if isinstance(body, dict) else None, "async invocation id missing")
    return FunctionInvocation(invocation_id)


class FunctionClient:
    """One callable function."""

    def __init__(self, name: str, url: str, sync_client: httpx.Client, async_client: httpx.AsyncClient) -> None:
        self._name = name
        self._url = url
        self._sync = sync_client
        self._async = async_client

    def _request(self, input: Any, path: str, headers: Mapping[str, str] | None, send: bool) -> dict[str, Any]:
        all_headers = {"content-type": "application/json", "accept": "application/json", **(headers or {}),
                       GREGALE_FUNCTION_CALL_HEADER: "1"}
        if send:
            all_headers[GREGALE_INVOKE_HEADER] = "async"
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

    async def send(self, input: Any = None, *, path: str = "/", headers: Mapping[str, str] | None = None) -> FunctionInvocation:
        """Enqueue a durable async invocation and return at once."""
        response = await self._async.post(**self._request(input, path, headers, True))
        _raise_for(self._name, response)
        return _invocation(self._name, response)

    def send_sync(self, input: Any = None, *, path: str = "/", headers: Mapping[str, str] | None = None) -> FunctionInvocation:
        """send for synchronous handlers."""
        response = self._sync.post(**self._request(input, path, headers, True))
        _raise_for(self._name, response)
        return _invocation(self._name, response)


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
        client = FunctionClient(name, function_base_url(slug, self._env), self._sync, self._async)
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
    "FunctionCallError",
    "FunctionClient",
    "FunctionInvocation",
    "Functions",
    "function_base_url",
    "functions",
    "functions_from_env",
]
