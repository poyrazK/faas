"""Application-owned protected queue integration; nothing starts on import."""

from __future__ import annotations

import asyncio
import base64
from collections.abc import Mapping, Sequence
from typing import Any
from uuid import uuid4

from faas_sdk.preview import AsyncEventAcknowledgmentWorker, EventAcknowledgmentWorkerCancelled

_HEADERS = frozenset(
    (
        "x-gregale-event-source",
        "x-gregale-event-id",
        "x-gregale-event-subscription-id",
        "x-gregale-event-acceptance-id",
        "x-gregale-event-acknowledgment-token",
        "x-gregale-event-acknowledgment-lease-seconds",
        "x-faas-invocation-id",
        "x-faas-invocation-source",
        "x-faas-invocation-attempt",
        "x-faas-invocation-replay-generation",
        "x-faas-invocation-replayed-from",
        "x-faas-invocation-replay-root",
    )
)


def _owner(scope: Mapping[str, str]) -> dict[str, str]:
    if not isinstance(scope, Mapping) or any(
        not isinstance(scope.get(key), str) for key in ("app", "account_id", "app_id")
    ):
        raise ValueError("trusted delivery owner required")
    return {
        "app": scope["app"],
        "account_id": scope["account_id"].lower(),
        "app_id": scope["app_id"].lower(),
    }


async def accept_authenticated_delivery(
    client: Any,
    headers: Mapping[str, str | Sequence[str]],
    original_body: bytes,
    scope: Mapping[str, str],
    queue: Any,
) -> dict[str, Any]:
    """Call after ingress authorization. enqueue_protected must commit before resolving."""
    return await _enqueue_authenticated_delivery(
        client, headers, original_body, scope, queue, 1, "python-immediate"
    )


async def _enqueue_authenticated_delivery(
    client: Any,
    headers: Mapping[str, str | Sequence[str]],
    original_body: bytes,
    scope: Mapping[str, str],
    queue: Any,
    version: int,
    mode: str,
) -> dict[str, Any]:
    if not isinstance(headers, Mapping) or any(
        not isinstance(key, str) for key in headers
    ):
        raise ValueError("original delivery headers required")
    saved_headers = {
        key: list(value)
        if isinstance(value, Sequence) and not isinstance(value, str)
        else value
        for key, value in headers.items()
        if key.lower() in _HEADERS
    }
    owner = _owner(scope)
    AsyncEventAcknowledgmentWorker.from_headers(
        client,
        owner["app"],
        saved_headers,
        original_body,
        owner["account_id"],
        owner["app_id"],
    )
    await queue.enqueue_protected(
        {
            "id": str(uuid4()),
            "version": version,
            "mode": mode,
            "owner": owner,
            "headers": saved_headers,
            "body_base64": base64.b64encode(original_body).decode("ascii"),
        }
    )
    return {"status_code": 202, "body": b'{"accepted":true}'}


def _owned_delivery(
    client: Any,
    job: Any,
    scope: Mapping[str, str],
    *,
    version: int = 1,
    mode: str = "python-immediate",
) -> tuple[AsyncEventAcknowledgmentWorker, bytes]:
    if not isinstance(job.stop_event, asyncio.Event):
        raise TypeError("owned job cancellation required")
    if job.stop_event.is_set():
        raise EventAcknowledgmentWorkerCancelled()
    owner, record = _owner(scope), job.record
    if (
        not isinstance(record, Mapping)
        or type(record.get("version")) is not int
        or record["version"] != version
        or record.get("mode") != mode
        or record.get("owner") != owner
        or not isinstance(record.get("body_base64"), str)
        or len(record["body_base64"]) > 1398104
    ):
        raise ValueError("invalid protected Python delivery job")
    try:
        body = base64.b64decode(record["body_base64"], validate=True)
    except (ValueError, UnicodeError):
        raise ValueError("invalid protected original body encoding") from None
    if base64.b64encode(body).decode("ascii") != record["body_base64"]:
        raise ValueError("invalid protected original body encoding")
    worker = AsyncEventAcknowledgmentWorker.from_headers(
        client,
        owner["app"],
        record.get("headers"),
        body,
        owner["account_id"],
        owner["app_id"],
    )
    return worker, body


async def process_owned_delivery(
    client: Any, job: Any, scope: Mapping[str, str], connection: Any, handler: Any
) -> Any:
    """Exclusive idle autocommit connection; handler(cursor, cancellation, original_bytes) uses SQL only."""
    worker, body = _owned_delivery(client, job, scope)
    result = await worker.run(
        connection,
        lambda cursor, cancel: handler(cursor, cancel, body),
        stop_event=job.stop_event,
    )
    if job.stop_event.is_set():
        raise EventAcknowledgmentWorkerCancelled(result)
    await job.ack()  # The adapter independently fences ownership and durable deletion.
    return result


async def complete_owned_receipt(
    client: Any, job: Any, scope: Mapping[str, str], connection: Any
) -> Any:
    """Explicit receipt-only reconciliation: never invokes the business callback."""
    worker, _body = _owned_delivery(client, job, scope)
    result = await worker.complete_receipt(connection, stop_event=job.stop_event)
    if job.stop_event.is_set():
        raise EventAcknowledgmentWorkerCancelled(result)
    await job.ack()
    return result
