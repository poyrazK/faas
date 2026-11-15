"""Batched trigger delivery helpers (ADR-1321).

A trigger with ``delivery: batch`` invokes the function once with every
claimed record, with header ``X-Gregale-Batch-Size`` and body::

    {"records": [{"item_identifier": "…", "data": {…}, "headers": {…}, "metadata": {…}},
                 {"item_identifier": "…", "data_base64": "…"}]}

``data`` carries a payload that was valid JSON and ``data_base64`` any other
payload. The function answers 2xx to complete every record, or
``{"batchItemFailures": [{"itemIdentifier": "…"}]}`` to retry some of them.
``handle_trigger_batch`` runs a handler for each record and builds that answer::

    from faas_sdk.preview.trigger_batch import handle_trigger_batch

    def handler(request):
        return handle_trigger_batch(request.body, lambda record: save(record.data))

A malformed body raises ``TriggerBatchError``; let it fail the request so the
whole batch is retried.
"""

from __future__ import annotations

import asyncio
import base64
import binascii
import inspect
import json
from collections.abc import Awaitable, Callable, Mapping
from dataclasses import dataclass, field
from types import MappingProxyType
from typing import Any

#: Header present on every batch delivery; its value is the record count.
TRIGGER_BATCH_SIZE_HEADER = "X-Gregale-Batch-Size"

_MAX_CONCURRENCY = 100


class TriggerBatchError(ValueError):
    """The body is not a batch delivery envelope."""


@dataclass(frozen=True)
class TriggerBatchRecord:
    """One delivered record."""

    item_identifier: str
    #: True when the payload was JSON; ``data`` then holds it, possibly None.
    is_json: bool
    #: The payload parsed as JSON, or None when it was not JSON.
    data: Any
    #: The payload bytes. A JSON payload arrives parsed, so these are its
    #: re-encoding and may differ in spacing from what was published.
    payload: bytes
    headers: Mapping[str, str] = field(default_factory=dict)
    #: Source details, such as a realtime channel and sequence or a Kafka offset.
    metadata: Mapping[str, Any] = field(default_factory=dict)

    def text(self) -> str:
        """The payload as UTF-8 text."""
        return self.payload.decode("utf-8")


def is_trigger_batch(headers: Mapping[str, str]) -> bool:
    """Report whether a request is a batch delivery."""
    wanted = TRIGGER_BATCH_SIZE_HEADER.lower()
    return any(name.lower() == wanted for name in headers)


def parse_trigger_batch(body: bytes | str | Mapping[str, Any]) -> list[TriggerBatchRecord]:
    """Validate a batch delivery body and return its records in delivery order.

    The body may be the parsed JSON object, its text, or its bytes.
    """
    envelope = _decode_body(body)
    records = envelope.get("records") if isinstance(envelope, Mapping) else None
    if not isinstance(records, list):
        raise TriggerBatchError("trigger batch body must be an object with a records list")
    out: list[TriggerBatchRecord] = []
    seen: set[str] = set()
    for index, raw in enumerate(records):
        record = _parse_record(raw, index)
        if record.item_identifier in seen:
            raise TriggerBatchError(f"trigger batch record {index} repeats item_identifier {record.item_identifier}")
        seen.add(record.item_identifier)
        out.append(record)
    return out


def handle_trigger_batch(
    body: bytes | str | Mapping[str, Any],
    handler: Callable[[TriggerBatchRecord], Any],
    *,
    stop_on_failure: bool = False,
    on_error: Callable[[BaseException, TriggerBatchRecord], None] | None = None,
) -> dict[str, list[dict[str, str]]]:
    """Run ``handler`` for every record, in delivery order, and return the response body.

    A record whose handler raises is listed in ``batchItemFailures`` and
    retried; the others are complete. With ``stop_on_failure`` the remaining
    records are reported for retry without running, so they are retried in
    order; each retry then counts as an attempt for them too.
    """
    records = parse_trigger_batch(body)
    failed: list[TriggerBatchRecord] = []
    for index, record in enumerate(records):
        try:
            handler(record)
        except Exception as error:  # noqa: BLE001 - any handler error retries the record
            _report(on_error, error, record)
            if stop_on_failure:
                failed.extend(records[index:])
                break
            failed.append(record)
    return _response(failed)


async def handle_trigger_batch_async(
    body: bytes | str | Mapping[str, Any],
    handler: Callable[[TriggerBatchRecord], Awaitable[Any] | Any],
    *,
    concurrency: int = 1,
    stop_on_failure: bool = False,
    on_error: Callable[[BaseException, TriggerBatchRecord], None] | None = None,
) -> dict[str, list[dict[str, str]]]:
    """Async form of :func:`handle_trigger_batch`.

    ``concurrency`` (1-100, default 1) handles that many records at once; use
    more only when records do not depend on each other's order.
    ``stop_on_failure`` needs concurrency 1.
    """
    if not isinstance(concurrency, int) or isinstance(concurrency, bool) or not 1 <= concurrency <= _MAX_CONCURRENCY:
        raise TypeError(f"concurrency must be an integer from 1 to {_MAX_CONCURRENCY}")
    if stop_on_failure and concurrency != 1:
        raise TypeError("stop_on_failure needs concurrency 1")
    records = parse_trigger_batch(body)
    failed = [False] * len(records)

    async def run(index: int) -> bool:
        record = records[index]
        try:
            result = handler(record)
            if inspect.isawaitable(result):
                await result
            return True
        except Exception as error:  # noqa: BLE001 - any handler error retries the record
            _report(on_error, error, record)
            failed[index] = True
            return False

    if concurrency == 1:
        for index in range(len(records)):
            if not await run(index) and stop_on_failure:
                for later in range(index, len(records)):
                    failed[later] = True
                break
    else:
        pending = iter(range(len(records)))

        async def worker() -> None:
            for index in pending:
                await run(index)

        await asyncio.gather(*(worker() for _ in range(min(concurrency, len(records)))))
    return _response([record for index, record in enumerate(records) if failed[index]])


def _decode_body(body: bytes | str | Mapping[str, Any]) -> Any:
    if isinstance(body, Mapping):
        return body
    try:
        return json.loads(body)
    except (TypeError, ValueError) as error:
        raise TriggerBatchError("trigger batch body is not JSON") from error


def _parse_record(raw: Any, index: int) -> TriggerBatchRecord:
    if not isinstance(raw, Mapping) or not isinstance(raw.get("item_identifier"), str) or not raw["item_identifier"]:
        raise TriggerBatchError(f"trigger batch record {index} needs an item_identifier")
    has_data, has_base64 = "data" in raw, "data_base64" in raw
    if has_data and has_base64:
        raise TriggerBatchError(f"trigger batch record {index} has both data and data_base64")
    if has_data:
        payload = json.dumps(raw["data"], separators=(",", ":"), ensure_ascii=False).encode("utf-8")
    elif has_base64:
        if not isinstance(raw["data_base64"], str):
            raise TriggerBatchError(f"trigger batch record {index} data_base64 must be a string")
        try:
            payload = base64.b64decode(raw["data_base64"], validate=True)
        except (binascii.Error, ValueError) as error:
            raise TriggerBatchError(f"trigger batch record {index} has invalid data_base64") from error
    else:
        payload = b""
    headers = raw.get("headers")
    metadata = raw.get("metadata")
    return TriggerBatchRecord(
        item_identifier=raw["item_identifier"],
        is_json=has_data,
        data=raw["data"] if has_data else None,
        payload=payload,
        headers=MappingProxyType(
            {k: v for k, v in headers.items() if isinstance(v, str)} if isinstance(headers, Mapping) else {}
        ),
        metadata=MappingProxyType(dict(metadata) if isinstance(metadata, Mapping) else {}),
    )


def _report(
    on_error: Callable[[BaseException, TriggerBatchRecord], None] | None,
    error: BaseException,
    record: TriggerBatchRecord,
) -> None:
    if on_error is None:
        return
    try:
        on_error(error, record)
    except Exception:  # noqa: BLE001,S110 - reporting must not change the outcome
        pass


def _response(failed: list[TriggerBatchRecord]) -> dict[str, list[dict[str, str]]]:
    return {"batchItemFailures": [{"itemIdentifier": record.item_identifier} for record in failed]}
