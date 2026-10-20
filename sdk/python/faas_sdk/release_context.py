"""Request-scoped Gregale release propagation for managed service calls."""

from __future__ import annotations

from collections.abc import Iterable
from contextlib import contextmanager
from contextvars import ContextVar
from typing import Any

import httpx

GREGALE_REVISION_HEADER = "X-Gregale-Revision"
GREGALE_RELEASE_HEADER = "X-Gregale-Release"

_release_context: ContextVar[str | None] = ContextVar("faas_sdk_gregale_release", default=None)


def _clean_release(release: str | None) -> str | None:
    if release is None:
        return None
    release = release.strip()
    if not release or any(char.isspace() or char == "," for char in release):
        return None
    return release


def current_gregale_release() -> str | None:
    """Return the release selected for the current request, if any."""
    return _release_context.get()


@contextmanager
def with_gregale_release(release: str | None):
    """Scope a release ID to the current synchronous or async task.

    In an ASGI app, prefer :class:`GregaleReleaseMiddleware`, which captures
    the header from each HTTP/WebSocket request automatically.
    """
    token = _release_context.set(_clean_release(release))
    try:
        yield current_gregale_release()
    finally:
        _release_context.reset(token)


def _release_from_asgi_headers(headers: Iterable[tuple[bytes, bytes]]) -> str | None:
    target = GREGALE_RELEASE_HEADER.lower().encode()
    values = [value.decode("latin-1") for name, value in headers if name.lower() == target]
    if len(values) != 1:
        return None
    return _clean_release(values[0])


class GregaleReleaseMiddleware:
    """ASGI middleware that captures Gregale's selected release per request."""

    def __init__(self, app: Any) -> None:
        self.app = app

    async def __call__(self, scope: dict[str, Any], receive: Any, send: Any) -> None:
        if scope.get("type") not in {"http", "websocket"}:
            await self.app(scope, receive, send)
            return

        token = _release_context.set(_release_from_asgi_headers(scope.get("headers", ())))
        try:
            await self.app(scope, receive, send)
        finally:
            _release_context.reset(token)


def _is_managed_service(request: httpx.Request) -> bool:
    return request.url.host.lower().rstrip(".").endswith(".svc.gregale")


def _apply_release_context(request: httpx.Request) -> None:
    if not _is_managed_service(request):
        return

    # A revision ID belongs to the caller app; only project graph context is
    # meaningful on an app-to-app hop.
    request.headers.pop(GREGALE_REVISION_HEADER, None)
    release = _clean_release(request.headers.get(GREGALE_RELEASE_HEADER)) or current_gregale_release()
    if release is None:
        request.headers.pop(GREGALE_RELEASE_HEADER, None)
    else:
        request.headers[GREGALE_RELEASE_HEADER] = release


class GregaleReleaseTransport(httpx.BaseTransport):
    """Sync HTTPX transport that forwards release context to managed services."""

    def __init__(self, transport: httpx.BaseTransport | None = None) -> None:
        self._transport = transport or httpx.HTTPTransport()

    def handle_request(self, request: httpx.Request) -> httpx.Response:
        _apply_release_context(request)
        return self._transport.handle_request(request)

    def close(self) -> None:
        self._transport.close()


class AsyncGregaleReleaseTransport(httpx.AsyncBaseTransport):
    """Async HTTPX transport with sync transport parity."""

    def __init__(self, transport: httpx.AsyncBaseTransport | None = None) -> None:
        self._transport = transport or httpx.AsyncHTTPTransport()

    async def handle_async_request(self, request: httpx.Request) -> httpx.Response:
        _apply_release_context(request)
        return await self._transport.handle_async_request(request)

    async def aclose(self) -> None:
        await self._transport.aclose()


__all__ = [
    "GREGALE_RELEASE_HEADER",
    "GREGALE_REVISION_HEADER",
    "AsyncGregaleReleaseTransport",
    "GregaleReleaseMiddleware",
    "GregaleReleaseTransport",
    "current_gregale_release",
    "with_gregale_release",
]
