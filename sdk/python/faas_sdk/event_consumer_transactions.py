"""Application PostgreSQL receipts for authenticated subscription consumers."""

from __future__ import annotations

import asyncio
import hashlib
import json
import re
from collections.abc import Awaitable, Callable, Mapping, Sequence
from dataclasses import dataclass, field
from decimal import Decimal
from importlib.resources import files
from typing import Any

from ._event_consumer_contract import (
    EVENT_CONSUMER_IDENTITY_MAX_BYTES,
    EVENT_CONSUMER_REQUEST_MAX_BYTES,
    EVENT_CONSUMER_RESULT_MAX_BYTES,
)

event_consumer_receipt_schema = files(__package__).joinpath("event_consumer_schema.sql").read_text(encoding="utf-8")
_SUPPORTED = object()
_LOCK = "SELECT pg_advisory_xact_lock(hashtextextended(%s, 0))"
_READ = "SELECT account_id::text,app_id::text,subscription_id::text,acceptance_id::text,event_source,event_id,request_digest,response_body FROM public.gregale_event_consumer_inbox WHERE identity_digest=%s"
_INSERT = "INSERT INTO public.gregale_event_consumer_inbox(identity_digest,account_id,app_id,subscription_id,acceptance_id,event_source,event_id,request_digest,response_body) VALUES (%s,%s::uuid,%s::uuid,%s::uuid,%s::bigint,%s,%s,%s,%s)"


class EventConsumerConflictError(ValueError):
    """The retained receipt has different scope or content."""


class EventConsumerCommitUnknownError(RuntimeError):
    """Retry the same acceptance and consumer; COMMIT may have succeeded."""


@dataclass(frozen=True)
class EventConsumerRequest:
    account_id: str
    app_id: str
    subscription_id: str
    acceptance_id: int
    source: str
    event_id: str
    body: bytes = field(repr=False)
    _support: object = field(default=None, repr=False, compare=False)


@dataclass(frozen=True)
class EventConsumerTransactionResult:
    """Acknowledge with body unchanged as application/json after success."""

    body: bytes
    replayed: bool


@dataclass(frozen=True)
class _EventConsumerTransactionHooks:
    storage_error: Callable[[], Exception]
    before_handler: Callable[[Any], Awaitable[None]] | None = None
    after_receipt: Callable[[Any], Awaitable[None]] | None = None


def _uuid(value: str) -> str:
    if (
        not isinstance(value, str)
        or not re.fullmatch(r"[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}", value)
        or value == "00000000-0000-0000-0000-000000000000"
    ):
        raise ValueError("event consumer scope must be nonzero UUIDs")
    return value.lower()


def _invalid_constant(value: str) -> Any:
    raise ValueError(f"invalid JSON constant: {value}")


def _json(body: bytes) -> Any:
    return json.loads(body.decode("utf-8"), parse_int=Decimal, parse_float=Decimal, parse_constant=_invalid_constant)


def new_event_consumer_request(
    account_id: str, app_id: str, subscription_id: str, acceptance_id: int, source: str, event_id: str, body: bytes
) -> EventConsumerRequest:
    """Supply authenticated identity and the exact original CloudEvents bytes."""
    if type(acceptance_id) is not int or not 0 < acceptance_id <= 9223372036854775807:
        raise ValueError("event acceptance must be a positive int64")
    if not isinstance(body, bytes) or not 0 < len(body) <= EVENT_CONSUMER_REQUEST_MAX_BYTES:
        raise ValueError("invalid or oversized event body")
    for value in (source, event_id):
        if not isinstance(value, str) or not value or len(value.encode("utf-8")) > EVENT_CONSUMER_IDENTITY_MAX_BYTES:
            raise ValueError("invalid event identity")
        if any(char in value for char in "\r\n\0"):
            raise ValueError("invalid event identity")
    envelope = _json(body)
    if (
        not isinstance(envelope, dict)
        or envelope.get("specversion") != "1.0"
        or envelope.get("source") != source
        or envelope.get("id") != event_id
        or not isinstance(envelope.get("type"), str)
        or not envelope["type"]
        or "data" not in envelope
    ):
        raise ValueError("event envelope identity differs")
    return EventConsumerRequest(
        _uuid(account_id),
        _uuid(app_id),
        _uuid(subscription_id),
        acceptance_id,
        source,
        event_id,
        bytes(body),
        _SUPPORTED,
    )


def event_consumer_request_from_headers(
    headers: Mapping[str, str | Sequence[str]], body: bytes, account_id: str, app_id: str
) -> EventConsumerRequest:
    """Headers are not authentication. Authenticate delivery; supply owner scope."""

    def read(name: str) -> str:
        values: list[str] = []
        for key, value in headers.items():
            if key.lower() == name:
                if isinstance(value, str):
                    values.append(value)
                else:
                    values.extend(value)
        if len(values) != 1 or not isinstance(values[0], str) or not values[0]:
            raise ValueError("missing or duplicate event context")
        return values[0]

    acceptance = read("x-gregale-event-acceptance-id")
    if not re.fullmatch(r"[1-9][0-9]{0,18}", acceptance):
        raise ValueError("event acceptance must be a canonical positive int64")
    return new_event_consumer_request(
        account_id,
        app_id,
        read("x-gregale-event-subscription-id"),
        int(acceptance),
        read("x-gregale-event-source"),
        read("x-gregale-event-id"),
        body,
    )


def _prepare(connection: Any, input: EventConsumerRequest, handler: Any) -> tuple[EventConsumerRequest, bytes, bytes]:
    if not isinstance(input, EventConsumerRequest) or input._support is not _SUPPORTED or not callable(handler):
        raise ValueError("use an event consumer request constructor")
    request = new_event_consumer_request(
        input.account_id,
        input.app_id,
        input.subscription_id,
        input.acceptance_id,
        input.source,
        input.event_id,
        input.body,
    )
    if connection.autocommit is not True or int(connection.info.transaction_status) != 0:
        raise ValueError("event consumer requires an exclusively leased idle autocommit connection")
    identity, digest = _digests(request)
    return request, identity, digest


def _digests(request: EventConsumerRequest) -> tuple[bytes, bytes]:
    frame = "\n".join(
        (
            "gregale.event-consumer.v1",
            request.account_id,
            request.app_id,
            request.subscription_id,
            str(request.acceptance_id),
        )
    )
    identity = hashlib.sha256(frame.encode("utf-8")).digest()
    digest = hashlib.sha256(b"gregale.event-consumer-input.v1\n" + request.body).digest()
    return identity, digest


def _result(value: bytes | str) -> bytes:
    if isinstance(value, str):
        value = value.encode("utf-8")
    if not isinstance(value, bytes) or not 0 < len(value) <= EVENT_CONSUMER_RESULT_MAX_BYTES or b"\0" in value:
        raise ValueError("invalid event consumer result")
    _json(value)
    return value


def _replay(row: Any, request: EventConsumerRequest, digest: bytes) -> bytes:
    if (
        tuple(row[:6])
        != (
            request.account_id,
            request.app_id,
            request.subscription_id,
            str(request.acceptance_id),
            request.source,
            request.event_id,
        )
        or bytes(row[6]) != digest
    ):
        raise EventConsumerConflictError("event consumer receipt scope or content differs")
    return _result(row[7])


def _params(request: EventConsumerRequest, identity: bytes, digest: bytes, body: bytes) -> tuple[Any, ...]:
    return (
        identity,
        request.account_id,
        request.app_id,
        request.subscription_id,
        request.acceptance_id,
        request.source,
        request.event_id,
        digest,
        body.decode("utf-8"),
    )


def _tuple_row(_cursor: Any) -> Callable[[Sequence[Any]], tuple[Any, ...]]:
    return tuple


def with_event_consumer_transaction(
    connection: Any, input: EventConsumerRequest, handler: Callable[[Any], bytes | str]
) -> EventConsumerTransactionResult:
    """Own a fresh psycopg transaction; callback uses only its supplied cursor.

    Do not control transaction lifecycle or perform external effects. Return JSON
    bytes/text. Retain receipts across retries/replay; acknowledge after success.
    """
    request, identity, digest = _prepare(connection, input, handler)
    completed = False
    try:
        with connection.transaction():
            with connection.cursor(row_factory=_tuple_row) as cursor:
                cursor.execute("SET TRANSACTION ISOLATION LEVEL READ COMMITTED")
                cursor.execute(_LOCK, ("gregale.event-consumer-inbox.v1:" + identity.hex(),))
                cursor.execute(_READ, (identity,))
                row = cursor.fetchone()
                if row is not None:
                    body = _replay(row, request, digest)
                else:
                    with connection.cursor() as business_cursor:
                        body = _result(handler(business_cursor))
                    cursor.execute(_INSERT, _params(request, identity, digest, body))
                completed = True
    except BaseException as error:
        if completed:
            raise EventConsumerCommitUnknownError(
                "event consumer commit outcome unknown; retry the same identity"
            ) from error
        raise
    return EventConsumerTransactionResult(body, row is not None)


async def awith_event_consumer_transaction(
    connection: Any, input: EventConsumerRequest, handler: Callable[[Any], Awaitable[bytes | str]]
) -> EventConsumerTransactionResult:
    """Async psycopg equivalent. Exclusively lease an idle autocommit connection."""
    return await _awith_event_consumer_transaction(connection, input, handler)


async def _awith_event_consumer_transaction(
    connection: Any,
    input: EventConsumerRequest,
    handler: Callable[[Any], Awaitable[bytes | str]],
    check: Callable[[], None] = lambda: None,
    before_commit: Callable[[Any, EventConsumerTransactionResult], Awaitable[None]] | None = None,
    hooks: _EventConsumerTransactionHooks | None = None,
) -> EventConsumerTransactionResult:
    """Internal cooperative checkpoints; the worker shields active SQL from task cancellation."""
    application_failure: BaseException | None = None

    def checkpoint() -> None:
        nonlocal application_failure
        try:
            check()
        except BaseException as error:
            application_failure = error
            raise

    async def invoke(operation: Callable[..., Awaitable[Any]], *args: Any) -> Any:
        nonlocal application_failure
        try:
            return await operation(*args)
        except BaseException as error:
            application_failure = error
            raise

    checkpoint()
    request, identity, digest = _prepare(connection, input, handler)
    completed = False
    failure: BaseException | None = None
    unknown = discard = False
    try:
        async with connection.transaction():
            checkpoint()
            async with connection.cursor(row_factory=_tuple_row) as cursor:
                checkpoint()
                await cursor.execute("SET TRANSACTION ISOLATION LEVEL READ COMMITTED")
                checkpoint()
                await cursor.execute(_LOCK, ("gregale.event-consumer-inbox.v1:" + identity.hex(),))
                checkpoint()
                await cursor.execute(_READ, (identity,))
                checkpoint()
                row = await cursor.fetchone()
                checkpoint()
                if row is not None:
                    body = _replay(row, request, digest)
                else:
                    if hooks is not None and hooks.before_handler is not None:
                        await invoke(hooks.before_handler, cursor)
                        checkpoint()
                    async with connection.cursor() as business_cursor:
                        checkpoint()

                        # Invalid callback JSON belongs to the application too.
                        async def apply() -> bytes:
                            return _result(await handler(business_cursor))

                        body = await invoke(apply)
                    checkpoint()
                    await cursor.execute(_INSERT, _params(request, identity, digest, body))
                    if hooks is not None and hooks.after_receipt is not None:
                        checkpoint()
                        await invoke(hooks.after_receipt, cursor)
                checkpoint()
                if before_commit is not None:
                    await invoke(before_commit, cursor, EventConsumerTransactionResult(body, row is not None))
                    checkpoint()
            checkpoint()
            completed = True
    except BaseException as error:
        if hooks is None:
            if completed:
                raise EventConsumerCommitUnknownError(
                    "event consumer commit outcome unknown; retry the same identity"
                ) from error
            raise
        if completed:
            unknown = discard = True
        elif error is application_failure or isinstance(error, (EventConsumerConflictError, asyncio.CancelledError)):
            failure = error
        else:
            discard = True
            failure = hooks.storage_error()
    if discard:
        try:
            await connection.close()
        except BaseException:
            pass
    # Raise outside the driver exception context: capabilities/SQL rows are private.
    if unknown:
        raise EventConsumerCommitUnknownError("event consumer commit outcome unknown; reconcile on a fresh connection")
    if failure is not None:
        raise failure
    return EventConsumerTransactionResult(body, row is not None)
