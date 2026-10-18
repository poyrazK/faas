"""Read-only, bounded application-owned callback recovery health. No capabilities or payloads."""

from __future__ import annotations

import asyncio
import math
from dataclasses import dataclass, field
from typing import Any

from ._event_acknowledgment_protocol import (
    Control,
    EventAcknowledgmentWorkerCancelled,
    EventAcknowledgmentWorkerError,
    owned_operation,
)
from .event_acknowledgment_intents import _instant, _integer, _query, _store_error
from .event_consumer_transactions import _tuple_row, _uuid


@dataclass(frozen=True)
class EventAcknowledgmentIntentHealthOptions:
    scan_limit: int = 1000
    stall_after_ms: int = 300000


@dataclass(frozen=True)
class EventAcknowledgmentIntentHealth:
    """Sampled local backlog only. Always export coverage/completeness with counts and ages."""

    account_id: str
    app_id: str
    observed_at: str
    complete: bool
    truncated: bool
    scan_limit: int
    invalid_rows: int
    outstanding: int
    pending: int
    unconfirmed: int
    due: int
    leased: int
    scheduled: int
    expired_claims: int
    stalled: int
    stall_after_seconds: float
    oldest_outstanding_at: str | None
    oldest_outstanding_age_seconds: float
    oldest_eligible_at: str | None
    oldest_overdue_age_seconds: float
    parked: int = 0
    coverage: str = field(default="bounded_retained_unfinished_intents", init=False)


# Keep the statement/classification aligned with the Node/Go intent health query.
# Three positional scan-limit bindings preserve the same bounded sample and extra row.
_HEALTH_QUERY = """WITH clock AS MATERIALIZED (SELECT clock_timestamp() AS observed_at), sample AS MATERIALIZED (
 SELECT state,next_attempt_at,claim_expires_at,created_at,updated_at,invocation_id,replay_generation,attempt
 FROM public.gregale_event_acknowledgment_intents
 WHERE account_id=%s::uuid AND app_id=%s::uuid AND state IN ('pending','unconfirmed','parked')
 ORDER BY next_attempt_at,invocation_id,replay_generation,attempt LIMIT %s::integer+1
 ), classified AS (
 SELECT *,greatest(created_at,next_attempt_at,coalesce(claim_expires_at,next_attempt_at)) AS eligible_at
 FROM sample ORDER BY next_attempt_at,invocation_id,replay_generation,attempt LIMIT %s::integer
 ) SELECT c.observed_at,(SELECT count(*) FROM sample)>%s::integer AS truncated,
 count(s.state)::integer AS outstanding,
 count(*) FILTER(WHERE s.state='pending')::integer AS pending,
 count(*) FILTER(WHERE s.state='unconfirmed')::integer AS unconfirmed,
 count(*) FILTER(WHERE s.state='parked')::integer AS parked,
 count(*) FILTER(WHERE s.state IN ('pending','unconfirmed') AND s.eligible_at<=c.observed_at AND (s.claim_expires_at IS NULL OR s.claim_expires_at<=c.observed_at))::integer AS due,
 count(*) FILTER(WHERE s.state IN ('pending','unconfirmed') AND s.claim_expires_at>c.observed_at)::integer AS leased,
 count(*) FILTER(WHERE s.state IN ('pending','unconfirmed') AND s.eligible_at>c.observed_at AND (s.claim_expires_at IS NULL OR s.claim_expires_at<=c.observed_at))::integer AS scheduled,
 count(*) FILTER(WHERE s.state IN ('pending','unconfirmed') AND s.claim_expires_at<=c.observed_at)::integer AS expired_claims,
 count(*) FILTER(WHERE s.state IN ('pending','unconfirmed') AND s.eligible_at<=c.observed_at-%s::double precision*interval '1 second' AND (s.claim_expires_at IS NULL OR s.claim_expires_at<=c.observed_at))::integer AS stalled,
 count(*) FILTER(WHERE s.created_at>c.observed_at OR s.updated_at>c.observed_at OR s.updated_at<s.created_at OR NOT isfinite(s.created_at) OR NOT isfinite(s.updated_at) OR NOT isfinite(s.next_attempt_at) OR (s.claim_expires_at IS NOT NULL AND NOT isfinite(s.claim_expires_at)))::integer AS invalid_rows,
 min(s.created_at) FILTER(WHERE isfinite(s.created_at))::timestamptz AS oldest_outstanding_at,
 coalesce(greatest(0,extract(epoch FROM c.observed_at-min(s.created_at) FILTER(WHERE isfinite(s.created_at)))),0)::double precision AS oldest_outstanding_age_seconds,
 min(s.eligible_at) FILTER(WHERE s.state IN ('pending','unconfirmed') AND isfinite(s.eligible_at) AND s.eligible_at<=c.observed_at AND (s.claim_expires_at IS NULL OR s.claim_expires_at<=c.observed_at))::timestamptz AS oldest_eligible_at,
 coalesce(greatest(0,extract(epoch FROM c.observed_at-min(s.eligible_at) FILTER(WHERE s.state IN ('pending','unconfirmed') AND isfinite(s.eligible_at) AND s.eligible_at<=c.observed_at AND (s.claim_expires_at IS NULL OR s.claim_expires_at<=c.observed_at)))),0)::double precision AS oldest_overdue_age_seconds
 FROM clock c LEFT JOIN classified s ON true GROUP BY c.observed_at"""


def _options(options: EventAcknowledgmentIntentHealthOptions | None) -> tuple[int, int]:
    if options is None:
        options = EventAcknowledgmentIntentHealthOptions()
    if not isinstance(options, EventAcknowledgmentIntentHealthOptions):
        raise ValueError("invalid acknowledgment intent health options")
    limit, stall = options.scan_limit, options.stall_after_ms
    if type(limit) is not int or not 1 <= limit <= 10000 or type(stall) is not int or not 1000 <= stall <= 86400000:
        raise ValueError("invalid acknowledgment intent health options")
    return limit, stall


def _age(value: Any) -> float:
    if type(value) is not float or not math.isfinite(value) or value < 0:
        raise _store_error()
    return value


def _summary(rows: Any, account: str, app: str, limit: int, stall: int) -> EventAcknowledgmentIntentHealth:
    if not isinstance(rows, (tuple, list)) or len(rows) != 1:
        raise _store_error()
    row = rows[0]
    if not isinstance(row, (tuple, list)) or len(row) != 16 or type(row[1]) is not bool:
        raise _store_error()
    outstanding, pending, unconfirmed, parked, due, leased, scheduled, expired, stalled, invalid = (
        _integer(value, 0, limit) for value in row[2:12]
    )
    if (
        pending + unconfirmed + parked != outstanding
        or due + leased + scheduled + parked != outstanding
        or expired > due + scheduled
        or stalled > due
        or invalid > outstanding
        or row[1]
        and outstanding != limit
    ):
        raise _store_error()
    observed = _instant(row[0])
    oldest = None if row[12] is None else _instant(row[12])
    eligible = None if row[14] is None else _instant(row[14])
    age, overdue = _age(row[13]), _age(row[15])
    if (
        outstanding == 0
        and (oldest is not None or age != 0)
        or due == 0
        and (eligible is not None or overdue != 0)
        or oldest is None
        and age != 0
        or eligible is None
        and overdue != 0
        or eligible is not None
        and row[14] > row[0]
        or invalid == 0
        and (
            outstanding > 0
            and oldest is None
            or due > 0
            and eligible is None
            or oldest is not None
            and row[12] > row[0]
            or overdue > age
        )
    ):
        raise _store_error()
    return EventAcknowledgmentIntentHealth(
        account_id=account,
        app_id=app,
        observed_at=observed,
        complete=not row[1] and invalid == 0,
        truncated=row[1],
        scan_limit=limit,
        invalid_rows=invalid,
        outstanding=outstanding,
        pending=pending,
        unconfirmed=unconfirmed,
        parked=parked,
        due=due,
        leased=leased,
        scheduled=scheduled,
        expired_claims=expired,
        stalled=stalled,
        stall_after_seconds=stall / 1000,
        oldest_outstanding_at=oldest,
        oldest_outstanding_age_seconds=age,
        oldest_eligible_at=eligible,
        oldest_overdue_age_seconds=overdue,
    )


async def _discard(connection: Any) -> None:
    try:
        await connection.close()
    except BaseException:
        pass  # Preserve the original fixed failure/cancellation category through cleanup.


async def aget_event_acknowledgment_intent_health(
    connection: Any,
    account_id: str,
    app_id: str,
    options: EventAcknowledgmentIntentHealthOptions | None = None,
    *,
    stop_event: asyncio.Event | None = None,
) -> EventAcknowledgmentIntentHealth:
    """One metadata SELECT on an exclusively leased idle autocommit psycopg async connection.

    No platform client, explicit transaction, lock, callback, polling or mutation.
    Cancellation joins active SQL/cleanup; configure bounded driver/statement calls.
    """
    account, app = _uuid(account_id), _uuid(app_id)
    limit, stall = _options(options)

    async def read(control: Control) -> EventAcknowledgmentIntentHealth:
        control.check()
        if connection.autocommit is not True or int(connection.info.transaction_status) != 0:
            raise ValueError("acknowledgment health requires an exclusively leased idle autocommit connection")
        failed = cancelled = False
        try:
            async with connection.cursor(row_factory=_tuple_row) as cursor:
                rows = await _query(
                    cursor, _HEALTH_QUERY, (account, app, limit, limit, limit, stall / 1000), control, fetch="all"
                )
        except (EventAcknowledgmentWorkerCancelled, EventAcknowledgmentWorkerError):
            raise
        except asyncio.CancelledError:
            cancelled = True
        except Exception:
            failed = True
        if failed or cancelled:
            await _discard(connection)
            if cancelled:
                raise EventAcknowledgmentWorkerCancelled()
            raise _store_error()
        control.check()
        try:
            summary = _summary(rows, account, app, limit, stall)
        except Exception:
            failed = True
        if failed:
            raise _store_error()
        return summary

    return await owned_operation(read, stop_event)
