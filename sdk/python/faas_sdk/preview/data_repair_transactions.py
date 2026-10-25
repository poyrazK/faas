"""Capture and operation receipts share one owned PostgreSQL transaction."""
from __future__ import annotations

import hashlib
from collections.abc import Awaitable, Callable
from typing import Any

from .data_repair import _params
from .operations import (
    CustomerOperationRequest,
    OperationOutcome,
    OperationRequest,
    OperationTransactionResult,
    _awith_operation_transaction,
    _customer_result,
    _normalize,
    _with_operation_transaction,
    customer_operation_request_digest,
    operation_request_digest,
)

_CAPTURE = "SELECT gregale_repair.capture_transaction(%s::uuid,%s::jsonb,%s,%s,%s,%s::bytea,%s::bytea,%s)"


class DataRepairCaptureError(RuntimeError):
    """A required capture could not be created or validated. SQL is redacted."""

    def __init__(self, error: Exception):
        self.code = {"GR001": "repair_capture_missing", "GR002": "repair_capture_mismatch",
                     "GR003": "repair_capture_incomplete", "22023": "repair_capture_incomplete"}.get(getattr(error, "sqlstate", None), "repair_capture_unavailable")
        super().__init__(self.code + (": check the owner upgrade and writer grants" if self.code == "repair_capture_unavailable" else ""))


def _configuration(input: OperationRequest, table: str, environment: str, release: str):
    request = _normalize(input)
    operation, scope, version, relation = _params(request, table, environment, release)
    if table.strip() != table or "." not in table:
        raise ValueError("invalid repairable operation capture configuration")
    protocol = "customer" if request._receipt_binding else "managed"
    return request, (operation, scope, version, relation, protocol, operation_request_digest(request))


def _phase(cursor: Any, parameters: tuple, mode: str, body: bytes | None = None):
    try:
        cursor.execute(_CAPTURE, (*parameters, hashlib.sha256(body).digest() if body is not None else None, mode))
    except Exception as error:
        raise DataRepairCaptureError(error) from None


async def _aphase(cursor: Any, parameters: tuple, mode: str, body: bytes | None = None):
    try:
        await cursor.execute(_CAPTURE, (*parameters, hashlib.sha256(body).digest() if body is not None else None, mode))
    except Exception as error:
        raise DataRepairCaptureError(error) from None


def _run(connection: Any, input: OperationRequest, handler: Callable[[Any], OperationOutcome], **config) -> OperationTransactionResult:
    request, parameters = _configuration(input, **config)
    def callback(cursor):
        _phase(cursor, parameters, "begin")
        result = handler(cursor)
        try:
            cursor.execute("SELECT gregale_repair.finish_operation(%s::uuid)", (request.operation_id,))
        except Exception as error:
            raise DataRepairCaptureError(error) from None
        return result
    return _with_operation_transaction(connection, request, callback,
        lambda cursor, body, replayed: _phase(cursor, parameters, "replay" if replayed else "seal", body))


async def _arun(connection: Any, input: OperationRequest, handler: Callable[[Any], Awaitable[OperationOutcome]], **config) -> OperationTransactionResult:
    request, parameters = _configuration(input, **config)
    async def callback(cursor):
        await _aphase(cursor, parameters, "begin")
        result = await handler(cursor)
        try:
            await cursor.execute("SELECT gregale_repair.finish_operation(%s::uuid)", (request.operation_id,))
        except Exception as error:
            raise DataRepairCaptureError(error) from None
        return result
    async def guard(cursor, body, replayed):
        await _aphase(cursor, parameters, "replay" if replayed else "seal", body)
    return await _awith_operation_transaction(connection, request, callback, guard)


def with_repairable_operation_transaction(connection: Any, input: OperationRequest, handler: Callable[[Any], OperationOutcome], *, table: str, environment: str, release: str) -> OperationTransactionResult:
    """Own capture, writes and receipt atomically; use only the supplied cursor.
    Replay requires the original capture configuration and skips the handler."""
    if input._receipt_binding:
        raise ValueError("use the repairable customer transaction helper for customer receipts")
    return _run(connection, input, handler, table=table, environment=environment, release=release)


async def awith_repairable_operation_transaction(connection: Any, input: OperationRequest, handler: Callable[[Any], Awaitable[OperationOutcome]], *, table: str, environment: str, release: str) -> OperationTransactionResult:
    """Async equivalent, requiring an idle/autocommit connection."""
    if input._receipt_binding:
        raise ValueError("use the repairable customer transaction helper for customer receipts")
    return await _arun(connection, input, handler, table=table, environment=environment, release=release)


def with_repairable_customer_operation_transaction(connection: Any, input: CustomerOperationRequest, handler: Callable[[Any], Any], *, table: str, environment: str, release: str) -> OperationTransactionResult:
    """Result-only Customer Operations equivalent using its receipt context."""
    customer_operation_request_digest(input)
    result = _run(connection, input._request, lambda cursor: {"result": handler(cursor)}, table=table, environment=environment, release=release)
    return OperationTransactionResult(_customer_result(result.body), result.replayed)


async def awith_repairable_customer_operation_transaction(connection: Any, input: CustomerOperationRequest, handler: Callable[[Any], Awaitable[Any]], *, table: str, environment: str, release: str) -> OperationTransactionResult:
    """Async result-only Customer Operations equivalent."""
    customer_operation_request_digest(input)
    async def callback(cursor):
        return {"result": await handler(cursor)}
    result = await _arun(connection, input._request, callback, table=table, environment=environment, release=release)
    return OperationTransactionResult(_customer_result(result.body), result.replayed)
