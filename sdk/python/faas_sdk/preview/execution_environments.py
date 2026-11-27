"""Durable build diagnostic watches. Stopping a watcher leaves the build running."""
from __future__ import annotations

import asyncio
import math
import time
from collections.abc import AsyncIterator, Iterator
from typing import Any

from .api.runs import get_execution_environment_events
from .models.execution_environment_events_response import ExecutionEnvironmentEventsResponse


def _validate(after: int, interval: float) -> None:
    if isinstance(after, bool) or not isinstance(after, int) or after < 0 or not math.isfinite(interval) or interval <= 0:
        raise ValueError("Invalid environment watch options")


def _events(page: ExecutionEnvironmentEventsResponse, after: int) -> Iterator[dict[str, Any]]:
    if page.next_sequence < after:
        raise RuntimeError("Environment diagnostic cursor regressed")
    if page.gap is True:
        yield {"type": "gap", "after": after, "through_sequence": page.events[0].sequence - 1 if page.events else after}
    for event in page.events:
        yield {"type": "diagnostic", "event": event.to_dict()}
    if page.status in ("ready", "failed", "retiring", "deleted"):
        yield {"type": "terminal", "status": page.status, "next_sequence": page.next_sequence}


def watch_execution_environment(client: Any, environment_id: str, *, after: int = 0, poll_interval: float = 2.0) -> Iterator[dict[str, Any]]:
    _validate(after, poll_interval)
    while True:
        page = get_execution_environment_events.sync(environment_id, client=client._gen, after=after)
        if not isinstance(page, ExecutionEnvironmentEventsResponse):
            raise RuntimeError("Could not read environment diagnostics")
        yield from _events(page, after)
        after = page.next_sequence
        if page.status in ("ready", "failed", "retiring", "deleted"):
            return
        time.sleep(poll_interval)


async def awatch_execution_environment(client: Any, environment_id: str, *, after: int = 0, poll_interval: float = 2.0) -> AsyncIterator[dict[str, Any]]:
    _validate(after, poll_interval)
    while True:
        page = await get_execution_environment_events.asyncio(environment_id, client=client._gen, after=after)
        if not isinstance(page, ExecutionEnvironmentEventsResponse):
            raise RuntimeError("Could not read environment diagnostics")
        for event in _events(page, after):
            yield event
        after = page.next_sequence
        if page.status in ("ready", "failed", "retiring", "deleted"):
            return
        await asyncio.sleep(poll_interval)
