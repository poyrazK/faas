"""Explicit version-2 durable mode. Importing this module starts no queue or recovery loop."""

from __future__ import annotations

from collections.abc import Mapping, Sequence
from typing import Any

from consumer import _enqueue_authenticated_delivery, _owned_delivery
from faas_sdk import (
    EventAcknowledgmentIntentError,
    EventAcknowledgmentWorkerCancelled,
    EventAcknowledgmentWorkerResult,
    arecover_next_event_acknowledgment_intent,
)


async def accept_authenticated_durable_delivery(
    client: Any,
    headers: Mapping[str, str | Sequence[str]],
    original_body: bytes,
    scope: Mapping[str, str],
    queue: Any,
) -> dict[str, Any]:
    """Authorize ingress first. New durable records only; never relabel immediate-mode records."""
    return await _enqueue_authenticated_delivery(
        client, headers, original_body, scope, queue, 2, "python-durable"
    )


async def _ack(job: Any, result: Any) -> None:
    if job.stop_event.is_set():
        raise EventAcknowledgmentWorkerCancelled(
            EventAcknowledgmentWorkerResult(True, result.receipt)
        )
    await job.ack()  # Must independently fence ownership and durable deletion.


async def process_owned_durable_delivery(
    client: Any, job: Any, scope: Mapping[str, str], connection: Any, handler: Any
) -> Any:
    """Commit SQL/receipt/intent together, then transfer callback responsibility to recovery."""
    worker, body = _owned_delivery(client, job, scope, version=2, mode="python-durable")
    result = await worker.run_durable(
        connection,
        lambda cursor, cancel: handler(cursor, cancel, body),
        stop_event=job.stop_event,
    )
    await _ack(job, result)
    return result


async def save_owned_completion_intent(
    client: Any, job: Any, scope: Mapping[str, str], connection: Any
) -> Any:
    """Explicit receipt-only recovery on a fresh connection after an uncertain COMMIT."""
    worker, _body = _owned_delivery(
        client, job, scope, version=2, mode="python-durable"
    )
    result = await worker.save_completion_intent(connection, stop_event=job.stop_event)
    await _ack(job, result)
    return result


async def recover_one_saved_callback(
    client: Any, connection: Any, scope: Mapping[str, str], stop_event: Any
) -> Any:
    """One iteration. The host owns authenticated client lifetime, polling and SQL deadlines."""
    try:
        return await arecover_next_event_acknowledgment_intent(
            client,
            connection,
            scope["account_id"],
            scope["app_id"],
            claim_duration_ms=30000,
            backoff_ms=1000,
            stop_event=stop_event,
        )
    except EventAcknowledgmentIntentError as error:
        if error.code != "event_acknowledgment_intent_empty":
            raise
        return None
