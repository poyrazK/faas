"""Owned async request policy. Generated service signatures stay unchanged."""

from __future__ import annotations

import asyncio
import contextvars
import inspect
import math
import re
import time
from collections.abc import Awaitable, Callable, Coroutine
from contextlib import suppress
from dataclasses import dataclass, field
from datetime import UTC, datetime
from email.utils import parsedate_to_datetime
from typing import Any, Literal, TypeVar

import httpx

from ._rfc7807 import parse_problem, raise_for_problem

T = TypeVar("T")
_UUID4 = re.compile(r"[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}", re.I)
_ATTEMPT = "gregale.async.attempt"


@dataclass(frozen=True, slots=True)
class ResponseInfo:
    """One HTTP attempt, measured until headers. No URL, body or arbitrary headers."""

    request_id: str | None
    status: int
    attempt: int
    duration_ms: float


ResponseObserver = Callable[[ResponseInfo], None | Awaitable[None]]


@dataclass(frozen=True, slots=True)
class RetryPolicy:
    """TOTAL HTTP attempts (1..9). A zero backoff uses 0.1 seconds; cap is 8s.

    Legacy RetryOptions.max_attempts continues to count retries, unchanged.
    """

    max_attempts: int = 1
    backoff: float = 0.1

    def __post_init__(self) -> None:
        if type(self.max_attempts) is not int or not 1 <= self.max_attempts <= 9:
            raise ValueError("max_attempts must be an integer from 1 through 9")
        if (
            not isinstance(self.backoff, (int, float))
            or isinstance(self.backoff, bool)
            or not math.isfinite(self.backoff)
            or self.backoff < 0
        ):
            raise ValueError("backoff must be finite nonnegative seconds")


@dataclass(frozen=True, slots=True)
class RequestOptions:
    """Options for FaaSClient.arequest. Seconds; None inherits the client default,
    zero disables the total deadline. Asyncio task cancellation stops the operation.
    """

    timeout: float | None = None
    retry: RetryPolicy | Literal[False] | None = None
    on_response: ResponseObserver | None = None

    def __post_init__(self) -> None:
        if self.timeout is not None:
            _validate_timeout(self.timeout)
        if self.retry is not None and self.retry is not False and not isinstance(self.retry, RetryPolicy):
            raise TypeError("retry must be a RetryPolicy, False or None")
        if self.on_response is not None and not callable(self.on_response):
            raise TypeError("on_response must be callable")


class RequestTimeoutError(TimeoutError):
    """The SDK's total operation budget expired, including retry/body waits."""


def _validate_timeout(value: float) -> None:
    if not isinstance(value, (int, float)) or isinstance(value, bool) or not math.isfinite(value) or value < 0:
        raise ValueError("timeout must be finite nonnegative seconds")


_BACKGROUND: set[asyncio.Future[Any]] = set()


def _retain(task: asyncio.Future[Any], tasks: set[asyncio.Future[Any]]) -> None:
    tasks.add(task)

    def done(future: asyncio.Future[Any]) -> None:
        tasks.discard(future)
        with suppress(Exception, asyncio.CancelledError):
            future.result()

    task.add_done_callback(done)


@dataclass(eq=False)
class _Scope:
    owner: object
    options: RequestOptions
    deadline: float | None
    active: bool = True
    streams: set[_OwnedStream] = field(default_factory=set)

    def check(self) -> None:
        if not self.active:
            raise asyncio.CancelledError()
        if self.deadline is not None and asyncio.get_running_loop().time() >= self.deadline:
            raise RequestTimeoutError("SDK request deadline exceeded")


_SCOPES: contextvars.ContextVar[tuple[_Scope, ...]] = contextvars.ContextVar("gregale_async_requests", default=())


def _scope_for(owner: object) -> _Scope | None:
    return next((scope for scope in reversed(_SCOPES.get()) if scope.owner is owner), None)


async def run_request(
    owner: object,
    operation: Callable[[], Coroutine[Any, Any, T]],
    options: RequestOptions,
    default_timeout: float,
) -> T:
    """Own a worker so a transport suppressing cancellation cannot hold the caller
    past its deadline. Late responses are retired without observer delivery.
    """
    if not callable(operation):
        raise TypeError("operation must be a factory returning a fresh coroutine")
    budget = default_timeout if options.timeout is None else options.timeout
    _validate_timeout(budget)
    scope = _Scope(owner, options, asyncio.get_running_loop().time() + budget if budget else None)

    async def invoke() -> T:
        token = _SCOPES.set((*_SCOPES.get(), scope))
        try:
            scope.check()
            coroutine = operation()
            if not inspect.iscoroutine(coroutine) or inspect.getcoroutinestate(coroutine) != inspect.CORO_CREATED:
                raise TypeError("operation must return a fresh coroutine, not an existing Task or Future")
            result = await coroutine
            scope.check()
            return result
        finally:
            _SCOPES.reset(token)

    worker = asyncio.create_task(invoke())
    _retain(worker, _BACKGROUND)
    try:
        done, _ = await asyncio.wait({worker}, timeout=budget or None)
        if not done:
            raise RequestTimeoutError("SDK request deadline exceeded")
        return worker.result()
    finally:
        scope.active = False
        if not worker.done():
            worker.cancel()
        cleanup: set[asyncio.Future[Any]] = {worker}
        for stream in tuple(scope.streams):
            closing = asyncio.create_task(stream.aclose())
            _retain(closing, _BACKGROUND)
            cleanup.add(closing)
        # Give cooperative HTTPX cancellation/close a turn without waiting for
        # arbitrary user transport cleanup to finish indefinitely.
        await asyncio.wait(cleanup, timeout=0)


class _OwnedStream(httpx.AsyncByteStream):
    def __init__(self, inner: httpx.AsyncByteStream, scope: _Scope) -> None:
        self.inner, self.scope, self.closed = inner, scope, False
        scope.streams.add(self)

    async def __aiter__(self):
        try:
            async for chunk in self.inner:
                self.scope.check()
                yield chunk
        except BaseException:
            with suppress(Exception, asyncio.CancelledError):
                await self.aclose()
            raise

    async def aclose(self) -> None:
        if not self.closed:
            self.closed = True
            self.scope.streams.discard(self)
            await self.inner.aclose()


class AsyncResponseTransport(httpx.AsyncBaseTransport):
    """Observe raw responses before Problem decoding or retry classification."""

    def __init__(self, inner: httpx.AsyncBaseTransport, observer: ResponseObserver | None) -> None:
        self.inner, self.observer = inner, observer
        self.tasks: set[asyncio.Future[Any]] = set()

    def observe(self, observer: ResponseObserver | None, info: ResponseInfo) -> None:
        if observer is None:
            return
        context = contextvars.copy_context()
        context.run(_SCOPES.set, ())
        try:
            result = context.run(observer, info)
            if inspect.isawaitable(result):
                _retain(context.run(asyncio.ensure_future, result, loop=asyncio.get_running_loop()), self.tasks)
        except (Exception, asyncio.CancelledError):
            pass

    async def handle_async_request(self, request: httpx.Request) -> httpx.Response:
        scope = _scope_for(self)
        if scope:
            scope.check()
        request.extensions[_ATTEMPT] = request.extensions.get(_ATTEMPT, 0) + 1
        start = time.perf_counter()
        response = await self.inner.handle_async_request(request)
        duration = max(0.0, (time.perf_counter() - start) * 1000)
        if scope:
            try:
                scope.check()
            except BaseException:
                closing = asyncio.create_task(response.aclose())
                _retain(closing, _BACKGROUND)
                raise
            if not response.is_closed and isinstance(response.stream, httpx.AsyncByteStream):
                response.stream = _OwnedStream(response.stream, scope)
        request_id = response.headers.get("X-Request-ID")
        info = ResponseInfo(
            request_id if request_id and _UUID4.fullmatch(request_id) else None,
            response.status_code,
            request.extensions[_ATTEMPT],
            duration,
        )
        self.observe(self.observer, info)
        if scope:
            self.observe(scope.options.on_response, info)
        return response

    async def aclose(self) -> None:
        for task in tuple(self.tasks):
            task.cancel()
        if self.tasks:
            await asyncio.wait(tuple(self.tasks), timeout=0)
        await self.inner.aclose()


def _retry_after(value: str | None) -> float:
    if not value:
        return 0
    try:
        delay = (
            float(int(value))
            if re.fullmatch(r"\d+", value.strip())
            else (parsedate_to_datetime(value) - datetime.now(UTC)).total_seconds()
        )
        return max(0.0, delay) if math.isfinite(delay) else 0
    except (ValueError, TypeError, OverflowError):
        return 0


class AsyncRequestTransport(httpx.AsyncBaseTransport):
    """Keep the original chain for existing calls, and apply owned policy to
    explicitly scoped calls. All mutable state lives on each HTTP request.
    """

    def __init__(
        self,
        legacy: httpx.AsyncBaseTransport,
        attempt: httpx.AsyncBaseTransport,
        owner: AsyncResponseTransport,
        default_retry: RetryPolicy,
    ) -> None:
        self.legacy, self.attempt, self.owner, self.default_retry = legacy, attempt, owner, default_retry

    async def handle_async_request(self, request: httpx.Request) -> httpx.Response:
        request.extensions = {**request.extensions, _ATTEMPT: 0}
        scope = _scope_for(self.owner)
        if scope is None:
            return await self.legacy.handle_async_request(request)
        scope.check()
        remaining = max(0.0, scope.deadline - asyncio.get_running_loop().time()) if scope.deadline else None
        request.extensions["timeout"] = dict.fromkeys(("connect", "read", "write", "pool"), remaining)
        policy = RetryPolicy() if scope.options.retry is False else scope.options.retry or self.default_retry
        try:
            body = request.content
        except httpx.RequestNotRead:
            body = None
        attempts = policy.max_attempts if body is not None else 1
        for number in range(attempts):
            scope.check()
            if body is not None:
                request.stream = httpx.ByteStream(body)
            response = await self.attempt.handle_async_request(request)
            retryable = response.status_code == 429 or response.status_code >= 500
            try:
                if response.status_code == 422 and response.headers.get("Retry-After"):
                    await response.aread()
                    with suppress(ValueError):
                        retryable = (
                            isinstance(response.json(), dict)
                            and response.json().get("code") == "traffic_policy_too_complex"
                        )
                if retryable and number + 1 < attempts:
                    delay = max(
                        min(8.0, (policy.backoff or 0.1) * 2**number), _retry_after(response.headers.get("Retry-After"))
                    )
                    await response.aclose()
                    await asyncio.sleep(delay)
                    continue
                if response.status_code >= 400:
                    content = await response.aread()
                    problem = parse_problem(content, response.headers.get("Content-Type", ""), response.status_code)
                    if problem is not None:
                        await response.aclose()
                        raise_for_problem(problem)
                return response
            except BaseException:
                with suppress(Exception, asyncio.CancelledError):
                    await response.aclose()
                raise
        raise RuntimeError("retry budget exhausted")

    async def aclose(self) -> None:
        await self.legacy.aclose()


class _BorrowedTransport(httpx.AsyncBaseTransport):
    def __init__(self, source: httpx.AsyncClient) -> None:
        self.source = source
        self.transport = source._transport

    async def handle_async_request(self, request: httpx.Request) -> httpx.Response:
        if self.source.is_closed:
            raise RuntimeError("The owning SDK client is closed")
        return await self.transport.handle_async_request(request)

    async def aclose(self) -> None:
        pass  # The SDK client owns the connection pool.


def owned_async_transport(source: httpx.AsyncClient) -> AsyncRequestTransport:
    """Find the policy owner through borrowed configuration snapshots."""
    transport = source._transport
    while isinstance(transport, _BorrowedTransport):
        transport = transport.transport
    if not isinstance(transport, AsyncRequestTransport):
        raise TypeError("This operation requires the SDK async transport chain")
    return transport


def snapshot_async_client(source: httpx.AsyncClient) -> httpx.AsyncClient:
    """Capture configuration for a multi-request helper, borrowing its pool."""
    return httpx.AsyncClient(
        base_url=source.base_url,
        headers=httpx.Headers(source.headers),
        cookies=httpx.Cookies(source.cookies),
        params=source.params,
        auth=source._auth,
        timeout=httpx.Timeout(source.timeout),
        follow_redirects=source.follow_redirects,
        max_redirects=source.max_redirects,
        event_hooks={name: list(hooks) for name, hooks in source.event_hooks.items()},
        transport=_BorrowedTransport(source),
        trust_env=False,
    )
