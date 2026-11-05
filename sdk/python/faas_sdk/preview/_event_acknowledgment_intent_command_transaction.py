"""Private, shielded transaction protocol for application-owned intent commands."""

from __future__ import annotations

import asyncio
from collections.abc import Awaitable, Callable
from typing import Any, TypeVar

from ._event_acknowledgment_protocol import Control, EventAcknowledgmentWorkerCancelled
from .event_acknowledgment_intents import EventAcknowledgmentIntentError, _query, _store_error
from .event_consumer_transactions import EventConsumerCommitUnknownError, _tuple_row

_T = TypeVar("_T")


async def _command_transaction(
    connection: Any,
    operation: Callable[[Any], Awaitable[tuple[_T, bool]]],
    control: Control,
) -> _T:
    """Owned/shielded by the public boundary. Replay finishes with ROLLBACK, never COMMIT."""
    control.check()
    if connection.autocommit is not True or int(connection.info.transaction_status) != 0:
        raise ValueError("intent commands require an exclusively leased idle autocommit connection")
    committing = unknown = discard = False
    failure: BaseException | None = None
    try:
        async with connection.cursor(row_factory=_tuple_row) as cursor:
            await _query(cursor, "BEGIN ISOLATION LEVEL READ COMMITTED", (), control, fetch="none")
            result, write = await operation(cursor)
            control.check()
        control.check()
        if write:
            committing = True
            await connection.commit()
        else:
            await connection.rollback()
    except BaseException as error:
        if committing:
            unknown = True
        elif isinstance(error, (EventAcknowledgmentIntentError, EventAcknowledgmentWorkerCancelled)):
            failure = error
        elif isinstance(error, asyncio.CancelledError):
            failure = EventAcknowledgmentWorkerCancelled(control.result)
        else:
            failure = _store_error()
    if unknown or failure is not None:
        try:
            await connection.rollback()
        except BaseException:
            discard = True
        if (
            unknown
            or discard
            or isinstance(failure, EventAcknowledgmentIntentError)
            and failure.code == "event_acknowledgment_intent_store"
        ):
            try:
                await connection.close()
            except BaseException:
                pass
    if unknown:
        raise EventConsumerCommitUnknownError(
            "acknowledgment intent command commit outcome unknown; retry the original command"
        )
    if failure is not None:
        raise failure
    # No cancellation checkpoint after confirmed COMMIT/ROLLBACK: preserve evidence.
    return result
