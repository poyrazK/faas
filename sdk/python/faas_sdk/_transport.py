"""faas_sdk._transport - BaseTransport chain (retry -> logging -> rfc7807 -> idempotency).

The chain is composed outermost -> innermost, mirroring the Go
SDK's `RoundTripper` stack in `sdk/go/transport.go` and the Node
SDK's `makeWrappedFetch` in `src/client.ts`:

    1. RetryTransport      (5xx + 429 only, exponential backoff)
    2. LoggingTransport    (one log line per attempt, optional logger)
    3. Rfc7807Layer        (decode Problem bodies, raise sentinels)
    4. IdempotencyTransport (mint Idempotency-Key on mutating calls)
    5. user_transport      (httpx default or caller's transport)

The retry layer is outermost because it owns the "do we try
again" decision (needs to see every response). The idempotency
layer is innermost so the key is attached before the wire - and
so retries replay the same key.

Sync + async parity: every sync transport has an async sibling
(`Async*Transport`) that subclasses `httpx.AsyncBaseTransport`.
The async chain is composed independently via `build_async_chain`
and installed by `install_chain` alongside the sync chain so that
both `*.sync(...)` AND `*.asyncio(...)` generated service calls
flow through RFC 7807 + idempotency. Cross-SDK parity is the
contract; missing parity here would let async callers bypass
RFC 7807 unwrap and Idempotency-Key minting.

`install_chain(client, options)` is the public seam: it swaps
the generator's `Client._client` (an `httpx.Client`) AND
`Client._async_client` (`httpx.AsyncClient`) for new ones whose
transports are the chains. The generated service functions call
`client.get_httpx_client().send(request)` for sync and
`client.get_async_httpx_client().send(request)` for async, so
both paths are covered.
"""

from __future__ import annotations

import asyncio
import logging
import time
from dataclasses import dataclass, field
from typing import Any

import httpx

from ._rfc7807 import parse_problem, raise_for_problem
from .idempotency import HEADER_NAME as IDEMPOTENCY_HEADER
from .idempotency import current_idempotency_key, mint_idempotency_key

MUTATING_METHODS = frozenset({"POST", "PUT", "PATCH", "DELETE"})


# ---------------------------------------------------------------------------
# Retry layer (outermost)
# ---------------------------------------------------------------------------


class _BodyCapturing:
    """Snapshot `Request.content` and re-apply on retries. httpx's
    `Request` does not rewind the body across calls; we capture it
    once and replay on retry.
    """

    __slots__ = ("_bytes",)

    def __init__(self, request: httpx.Request) -> None:
        self._bytes: bytes = request.content

    def apply(self, request: httpx.Request) -> None:
        if request.content != self._bytes:
            # Internal but stable across httpx 0.27/0.28.
            request._content = self._bytes  # noqa: SLF001


def _is_retryable(response: httpx.Response) -> bool:
    return response.status_code == 429 or response.status_code >= 500


def _retry_after_seconds(response: httpx.Response) -> float | None:
    """Read `Retry-After` (seconds form). Returns None when absent
    or invalid."""
    raw = response.headers.get("Retry-After")
    if raw is None:
        return None
    try:
        return float(raw)
    except ValueError:
        return None


class RetryTransport(httpx.BaseTransport):
    """Retries on 5xx + 429 only. Bounded exponential backoff.

    `max_attempts=0` is a true identity (the wrapped transport runs
    once). `backoff=0` retries with no inter-attempt sleep (useful
    in tests).
    """

    def __init__(
        self,
        transport: httpx.BaseTransport,
        max_attempts: int = 0,
        backoff: float = 0.5,
    ) -> None:
        self._transport = transport
        self._max_attempts = max_attempts
        self._backoff = backoff

    def handle_request(self, request: httpx.Request) -> httpx.Response:
        if self._max_attempts <= 0:
            return self._transport.handle_request(request)

        capture = _BodyCapturing(request)
        attempts = 0
        delay = self._backoff
        while True:
            capture.apply(request)
            response = self._transport.handle_request(request)
            attempts += 1
            if not _is_retryable(response) or attempts > self._max_attempts:
                return response
            response.close()
            sleep_for = _retry_after_seconds(response) or delay
            time.sleep(sleep_for)
            delay = min(delay * 2, 8.0)


# ---------------------------------------------------------------------------
# Logging layer
# ---------------------------------------------------------------------------


class LoggingTransport(httpx.BaseTransport):
    """Emits one debug log line per request + per response.
    `logger=None` makes the wrapper a no-op identity pass-through.
    """

    def __init__(
        self,
        transport: httpx.BaseTransport,
        logger: logging.Logger | None = None,
    ) -> None:
        self._transport = transport
        self._logger = logger

    def handle_request(self, request: httpx.Request) -> httpx.Response:
        if self._logger is None:
            return self._transport.handle_request(request)
        self._logger.debug(
            "request",
            extra={"method": request.method, "url": str(request.url)},
        )
        response = self._transport.handle_request(request)
        self._logger.debug(
            "response",
            extra={
                "method": request.method,
                "url": str(request.url),
                "status": response.status_code,
            },
        )
        return response


# ---------------------------------------------------------------------------
# RFC 7807 layer
# ---------------------------------------------------------------------------


class Rfc7807Layer(httpx.BaseTransport):
    """Decodes `application/problem+json` and raises a typed
    `FaasProblemError`. Conservative: only statuses >= 400 AND a
    parseable Problem body raise. Other failures (network errors,
    5xx with non-Problem body) propagate normally so the retry
    layer can act on them.
    """

    def __init__(self, transport: httpx.BaseTransport) -> None:
        self._transport = transport

    def handle_request(self, request: httpx.Request) -> httpx.Response:
        response = self._transport.handle_request(request)
        # Only consume the body when the response is in the error range.
        # Reading the body on 2xx would drain a streaming response that
        # the caller (e.g. SSE) intends to iterate; reading on 4xx is
        # required so `parse_problem` can decode the Problem.
        if response.status_code < 400:
            return response
        body = response.read()
        content_type = response.headers.get("Content-Type", "")
        problem = parse_problem(body, content_type, response.status_code)
        if problem is not None:
            response.close()
            raise_for_problem(problem)
        return response


# ---------------------------------------------------------------------------
# Idempotency layer (innermost)
# ---------------------------------------------------------------------------


class IdempotencyTransport(httpx.BaseTransport):
    """Attaches `Idempotency-Key` on mutating calls.

    Order of precedence:
      1. Caller-supplied via `with_idempotency_key(...)` (ContextVar).
      2. Caller-supplied via a per-request `Idempotency-Key` header.
      3. Auto-mint a fresh UUIDv4.
    GET/HEAD/OPTIONS skip the header (mirrors
    `pkg/api/client.go::do`'s `method != GET && method != HEAD`
    guard).
    """

    def __init__(self, transport: httpx.BaseTransport) -> None:
        self._transport = transport

    def handle_request(self, request: httpx.Request) -> httpx.Response:
        if request.method.upper() in MUTATING_METHODS:
            explicit = request.headers.get(IDEMPOTENCY_HEADER)
            if not explicit:
                ctx = current_idempotency_key()
                if ctx is not None:
                    request.headers[IDEMPOTENCY_HEADER] = ctx
                else:
                    request.headers[IDEMPOTENCY_HEADER] = mint_idempotency_key()
        return self._transport.handle_request(request)


# ---------------------------------------------------------------------------
# Async siblings — every sync transport above has an async equivalent
# below. httpx dispatches `*.asyncio(...)` to `AsyncClient`, which
# uses `AsyncBaseTransport` (a separate interface from `BaseTransport`).
# Cross-language parity requires the chain on BOTH paths.
# ---------------------------------------------------------------------------


class AsyncRetryTransport(httpx.AsyncBaseTransport):
    """Async sibling of `RetryTransport`. Uses `asyncio.sleep` so the
    event loop stays responsive during the backoff window."""

    def __init__(
        self,
        transport: httpx.AsyncBaseTransport,
        max_attempts: int = 0,
        backoff: float = 0.5,
    ) -> None:
        self._transport = transport
        self._max_attempts = max_attempts
        self._backoff = backoff

    async def handle_async_request(self, request: httpx.Request) -> httpx.Response:
        if self._max_attempts <= 0:
            return await self._transport.handle_async_request(request)

        capture = _BodyCapturing(request)
        attempts = 0
        delay = self._backoff
        while True:
            capture.apply(request)
            response = await self._transport.handle_async_request(request)
            attempts += 1
            if not _is_retryable(response) or attempts > self._max_attempts:
                return response
            response.close()
            sleep_for = _retry_after_seconds(response) or delay
            await asyncio.sleep(sleep_for)
            delay = min(delay * 2, 8.0)

    async def aclose(self) -> None:
        await self._transport.aclose()


class AsyncLoggingTransport(httpx.AsyncBaseTransport):
    """Async sibling of `LoggingTransport`."""

    def __init__(
        self,
        transport: httpx.AsyncBaseTransport,
        logger: logging.Logger | None = None,
    ) -> None:
        self._transport = transport
        self._logger = logger

    async def handle_async_request(self, request: httpx.Request) -> httpx.Response:
        if self._logger is None:
            return await self._transport.handle_async_request(request)
        self._logger.debug(
            "request",
            extra={"method": request.method, "url": str(request.url)},
        )
        response = await self._transport.handle_async_request(request)
        self._logger.debug(
            "response",
            extra={
                "method": request.method,
                "url": str(request.url),
                "status": response.status_code,
            },
        )
        return response

    async def aclose(self) -> None:
        await self._transport.aclose()


class AsyncRfc7807Layer(httpx.AsyncBaseTransport):
    """Async sibling of `Rfc7807Layer`. Body read still requires
    `await response.aread()` (httpx async stream drain)."""

    def __init__(self, transport: httpx.AsyncBaseTransport) -> None:
        self._transport = transport

    async def handle_async_request(self, request: httpx.Request) -> httpx.Response:
        response = await self._transport.handle_async_request(request)
        if response.status_code < 400:
            return response
        body = await response.aread()
        content_type = response.headers.get("Content-Type", "")
        problem = parse_problem(body, content_type, response.status_code)
        if problem is not None:
            response.close()
            raise_for_problem(problem)
        return response

    async def aclose(self) -> None:
        await self._transport.aclose()


class AsyncIdempotencyTransport(httpx.AsyncBaseTransport):
    """Async sibling of `IdempotencyTransport`. The ContextVar
    hooks are async-safe (`asyncio.Task` inheritance is implicit
    through `ContextVar`)."""

    def __init__(self, transport: httpx.AsyncBaseTransport) -> None:
        self._transport = transport

    async def handle_async_request(self, request: httpx.Request) -> httpx.Response:
        if request.method.upper() in MUTATING_METHODS:
            explicit = request.headers.get(IDEMPOTENCY_HEADER)
            if not explicit:
                ctx = current_idempotency_key()
                if ctx is not None:
                    request.headers[IDEMPOTENCY_HEADER] = ctx
                else:
                    request.headers[IDEMPOTENCY_HEADER] = mint_idempotency_key()
        return await self._transport.handle_async_request(request)

    async def aclose(self) -> None:
        await self._transport.aclose()


# ---------------------------------------------------------------------------
# Public chain + install seam
# ---------------------------------------------------------------------------


@dataclass
class RetryOptions:
    """Bounded exponential-backoff retry policy.

    `max_attempts=0` disables retries (the default; matches the Go
    SDK's `WithRetry(0, _)` no-op identity).
    """

    max_attempts: int = 0
    backoff: float = 0.5


@dataclass
class WrapperOptions:
    """Knobs for the BaseTransport chain.

    Mirrors the Go SDK's `WithRetry` / `WithLogger` options shape
    and the Node SDK's `FaaSClientOptions`.
    """

    retry: RetryOptions = field(default_factory=RetryOptions)
    logger: logging.Logger | None = None


def build_chain(
    user_transport: httpx.BaseTransport,
    *,
    options: WrapperOptions,
) -> httpx.BaseTransport:
    """Compose the BaseTransport chain in canonical order.
    Outer -> inner: Retry -> Logging -> Rfc7807 -> Idempotency
    -> user_transport.
    """
    chain: httpx.BaseTransport = user_transport
    chain = IdempotencyTransport(chain)
    chain = Rfc7807Layer(chain)
    chain = LoggingTransport(chain, logger=options.logger)
    chain = RetryTransport(
        chain,
        max_attempts=options.retry.max_attempts,
        backoff=options.retry.backoff,
    )
    return chain


def build_async_chain(
    user_transport: httpx.AsyncBaseTransport,
    *,
    options: WrapperOptions,
) -> httpx.AsyncBaseTransport:
    """Async sibling of `build_chain`. Same canonical order
    (Retry -> Logging -> Rfc7807 -> Idempotency -> user_transport)
    on the async path. Every layer is `httpx.AsyncBaseTransport`.
    """
    chain: httpx.AsyncBaseTransport = user_transport
    chain = AsyncIdempotencyTransport(chain)
    chain = AsyncRfc7807Layer(chain)
    chain = AsyncLoggingTransport(chain, logger=options.logger)
    chain = AsyncRetryTransport(
        chain,
        max_attempts=options.retry.max_attempts,
        backoff=options.retry.backoff,
    )
    return chain


def install_chain(
    client: Any,
    *,
    options: WrapperOptions,
    verify_ssl: bool | str = True,
) -> None:
    """Swap the generator's sync `Client._client` AND its async
    `Client._async_client` for new ones whose `transport=` is the
    BaseTransport chain (sync or async). Generated service
    functions call `client.get_httpx_client().send(request)` for
    sync and `client.get_async_httpx_client().send(request)` for
    async; both paths are covered here.
    """
    # Sync chain.
    inner = client.get_httpx_client() if hasattr(client, "get_httpx_client") else None
    if inner is not None and hasattr(client, "set_httpx_client"):
        chain = build_chain(
            httpx.HTTPTransport(verify=verify_ssl),
            options=options,
        )
        new_inner = httpx.Client(
            base_url=inner.base_url,
            headers=inner.headers,
            cookies=inner.cookies,
            timeout=inner.timeout,
            follow_redirects=inner.follow_redirects,
            transport=chain,
        )
        client.set_httpx_client(new_inner)
    # Async chain.
    async_inner = (
        client.get_async_httpx_client()
        if hasattr(client, "get_async_httpx_client")
        else None
    )
    if async_inner is not None and hasattr(client, "set_async_httpx_client"):
        async_chain = build_async_chain(
            httpx.AsyncHTTPTransport(verify=verify_ssl),
            options=options,
        )
        new_async = httpx.AsyncClient(
            base_url=async_inner.base_url,
            headers=async_inner.headers,
            cookies=async_inner.cookies,
            timeout=async_inner.timeout,
            follow_redirects=async_inner.follow_redirects,
            transport=async_chain,
        )
        client.set_async_httpx_client(new_async)


__all__ = [
    "MUTATING_METHODS",
    "RetryTransport",
    "LoggingTransport",
    "Rfc7807Layer",
    "IdempotencyTransport",
    "AsyncRetryTransport",
    "AsyncLoggingTransport",
    "AsyncRfc7807Layer",
    "AsyncIdempotencyTransport",
    "RetryOptions",
    "WrapperOptions",
    "build_chain",
    "build_async_chain",
    "install_chain",
]
