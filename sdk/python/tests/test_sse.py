"""test_sse — unit tests for the SSE parser.

The parser is hand-rolled (`faas_sdk._sse._parse_frame` +
`iter_sse` + `aiter_sse`). These tests cover the canonical SSE wire
shape: field/value pairs separated by `\\n`, terminated by `\\n\\n`;
comments starting with `:` ignored; multi-line `data:` concatenated
with `\\n`; `retry:` parsed as int milliseconds; `id:` preserved.
"""

from __future__ import annotations

import httpx
import pytest

from faas_sdk._sse import SseEvent, aiter_sse, iter_sse, parse_data_json


def _sse_response(body: bytes) -> httpx.Response:
    """Wrap a UTF-8 SSE body in an `httpx.Response` for the parser."""
    return httpx.Response(
        200,
        headers={"Content-Type": "text/event-stream"},
        content=body,
    )


def test_parse_frame_yields_event_with_data() -> None:
    resp = _sse_response(b"data: hello\n\n")
    events = list(iter_sse(resp))
    assert len(events) == 1
    assert events[0].data == "hello"
    assert events[0].event is None
    assert events[0].id is None
    assert events[0].retry_ms is None


def test_parse_frame_handles_named_event() -> None:
    resp = _sse_response(b"event: log\ndata: hello\n\n")
    events = list(iter_sse(resp))
    assert len(events) == 1
    assert events[0].event == "log"
    assert events[0].data == "hello"


def test_parse_frame_concatenates_multiline_data() -> None:
    """Multi-line `data:` fields are joined with `\\n` (RFC 8895)."""
    resp = _sse_response(b"data: line1\ndata: line2\n\n")
    events = list(iter_sse(resp))
    assert len(events) == 1
    assert events[0].data == "line1\nline2"


def test_parse_frame_ignores_comments() -> None:
    resp = _sse_response(b": this is a comment\ndata: hello\n\n")
    events = list(iter_sse(resp))
    assert len(events) == 1
    assert events[0].data == "hello"


def test_parse_frame_handles_id_and_retry() -> None:
    resp = _sse_response(b"id: 42\nretry: 1500\ndata: hello\n\n")
    events = list(iter_sse(resp))
    assert len(events) == 1
    assert events[0].id == "42"
    assert events[0].retry_ms == 1500


def test_parse_frame_yields_multiple_events() -> None:
    body = b"data: a\n\ndata: b\n\ndata: c\n\n"
    resp = _sse_response(body)
    events = list(iter_sse(resp))
    assert [e.data for e in events] == ["a", "b", "c"]


def test_parse_frame_returns_none_on_pure_comment_frame() -> None:
    """A frame containing only `:comment` is non-dispatchable; the
    parser returns None and skips it.
    """
    # Hand-test the inner helper by feeding it a buffer with a
    # comment-only frame.
    from faas_sdk._sse import _parse_frame

    event, leftover = _parse_frame(": this is a comment\n\n")
    assert event is None
    assert leftover == ""


def test_parse_data_json_decodes_payload() -> None:
    body = b'data: {"foo":"bar","n":1}\n\n'
    resp = _sse_response(body)
    events = list(iter_sse(resp))
    payload = parse_data_json(events[0])
    assert payload == {"foo": "bar", "n": 1}


@pytest.mark.asyncio
async def test_aiter_sse_yields_same_events_as_sync() -> None:
    body = b"data: a\n\ndata: b\n\n"
    resp = _sse_response(body)
    events: list[SseEvent] = []
    async for e in aiter_sse(resp):
        events.append(e)
    assert [e.data for e in events] == ["a", "b"]
