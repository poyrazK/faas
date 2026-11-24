"""Guarded budget adoption for active, application-owned acknowledgment backlog."""

from __future__ import annotations

import asyncio
from importlib.resources import files
from typing import Any

from ._event_acknowledgment_intent_budget_adoption_state import (
    EventAcknowledgmentIntentAdoptRetryBudgetOptions,
    EventAcknowledgmentIntentBudgetAdoptionError,
    EventAcknowledgmentIntentBudgetAdoptionReceipt,
    _BudgetAdoptionRequest,
    _receipt,
    _request,
    _selection,
    _snapshot,
    _validate_status,
)
from ._event_acknowledgment_intent_command_transaction import _command_transaction
from ._event_acknowledgment_intent_policy_adoption_state import _adoption_status
from ._event_acknowledgment_protocol import Control, owned_operation
from .event_acknowledgment_intents import _COLUMNS, _KEY, _TABLE, EventAcknowledgmentIntentStatus, _query, _store_error
from .event_consumer_transactions import _uuid

event_acknowledgment_intent_budget_adoption_schema = (
    files(__package__).joinpath("event_acknowledgment_intent_budget_adoption_schema.sql").read_text(encoding="utf-8")
)
_RECEIPT_COLUMNS = (
    "command_id::text,invocation_id::text,replay_generation,attempt,outcome,recorded_at,intent_status,request_digest"
)
_RECEIPT_TABLE = "public.gregale_event_ack_intent_budget_adoptions"
# Every target reference is qualified because the newer-generation subquery shares names.
_BUDGET_COLUMNS = "target.invocation_id::text,target.replay_generation,target.attempt,target.action,target.reason,target.state,target.delivery_attempts,target.next_attempt_at,target.claim_expires_at,target.last_code,target.created_at,target.updated_at,target.retry_policy_version,target.retry_initial_backoff_ms,target.retry_max_backoff_ms,target.retry_max_delivery_attempts,target.delivery_attempt_limit"


async def aget_event_acknowledgment_intent_budget_adoption_candidate(
    connection: Any,
    account_id: str,
    app_id: str,
    invocation_id: str,
    replay_generation: int,
    attempt: int,
    *,
    stop_event: asyncio.Event | None = None,
) -> EventAcknowledgmentIntentStatus:
    """Read exact microsecond fences without a claim, row lock, callback or command write."""
    account, app = _uuid(account_id), _uuid(app_id)
    selection = _selection(invocation_id, replay_generation, attempt)

    async def read(control: Control) -> EventAcknowledgmentIntentStatus:
        async def candidate(cursor: Any) -> tuple[EventAcknowledgmentIntentStatus, bool]:
            row = await _query(
                cursor,
                f"SELECT {_COLUMNS} FROM {_TABLE} WHERE {_KEY} AND replay_generation=%s::bigint AND attempt=%s",
                (account, app, *selection),
                control,
            )
            if row is None:
                raise EventAcknowledgmentIntentBudgetAdoptionError("event_acknowledgment_intent_not_found")
            return _adoption_status(row), False

        return await _command_transaction(connection, candidate, control)

    return await owned_operation(read, stop_event)


async def _apply(
    cursor: Any, account: str, app: str, request: _BudgetAdoptionRequest, control: Control
) -> EventAcknowledgmentIntentBudgetAdoptionReceipt:
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
            f"UPDATE {_TABLE} AS target SET retry_policy_version=2,retry_initial_backoff_ms=%s::bigint,retry_max_backoff_ms=%s::bigint,retry_max_delivery_attempts=%s::bigint,delivery_attempt_limit=%s::bigint,updated_at=greatest(clock_timestamp(),target.updated_at+interval '1 microsecond') WHERE target.account_id=%s::uuid AND target.app_id=%s::uuid AND target.invocation_id=%s::uuid AND target.replay_generation=%s::bigint AND target.attempt=%s AND target.updated_at=%s::timestamptz AND target.delivery_attempts=%s::bigint AND target.state IN ('pending','unconfirmed') AND target.retry_max_delivery_attempts IS NULL AND target.delivery_attempt_limit IS NULL AND %s::bigint>target.delivery_attempts AND (target.claim_expires_at IS NULL OR target.claim_expires_at<=clock_timestamp()) AND ((%s::integer=0 AND target.retry_policy_version IS NULL AND target.retry_initial_backoff_ms IS NULL AND target.retry_max_backoff_ms IS NULL) OR (%s::integer=1 AND target.retry_policy_version=1 AND target.retry_initial_backoff_ms=%s::bigint AND target.retry_max_backoff_ms=%s::bigint AND %s::bigint=target.retry_initial_backoff_ms AND %s::bigint=target.retry_max_backoff_ms)) AND NOT EXISTS(SELECT 1 FROM {_TABLE} newer WHERE newer.account_id=target.account_id AND newer.app_id=target.app_id AND newer.invocation_id=target.invocation_id AND (newer.replay_generation,newer.attempt)>(target.replay_generation,target.attempt)) RETURNING {_BUDGET_COLUMNS}",
            (
                request.initial,
                request.maximum,
                request.total,
                request.total,
                *key,
                request.expected_updated_at,
                request.expected_delivery_attempts,
                request.total,
                request.expected_policy_version,
                request.expected_policy_version,
                request.expected_initial,
                request.expected_maximum,
                request.initial,
                request.maximum,
            ),
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


async def aadopt_event_acknowledgment_intent_retry_budget(
    connection: Any,
    account_id: str,
    app_id: str,
    invocation_id: str,
    replay_generation: int,
    attempt: int,
    options: EventAcknowledgmentIntentAdoptRetryBudgetOptions,
    *,
    stop_event: asyncio.Event | None = None,
) -> EventAcknowledgmentIntentBudgetAdoptionReceipt:
    """Attach v2 to one active unbound/v1 intent, retaining the outcome atomically.

    Guards require exact timestamp/count/policy, no live claim, no newer attempt,
    unchanged v1 timing and a total above consumed claims. Preserve callback/state/
    count/due time and expired claim metadata. Persist the original command first.
    Exact retries read historical evidence before current rows without COMMIT.
    Unknown COMMIT returns no receipt: retry unchanged on a fresh connection.
    Requires an exclusively leased idle autocommit async psycopg connection. No
    HTTP, business work, renewal, recovery capability or scheduling occurs.
    """
    account, app = _uuid(account_id), _uuid(app_id)
    request = _request(invocation_id, replay_generation, attempt, options)

    async def adopt(control: Control) -> EventAcknowledgmentIntentBudgetAdoptionReceipt:
        async def command(cursor: Any) -> tuple[EventAcknowledgmentIntentBudgetAdoptionReceipt, bool]:
            await _query(
                cursor,
                "SELECT pg_advisory_xact_lock(hashtextextended(%s,0))",
                ("gregale.event-ack-intent-budget-adoption.v1:" + account + ":" + app + ":" + request.command_id,),
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
            raise EventAcknowledgmentIntentBudgetAdoptionError(
                "event_acknowledgment_intent_conflict"
                if receipt.outcome == "conflict"
                else "event_acknowledgment_intent_not_found",
                receipt,
            )
        return receipt

    return await owned_operation(adopt, stop_event)
