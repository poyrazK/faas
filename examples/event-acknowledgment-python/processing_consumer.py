"""Explicit version-4 leased jobs. The host owns preparation and renewal; no loop starts."""

from __future__ import annotations

from collections.abc import Mapping, Sequence
from typing import Any

from consumer import _enqueue_authenticated_delivery, _owned_delivery
from durable_consumer import _ack
from faas_sdk.preview import (
    aclaim_event_consumer_processing,
    ainspect_event_consumer_processing,
    event_consumer_request_from_headers,
)


async def accept_authenticated_processing_delivery(
    client: Any,
    headers: Mapping[str, str | Sequence[str]],
    original_body: bytes,
    scope: Mapping[str, str],
    queue: Any,
) -> dict[str, Any]:
    """Separate ingress for new leased jobs. Never relabel existing protected records."""
    return await _enqueue_authenticated_delivery(
        client, headers, original_body, scope, queue, 4, "python-durable-processing"
    )


def _processing_delivery(client: Any, job: Any, scope: Mapping[str, str]) -> Any:
    if isinstance(job.record, Mapping) and "failure_decision" in job.record:
        raise ValueError("failure decisions require the failure-intent processor")
    return _owned_delivery(
        client, job, scope, version=4, mode="python-durable-processing"
    )


async def claim_owned_processing_delivery(
    client: Any, job: Any, scope: Mapping[str, str], connection: Any, duration_ms: int
) -> Any:
    """Reclaim the original consumer after restart, never a serialized capability."""
    _worker, body = _processing_delivery(client, job, scope)
    request = event_consumer_request_from_headers(
        job.record["headers"], body, scope["account_id"], scope["app_id"]
    )
    return await aclaim_event_consumer_processing(
        connection, request, duration_ms, stop_event=job.stop_event
    )


async def complete_owned_processing_delivery(
    client: Any,
    job: Any,
    scope: Mapping[str, str],
    connection: Any,
    lease: Any,
    handler: Any,
) -> Any:
    """Stop/join local renewal first; final handler uses short transactional SQL only."""
    worker, body = _processing_delivery(client, job, scope)
    if not callable(handler):
        raise TypeError("invalid event consumer handler")
    result = await worker.run_durable_with_processing_lease(
        connection,
        lease,
        lambda cursor, cancel: handler(cursor, cancel, body),
        stop_event=job.stop_event,
    )
    await _ack(job, result)
    return result


async def save_owned_processing_completion_intent(
    client: Any, job: Any, scope: Mapping[str, str], connection: Any
) -> Any:
    """Explicit receipt-only reconciliation on a fresh connection after uncertain COMMIT."""
    worker, _body = _processing_delivery(client, job, scope)
    result = await worker.save_completion_intent(connection, stop_event=job.stop_event)
    await _ack(job, result)
    return result


async def inspect_owned_processing_delivery(
    client: Any, job: Any, scope: Mapping[str, str], connection: Any
) -> Any:
    """Read metadata only; neither claims, prepares, reconciles callbacks nor acks the job."""
    _worker, body = _processing_delivery(client, job, scope)
    request = event_consumer_request_from_headers(
        job.record["headers"], body, scope["account_id"], scope["app_id"]
    )
    return await ainspect_event_consumer_processing(
        connection, request, stop_event=job.stop_event
    )
