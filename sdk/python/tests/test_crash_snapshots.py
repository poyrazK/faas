"""ADR-733 SDK trigger helper: request shape and outcomes, never raising."""

from __future__ import annotations

import asyncio
import json

import httpx

from faas_sdk import CRASH_SNAPSHOT_ENDPOINT, CrashSnapshotResult, acapture_crash_snapshot, capture_crash_snapshot


def test_capture_posts_reason_route_and_wait() -> None:
    seen: list[httpx.Request] = []

    def handler(request: httpx.Request) -> httpx.Response:
        seen.append(request)
        return httpx.Response(200, json={"status": "captured", "capture_id": "c1"})

    result = capture_crash_snapshot("checkout panic", route="/orders", wait_ms=5000, transport=httpx.MockTransport(handler))
    assert result == CrashSnapshotResult(status="captured", capture_id="c1")
    assert str(seen[0].url) == CRASH_SNAPSHOT_ENDPOINT
    assert seen[0].method == "POST"
    assert json.loads(seen[0].content) == {"reason": "checkout panic", "route": "/orders", "wait_ms": 5000}


def test_capture_reports_fork_refusal_and_unreachable() -> None:
    forked = capture_crash_snapshot(
        transport=httpx.MockTransport(lambda _: httpx.Response(200, json={"status": "captured", "capture_id": "c2", "in_fork": True}))
    )
    assert forked == CrashSnapshotResult(status="captured", capture_id="c2", in_fork=True)
    refused = capture_crash_snapshot(transport=httpx.MockTransport(lambda _: httpx.Response(409, json={"status": "refused"})))
    assert refused.status == "refused"

    def boom(_: httpx.Request) -> httpx.Response:
        raise httpx.ConnectError("no metadata endpoint")

    assert capture_crash_snapshot(transport=httpx.MockTransport(boom)) == CrashSnapshotResult(status="unavailable")


def test_async_capture() -> None:
    transport = httpx.MockTransport(lambda _: httpx.Response(202, json={"status": "pending", "capture_id": "c3"}))
    result = asyncio.run(acapture_crash_snapshot("slow", transport=transport))
    assert result == CrashSnapshotResult(status="pending", capture_id="c3")
