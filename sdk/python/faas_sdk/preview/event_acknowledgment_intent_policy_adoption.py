"""Guarded, application-owned adoption of retry policy for retained callback backlog."""

from __future__ import annotations

import asyncio
from importlib.resources import files
from typing import Any

from ._event_acknowledgment_intent_command_transaction import _command_transaction
from ._event_acknowledgment_intent_policy_adoption_state import (
    EventAcknowledgmentIntentAdoptRetryPolicyOptions,
    EventAcknowledgmentIntentPolicyAdoptionError,
    EventAcknowledgmentIntentPolicyAdoptionReceipt,
    _adoption_status,
    _AdoptionRequest,
    _receipt,
    _request,
    _snapshot,
    _validate_status,
)
from ._event_acknowledgment_protocol import Control, owned_operation
from .event_acknowledgment_intents import (
    _COLUMNS,
    _KEY,
    _TABLE,
    _query,
    _store_error,
)
from .event_consumer_transactions import _uuid

event_acknowledgment_intent_policy_adoption_schema = (
    files(__package__).joinpath("event_acknowledgment_intent_policy_adoption_schema.sql").read_text(encoding="utf-8")
)
_RECEIPT_COLUMNS = (
    "command_id::text,invocation_id::text,replay_generation,attempt,outcome,recorded_at,intent_status,request_digest"
)
_RECEIPT_TABLE = "public.gregale_event_ack_intent_policy_adoptions"


async def _apply(
    cursor: Any, account: str, app: str, request: _AdoptionRequest, control: Control
) -> EventAcknowledgmentIntentPolicyAdoptionReceipt:
    key = (account, app, request.invocation_id, request.generation, request.attempt)
    await _query(
        cursor,
        "SELECT pg_advisory_xact_lock(hashtextextended(%s,0))",
        ("gregale.event-ack-intent.v1:" + ":".join(key[:3]),),
        control,
        fetch="none",
    )
    existing = await _query(
        cursor,
        f"SELECT {_COLUMNS} FROM {_TABLE} WHERE {_KEY} AND replay_generation=%s::bigint AND attempt=%s FOR UPDATE",
        key,
        control,
    )
    status, outcome = None, "not_found"
    if existing is not None:
        _adoption_status(existing)
        row = await _query(
            cursor,
            f"UPDATE {_TABLE} SET retry_policy_version=1,retry_initial_backoff_ms=%s::bigint,retry_max_backoff_ms=%s::bigint,updated_at=greatest(clock_timestamp(),updated_at+interval '1 microsecond') WHERE {_KEY} AND replay_generation=%s::bigint AND attempt=%s AND updated_at=%s::timestamptz AND delivery_attempts=%s::bigint AND state IN ('pending','unconfirmed') AND retry_policy_version IS NULL AND retry_initial_backoff_ms IS NULL AND retry_max_backoff_ms IS NULL AND retry_max_delivery_attempts IS NULL AND delivery_attempt_limit IS NULL AND (claim_expires_at IS NULL OR claim_expires_at<=clock_timestamp()) RETURNING {_COLUMNS}",
            (request.initial, request.maximum, *key, request.expected_updated_at, request.expected_delivery_attempts),
            control,
        )
        outcome = "conflict" if row is None else "adopted"
        if row is not None:
            status = _adoption_status(row)
            _validate_status(status, request)
    body = None if status is None else _snapshot(status)
    if body is not None and len(body.encode("utf-8")) > 8192:
        raise _store_error()
    row = await _query(
        cursor,
        f"INSERT INTO {_RECEIPT_TABLE}(account_id,app_id,command_id,invocation_id,replay_generation,attempt,request_digest,outcome,intent_status) VALUES(%s::uuid,%s::uuid,%s::uuid,%s::uuid,%s::bigint,%s,%s,%s,%s) RETURNING {_RECEIPT_COLUMNS}",
        (
            account,
            app,
            request.command_id,
            request.invocation_id,
            request.generation,
            request.attempt,
            request.digest,
            outcome,
            body,
        ),
        control,
    )
    return _receipt(row, request, replay=False)


async def aadopt_event_acknowledgment_intent_retry_policy(
    connection: Any,
    account_id: str,
    app_id: str,
    invocation_id: str,
    replay_generation: int,
    attempt: int,
    options: EventAcknowledgmentIntentAdoptRetryPolicyOptions,
    *,
    stop_event: asyncio.Event | None = None,
) -> EventAcknowledgmentIntentPolicyAdoptionReceipt:
    """Bind an unresolved unbound intent once, preserving callback/state/count/due time.

    Exact original commands resolve uncertain commits via retained receipts, even
    after settlement, supersession or pruning. Guard rejections raise a typed error
    with their confirmed receipt; changed requests under a retained ID have none.
    Requires an exclusively leased idle autocommit async psycopg connection. No
    HTTP, business callback, recovery claim or platform renewal occurs.
    """
    account, app = _uuid(account_id), _uuid(app_id)
    request = _request(invocation_id, replay_generation, attempt, options)

    async def adopt(control: Control) -> EventAcknowledgmentIntentPolicyAdoptionReceipt:
        async def command(cursor: Any) -> tuple[EventAcknowledgmentIntentPolicyAdoptionReceipt, bool]:
            await _query(
                cursor,
                "SELECT pg_advisory_xact_lock(hashtextextended(%s,0))",
                ("gregale.event-ack-intent-policy-adoption.v1:" + account + ":" + app + ":" + request.command_id,),
                control,
                fetch="none",
            )
            saved = await _query(
                cursor,
                f"SELECT {_RECEIPT_COLUMNS} FROM {_RECEIPT_TABLE} WHERE account_id=%s::uuid AND app_id=%s::uuid AND command_id=%s::uuid",
                (account, app, request.command_id),
                control,
            )
            if saved is not None:
                return _receipt(saved, request, replay=True), False
            return await _apply(cursor, account, app, request, control), True

        receipt = await _command_transaction(connection, command, control)
        if receipt.outcome != "adopted":
            raise EventAcknowledgmentIntentPolicyAdoptionError(
                "event_acknowledgment_intent_conflict"
                if receipt.outcome == "conflict"
                else "event_acknowledgment_intent_not_found",
                receipt,
            )
        return receipt

    return await owned_operation(adopt, stop_event)
