"""Version-2 durable work and version-3 failure decisions. No queue/recovery loop starts on import."""

from __future__ import annotations

import copy
import re
from collections.abc import Mapping, Sequence
from typing import Any

from consumer import _enqueue_authenticated_delivery, _owned_delivery
from faas_sdk.preview import (
    EventAcknowledgmentIntentCheckpointWatchOptions,
    EventAcknowledgmentIntentError,
    EventAcknowledgmentIntentHealthOptions,
    EventAcknowledgmentIntentRecoveryPassOptions,
    EventAcknowledgmentIntentRetryPolicy,
    EventAcknowledgmentIntentWatchOptions,
    EventAcknowledgmentWorkerCancelled,
    EventAcknowledgmentWorkerResult,
    aget_event_acknowledgment_intent_health,
    arecover_event_acknowledgment_intents,
    arecover_next_event_acknowledgment_intent,
    awatch_event_acknowledgment_intent_health,
    awatch_event_acknowledgment_intent_health_with_checkpoint,
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


async def _ack(job: Any, result: Any = None) -> None:
    if job.stop_event.is_set():
        raise EventAcknowledgmentWorkerCancelled(
            EventAcknowledgmentWorkerResult()
            if result is None
            else EventAcknowledgmentWorkerResult(True, result.receipt)
        )
    await job.ack()  # Must independently fence ownership and durable deletion.


async def process_owned_durable_delivery(
    client: Any, job: Any, scope: Mapping[str, str], connection: Any, handler: Any
) -> Any:
    """Commit SQL/receipt/intent together, then transfer callback responsibility to recovery."""
    worker, body = _processing_delivery(client, job, scope)
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
    worker, _body = _processing_delivery(client, job, scope)
    result = await worker.save_completion_intent(connection, stop_event=job.stop_event)
    await _ack(job, result)
    return result


def _processing_delivery(client: Any, job: Any, scope: Mapping[str, str]) -> Any:
    if isinstance(job.record, Mapping) and "failure_decision" in job.record:
        raise ValueError("failure decisions require the failure-intent processor")
    return _owned_delivery(client, job, scope, version=2, mode="python-durable")


def _failure_decision(action: Any, reason: Any) -> dict[str, str]:
    if (
        action not in ("retry", "dead-letter")
        or not isinstance(reason, str)
        or re.fullmatch(r"[a-z0-9_.-]{1,128}", reason) is None
    ):
        raise ValueError("invalid protected failure decision")
    return {"action": action, "reason": reason}


def _saved_failure(record: Mapping[str, Any]) -> dict[str, str]:
    decision = record.get("failure_decision")
    if not isinstance(decision, Mapping) or set(decision) != {"action", "reason"}:
        raise ValueError("missing protected failure decision")
    return _failure_decision(decision["action"], decision["reason"])


async def persist_owned_failure_decision(
    client: Any, job: Any, scope: Mapping[str, str], action: str, reason: str
) -> None:
    """Only after known rollback/stopped work. Fenced adapter must commit and refresh job.record."""
    decision = _failure_decision(action, reason)
    if (
        isinstance(job.record, Mapping)
        and type(job.record.get("version")) is int
        and job.record["version"] == 3
    ):
        _owned_delivery(client, job, scope, version=3, mode="python-durable-failure")
        if _saved_failure(job.record) != decision:
            raise ValueError("protected failure decision is immutable")
        return
    _owned_delivery(client, job, scope, version=2, mode="python-durable")
    if "failure_decision" in job.record:
        raise ValueError("invalid protected processing record")
    replacement = copy.deepcopy(dict(job.record))
    replacement.update(
        version=3, mode="python-durable-failure", failure_decision=decision
    )
    await job.save_failure_decision(replacement)
    # A stale view could admit business work again. Never proceed with that handle.
    if job.record != replacement:
        raise ValueError("reload the owned protected job after decision persistence")


async def save_owned_failure_intent(
    client: Any, job: Any, scope: Mapping[str, str], connection: Any
) -> Any:
    """Recover only the durable decision, never business processing or a newly inferred action."""
    worker, _body = _owned_delivery(
        client, job, scope, version=3, mode="python-durable-failure"
    )
    decision = _saved_failure(job.record)
    save = (
        worker.save_retry_intent
        if decision["action"] == "retry"
        else worker.save_dead_letter_intent
    )
    status = await save(connection, decision["reason"], stop_event=job.stop_event)
    await _ack(job)  # Local negative-intent COMMIT transfers callback responsibility.
    return status


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


async def read_acknowledgment_recovery_health(
    connection: Any, scope: Mapping[str, str], stop_event: Any
) -> Any:
    """One metadata observation; use a separately leased connection from active recovery/business work."""
    return await aget_event_acknowledgment_intent_health(
        connection,
        scope["account_id"],
        scope["app_id"],
        EventAcknowledgmentIntentHealthOptions(scan_limit=1000, stall_after_ms=300000),
        stop_event=stop_event,
    )


async def recover_saved_callbacks_pass(
    client: Any, connection_factory: Any, scope: Mapping[str, str], stop_event: Any
) -> Any:
    """One finite host-scheduled pass; no queue/job mutation or business work."""
    return await arecover_event_acknowledgment_intents(
        client,
        connection_factory,
        scope["account_id"],
        scope["app_id"],
        EventAcknowledgmentIntentRecoveryPassOptions(
            max_attempts=100,
            concurrency=4,
            retry_policy=EventAcknowledgmentIntentRetryPolicy(
                initial_backoff_ms=1000, max_backoff_ms=300000
            ),
        ),
        stop_event=stop_event,
    )


async def watch_acknowledgment_recovery(
    connection_factory: Any, scope: Mapping[str, str], stop_event: Any, notify: Any
) -> Any:
    """Explicit monitoring-host lifetime. Factory/callback must provide bounded cooperative cleanup."""
    return await awatch_event_acknowledgment_intent_health(
        connection_factory,
        scope["account_id"],
        scope["app_id"],
        EventAcknowledgmentIntentWatchOptions(
            health=EventAcknowledgmentIntentHealthOptions(
                scan_limit=1000, stall_after_ms=300000
            ),
            due_threshold=100,
            stalled_threshold=1,
            parked_threshold=1,
            sustain_ms=30000,
        ),
        notify,
        stop_event=stop_event,
    )


async def watch_acknowledgment_recovery_with_checkpoint(
    connection_factory: Any,
    scope: Mapping[str, str],
    stop_event: Any,
    store: Any,
    notify: Any,
) -> Any:
    """One fenced writer; callbacks below require the durable/idempotent adapter contract in README."""
    saved = await store.load_owned(stop_event)

    async def poll(checkpoint: Any, cancel: Any) -> Any:
        return await store.list_acknowledgments(checkpoint, cancel)

    async def progress(snapshot: Any, cancel: Any) -> None:
        # Commit the complete checkpoint BEFORE every external notification attempt.
        await store.save_owned(snapshot["checkpoint"], cancel)
        for notification in snapshot["checkpoint"]["pending"]:
            cancel.check()
            await notify(
                notification, cancel
            )  # Deduplicate by owner/session incident ID + sequence.
            cancel.check()
            await store.record_acknowledgment(
                {
                    "incident_id": notification["incident_id"],
                    "sequence": notification["sequence"],
                },
                cancel,
            )

    return await awatch_event_acknowledgment_intent_health_with_checkpoint(
        connection_factory,
        scope["account_id"],
        scope["app_id"],
        EventAcknowledgmentIntentCheckpointWatchOptions(
            health=EventAcknowledgmentIntentHealthOptions(
                scan_limit=1000, stall_after_ms=300000
            ),
            due_threshold=100,
            stalled_threshold=1,
            parked_threshold=1,
            sustain_ms=30000,
            resume=saved,
            on_progress=progress,
            poll_acknowledgments=poll,
        ),
        stop_event=stop_event,
    )
