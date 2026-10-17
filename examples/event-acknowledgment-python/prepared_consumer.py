"""Version-5 prepared jobs; no worker, renewal or queue loop starts on import."""

from __future__ import annotations

from collections.abc import Mapping, Sequence
from typing import Any

from consumer import _enqueue_authenticated_delivery, _owned_delivery
from durable_consumer import _ack
from faas_sdk import (
    EventAcknowledgmentPreparedOptions,
    ainspect_event_consumer_processing,
    event_consumer_request_from_headers,
)


async def accept_authenticated_prepared_delivery(
    client: Any,
    headers: Mapping[str, str | Sequence[str]],
    original_body: bytes,
    scope: Mapping[str, str],
    queue: Any,
) -> dict[str, Any]:
    """Separate authenticated ingress for new jobs; never relabel earlier modes."""
    return await _enqueue_authenticated_delivery(
        client, headers, original_body, scope, queue, 5, "python-prepared-durable"
    )


def _prepared_delivery(client: Any, job: Any, scope: Mapping[str, str]) -> Any:
    if isinstance(job.record, Mapping) and "failure_decision" in job.record:
        raise ValueError("failure decisions require the failure-intent processor")
    return _owned_delivery(
        client, job, scope, version=5, mode="python-prepared-durable"
    )


async def process_owned_prepared_delivery(
    client: Any,
    job: Any,
    scope: Mapping[str, str],
    connection_factory: Any,
    prepare: Any,
    handler: Any,
    options: EventAcknowledgmentPreparedOptions,
) -> Any:
    """Prepare without effects; final SQL/receipt/claim removal/intent commit atomically."""
    worker, body = _prepared_delivery(client, job, scope)
    if not callable(prepare) or not callable(handler):
        raise TypeError("invalid preparation callbacks")
    result = await worker.run_prepared_durable(
        connection_factory,
        lambda cancel: prepare(body, cancel),
        lambda cursor, cancel, prepared: handler(cursor, cancel, body, prepared),
        options,
        stop_event=job.stop_event,
    )
    await _ack(job, result)
    return result


async def save_owned_prepared_completion_intent(
    client: Any, job: Any, scope: Mapping[str, str], connection: Any
) -> Any:
    """Explicit receipt-only recovery on a fresh connection; never prepare or claim."""
    worker, _body = _prepared_delivery(client, job, scope)
    result = await worker.save_completion_intent(connection, stop_event=job.stop_event)
    await _ack(job, result)
    return result


async def inspect_owned_prepared_delivery(
    client: Any, job: Any, scope: Mapping[str, str], connection: Any
) -> Any:
    """Read metadata only; neither claims, prepares, reconciles callbacks nor acks the job."""
    _worker, body = _prepared_delivery(client, job, scope)
    request = event_consumer_request_from_headers(
        job.record["headers"], body, scope["account_id"], scope["app_id"]
    )
    return await ainspect_event_consumer_processing(
        connection, request, stop_event=job.stop_event
    )
