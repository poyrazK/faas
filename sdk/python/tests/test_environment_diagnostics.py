from __future__ import annotations

import asyncio

import httpx
import pytest
from faas_sdk.preview import FaaSClient


def _page(status: str, *, sequence: int = 8) -> dict[str, object]:
    return {
        "events": [
            {
                "sequence": sequence,
                "stage": "build",
                "code": "build_output",
                "message": "installing",
                "attempt": 1,
                "created_at": "2026-11-15T00:00:00Z",
            }
        ],
        "next_sequence": sequence,
        "gap": True,
        "status": status,
    }


def test_environment_watch_cursor_gap_and_terminal() -> None:
    calls: list[httpx.Request] = []

    def handler(req: httpx.Request) -> httpx.Response:
        calls.append(req)
        return httpx.Response(
            200, json=_page("building") if len(calls) == 1 else {"events": [], "next_sequence": 8, "status": "ready"}
        )

    client = FaaSClient(base_url="https://api.example.test", httpx_args={"transport": httpx.MockTransport(handler)})
    try:
        events = list(client.watch_execution_environment("env-1", poll_interval=0.001))
        assert [e["type"] for e in events] == ["gap", "diagnostic", "terminal"]
        assert calls[1].url.params["after"] == "8"
        assert all(req.method == "GET" for req in calls)
    finally:
        client.close()


@pytest.mark.asyncio
async def test_async_environment_watch_can_be_cancelled_without_cancelling_build() -> None:
    calls: list[httpx.Request] = []

    def handler(req: httpx.Request) -> httpx.Response:
        calls.append(req)
        return httpx.Response(200, json={"events": [], "next_sequence": 0, "status": "building"})

    client = FaaSClient(base_url="https://api.example.test", httpx_args={"transport": httpx.MockTransport(handler)})
    try:
        watcher = client.awatch_execution_environment("env-1", poll_interval=10)
        task = asyncio.create_task(anext(watcher))
        await asyncio.sleep(0.02)
        task.cancel()
        with pytest.raises(asyncio.CancelledError):
            await task
        assert len(calls) == 1 and calls[0].method == "GET"
    finally:
        await client.aclose()
