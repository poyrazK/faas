"""Explicit sequential recovery monitoring. Applications own connection leases and notifications."""

from __future__ import annotations

import asyncio
import inspect
import time
from collections.abc import Awaitable, Callable
from contextlib import AbstractAsyncContextManager
from datetime import UTC, datetime
from typing import Any, NoReturn

from ._event_acknowledgment_intent_watch_policy import (
    Condition,
    EventAcknowledgmentIntentAlert,
    EventAcknowledgmentIntentWatchCallbackError,
    EventAcknowledgmentIntentWatchOptions,
    Policy,
    evidence,
    policy,
)
from ._event_acknowledgment_protocol import Control, EventAcknowledgmentWorkerCancellation, owned_operation, pause
from ._event_deadline_policy import timestamp
from .event_acknowledgment_intent_health import EventAcknowledgmentIntentHealth, aget_event_acknowledgment_intent_health
from .event_consumer_transactions import _uuid

ConnectionFactory = Callable[[], AbstractAsyncContextManager[Any]]
Notify = Callable[[EventAcknowledgmentIntentAlert, EventAcknowledgmentWorkerCancellation], Awaitable[None] | None]


async def _read_health(
    connection_factory: ConnectionFactory,
    account: str,
    app: str,
    settings: Policy,
    control: Control,
) -> EventAcknowledgmentIntentHealth | None:
    """The cooperative deadline includes acquisition, SQL and connection/cursor cleanup."""
    control.check()
    stopped = asyncio.Event()
    started = time.monotonic_ns()

    async def forward() -> None:
        await control.event.wait()
        stopped.set()

    watcher = asyncio.create_task(forward())
    timer = asyncio.get_running_loop().call_later(settings.query_timeout_ms / 1000, stopped.set)
    health = None
    try:
        if control.stopped():
            stopped.set()
        async with connection_factory() as connection:
            control.check()
            if not stopped.is_set() and time.monotonic_ns() - started < settings.query_timeout_ms * 1000000:
                health = await aget_event_acknowledgment_intent_health(
                    connection,
                    account,
                    app,
                    settings.health,
                    stop_event=stopped,
                )
    except (Exception, asyncio.CancelledError):
        health = None  # No raw storage/lease error survives into an alert.
    finally:
        timer.cancel()
        watcher.cancel()
        await asyncio.gather(watcher, return_exceptions=True)
    # Include all cleanup even when timer callbacks were delayed by driver/host work.
    if stopped.is_set() or time.monotonic_ns() - started >= settings.query_timeout_ms * 1000000:
        return None
    return health


async def _notify(notify: Notify, alert: EventAcknowledgmentIntentAlert, control: Control) -> None:
    control.check()
    failed = False
    try:
        result = notify(alert, EventAcknowledgmentWorkerCancellation(control))
        if inspect.isawaitable(result):
            await result
    except (Exception, asyncio.CancelledError):
        failed = True
    # Host cancellation is primary, after the actual callback has finished/cleaned up.
    control.check()
    if failed:
        raise EventAcknowledgmentIntentWatchCallbackError(alert)


def _at(wall: int) -> str:
    at = datetime.fromtimestamp(wall // 1000000000, UTC).replace(microsecond=wall % 1000000000 // 1000)
    return at.isoformat(timespec="microseconds").replace("+00:00", "Z")


async def awatch_event_acknowledgment_intent_health(
    connection_factory: ConnectionFactory,
    account_id: str,
    app_id: str,
    options: EventAcknowledgmentIntentWatchOptions,
    notify: Notify,
    *,
    stop_event: asyncio.Event | None = None,
) -> NoReturn:
    """Watch until cancellation or callback failure. State is in memory and resets on restart.

    The zero-argument factory supplies a fresh, exclusive idle autocommit async
    connection scope for every read, and must discard closed connections.
    Configure bounded acquisition/driver/cleanup calls; deadlines are cooperative.
    """
    if not callable(connection_factory) or not callable(notify):
        raise ValueError("invalid acknowledgment intent watch factory or callback")
    account, app = _uuid(account_id), _uuid(app_id)
    settings = policy(options)
    conditions = []
    if settings.due_threshold > 0:
        conditions.append(Condition("due_backlog", settings.due_threshold))
    if settings.stalled_threshold > 0:
        conditions.append(Condition("stalled_recovery", settings.stalled_threshold))

    async def watch(control: Control) -> NoReturn:
        previous_observed = previous_poll = None
        while True:
            control.check()
            health = await _read_health(connection_factory, account, app, settings, control)
            control.check()
            wall, local = time.time_ns(), time.monotonic_ns()
            max_age = settings.max_age_ms * 1000000
            reason = evidence(health, previous_observed, previous_poll, wall, local, max_age)
            previous_poll = local
            observed = None if health is None else timestamp(health.observed_at)
            # Advance the high-water mark for fresh incomplete reads as Go/Node do.
            if (
                observed is not None
                and observed <= wall
                and wall - observed <= max_age
                and (previous_observed is None or observed > previous_observed)
            ):
                previous_observed = observed
            alerts = []
            for condition in conditions:
                value = 0 if health is None else health.due if condition.metric == "due_backlog" else health.stalled
                transition = condition.update(value, reason, local, settings.sustain_ms * 1000000)
                if transition is not None:
                    change, why = transition
                    alerts.append(
                        EventAcknowledgmentIntentAlert(
                            account, app, condition.metric, change, why, _at(wall), value, condition.threshold, health
                        )
                    )
            for alert in alerts:
                await _notify(notify, alert, control)
            await pause(settings.poll_interval_ms, control)

    return await owned_operation(watch, stop_event)
