"""Explicit same-loop recovery-health polling and cache-only ASGI scraping."""

from __future__ import annotations

from collections.abc import Awaitable, Callable
from typing import Any

from faas_sdk import (
    EVENT_ACKNOWLEDGMENT_INTENT_HEALTH_PROMETHEUS_CONTENT_TYPE,
    EventAcknowledgmentIntentHealthMetrics,
    EventAcknowledgmentIntentHealthMetricsOptions,
    awatch_event_acknowledgment_intent_health_metrics,
    format_event_acknowledgment_intent_health_metrics,
)

_Receive = Callable[[], Awaitable[dict[str, Any]]]
_Send = Callable[[dict[str, Any]], Awaitable[None]]


class RecoveryHealthMetricsHost:
    """Keep polling, ASGI and this cache in one loop/process. Construction starts nothing."""

    def __init__(self, connection_factory: Any, account_id: str, app_id: str) -> None:
        if not callable(connection_factory):
            raise TypeError("recovery metrics connection factory required")
        self.metrics = EventAcknowledgmentIntentHealthMetrics(
            account_id,
            app_id,
            EventAcknowledgmentIntentHealthMetricsOptions(max_age_ms=30000),
        )
        self._factory, self._account, self._app = connection_factory, account_id, app_id

    async def run(self, *, stop_event: Any = None) -> None:
        await awatch_event_acknowledgment_intent_health_metrics(
            self._factory, self._account, self._app, self.metrics, stop_event=stop_event
        )

    async def app(self, scope: dict[str, Any], _receive: _Receive, send: _Send) -> None:
        if scope.get("type") != "http":
            raise ValueError(
                "HTTP scope required; disable ASGI lifespan for this adapter"
            )
        headers = [(b"cache-control", b"no-store")]
        content_type = "text/plain; charset=utf-8"
        if scope.get("path") != "/metrics" or scope.get("query_string", b""):
            status, body = 404, b"not found\n"
        elif scope.get("method") != "GET":
            status, body = 405, b"method not allowed\n"
            headers.append((b"allow", b"GET"))
        else:
            try:
                body = format_event_acknowledgment_intent_health_metrics(
                    self.metrics.snapshot()
                ).encode("utf-8")
                status, content_type = (
                    200,
                    EVENT_ACKNOWLEDGMENT_INTENT_HEALTH_PROMETHEUS_CONTENT_TYPE,
                )
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
