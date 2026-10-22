"""Explicit same-loop ASGI scrape application; importing starts no listener."""

from __future__ import annotations

from collections.abc import Awaitable, Callable
from typing import Any

from faas_sdk import (
    EVENT_ACKNOWLEDGMENT_PREPARATION_PROMETHEUS_CONTENT_TYPE,
    EventAcknowledgmentPreparationMetrics,
    format_event_acknowledgment_preparation_metrics,
)

_Receive = Callable[[], Awaitable[dict[str, Any]]]
_Send = Callable[[dict[str, Any]], Awaitable[None]]
_App = Callable[[dict[str, Any], _Receive, _Send], Awaitable[None]]


def create_preparation_metrics_app(
    metrics: EventAcknowledgmentPreparationMetrics,
) -> _App:
    """The host supplies the same collector, event loop, protected listener and shutdown."""
    if not isinstance(metrics, EventAcknowledgmentPreparationMetrics):
        raise TypeError("preparation metrics collector required")

    async def app(scope: dict[str, Any], _receive: _Receive, send: _Send) -> None:
        if scope.get("type") != "http":
            raise ValueError("HTTP scope required")
        headers = [(b"cache-control", b"no-store")]
        content_type = "text/plain; charset=utf-8"
        if scope.get("path") != "/metrics" or scope.get("query_string", b""):
            status, body = 404, b"not found\n"
        elif scope.get("method") != "GET":
            status, body = 405, b"method not allowed\n"
            headers.append((b"allow", b"GET"))
        else:
            try:
                body = format_event_acknowledgment_preparation_metrics(
                    metrics.snapshot()
                ).encode("utf-8")
                status = 200
                content_type = EVENT_ACKNOWLEDGMENT_PREPARATION_PROMETHEUS_CONTENT_TYPE
            except (ValueError, TypeError):
                status, body = 503, b"metrics unavailable\n"
        headers.extend(
            (
                (b"content-type", content_type.encode("ascii")),
                (b"content-length", str(len(body)).encode("ascii")),
            )
        )
        await send(
            {"type": "http.response.start", "status": status, "headers": headers}
        )
        await send({"type": "http.response.body", "body": body})

    return app
