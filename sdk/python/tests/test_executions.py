from __future__ import annotations

import httpx
import pytest

from faas_sdk import FaaSClient, awatch_execution


def _stream(body: str) -> httpx.Response:
    return httpx.Response(
        200,
        headers={"content-type": "text/event-stream"},
        content=body.encode(),
    )


def test_watch_execution_decodes_and_resumes_with_latest_cursor() -> None:
    requests: list[httpx.Request] = []
    calls = 0

    def handler(request: httpx.Request) -> httpx.Response:
        nonlocal calls
        requests.append(request)
        calls += 1
        if calls == 1:
            return _stream(
                'id: 1\nevent: status\ndata: {"status":"running"}\n\n'
                'id: 2\nevent: stdout\ndata: {"chunk":"hello\\n"}\n\n'
            )
        return _stream('id: 3\nevent: terminal\ndata: {"status":"succeeded","exit_code":0}\n\n')

    client = FaaSClient(
        base_url="https://api.example.test",
        httpx_args={"transport": httpx.MockTransport(handler)},
    )
    try:
        events = list(client.watch_execution("run/1", retry_initial=0, retry_max=0))
        assert [event.type for event in events] == ["status", "stdout", "terminal"]
        assert events[0].id == 1
        assert events[0].status == "running"
        assert events[1].chunk == "hello\n"
        assert events[2].data["exit_code"] == 0
        assert str(requests[0].url).endswith("/v1/executions/run%2F1/events?limit=100")
        assert requests[1].url.params["after"] == "2"
    finally:
        client.close()


@pytest.mark.asyncio
async def test_awatch_execution_uses_async_transport() -> None:
    requests: list[httpx.Request] = []

    def handler(request: httpx.Request) -> httpx.Response:
        requests.append(request)
        return _stream('id: 7\nevent: terminal\ndata: {"status":"succeeded"}\n\n')

    client = FaaSClient(
        base_url="https://api.example.test",
        httpx_args={"transport": httpx.MockTransport(handler)},
    )
    try:
        events = [event async for event in awatch_execution(client, "run-async", retry_initial=0, retry_max=0)]
        assert len(events) == 1
        assert events[0].type == "terminal"
        assert requests[0].headers["accept"] == "text/event-stream"
    finally:
        await client.async_httpx_client.aclose()
