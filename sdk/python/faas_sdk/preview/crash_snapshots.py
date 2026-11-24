"""Crash snapshots from inside an app (ADR-733 SDK trigger).

Call :func:`capture_crash_snapshot` from an error handler, before the error is
handled, to capture the app's own instance while the failing request's state
is still in memory. The call blocks while Gregale pauses and snapshots the
instance, then the instance resumes and the call returns. Open the capture
later as a fork (``POST /v1/apps/{slug}/crash-snapshots/{id}/fork``).

In a fork of that capture the same call returns again with ``in_fork=True``,
so the handler can continue in the debug copy.

The app must opt in (``PUT /v1/apps/{slug}/crash-snapshots/settings``); refused requests
(no opt-in, a capture in flight, within the cooldown) return
``status="refused"`` instead of raising.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any

import httpx

CRASH_SNAPSHOT_ENDPOINT = "http://169.254.169.254/v1/crash-snapshots:capture"


@dataclass(frozen=True)
class CrashSnapshotResult:
    """Outcome of a capture request.

    ``status`` is one of captured, pending, refused, failed, not_enabled,
    unavailable or invalid_request.
    """

    status: str
    capture_id: str | None = None
    code: str | None = None
    in_fork: bool = False


def _request_body(reason: str | None, route: str | None, wait_ms: int | None) -> dict[str, Any]:
    body: dict[str, Any] = {}
    if reason:
        body["reason"] = reason
    if route:
        body["route"] = route
    if wait_ms is not None:
        body["wait_ms"] = int(wait_ms)
    return body


def _timeout(wait_ms: int | None) -> float:
    wait = 15000 if not wait_ms or wait_ms <= 0 else min(wait_ms, 30000)
    return wait / 1000 + 20


def _result(response: httpx.Response) -> CrashSnapshotResult:
    try:
        data = response.json()
    except ValueError:
        return CrashSnapshotResult(status="unavailable")
    return CrashSnapshotResult(
        status=str(data.get("status") or "unavailable"),
        capture_id=data.get("capture_id") or None,
        code=data.get("code") or None,
        in_fork=data.get("in_fork") is True,
    )


def capture_crash_snapshot(
    reason: str | None = None,
    *,
    route: str | None = None,
    wait_ms: int | None = None,
    endpoint: str = CRASH_SNAPSHOT_ENDPOINT,
    transport: httpx.BaseTransport | None = None,
) -> CrashSnapshotResult:
    """Ask Gregale to capture this instance now. Never raises."""
    try:
        with httpx.Client(transport=transport, timeout=_timeout(wait_ms)) as client:
            return _result(client.post(endpoint, json=_request_body(reason, route, wait_ms)))
    except httpx.HTTPError:
        return CrashSnapshotResult(status="unavailable")


async def acapture_crash_snapshot(
    reason: str | None = None,
    *,
    route: str | None = None,
    wait_ms: int | None = None,
    endpoint: str = CRASH_SNAPSHOT_ENDPOINT,
    transport: httpx.AsyncBaseTransport | None = None,
) -> CrashSnapshotResult:
    """Async form of :func:`capture_crash_snapshot`."""
    try:
        async with httpx.AsyncClient(transport=transport, timeout=_timeout(wait_ms)) as client:
            return _result(await client.post(endpoint, json=_request_body(reason, route, wait_ms)))
    except httpx.HTTPError:
        return CrashSnapshotResult(status="unavailable")
