"""Request-scoped Dev Bridge propagation for ASGI and HTTPX applications."""

from __future__ import annotations

import base64
import re
from contextlib import contextmanager
from contextvars import ContextVar
from typing import Any

import httpx

DEV_BRIDGE_CONTEXT_HEADER = "X-Gregale-Dev-Session-Context"
_context: ContextVar[str | None] = ContextVar("gregale_dev_bridge", default=None)


def _parse(value: str | None) -> str | None:
    if not value:
        return None
    parts = value.split(".")
    if len(parts) != 3 or not parts[0] or len(parts[0]) > 64:
        return None
    for part in parts[1:]:
        if not re.fullmatch(r"[A-Za-z0-9_-]{43}", part):
            return None
        if len(base64.urlsafe_b64decode(part + "=")) != 32:
            return None
    return value


def current_dev_bridge_context() -> str | None:
    return _context.get()


@contextmanager
def with_dev_bridge_context(value: str | None):
    """Capture request authority, with isolation across concurrent async tasks."""
    token = _context.set(_parse(value))
    try:
        yield current_dev_bridge_context()
    finally:
        _context.reset(token)


class DevBridgeMiddleware:
    """ASGI middleware for FastAPI/Starlette; no framework dependency required."""

    def __init__(self, app: Any) -> None:
        self.app = app

    async def __call__(self, scope: dict[str, Any], receive: Any, send: Any) -> None:
        if scope.get("type") != "http":
            await self.app(scope, receive, send)
            return
        values = [
            value.decode("latin-1")
            for name, value in scope.get("headers", ())
            if name.lower() == DEV_BRIDGE_CONTEXT_HEADER.lower().encode()
        ]
        with with_dev_bridge_context(values[0] if len(values) == 1 else None):
            await self.app(scope, receive, send)


def _apply(request: httpx.Request) -> None:
    for name in list(request.headers):
        if name.lower().startswith("x-gregale-dev-bridge-") or name.lower() == DEV_BRIDGE_CONTEXT_HEADER.lower():
            del request.headers[name]
    host = request.url.host.lower().rstrip(".")
    value = current_dev_bridge_context()
    if value and not request.url.userinfo and re.fullmatch(r"[^.]+\.(?:svc\.gregale|internal)", host):
        request.headers[DEV_BRIDGE_CONTEXT_HEADER] = value


class DevBridgeTransport(httpx.BaseTransport):
    """Remove credentials and re-evaluate scope on every request/redirect hop."""

    def __init__(self, transport: httpx.BaseTransport | None = None) -> None:
        self._transport = transport or httpx.HTTPTransport()

    def handle_request(self, request: httpx.Request) -> httpx.Response:
        _apply(request)
        return self._transport.handle_request(request)

    def close(self) -> None:
        self._transport.close()


class AsyncDevBridgeTransport(httpx.AsyncBaseTransport):
    def __init__(self, transport: httpx.AsyncBaseTransport | None = None) -> None:
        self._transport = transport or httpx.AsyncHTTPTransport()

    async def handle_async_request(self, request: httpx.Request) -> httpx.Response:
        _apply(request)
        return await self._transport.handle_async_request(request)

    async def aclose(self) -> None:
        await self._transport.aclose()
