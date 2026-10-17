"""Read-only metadata for one original consumer request; never grants ownership."""

from __future__ import annotations

import asyncio
import re
from dataclasses import dataclass
from typing import Any, Literal

from ._event_acknowledgment_protocol import (
    Control,
    EventAcknowledgmentWorkerCancelled,
    EventAcknowledgmentWorkerError,
    owned_operation,
)
from ._event_consumer_processing_store import _query, _store_error
from ._event_deadline_policy import timestamp
from .event_consumer_transactions import EventConsumerRequest, _prepare, _tuple_row

_Receipt = Literal["absent", "matching", "conflict"]
_Claim = Literal["absent", "active", "expired", "conflict"]
_State = Literal["completed", "active", "expired", "absent", "conflict"]


@dataclass(frozen=True)
class EventConsumerProcessingInspection:
    """Snapshot evidence only. Matching receipt metadata does not validate saved JSON."""

    state: _State
    observed_at: str
    receipt: _Receipt
    claim: _Claim
    expires_at: str | None = None
    remaining_lease_ms: int | None = None


# Match Node's statement exactly, using positional psycopg bindings for repeated
# digests. Only equality evidence and bounded timestamps leave PostgreSQL.
_INSPECTION_QUERY = """WITH clock AS MATERIALIZED (SELECT clock_timestamp() AS observed_at),
 receipt AS MATERIALIZED (
  SELECT true AS present,account_id=%s::uuid AND app_id=%s::uuid AND subscription_id=%s::uuid
   AND acceptance_id=%s::bigint AND event_source=%s AND event_id=%s AND request_digest=%s AS matching
  FROM public.gregale_event_consumer_inbox WHERE identity_digest=%s
 ), claim AS MATERIALIZED (
  SELECT true AS present,request_digest=%s AS matching,expires_at
  FROM public.gregale_event_consumer_processing WHERE identity_digest=%s
 ) SELECT
 CASE WHEN c.observed_at BETWEEN '0001-01-01T00:00:00Z'::timestamptz AND '9999-12-31T23:59:59.999999Z'::timestamptz
  THEN to_char(c.observed_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"') END AS observed_at,
 coalesce(r.present,false) AS receipt_present,r.matching AS receipt_matching,
 coalesce(p.present,false) AS claim_present,p.matching AS claim_matching,
 CASE WHEN p.matching AND p.expires_at BETWEEN '0001-01-01T00:00:00Z'::timestamptz AND '9999-12-31T23:59:59.999999Z'::timestamptz
  THEN to_char(p.expires_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"') END AS expires_at
 FROM clock c LEFT JOIN receipt r ON true LEFT JOIN claim p ON true"""


def _instant(raw: Any) -> tuple[str, int]:
    if (
        type(raw) is not str
        or re.fullmatch(r"[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}\.[0-9]{6}Z", raw) is None
        or raw.startswith("0000-")
    ):
        raise _store_error()
    at = timestamp(raw)
    if at is None:
        raise _store_error()
    return raw, at


def _evidence(present: Any, matching: Any) -> _Receipt:
    if type(present) is not bool or (type(matching) is not bool if present else matching is not None):
        raise _store_error()
    return ("matching" if matching else "conflict") if present else "absent"


def _observation(rows: Any) -> EventConsumerProcessingInspection:
    if not isinstance(rows, (tuple, list)) or len(rows) != 1:
        raise _store_error()
    row = rows[0]
    if not isinstance(row, (tuple, list)) or len(row) != 6:
        raise _store_error()
    observed_at, observed = _instant(row[0])
    receipt = _evidence(row[1], row[2])
    claim_evidence = _evidence(row[3], row[4])
    expires_at: str | None = None
    remaining_ms: int | None = None
    claim: _Claim
    if claim_evidence == "matching":
        expires_at, expires = _instant(row[5])
        remaining = expires - observed
        claim = "active" if remaining > 0 else "expired"
        remaining_ms = max(0, remaining // 1000000)
    else:
        if row[5] is not None:
            raise _store_error()
        claim = claim_evidence
    state: _State = (
        "conflict" if receipt == "conflict" or claim == "conflict" else "completed" if receipt == "matching" else claim
    )
    return EventConsumerProcessingInspection(state, observed_at, receipt, claim, expires_at, remaining_ms)


async def ainspect_event_consumer_processing(
    connection: Any,
    input: EventConsumerRequest,
    *,
    stop_event: asyncio.Event | None = None,
) -> EventConsumerProcessingInspection:
    """One metadata SELECT on an exclusive idle autocommit psycopg async connection.

    Authenticate/authorize first; supply exact original bytes through a request
    constructor. No explicit transaction, row/advisory lock, platform call or mutation.
    Cancellation joins active SQL/cleanup; bound driver and statement calls.
    """
    request, identity, digest = _prepare(connection, input, lambda: None)
    params = (
        request.account_id,
        request.app_id,
        request.subscription_id,
        request.acceptance_id,
        request.source,
        request.event_id,
        digest,
        identity,
        digest,
        identity,
    )

    async def read(control: Control) -> EventConsumerProcessingInspection:
        control.check()
        if connection.autocommit is not True or int(connection.info.transaction_status) != 0:
            raise ValueError("processing inspection requires an exclusively leased idle autocommit connection")
        failed = cancelled = False
        try:
            async with connection.cursor(row_factory=_tuple_row) as cursor:
                rows = await _query(cursor, _INSPECTION_QUERY, params, control)
        except (EventAcknowledgmentWorkerCancelled, EventAcknowledgmentWorkerError):
            raise
        except asyncio.CancelledError:
            cancelled = True
        except Exception:
            failed = True
        if failed or cancelled:
            try:
                await connection.close()
            except BaseException:
                pass
        control.check()
        if cancelled:
            raise EventAcknowledgmentWorkerCancelled()
        if failed:
            raise _store_error()
        try:
            result = _observation(rows)
        except Exception:
            failed = True
        # Raise outside the driver/decoder exception context, without rejected rows.
        if failed:
            raise _store_error()
        return result

    return await owned_operation(read, stop_event)
