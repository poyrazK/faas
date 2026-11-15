"""Private cooperative preparation ownership and bounded connection-scope diagnostics."""

from __future__ import annotations

import asyncio
import time
from collections.abc import Awaitable, Callable
from contextlib import AbstractAsyncContextManager
from typing import Any, TypeVar

from ._event_acknowledgment_protocol import (
    Control,
    EventAcknowledgmentWorkerCancelled,
    EventAcknowledgmentWorkerError,
)
from .event_acknowledgment_intents import EventAcknowledgmentIntentResult
from .event_consumer_transactions import EventConsumerCommitUnknownError

ConnectionFactory = Callable[[], AbstractAsyncContextManager[Any]]
_T = TypeVar("_T")
_ABSENT = object()


class PreparationControl(Control):
    def __init__(self, parent: Control, timeout_ms: int):
        super().__init__()
        self.parent = parent
        self.deadline = time.monotonic_ns() + timeout_ms * 1000000
        self.preparing = True
        self.problem: BaseException | None = None
        self.unknown: EventConsumerCommitUnknownError | None = None

    def stop(self, failure: BaseException | None = None) -> None:
        # Even a renewal finishing after another failure can reveal uncertain COMMIT.
        if isinstance(failure, EventConsumerCommitUnknownError) and self.unknown is None:
            self.unknown = failure
        if not self.event.is_set():
            self.problem = failure
            self.event.set()

    def stopped(self) -> bool:
        if self.parent.stopped():
            self.stop(self.parent.failure)
        if self.preparing and time.monotonic_ns() >= self.deadline:
            self.stop(EventAcknowledgmentWorkerError("event_consumer_preparation_timeout", self.result))
        return self.event.is_set()

    def check(self) -> None:
        if not self.stopped():
            return
        if self.unknown is not None:
            raise self.unknown from None
        if isinstance(self.problem, EventAcknowledgmentWorkerError):
            raise EventAcknowledgmentWorkerError(self.problem.code, self.result, self.problem.status) from None
        if self.problem is not None and not isinstance(self.problem, asyncio.CancelledError):
            raise self.problem from None
        raise EventAcknowledgmentWorkerCancelled(self.result) from None


async def connection_operation(
    factory: ConnectionFactory,
    control: Control,
    operation: Callable[[Any], Awaitable[_T]],
    storage_error: Callable[[], Exception],
) -> _T:
    """Join acquisition/cleanup. Keep actual commit evidence if context exit fails."""
    control.check()
    result: Any = _ABSENT
    operation_failure: BaseException | None = None
    failure: BaseException | None = None
    connection = None
    try:
        async with factory() as connection:
            try:
                control.check()
                result = await operation(connection)
            except BaseException as error:
                operation_failure = error
                raise
    except BaseException as error:
        if error is operation_failure:
            failure = error
        elif isinstance(error, asyncio.CancelledError):
            failure = EventAcknowledgmentWorkerCancelled(control.result)
        else:
            failure = storage_error()
        if error is not operation_failure and connection is not None:
            try:
                await connection.close()
            except BaseException:
                pass
    # A context manager cannot suppress an operation failure or erase uncertainty.
    if isinstance(operation_failure, EventConsumerCommitUnknownError):
        raise operation_failure
    if isinstance(result, EventAcknowledgmentIntentResult):
        # Confirmed local staging remains confirmed after late cancellation/cleanup.
        return result
    if failure is not None:
        raise failure
    if operation_failure is not None:
        raise operation_failure
    if result is _ABSENT:
        raise storage_error()
    return result


async def renew_processing(
    renew: Callable[[], Awaitable[None]], interval_ms: int, stopping: asyncio.Event, work: PreparationControl
) -> None:
    """Stop waits, not active SQL. Each call finishes before the next wait begins."""
    try:
        while not work.stopped() and not stopping.is_set():
            waits = (asyncio.create_task(stopping.wait()), asyncio.create_task(work.event.wait()))
            try:
                await asyncio.wait(waits, timeout=interval_ms / 1000, return_when=asyncio.FIRST_COMPLETED)
            finally:
                for wait in waits:
                    wait.cancel()
                await asyncio.gather(*waits, return_exceptions=True)
            if work.stopped() or stopping.is_set():
                return
            await renew()
    except BaseException as error:
        work.stop(error)
