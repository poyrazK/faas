"""Private DB-clock processing protocol; never retain rejected SQL diagnostics."""

from __future__ import annotations

import asyncio
from collections.abc import Awaitable, Callable
from typing import Any, TypeVar

from ._event_acknowledgment_protocol import Control, EventAcknowledgmentWorkerError
from .event_consumer_transactions import EventConsumerCommitUnknownError, EventConsumerConflictError, _tuple_row

_MESSAGES = {
    "event_consumer_processing_busy": "event consumer processing lease is held",
    "event_consumer_processing_lease_lost": "event consumer processing lease expired or replaced",
    "event_consumer_processing_operation_busy": "event consumer processing lease operation already running",
    "event_consumer_processing_store": "event consumer processing storage operation failed",
}
_T = TypeVar("_T")


class EventConsumerProcessingError(RuntimeError):
    """Fixed code/message; no driver diagnostics, rejected rows or capability data."""

    def __init__(self, code: str):
        if code not in _MESSAGES:
            raise ValueError("invalid consumer processing error code")
        super().__init__(_MESSAGES[code])
        self.code = code


def _store_error() -> EventConsumerProcessingError:
    return EventConsumerProcessingError("event_consumer_processing_store")


def _duration(value: int) -> None:
    if type(value) is not int or not 1 <= value <= 9223372036854:
        raise ValueError("invalid consumer processing lease duration")


def _bytes(value: Any) -> bytes:
    if not isinstance(value, (bytes, memoryview)) or len(value) != 32:
        raise _store_error()
    return bytes(value)


async def _query(cursor: Any, sql: str, params: tuple[Any, ...], control: Control, *, fetch: bool = True) -> Any:
    control.check()
    failed = False
    try:
        await cursor.execute(sql, params)
        result = await cursor.fetchall() if fetch else None
    except Exception:
        failed = True
    if failed:
        raise _store_error()
    control.check()
    return result


def _affected(rows: Any, identity: bytes) -> None:
    if not isinstance(rows, (tuple, list)):
        raise _store_error()
    if len(rows) == 0:
        raise EventConsumerProcessingError("event_consumer_processing_lease_lost")
    if len(rows) != 1 or not isinstance(rows[0], (tuple, list)) or len(rows[0]) != 1:
        raise _store_error()
    if _bytes(rows[0][0]) != identity:
        raise _store_error()


async def _transaction(connection: Any, operation: Callable[[Any], Awaitable[_T]], control: Control) -> _T:
    control.check()
    if connection.autocommit is not True or int(connection.info.transaction_status) != 0:
        raise ValueError("consumer processing requires an exclusively leased idle autocommit connection")
    completed = unknown = discard = False
    failure: BaseException | None = None
    try:
        async with connection.transaction():
            control.check()
            async with connection.cursor(row_factory=_tuple_row) as cursor:
                await _query(cursor, "SET TRANSACTION ISOLATION LEVEL READ COMMITTED", (), control, fetch=False)
                result = await operation(cursor)
                control.check()
            control.check()
            completed = True
    except BaseException as error:
        if completed:
            unknown = discard = True
        elif isinstance(
            error, (EventConsumerProcessingError, EventConsumerConflictError, EventAcknowledgmentWorkerError)
        ):
            failure = error
            discard = (
                isinstance(error, EventConsumerProcessingError) and error.code == "event_consumer_processing_store"
            )
        elif isinstance(error, asyncio.CancelledError):
            failure = error
        else:
            discard = True
            failure = _store_error()
    if discard:
        try:
            await connection.close()
        except BaseException:
            pass
    if unknown:
        raise EventConsumerCommitUnknownError(
            "consumer processing commit outcome unknown; reconcile on a fresh connection"
        )
    if failure is not None:
        raise failure
    return result
