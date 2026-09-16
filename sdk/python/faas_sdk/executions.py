"""Typed, resumable disposable execution event streams."""

from __future__ import annotations

import asyncio
import inspect
import json
import time
from collections.abc import AsyncIterator, Awaitable, Callable, Iterator
from dataclasses import dataclass
from typing import TYPE_CHECKING, Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ._sse import SseEvent, aiter_sse, iter_sse
from .api.runs import create_execution, get_execution
from .models.execution_response import ExecutionResponse
from .types import UNSET, Unset

if TYPE_CHECKING:
    from ._wrapper import FaaSClient

ExecutionID = str | UUID


@dataclass(frozen=True, slots=True)
class ExecutionEvent:
    """One decoded event from ``/v1/executions/{id}/events``.

    ``data`` contains the documented JSON fields (``status``, ``chunk``,
    terminal receipt fields) and keeps unknown fields for forward
    compatibility. ``id`` is the monotonic resume cursor.
    """

    type: str
    id: int | None
    data: dict[str, Any]
    raw: SseEvent

    @property
    def status(self) -> str | None:
        value = self.data.get("status")
        return value if isinstance(value, str) else None

    @property
    def chunk(self) -> str | None:
        value = self.data.get("chunk")
        return value if isinstance(value, str) else None


class ExecutionEventParseError(ValueError):
    """The daemon sent a malformed execution event payload."""


def _decode(raw: SseEvent) -> ExecutionEvent:
    if raw.data == "":
        data: dict[str, Any] = {}
    else:
        try:
            parsed = json.loads(raw.data)
        except json.JSONDecodeError as exc:
            raise ExecutionEventParseError(
                f"execution event {raw.event} contains invalid JSON"
            ) from exc
        if isinstance(parsed, dict):
            data = parsed
        else:
            data = {"value": parsed}

    event_id: int | None = None
    if raw.id is not None:
        try:
            event_id = int(raw.id)
        except ValueError:
            # Preserve the event; a malformed cursor cannot advance a
            # resume request, but should not hide the payload itself.
            event_id = None
    return ExecutionEvent(type=raw.event or "message", id=event_id, data=data, raw=raw)


def _path(execution_id: str) -> str:
    return f"/v1/executions/{quote(str(execution_id), safe='')}/events"


def _retryable(exc: BaseException) -> bool:
    if isinstance(exc, (ExecutionEventParseError, ValueError)):
        return False
    status = getattr(exc, "status", None)
    if isinstance(status, int):
        return status == 408 or status == 429 or status >= 500
    if isinstance(exc, httpx.HTTPStatusError):
        return exc.response.status_code == 408 or exc.response.status_code == 429 or exc.response.status_code >= 500
    return isinstance(exc, (httpx.HTTPError, OSError, TimeoutError))


def _status_error(response: httpx.Response) -> httpx.HTTPStatusError:
    return httpx.HTTPStatusError(
        f"execution event stream returned HTTP {response.status_code}",
        request=response.request,
        response=response,
    )


def _sleep(delay: float) -> None:
    if delay > 0:
        time.sleep(delay)


async def _asleep(delay: float) -> None:
    if delay > 0:
        await asyncio.sleep(delay)


def _params(after: int, limit: int) -> dict[str, int]:
    params = {"limit": limit}
    if after > 0:
        params["after"] = after
    return params


def watch_execution(
    client: FaaSClient,
    execution_id: ExecutionID,
    *,
    after: int = 0,
    limit: int = 100,
    retry_initial: float = 0.1,
    retry_max: float = 2.0,
) -> Iterator[ExecutionEvent]:
    """Yield execution events and resume after transient disconnects.

    A non-successful initial response is raised immediately. Once a stream
    has delivered a successful response, EOF before ``terminal`` reconnects
    with the latest cursor. The returned generator owns each response
    context and closes it before reconnecting.
    """

    cursor = max(0, after)
    batch_limit = max(1, limit)
    delay = max(0.0, retry_initial)
    max_delay = max(delay, retry_max)
    connected = False

    while True:
        try:
            with client.httpx_client.stream(
                "GET",
                _path(execution_id),
                params=_params(cursor, batch_limit),
                headers={"Accept": "text/event-stream"},
            ) as response:
                if response.status_code >= 400:
                    raise _status_error(response)
                connected = True
                for raw in iter_sse(response):
                    event = _decode(raw)
                    if event.id is not None and event.id > cursor:
                        cursor = event.id
                    yield event
                    if event.type == "terminal":
                        return
        except Exception as exc:
            if not connected or not _retryable(exc):
                raise
        _sleep(delay)
        delay = min(max(delay * 2, retry_initial), max_delay)


async def awatch_execution(
    client: FaaSClient,
    execution_id: ExecutionID,
    *,
    after: int = 0,
    limit: int = 100,
    retry_initial: float = 0.1,
    retry_max: float = 2.0,
) -> AsyncIterator[ExecutionEvent]:
    """Async counterpart to :func:`watch_execution`."""

    cursor = max(0, after)
    batch_limit = max(1, limit)
    delay = max(0.0, retry_initial)
    max_delay = max(delay, retry_max)
    connected = False

    while True:
        try:
            async with client.async_httpx_client.stream(
                "GET",
                _path(execution_id),
                params=_params(cursor, batch_limit),
                headers={"Accept": "text/event-stream"},
            ) as response:
                if response.status_code >= 400:
                    raise _status_error(response)
                connected = True
                async for raw in aiter_sse(response):
                    event = _decode(raw)
                    if event.id is not None and event.id > cursor:
                        cursor = event.id
                    yield event
                    if event.type == "terminal":
                        return
        except asyncio.CancelledError:
            raise
        except Exception as exc:
            if not connected or not _retryable(exc):
                raise
        await _asleep(delay)
        delay = min(max(delay * 2, retry_initial), max_delay)


def _require_execution_response(value: Any, operation: str) -> ExecutionResponse:
    if isinstance(value, ExecutionResponse):
        return value
    raise RuntimeError(f"{operation} did not return an execution receipt")


def run_execution(
    client: FaaSClient,
    body: Any,
    *,
    on_event: Callable[[ExecutionEvent], Any] | None = None,
    idempotency_key: str | Unset | None = UNSET,
    after: int = 0,
    limit: int = 100,
    retry_initial: float = 0.1,
    retry_max: float = 2.0,
) -> ExecutionResponse:
    """Create one disposable execution, stream it to completion, and fetch
    the terminal receipt.

    ``on_event`` is called in event order. The callback may raise to stop
    local consumption; the already-admitted remote execution is not cancelled.
    Source/files are used only by the ephemeral guest and never persisted as a
    customer-facing workspace.
    """
    if idempotency_key is None:
        idempotency_key = UNSET
    created = _require_execution_response(
        create_execution.sync(
            client=client.inner,
            body=body,
            idempotency_key=idempotency_key,
        ),
        "create_execution",
    )
    for event in watch_execution(
        client,
        created.id,
        after=after,
        limit=limit,
        retry_initial=retry_initial,
        retry_max=retry_max,
    ):
        if on_event is not None:
            on_event(event)
        if event.type == "terminal":
            break
    return _require_execution_response(
        get_execution.sync(client=client.inner, id=created.id),
        "get_execution",
    )


async def arun_execution(
    client: FaaSClient,
    body: Any,
    *,
    on_event: Callable[[ExecutionEvent], Awaitable[Any] | Any] | None = None,
    idempotency_key: str | Unset | None = UNSET,
    after: int = 0,
    limit: int = 100,
    retry_initial: float = 0.1,
    retry_max: float = 2.0,
) -> ExecutionResponse:
    """Async counterpart to :func:`run_execution`.

    ``on_event`` may be synchronous or awaitable. Cancelling the surrounding
    task stops local consumption while the remote execution remains governed
    by its admitted deadline.
    """
    if idempotency_key is None:
        idempotency_key = UNSET
    created = _require_execution_response(
        await create_execution.asyncio(
            client=client.inner,
            body=body,
            idempotency_key=idempotency_key,
        ),
        "create_execution",
    )
    async for event in awatch_execution(
        client,
        created.id,
        after=after,
        limit=limit,
        retry_initial=retry_initial,
        retry_max=retry_max,
    ):
        if on_event is not None:
            callback_result = on_event(event)
            if inspect.isawaitable(callback_result):
                await callback_result
        if event.type == "terminal":
            break
    return _require_execution_response(
        await get_execution.asyncio(client=client.inner, id=created.id),
        "get_execution",
    )


__all__ = [
    "ExecutionEvent",
    "ExecutionEventParseError",
    "ExecutionID",
    "arun_execution",
    "watch_execution",
    "awatch_execution",
    "run_execution",
]
