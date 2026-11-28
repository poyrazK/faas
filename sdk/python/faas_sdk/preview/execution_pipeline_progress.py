"""Durable pipeline progress watches. Stopping a watcher leaves the pipeline running."""
from __future__ import annotations

import asyncio
import math
import time
from collections.abc import AsyncIterator, Iterator
from typing import Any

from .api.runs import get_execution_pipeline_events
from .models.execution_pipeline_events_response import ExecutionPipelineEventsResponse


def _validate(after: int, interval: float) -> None:
    if isinstance(after, bool) or not isinstance(after, int) or after < 0 or not math.isfinite(interval) or interval <= 0:
        raise ValueError("Invalid pipeline watch options")


def _events(page: ExecutionPipelineEventsResponse, after: int) -> Iterator[dict[str, Any]]:
    if page.next_sequence < after:
        raise RuntimeError("Pipeline progress cursor regressed")
    if page.gap is True:
        yield {"type": "gap", "after": after, "through_sequence": page.events[0].sequence - 1 if page.events else after}
    for event in page.events:
        yield {"type": "progress", "event": event.to_dict()}
    if page.terminal:
        yield {"type": "terminal", "status": page.progress.status if hasattr(page.progress,"status") else "terminal", "next_sequence": page.next_sequence}


def watch_execution_pipeline(client: Any, pipeline_id: str, *, after: int = 0, poll_interval: float = 2.0) -> Iterator[dict[str, Any]]:
    _validate(after, poll_interval)
    while True:
        page = get_execution_pipeline_events.sync(pipeline_id, client=client._gen, after=after)
        if not isinstance(page, ExecutionPipelineEventsResponse):
            raise RuntimeError("Could not read pipeline progress")
        yield from _events(page, after)
        after = page.next_sequence
        if page.terminal:
            return
        time.sleep(poll_interval)


async def awatch_execution_pipeline(client: Any, pipeline_id: str, *, after: int = 0, poll_interval: float = 2.0) -> AsyncIterator[dict[str, Any]]:
    _validate(after, poll_interval)
    while True:
        page = await get_execution_pipeline_events.asyncio(pipeline_id, client=client._gen, after=after)
        if not isinstance(page, ExecutionPipelineEventsResponse):
            raise RuntimeError("Could not read pipeline progress")
        for event in _events(page, after):
            yield event
        after = page.next_sequence
        if page.terminal:
            return
        await asyncio.sleep(poll_interval)
