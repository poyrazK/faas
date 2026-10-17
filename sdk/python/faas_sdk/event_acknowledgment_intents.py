"""Application-owned acknowledgment intents shared with Go and Node. Never log SQL diagnostics."""

from __future__ import annotations

import asyncio
import hashlib
from collections.abc import Awaitable, Callable
from dataclasses import dataclass, field
from datetime import UTC, datetime
from importlib.resources import files
from typing import Any, TypeVar

from ._event_acknowledgment_protocol import (
    Control,
    DeliveryFence,
    EventAcknowledgmentWorkerCancelled,
    EventAcknowledgmentWorkerError,
    EventAcknowledgmentWorkerResult,
    owned_operation,
    reason_code,
)
from .event_consumer_transactions import (
    EventConsumerCommitUnknownError,
    EventConsumerConflictError,
    EventConsumerRequest,
    EventConsumerTransactionResult,
    _digests,
    _prepare,
    _tuple_row,
    _uuid,
)

event_acknowledgment_intent_schema = (
    files(__package__).joinpath("event_acknowledgment_intent_schema.sql").read_text(encoding="utf-8")
)

_MESSAGES = {
    "event_acknowledgment_intent_conflict": "immutable acknowledgment intent conflicts",
    "event_acknowledgment_intent_superseded": "acknowledgment intent has a newer claim",
    "event_acknowledgment_intent_store": "acknowledgment intent storage operation failed",
    "event_acknowledgment_intent_empty": "no acknowledgment intent is due",
    "event_acknowledgment_intent_claim_lost": "acknowledgment intent recovery lease expired or replaced",
}


class EventAcknowledgmentIntentError(RuntimeError):
    """Fixed code/message; no driver diagnostics, SQL parameters or callback capability."""

    def __init__(self, code: str):
        if code not in _MESSAGES:
            raise ValueError("invalid acknowledgment intent error code")
        super().__init__(_MESSAGES[code])
        self.code = code


class _CallbackOptionsError(ValueError):
    """Safe input error allowed through the sanitized storage boundary."""

    def __init__(self):
        super().__init__("invalid acknowledgment worker callback budget")


@dataclass(frozen=True)
class EventAcknowledgmentIntentStatus:
    """Retained metadata only; native integers preserve the full int64 generation."""

    invocation_id: str
    replay_generation: int
    attempt: int
    action: str
    reason: str
    state: str
    delivery_attempts: int
    next_attempt_at: str
    claim_expires_at: str | None
    last_code: str
    created_at: str
    updated_at: str


@dataclass(frozen=True)
class EventAcknowledgmentIntentResult:
    """Confirmed local receipt/intent commit. Platform acknowledgment is still independent."""

    receipt: EventConsumerTransactionResult = field(repr=False)
    intent: EventAcknowledgmentIntentStatus
    receipt_confirmed: bool = field(default=True, init=False)


_COLUMNS = "invocation_id::text,replay_generation,attempt,action,reason,state,delivery_attempts,next_attempt_at,claim_expires_at,last_code,created_at,updated_at"
_TABLE = "public.gregale_event_acknowledgment_intents"
_KEY = "account_id=%s::uuid AND app_id=%s::uuid AND invocation_id=%s::uuid"
_T = TypeVar("_T")


def _store_error() -> EventAcknowledgmentIntentError:
    return EventAcknowledgmentIntentError("event_acknowledgment_intent_store")


def _integer(value: Any, minimum: int, maximum: int) -> int:
    if type(value) is not int or not minimum <= value <= maximum:
        raise _store_error()
    return value


def _instant(value: Any) -> str:
    if not isinstance(value, datetime) or value.tzinfo is None or value.utcoffset() is None:
        raise _store_error()
    return value.astimezone(UTC).isoformat(timespec="microseconds").replace("+00:00", "Z")


def _status(row: Any) -> EventAcknowledgmentIntentStatus:
    if not isinstance(row, (tuple, list)) or len(row) < 12:
        raise _store_error()
    invocation, generation, attempt, action, reason, state, deliveries, due, expiry, code, created, updated = row[:12]
    if (
        not isinstance(invocation, str)
        or _uuid(invocation) != invocation
        or action not in ("complete", "retry", "dead-letter")
        or (reason != "" if action == "complete" else not reason_code(reason))
        or state not in ("pending", "unconfirmed", "confirmed", "superseded")
        or code not in ("", "unconfirmed", "lease_lost", "callback_confirmed", "claim_replaced", "receipt_committed")
    ):
        raise _store_error()
    return EventAcknowledgmentIntentStatus(
        invocation,
        _integer(generation, 0, 9223372036854775807),
        _integer(attempt, 1, 2147483647),
        action,
        reason,
        state,
        _integer(deliveries, 0, 2147483647),
        _instant(due),
        None if expiry is None else _instant(expiry),
        code,
        _instant(created),
        _instant(updated),
    )


def _hash(value: str) -> bytes:
    return hashlib.sha256(value.encode("utf-8")).digest()


def _bytes(value: Any) -> bytes:
    if not isinstance(value, (bytes, memoryview)) or len(value) != 32:
        raise _store_error()
    return bytes(value)


async def _query(cursor: Any, sql: str, params: tuple[Any, ...], control: Control, *, fetch: str = "one") -> Any:
    control.check()
    failed = False
    try:
        await cursor.execute(sql, params)
    except Exception:
        failed = True
    if failed:
        raise _store_error()
    control.check()
    if fetch == "none":
        return None
    try:
        result = await (cursor.fetchall() if fetch == "all" else cursor.fetchone())
    except Exception:
        failed = True
    if failed:
        raise _store_error()
    control.check()
    return result


async def _transaction(connection: Any, operation: Callable[[Any], Awaitable[_T]], control: Control) -> _T:
    """Shield at the public boundary. Never return a result after uncertain COMMIT."""
    control.check()
    if connection.autocommit is not True or int(connection.info.transaction_status) != 0:
        raise ValueError("acknowledgment intents require an exclusively leased idle autocommit connection")
    completed = unknown = False
    failure: BaseException | None = None
    try:
        async with connection.transaction():
            control.check()
            async with connection.cursor(row_factory=_tuple_row) as cursor:
                await _query(cursor, "SET TRANSACTION ISOLATION LEVEL READ COMMITTED", (), control, fetch="none")
                result = await operation(cursor)
                control.check()
            control.check()
            completed = True
    except BaseException as error:
        if completed:
            unknown = True
        elif isinstance(
            error,
            (
                EventAcknowledgmentIntentError,
                EventAcknowledgmentWorkerError,
                EventConsumerConflictError,
                _CallbackOptionsError,
            ),
        ):
            failure = error
        elif isinstance(error, asyncio.CancelledError):
            failure = EventAcknowledgmentWorkerCancelled(control.result)
        else:
            failure = _store_error()
    if unknown:
        try:
            await connection.close()
        except BaseException:
            pass
        raise EventConsumerCommitUnknownError(
            "acknowledgment intent commit outcome unknown; reconcile on a fresh connection"
        )
    if failure is not None:
        raise failure
    return result


async def _save_completion(
    cursor: Any,
    request: EventConsumerRequest,
    invocation: str,
    generation: int,
    attempt: int,
    slug: str,
    seconds: int,
    token: str,
    receipt: EventConsumerTransactionResult,
    control: Control,
) -> EventAcknowledgmentIntentStatus:
    return await _save_intent(
        cursor,
        request,
        invocation,
        generation,
        attempt,
        slug,
        seconds,
        token,
        "complete",
        "",
        receipt.body.decode("utf-8"),
        control,
    )


async def _save_intent(
    cursor: Any,
    request: EventConsumerRequest,
    invocation: str,
    generation: int,
    attempt: int,
    slug: str,
    seconds: int,
    token: str,
    action: str,
    reason: str,
    body: str | None,
    control: Control,
) -> EventAcknowledgmentIntentStatus:
    identity, digest = _digests(request)
    key = (request.account_id, request.app_id, invocation)
    capability, payload = _hash(token), _hash(action + "\n" + reason + "\n" + (body or ""))
    await _query(
        cursor,
        "SELECT pg_advisory_xact_lock(hashtextextended(%s,0))",
        ("gregale.event-ack-intent.v1:" + ":".join(key),),
        control,
        fetch="none",
    )
    old = await _query(
        cursor,
        f"SELECT replay_generation,attempt,identity_digest,request_digest,capability_digest,action_digest,app_slug,lease_seconds FROM {_TABLE} WHERE {_KEY} ORDER BY replay_generation DESC,attempt DESC LIMIT 1",
        key,
        control,
    )
    if old is not None:
        if _bytes(old[2]) != identity or _bytes(old[3]) != digest:
            raise EventAcknowledgmentIntentError("event_acknowledgment_intent_conflict")
        previous = (_integer(old[0], 0, 9223372036854775807), _integer(old[1], 1, 2147483647))
        if previous > (generation, attempt):
            raise EventAcknowledgmentIntentError("event_acknowledgment_intent_superseded")
        if previous == (generation, attempt):
            if _bytes(old[4]) != capability or _bytes(old[5]) != payload or old[6] != slug or old[7] != seconds:
                raise EventAcknowledgmentIntentError("event_acknowledgment_intent_conflict")
            status = _status(
                await _query(
                    cursor,
                    f"SELECT {_COLUMNS} FROM {_TABLE} WHERE {_KEY} AND replay_generation=%s::bigint AND attempt=%s",
                    (*key, generation, attempt),
                    control,
                )
            )
            if status.state == "superseded":
                raise EventAcknowledgmentIntentError("event_acknowledgment_intent_superseded")
            return status
    await _query(
        cursor,
        f"UPDATE {_TABLE} SET state='superseded',token=NULL,result_body=NULL,claim_token=NULL,claim_expires_at=NULL,last_code='claim_replaced',updated_at=clock_timestamp() WHERE {_KEY} AND state IN ('pending','unconfirmed')",
        key,
        control,
        fetch="none",
    )
    return _status(
        await _query(
            cursor,
            f"INSERT INTO {_TABLE}(account_id,app_id,invocation_id,replay_generation,attempt,identity_digest,request_digest,capability_digest,action_digest,app_slug,lease_seconds,action,reason,token,result_body,state) VALUES(%s::uuid,%s::uuid,%s::uuid,%s::bigint,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,'pending') RETURNING {_COLUMNS}",
            (
                *key,
                generation,
                attempt,
                identity,
                digest,
                capability,
                payload,
                slug,
                seconds,
                action,
                reason,
                token,
                body,
            ),
            control,
        )
    )


async def _save_negative(
    connection: Any,
    input: EventConsumerRequest,
    delivery: DeliveryFence,
    slug: str,
    seconds: int,
    token: str,
    action: str,
    reason: str,
    control: Control,
) -> EventAcknowledgmentIntentStatus:
    """Known rollback/stopped work/exclusive ownership are caller preconditions."""
    if action not in ("retry", "dead-letter") or not reason_code(reason):
        raise ValueError("invalid event acknowledgment failure decision")
    control.check()
    request, identity, digest = _prepare(connection, input, lambda _cursor: None)

    async def save(cursor: Any) -> EventAcknowledgmentIntentStatus:
        await _query(
            cursor,
            "SELECT pg_advisory_xact_lock(hashtextextended(%s,0))",
            ("gregale.event-consumer-inbox.v1:" + identity.hex(),),
            control,
            fetch="none",
        )
        row = await _query(
            cursor,
            "SELECT account_id::text,app_id::text,subscription_id::text,acceptance_id::text,event_source,event_id,request_digest FROM public.gregale_event_consumer_inbox WHERE identity_digest=%s",
            (identity,),
            control,
        )
        if row is not None:
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
                or _bytes(row[6]) != digest
            ):
                raise EventConsumerConflictError("event consumer receipt scope or content differs")
            control.result = EventAcknowledgmentWorkerResult(receipt_confirmed=True)
            raise EventAcknowledgmentWorkerError("event_consumer_receipt_committed", control.result)
        return await _save_intent(
            cursor,
            request,
            delivery.invocation_id,
            delivery.replay_generation,
            delivery.attempt,
            slug,
            seconds,
            token,
            action,
            reason,
            None,
            control,
        )

    return await _transaction(connection, save, control)


async def alist_event_acknowledgment_intents(
    connection: Any,
    account_id: str,
    app_id: str,
    *,
    invocation_id: str | None = None,
    limit: int = 25,
    stop_event: asyncio.Event | None = None,
) -> tuple[EventAcknowledgmentIntentStatus, ...]:
    """One bounded metadata query. Does not claim work or send callbacks."""
    account, app = _uuid(account_id), _uuid(app_id)
    invocation = "" if invocation_id is None else _uuid(invocation_id)
    if type(limit) is not int or not 1 <= limit <= 100:
        raise ValueError("invalid acknowledgment intent diagnostic limit")

    async def read(control: Control) -> tuple[EventAcknowledgmentIntentStatus, ...]:
        async def rows(cursor: Any) -> tuple[EventAcknowledgmentIntentStatus, ...]:
            return tuple(
                _status(row)
                for row in await _query(
                    cursor,
                    f"SELECT {_COLUMNS} FROM {_TABLE} WHERE account_id=%s::uuid AND app_id=%s::uuid AND (%s='' OR invocation_id::text=%s) ORDER BY updated_at DESC,invocation_id,replay_generation DESC,attempt DESC LIMIT %s",
                    (account, app, invocation, invocation, limit),
                    control,
                    fetch="all",
                )
            )

        return await _transaction(connection, rows, control)

    return await owned_operation(read, stop_event)
