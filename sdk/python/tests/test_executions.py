from __future__ import annotations

import httpx
import pytest

from faas_sdk import FaaSClient, awatch_execution

_EXECUTION_ID = "01234567-89ab-cdef-0123-456789abcdef"
_LIMITS = {
    "timeout_ms": 1000,
    "memory_mb": 128,
    "cpu_millicores": 250,
    "ephemeral_disk_mb": 64,
    "max_output_bytes": 1024,
    "pids_max": 32,
}


def _receipt(status: str, **extra: object) -> dict[str, object]:
    return {
        "id": _EXECUTION_ID,
        "status": status,
        "runtime": "node22",
        "limits": _LIMITS,
        "output_truncated": False,
        "created_at": "2026-01-01T00:00:00+00:00",
        **extra,
    }


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


def test_run_execution_creates_streams_and_fetches_receipt() -> None:
    requests: list[httpx.Request] = []

    def handler(request: httpx.Request) -> httpx.Response:
        requests.append(request)
        if request.method == "POST" and request.url.path == "/v1/executions":
            assert request.headers["idempotency-key"] == "run-key-1"
            body = request.read().decode()
            assert "console.log('hello')" in body
            return httpx.Response(202, json=_receipt("queued"))
        if request.url.path == f"/v1/executions/{_EXECUTION_ID}/events":
            return _stream(
                'id: 1\nevent: status\ndata: {"status":"running"}\n\n'
                'id: 2\nevent: stdout\ndata: {"chunk":"hello"}\n\n'
                'id: 3\nevent: terminal\ndata: {"status":"succeeded"}\n\n'
            )
        if request.method == "GET" and request.url.path == f"/v1/executions/{_EXECUTION_ID}":
            return httpx.Response(200, json=_receipt("succeeded", result={"ok": True}, stdout="hello"))
        return httpx.Response(404)

    client = FaaSClient(
        base_url="https://api.example.test",
        httpx_args={"transport": httpx.MockTransport(handler)},
    )
    try:
        seen: list[str] = []
        receipt = client.run_execution(
            {"runtime": "node22", "source": "console.log('hello')"},
            on_event=lambda event: seen.append(event.type),
            idempotency_key="run-key-1",
            retry_initial=0,
            retry_max=0,
        )
        assert str(receipt.id) == _EXECUTION_ID
        assert receipt.status == "succeeded"
        assert receipt.result == {"ok": True}
        assert seen == ["status", "stdout", "terminal"]
        assert [request.method for request in requests] == ["POST", "GET", "GET"]
    finally:
        client.close()


@pytest.mark.asyncio
async def test_arun_execution_awaits_callback_and_returns_receipt() -> None:
    requests: list[httpx.Request] = []

    def handler(request: httpx.Request) -> httpx.Response:
        requests.append(request)
        if request.method == "POST":
            return httpx.Response(202, json=_receipt("queued"))
        if request.url.path.endswith("/events"):
            return _stream('id: 1\nevent: terminal\ndata: {"status":"succeeded"}\n\n')
        return httpx.Response(200, json=_receipt("succeeded", result=42))

    client = FaaSClient(
        base_url="https://api.example.test",
        httpx_args={"transport": httpx.MockTransport(handler)},
    )
    seen: list[str] = []

    async def on_event(event: object) -> None:
        seen.append(getattr(event, "type"))

    try:
        receipt = await client.arun_execution(
            {"runtime": "node22", "source": "42"},
            on_event=on_event,
            retry_initial=0,
            retry_max=0,
        )
        assert receipt.status == "succeeded"
        assert receipt.result == 42
        assert seen == ["terminal"]
        assert len(requests) == 3
    finally:
        await client.aclose()
