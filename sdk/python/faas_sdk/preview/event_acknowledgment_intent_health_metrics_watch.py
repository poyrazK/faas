"""Explicit sequential metrics polling with the health watch's joined read/cancellation protocol."""

from __future__ import annotations

import asyncio
from dataclasses import dataclass
from typing import NoReturn

from ._event_acknowledgment_protocol import Control, owned_operation, pause
from .event_acknowledgment_intent_health import EventAcknowledgmentIntentHealthOptions
from .event_acknowledgment_intent_health import _options as health_options
from .event_acknowledgment_intent_health_metrics import EventAcknowledgmentIntentHealthMetrics
from .event_acknowledgment_intent_watch import ConnectionFactory, _read_health
from .event_consumer_transactions import _uuid


@dataclass(frozen=True, slots=True)
class _Settings:
    health: EventAcknowledgmentIntentHealthOptions
    poll_interval_ms: int
    query_timeout_ms: int


async def awatch_event_acknowledgment_intent_health_metrics(
    connection_factory: ConnectionFactory,
    account_id: str,
    app_id: str,
    metrics: EventAcknowledgmentIntentHealthMetrics,
    *,
    health: EventAcknowledgmentIntentHealthOptions | None = None,
    poll_interval_ms: int = 5000,
    query_timeout_ms: int = 5000,
    stop_event: asyncio.Event | None = None,
) -> NoReturn:
    """Feed one scope/cache until cancellation, with sequential bounded reads and no notifications.

    Supply exclusive idle autocommit async connection scopes. Active acquisition,
    SQL and cleanup are joined; bound driver operations separately. Stop clears
    the cached sample, retaining its timestamp high-water mark.
    """
    if (
        not callable(connection_factory)
        or type(account_id) is not str
        or type(app_id) is not str
        or type(metrics) is not EventAcknowledgmentIntentHealthMetrics
        or type(poll_interval_ms) is not int
        or not 100 <= poll_interval_ms <= 60000
        or type(query_timeout_ms) is not int
        or not 10 <= query_timeout_ms <= 30000
        or stop_event is not None
        and not isinstance(stop_event, asyncio.Event)
    ):
        raise ValueError("invalid recovery health metrics polling options")
    account, app = _uuid(account_id), _uuid(app_id)
    limit, stall = health_options(health)
    settings = _Settings(EventAcknowledgmentIntentHealthOptions(limit, stall), poll_interval_ms, query_timeout_ms)

    async def watch(control: Control) -> NoReturn:
        while True:
            control.check()
            observed = await _read_health(connection_factory, account, app, settings, control)
            control.check()
            if observed is None:
                metrics.record_read_failure()
            else:
                metrics.record(observed)
            await pause(settings.poll_interval_ms, control)

    metrics._begin_polling(account, app)
    try:
        return await owned_operation(watch, stop_event)
    finally:
        metrics._end_polling()
