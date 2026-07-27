"""faas_sdk._sse - Server-Sent Events stream parser.

Mirrors the Go SDK's `pkg/api/sse.go::Decoder` and the Node SDK's
`sdk/node/src/sse.ts::streamSse`. Yields typed `SseEvent` records
(`event`, `data`, `id`, `retry_ms`) until the upstream
`httpx.Response` is exhausted.

The parser is robust to chunk-boundary splits via an internal
buffer that carries trailing partial lines across reads.
"""

from __future__ import annotations

import json
from collections.abc import AsyncIterator, Iterator
from dataclasses import dataclass

import httpx


@dataclass(frozen=True)
class SseEvent:
    """A single SSE event, parsed from the wire.

    `data` is the concatenated `data:` payload (newlines between
    `data:` lines preserved as `\\n`). `retry_ms` is the parsed
    `retry:` field in milliseconds (None if absent).
    """

    event: str | None
    data: str
    id: str | None
    retry_ms: int | None


def _parse_frame(buffer: str) -> tuple[SseEvent | None, str]:
    """Parse a single frame from the buffer.

    Returns `(event, leftover)`. `event` is None when the buffer
    contains only comments / blanks (no dispatchable event yet).
    """
    if "\n\n" not in buffer:
        return None, buffer
    frame, _, buffer = buffer.partition("\n\n")

    event_name: str | None = None
    data_lines: list[str] = []
    event_id: str | None = None
    retry_ms: int | None = None

    for raw in frame.splitlines():
        # Comments (start with ':') are ignored per the SSE spec.
        if not raw or raw.startswith(":"):
            continue
        if ":" in raw:
            field, _, value = raw.partition(":")
            if value.startswith(" "):
                value = value[1:]
        else:
            field, value = raw, ""
        if field == "event":
            event_name = value
        elif field == "data":
            data_lines.append(value)
        elif field == "id":
            event_id = value
        elif field == "retry":
            try:
                retry_ms = int(value)
            except ValueError:
                retry_ms = None

    if not data_lines and event_name is None and event_id is None and retry_ms is None:
        return None, buffer

    return (
        SseEvent(
            event=event_name,
            data="\n".join(data_lines),
            id=event_id,
            retry_ms=retry_ms,
        ),
        buffer,
    )


def iter_sse(response: httpx.Response) -> Iterator[SseEvent]:
    """Sync SSE consumer over an `httpx.Response`. Caller is
    responsible for closing `response` when iteration ends.
    """
    buffer = ""
    for line in response.iter_lines():
        if line is None:
            continue
        buffer += line + "\n"
        while True:
            event, buffer = _parse_frame(buffer)
            if event is None:
                break
            yield event


async def aiter_sse(response: httpx.Response) -> AsyncIterator[SseEvent]:
    """Async SSE consumer over an `httpx.Response`."""
    buffer = ""
    async for line in response.aiter_lines():
        if line is None:
            continue
        buffer += line + "\n"
        while True:
            event, buffer = _parse_frame(buffer)
            if event is None:
                break
            yield event


def parse_data_json(event: SseEvent) -> object:
    """Convenience: `json.loads(event.data)`."""
    return json.loads(event.data)


__all__ = [
    "SseEvent",
    "iter_sse",
    "aiter_sse",
    "parse_data_json",
]
