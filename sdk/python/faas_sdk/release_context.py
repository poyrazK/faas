"""Request-scoped Gregale release and deadline propagation for managed calls."""

from __future__ import annotations

import math
import re
import time
from collections.abc import Iterable
from contextlib import contextmanager
from contextvars import ContextVar
from typing import Any

import httpx

GREGALE_REVISION_HEADER = "X-Gregale-Revision"
GREGALE_RELEASE_HEADER = "X-Gregale-Release"
GREGALE_REQUEST_DEADLINE_HEADER = "X-Gregale-Request-Deadline"
# Time the request may still run, in milliseconds. Gregale sets it on every
# request it forwards to an app; on a call to another app's service it tells
# the service proxy when the caller's own request runs out, so the call ends
# then (ADR-20261102082606).
GREGALE_DEADLINE_MS_HEADER = "X-Faas-Deadline-Ms"
_DEADLINE_MS_MAX = 24 * 60 * 60 * 1000

_release_context: ContextVar[str | None] = ContextVar("faas_sdk_gregale_release", default=None)
_deadline_context: ContextVar[str | None] = ContextVar("faas_sdk_gregale_deadline", default=None)
# time.monotonic() at which the current request runs out.
_deadline_at_context: ContextVar[float | None] = ContextVar("faas_sdk_gregale_deadline_at", default=None)


def _parse_deadline_ms(value: str | None) -> int | None:
    if value is None or not re.fullmatch(r"\s*\d{1,9}\s*", value):
        return None
    ms = int(value)
    return ms if 0 < ms <= _DEADLINE_MS_MAX else None


def _deadline_at_from(values: list[str]) -> float | None:
    ms = _parse_deadline_ms(values[0]) if len(values) == 1 else None
    return None if ms is None else time.monotonic() + ms / 1000


def gregale_request_time_remaining_ms() -> int | None:
    """Milliseconds the current request may still run; 0 once it has run out.

    ``None`` unless the handler runs in :func:`with_gregale_request_context` or
    :class:`GregaleReleaseMiddleware` and Gregale sent ``X-Faas-Deadline-Ms``.
    """
    deadline_at = _deadline_at_context.get()
    if deadline_at is None:
        return None
    return max(math.floor((deadline_at - time.monotonic()) * 1000), 0)


def _clean_deadline(value: str | None) -> str | None:
    return value if value and len(value) <= 2048 and re.fullmatch(r"[A-Za-z0-9._-]+", value) else None


def current_gregale_request_deadline() -> str | None:
    """Return this request's opaque platform deadline; never edit its bytes."""
    return _deadline_context.get()


@contextmanager
def with_gregale_request_context(headers):
    """Capture Gregale release and deadline headers for a synchronous handler."""
    inbound = httpx.Headers(headers)
    values = inbound.get_list(GREGALE_REQUEST_DEADLINE_HEADER)
    token = _deadline_context.set(_clean_deadline(values[0]) if len(values) == 1 else None)
    at_token = _deadline_at_context.set(_deadline_at_from(inbound.get_list(GREGALE_DEADLINE_MS_HEADER)))
    try:
        with with_gregale_release(inbound.get(GREGALE_RELEASE_HEADER)):
            yield
    finally:
        _deadline_at_context.reset(at_token)
        _deadline_context.reset(token)


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


def _values_from_asgi_headers(headers: Iterable[tuple[bytes, bytes]], header: str) -> list[str]:
    target = header.lower().encode()
    return [value.decode("latin-1") for name, value in headers if name.lower() == target]


def _value_from_asgi_headers(headers: Iterable[tuple[bytes, bytes]], header: str) -> str | None:
    values = _values_from_asgi_headers(headers, header)
    if len(values) != 1:
        return None
    return values[0]


class GregaleReleaseMiddleware:
    """Capture Gregale release context and ordinary HTTP deadline context."""

    def __init__(self, app: Any) -> None:
        self.app = app

    async def __call__(self, scope: dict[str, Any], receive: Any, send: Any) -> None:
        if scope.get("type") not in {"http", "websocket"}:
            await self.app(scope, receive, send)
            return

        headers = scope.get("headers", ())
        token = _release_context.set(_clean_release(_value_from_asgi_headers(headers, GREGALE_RELEASE_HEADER)))
        deadline_token = _deadline_context.set(
            _clean_deadline(_value_from_asgi_headers(headers, GREGALE_REQUEST_DEADLINE_HEADER))
            if scope.get("type") == "http"
            else None
        )
        at_token = _deadline_at_context.set(
            _deadline_at_from(_values_from_asgi_headers(headers, GREGALE_DEADLINE_MS_HEADER))
            if scope.get("type") == "http"
            else None
        )
        try:
            await self.app(scope, receive, send)
        finally:
            _release_context.reset(token)
            _deadline_context.reset(deadline_token)
            _deadline_at_context.reset(at_token)


def _is_managed_service(request: httpx.Request) -> bool:
    return request.url.host.lower().rstrip(".").endswith((".svc.gregale", ".internal"))


def _apply_release_context(request: httpx.Request) -> None:
    if not _is_managed_service(request):
        request.headers.pop(GREGALE_REQUEST_DEADLINE_HEADER, None)
        return

    # A revision ID belongs to the caller app; only project graph context is
    # meaningful on an app-to-app hop.
    request.headers.pop(GREGALE_REVISION_HEADER, None)
    release = _clean_release(request.headers.get(GREGALE_RELEASE_HEADER)) or current_gregale_release()
    if release is None:
        request.headers.pop(GREGALE_RELEASE_HEADER, None)
    else:
        request.headers[GREGALE_RELEASE_HEADER] = release

    deadline = current_gregale_request_deadline() or _clean_deadline(
        request.headers.get(GREGALE_REQUEST_DEADLINE_HEADER)
    )
    if deadline is None:
        request.headers.pop(GREGALE_REQUEST_DEADLINE_HEADER, None)
    else:
        request.headers[GREGALE_REQUEST_DEADLINE_HEADER] = deadline

    # The time left bounds the call at the service proxy and only shortens it.
    # A request with no time left still sends 1 ms, so the proxy refuses the
    # call instead of waking the target for an answer nobody will read.
    deadline_at = _deadline_at_context.get()
    if deadline_at is not None:
        remaining = max(math.floor((deadline_at - time.monotonic()) * 1000), 1)
        own = request.headers.get_list(GREGALE_DEADLINE_MS_HEADER)
        own_ms = _parse_deadline_ms(own[0]) if len(own) == 1 else None
        request.headers[GREGALE_DEADLINE_MS_HEADER] = str(
            own_ms if own_ms is not None and own_ms < remaining else remaining
        )


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
    "GREGALE_DEADLINE_MS_HEADER",
    "GREGALE_RELEASE_HEADER",
    "GREGALE_REVISION_HEADER",
    "GREGALE_REQUEST_DEADLINE_HEADER",
    "AsyncGregaleReleaseTransport",
    "GregaleReleaseMiddleware",
    "GregaleReleaseTransport",
    "current_gregale_release",
    "current_gregale_request_deadline",
    "gregale_request_time_remaining_ms",
    "with_gregale_request_context",
    "with_gregale_release",
]
