"""Application-owned processing leases for side-effect-free preparation."""

from __future__ import annotations

import asyncio
import secrets
from collections.abc import AsyncIterator, Awaitable, Callable
from contextlib import asynccontextmanager
from importlib.resources import files
from typing import Any

from ._event_acknowledgment_protocol import (
    Control,
    EventAcknowledgmentWorkerCancellation,
    EventAcknowledgmentWorkerResult,
    owned_operation,
)
from ._event_consumer_processing_store import (
    EventConsumerProcessingError,
    _affected,
    _bytes,
    _duration,
    _query,
    _store_error,
    _transaction,
)
from .event_consumer_transactions import (
    _LOCK,
    EventConsumerConflictError,
    EventConsumerRequest,
    EventConsumerTransactionResult,
    _awith_event_consumer_transaction,
    _digests,
    _EventConsumerTransactionHooks,
    _prepare,
)

event_consumer_processing_schema = (
    files(__package__).joinpath("event_consumer_processing_schema.sql").read_text(encoding="utf-8")
)
_SUPPORTED = object()
_RECEIPT = "SELECT account_id=%s::uuid AND app_id=%s::uuid AND subscription_id=%s::uuid AND acceptance_id=%s::bigint AND event_source=%s AND event_id=%s AND request_digest=%s FROM public.gregale_event_consumer_inbox WHERE identity_digest=%s"
_INSERT = "INSERT INTO public.gregale_event_consumer_processing(identity_digest,request_digest,claim_token,expires_at) VALUES (%s,%s,%s,clock_timestamp()+%s::bigint*interval '1 millisecond') ON CONFLICT (identity_digest) DO NOTHING"
_READ = "SELECT request_digest,claim_token,expires_at<=clock_timestamp() FROM public.gregale_event_consumer_processing WHERE identity_digest=%s FOR UPDATE"
_TAKE = "UPDATE public.gregale_event_consumer_processing SET claim_token=%s,expires_at=clock_timestamp()+%s::bigint*interval '1 millisecond' WHERE identity_digest=%s RETURNING identity_digest"
_RENEW = "UPDATE public.gregale_event_consumer_processing SET expires_at=clock_timestamp()+%s::bigint*interval '1 millisecond' WHERE identity_digest=%s AND request_digest=%s AND claim_token=%s AND expires_at>clock_timestamp() RETURNING identity_digest"
_FENCE = "SELECT request_digest=%s AND claim_token=%s AND expires_at>clock_timestamp() FROM public.gregale_event_consumer_processing WHERE identity_digest=%s FOR UPDATE"
_DELETE = "DELETE FROM public.gregale_event_consumer_processing WHERE identity_digest=%s AND request_digest=%s AND claim_token=%s AND expires_at>clock_timestamp() RETURNING identity_digest"


def _rows(rows: Any, width: int) -> Any:
    if not isinstance(rows, (tuple, list)) or len(rows) > 1:
        raise _store_error()
    if rows and (not isinstance(rows[0], (tuple, list)) or len(rows[0]) != width):
        raise _store_error()
    return rows[0] if rows else None


class EventConsumerProcessingLease:
    """Redacted process-local capability. Reclaim from original input after restart."""

    __slots__ = ("_input", "_identity", "_digest", "_token", "_completed", "_busy", "_loop")

    def __init__(
        self, key: object, request: EventConsumerRequest, identity: bytes, digest: bytes, token: bytes, completed: bool
    ):
        if key is not _SUPPORTED:
            raise TypeError("use aclaim_event_consumer_processing")
        self._input, self._identity, self._digest, self._token = request, identity, digest, token
        self._completed, self._busy, self._loop = completed, False, asyncio.get_running_loop()

    @property
    def completed(self) -> bool:
        """Claim observed a matching receipt; not updated later and not platform acknowledgment proof."""
        return self._completed

    def __repr__(self) -> str:
        return "EventConsumerProcessingLease{redacted}"

    __str__ = __repr__

    def __reduce_ex__(self, _protocol: int) -> Any:
        raise TypeError("processing leases cannot be persisted")

    def __copy__(self) -> Any:
        raise TypeError("processing leases cannot be copied")

    def __deepcopy__(self, _memo: Any) -> Any:
        raise TypeError("processing leases cannot be copied")

    @asynccontextmanager
    async def _use(
        self, input: EventConsumerRequest, control: Control
    ) -> AsyncIterator[_EventConsumerTransactionHooks]:
        if self._loop is not asyncio.get_running_loop():
            raise ValueError("processing lease belongs to another event loop")
        identity, digest = _digests(input)
        if identity != self._identity or digest != self._digest:
            raise ValueError("consumer processing lease input differs")
        if self._busy:
            raise EventConsumerProcessingError("event_consumer_processing_operation_busy")
        self._busy = True

        async def before_handler(cursor: Any) -> None:
            row = _rows(await _query(cursor, _FENCE, (digest, self._token, identity), control), 1)
            if row is not None and type(row[0]) is not bool:
                raise _store_error()
            if row is None or not row[0]:
                raise EventConsumerProcessingError("event_consumer_processing_lease_lost")

        async def after_receipt(cursor: Any) -> None:
            _affected(await _query(cursor, _DELETE, (identity, digest, self._token), control), identity)

        try:
            control.check()
            yield _EventConsumerTransactionHooks(_store_error, before_handler, after_receipt)
        finally:
            self._busy = False

    async def renew(self, connection: Any, duration_ms: int, *, stop_event: asyncio.Event | None = None) -> None:
        """Extend only this unexpired claim. Stop preparation on any failure/uncertainty."""
        _duration(duration_ms)

        async def renew(control: Control) -> None:
            async with self._use(self._input, control):

                async def update(cursor: Any) -> None:
                    _affected(
                        await _query(cursor, _RENEW, (duration_ms, self._identity, self._digest, self._token), control),
                        self._identity,
                    )

                await _transaction(connection, update, control)

        await owned_operation(renew, stop_event)

    async def complete(
        self,
        connection: Any,
        handler: Callable[[Any, EventAcknowledgmentWorkerCancellation], Awaitable[bytes | str]],
        *,
        stop_event: asyncio.Event | None = None,
    ) -> EventConsumerTransactionResult:
        """Short SQL writes only. Receipt replay skips the handler and old local fence."""
        if not callable(handler):
            raise ValueError("invalid event consumer handler")

        async def complete(control: Control) -> EventConsumerTransactionResult:
            async with self._use(self._input, control) as hooks:
                receipt = await _awith_event_consumer_transaction(
                    connection,
                    self._input,
                    lambda cursor: handler(cursor, EventAcknowledgmentWorkerCancellation(control)),
                    control.check,
                    hooks=hooks,
                )
                control.result = EventAcknowledgmentWorkerResult(True, receipt)
                return receipt

        return await owned_operation(complete, stop_event)


async def aclaim_event_consumer_processing(
    connection: Any,
    input: EventConsumerRequest,
    duration_ms: int,
    *,
    stop_event: asyncio.Event | None = None,
) -> EventConsumerProcessingLease:
    """Own a fresh psycopg transaction; an uncertain COMMIT returns no capability."""
    _duration(duration_ms)
    request, identity, digest = _prepare(connection, input, lambda: None)
    token = secrets.token_bytes(32)

    async def claim(control: Control) -> EventConsumerProcessingLease:
        async def acquire(cursor: Any) -> bool:
            await _query(cursor, _LOCK, ("gregale.event-consumer-inbox.v1:" + identity.hex(),), control, fetch=False)
            receipt = _rows(
                await _query(
                    cursor,
                    _RECEIPT,
                    (
                        request.account_id,
                        request.app_id,
                        request.subscription_id,
                        request.acceptance_id,
                        request.source,
                        request.event_id,
                        digest,
                        identity,
                    ),
                    control,
                ),
                1,
            )
            if receipt is not None:
                if type(receipt[0]) is not bool:
                    raise _store_error()
                if not receipt[0]:
                    raise EventConsumerConflictError("event consumer receipt scope or content differs")
                return True
            await _query(cursor, _INSERT, (identity, digest, token, duration_ms), control, fetch=False)
            row = _rows(await _query(cursor, _READ, (identity,), control), 3)
            if row is None or type(row[2]) is not bool:
                raise _store_error()
            saved_digest, saved_token = _bytes(row[0]), _bytes(row[1])
            if saved_digest != digest:
                raise EventConsumerConflictError("event consumer receipt scope or content differs")
            if saved_token != token:
                if not row[2]:
                    raise EventConsumerProcessingError("event_consumer_processing_busy")
                _affected(await _query(cursor, _TAKE, (token, duration_ms, identity), control), identity)
            return False

        completed = await _transaction(connection, acquire, control)
        return EventConsumerProcessingLease(_SUPPORTED, request, identity, digest, token, completed)

    return await owned_operation(claim, stop_event)
