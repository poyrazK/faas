"""Coordinated local/platform leases for bounded side-effect-free preparation."""

from __future__ import annotations

import asyncio
from collections.abc import Awaitable, Callable
from dataclasses import dataclass, field
from typing import TYPE_CHECKING, Any, TypeVar

from ._event_acknowledgment_preparation_runtime import (
    ConnectionFactory,
    PreparationControl,
    connection_operation,
    renew_processing,
)
from ._event_acknowledgment_protocol import (
    Control,
    EventAcknowledgmentWorkerCancellation,
    EventAcknowledgmentWorkerError,
)
from ._event_consumer_processing_store import _store_error as processing_store_error
from .event_acknowledgment_intent_retry_policy import EventAcknowledgmentIntentStoredRetryPolicy
from .event_acknowledgment_intents import EventAcknowledgmentIntentResult
from .event_acknowledgment_intents import _store_error as intent_store_error
from .event_acknowledgment_preparation_events import EventAcknowledgmentPreparationEvent, PreparationEvents
from .event_consumer_processing import aclaim_event_consumer_processing
from .event_consumer_transactions import _EventConsumerTransactionHooks

if TYPE_CHECKING:
    from .event_acknowledgment_worker import AsyncEventAcknowledgmentWorker

_T = TypeVar("_T")


@dataclass(frozen=True)
class EventAcknowledgmentPreparedOptions:
    preparation_timeout_ms: int
    processing_lease_ms: int = 30000
    on_event: Callable[[EventAcknowledgmentPreparationEvent], None] | None = field(
        default=None, kw_only=True, repr=False
    )


def _settings(options: EventAcknowledgmentPreparedOptions) -> EventAcknowledgmentPreparedOptions:
    if not isinstance(options, EventAcknowledgmentPreparedOptions):
        raise ValueError("invalid acknowledgment preparation budget")
    timeout, lease, observer = options.preparation_timeout_ms, options.processing_lease_ms, options.on_event
    if (
        type(timeout) is not int
        or not 1 <= timeout <= 2147483647
        or type(lease) is not int
        or not 3 <= lease <= 2147483647
    ):
        raise ValueError("invalid acknowledgment preparation budget")
    if observer is not None and not callable(observer):
        raise ValueError("invalid acknowledgment preparation observer")
    return EventAcknowledgmentPreparedOptions(timeout, lease, on_event=observer)


async def _run_prepared(
    worker: AsyncEventAcknowledgmentWorker,
    factory: ConnectionFactory,
    prepare: Callable[[EventAcknowledgmentWorkerCancellation], Awaitable[_T]],
    handler: Callable[[Any, EventAcknowledgmentWorkerCancellation, _T], Awaitable[bytes | str]],
    options: EventAcknowledgmentPreparedOptions,
    parent: Control,
    events: PreparationEvents,
    policy: EventAcknowledgmentIntentStoredRetryPolicy | None = None,
) -> EventAcknowledgmentIntentResult:
    async def stage(
        control: Control,
        callback: Callable[[Any], Awaitable[bytes | str]] | None = None,
        hooks: _EventConsumerTransactionHooks | None = None,
    ) -> EventAcknowledgmentIntentResult:
        result = await connection_operation(
            factory,
            control,
            lambda connection: worker._stage(connection, callback, control, hooks, policy),
            intent_store_error,
        )
        events.intent_committed(result.receipt.replayed)
        return result

    try:
        return await stage(parent, hooks=_EventConsumerTransactionHooks(intent_store_error))
    except EventAcknowledgmentWorkerError as error:
        if error.code != "event_consumer_receipt_missing":
            raise

    # Start the preparation budget only after receipt reconciliation and scope cleanup.
    work = PreparationControl(parent, options.preparation_timeout_ms)
    timer = asyncio.get_running_loop().call_later(
        options.preparation_timeout_ms / 1000,
        work.stop,
        EventAcknowledgmentWorkerError("event_consumer_preparation_timeout"),
    )
    local_stop = asyncio.Event()
    local: asyncio.Task[None] | None = None
    platform: asyncio.Task[None] | None = None

    async def forward() -> None:
        await parent.event.wait()
        work.stop(parent.failure)

    forwarding = asyncio.create_task(forward())
    result: EventAcknowledgmentIntentResult | None = None
    try:
        work.check()
        events.stage("platform_start")
        startup_failure: BaseException | None = None
        try:
            await worker._start(work)
        except BaseException as error:
            startup_failure = error
        if startup_failure is not None:
            already_stopped = work.event.is_set()
            work.stop(startup_failure)
            events.renewal_failed("platform", startup_failure, already_stopped)
            raise startup_failure
        work.check()
        platform = asyncio.create_task(
            worker._monitor(work, lambda error, stopped: events.renewal_failed("platform", error, stopped))
        )
        events.stage("claim")
        lease = await connection_operation(
            factory,
            work,
            lambda connection: aclaim_event_consumer_processing(
                connection, worker._input, options.processing_lease_ms, stop_event=work.event
            ),
            processing_store_error,
        )
        if not lease.completed:
            events.claim_acquired()
        work.check()
        if lease.completed:
            work.preparing = False
            timer.cancel()
            events.stage("receipt_reconciliation")
            result = await stage(work, hooks=_EventConsumerTransactionHooks(intent_store_error))
        else:

            async def renew() -> None:
                failure: BaseException | None = None
                try:
                    await connection_operation(
                        factory,
                        work,
                        lambda connection: lease.renew(connection, options.processing_lease_ms, stop_event=work.event),
                        processing_store_error,
                    )
                except BaseException as error:
                    failure = error
                if failure is not None:
                    already_stopped = work.event.is_set()
                    work.stop(failure)
                    events.renewal_failed("local", failure, already_stopped)
                    raise failure

            events.stage("local_renewal")
            await renew()
            work.check()
            local = asyncio.create_task(renew_processing(renew, options.processing_lease_ms // 3, local_stop, work))
            events.stage("preparation")
            prepared = await events.prepare(lambda: prepare(EventAcknowledgmentWorkerCancellation(work)))
            work.check()
            events.stage("local_handoff")
            local_stop.set()
            await local
            work.check()
            events.stage("local_renewal")
            await renew()
            work.check()
            work.preparing = False
            timer.cancel()
            events.stage("completion")
            async with lease._use(worker._input, work) as hooks:
                result = await stage(
                    work, lambda cursor: handler(cursor, EventAcknowledgmentWorkerCancellation(work), prepared), hooks
                )
    except BaseException as error:
        work.stop(error)
    finally:
        timer.cancel()
        work.stop()
        local_stop.set()
        # Local renewal may be in SQL/COMMIT: never cancel or abandon it.
        if local is not None:
            await local
        if platform is not None:
            platform.cancel()  # Existing monitor joins its bounded HTTP cleanup.
            await asyncio.gather(platform, return_exceptions=True)
        forwarding.cancel()
        await asyncio.gather(forwarding, return_exceptions=True)
        parent.result = work.result
    if result is not None:
        return result
    # Unknown claim/renewal/final COMMIT takes precedence over cancellation/timeout.
    if work.unknown is not None:
        raise work.unknown
    work.check()
    raise RuntimeError("event preparation ended without a result")
